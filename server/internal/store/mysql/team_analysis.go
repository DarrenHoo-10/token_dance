package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

func (s *teamsStore) QueueTeamExportTx(ctx context.Context, in store.QueueTeamExportTxInput) (*domain.TeamExportJob, error) {
	var result *domain.TeamExportJob
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.Job.TeamID)
		if err != nil {
			return err
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
			return err
		} else if rec != nil {
			job, err := scanExport(tx.QueryRowContext(ctx, exportSelectSQL+` WHERE export_id = ?`, rec.ResultID))
			if err != nil {
				return errCommandResultUnavailable()
			}
			result = job
			return nil
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		mem, err := s.currentMembership(ctx, tx, in.Job.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		if !isManager(*team, mem) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		job := in.Job
		if job.ExportID == "" {
			id, err := newTeamID(domain.ExportIDPrefix)
			if err != nil {
				return err
			}
			job.ExportID = id
		}
		job.RequesterUserID = in.ActorUserID
		job.RequesterMembershipID = mem.MembershipID
		job.AuthRevision = team.AuthRevision
		job.Status = domain.TeamExportQueued
		job.CreatedAt = now
		if job.ExpiresAt.IsZero() {
			job.ExpiresAt = now.Add(30 * 24 * time.Hour)
		}
		if job.NextAttemptAt.IsZero() {
			job.NextAttemptAt = now
		}
		if job.FilterJSON == "" {
			job.FilterJSON = "{}"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO team_export_jobs (
				export_id, team_id, requester_user_id, requester_membership_id, snapshot_id, auth_revision,
				export_kind, filter_json, status, attempt_count, next_attempt_at, created_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, CAST(? AS JSON), 'queued', 0, ?, ?, ?)`,
			job.ExportID, job.TeamID, job.RequesterUserID, job.RequesterMembershipID, job.SnapshotID, job.AuthRevision,
			job.Kind, job.FilterJSON, job.NextAttemptAt, job.CreatedAt, job.ExpiresAt,
		); err != nil {
			return fmt.Errorf("queue team export: %w", err)
		}
		if err := s.insertAudit(ctx, tx, job.TeamID, in.ActorUserID, "export_create", "export", job.ExportID, map[string]any{
			"kind": string(job.Kind),
		}, now); err != nil {
			return err
		}
		if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "export", job.ExportID, now); err != nil {
			return err
		}
		result = &job
		return nil
	})
	return result, err
}

func (s *teamsStore) CreateTeamAvatarUploadIntent(ctx context.Context, obj domain.TeamUploadObject) (*domain.TeamUploadObject, error) {
	if obj.Status == "" {
		obj.Status = "pending"
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO team_upload_objects (
			object_id, team_id, uploader_user_id, object_key, content_type, byte_size,
			image_width, image_height, sha256, status, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		obj.ObjectID, obj.TeamID, obj.UploaderID, obj.ObjectKey, obj.ContentType, obj.ByteSize,
		obj.ImageWidth, obj.ImageHeight, obj.SHA256[:], obj.Status, nullTimeFromPtr(obj.ExpiresAt),
	); err != nil {
		return nil, fmt.Errorf("create team avatar intent: %w", err)
	}
	return &obj, nil
}

func (s *teamsStore) CompleteTeamAvatarUpload(ctx context.Context, teamID, objectID, actorUserID string, meta store.AvatarReadyMeta, expectedProfileVersion uint64, now time.Time) (*domain.Team, error) {
	var result *domain.Team
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, actorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, teamID)
		if err != nil {
			return err
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		if team.ProfileVersion != expectedProfileVersion {
			return errVersionConflict()
		}
		mem, err := s.currentMembership(ctx, tx, teamID, actorUserID)
		if err != nil {
			return err
		}
		if !isManager(*team, mem) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		obj, err := scanUploadObject(tx.QueryRowContext(ctx, uploadSelectSQL+` WHERE object_id = ? AND team_id = ? FOR UPDATE`, objectID, teamID))
		if errors.Is(err, sql.ErrNoRows) {
			return errResourceNotFound()
		}
		if err != nil {
			return err
		}
		if obj.UploaderID != actorUserID {
			return errPermissionDenied()
		}
		tNow, err := s.txNow(ctx, tx, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_upload_objects
			SET status = 'ready', byte_size = ?, sha256 = ?, image_width = ?, image_height = ?, content_type = ?
			WHERE object_id = ?`,
			meta.ByteSize, meta.ContentSha256[:], meta.ImageWidth, meta.ImageHeight, meta.ContentType, objectID,
		); err != nil {
			return fmt.Errorf("complete team avatar object: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE teams
			SET avatar_object_id = ?, profile_version = profile_version + 1
			WHERE team_id = ? AND profile_version = ?`,
			objectID, teamID, expectedProfileVersion,
		); err != nil {
			return fmt.Errorf("attach team avatar: %w", err)
		}
		if err := s.insertAudit(ctx, tx, teamID, actorUserID, "avatar_change", "object", objectID, map[string]any{
			"result": "completed",
		}, tNow); err != nil {
			return err
		}
		result, err = s.loadTeam(ctx, tx, teamID)
		return err
	})
	return result, err
}

func (s *teamsStore) ClearTeamAvatar(ctx context.Context, teamID, actorUserID string, expectedProfileVersion uint64, now time.Time) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, actorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, teamID)
		if err != nil {
			return err
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		if team.ProfileVersion != expectedProfileVersion {
			return errVersionConflict()
		}
		mem, err := s.currentMembership(ctx, tx, teamID, actorUserID)
		if err != nil {
			return err
		}
		if !isManager(*team, mem) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		tNow, err := s.txNow(ctx, tx, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE teams
			SET avatar_object_id = NULL, profile_version = profile_version + 1
			WHERE team_id = ? AND profile_version = ?`, teamID, expectedProfileVersion); err != nil {
			return fmt.Errorf("clear team avatar: %w", err)
		}
		return s.insertAudit(ctx, tx, teamID, actorUserID, "avatar_change", "team", teamID, map[string]any{
			"result": "cleared",
		}, tNow)
	})
}

func (s *teamsStore) GetOrQueueAnalysis(ctx context.Context, teamID string, from, toExclusive time.Time, authRevision uint64, ruleVersion string, now time.Time) (*domain.TeamAnalysisSnapshot, bool, error) {
	var snap *domain.TeamAnalysisSnapshot
	var queued bool
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		team, err := s.lockTeam(ctx, tx, teamID)
		if err != nil {
			return err
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		tNow, err := s.txNow(ctx, tx, now)
		if err != nil {
			return err
		}
		fromDate := domain.FormatTeamCalendarDate(from, team.TimezoneName)
		toDate := domain.FormatTeamCalendarDate(toExclusive, team.TimezoneName)
		ready, err := scanSnapshot(tx.QueryRowContext(ctx, snapshotSelectSQL+`
			WHERE team_id = ? AND from_date = ? AND to_date_exclusive = ? AND auth_revision = ? AND rule_version = ? AND status = 'ready'
			ORDER BY as_of DESC
			LIMIT 1`, teamID, fromDate, toDate, authRevision, ruleVersion))
		if err == nil && tNow.Sub(ready.AsOf) <= 30*time.Second {
			snap = ready
			queued = false
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load ready snapshot: %w", err)
		}
		key := analysisRequestKey(teamID, from, toExclusive, authRevision, ruleVersion, team.TimezoneName)
		existing, err := scanSnapshot(tx.QueryRowContext(ctx, snapshotSelectSQL+` WHERE active_request_key = ? FOR UPDATE`, key))
		if err == nil {
			snap = existing
			queued = existing.Status == domain.SnapshotQueued || existing.Status == domain.SnapshotBuilding
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load queued snapshot: %w", err)
		}
		id, err := newTeamID(domain.SnapshotIDPrefix)
		if err != nil {
			return err
		}
		var source uint64
		if err := tx.QueryRowContext(ctx, `SELECT source_revision FROM team_source_revisions WHERE team_id = ?`, teamID).Scan(&source); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load source revision: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO team_analysis_snapshots (
				snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision, rule_version,
				status, active_request_key, as_of, next_attempt_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?)`,
			id, teamID, fromDate, toDate,
			authRevision, source, ruleVersion, key, tNow, tNow, tNow.Add(30*time.Minute),
		); err != nil {
			if isDuplicateKey(err) {
				existing, err := scanSnapshot(tx.QueryRowContext(ctx, snapshotSelectSQL+` WHERE active_request_key = ?`, key))
				if err != nil {
					return err
				}
				snap = existing
				queued = true
				return nil
			}
			return fmt.Errorf("queue analysis snapshot: %w", err)
		}
		created, err := scanSnapshot(tx.QueryRowContext(ctx, snapshotSelectSQL+` WHERE snapshot_id = ?`, id))
		if err != nil {
			return err
		}
		snap = created
		queued = true
		return nil
	})
	return snap, queued, err
}

func (s *teamsStore) GetReadySnapshot(ctx context.Context, teamID, snapshotID string) (*domain.TeamAnalysisSnapshot, error) {
	snap, err := scanSnapshot(s.db.QueryRowContext(ctx, snapshotSelectSQL+` WHERE snapshot_id = ? AND team_id = ? AND status = 'ready'`, snapshotID, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errResourceNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get ready snapshot: %w", err)
	}
	return snap, nil
}

func (s *teamsStore) ListAnalysisRows(ctx context.Context, snapshotID string, generation uint64) ([]domain.TeamAnalysisRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT snapshot_id, build_generation, row_key, membership_id, metric_date, visibility_mask,
		       agent_id, provider_id, model_id, currency, token_exact_total, token_derived_total,
		       usage_event_count, token_supported_event_count, reported_cost_amount, estimated_cost_amount,
		       reported_cost_event_count, estimated_cost_event_count, reported_covered_usage_count,
		       estimated_covered_usage_count, unattributed_cost_count, max_received_at
		FROM team_analysis_rows
		WHERE snapshot_id = ? AND build_generation = ?`, snapshotID, generation)
	if err != nil {
		return nil, fmt.Errorf("list analysis rows: %w", err)
	}
	defer rows.Close()
	var out []domain.TeamAnalysisRow
	for rows.Next() {
		var row domain.TeamAnalysisRow
		var membership, metricDate, agent, provider, model, currency sql.NullString
		var maxRecv sql.NullTime
		if err := rows.Scan(
			&row.SnapshotID, &row.BuildGeneration, &row.RowKey, &membership, &metricDate, &row.VisibilityMask,
			&agent, &provider, &model, &currency, &row.TokenExactTotal, &row.TokenDerivedTotal,
			&row.UsageEventCount, &row.TokenSupportedEventCount, &row.ReportedCostAmount, &row.EstimatedCostAmount,
			&row.ReportedCostEventCount, &row.EstimatedCostEventCount, &row.ReportedCoveredUsageCount,
			&row.EstimatedCoveredUsageCount, &row.UnattributedCostCount, &maxRecv,
		); err != nil {
			return nil, err
		}
		row.MembershipID = ptrFromNullString(membership)
		row.MetricDate = ptrFromNullString(metricDate)
		row.AgentID = ptrFromNullString(agent)
		row.ProviderID = ptrFromNullString(provider)
		row.ModelID = ptrFromNullString(model)
		row.Currency = ptrFromNullString(currency)
		row.MaxReceivedAt = ptrFromNullTime(maxRecv)
		out = append(out, row)
	}
	if out == nil {
		out = []domain.TeamAnalysisRow{}
	}
	return out, rows.Err()
}

func (s *teamsStore) ClaimAnalysis(ctx context.Context, workerID string, lease time.Duration, now time.Time) (*domain.TeamAnalysisSnapshot, error) {
	var result *domain.TeamAnalysisSnapshot
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		tNow := now
		if tNow.IsZero() {
			var err error
			tNow, err = s.txNow(ctx, tx, now)
			if err != nil {
				return err
			}
		}
		snap, err := scanSnapshot(tx.QueryRowContext(ctx, snapshotSelectSQL+`
			WHERE status IN ('queued', 'building')
			  AND next_attempt_at <= ?
			  AND (lease_expires_at IS NULL OR lease_expires_at < ?)
			ORDER BY next_attempt_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED`, tNow, tNow))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim analysis: %w", err)
		}
		token := workerID
		if token == "" {
			id, err := newTeamID("twk_")
			if err != nil {
				return err
			}
			token = id
		}
		gen := snap.LeaseGeneration + 1
		expires := tNow.Add(lease)
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_analysis_snapshots
			SET status = 'building', lease_token = ?, lease_generation = ?, lease_expires_at = ?,
			    attempt_count = attempt_count + 1, next_attempt_at = ?
			WHERE snapshot_id = ?`,
			token, gen, expires, expires, snap.SnapshotID,
		); err != nil {
			return fmt.Errorf("take analysis lease: %w", err)
		}
		updated, err := scanSnapshot(tx.QueryRowContext(ctx, snapshotSelectSQL+` WHERE snapshot_id = ?`, snap.SnapshotID))
		if err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *teamsStore) PublishAnalysis(ctx context.Context, snapshotID, leaseToken string, leaseGeneration, capturedAuth, capturedSource, publishedGeneration uint64, now time.Time) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		var teamID string
		if err := tx.QueryRowContext(ctx, `SELECT team_id FROM team_analysis_snapshots WHERE snapshot_id = ?`, snapshotID).Scan(&teamID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errResourceNotFound()
			}
			return err
		}
		team, err := s.lockTeam(ctx, tx, teamID)
		if err != nil {
			return err
		}
		snap, err := scanSnapshot(tx.QueryRowContext(ctx, snapshotSelectSQL+` WHERE snapshot_id = ? FOR UPDATE`, snapshotID))
		if errors.Is(err, sql.ErrNoRows) {
			return errResourceNotFound()
		}
		if err != nil {
			return err
		}
		tNow, err := s.txNow(ctx, tx, now)
		if err != nil {
			return err
		}
		if team.Status != domain.TeamStatusActive || team.AuthRevision != capturedAuth {
			if _, err := tx.ExecContext(ctx, `
				UPDATE team_analysis_snapshots
				SET status = 'obsolete', active_request_key = NULL
				WHERE snapshot_id = ?`, snapshotID); err != nil {
				return err
			}
			return errVersionConflict()
		}
		var barrier int
		err = tx.QueryRowContext(ctx, `
			SELECT 1 FROM team_deletion_barriers
			WHERE team_id = ? AND released_at IS NULL
			LIMIT 1`, teamID).Scan(&barrier)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			return errTeamUnavailable(fmt.Errorf("deletion barrier open"))
		}
		if snap.LeaseToken == nil || *snap.LeaseToken != leaseToken || snap.LeaseGeneration != leaseGeneration {
			return errVersionConflict()
		}
		if snap.LeaseExpiresAt != nil && !snap.LeaseExpiresAt.After(tNow) {
			return errVersionConflict()
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_analysis_snapshots
			SET status = 'ready', active_request_key = NULL, source_revision = ?, published_generation = ?, as_of = ?
			WHERE snapshot_id = ?`, capturedSource, publishedGeneration, tNow, snapshotID); err != nil {
			return fmt.Errorf("publish analysis: %w", err)
		}
		return nil
	})
}

func (s *teamsStore) MarkAnalysisFailed(ctx context.Context, snapshotID, leaseToken string, leaseGeneration uint64, errorCode string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE team_analysis_snapshots
		SET status = 'failed', active_request_key = NULL, error_code = ?
		WHERE snapshot_id = ? AND lease_token = ? AND lease_generation = ?`,
		errorCode, snapshotID, leaseToken, leaseGeneration,
	)
	if err != nil {
		return fmt.Errorf("mark analysis failed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errResourceNotFound()
	}
	return nil
}

func (s *teamsStore) BumpSourceRevision(ctx context.Context, teamID string, now time.Time) (uint64, error) {
	var rev uint64
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockTeam(ctx, tx, teamID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO team_source_revisions (team_id, source_revision, changed_at)
			VALUES (?, 1, ?)
			ON DUPLICATE KEY UPDATE source_revision = source_revision + 1, changed_at = VALUES(changed_at)`,
			teamID, now,
		); err != nil {
			return fmt.Errorf("bump source revision: %w", err)
		}
		return tx.QueryRowContext(ctx, `SELECT source_revision FROM team_source_revisions WHERE team_id = ?`, teamID).Scan(&rev)
	})
	return rev, err
}

func (s *teamsStore) RegisterDeletionBarrier(ctx context.Context, deletionRequestID, teamID string, now time.Time) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockTeam(ctx, tx, teamID); err != nil {
			return err
		}
		if err := s.upsertBarrier(ctx, tx, deletionRequestID, teamID, now); err != nil {
			return err
		}
		_, err := s.bumpAuth(ctx, tx, teamID)
		return err
	})
}

func (s *teamsStore) ReleaseDeletionBarrier(ctx context.Context, deletionRequestID, teamID string, now time.Time) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockTeam(ctx, tx, teamID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_deletion_barriers
			SET released_at = ?
			WHERE deletion_request_id = ? AND team_id = ? AND released_at IS NULL`,
			now, deletionRequestID, teamID,
		); err != nil {
			return fmt.Errorf("release deletion barrier: %w", err)
		}
		_, err := s.bumpAuth(ctx, tx, teamID)
		return err
	})
}

func (s *teamsStore) ClaimTeamExport(ctx context.Context, workerID string, lease time.Duration, now time.Time) (*domain.TeamExportJob, error) {
	var result *domain.TeamExportJob
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		tNow := now
		if tNow.IsZero() {
			var err error
			tNow, err = s.txNow(ctx, tx, now)
			if err != nil {
				return err
			}
		}
		job, err := scanExport(tx.QueryRowContext(ctx, exportSelectSQL+`
			WHERE status IN ('queued', 'running')
			  AND next_attempt_at <= ?
			  AND (lease_expires_at IS NULL OR lease_expires_at < ?)
			ORDER BY next_attempt_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED`, tNow, tNow))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim team export: %w", err)
		}
		token := workerID
		if token == "" {
			id, err := newTeamID("twk_")
			if err != nil {
				return err
			}
			token = id
		}
		gen := job.LeaseGeneration + 1
		expires := tNow.Add(lease)
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_export_jobs
			SET status = 'running', lease_token = ?, lease_generation = ?, lease_expires_at = ?,
			    attempt_count = attempt_count + 1, next_attempt_at = ?
			WHERE export_id = ?`,
			token, gen, expires, expires, job.ExportID,
		); err != nil {
			return fmt.Errorf("take export lease: %w", err)
		}
		updated, err := scanExport(tx.QueryRowContext(ctx, exportSelectSQL+` WHERE export_id = ?`, job.ExportID))
		if err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *teamsStore) CompleteTeamExport(ctx context.Context, exportID, leaseToken string, leaseGeneration uint64, objectKey string, sha256 [32]byte, size uint64, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE team_export_jobs
		SET status = 'completed', object_key = ?, file_sha256 = ?, file_size = ?, lease_token = NULL, lease_expires_at = NULL
		WHERE export_id = ? AND lease_token = ? AND lease_generation = ? AND status = 'running'`,
		objectKey, sha256[:], size, exportID, leaseToken, leaseGeneration,
	)
	if err != nil {
		return fmt.Errorf("complete team export: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errResourceNotFound()
	}
	return nil
}

func (s *teamsStore) FailTeamExport(ctx context.Context, exportID, leaseToken string, leaseGeneration uint64, errorCode string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE team_export_jobs
		SET status = 'failed', error_code = ?
		WHERE export_id = ? AND lease_token = ? AND lease_generation = ?`,
		errorCode, exportID, leaseToken, leaseGeneration,
	)
	if err != nil {
		return fmt.Errorf("fail team export: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errResourceNotFound()
	}
	return nil
}
