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
	"tokendance/internal/pricing"
	"tokendance/internal/provider"
)

type Worker struct {
	pricing       *pricing.Client
	priceCursor   uint64
	priceRetry    time.Time
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

	now := w.clk.Now().Truncate(time.Millisecond)
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
		    (delivery_status = 'pending' AND next_attempt_at <= ? AND (locked_at IS NULL OR locked_at <= ?))
		    OR
		    (delivery_status = 'sending' AND (locked_at IS NULL OR locked_at <= ?))
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
		); err != nil {
			log.Printf("[Worker %s] scan outbox row error: %v", w.workerID, err)
			continue
		}
		items = append(items, item)
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
				    payload_ciphertext = '',
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

// ProcessRankingOutbox claims ranking publish tasks. Redis apply is Phase D;
// without Redis the tasks stay pending so they are not dropped.
func (w *Worker) ProcessRankingOutbox(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	return 0, nil
}

// RunPass runs one full pass of all worker tasks
func (w *Worker) RunPass(ctx context.Context) {
	if _, err := w.ProcessPrices(ctx); err != nil {
		log.Printf("[Worker] Price estimation error: %v", err)
	}
	if _, err := w.ProcessAggregates(ctx); err != nil {
		log.Printf("[Worker %s] Aggregate processing error: %v", w.workerID, err)
	}
	if _, err := w.ProcessRankingOutbox(ctx); err != nil {
		log.Printf("[Worker %s] Ranking outbox processing error: %v", w.workerID, err)
	}
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
