package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/provider"
)

const (
	teamSnapshotReadyTTL          = 30 * time.Minute
	teamTerminalCipherRetention   = 30 * 24 * time.Hour
	teamInviteJoinRetention       = 30 * 24 * time.Hour
	teamCleanupBatch              = 200
	teamExportMetaRetention       = 30 * 24 * time.Hour
)

func (w *Worker) ProcessTeamCleanup(ctx context.Context) error {
	if w.db == nil {
		return nil
	}
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	steps := []func(context.Context, time.Time) error{
		w.expireTeamInvitations,
		w.scrubExpiredInvitationSecrets,
		w.expireTeamSnapshots,
		w.expireTeamExportJobs,
		w.deleteExpiredTeamExportObjects,
		w.deleteObsoleteTeamSnapshots,
		w.cleanupTeamCommandReceipts,
		w.scrubInviteLinkSecrets,
		w.cleanupInviteLinkJoins,
		w.expireTeamUploadObjects,
	}
	var firstErr error
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := step(ctx, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (w *Worker) expireTeamInvitations(ctx context.Context, now time.Time) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE team_invitations
		SET status = 'expired', active_recipient_hash = NULL, version = version + 1
		WHERE status = 'pending' AND expires_at <= ?`, now)
	if err != nil {
		return fmt.Errorf("expire team invitations: %w", err)
	}
	return nil
}

func (w *Worker) scrubExpiredInvitationSecrets(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-teamTerminalCipherRetention)
	if _, err := w.db.ExecContext(ctx, `
		UPDATE team_invitations
		SET recipient_ciphertext = x''
		WHERE status IN ('accepted', 'revoked', 'expired')
		  AND expires_at <= ?
		  AND LENGTH(recipient_ciphertext) > 0`, cutoff); err != nil {
		return fmt.Errorf("scrub invitation ciphertext: %w", err)
	}
	if _, err := w.db.ExecContext(ctx, `
		UPDATE email_outbox
		SET payload_ciphertext = ''
		WHERE team_invitation_id IS NOT NULL
		  AND delivery_status IN ('sent', 'failed', 'cancelled')
		  AND updated_at <= ?
		  AND payload_ciphertext <> ''`, cutoff); err != nil {
		return fmt.Errorf("scrub invitation email payloads: %w", err)
	}
	return nil
}

func (w *Worker) expireTeamSnapshots(ctx context.Context, now time.Time) error {
	if _, err := w.db.ExecContext(ctx, `
		UPDATE team_analysis_snapshots s
		SET s.status = 'expired',
		    s.active_request_key = NULL,
		    s.lease_token = NULL,
		    s.lease_expires_at = NULL
		WHERE s.status = 'ready'
		  AND s.expires_at <= ?
		  AND NOT EXISTS (
		      SELECT 1 FROM team_export_jobs e
		      WHERE e.snapshot_id = s.snapshot_id
		        AND e.status IN ('queued', 'running')
		  )`, now); err != nil {
		return fmt.Errorf("expire ready team snapshots: %w", err)
	}
	if _, err := w.db.ExecContext(ctx, `
		UPDATE team_analysis_snapshots
		SET status = 'expired',
		    active_request_key = NULL,
		    lease_token = NULL,
		    lease_expires_at = NULL
		WHERE status IN ('queued', 'building')
		  AND expires_at <= ?`, now); err != nil {
		return fmt.Errorf("expire stale team snapshots: %w", err)
	}
	return nil
}

func (w *Worker) deleteObsoleteTeamSnapshots(ctx context.Context, now time.Time) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := w.db.ExecContext(ctx, `
			DELETE FROM team_analysis_snapshots
			WHERE status IN ('obsolete', 'expired', 'failed')
			  AND snapshot_id NOT IN (
			      SELECT snapshot_id FROM (
			          SELECT snapshot_id FROM team_export_jobs
			      ) referenced_exports
			  )
			LIMIT ?`, teamCleanupBatch)
		if err != nil {
			return fmt.Errorf("delete obsolete team snapshots: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
	}
}

func (w *Worker) expireTeamExportJobs(ctx context.Context, now time.Time) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE team_export_jobs
		SET status = 'expired', lease_token = NULL, lease_expires_at = NULL
		WHERE status = 'completed' AND expires_at <= ?`, now)
	if err != nil {
		return fmt.Errorf("expire team export jobs: %w", err)
	}
	return nil
}

func (w *Worker) deleteExpiredTeamExportObjects(ctx context.Context, now time.Time) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT export_id, object_key
		FROM team_export_jobs
		WHERE object_key IS NOT NULL AND object_key <> ''
		  AND (
		    status IN ('expired', 'revoked', 'failed')
		    OR (status = 'completed' AND expires_at <= ?)
		  )
		ORDER BY export_id
		LIMIT ?`, now, teamCleanupBatch)
	if err != nil {
		return fmt.Errorf("list expired team export objects: %w", err)
	}
	defer rows.Close()

	type item struct {
		exportID  string
		objectKey string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.exportID, &it.objectKey); err != nil {
			return fmt.Errorf("scan expired team export object: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()

	metaCutoff := now.Add(-teamExportMetaRetention)
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.storage.DeleteObject(ctx, it.objectKey); err != nil && !errors.Is(err, provider.ErrObjectNotFound) {
			return fmt.Errorf("delete team export object: %w", err)
		}
		if _, err := w.db.ExecContext(ctx, `
			UPDATE team_export_jobs
			SET object_key = NULL
			WHERE export_id = ?`, it.exportID); err != nil {
			return fmt.Errorf("clear team export object key: %w", err)
		}
	}
	if _, err := w.db.ExecContext(ctx, `
		DELETE FROM team_export_jobs
		WHERE status IN ('expired', 'revoked', 'failed')
		  AND created_at <= ?
		  AND (object_key IS NULL OR object_key = '')`, metaCutoff); err != nil {
		return fmt.Errorf("delete expired team export metadata: %w", err)
	}
	return nil
}

func (w *Worker) cleanupTeamCommandReceipts(ctx context.Context, now time.Time) error {
	_, err := w.db.ExecContext(ctx, `
		DELETE FROM team_command_receipts WHERE expires_at <= ?`, now)
	if err != nil {
		return fmt.Errorf("cleanup team command receipts: %w", err)
	}
	return nil
}

func (w *Worker) scrubInviteLinkSecrets(ctx context.Context, now time.Time) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE team_invite_links
		SET token_ciphertext = x''
		WHERE LENGTH(token_ciphertext) > 0
		  AND (
		    status = 'revoked'
		    OR expires_at <= ?
		    OR used_count >= max_uses
		  )`, now)
	if err != nil {
		return fmt.Errorf("scrub invite link ciphertext: %w", err)
	}
	return nil
}

func (w *Worker) cleanupInviteLinkJoins(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-teamInviteJoinRetention)
	_, err := w.db.ExecContext(ctx, `
		DELETE j FROM team_invite_link_joins j
		JOIN team_invite_links l ON l.link_id = j.link_id
		WHERE (
		    l.status = 'revoked'
		    OR l.expires_at <= ?
		    OR l.used_count >= l.max_uses
		  )
		  AND GREATEST(l.expires_at, COALESCE(l.revoked_at, l.expires_at)) <= ?`, now, cutoff)
	if err != nil {
		return fmt.Errorf("cleanup invite link joins: %w", err)
	}
	return nil
}

func (w *Worker) expireTeamUploadObjects(ctx context.Context, now time.Time) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT object_id, object_key
		FROM team_upload_objects
		WHERE status IN ('pending', 'deleted')
		  AND expires_at IS NOT NULL AND expires_at <= ?
		ORDER BY object_id
		LIMIT ?`, now, teamCleanupBatch)
	if err != nil {
		return fmt.Errorf("list expired team upload objects: %w", err)
	}
	defer rows.Close()
	type item struct {
		objectID  string
		objectKey string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.objectID, &it.objectKey); err != nil {
			return fmt.Errorf("scan expired team upload object: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()

	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if it.objectKey != "" {
			if err := w.storage.DeleteObject(ctx, it.objectKey); err != nil && !errors.Is(err, provider.ErrObjectNotFound) {
				return fmt.Errorf("delete team upload object: %w", err)
			}
		}
		if _, err := w.db.ExecContext(ctx, `
			UPDATE teams SET avatar_object_id = NULL WHERE avatar_object_id = ?`, it.objectID); err != nil {
			return fmt.Errorf("clear expired team avatar: %w", err)
		}
		if _, err := w.db.ExecContext(ctx, `
			DELETE FROM team_upload_objects WHERE object_id = ?`, it.objectID); err != nil {
			return fmt.Errorf("delete team upload row: %w", err)
		}
	}
	return nil
}
