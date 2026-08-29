package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	mysqlerr "github.com/go-sql-driver/mysql"

	"tokendance/internal/domain"
	"tokendance/internal/store/sqlcgen"
	"tokendance/internal/telemetry"
)

type ingestStore struct {
	db *sql.DB
}

func (s *ingestStore) GetIngestInstallation(ctx context.Context, installationID string) (*domain.Installation, error) {
	row, err := sqlcgen.New(s.db).GetIngestInstallationByID(ctx, installationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get ingest installation: %w", err)
	}
	return &domain.Installation{
		InstallationID:     row.InstallationID,
		UserID:             row.UserID,
		DevicePublicKey:    scanBytes32(row.DevicePublicKey),
		DeviceName:         ptrFromNullString(row.DeviceName),
		OSType:             row.OsType,
		OSVersion:          ptrFromNullString(row.OsVersion),
		Architecture:       row.Architecture,
		CollectorVersion:   row.CollectorVersion,
		InstallationStatus: domain.InstallationStatus(row.InstallationStatus),
		DisabledAt:         ptrFromNullTime(row.DisabledAt),
		DisabledReason:     ptrFromNullString(row.DisabledReason),
		StatusVersion:      row.StatusVersion,
		RegisteredAt:       row.RegisteredAt,
		LastSeenAt:         ptrFromNullTime(row.LastSeenAt),
		RevokedAt:          ptrFromNullTime(row.RevokedAt),
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

func (s *ingestStore) CommitIngest(ctx context.Context, batch domain.IngestBatch) (*domain.IngestResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer tx.Rollback()

	var userID string
	var installationStatus domain.InstallationStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT user_id, installation_status
		FROM installations
		WHERE installation_id = ?
		FOR UPDATE`, batch.InstallationID).Scan(&userID, &installationStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("lock ingest installation: %w", err)
	}
	if installationStatus == domain.InstallationStatusRevoked {
		return nil, domain.ErrDeviceRevoked
	}
	if installationStatus == domain.InstallationStatusDisabled {
		return nil, domain.ErrDeviceDisabled
	}

	var accountStatus domain.AccountStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT account_status
		FROM users
		WHERE user_id = ?
		FOR UPDATE`, userID).Scan(&accountStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrAccountSuspended
		}
		return nil, fmt.Errorf("lock ingest user: %w", err)
	}
	if accountStatus != domain.AccountStatusActive {
		return nil, domain.ErrAccountSuspended
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ingest_nonces (installation_id, nonce_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?)`, batch.InstallationID, batch.NonceHash[:], batch.NonceExpiresAt, batch.ReceivedAt); err != nil {
		if isDuplicateKey(err) {
			return nil, domain.ErrNonceReplay
		}
		return nil, fmt.Errorf("reserve ingest nonce: %w", err)
	}

	var result domain.IngestResult
	existing, err := sqlcgen.New(tx).LockIngestBatch(ctx, batch.BatchID)
	if err == nil {
		if existing.InstallationID != batch.InstallationID || scanBytes32(existing.RequestSha256) != batch.RequestSHA256 {
			return nil, domain.ErrBatchHashConflict
		}
		result.BatchID = batch.BatchID
		result.AcceptedCount = existing.AcceptedCount
		result.DuplicateCount = existing.DuplicateCount
		result.RejectedCount = existing.RejectedCount
		if existing.CommittedAt.Valid {
			result.CommittedAt = existing.CommittedAt.Time
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit idempotent ingest: %w", err)
		}
		return &result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lock ingest batch: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ingest_batches (
			batch_id, installation_id, request_sha256, event_count,
			accepted_count, duplicate_count, rejected_count, batch_status,
			received_at, updated_at
		) VALUES (?, ?, ?, ?, 0, 0, ?, 'received', ?, ?)`,
		batch.BatchID,
		batch.InstallationID,
		batch.RequestSHA256[:],
		batch.EventCount,
		batch.RejectedCount,
		batch.ReceivedAt,
		batch.ReceivedAt,
	); err != nil {
		if isDuplicateKey(err) {
			return nil, domain.ErrBatchHashConflict
		}
		return nil, fmt.Errorf("reserve ingest batch: %w", err)
	}

	for i := range batch.Events {
		inserted, err := insertUsageEvent(ctx, tx, batch, userID, &batch.Events[i])
		if err != nil {
			return nil, err
		}
		if inserted {
			result.AcceptedCount++
		} else {
			result.DuplicateCount++
		}
	}
	result.BatchID = batch.BatchID
	result.RejectedCount = batch.RejectedCount
	result.CommittedAt = batch.ReceivedAt

	status := "committed"
	if result.RejectedCount > 0 {
		status = "partial"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE ingest_batches
		SET accepted_count = ?, duplicate_count = ?, rejected_count = ?,
		    batch_status = ?, committed_at = ?, updated_at = ?
		WHERE batch_id = ?`,
		result.AcceptedCount,
		result.DuplicateCount,
		result.RejectedCount,
		status,
		batch.ReceivedAt,
		batch.ReceivedAt,
		batch.BatchID,
	); err != nil {
		return nil, fmt.Errorf("finalize ingest batch: %w", err)
	}
	if err := sqlcgen.New(tx).UpdateInstallationLastSeen(ctx, sqlcgen.UpdateInstallationLastSeenParams{
		LastSeenAt:     sql.NullTime{Time: batch.ReceivedAt, Valid: true},
		UpdatedAt:      batch.ReceivedAt,
		InstallationID: batch.InstallationID,
	}); err != nil {
		return nil, fmt.Errorf("update installation last seen: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit ingest transaction: %w", err)
	}
	return &result, nil
}

func insertUsageEvent(ctx context.Context, tx *sql.Tx, batch domain.IngestBatch, userID string, event *domain.UsageEvent) (bool, error) {
	if err := telemetry.ValidateSafeExtensionJSON(event.EventType, event.SafeExtensionJSON); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO usage_events (
			event_id, schema_version, batch_id, installation_id, user_id,
			adapter_id, adapter_version, agent_id, agent_version, provider_id, model_id,
			event_type, accuracy, source_kind, occurred_at, received_at,
			session_hash, parent_session_hash, turn_hash, tool_call_hash,
			token_input, token_output, token_cache_read, token_cache_write, token_reasoning, token_total,
			duration_ms, success, tool_category, skill_key, skill_public_name, skill_invoke_type, plugin_key,
			code_generated_lines, code_accepted_lines, code_added_lines, code_deleted_lines, code_file_count,
			cost_amount, cost_currency, cost_source, privacy_policy_version, safe_extension_json
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)
		ON DUPLICATE KEY UPDATE event_id = event_id`,
		event.EventID[:], event.SchemaVersion, batch.BatchID, batch.InstallationID, userID,
		event.AdapterID, event.AdapterVersion, event.AgentID, event.AgentVersion, event.ProviderID, event.ModelID,
		event.EventType, event.Accuracy, event.SourceKind, event.OccurredAt, batch.ReceivedAt,
		bytes32PtrSlice(event.SessionHash), bytes32PtrSlice(event.ParentSessionHash), bytes32PtrSlice(event.TurnHash), bytes32PtrSlice(event.ToolCallHash),
		event.TokenInput, event.TokenOutput, event.TokenCacheRead, event.TokenCacheWrite, event.TokenReasoning, event.TokenTotal,
		event.DurationMS, event.Success, event.ToolCategory, bytes32PtrSlice(event.SkillKey), event.SkillPublicName, event.SkillInvokeType, bytes32PtrSlice(event.PluginKey),
		event.CodeGeneratedLines, event.CodeAcceptedLines, event.CodeAddedLines, event.CodeDeletedLines, event.CodeFileCount,
		event.CostAmount, event.CostCurrency, event.CostSource, event.PrivacyPolicyVersion, nullableJSON(event.SafeExtensionJSON),
	)
	if err != nil {
		return false, fmt.Errorf("insert usage event: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read usage event insert result: %w", err)
	}
	return rows == 1, nil
}

func nullableJSON(value []byte) interface{} {
	if len(value) == 0 {
		return nil
	}
	return value
}

func isDuplicateKey(err error) bool {
	var mysqlError *mysqlerr.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}
