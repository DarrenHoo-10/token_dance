package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/protocol"
	"github.com/tokendance/token-collector/server/internal/store"
)

// Service handles batch ingestion with idempotency and deduplication.
type Service struct {
	Batches  store.BatchStore
	Events   store.EventStore
	Installs store.InstallationStore
	Users    store.UserStore
}

// Result is the outcome of processing an upload batch.
type Result struct {
	BatchID    string
	Accepted   uint32
	Duplicates uint32
	Rejected   []RejectedEvent
	ServerTime time.Time
}

type RejectedEvent struct {
	EventID   string
	ErrorCode string
	Retryable bool
}

// ProcessBatch ingests a batch of events with batch-level and event-level
// idempotency. If the batch has already been processed, it returns the
// previous result.
func (s *Service) ProcessBatch(ctx context.Context, installationID string, batch protocol.UploadBatch) (*Result, error) {
	// Verify installation ownership
	inst, err := s.Installs.GetInstallation(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("installation lookup: %w", err)
	}
	if inst.InstallationStatus != "active" {
		return nil, fmt.Errorf("installation %s is %s", installationID, inst.InstallationStatus)
	}
	if batch.InstallationID != installationID {
		return nil, fmt.Errorf("batch installation_id mismatch")
	}

	// Batch idempotency: check if batch already exists
	existing, err := s.Batches.GetBatch(ctx, batch.BatchID)
	if err == nil && existing != nil {
		return s.buildResultFromExisting(ctx, existing)
	}

	now := time.Now().UTC()
	bodyHash := sha256.Sum256([]byte(batch.BatchID))

	ib := &domain.IngestBatch{
		BatchID:        batch.BatchID,
		InstallationID: installationID,
		RequestSHA256:  bodyHash,
		EventCount:     uint32(len(batch.Events)),
		BatchStatus:    "received",
		ReceivedAt:     now,
	}
	if err := s.Batches.CreateBatch(ctx, ib); err != nil {
		// Race: another goroutine created it
		existing, err2 := s.Batches.GetBatch(ctx, batch.BatchID)
		if err2 == nil && existing != nil {
			return s.buildResultFromExisting(ctx, existing)
		}
		return nil, fmt.Errorf("create batch: %w", err)
	}

	var accepted, duplicates uint32
	var rejected []RejectedEvent

	for _, env := range batch.Events {
		rej := validateEnvelope(env)
		if rej != nil {
			rejected = append(rejected, *rej)
			continue
		}

		evt, err := envelopeToEvent(env, batch.BatchID, installationID, inst.UserID, now)
		if err != nil {
			rejected = append(rejected, RejectedEvent{
				EventID:   string(env.EventID),
				ErrorCode: "CONVERSION_ERROR",
				Retryable: false,
			})
			continue
		}

		inserted, err := s.Events.InsertEvent(ctx, evt)
		if err != nil {
			rejected = append(rejected, RejectedEvent{
				EventID:   string(env.EventID),
				ErrorCode: "STORE_ERROR",
				Retryable: true,
			})
			continue
		}
		if inserted {
			accepted++
		} else {
			duplicates++
		}
	}

	ib.AcceptedCount = accepted
	ib.DuplicateCount = duplicates
	ib.RejectedCount = uint32(len(rejected))
	if len(rejected) == 0 {
		ib.BatchStatus = "committed"
	} else if accepted > 0 {
		ib.BatchStatus = "partial"
	} else {
		ib.BatchStatus = "rejected"
	}
	committedAt := time.Now().UTC()
	ib.CommittedAt = &committedAt
	_ = s.Batches.UpdateBatch(ctx, ib)

	// Touch last_seen
	_ = s.Installs.UpdateLastSeen(ctx, installationID, now)

	return &Result{
		BatchID:    batch.BatchID,
		Accepted:   accepted,
		Duplicates: duplicates,
		Rejected:   rejected,
		ServerTime: time.Now().UTC(),
	}, nil
}

func (s *Service) buildResultFromExisting(ctx context.Context, b *domain.IngestBatch) (*Result, error) {
	return &Result{
		BatchID:    b.BatchID,
		Accepted:   b.AcceptedCount,
		Duplicates: b.DuplicateCount,
		ServerTime: time.Now().UTC(),
	}, nil
}

func validateEnvelope(env protocol.EventEnvelope) *RejectedEvent {
	if env.EventID == "" {
		return &RejectedEvent{EventID: "", ErrorCode: "MISSING_EVENT_ID", Retryable: false}
	}
	if env.InstallationID == "" {
		return &RejectedEvent{EventID: string(env.EventID), ErrorCode: "MISSING_INSTALLATION_ID", Retryable: false}
	}
	return nil
}

func envelopeToEvent(env protocol.EventEnvelope, batchID, installationID, userID string, receivedAt time.Time) (*domain.UsageEvent, error) {
	occurredAt, err := time.Parse(time.RFC3339Nano, env.OccurredAt)
	if err != nil {
		return nil, fmt.Errorf("invalid occurredAt: %w", err)
	}

	eventIDBytes, err := decodeBase64URL32(string(env.EventID))
	if err != nil {
		return nil, fmt.Errorf("invalid event_id: %w", err)
	}
	sourceCursor, err := decodeHMAC(string(env.Source.CursorHMAC))
	if err != nil {
		return nil, fmt.Errorf("invalid source cursor HMAC: %w", err)
	}
	rawFingerprint, err := decodeHMAC(string(env.Source.RawFingerprintHMAC))
	if err != nil {
		return nil, fmt.Errorf("invalid raw fingerprint HMAC: %w", err)
	}

	evt := &domain.UsageEvent{
		EventID:            eventIDBytes,
		SchemaVersion:      env.SchemaVersion,
		BatchID:            batchID,
		InstallationID:     installationID,
		UserID:             userID,
		AdapterID:          env.AdapterID,
		AdapterVersion:     env.AdapterVersion,
		AgentID:            env.AgentID,
		AgentVersion:       env.AgentVersion,
		Accuracy:           string(env.Accuracy),
		SourceKind:         string(env.Source.Kind),
		SourceCursorHMAC:   sourceCursor,
		RawFingerprintHMAC: rawFingerprint,
		OccurredAt:         occurredAt,
		OccurredDate:       occurredAt.Truncate(24 * time.Hour),
		ReceivedAt:         receivedAt,
	}

	if env.SessionHash != nil {
		value, err := decodeHMAC(string(*env.SessionHash))
		if err != nil {
			return nil, err
		}
		evt.SessionHash = &value
	}
	if env.TurnHash != nil {
		value, err := decodeHMAC(string(*env.TurnHash))
		if err != nil {
			return nil, err
		}
		evt.TurnHash = &value
	}
	if env.ToolCallHash != nil {
		value, err := decodeHMAC(string(*env.ToolCallHash))
		if err != nil {
			return nil, err
		}
		evt.ToolCallHash = &value
	}

	switch payload := env.Payload.Value.(type) {
	case protocol.SessionStartedPayload:
		evt.EventType = string(protocol.EventTypeSessionStarted)
		evt.ModelID = payload.ModelID
	case protocol.SessionEndedPayload:
		evt.EventType = string(protocol.EventTypeSessionEnded)
		evt.DurationMs, err = parseOptionalUInt64(payload.DurationMs)
	case protocol.TurnStartedPayload:
		evt.EventType = string(protocol.EventTypeTurnStarted)
	case protocol.TurnCompletedPayload:
		evt.EventType = string(protocol.EventTypeTurnCompleted)
		evt.Success = &payload.Success
		evt.DurationMs, err = parseOptionalUInt64(payload.DurationMs)
	case protocol.ModelUsageRecordedPayload:
		evt.EventType = string(protocol.EventTypeModelUsageRecorded)
		providerID, modelID := payload.ProviderID, payload.ModelID
		evt.ProviderID, evt.ModelID = &providerID, &modelID
		if evt.TokenInput, err = parseOptionalUInt64(payload.Tokens.InputTokens); err == nil {
			evt.TokenOutput, err = parseOptionalUInt64(payload.Tokens.OutputTokens)
		}
		if err == nil {
			evt.TokenCacheRead, err = parseOptionalUInt64(payload.Tokens.CacheReadTokens)
		}
		if err == nil {
			evt.TokenCacheWrite, err = parseOptionalUInt64(payload.Tokens.CacheWriteTokens)
		}
		if err == nil {
			evt.TokenReasoning, err = parseOptionalUInt64(payload.Tokens.ReasoningTokens)
		}
		if err == nil {
			evt.TokenTool, err = parseOptionalUInt64(payload.Tokens.ToolTokens)
		}
		if err == nil {
			evt.TokenTotal, err = parseOptionalUInt64(payload.Tokens.TotalTokens)
		}
	case protocol.ToolInvokedPayload:
		evt.EventType = string(protocol.EventTypeToolInvoked)
		evt.Success = &payload.Success
		evt.DurationMs, err = parseOptionalUInt64(payload.DurationMs)
	case protocol.SkillInvokedPayload:
		evt.EventType = string(protocol.EventTypeSkillInvoked)
		evt.Success = &payload.Success
		evt.DurationMs, err = parseOptionalUInt64(payload.DurationMs)
	case protocol.CodeChangedPayload:
		evt.EventType = string(protocol.EventTypeCodeChanged)
		if evt.CodeAddedLines, err = parseUInt64(payload.AddedLines); err == nil {
			evt.CodeDeletedLines, err = parseUInt64(payload.RemovedLines)
		}
		if err == nil {
			evt.CodeGeneratedLines, err = parseOptionalUInt64(payload.GeneratedLines)
		}
		if err == nil {
			evt.CodeAcceptedLines, err = parseOptionalUInt64(payload.AcceptedLines)
		}
		evt.CodeFileCount = &payload.FileCount
	case protocol.CostRecordedPayload:
		evt.EventType = string(protocol.EventTypeCostRecorded)
	case protocol.AgentSpawnedPayload:
		evt.EventType = string(protocol.EventTypeAgentSpawned)
	default:
		return nil, fmt.Errorf("unsupported payload %T", env.Payload.Value)
	}
	if err != nil {
		return nil, fmt.Errorf("invalid uint64 wire value: %w", err)
	}
	return evt, nil
}

func parseOptionalUInt64(value *protocol.UInt64String) (*uint64, error) {
	if value == nil {
		return nil, nil
	}
	return parseUInt64(*value)
}

func parseUInt64(value protocol.UInt64String) (*uint64, error) {
	parsed, err := strconv.ParseUint(string(value), 10, 64)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func decodeBase64URL32(value string) ([32]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, fmt.Errorf("expected 32-byte base64url value")
	}
	var result [32]byte
	copy(result[:], decoded)
	return result, nil
}

func decodeHMAC(value string) ([32]byte, error) {
	const prefix = "hmac-sha256:"
	if !strings.HasPrefix(value, prefix) {
		return [32]byte{}, fmt.Errorf("missing HMAC prefix")
	}
	return decodeBase64URL32(strings.TrimPrefix(value, prefix))
}

func hexOrHash(s string) ([32]byte, error) {
	// Try hex decode first; if not valid hex, SHA256 hash the string
	b, err := hex.DecodeString(s)
	if err == nil && len(b) == 32 {
		var arr [32]byte
		copy(arr[:], b)
		return arr, nil
	}
	return sha256.Sum256([]byte(s)), nil
}
