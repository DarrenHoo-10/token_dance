package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
)

func (s *ingestStore) CommitTelemetryEventsV2(ctx context.Context, in domain.TelemetryEventsV2Input) (*domain.TelemetryEventsV2Result, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin telemetry v2 ingest: %w", err)
	}
	defer tx.Rollback()

	var accountStatus domain.AccountStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT account_status FROM users WHERE user_id = ? FOR SHARE`, in.UserID).Scan(&accountStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrAccountSuspended
		}
		return nil, fmt.Errorf("lock ingest user: %w", err)
	}
	if accountStatus != domain.AccountStatusActive {
		return nil, domain.ErrAccountSuspended
	}

	var (
		ownerUserID        string
		installationStatus domain.InstallationStatus
		statusVersion      uint64
		disabledAt         sql.NullTime
		revokedAt          sql.NullTime
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT user_id, installation_status, status_version, disabled_at, revoked_at
		FROM installations
		WHERE installation_id = ?
		FOR SHARE`, in.InstallationID).Scan(&ownerUserID, &installationStatus, &statusVersion, &disabledAt, &revokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("lock ingest installation: %w", err)
	}
	if installationStatus == domain.InstallationStatusRevoked || revokedAt.Valid {
		return nil, domain.ErrDeviceRevoked
	}
	if installationStatus == domain.InstallationStatusDisabled || disabledAt.Valid {
		return nil, domain.ErrDeviceDisabled
	}
	if ownerUserID != in.UserID {
		return nil, domain.ErrInstallationUserMismatch
	}
	if statusVersion != in.BindingStatusVersion {
		return nil, domain.ErrBindingVersionMismatch
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ingest_nonces (installation_id, nonce_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?)`,
		in.InstallationID, in.NonceHash[:], in.NonceExpiresAt, in.ReceivedAt,
	); err != nil {
		if isDuplicateKey(err) {
			return nil, domain.ErrNonceReplay
		}
		return nil, fmt.Errorf("reserve ingest nonce: %w", err)
	}

	type indexedEvent struct {
		index int
		event v2.EventEnvelope
		id    [32]byte
	}
	ordered := make([]indexedEvent, 0, len(in.Events))
	for i := range in.Events {
		id, err := decodeBase64URL32(string(in.Events[i].EventID))
		if err != nil {
			return nil, fmt.Errorf("decode event id: %w", domain.ErrInvalidArgument)
		}
		ordered = append(ordered, indexedEvent{index: i, event: in.Events[i], id: id})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i].id[:], ordered[j].id[:]) < 0
	})

	acks := make([]v2.EventAck, len(in.Events))
	nowMs := in.ReceivedAt.UnixMilli()
	lowerBoundMs := domain.StartOfDay(in.ReceivedAt).AddDate(0, 0, -14).UnixMilli()
	futureLimitMs := in.ReceivedAt.Add(5 * time.Minute).UnixMilli()

	accepted := false
	for _, item := range ordered {
		ack, err := processTelemetryEventV2(ctx, tx, in, item.event, item.id, nowMs, lowerBoundMs, futureLimitMs)
		if err != nil {
			return nil, err
		}
		acks[item.index] = ack
		accepted = accepted || ack.Result == v2.AckResultAccepted
	}

	// The revision and facts commit together. Duplicate retries must not make
	// team snapshots stale, and an upload outside a team must remain valid.
	if accepted {
		if err := bumpTeamSourceRevisionForUser(ctx, tx, in.UserID, in.ReceivedAt); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit telemetry v2 ingest: %w", err)
	}

	return &domain.TelemetryEventsV2Result{
		RequestID:    in.RequestID,
		ServerTimeMs: uint64(nowMs),
		Acks:         acks,
	}, nil
}

func processTelemetryEventV2(
	ctx context.Context,
	tx *sql.Tx,
	in domain.TelemetryEventsV2Input,
	event v2.EventEnvelope,
	eventID [32]byte,
	nowMs, lowerBoundMs, futureLimitMs int64,
) (v2.EventAck, error) {
	ack := v2.EventAck{
		EventID:     event.EventID,
		ContentHash: event.ContentHash,
	}

	if event.SchemaVersion != v2.SchemaVersion || event.MetricSemanticsVersion != v2.MetricSemanticsVersion {
		code := v2.AckErrorCodeUnsupportedVersion
		ack.Result = v2.AckResultBlocked
		ack.Code = &code
		return ack, nil
	}

	occurredAt, err := strconv.ParseUint(string(event.OccurredAt), 10, 64)
	if err != nil {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}
	if int64(occurredAt) > futureLimitMs {
		code := v2.AckErrorCodeFutureEventTime
		ack.Result = v2.AckResultRetry
		ack.Code = &code
		return ack, nil
	}

	providedHash, err := decodeBase64URL32(string(event.ContentHash))
	if err != nil {
		code := v2.AckErrorCodeHashMismatch
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}
	eventMap, err := eventEnvelopeToMap(event)
	if err != nil {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}
	computedHashStr, err := v2.ComputeContentHash(eventMap)
	if err != nil {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}
	computedHash, err := decodeBase64URL32(computedHashStr)
	if err != nil || providedHash != computedHash {
		code := v2.AckErrorCodeHashMismatch
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}

	if int64(occurredAt) < lowerBoundMs {
		existingAck, found, err := classifyExistingTelemetryEvent(ctx, tx, in.InstallationID, eventID, providedHash, event.ContentHash)
		if err != nil {
			return ack, err
		}
		if found {
			return existingAck, nil
		}
		code := v2.AckErrorCodeOutsideWindow
		ack.Result = v2.AckResultDiscarded
		ack.Code = &code
		return ack, nil
	}

	modelKey := uint64(1)
	if event.Model != nil {
		modelKey, err = upsertTelemetryModel(ctx, tx, event.Model.ProviderID, event.Model.ModelID, nowMs)
		if err != nil {
			return ack, err
		}
	}

	var skillID sql.NullInt64
	if event.Skill != nil {
		skillKey, err := decodeBase64URL32(string(event.Skill.SkillKey))
		if err != nil {
			code := v2.AckErrorCodeSchemaInvalid
			ack.Result = v2.AckResultInvalid
			ack.Code = &code
			return ack, nil
		}
		id, err := upsertTelemetrySkill(ctx, tx, in.InstallationID, skillKey, event.Skill.PublicName, nowMs)
		if err != nil {
			return ack, err
		}
		skillID = sql.NullInt64{Int64: int64(id), Valid: true}
	}

	factKey, err := decodeBase64URL32(string(event.FactKey))
	if err != nil {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}
	factRevision, err := strconv.ParseUint(string(event.FactRevision), 10, 64)
	if err != nil || factRevision == 0 {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}

	sessionKey, err := optionalBase64URL32(event.SessionKey)
	if err != nil {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}
	turnKey, err := optionalBase64URL32(event.TurnKey)
	if err != nil {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}
	costScopeKey, err := optionalBase64URL32(event.CostScopeKey)
	if err != nil {
		code := v2.AckErrorCodeSchemaInvalid
		ack.Result = v2.AckResultInvalid
		ack.Code = &code
		return ack, nil
	}

	payloadJSON, err := json.Marshal(event.Payload)
	if err != nil {
		return ack, fmt.Errorf("marshal payload: %w", err)
	}
	statusJSON := []byte(`{"hour":0,"day":0,"month":0}`)

	res, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_events (
			created_at, updated_at, extra,
			user_id, installation_id, event_id, fact_key, fact_revision,
			harness_id, event_type, schema_version, metric_semantics_version,
			content_hash, occurred_at, model_key, skill_id,
			session_key, turn_key, cost_scope_key, payload_json, status_json
		) VALUES (
			?, ?, JSON_OBJECT(),
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?
		)`,
		nowMs, nowMs,
		in.UserID, in.InstallationID, eventID[:], factKey[:], factRevision,
		event.HarnessID, string(event.EventType), event.SchemaVersion, event.MetricSemanticsVersion,
		providedHash[:], occurredAt, modelKey, skillID,
		nullableBytes(sessionKey), nullableBytes(turnKey), nullableBytes(costScopeKey),
		payloadJSON, statusJSON,
	)
	if err == nil {
		rowID, err := res.LastInsertId()
		if err != nil {
			return ack, fmt.Errorf("telemetry event last insert id: %w", err)
		}
		if err := insertTelemetryTasks(ctx, tx, rowID, nowMs); err != nil {
			return ack, err
		}
		ack.Result = v2.AckResultAccepted
		return ack, nil
	}
	if !isDuplicateKey(err) {
		return ack, fmt.Errorf("insert telemetry event: %w", err)
	}

	existingAck, found, err := classifyExistingTelemetryEvent(ctx, tx, in.InstallationID, eventID, providedHash, event.ContentHash)
	if err != nil {
		return ack, err
	}
	if !found {
		return ack, fmt.Errorf("duplicate telemetry event missing after conflict")
	}
	return existingAck, nil
}

func classifyExistingTelemetryEvent(
	ctx context.Context,
	tx *sql.Tx,
	installationID string,
	eventID [32]byte,
	candidateHash [32]byte,
	candidateHashWire v2.Base64Url32,
) (v2.EventAck, bool, error) {
	ack := v2.EventAck{EventID: v2.Base64Url32(base64.RawURLEncoding.EncodeToString(eventID[:])), ContentHash: candidateHashWire}
	var (
		existingHash []byte
		deleteAt     sql.NullInt64
	)
	err := tx.QueryRowContext(ctx, `
		SELECT content_hash, delete_at
		FROM telemetry_events
		WHERE installation_id = ? AND event_id = ?
		FOR UPDATE`, installationID, eventID[:],
	).Scan(&existingHash, &deleteAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ack, false, nil
	}
	if err != nil {
		return ack, false, fmt.Errorf("lock existing telemetry event: %w", err)
	}
	if deleteAt.Valid {
		code := v2.AckErrorCodeDeleted
		ack.Result = v2.AckResultDiscarded
		ack.Code = &code
		return ack, true, nil
	}
	var existing [32]byte
	copy(existing[:], existingHash)
	if existing == candidateHash {
		ack.Result = v2.AckResultDuplicate
		return ack, true, nil
	}
	code := v2.AckErrorCodeHashMismatch
	ack.Result = v2.AckResultConflict
	ack.Code = &code
	return ack, true, nil
}

func upsertTelemetryModel(ctx context.Context, tx *sql.Tx, providerID, modelID string, nowMs int64) (uint64, error) {
	if providerID == "" || modelID == "" {
		return 1, nil
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_models (created_at, updated_at, provider_id, model_id)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`,
		nowMs, nowMs, providerID, modelID,
	)
	if err != nil {
		return 0, fmt.Errorf("upsert telemetry model: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("telemetry model id: %w", err)
	}
	return uint64(id), nil
}

func upsertTelemetrySkill(ctx context.Context, tx *sql.Tx, installationID string, skillKey [32]byte, publicName *string, nowMs int64) (uint64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_skills (created_at, updated_at, installation_id, skill_key, public_name)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			id = LAST_INSERT_ID(id),
			public_name = COALESCE(VALUES(public_name), public_name),
			updated_at = IF(
				VALUES(public_name) IS NULL OR public_name <=> VALUES(public_name),
				updated_at,
				VALUES(updated_at)
			)`,
		nowMs, nowMs, installationID, skillKey[:], nullStringFromPtr(publicName),
	)
	if err != nil {
		return 0, fmt.Errorf("upsert telemetry skill: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("telemetry skill id: %w", err)
	}
	return uint64(id), nil
}

func insertTelemetryTasks(ctx context.Context, tx *sql.Tx, eventRowID int64, nowMs int64) error {
	for _, consumer := range []string{"hour", "day", "month"} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO telemetry_tasks (
				created_at, updated_at, extra, event_row_id, consumer, runnable_at, attempt_count
			) VALUES (?, ?, JSON_OBJECT(), ?, ?, ?, 0)`,
			nowMs, nowMs, eventRowID, consumer, nowMs,
		); err != nil {
			return fmt.Errorf("insert telemetry task %s: %w", consumer, err)
		}
	}
	return nil
}

func eventEnvelopeToMap(event v2.EventEnvelope) (map[string]any, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	return v2.DecodeEventMap(raw)
}

func decodeBase64URL32(value string) ([32]byte, error) {
	var out [32]byte
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return out, err
	}
	if len(decoded) != 32 {
		return out, fmt.Errorf("expected 32 bytes, got %d", len(decoded))
	}
	copy(out[:], decoded)
	return out, nil
}

func optionalBase64URL32(value *v2.Base64Url32) (*[32]byte, error) {
	if value == nil {
		return nil, nil
	}
	decoded, err := decodeBase64URL32(string(*value))
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}

func nullableBytes(value *[32]byte) interface{} {
	if value == nil {
		return nil
	}
	return value[:]
}
