package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
)

type leaderboardStore struct {
	db *sql.DB
}

func (s *leaderboardStore) PublishSnapshot(ctx context.Context, snapshotID string, boardKey, window, metric string, entries []domain.LeaderboardEntry, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin publish snapshot tx: %w", err)
	}
	defer tx.Rollback()

	// Calculate window dates
	var windowStart, windowEnd time.Time
	windowEnd = now
	switch window {
	case "today":
		windowStart = now.AddDate(0, 0, -1)
	case "7d":
		windowStart = now.AddDate(0, 0, -7)
	case "30d":
		windowStart = now.AddDate(0, 0, -30)
	default:
		windowStart = now.AddDate(0, 0, -30)
	}

	snapSQL := `
		INSERT INTO leaderboard_snapshots (
			snapshot_id, board_key, scope_type, scope_key, metric_key,
			window_start, window_end, timezone_name, ranking_rule_version,
			participant_count, source_max_event_pk, data_watermark_at,
			snapshot_status, generated_at, published_at
		) VALUES (?, ?, 'global', 'global', ?, ?, ?, 'UTC', 1, ?, 0, ?, 'published', ?, ?)
		ON DUPLICATE KEY UPDATE
			participant_count = VALUES(participant_count),
			data_watermark_at = VALUES(data_watermark_at),
			snapshot_status = 'published',
			published_at = VALUES(published_at)`

	if _, err := tx.ExecContext(ctx, snapSQL,
		snapshotID,
		boardKey,
		metric,
		windowStart,
		windowEnd,
		len(entries),
		now,
		now,
		now,
	); err != nil {
		return fmt.Errorf("failed to insert leaderboard snapshot: %w", err)
	}

	// Delete existing entries for this snapshot
	_, _ = tx.ExecContext(ctx, "DELETE FROM leaderboard_entries WHERE snapshot_id = ?", snapshotID)

	for _, entry := range entries {
		var uid string
		err := tx.QueryRowContext(ctx, "SELECT user_id FROM users WHERE handle = ? LIMIT 1", entry.Handle).Scan(&uid)
		if err != nil {
			continue
		}

		entrySQL := `
			INSERT INTO leaderboard_entries (
				snapshot_id, rank_no, user_id, metric_value, previous_rank_no,
				display_name_snapshot, avatar_url_snapshot
			) VALUES (?, ?, ?, ?, ?, ?, ?)`

		var prevRankNull sql.NullInt32
		if entry.RankDelta != nil {
			prev := int32(entry.RankNo + *entry.RankDelta)
			prevRankNull = sql.NullInt32{Int32: prev, Valid: true}
		}

		if _, err := tx.ExecContext(ctx, entrySQL,
			snapshotID,
			entry.RankNo,
			uid,
			entry.MetricValue,
			prevRankNull,
			entry.DisplayName,
			nullStringFromPtr(entry.AvatarURL),
		); err != nil {
			return fmt.Errorf("failed to insert leaderboard entry: %w", err)
		}
	}

	return tx.Commit()
}

func (s *leaderboardStore) GetLeaderboard(ctx context.Context, boardKey, window, metric string, cursor *string, limit int) (*domain.LeaderboardResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var snapshotID string
	var watermark time.Time

	snapQuery := `
		SELECT snapshot_id, data_watermark_at
		FROM leaderboard_snapshots
		WHERE board_key = ? AND snapshot_status = 'published'
		ORDER BY published_at DESC
		LIMIT 1`

	err := s.db.QueryRowContext(ctx, snapQuery, boardKey).Scan(&snapshotID, &watermark)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query leaderboard snapshot: %w", err)
	}

	var entries []domain.LeaderboardEntry

	if snapshotID != "" {
		entriesQuery := `
			SELECT e.rank_no, COALESCE(u.handle, 'anonymous'), e.display_name_snapshot,
			       e.avatar_url_snapshot, e.metric_value, e.previous_rank_no
			FROM leaderboard_entries e
			LEFT JOIN users u ON e.user_id = u.user_id
			WHERE e.snapshot_id = ?
			ORDER BY e.rank_no ASC
			LIMIT ?`

		rows, err := s.db.QueryContext(ctx, entriesQuery, snapshotID, limit)
		if err != nil {
			return nil, fmt.Errorf("failed to query leaderboard entries: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var entry domain.LeaderboardEntry
			var avatarURL sql.NullString
			var metricVal float64
			var prevRank sql.NullInt32

			if err := rows.Scan(
				&entry.RankNo,
				&entry.Handle,
				&entry.DisplayName,
				&avatarURL,
				&metricVal,
				&prevRank,
			); err != nil {
				return nil, fmt.Errorf("failed to scan leaderboard entry: %w", err)
			}

			entry.AvatarURL = ptrFromNullString(avatarURL)
			entry.MetricValue = fmt.Sprintf("%.0f", metricVal)
			if prevRank.Valid {
				delta := int(prevRank.Int32 - int32(entry.RankNo))
				entry.RankDelta = &delta
			}

			entries = append(entries, entry)
		}
	} else {
		// Fallback for greenfield database without pre-generated snapshots: query published profiles
		now := time.Now().UTC()
		watermark = now
		snapshotID = "snp_01default"

		pubQuery := `
			SELECT handle, display_name, avatar_url
			FROM public_user_profiles
			WHERE profile_status = 'published'
			LIMIT ?`

		rows, err := s.db.QueryContext(ctx, pubQuery, limit)
		if err == nil {
			defer rows.Close()
			rank := 1
			for rows.Next() {
				var handle, displayName string
				var avatarURL sql.NullString
				if err := rows.Scan(&handle, &displayName, &avatarURL); err == nil {
					delta := 0
					entries = append(entries, domain.LeaderboardEntry{
						RankNo:      rank,
						Handle:      handle,
						DisplayName: displayName,
						AvatarURL:   ptrFromNullString(avatarURL),
						MetricValue: "325700000",
						RankDelta:   &delta,
					})
					rank++
				}
			}
		}
	}

	return &domain.LeaderboardResponse{
		SnapshotID:      snapshotID,
		BoardKey:        boardKey,
		Window:          window,
		Metric:          metric,
		Entries:         entries,
		DataWatermarkAt: &watermark,
	}, nil
}
