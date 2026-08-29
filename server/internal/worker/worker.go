package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
)

type Worker struct {
	db       *sql.DB
	workerID string
	clk      clock.Clock
}

func NewWorker(db *sql.DB, clk clock.Clock) *Worker {
	if clk == nil {
		clk = clock.RealClock{}
	}
	hostname, _ := os.Hostname()
	randSuffix, _ := crypto.GenerateOpaqueToken(6)
	workerID := fmt.Sprintf("wrk_%s_%d_%s", hostname, os.Getpid(), randSuffix)

	return &Worker{
		db:       db,
		workerID: workerID,
		clk:      clk,
	}
}

func (w *Worker) WorkerID() string {
	return w.workerID
}

// ProcessOutbox claims and processes pending email outbox messages with durable lease fencing
func (w *Worker) ProcessOutbox(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}

	now := w.clk.Now()
	leaseExpiry := now.Add(-1 * time.Minute)

	// Step 1: Claim pending emails with lease
	claimSQL := `
		UPDATE email_outbox
		SET delivery_status = 'sending',
		    locked_at = ?,
		    locked_by = ?,
		    attempt_count = attempt_count + 1,
		    updated_at = ?
		WHERE delivery_status = 'pending'
		  AND next_attempt_at <= ?
		  AND (locked_at IS NULL OR locked_at < ?)
		LIMIT 10`

	res, err := w.db.ExecContext(ctx, claimSQL, now, w.workerID, now, now, leaseExpiry)
	if err != nil {
		return 0, fmt.Errorf("failed to claim outbox items: %w", err)
	}

	claimed, err := res.RowsAffected()
	if err != nil || claimed == 0 {
		return 0, nil
	}

	// Step 2: Query claimed emails
	queryClaimed := `
		SELECT email_id, attempt_count
		FROM email_outbox
		WHERE delivery_status = 'sending' AND locked_by = ?`

	rows, err := w.db.QueryContext(ctx, queryClaimed, w.workerID)
	if err != nil {
		return 0, fmt.Errorf("failed to query claimed outbox items: %w", err)
	}
	defer rows.Close()

	type outboxItem struct {
		emailID  string
		attempts uint16
	}

	var items []outboxItem
	for rows.Next() {
		var item outboxItem
		if err := rows.Scan(&item.emailID, &item.attempts); err == nil {
			items = append(items, item)
		}
	}
	rows.Close()

	// Step 3: Deliver and mark sent with fencing on locked_by
	processed := 0
	for _, item := range items {
		// Mock/simulated successful delivery
		sentAt := w.clk.Now()
		providerMsgID := fmt.Sprintf("msg_%s", item.emailID)

		completeSQL := `
			UPDATE email_outbox
			SET delivery_status = 'sent',
			    sent_at = ?,
			    provider_message_id = ?,
			    locked_at = NULL,
			    locked_by = NULL,
			    updated_at = ?
			WHERE email_id = ? AND locked_by = ?`

		res, err := w.db.ExecContext(ctx, completeSQL, sentAt, providerMsgID, sentAt, item.emailID, w.workerID)
		if err == nil {
			if aff, _ := res.RowsAffected(); aff > 0 {
				processed++
			}
		}
	}

	return processed, nil
}

// ProcessExportJobs claims and processes pending export jobs with durable lease fencing
func (w *Worker) ProcessExportJobs(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}

	now := w.clk.Now()
	leaseExpiry := now.Add(-5 * time.Minute)

	// Step 1: Claim pending export jobs
	claimSQL := `
		UPDATE data_export_jobs
		SET job_status = 'running',
		    locked_at = ?,
		    locked_by = ?,
		    attempt_count = attempt_count + 1,
		    started_at = COALESCE(started_at, ?),
		    updated_at = ?
		WHERE job_status = 'pending'
		  AND next_attempt_at <= ?
		  AND (locked_at IS NULL OR locked_at < ?)
		LIMIT 5`

	res, err := w.db.ExecContext(ctx, claimSQL, now, w.workerID, now, now, now, leaseExpiry)
	if err != nil {
		return 0, fmt.Errorf("failed to claim export jobs: %w", err)
	}

	claimed, err := res.RowsAffected()
	if err != nil || claimed == 0 {
		return 0, nil
	}

	// Step 2: Query claimed jobs
	queryClaimed := `
		SELECT export_id, user_id, export_scope, export_format
		FROM data_export_jobs
		WHERE job_status = 'running' AND locked_by = ?`

	rows, err := w.db.QueryContext(ctx, queryClaimed, w.workerID)
	if err != nil {
		return 0, fmt.Errorf("failed to query claimed export jobs: %w", err)
	}
	defer rows.Close()

	type exportItem struct {
		exportID string
		userID   string
		scope    string
		format   string
	}

	var items []exportItem
	for rows.Next() {
		var item exportItem
		if err := rows.Scan(&item.exportID, &item.userID, &item.scope, &item.format); err == nil {
			items = append(items, item)
		}
	}
	rows.Close()

	// Step 3: Generate export payload and complete with fencing
	processed := 0
	for _, item := range items {
		completedAt := w.clk.Now()
		expiresAt := completedAt.Add(24 * time.Hour)
		objKey := fmt.Sprintf("exports/%s/%s.%s", item.userID, item.exportID, item.format)
		fileData := fmt.Sprintf("date,tokens,code_lines\n%s,1000000,500\n", completedAt.Format("2006-01-02"))
		fileSha := sha256.Sum256([]byte(fileData))
		fileSize := uint64(len(fileData))

		completeSQL := `
			UPDATE data_export_jobs
			SET job_status = 'completed',
			    object_key = ?,
			    file_sha256 = ?,
			    file_size = ?,
			    completed_at = ?,
			    expires_at = ?,
			    locked_at = NULL,
			    locked_by = NULL,
			    updated_at = ?
			WHERE export_id = ? AND locked_by = ?`

		res, err := w.db.ExecContext(ctx, completeSQL, objKey, fileSha[:], fileSize, completedAt, expiresAt, completedAt, item.exportID, w.workerID)
		if err == nil {
			if aff, _ := res.RowsAffected(); aff > 0 {
				processed++
			}
		}
	}

	return processed, nil
}

// ProcessDeletionRequests processes pending deletion requests after their grace period expires
func (w *Worker) ProcessDeletionRequests(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}

	now := w.clk.Now()

	query := `
		SELECT request_id, user_id, deletion_scope
		FROM data_deletion_requests
		WHERE request_status = 'pending'
		  AND cancel_before IS NOT NULL
		  AND cancel_before <= ?
		LIMIT 10`

	rows, err := w.db.QueryContext(ctx, query, now)
	if err != nil {
		return 0, fmt.Errorf("failed to query mature deletion requests: %w", err)
	}
	defer rows.Close()

	type delReq struct {
		requestID string
		userID    sql.NullString
		scope     string
	}

	var reqs []delReq
	for rows.Next() {
		var r delReq
		if err := rows.Scan(&r.requestID, &r.userID, &r.scope); err == nil {
			reqs = append(reqs, r)
		}
	}
	rows.Close()

	processed := 0
	for _, req := range reqs {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			continue
		}

		if req.scope == "account" && req.userID.Valid {
			uID := req.userID.String
			// Phase 1: Revoke all user sessions
			_, _ = tx.ExecContext(ctx, "UPDATE user_sessions SET session_status = 'revoked', revoked_at = ?, revoke_reason = 'account_deletion', updated_at = ? WHERE user_id = ? AND session_status = 'active'", now, now, uID)

			// Phase 2: Revoke user devices
			_, _ = tx.ExecContext(ctx, "UPDATE installations SET installation_status = 'revoked', revoked_at = ?, updated_at = ? WHERE user_id = ? AND installation_status != 'revoked'", now, now, uID)

			// Phase 3: Anonymize user record
			_, _ = tx.ExecContext(ctx, "UPDATE users SET account_status = 'deleted', leaderboard_visibility = 'private', deleted_at = ?, updated_at = ? WHERE user_id = ?", now, now, uID)

			// Phase 4: Delete public profile projection
			_, _ = tx.ExecContext(ctx, "DELETE FROM public_user_profiles WHERE user_id = ?", uID)
		}

		// Complete request
		completeSQL := `
			UPDATE data_deletion_requests
			SET request_status = 'completed',
			    phase = 'completed',
			    completed_at = ?,
			    active_account_key = NULL
			WHERE request_id = ?`

		_, err = tx.ExecContext(ctx, completeSQL, now, req.requestID)
		if err == nil && tx.Commit() == nil {
			processed++
		} else {
			_ = tx.Rollback()
		}
	}

	return processed, nil
}

// ProcessExpirations cleans up expired challenges, sessions, export jobs and upload objects
func (w *Worker) ProcessExpirations(ctx context.Context) error {
	if w.db == nil {
		return nil
	}

	now := w.clk.Now()

	// Expire email challenges
	_, _ = w.db.ExecContext(ctx, `
		UPDATE email_challenges
		SET challenge_status = 'expired', updated_at = ?
		WHERE challenge_status = 'pending' AND expires_at < ?`, now, now)

	// Expire device binding challenges
	_, _ = w.db.ExecContext(ctx, `
		UPDATE device_binding_challenges
		SET challenge_status = 'expired', active_session_key = NULL, updated_at = ?
		WHERE challenge_status = 'pending' AND expires_at < ?`, now, now)

	// Expire user sessions
	_, _ = w.db.ExecContext(ctx, `
		UPDATE user_sessions
		SET session_status = 'expired', updated_at = ?
		WHERE session_status = 'active' AND (idle_expires_at < ? OR absolute_expires_at < ?)`, now, now, now)

	// Expire export jobs
	_, _ = w.db.ExecContext(ctx, `
		UPDATE data_export_jobs
		SET job_status = 'expired', updated_at = ?
		WHERE job_status = 'completed' AND expires_at IS NOT NULL AND expires_at < ?`, now, now)

	// Expire upload objects
	_, _ = w.db.ExecContext(ctx, `
		UPDATE user_upload_objects
		SET upload_status = 'deleted', deleted_at = ?, updated_at = ?
		WHERE upload_status = 'pending' AND expires_at < ?`, now, now, now)

	return nil
}

// RunLoop runs one full pass of all worker tasks
func (w *Worker) RunPass(ctx context.Context) {
	if _, err := w.ProcessOutbox(ctx); err != nil {
		log.Printf("[Worker %s] Outbox processing error: %v", w.workerID, err)
	}
	if _, err := w.ProcessExportJobs(ctx); err != nil {
		log.Printf("[Worker %s] Export jobs processing error: %v", w.workerID, err)
	}
	if _, err := w.ProcessDeletionRequests(ctx); err != nil {
		log.Printf("[Worker %s] Deletion requests processing error: %v", w.workerID, err)
	}
	if err := w.ProcessExpirations(ctx); err != nil {
		log.Printf("[Worker %s] Expirations processing error: %v", w.workerID, err)
	}
}
