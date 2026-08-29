package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store/sqlcgen"
)

type exportStore struct {
	db *sql.DB
}

func exportJobFromSQLC(row sqlcgen.DataExportJob) domain.DataExportJob {
	job := domain.DataExportJob{
		ExportID:       row.ExportID,
		UserID:         row.UserID,
		IdempotencyKey: row.IdempotencyKey,
		RequestHash:    scanBytes32(row.RequestHash),
		ExportScope:    row.ExportScope,
		ExportFormat:   row.ExportFormat,
		JobStatus:      domain.ExportJobStatus(row.JobStatus),
		AttemptCount:   row.AttemptCount,
		NextAttemptAt:  row.NextAttemptAt,
		LockedAt:       ptrFromNullTime(row.LockedAt),
		LockedBy:       ptrFromNullString(row.LockedBy),
		ObjectKey:      ptrFromNullString(row.ObjectKey),
		LastErrorCode:  ptrFromNullString(row.LastErrorCode),
		StartedAt:      ptrFromNullTime(row.StartedAt),
		CompletedAt:    ptrFromNullTime(row.CompletedAt),
		ExpiresAt:      ptrFromNullTime(row.ExpiresAt),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if len(row.FilterJson) > 0 {
		_ = json.Unmarshal(row.FilterJson, &job.FilterJSON)
	}
	if row.FileSha256.Valid {
		job.FileSha256 = scanBytes32Ptr([]byte(row.FileSha256.String))
	}
	if row.FileSize.Valid {
		size := uint64(row.FileSize.Int64)
		job.FileSize = &size
	}
	return job
}

func (s *exportStore) CreateJob(ctx context.Context, job domain.DataExportJob, idempotencyKeys []string) (*domain.DataExportJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin export job tx: %w", err)
	}
	defer tx.Rollback()

	var lockedUser string
	if err := tx.QueryRowContext(ctx, "SELECT user_id FROM users WHERE user_id = ? FOR UPDATE", job.UserID).Scan(&lockedUser); err != nil {
		return nil, fmt.Errorf("failed to lock export job user: %w", err)
	}
	candidateJSON, err := json.Marshal(idempotencyKeys)
	if err != nil {
		return nil, fmt.Errorf("failed to encode export idempotency candidates: %w", err)
	}

	queryExisting := `
		SELECT export_id, user_id, idempotency_key, request_hash, export_scope,
		       export_format, filter_json, job_status, attempt_count, next_attempt_at,
		       locked_at, locked_by, object_key, file_sha256, file_size,
		       last_error_code, started_at, completed_at, expires_at, created_at, updated_at
		FROM data_export_jobs
		WHERE user_id = ?
		  AND idempotency_key IN (
		    SELECT candidate_key
		    FROM JSON_TABLE(?, '$[*]' COLUMNS(candidate_key VARCHAR(64) PATH '$')) candidates
		  )
		LIMIT 1`

	var existing domain.DataExportJob
	var reqHash, fileSha []byte
	var filterJSON []byte
	var lockedBy, objKey, lastErrCode sql.NullString
	var fileSize sql.NullInt64
	var lockedAt, startedAt, completedAt, expiresAt sql.NullTime

	err = tx.QueryRowContext(ctx, queryExisting, job.UserID, candidateJSON).Scan(
		&existing.ExportID,
		&existing.UserID,
		&existing.IdempotencyKey,
		&reqHash,
		&existing.ExportScope,
		&existing.ExportFormat,
		&filterJSON,
		&existing.JobStatus,
		&existing.AttemptCount,
		&existing.NextAttemptAt,
		&lockedAt,
		&lockedBy,
		&objKey,
		&fileSha,
		&fileSize,
		&lastErrCode,
		&startedAt,
		&completedAt,
		&expiresAt,
		&existing.CreatedAt,
		&existing.UpdatedAt,
	)

	if err == nil {
		existing.RequestHash = scanBytes32(reqHash)
		if existing.RequestHash == job.RequestHash {
			existing.FileSha256 = scanBytes32Ptr(fileSha)
			existing.LockedBy = ptrFromNullString(lockedBy)
			existing.ObjectKey = ptrFromNullString(objKey)
			existing.LastErrorCode = ptrFromNullString(lastErrCode)
			existing.LockedAt = ptrFromNullTime(lockedAt)
			existing.StartedAt = ptrFromNullTime(startedAt)
			existing.CompletedAt = ptrFromNullTime(completedAt)
			existing.ExpiresAt = ptrFromNullTime(expiresAt)
			if fileSize.Valid {
				sz := uint64(fileSize.Int64)
				existing.FileSize = &sz
			}
			if len(filterJSON) > 0 {
				_ = json.Unmarshal(filterJSON, &existing.FilterJSON)
			}
			return &existing, nil
		}
		return nil, domain.ErrIdempotencyReused
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query existing export job: %w", err)
	}

	var fJSON []byte
	if len(job.FilterJSON) > 0 {
		fJSON, _ = json.Marshal(job.FilterJSON)
	} else {
		fJSON = []byte("{}")
	}

	var fsNull sql.NullInt64
	if job.FileSize != nil {
		fsNull = sql.NullInt64{Int64: int64(*job.FileSize), Valid: true}
	}

	insertSQL := `
		INSERT INTO data_export_jobs (
			export_id, user_id, idempotency_key, request_hash, export_scope,
			export_format, filter_json, job_status, attempt_count, next_attempt_at,
			locked_at, locked_by, object_key, file_sha256, file_size,
			last_error_code, started_at, completed_at, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertSQL,
		job.ExportID,
		job.UserID,
		job.IdempotencyKey,
		bytes32Slice(job.RequestHash),
		job.ExportScope,
		job.ExportFormat,
		fJSON,
		job.JobStatus,
		job.AttemptCount,
		job.NextAttemptAt,
		nullTimeFromPtr(job.LockedAt),
		nullStringFromPtr(job.LockedBy),
		nullStringFromPtr(job.ObjectKey),
		bytes32PtrSlice(job.FileSha256),
		fsNull,
		nullStringFromPtr(job.LastErrorCode),
		nullTimeFromPtr(job.StartedAt),
		nullTimeFromPtr(job.CompletedAt),
		nullTimeFromPtr(job.ExpiresAt),
		job.CreatedAt,
		job.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert export job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit export job tx: %w", err)
	}

	jCopy := job
	return &jCopy, nil
}

func (s *exportStore) ListJobs(ctx context.Context, userID string) ([]domain.DataExportJob, error) {
	rows, err := sqlcgen.New(s.db).ListExportJobsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list export jobs: %w", err)
	}
	list := make([]domain.DataExportJob, 0, len(rows))
	for _, row := range rows {
		list = append(list, exportJobFromSQLC(row))
	}
	return list, nil
}

func (s *exportStore) GetJob(ctx context.Context, exportID, userID string) (*domain.DataExportJob, error) {
	row, err := sqlcgen.New(s.db).GetExportJobByOwner(ctx, sqlcgen.GetExportJobByOwnerParams{
		ExportID: exportID,
		UserID:   userID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get export job: %w", err)
	}
	job := exportJobFromSQLC(row)
	return &job, nil
}

func (s *exportStore) ClaimPendingJob(ctx context.Context, workerID string, leaseDuration time.Duration, now time.Time) (*domain.DataExportJob, error) {
	leaseExpiry := now.Add(-leaseDuration)
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
		ORDER BY created_at ASC
		LIMIT 1`

	res, err := s.db.ExecContext(ctx, claimSQL, now, workerID, now, now, now, leaseExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to claim export job: %w", err)
	}

	aff, err := res.RowsAffected()
	if err != nil || aff == 0 {
		return nil, domain.ErrNotFound
	}

	query := `
		SELECT export_id, user_id, idempotency_key, request_hash, export_scope,
		       export_format, filter_json, job_status, attempt_count, next_attempt_at,
		       locked_at, locked_by, object_key, file_sha256, file_size,
		       last_error_code, started_at, completed_at, expires_at, created_at, updated_at
		FROM data_export_jobs
		WHERE job_status = 'running' AND locked_by = ?
		ORDER BY locked_at DESC
		LIMIT 1`

	var j domain.DataExportJob
	var reqHash, fileSha []byte
	var filterJSON []byte
	var lockedBy, objKey, lastErrCode sql.NullString
	var fileSize sql.NullInt64
	var lockedAt, startedAt, completedAt, expiresAt sql.NullTime

	err = s.db.QueryRowContext(ctx, query, workerID).Scan(
		&j.ExportID,
		&j.UserID,
		&j.IdempotencyKey,
		&reqHash,
		&j.ExportScope,
		&j.ExportFormat,
		&filterJSON,
		&j.JobStatus,
		&j.AttemptCount,
		&j.NextAttemptAt,
		&lockedAt,
		&lockedBy,
		&objKey,
		&fileSha,
		&fileSize,
		&lastErrCode,
		&startedAt,
		&completedAt,
		&expiresAt,
		&j.CreatedAt,
		&j.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch claimed job: %w", err)
	}

	j.RequestHash = scanBytes32(reqHash)
	j.FileSha256 = scanBytes32Ptr(fileSha)
	j.LockedBy = ptrFromNullString(lockedBy)
	j.ObjectKey = ptrFromNullString(objKey)
	j.LastErrorCode = ptrFromNullString(lastErrCode)
	j.LockedAt = ptrFromNullTime(lockedAt)
	j.StartedAt = ptrFromNullTime(startedAt)
	j.CompletedAt = ptrFromNullTime(completedAt)
	j.ExpiresAt = ptrFromNullTime(expiresAt)
	if fileSize.Valid {
		sz := uint64(fileSize.Int64)
		j.FileSize = &sz
	}
	if len(filterJSON) > 0 {
		_ = json.Unmarshal(filterJSON, &j.FilterJSON)
	}

	return &j, nil
}

func (s *exportStore) CompleteJob(ctx context.Context, exportID string, workerID string, objectKey string, fileSha256 [32]byte, fileSize uint64, now time.Time) error {
	rows, err := sqlcgen.New(s.db).CompleteExportJob(ctx, sqlcgen.CompleteExportJobParams{
		ObjectKey:   sql.NullString{String: objectKey, Valid: true},
		FileSha256:  sql.NullString{String: string(fileSha256[:]), Valid: true},
		FileSize:    sql.NullInt64{Int64: int64(fileSize), Valid: true},
		CompletedAt: sql.NullTime{Time: now, Valid: true},
		ExpiresAt:   sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true},
		UpdatedAt:   now,
		ExportID:    exportID,
		LockedBy:    sql.NullString{String: workerID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to complete export job: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *exportStore) FailJob(ctx context.Context, exportID string, workerID string, lastError string, now time.Time) error {
	query := `
		UPDATE data_export_jobs
		SET job_status = CASE WHEN attempt_count >= 5 THEN 'failed' ELSE 'pending' END,
		    last_error_code = ?,
		    next_attempt_at = DATE_ADD(?, INTERVAL 1 MINUTE),
		    locked_at = NULL,
		    locked_by = NULL,
		    updated_at = ?
		WHERE export_id = ? AND (locked_by = ? OR locked_by IS NULL)`

	res, err := s.db.ExecContext(ctx, query, lastError, now, now, exportID, workerID)
	if err != nil {
		return fmt.Errorf("failed to fail export job: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}
