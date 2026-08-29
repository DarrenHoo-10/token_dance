package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	body, err := json.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("marshal batch: %w", err)
	}
	return s.ProcessBatchWithHash(ctx, installationID, batch, sha256.Sum256(body))
}

// ProcessBatchWithHash ingests a batch using the hash of the authenticated body.
func (s *Service) ProcessBatchWithHash(ctx context.Context, installationID string, batch protocol.UploadBatch, bodyHash [32]byte) (*Result, error) {
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

	// Batch idempotency: the same ID may only be replayed with the same body.
	existing, err := s.Batches.GetBatch(ctx, batch.BatchID)
	if err == nil && existing != nil {
		if existing.InstallationID != installationID || existing.RequestSHA256 != bodyHash {
			return nil, store.ErrBatchHashConflict
		}
		if existing.CommittedAt != nil {
			return s.buildResultFromExisting(ctx, existing)
		}
	}

	now := time.Now().UTC()

	ib := &domain.IngestBatch{
		BatchID:        batch.BatchID,
		InstallationID: installationID,
		RequestSHA256:  bodyHash,
		EventCount:     uint32(len(batch.Events)),
		BatchStatus:    "received",
		ReceivedAt:     now,
	}
	var validEvents []*store.IngestEvent
	var rejected []RejectedEvent
	var durableRejected []store.BatchRejection
	for ordinal, env := range batch.Events {
		if rej := validateEnvelope(env); rej != nil {
			rejected = append(rejected, *rej)
			durableRejected = append(durableRejected, toBatchRejection(uint32(ordinal), *rej))
			continue
		}
		evt, err := envelopeToEvent(env, batch.BatchID, installationID, inst.UserID, now)
		if err != nil {
			rej := RejectedEvent{EventID: string(env.EventID), ErrorCode: string(protocol.RejectedEventErrorCodeSchemaInvalid), Retryable: false}
			rejected = append(rejected, rej)
			durableRejected = append(durableRejected, toBatchRejection(uint32(ordinal), rej))
			continue
		}
		validEvents = append(validEvents, evt)
	}
	ib.RejectedCount = uint32(len(rejected))

	if atomic, ok := s.Batches.(store.AtomicIngestStore); ok {
		committed, err := atomic.CommitBatch(ctx, ib, validEvents, durableRejected)
		if err != nil {
			if errors.Is(err, store.ErrBatchHashConflict) {
				return nil, err
			}
			return nil, fmt.Errorf("commit batch: %w", err)
		}
		return resultFromCommit(committed), nil
	}

	if err := s.Batches.CreateBatch(ctx, ib); err != nil {
		existing, err2 := s.Batches.GetBatch(ctx, batch.BatchID)
		if err2 == nil && existing != nil {
			if existing.InstallationID != installationID || existing.RequestSHA256 != bodyHash {
				return nil, store.ErrBatchHashConflict
			}
			return s.buildResultFromExisting(ctx, existing)
		}
		return nil, fmt.Errorf("create batch: %w", err)
	}
	for _, ingestEvent := range validEvents {
		evt := ingestEvent.Event
		inserted, err := s.Events.InsertEvent(ctx, evt)
		if err != nil {
			rejected = append(rejected, RejectedEvent{EventID: base64.RawURLEncoding.EncodeToString(evt.EventID[:]), ErrorCode: string(protocol.RejectedEventErrorCodeInternalRetryable), Retryable: true})
			continue
		}
		if inserted {
			ib.AcceptedCount++
		} else {
			ib.DuplicateCount++
		}
	}
	ib.RejectedCount = uint32(len(rejected))
	if ib.RejectedCount == 0 {
		ib.BatchStatus = "committed"
	} else if ib.AcceptedCount > 0 {
		ib.BatchStatus = "partial"
	} else {
		ib.BatchStatus = "rejected"
	}
	committedAt := time.Now().UTC()
	ib.CommittedAt = &committedAt
	if err := s.Batches.UpdateBatch(ctx, ib); err != nil {
		return nil, fmt.Errorf("update batch: %w", err)
	}
	if err := s.Installs.UpdateLastSeen(ctx, installationID, now); err != nil {
		return nil, fmt.Errorf("update last seen: %w", err)
	}
	return &Result{BatchID: batch.BatchID, Accepted: ib.AcceptedCount, Duplicates: ib.DuplicateCount, Rejected: rejected, ServerTime: time.Now().UTC()}, nil
}

func (s *Service) buildResultFromExisting(ctx context.Context, b *domain.IngestBatch) (*Result, error) {
	result := &Result{
		BatchID:    b.BatchID,
		Accepted:   b.AcceptedCount,
		Duplicates: b.DuplicateCount,
		ServerTime: time.Now().UTC(),
	}
	if rejectionStore, ok := s.Batches.(store.BatchRejectionStore); ok {
		rejected, err := rejectionStore.GetBatchRejections(ctx, b.BatchID)
		if err != nil {
			return nil, fmt.Errorf("load batch rejections: %w", err)
		}
		for _, rejection := range rejected {
			result.Rejected = append(result.Rejected, RejectedEvent{EventID: rejection.EventID, ErrorCode: rejection.ErrorCode, Retryable: rejection.Retryable})
		}
	}
	return result, nil
}

func resultFromCommit(committed *store.IngestCommitResult) *Result {
	result := &Result{BatchID: committed.Batch.BatchID, Accepted: committed.Batch.AcceptedCount, Duplicates: committed.Batch.DuplicateCount, ServerTime: time.Now().UTC()}
	for _, rejection := range committed.Rejected {
		result.Rejected = append(result.Rejected, RejectedEvent{EventID: rejection.EventID, ErrorCode: rejection.ErrorCode, Retryable: rejection.Retryable})
	}
	return result
}

func toBatchRejection(ordinal uint32, rejected RejectedEvent) store.BatchRejection {
	return store.BatchRejection{Ordinal: ordinal, EventID: rejected.EventID, ErrorCode: rejected.ErrorCode, Retryable: rejected.Retryable}
}

func validateEnvelope(env protocol.EventEnvelope) *RejectedEvent {
	if env.EventID == "" {
		return &RejectedEvent{EventID: "", ErrorCode: string(protocol.RejectedEventErrorCodeSchemaInvalid), Retryable: false}
	}
	if env.InstallationID == "" {
		return &RejectedEvent{EventID: string(env.EventID), ErrorCode: string(protocol.RejectedEventErrorCodeSchemaInvalid), Retryable: false}
	}
	return nil
}

func envelopeToEvent(env protocol.EventEnvelope, batchID, installationID, userID string, receivedAt time.Time) (*store.IngestEvent, error) {
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

	persisted := &store.IngestEvent{Event: evt}
	switch payload := env.Payload.Value.(type) {
	case protocol.SessionStartedPayload:
		evt.EventType = string(protocol.EventTypeSessionStarted)
		evt.ModelID = payload.ModelID
		if payload.WorkspaceHash != nil {
			value, decodeErr := decodeHMAC(string(*payload.WorkspaceHash))
			err = decodeErr
			evt.WorkspaceHash = &value
		}
	case protocol.SessionEndedPayload:
		evt.EventType = string(protocol.EventTypeSessionEnded)
		reason := domain.SessionEndReason(payload.Reason)
		evt.SessionEndReason = &reason
		evt.DurationMs, err = parseOptionalUInt64(payload.DurationMs)
	case protocol.TurnStartedPayload:
		evt.EventType = string(protocol.EventTypeTurnStarted)
		if payload.Trigger != nil {
			trigger := domain.TurnTrigger(*payload.Trigger)
			evt.TurnTrigger = &trigger
		}
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
		persisted.ToolCategory = &payload.ToolCategory
	case protocol.SkillInvokedPayload:
		evt.EventType = string(protocol.EventTypeSkillInvoked)
		evt.Success = &payload.Success
		evt.DurationMs, err = parseOptionalUInt64(payload.DurationMs)
		if err == nil {
			value, decodeErr := decodeHMAC(string(payload.SkillKey))
			err = decodeErr
			persisted.SkillKey = &value
		}
		invokeType := string(payload.InvokeType)
		persisted.SkillInvokeType = &invokeType
		if payload.PluginKey != nil && err == nil {
			value, decodeErr := decodeHMAC(string(*payload.PluginKey))
			err = decodeErr
			persisted.PluginKey = &value
		}
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
		persisted.CodeLanguage = payload.Language
	case protocol.CostRecordedPayload:
		evt.EventType = string(protocol.EventTypeCostRecorded)
		amount, currency, source := string(payload.Amount), payload.Currency, string(payload.Source)
		persisted.CostAmount, persisted.CostCurrency, persisted.CostSource = &amount, &currency, &source
		if payload.DiscountAmount != nil {
			discount := string(*payload.DiscountAmount)
			persisted.CostDiscountAmount = &discount
		}
	case protocol.AgentSpawnedPayload:
		evt.EventType = string(protocol.EventTypeAgentSpawned)
		value, decodeErr := decodeHMAC(string(payload.ChildSessionHash))
		err = decodeErr
		persisted.ChildSessionHash = &value
		persisted.SpawnedAgentType = &payload.SpawnedAgentType
	default:
		return nil, fmt.Errorf("unsupported payload %T", env.Payload.Value)
	}
	if err != nil {
		return nil, fmt.Errorf("invalid uint64 wire value: %w", err)
	}
	return persisted, nil
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
