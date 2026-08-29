package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/provider"
)

const deletionLeaseDuration = 2 * time.Minute

type deletionClaim struct {
	requestID      string
	userID         sql.NullString
	installationID sql.NullString
	scope          string
	filterJSON     []byte
	phase          string
	cursor         uint64
	claimToken     string
	generation     uint64
}

type deletionFilter struct {
	InstallationID string `json:"installationId"`
	From           string `json:"from"`
	To             string `json:"to"`
}

func (w *Worker) ProcessDeletionRequests(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}

	completed := 0
	var firstErr error
	for i := 0; i < 5; i++ {
		claim, err := w.claimDeletionRequest(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}

		err = w.executeDeletionClaim(ctx, claim)
		if err == nil {
			completed++
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		if failErr := w.failDeletionClaim(context.Background(), claim, "DELETION_PHASE_FAILED"); failErr != nil && firstErr == nil {
			firstErr = fmt.Errorf("deletion failed: %v; failed to persist retry state: %w", err, failErr)
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return completed, firstErr
}

func (w *Worker) claimDeletionRequest(ctx context.Context) (*deletionClaim, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin deletion claim: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT request_id, user_id, installation_id, deletion_scope,
		       scope_filter_json, phase, progress_cursor
		FROM data_deletion_requests
		WHERE next_attempt_at <= CURRENT_TIMESTAMP(3)
		  AND (
		    (request_status = 'pending'
		     AND (deletion_scope <> 'account' OR cancel_before <= CURRENT_TIMESTAMP(3)))
		    OR request_status = 'failed'
		    OR (request_status = 'running' AND lease_expires_at < CURRENT_TIMESTAMP(3))
		  )
		ORDER BY requested_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`)

	claim := &deletionClaim{}
	if err := row.Scan(
		&claim.requestID,
		&claim.userID,
		&claim.installationID,
		&claim.scope,
		&claim.filterJSON,
		&claim.phase,
		&claim.cursor,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("select deletion claim: %w", err)
	}

	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return nil, fmt.Errorf("generate deletion claim token: %w", err)
	}
	claim.claimToken = "clm_" + token
	claim.phase = deletionPhaseForCursor(claim.cursor)

	res, err := tx.ExecContext(ctx, `
		UPDATE data_deletion_requests
		SET request_status = 'running',
		    phase = ?,
		    claim_token = ?,
		    claim_generation = claim_generation + 1,
		    locked_by = ?,
		    lease_expires_at = DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL ? SECOND),
		    attempt_count = attempt_count + 1,
		    started_at = COALESCE(started_at, CURRENT_TIMESTAMP(3)),
		    last_error_code = NULL,
		    updated_at = CURRENT_TIMESTAMP(3)
		WHERE request_id = ?
		  AND (
		    (request_status = 'pending'
		     AND (deletion_scope <> 'account' OR cancel_before <= CURRENT_TIMESTAMP(3)))
		    OR request_status = 'failed'
		    OR (request_status = 'running' AND lease_expires_at < CURRENT_TIMESTAMP(3))
		  )`, claim.phase, claim.claimToken, w.workerID, int(deletionLeaseDuration/time.Second), claim.requestID)
	if err != nil {
		return nil, fmt.Errorf("update deletion claim: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read deletion claim result: %w", err)
	}
	if affected != 1 {
		return nil, fmt.Errorf("deletion claim lost before update")
	}

	if err := tx.QueryRowContext(ctx,
		"SELECT claim_generation FROM data_deletion_requests WHERE request_id = ?",
		claim.requestID,
	).Scan(&claim.generation); err != nil {
		return nil, fmt.Errorf("read deletion claim generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit deletion claim: %w", err)
	}
	return claim, nil
}

func deletionPhaseForCursor(cursor uint64) string {
	switch cursor {
	case 0:
		return "revoking_access"
	case 1:
		return "deleting_events"
	case 2:
		return "deleting_aggregates"
	case 3:
		return "deleting_objects"
	case 4:
		return "deleting_identity"
	default:
		return "reconciling"
	}
}

func (w *Worker) executeDeletionClaim(ctx context.Context, claim *deletionClaim) error {
	for claim.cursor < 6 {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		switch claim.cursor {
		case 0:
			err = w.deletionRevokeAccess(ctx, claim)
		case 1:
			err = w.deletionDeleteEvents(ctx, claim)
		case 2:
			err = w.deletionDeleteAggregates(ctx, claim)
		case 3:
			err = w.deletionDeleteObjects(ctx, claim)
		case 4:
			err = w.deletionDeleteIdentity(ctx, claim)
		case 5:
			err = w.deletionReconcileAndComplete(ctx, claim)
		}
		if err != nil {
			return err
		}
		claim.cursor++
		claim.phase = deletionPhaseForCursor(claim.cursor)
	}
	return nil
}

func (w *Worker) withDeletionPhaseTx(ctx context.Context, claim *deletionClaim, nextPhase string, nextCursor uint64, fn func(*sql.Tx) error) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deletion phase %s: %w", claim.phase, err)
	}
	defer tx.Rollback()

	var one int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM data_deletion_requests
		WHERE request_id = ? AND request_status = 'running'
		  AND claim_token = ? AND claim_generation = ? AND locked_by = ?
		FOR UPDATE`, claim.requestID, claim.claimToken, claim.generation, w.workerID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("deletion claim fenced")
		}
		return fmt.Errorf("lock deletion phase %s: %w", claim.phase, err)
	}
	if fn != nil {
		if err := fn(tx); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE data_deletion_requests
		SET phase = ?, progress_cursor = ?,
		    lease_expires_at = DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL ? SECOND),
		    updated_at = CURRENT_TIMESTAMP(3)
		WHERE request_id = ? AND request_status = 'running'
		  AND claim_token = ? AND claim_generation = ? AND locked_by = ?`,
		nextPhase, nextCursor, int(deletionLeaseDuration/time.Second), claim.requestID,
		claim.claimToken, claim.generation, w.workerID)
	if err != nil {
		return fmt.Errorf("advance deletion phase %s: %w", claim.phase, err)
	}
	if err := requireOneRow(res); err != nil {
		return fmt.Errorf("advance deletion phase %s: %w", claim.phase, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deletion phase %s: %w", claim.phase, err)
	}
	return nil
}

func (w *Worker) deletionRevokeAccess(ctx context.Context, claim *deletionClaim) error {
	return w.withDeletionPhaseTx(ctx, claim, "deleting_events", 1, func(tx *sql.Tx) error {
		if !claim.userID.Valid {
			return fmt.Errorf("deletion request has no user")
		}
		userID := claim.userID.String
		if claim.scope != "account" {
			return nil
		}
		statements := []struct {
			query string
			args  []interface{}
		}{
			{`UPDATE user_sessions SET session_status = 'revoked', revoked_at = CURRENT_TIMESTAMP(3), revoke_reason = 'account_deletion', updated_at = CURRENT_TIMESTAMP(3) WHERE user_id = ? AND session_status = 'active'`, []interface{}{userID}},
			{`UPDATE installations SET installation_status = 'revoked', revoked_at = COALESCE(revoked_at, CURRENT_TIMESTAMP(3)), disabled_at = NULL, disabled_reason = NULL, updated_at = CURRENT_TIMESTAMP(3) WHERE user_id = ? AND installation_status <> 'revoked'`, []interface{}{userID}},
			{`UPDATE email_challenges SET challenge_status = 'cancelled', updated_at = CURRENT_TIMESTAMP(3) WHERE user_id = ? AND challenge_status = 'pending'`, []interface{}{userID}},
			{`UPDATE email_outbox SET delivery_status = 'cancelled', locked_at = NULL, locked_by = NULL, updated_at = CURRENT_TIMESTAMP(3) WHERE user_id = ? AND delivery_status IN ('pending', 'sending')`, []interface{}{userID}},
			{`UPDATE device_binding_challenges SET challenge_status = 'cancelled', active_session_key = NULL, updated_at = CURRENT_TIMESTAMP(3) WHERE user_id = ? AND challenge_status = 'pending'`, []interface{}{userID}},
		}
		return execDeletionStatements(ctx, tx, statements)
	})
}

func (w *Worker) deletionDeleteEvents(ctx context.Context, claim *deletionClaim) error {
	return w.withDeletionPhaseTx(ctx, claim, "deleting_aggregates", 2, func(tx *sql.Tx) error {
		if !claim.userID.Valid {
			return fmt.Errorf("deletion request has no user")
		}
		switch claim.scope {
		case "account", "all_usage":
			if _, err := tx.ExecContext(ctx, "DELETE FROM usage_events WHERE user_id = ?", claim.userID.String); err != nil {
				return fmt.Errorf("delete user usage events: %w", err)
			}
		case "installation":
			installationID, err := claimInstallationID(claim)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM usage_events WHERE user_id = ? AND installation_id = ?", claim.userID.String, installationID); err != nil {
				return fmt.Errorf("delete installation usage events: %w", err)
			}
		case "time_range":
			from, to, err := claimTimeRange(claim)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM usage_events WHERE user_id = ? AND occurred_at >= ? AND occurred_at < ?", claim.userID.String, from, to); err != nil {
				return fmt.Errorf("delete time-range usage events: %w", err)
			}
		default:
			return fmt.Errorf("unknown deletion scope %q", claim.scope)
		}
		return nil
	})
}

func (w *Worker) deletionDeleteAggregates(ctx context.Context, claim *deletionClaim) error {
	return w.withDeletionPhaseTx(ctx, claim, "deleting_objects", 3, func(tx *sql.Tx) error {
		if !claim.userID.Valid {
			return fmt.Errorf("deletion request has no user")
		}
		userID := claim.userID.String
		if claim.scope == "time_range" {
			from, to, err := claimTimeRange(claim)
			if err != nil {
				return err
			}
			fromDate := from.Format("2006-01-02")
			toDate := to.Add(-time.Millisecond).Format("2006-01-02")
			for _, table := range []string{"daily_user_agent_metrics", "daily_user_agent_model_metrics", "daily_skill_metrics"} {
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?", userID, fromDate, toDate); err != nil {
					return fmt.Errorf("delete %s time range: %w", table, err)
				}
			}
		} else {
			for _, table := range []string{"daily_user_agent_metrics", "daily_user_agent_model_metrics", "daily_skill_metrics"} {
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id = ?", userID); err != nil {
					return fmt.Errorf("delete %s: %w", table, err)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM leaderboard_entries WHERE user_id = ?", userID); err != nil {
			return fmt.Errorf("delete leaderboard entries: %w", err)
		}
		return nil
	})
}

func (w *Worker) deletionDeleteObjects(ctx context.Context, claim *deletionClaim) error {
	if claim.scope != "account" {
		return w.withDeletionPhaseTx(ctx, claim, "deleting_identity", 4, nil)
	}
	if !claim.userID.Valid {
		return fmt.Errorf("account deletion request has no user")
	}
	userID := claim.userID.String

	if err := w.withDeletionClaimLock(ctx, claim, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO deletion_object_keys (request_id, object_key, object_kind)
			SELECT ?, object_key, 'export' FROM data_export_jobs
			WHERE user_id = ? AND object_key IS NOT NULL AND object_key <> ''`, claim.requestID, userID); err != nil {
			return fmt.Errorf("queue export object keys: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO deletion_object_keys (request_id, object_key, object_kind)
			SELECT ?, object_key, 'upload' FROM user_upload_objects
			WHERE user_id = ? AND object_key <> ''`, claim.requestID, userID); err != nil {
			return fmt.Errorf("queue upload object keys: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	rows, err := w.db.QueryContext(ctx, `
		SELECT object_key FROM deletion_object_keys
		WHERE request_id = ? AND deletion_status <> 'deleted'
		ORDER BY object_key`, claim.requestID)
	if err != nil {
		return fmt.Errorf("query deletion object keys: %w", err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return fmt.Errorf("scan deletion object key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate deletion object keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close deletion object keys: %w", err)
	}

	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.renewDeletionLease(ctx, claim); err != nil {
			return err
		}
		err := w.storage.DeleteObject(ctx, key)
		if err != nil && !errors.Is(err, provider.ErrObjectNotFound) {
			if markErr := w.markDeletionObjectFailure(ctx, claim, key, "OBJECT_DELETE_FAILED"); markErr != nil {
				return fmt.Errorf("delete object %s: %v; persist object failure: %w", key, err, markErr)
			}
			return fmt.Errorf("delete object %s: %w", key, err)
		}
		if err := w.markDeletionObjectDeleted(ctx, claim, key); err != nil {
			return err
		}
	}

	return w.withDeletionPhaseTx(ctx, claim, "deleting_identity", 4, func(tx *sql.Tx) error {
		var remaining int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM deletion_object_keys WHERE request_id = ? AND deletion_status <> 'deleted'", claim.requestID).Scan(&remaining); err != nil {
			return fmt.Errorf("count pending deletion object keys: %w", err)
		}
		if remaining != 0 {
			return fmt.Errorf("%d object keys are not deleted", remaining)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM data_export_jobs WHERE user_id = ?", userID); err != nil {
			return fmt.Errorf("delete export jobs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM user_upload_objects WHERE user_id = ?", userID); err != nil {
			return fmt.Errorf("delete upload object rows: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM deletion_object_keys WHERE request_id = ?", claim.requestID); err != nil {
			return fmt.Errorf("delete object-key reconciliation rows: %w", err)
		}
		return nil
	})
}

func (w *Worker) deletionDeleteIdentity(ctx context.Context, claim *deletionClaim) error {
	return w.withDeletionPhaseTx(ctx, claim, "reconciling", 5, func(tx *sql.Tx) error {
		if !claim.userID.Valid {
			return fmt.Errorf("deletion request has no user")
		}
		userID := claim.userID.String
		switch claim.scope {
		case "account":
			statements := []struct {
				query string
				args  []interface{}
			}{
				{`DELETE FROM user_password_credentials WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM email_outbox WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM email_challenges WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM device_binding_challenges WHERE user_id = ?`, []interface{}{userID}},
				{`UPDATE user_security_events SET user_id = NULL, session_id = NULL, subject_lookup_hash = NULL, ip_prefix_hash = NULL, user_agent_hash = NULL, metadata_json = NULL WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM user_sessions WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM public_user_profiles WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM user_privacy_settings WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM user_handle_history WHERE user_id = ?`, []interface{}{userID}},
				{`DELETE FROM installation_adapter_status WHERE installation_id IN (SELECT installation_id FROM installations WHERE user_id = ?)`, []interface{}{userID}},
				{`DELETE FROM ingest_nonces WHERE installation_id IN (SELECT installation_id FROM installations WHERE user_id = ?)`, []interface{}{userID}},
				{`DELETE FROM ingest_batches WHERE installation_id IN (SELECT installation_id FROM installations WHERE user_id = ?)`, []interface{}{userID}},
				{`DELETE FROM installations WHERE user_id = ?`, []interface{}{userID}},
			}
			if err := execDeletionStatements(ctx, tx, statements); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE users
				SET auth_subject_hash = UNHEX(SHA2(CONCAT('deleted:', ?), 256)),
				    email_lookup_hash = NULL, email_ciphertext = NULL,
				    handle = NULL, display_name = 'Deleted User',
				    avatar_url = NULL, avatar_object_id = NULL, bio = NULL,
				    timezone_name = 'UTC', locale = 'en-US',
				    onboarding_completed_at = NULL,
				    account_status = 'deleted', leaderboard_visibility = 'private',
				    deleted_at = CURRENT_TIMESTAMP(3), updated_at = CURRENT_TIMESTAMP(3)
				WHERE user_id = ?`, claim.requestID, userID); err != nil {
				return fmt.Errorf("scrub user tombstone: %w", err)
			}
		case "installation":
			installationID, err := claimInstallationID(claim)
			if err != nil {
				return err
			}
			statements := []struct {
				query string
				args  []interface{}
			}{
				{`DELETE FROM installation_adapter_status WHERE installation_id = ?`, []interface{}{installationID}},
				{`DELETE FROM ingest_nonces WHERE installation_id = ?`, []interface{}{installationID}},
				{`DELETE FROM ingest_batches WHERE installation_id = ?`, []interface{}{installationID}},
				{`DELETE FROM installations WHERE installation_id = ? AND user_id = ?`, []interface{}{installationID, userID}},
			}
			return execDeletionStatements(ctx, tx, statements)
		case "time_range", "all_usage":
			return nil
		default:
			return fmt.Errorf("unknown deletion scope %q", claim.scope)
		}
		return nil
	})
}

func (w *Worker) deletionReconcileAndComplete(ctx context.Context, claim *deletionClaim) error {
	return w.withDeletionClaimLock(ctx, claim, func(tx *sql.Tx) error {
		if err := reconcileDeletionResiduals(ctx, tx, claim); err != nil {
			return err
		}
		auditToken, err := crypto.GenerateOpaqueToken(13)
		if err != nil {
			return fmt.Errorf("generate deletion audit reference: %w", err)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET request_status = 'completed', phase = 'completed', progress_cursor = 6,
			    completed_at = CURRENT_TIMESTAMP(3), audit_reference = ?,
			    active_account_key = NULL, claim_token = NULL, locked_by = NULL,
			    lease_expires_at = NULL, last_error_code = NULL,
			    updated_at = CURRENT_TIMESTAMP(3)
			WHERE request_id = ? AND request_status = 'running'
			  AND claim_token = ? AND claim_generation = ? AND locked_by = ?`,
			"aud_"+auditToken, claim.requestID, claim.claimToken, claim.generation, w.workerID)
		if err != nil {
			return fmt.Errorf("complete deletion request: %w", err)
		}
		if err := requireOneRow(res); err != nil {
			return fmt.Errorf("complete deletion request: %w", err)
		}
		return nil
	})
}

func reconcileDeletionResiduals(ctx context.Context, tx *sql.Tx, claim *deletionClaim) error {
	if !claim.userID.Valid {
		return fmt.Errorf("deletion request has no user")
	}
	userID := claim.userID.String
	var checks []struct {
		name  string
		query string
		args  []interface{}
	}
	checks = append(checks, struct {
		name  string
		query string
		args  []interface{}
	}{"leaderboard entries", "SELECT COUNT(*) FROM leaderboard_entries WHERE user_id = ?", []interface{}{userID}})

	switch claim.scope {
	case "account":
		for _, table := range []string{
			"user_password_credentials", "user_sessions", "email_challenges", "email_outbox",
			"device_binding_challenges", "installations", "usage_events",
			"daily_user_agent_metrics", "daily_user_agent_model_metrics", "daily_skill_metrics",
			"data_export_jobs", "user_upload_objects", "public_user_profiles",
			"user_privacy_settings", "user_handle_history",
		} {
			checks = append(checks, struct {
				name  string
				query string
				args  []interface{}
			}{table, "SELECT COUNT(*) FROM " + table + " WHERE user_id = ?", []interface{}{userID}})
		}
		checks = append(checks,
			struct {
				name  string
				query string
				args  []interface{}
			}{"object keys", "SELECT COUNT(*) FROM deletion_object_keys WHERE request_id = ?", []interface{}{claim.requestID}},
			struct {
				name  string
				query string
				args  []interface{}
			}{"security event PII", "SELECT COUNT(*) FROM user_security_events WHERE user_id = ? OR session_id IS NOT NULL AND session_id IN (SELECT session_id FROM user_sessions WHERE user_id = ?)", []interface{}{userID, userID}},
			struct {
				name  string
				query string
				args  []interface{}
			}{"user PII", "SELECT COUNT(*) FROM users WHERE user_id = ? AND (account_status <> 'deleted' OR email_lookup_hash IS NOT NULL OR email_ciphertext IS NOT NULL OR handle IS NOT NULL OR avatar_url IS NOT NULL OR avatar_object_id IS NOT NULL OR bio IS NOT NULL OR display_name <> 'Deleted User')", []interface{}{userID}},
		)
	case "all_usage", "installation":
		for _, table := range []string{"usage_events", "daily_user_agent_metrics", "daily_user_agent_model_metrics", "daily_skill_metrics"} {
			checks = append(checks, struct {
				name  string
				query string
				args  []interface{}
			}{table, "SELECT COUNT(*) FROM " + table + " WHERE user_id = ?", []interface{}{userID}})
		}
		if claim.scope == "installation" {
			installationID, err := claimInstallationID(claim)
			if err != nil {
				return err
			}
			checks = append(checks, struct {
				name  string
				query string
				args  []interface{}
			}{"installation", "SELECT COUNT(*) FROM installations WHERE installation_id = ?", []interface{}{installationID}})
		}
	case "time_range":
		from, to, err := claimTimeRange(claim)
		if err != nil {
			return err
		}
		checks = append(checks, struct {
			name  string
			query string
			args  []interface{}
		}{"usage events", "SELECT COUNT(*) FROM usage_events WHERE user_id = ? AND occurred_at >= ? AND occurred_at < ?", []interface{}{userID, from, to}})
		fromDate := from.Format("2006-01-02")
		toDate := to.Add(-time.Millisecond).Format("2006-01-02")
		for _, table := range []string{"daily_user_agent_metrics", "daily_user_agent_model_metrics", "daily_skill_metrics"} {
			checks = append(checks, struct {
				name  string
				query string
				args  []interface{}
			}{table, "SELECT COUNT(*) FROM " + table + " WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?", []interface{}{userID, fromDate, toDate}})
		}
	default:
		return fmt.Errorf("unknown deletion scope %q", claim.scope)
	}

	for _, check := range checks {
		var count int64
		if err := tx.QueryRowContext(ctx, check.query, check.args...).Scan(&count); err != nil {
			return fmt.Errorf("reconcile %s: %w", check.name, err)
		}
		if count != 0 {
			return fmt.Errorf("reconcile %s: %d residual rows", check.name, count)
		}
	}
	return nil
}

func (w *Worker) withDeletionClaimLock(ctx context.Context, claim *deletionClaim, fn func(*sql.Tx) error) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deletion fenced transaction: %w", err)
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM data_deletion_requests
		WHERE request_id = ? AND request_status = 'running'
		  AND claim_token = ? AND claim_generation = ? AND locked_by = ?
		FOR UPDATE`, claim.requestID, claim.claimToken, claim.generation, w.workerID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("deletion claim fenced")
		}
		return fmt.Errorf("lock deletion claim: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deletion fenced transaction: %w", err)
	}
	return nil
}

func (w *Worker) renewDeletionLease(ctx context.Context, claim *deletionClaim) error {
	res, err := w.db.ExecContext(ctx, `
		UPDATE data_deletion_requests
		SET lease_expires_at = DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL ? SECOND),
		    updated_at = CURRENT_TIMESTAMP(3)
		WHERE request_id = ? AND request_status = 'running'
		  AND claim_token = ? AND claim_generation = ? AND locked_by = ?`,
		int(deletionLeaseDuration/time.Second), claim.requestID, claim.claimToken, claim.generation, w.workerID)
	if err != nil {
		return fmt.Errorf("renew deletion lease: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return fmt.Errorf("renew deletion lease: %w", err)
	}
	return nil
}

func (w *Worker) markDeletionObjectDeleted(ctx context.Context, claim *deletionClaim, key string) error {
	res, err := w.db.ExecContext(ctx, `
		UPDATE deletion_object_keys k
		JOIN data_deletion_requests r ON r.request_id = k.request_id
		SET k.deletion_status = 'deleted', k.deleted_at = CURRENT_TIMESTAMP(3),
		    k.last_error_code = NULL, k.attempt_count = k.attempt_count + 1,
		    k.updated_at = CURRENT_TIMESTAMP(3)
		WHERE k.request_id = ? AND k.object_key = ?
		  AND r.request_status = 'running' AND r.claim_token = ?
		  AND r.claim_generation = ? AND r.locked_by = ?`,
		claim.requestID, key, claim.claimToken, claim.generation, w.workerID)
	if err != nil {
		return fmt.Errorf("mark object deleted: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return fmt.Errorf("mark object deleted: %w", err)
	}
	return nil
}

func (w *Worker) markDeletionObjectFailure(ctx context.Context, claim *deletionClaim, key, code string) error {
	res, err := w.db.ExecContext(ctx, `
		UPDATE deletion_object_keys k
		JOIN data_deletion_requests r ON r.request_id = k.request_id
		SET k.deletion_status = 'failed', k.deleted_at = NULL,
		    k.last_error_code = ?, k.attempt_count = k.attempt_count + 1,
		    k.updated_at = CURRENT_TIMESTAMP(3)
		WHERE k.request_id = ? AND k.object_key = ?
		  AND r.request_status = 'running' AND r.claim_token = ?
		  AND r.claim_generation = ? AND r.locked_by = ?`,
		code, claim.requestID, key, claim.claimToken, claim.generation, w.workerID)
	if err != nil {
		return fmt.Errorf("mark object failure: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return fmt.Errorf("mark object failure: %w", err)
	}
	return nil
}

func (w *Worker) failDeletionClaim(ctx context.Context, claim *deletionClaim, code string) error {
	res, err := w.db.ExecContext(ctx, `
		UPDATE data_deletion_requests
		SET request_status = 'failed', phase = 'failed',
		    claim_token = NULL, locked_by = NULL, lease_expires_at = NULL,
		    last_error_code = ?,
		    next_attempt_at = DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL 5 SECOND),
		    updated_at = CURRENT_TIMESTAMP(3)
		WHERE request_id = ? AND request_status = 'running'
		  AND claim_token = ? AND claim_generation = ? AND locked_by = ?`,
		code, claim.requestID, claim.claimToken, claim.generation, w.workerID)
	if err != nil {
		return fmt.Errorf("mark deletion failed: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return fmt.Errorf("mark deletion failed: %w", err)
	}
	return nil
}

func claimInstallationID(claim *deletionClaim) (string, error) {
	if claim.installationID.Valid && claim.installationID.String != "" {
		return claim.installationID.String, nil
	}
	var filter deletionFilter
	if err := json.Unmarshal(claim.filterJSON, &filter); err != nil {
		return "", fmt.Errorf("decode installation deletion filter: %w", err)
	}
	if filter.InstallationID == "" {
		return "", fmt.Errorf("installation deletion requires installationId")
	}
	return filter.InstallationID, nil
}

func claimTimeRange(claim *deletionClaim) (time.Time, time.Time, error) {
	var filter deletionFilter
	if err := json.Unmarshal(claim.filterJSON, &filter); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("decode time-range deletion filter: %w", err)
	}
	from, err := time.Parse(time.RFC3339, filter.From)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid deletion from timestamp: %w", err)
	}
	to, err := time.Parse(time.RFC3339, filter.To)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid deletion to timestamp: %w", err)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("deletion time range must have to after from")
	}
	return from.UTC(), to.UTC(), nil
}

func execDeletionStatements(ctx context.Context, tx *sql.Tx, statements []struct {
	query string
	args  []interface{}
}) error {
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("execute deletion statement %q: %w", compactSQL(statement.query), err)
		}
	}
	return nil
}

func compactSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}

func requireOneRow(res sql.Result) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("expected one affected row, got %d", affected)
	}
	return nil
}
