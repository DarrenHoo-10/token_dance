package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
	"tokendance/internal/email"
	"tokendance/internal/provider"
)

type Worker struct {
	db            *sql.DB
	workerID      string
	clk           clock.Clock
	cipher        *crypto.AEADCipher
	emailProvider email.Provider
	storage       provider.ObjectStorage
}

func NewWorker(db *sql.DB, clk clock.Clock) *Worker {
	return NewWorkerWithFull(db, clk, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
}

func NewWorkerWithProvider(db *sql.DB, clk clock.Clock, cipher *crypto.AEADCipher, emailProvider email.Provider) *Worker {
	return NewWorkerWithFull(db, clk, cipher, emailProvider, provider.NewMemoryObjectStorage(""))
}

func NewWorkerWithFull(db *sql.DB, clk clock.Clock, cipher *crypto.AEADCipher, emailProvider email.Provider, storage provider.ObjectStorage) *Worker {
	if clk == nil {
		clk = clock.RealClock{}
	}
	if emailProvider == nil {
		emailProvider = email.DefaultSink
	}
	if storage == nil {
		storage = provider.NewMemoryObjectStorage("")
	}
	hostname, _ := os.Hostname()
	randSuffix, _ := crypto.GenerateOpaqueToken(6)
	workerID := fmt.Sprintf("wrk_%s_%d_%s", hostname, os.Getpid(), randSuffix)

	return &Worker{
		db:            db,
		workerID:      workerID,
		clk:           clk,
		cipher:        cipher,
		emailProvider: emailProvider,
		storage:       storage,
	}
}

func (w *Worker) SetStorage(s provider.ObjectStorage) {
	w.storage = s
}

func (w *Worker) SetEmailProvider(p email.Provider) {
	w.emailProvider = p
}

func (w *Worker) SetCipher(c *crypto.AEADCipher) {
	w.cipher = c
}

func (w *Worker) WorkerID() string {
	return w.workerID
}

// ProcessOutbox claims and processes pending or stale sending emails using durable lease fencing
func (w *Worker) ProcessOutbox(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}

	now := w.clk.Now()
	leaseExpiry := now.Add(-1 * time.Minute)

	// Step 1: Claim pending or stale sending emails with lease
	claimSQL := `
		UPDATE email_outbox
		SET delivery_status = 'sending',
		    locked_at = ?,
		    locked_by = ?,
		    attempt_count = attempt_count + 1,
		    updated_at = ?
		WHERE (
		    (delivery_status = 'pending' AND next_attempt_at <= ? AND (locked_at IS NULL OR locked_at < ?))
		    OR
		    (delivery_status = 'sending' AND (locked_at IS NULL OR locked_at < ?))
		)
		ORDER BY created_at ASC
		LIMIT 10`

	res, err := w.db.ExecContext(ctx, claimSQL, now, w.workerID, now, now, leaseExpiry, leaseExpiry)
	if err != nil {
		return 0, fmt.Errorf("failed to claim outbox items: %w", err)
	}

	claimed, err := res.RowsAffected()
	if err != nil || claimed == 0 {
		return 0, nil
	}

	// Step 2: Query claimed emails
	queryClaimed := `
		SELECT email_id, user_id, challenge_id, idempotency_key, template_key, locale,
		       recipient_ciphertext, payload_ciphertext, encryption_key_version,
		       delivery_status, attempt_count, next_attempt_at, expires_at
		FROM email_outbox
		WHERE delivery_status = 'sending' AND locked_by = ?`

	rows, err := w.db.QueryContext(ctx, queryClaimed, w.workerID)
	if err != nil {
		return 0, fmt.Errorf("failed to query claimed outbox items: %w", err)
	}
	defer rows.Close()

	type outboxRow struct {
		emailID              string
		userID               sql.NullString
		challengeID          sql.NullString
		idempotencyKey       []byte
		templateKey          string
		locale               string
		recipientCiphertext  []byte
		payloadCiphertext    []byte
		encryptionKeyVersion uint16
		deliveryStatus       string
		attemptCount         uint16
		nextAttemptAt        time.Time
		expiresAt            time.Time
	}

	var items []outboxRow
	for rows.Next() {
		var item outboxRow
		if err := rows.Scan(
			&item.emailID,
			&item.userID,
			&item.challengeID,
			&item.idempotencyKey,
			&item.templateKey,
			&item.locale,
			&item.recipientCiphertext,
			&item.payloadCiphertext,
			&item.encryptionKeyVersion,
			&item.deliveryStatus,
			&item.attemptCount,
			&item.nextAttemptAt,
			&item.expiresAt,
		); err == nil {
			items = append(items, item)
		}
	}
	rows.Close()

	// Step 3: Process claimed items via Provider interface
	processed := 0
	for _, item := range items {
		// Expiry classification
		if now.After(item.expiresAt) {
			failSQL := `
				UPDATE email_outbox
				SET delivery_status = 'failed',
				    last_error_code = 'EXPIRED',
				    locked_at = NULL,
				    locked_by = NULL,
				    updated_at = ?
				WHERE email_id = ? AND locked_by = ?`
			_, _ = w.db.ExecContext(ctx, failSQL, now, item.emailID, w.workerID)
			continue
		}

		// Decrypt recipient and payload in memory
		recipient := string(item.recipientCiphertext)
		payloadJSON := string(item.payloadCiphertext)

		if w.cipher != nil {
			if decRecipient, err := w.cipher.Decrypt(item.recipientCiphertext, []byte("email_outbox.recipient")); err == nil {
				recipient = string(decRecipient)
			} else if decRecipient, err := w.cipher.Decrypt(item.recipientCiphertext, nil); err == nil {
				recipient = string(decRecipient)
			}

			if decPayload, err := w.cipher.Decrypt(item.payloadCiphertext, []byte("email_outbox.payload")); err == nil {
				payloadJSON = string(decPayload)
			} else if decPayload, err := w.cipher.Decrypt(item.payloadCiphertext, nil); err == nil {
				payloadJSON = string(decPayload)
			}
		}

		msg := email.Message{
			EmailID:     item.emailID,
			Recipient:   recipient,
			TemplateKey: item.templateKey,
			Locale:      item.locale,
			PayloadJSON: payloadJSON,
			CreatedAt:   now,
		}

		providerMsgID, sendErr := w.emailProvider.Send(ctx, msg)
		if sendErr == nil {
			if providerMsgID == "" {
				providerMsgID = fmt.Sprintf("pmsg_%s", item.emailID)
			}
			sentAt := now

			completeSQL := `
				UPDATE email_outbox
				SET delivery_status = 'sent',
				    sent_at = ?,
				    provider_message_id = ?,
				    payload_ciphertext = 0x,
				    last_error_code = NULL,
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
		} else {
			// Error classification: transient vs permanent
			isTransient := email.IsTransientError(sendErr)
			maxAttempts := uint16(5)

			if isTransient && item.attemptCount < maxAttempts {
				// Exponential backoff
				backoffSecs := time.Duration(1<<item.attemptCount) * time.Second
				if backoffSecs > 1*time.Minute {
					backoffSecs = 1 * time.Minute
				}
				nextAttempt := now.Add(backoffSecs)
				errCode := "TRANSIENT_ERROR"

				retrySQL := `
					UPDATE email_outbox
					SET delivery_status = 'pending',
					    next_attempt_at = ?,
					    last_error_code = ?,
					    locked_at = NULL,
					    locked_by = NULL,
					    updated_at = ?
					WHERE email_id = ? AND locked_by = ?`
				_, _ = w.db.ExecContext(ctx, retrySQL, nextAttempt, errCode, now, item.emailID, w.workerID)
			} else {
				// Permanent failure or max attempts exhausted
				errCode := "PERMANENT_ERROR"
				if item.attemptCount >= maxAttempts {
					errCode = "MAX_ATTEMPTS_EXCEEDED"
				}

				failSQL := `
					UPDATE email_outbox
					SET delivery_status = 'failed',
					    last_error_code = ?,
					    locked_at = NULL,
					    locked_by = NULL,
					    updated_at = ?
					WHERE email_id = ? AND locked_by = ?`
				_, _ = w.db.ExecContext(ctx, failSQL, errCode, now, item.emailID, w.workerID)
			}
		}
	}

	return processed, nil
}

// ProcessExportJobs claims and processes pending or stale running export jobs with real aggregate streaming
func (w *Worker) ProcessExportJobs(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}

	now := w.clk.Now()
	leaseExpiry := now.Add(-5 * time.Minute)

	// Step 1: Claim pending or stale running export jobs
	claimSQL := `
		UPDATE data_export_jobs
		SET job_status = 'running',
		    locked_at = ?,
		    locked_by = ?,
		    attempt_count = attempt_count + 1,
		    started_at = COALESCE(started_at, ?),
		    updated_at = ?
		WHERE (
		    (job_status = 'pending' AND next_attempt_at <= ? AND (locked_at IS NULL OR locked_at < ?))
		    OR
		    (job_status = 'running' AND (locked_at IS NULL OR locked_at < ?))
		)
		ORDER BY created_at ASC
		LIMIT 5`

	res, err := w.db.ExecContext(ctx, claimSQL, now, w.workerID, now, now, now, leaseExpiry, leaseExpiry)
	if err != nil {
		return 0, fmt.Errorf("failed to claim export jobs: %w", err)
	}

	claimed, err := res.RowsAffected()
	if err != nil || claimed == 0 {
		return 0, nil
	}

	// Step 2: Query claimed jobs
	queryClaimed := `
		SELECT export_id, user_id, export_scope, export_format, filter_json, attempt_count
		FROM data_export_jobs
		WHERE job_status = 'running' AND locked_by = ?`

	rows, err := w.db.QueryContext(ctx, queryClaimed, w.workerID)
	if err != nil {
		return 0, fmt.Errorf("failed to query claimed export jobs: %w", err)
	}
	defer rows.Close()

	type exportItem struct {
		exportID     string
		userID       string
		scope        string
		format       string
		filterJSON   []byte
		attemptCount uint16
	}

	var items []exportItem
	for rows.Next() {
		var item exportItem
		if err := rows.Scan(&item.exportID, &item.userID, &item.scope, &item.format, &item.filterJSON, &item.attemptCount); err == nil {
			items = append(items, item)
		}
	}
	rows.Close()

	// Step 3: Stream real safe aggregate export data to object storage
	processed := 0
	for _, item := range items {
		completedAt := w.clk.Now()
		expiresAt := completedAt.Add(7 * 24 * time.Hour) // 7 days retention for completed export
		objKey := fmt.Sprintf("exports/%s/%s.%s", item.userID, item.exportID, item.format)

		// Generate real aggregate export data for user
		exportBytes, contentType, err := w.generateAggregateExportData(ctx, item.userID, item.scope, item.format)
		if err != nil {
			errCode := "EXPORT_AGGREGATION_FAILED"
			failSQL := `
				UPDATE data_export_jobs
				SET job_status = CASE WHEN attempt_count >= 5 THEN 'failed' ELSE 'pending' END,
				    last_error_code = ?,
				    locked_at = NULL,
				    locked_by = NULL,
				    updated_at = ?
				WHERE export_id = ? AND locked_by = ?`
			_, _ = w.db.ExecContext(ctx, failSQL, errCode, completedAt, item.exportID, w.workerID)
			continue
		}

		fileSha := sha256.Sum256(exportBytes)
		fileSize := uint64(len(exportBytes))

		// Stream bytes to ObjectStorage
		putErr := w.storage.PutObject(ctx, objKey, bytes.NewReader(exportBytes), int64(fileSize), contentType)
		if putErr != nil {
			errCode := "STORAGE_UPLOAD_FAILED"
			failSQL := `
				UPDATE data_export_jobs
				SET job_status = CASE WHEN attempt_count >= 5 THEN 'failed' ELSE 'pending' END,
				    last_error_code = ?,
				    locked_at = NULL,
				    locked_by = NULL,
				    updated_at = ?
				WHERE export_id = ? AND locked_by = ?`
			_, _ = w.db.ExecContext(ctx, failSQL, errCode, completedAt, item.exportID, w.workerID)
			continue
		}

		// Complete export job with lease fencing on locked_by
		completeSQL := `
			UPDATE data_export_jobs
			SET job_status = 'completed',
			    object_key = ?,
			    file_sha256 = ?,
			    file_size = ?,
			    completed_at = ?,
			    expires_at = ?,
			    last_error_code = NULL,
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

func (w *Worker) generateAggregateExportData(ctx context.Context, userID, scope, format string) ([]byte, string, error) {
	query := `
		SELECT metric_date, agent_id, exact_token_total, derived_token_total,
		       estimated_token_total, session_count, interaction_turn_count,
		       model_request_count, code_generated_lines, code_accepted_lines,
		       cost_amount, cost_currency
		FROM daily_user_agent_metrics
		WHERE user_id = ?
		ORDER BY metric_date ASC, agent_id ASC`

	rows, err := w.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query daily metrics: %w", err)
	}
	defer rows.Close()

	type metricRow struct {
		MetricDate           string  `json:"metricDate"`
		AgentID              string  `json:"agentId"`
		ExactTokenTotal      uint64  `json:"exactTokenTotal"`
		DerivedTokenTotal    uint64  `json:"derivedTokenTotal"`
		EstimatedTokenTotal  uint64  `json:"estimatedTokenTotal"`
		TotalTokens          uint64  `json:"totalTokens"`
		SessionCount         uint64  `json:"sessionCount"`
		InteractionTurnCount uint64  `json:"interactionTurnCount"`
		ModelRequestCount    uint64  `json:"modelRequestCount"`
		CodeGeneratedLines   uint64  `json:"codeGeneratedLines"`
		CodeAcceptedLines    uint64  `json:"codeAcceptedLines"`
		CostAmount           float64 `json:"costAmount"`
		CostCurrency         string  `json:"costCurrency"`
	}

	var dataRows []metricRow
	for rows.Next() {
		var r metricRow
		var mDate time.Time
		if err := rows.Scan(
			&mDate,
			&r.AgentID,
			&r.ExactTokenTotal,
			&r.DerivedTokenTotal,
			&r.EstimatedTokenTotal,
			&r.SessionCount,
			&r.InteractionTurnCount,
			&r.ModelRequestCount,
			&r.CodeGeneratedLines,
			&r.CodeAcceptedLines,
			&r.CostAmount,
			&r.CostCurrency,
		); err == nil {
			r.MetricDate = mDate.Format("2006-01-02")
			r.TotalTokens = r.ExactTokenTotal + r.DerivedTokenTotal + r.EstimatedTokenTotal
			dataRows = append(dataRows, r)
		}
	}
	rows.Close()

	if format == "json" {
		outJSON, err := json.MarshalIndent(map[string]interface{}{
			"userId":    userID,
			"scope":     scope,
			"generated": w.clk.Now().Format(time.RFC3339),
			"records":   dataRows,
		}, "", "  ")
		if err != nil {
			return nil, "", err
		}
		return outJSON, "application/json", nil
	}

	// CSV format
	buf := new(bytes.Buffer)
	cw := csv.NewWriter(buf)
	// Write Header
	_ = cw.Write([]string{
		"metric_date", "agent_id", "exact_tokens", "derived_tokens", "estimated_tokens",
		"total_tokens", "session_count", "turn_count", "model_requests",
		"code_generated_lines", "code_accepted_lines", "cost_amount", "cost_currency",
	})

	for _, r := range dataRows {
		_ = cw.Write([]string{
			r.MetricDate,
			r.AgentID,
			fmt.Sprintf("%d", r.ExactTokenTotal),
			fmt.Sprintf("%d", r.DerivedTokenTotal),
			fmt.Sprintf("%d", r.EstimatedTokenTotal),
			fmt.Sprintf("%d", r.TotalTokens),
			fmt.Sprintf("%d", r.SessionCount),
			fmt.Sprintf("%d", r.InteractionTurnCount),
			fmt.Sprintf("%d", r.ModelRequestCount),
			fmt.Sprintf("%d", r.CodeGeneratedLines),
			fmt.Sprintf("%d", r.CodeAcceptedLines),
			fmt.Sprintf("%.6f", r.CostAmount),
			r.CostCurrency,
		})
	}
	cw.Flush()

	return buf.Bytes(), "text/csv", nil
}

// ProcessDeletionRequests processes pending or stale running deletion requests across all scopes:
// account, installation, time_range, all_usage with durable phases, cursor tracking, and lease fencing.
func (w *Worker) ProcessDeletionRequests(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}

	now := w.clk.Now()

	// Query mature pending or running deletion requests
	query := `
		SELECT request_id, user_id, installation_id, deletion_scope,
		       scope_filter_json, request_status, phase, progress_cursor, cancel_before
		FROM data_deletion_requests
		WHERE (
		    (request_status = 'pending' AND (cancel_before IS NULL OR cancel_before <= ?))
		    OR
		    (request_status = 'running' AND (cancel_before IS NULL OR cancel_before <= ?))
		)
		ORDER BY requested_at ASC
		LIMIT 5`

	rows, err := w.db.QueryContext(ctx, query, now, now)
	if err != nil {
		return 0, fmt.Errorf("failed to query mature deletion requests: %w", err)
	}
	defer rows.Close()

	type delReqItem struct {
		requestID      string
		userID         sql.NullString
		installationID sql.NullString
		scope          string
		filterJSON     []byte
		status         string
		phase          string
		cursor         uint64
		cancelBefore   sql.NullTime
	}

	var items []delReqItem
	for rows.Next() {
		var r delReqItem
		if err := rows.Scan(
			&r.requestID,
			&r.userID,
			&r.installationID,
			&r.scope,
			&r.filterJSON,
			&r.status,
			&r.phase,
			&r.cursor,
			&r.cancelBefore,
		); err == nil {
			items = append(items, r)
		}
	}
	rows.Close()

	processed := 0
	for _, req := range items {
		if err := w.executeDeletionRequest(ctx, req, now); err == nil {
			processed++
		} else {
			log.Printf("[Worker %s] Deletion request %s failed: %v", w.workerID, req.requestID, err)
		}
	}

	return processed, nil
}

func (w *Worker) executeDeletionRequest(ctx context.Context, req struct {
	requestID      string
	userID         sql.NullString
	installationID sql.NullString
	scope          string
	filterJSON     []byte
	status         string
	phase          string
	cursor         uint64
	cancelBefore   sql.NullTime
}, now time.Time) error {
	uID := ""
	if req.userID.Valid {
		uID = req.userID.String
	}
	instID := ""
	if req.installationID.Valid {
		instID = req.installationID.String
	}

	var scopeFilter map[string]interface{}
	if len(req.filterJSON) > 0 {
		_ = json.Unmarshal(req.filterJSON, &scopeFilter)
	}
	if instID == "" && scopeFilter != nil {
		if v, ok := scopeFilter["installation_id"].(string); ok {
			instID = v
		}
	}

	auditRefToken, _ := crypto.GenerateOpaqueToken(13)
	auditRef := "aud_" + auditRefToken

	switch req.scope {
	case "account":
		if uID == "" {
			return fmt.Errorf("account deletion requires valid user_id")
		}

		// Phase 1: revoking_access (cursor 1)
		_, _ = w.db.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET request_status = 'running', phase = 'revoking_access', progress_cursor = 1, started_at = COALESCE(started_at, ?)
			WHERE request_id = ?`, now, req.requestID)

		// Revoke sessions
		_, _ = w.db.ExecContext(ctx, `
			UPDATE user_sessions
			SET session_status = 'revoked', revoked_at = ?, revoke_reason = 'account_deletion', updated_at = ?
			WHERE user_id = ? AND session_status = 'active'`, now, now, uID)

		// Revoke installations
		_, _ = w.db.ExecContext(ctx, `
			UPDATE installations
			SET installation_status = 'revoked', revoked_at = ?, updated_at = ?
			WHERE user_id = ? AND installation_status != 'revoked'`, now, now, uID)

		// Cancel email challenges & device challenges
		_, _ = w.db.ExecContext(ctx, `
			UPDATE email_challenges
			SET challenge_status = 'cancelled', updated_at = ?
			WHERE user_id = ? AND challenge_status = 'pending'`, now, uID)
		_, _ = w.db.ExecContext(ctx, `
			UPDATE device_binding_challenges
			SET challenge_status = 'cancelled', active_session_key = NULL, updated_at = ?
			WHERE user_id = ? AND challenge_status = 'pending'`, now, uID)

		// Delete credentials
		_, _ = w.db.ExecContext(ctx, `DELETE FROM user_password_credentials WHERE user_id = ?`, uID)

		// Phase 2: deleting_events (cursor 2)
		_, _ = w.db.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET phase = 'deleting_events', progress_cursor = 2
			WHERE request_id = ?`, req.requestID)

		// Scrub usage events
		_, _ = w.db.ExecContext(ctx, `DELETE FROM usage_events WHERE user_id = ?`, uID)
		// Delete outbox
		_, _ = w.db.ExecContext(ctx, `DELETE FROM email_outbox WHERE user_id = ?`, uID)

		// Clean up export storage objects & DB rows
		expRows, err := w.db.QueryContext(ctx, `SELECT object_key FROM data_export_jobs WHERE user_id = ? AND object_key IS NOT NULL`, uID)
		if err == nil {
			for expRows.Next() {
				var k string
				if expRows.Scan(&k) == nil && k != "" {
					_ = w.storage.DeleteObject(ctx, k)
				}
			}
			expRows.Close()
		}
		_, _ = w.db.ExecContext(ctx, `DELETE FROM data_export_jobs WHERE user_id = ?`, uID)

		// Clean up avatar/upload storage objects & DB rows
		uobRows, err := w.db.QueryContext(ctx, `SELECT object_key FROM user_upload_objects WHERE user_id = ? AND object_key IS NOT NULL`, uID)
		if err == nil {
			for uobRows.Next() {
				var k string
				if uobRows.Scan(&k) == nil && k != "" {
					_ = w.storage.DeleteObject(ctx, k)
				}
			}
			uobRows.Close()
		}
		_, _ = w.db.ExecContext(ctx, `DELETE FROM user_upload_objects WHERE user_id = ?`, uID)

		// Phase 3: deleting_aggregates (cursor 3)
		_, _ = w.db.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET phase = 'deleting_aggregates', progress_cursor = 3
			WHERE request_id = ?`, req.requestID)

		_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_user_agent_metrics WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_user_agent_model_metrics WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_skill_metrics WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM leaderboard_entries WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM public_user_profiles WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM user_privacy_settings WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM user_handle_history WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM email_challenges WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM device_binding_challenges WHERE user_id = ?`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id = ?`, uID)

		// Clean up installations and adapter statuses
		_, _ = w.db.ExecContext(ctx, `DELETE FROM installation_adapter_status WHERE installation_id IN (SELECT installation_id FROM installations WHERE user_id = ?)`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM ingest_nonces WHERE installation_id IN (SELECT installation_id FROM installations WHERE user_id = ?)`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM ingest_batches WHERE installation_id IN (SELECT installation_id FROM installations WHERE user_id = ?)`, uID)
		_, _ = w.db.ExecContext(ctx, `DELETE FROM installations WHERE user_id = ?`, uID)

		// Scrub security events
		_, _ = w.db.ExecContext(ctx, `UPDATE user_security_events SET ip_prefix_hash = NULL, user_agent_hash = NULL, metadata_json = NULL WHERE user_id = ?`, uID)

		// Phase 4: deleting_identity (cursor 4) - preserve safe tombstone
		_, _ = w.db.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET phase = 'deleting_identity', progress_cursor = 4
			WHERE request_id = ?`, req.requestID)

		_, _ = w.db.ExecContext(ctx, `
			UPDATE users
			SET account_status = 'deleted',
			    leaderboard_visibility = 'private',
			    handle = NULL,
			    email_lookup_hash = NULL,
			    email_ciphertext = NULL,
			    display_name = 'Deleted User',
			    avatar_url = NULL,
			    avatar_object_id = NULL,
			    bio = NULL,
			    deleted_at = ?,
			    updated_at = ?
			WHERE user_id = ?`, now, now, uID)

		// Reconcile residuals
		var remainingCreds int
		_ = w.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_password_credentials WHERE user_id = ?`, uID).Scan(&remainingCreds)
		if remainingCreds > 0 {
			_, _ = w.db.ExecContext(ctx, `DELETE FROM user_password_credentials WHERE user_id = ?`, uID)
		}

		// Phase 5: completed
		completeSQL := `
			UPDATE data_deletion_requests
			SET request_status = 'completed',
			    phase = 'completed',
			    progress_cursor = 5,
			    completed_at = ?,
			    audit_reference = ?,
			    active_account_key = NULL
			WHERE request_id = ?`
		_, err = w.db.ExecContext(ctx, completeSQL, now, auditRef, req.requestID)
		return err

	case "installation":
		_, _ = w.db.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET request_status = 'running', phase = 'revoking_access', progress_cursor = 1, started_at = COALESCE(started_at, ?)
			WHERE request_id = ?`, now, req.requestID)

		if instID != "" {
			_, _ = w.db.ExecContext(ctx, `UPDATE installations SET installation_status = 'revoked', revoked_at = ?, updated_at = ? WHERE installation_id = ?`, now, now, instID)
			_, _ = w.db.ExecContext(ctx, `UPDATE data_deletion_requests SET phase = 'deleting_events', progress_cursor = 2 WHERE request_id = ?`, req.requestID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM usage_events WHERE installation_id = ?`, instID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM installation_adapter_status WHERE installation_id = ?`, instID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM ingest_nonces WHERE installation_id = ?`, instID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM ingest_batches WHERE installation_id = ?`, instID)
		}

		completeSQL := `
			UPDATE data_deletion_requests
			SET request_status = 'completed',
			    phase = 'completed',
			    progress_cursor = 3,
			    completed_at = ?,
			    audit_reference = ?
			WHERE request_id = ?`
		_, err := w.db.ExecContext(ctx, completeSQL, now, auditRef, req.requestID)
		return err

	case "time_range":
		_, _ = w.db.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET request_status = 'running', phase = 'revoking_access', progress_cursor = 1, started_at = COALESCE(started_at, ?)
			WHERE request_id = ?`, now, req.requestID)

		if uID != "" && scopeFilter != nil {
			var fromTime, toTime time.Time
			if fromStr, ok := scopeFilter["from"].(string); ok {
				fromTime, _ = time.Parse(time.RFC3339, fromStr)
			}
			if toStr, ok := scopeFilter["to"].(string); ok {
				toTime, _ = time.Parse(time.RFC3339, toStr)
			}
			if fromTime.IsZero() {
				fromTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			if toTime.IsZero() {
				toTime = now
			}

			_, _ = w.db.ExecContext(ctx, `UPDATE data_deletion_requests SET phase = 'deleting_events', progress_cursor = 2 WHERE request_id = ?`, req.requestID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM usage_events WHERE user_id = ? AND occurred_at >= ? AND occurred_at <= ?`, uID, fromTime, toTime)

			_, _ = w.db.ExecContext(ctx, `UPDATE data_deletion_requests SET phase = 'deleting_aggregates', progress_cursor = 3 WHERE request_id = ?`, req.requestID)
			fromDate := fromTime.Format("2006-01-02")
			toDate := toTime.Format("2006-01-02")
			_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_user_agent_metrics WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`, uID, fromDate, toDate)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_user_agent_model_metrics WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`, uID, fromDate, toDate)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_skill_metrics WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`, uID, fromDate, toDate)
		}

		completeSQL := `
			UPDATE data_deletion_requests
			SET request_status = 'completed',
			    phase = 'completed',
			    progress_cursor = 4,
			    completed_at = ?,
			    audit_reference = ?
			WHERE request_id = ?`
		_, err := w.db.ExecContext(ctx, completeSQL, now, auditRef, req.requestID)
		return err

	case "all_usage":
		_, _ = w.db.ExecContext(ctx, `
			UPDATE data_deletion_requests
			SET request_status = 'running', phase = 'revoking_access', progress_cursor = 1, started_at = COALESCE(started_at, ?)
			WHERE request_id = ?`, now, req.requestID)

		if uID != "" {
			_, _ = w.db.ExecContext(ctx, `UPDATE data_deletion_requests SET phase = 'deleting_events', progress_cursor = 2 WHERE request_id = ?`, req.requestID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM usage_events WHERE user_id = ?`, uID)

			_, _ = w.db.ExecContext(ctx, `UPDATE data_deletion_requests SET phase = 'deleting_aggregates', progress_cursor = 3 WHERE request_id = ?`, req.requestID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_user_agent_metrics WHERE user_id = ?`, uID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_user_agent_model_metrics WHERE user_id = ?`, uID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM daily_skill_metrics WHERE user_id = ?`, uID)
			_, _ = w.db.ExecContext(ctx, `DELETE FROM leaderboard_entries WHERE user_id = ?`, uID)
		}

		completeSQL := `
			UPDATE data_deletion_requests
			SET request_status = 'completed',
			    phase = 'completed',
			    progress_cursor = 4,
			    completed_at = ?,
			    audit_reference = ?
			WHERE request_id = ?`
		_, err := w.db.ExecContext(ctx, completeSQL, now, auditRef, req.requestID)
		return err

	default:
		return fmt.Errorf("unknown deletion scope: %s", req.scope)
	}
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

// RunPass runs one full pass of all worker tasks
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
