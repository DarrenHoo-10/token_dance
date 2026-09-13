package teammetrics

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"tokendance/internal/domain"
)

func calendarDate(t time.Time) string {
	return t.In(domain.DayTZ).Format("2006-01-02")
}

func HandleID(teamID string, from, toExclusive time.Time, authRevision uint64) string {
	key := teamID + "|" + calendarDate(from) + "|" + calendarDate(toExclusive) + "|" + strconv.FormatUint(authRevision, 10) + "|" + RuleVersion
	sum := sha256.Sum256([]byte(key))
	return domain.SnapshotIDPrefix + hex.EncodeToString(sum[:])[:26]
}

func lastStaticCommit(ctx context.Context, tx *sql.Tx, teamID string) (time.Time, error) {
	var ms sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(updated_at) FROM team_member_day_metrics
		WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&ms); err != nil {
		return time.Time{}, err
	}
	if ms.Valid && ms.Int64 > 0 {
		return time.UnixMilli(ms.Int64).UTC(), nil
	}
	var changed sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT changed_at FROM team_source_revisions WHERE team_id = ?`, teamID).Scan(&changed); err != nil && err != sql.ErrNoRows {
		return time.Time{}, err
	}
	if changed.Valid {
		return changed.Time.UTC(), nil
	}
	return time.Time{}, nil
}

func EnsureHandleTx(ctx context.Context, tx *sql.Tx, teamID string, from, toExclusive time.Time, authRevision, sourceRevision uint64, asOf, now time.Time) (*domain.TeamAnalysisSnapshot, error) {
	id := HandleID(teamID, from, toExclusive, authRevision)
	expires := now.Add(48 * time.Hour)
	fromDate := calendarDate(from)
	toDate := calendarDate(toExclusive)
	commitAt, err := lastStaticCommit(ctx, tx, teamID)
	if err != nil {
		return nil, err
	}
	if !commitAt.IsZero() {
		asOf = commitAt
	} else {
		var created sql.NullTime
		if err := tx.QueryRowContext(ctx, `SELECT created_at FROM teams WHERE team_id = ?`, teamID).Scan(&created); err == nil && created.Valid {
			asOf = created.Time.UTC()
		}
	}
	if asOf.IsZero() {
		asOf = now
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_analysis_snapshots (
			snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision,
			rule_version, status, active_request_key, as_of, lease_generation, published_generation,
			attempt_count, next_attempt_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'ready', NULL, ?, 0, 1, 0, ?, ?)
		ON DUPLICATE KEY UPDATE
			status = 'ready',
			active_request_key = NULL,
			source_revision = VALUES(source_revision),
			as_of = VALUES(as_of),
			expires_at = VALUES(expires_at),
			error_code = NULL`,
		id, teamID, fromDate, toDate, authRevision, sourceRevision, RuleVersion, asOf, now, expires,
	); err != nil {
		return nil, fmt.Errorf("upsert analysis handle: %w", err)
	}
	return &domain.TeamAnalysisSnapshot{
		SnapshotID: id, TeamID: teamID, FromDate: from, ToDateExclusive: toExclusive,
		AuthRevision: authRevision, SourceRevision: sourceRevision, RuleVersion: RuleVersion,
		Status: domain.SnapshotReady, AsOf: asOf, PublishedGeneration: 1, NextAttemptAt: now, ExpiresAt: expires,
	}, nil
}
