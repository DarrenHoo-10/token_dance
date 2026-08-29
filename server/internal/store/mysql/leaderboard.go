package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store/sqlcgen"
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

	if err := sqlcgen.New(tx).DeleteSnapshotEntries(ctx, snapshotID); err != nil {
		return fmt.Errorf("failed to clean existing snapshot entries: %w", err)
	}

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

	queries := sqlcgen.New(s.db)
	snapshot, err := queries.GetLatestPublishedSnapshot(ctx, sqlcgen.GetLatestPublishedSnapshotParams{
		BoardKey:  boardKey,
		MetricKey: metric,
	})
	snapshotID := snapshot.SnapshotID
	watermark := snapshot.DataWatermarkAt
	if errors.Is(err, sql.ErrNoRows) {
		fallback, fallbackErr := queries.GetLatestPublishedSnapshotByBoard(ctx, boardKey)
		err = fallbackErr
		snapshotID = fallback.SnapshotID
		watermark = fallback.DataWatermarkAt
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.LeaderboardResponse{
				BoardKey: boardKey,
				Window:   window,
				Metric:   metric,
				Entries:  []domain.LeaderboardEntry{},
			}, nil
		}
		return nil, fmt.Errorf("failed to query leaderboard snapshot: %w", err)
	}

	rows, err := queries.ListVisibleLeaderboardEntries(ctx, sqlcgen.ListVisibleLeaderboardEntriesParams{
		SnapshotID: snapshotID,
		Limit:      int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query leaderboard entries: %w", err)
	}
	entries := make([]domain.LeaderboardEntry, 0, len(rows))
	for index, row := range rows {
		var rankDelta *int
		if row.PreviousRankNo.Valid {
			delta := int(row.PreviousRankNo.Int32 - int32(row.RankNo))
			rankDelta = &delta
		}
		metricValue := row.MetricValue
		if value, parseErr := strconv.ParseFloat(row.MetricValue, 64); parseErr == nil {
			metricValue = fmt.Sprintf("%.0f", value)
		}
		entries = append(entries, domain.LeaderboardEntry{
			RankNo:      index + 1,
			Handle:      row.Handle,
			DisplayName: row.DisplayName,
			AvatarURL:   ptrFromNullString(row.AvatarUrl),
			MetricValue: metricValue,
			RankDelta:   rankDelta,
		})
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
