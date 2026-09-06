package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/ranking"
	"tokendance/internal/store"
	"tokendance/internal/store/sqlcgen"
)

type leaderboardStore struct {
	db    *sql.DB
	index *ranking.Index
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
	return s.GetLeaderboardView(ctx, store.LeaderboardQuery{
		BoardKey: boardKey,
		Window:   window,
		Metric:   metric,
		Cursor:   cursor,
		Limit:    limit,
	})
}

func (s *leaderboardStore) GetLeaderboardView(ctx context.Context, q store.LeaderboardQuery) (*domain.LeaderboardResponse, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	if q.BoardKey == "" {
		q.BoardKey = "global"
	}
	if q.Window == "" {
		q.Window = "30d"
	}
	if q.Metric == "" {
		q.Metric = "tokens"
	}
	if q.BoardKey == "global" && q.Metric == "tokens" && s.index != nil {
		offset := 0
		if q.Cursor != nil {
			parsed, err := strconv.Atoi(*q.Cursor)
			if err != nil || parsed < 0 || parsed >= ranking.PublicCap {
				return nil, domain.ErrInvalidArgument
			}
			offset = parsed
		}
		view, err := s.index.Read(ctx, ranking.ReadRequest{
			Window:     q.Window,
			SnapshotID: q.SnapshotID,
			Offset:     offset,
			Limit:      q.Limit,
			UserID:     q.ViewerUserID,
		})
		if err != nil {
			log.Printf("ranking redis read failed, falling back to mysql: %v", err)
		} else if view != nil && !view.Miss {
			return s.hydrateRankingView(ctx, q, view, offset)
		}
	}
	if q.BoardKey == "global" && q.Metric == "tokens" {
		resp, err := s.getLiveTokenLeaderboard(ctx, q.Window, q.Cursor, q.Limit, time.Now())
		if err != nil {
			return nil, err
		}
		resp.ViewKind = "mysql"
		if q.ViewerUserID != "" {
			own, _, err := s.liveOwnTokenEntry(ctx, q.ViewerUserID, q.Window, time.Now())
			if err != nil {
				return nil, err
			}
			if own != nil && own.RankNo > ranking.PublicCap {
				resp.OwnEntry = own
			}
		}
		return resp, nil
	}
	limit := q.Limit

	queries := sqlcgen.New(s.db)
	snapshot, err := queries.GetLatestPublishedSnapshot(ctx, sqlcgen.GetLatestPublishedSnapshotParams{
		BoardKey:  q.BoardKey,
		MetricKey: q.Metric,
	})
	snapshotID := snapshot.SnapshotID
	watermark := snapshot.DataWatermarkAt
	if errors.Is(err, sql.ErrNoRows) {
		fallback, fallbackErr := queries.GetLatestPublishedSnapshotByBoard(ctx, q.BoardKey)
		err = fallbackErr
		snapshotID = fallback.SnapshotID
		watermark = fallback.DataWatermarkAt
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.LeaderboardResponse{
				BoardKey: q.BoardKey,
				Window:   q.Window,
				Metric:   q.Metric,
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
		BoardKey:        q.BoardKey,
		Window:          q.Window,
		Metric:          q.Metric,
		Entries:         entries,
		DataWatermarkAt: &watermark,
	}, nil
}

func (s *leaderboardStore) hydrateRankingView(ctx context.Context, q store.LeaderboardQuery, view *ranking.ReadResult, offset int) (*domain.LeaderboardResponse, error) {
	ids := make([]string, 0, len(view.Entries)+1)
	for _, entry := range view.Entries {
		ids = append(ids, entry.UserID)
	}
	if view.Own != nil {
		ids = append(ids, view.Own.UserID)
	}
	profiles, err := s.rankingProfiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	entries := make([]domain.LeaderboardEntry, 0, len(view.Entries))
	ownOnPage := false
	for _, item := range view.Entries {
		entry := rankingEntry(item, profiles[item.UserID])
		if q.ViewerUserID != "" && item.UserID == q.ViewerUserID {
			ownOnPage = true
		}
		entries = append(entries, entry)
	}
	participants := view.Participants
	resp := &domain.LeaderboardResponse{
		TotalEntries:      &participants,
		TotalParticipants: &participants,
		Timezone:          "UTC",
		Generation:        view.Generation,
		SnapshotID:        view.SnapshotID,
		Revision:          view.Revision,
		ViewKind:          view.Kind,
		BoardKey:          q.BoardKey,
		Window:            q.Window,
		Metric:            q.Metric,
		Entries:           entries,
	}
	if view.Own != nil && view.Own.Rank > ranking.PublicCap && !ownOnPage {
		own := rankingEntry(*view.Own, profiles[view.Own.UserID])
		resp.OwnEntry = &own
	}
	if len(entries) > 0 && offset+len(entries) < participants && offset+len(entries) < ranking.PublicCap {
		next := strconv.Itoa(offset + len(entries))
		resp.NextCursor = &next
	}
	return resp, nil
}

type rankingProfile struct {
	Handle      string
	DisplayName string
	AvatarURL   *string
}

func rankingEntry(item ranking.Entry, profile rankingProfile) domain.LeaderboardEntry {
	handle := profile.Handle
	if handle == "" {
		handle = item.UserID
	}
	entry := domain.LeaderboardEntry{
		RankNo:      item.Rank,
		Handle:      handle,
		DisplayName: displayNameOrDefault(profile.DisplayName, ""),
		AvatarURL:   profile.AvatarURL,
		MetricValue: item.Tokens,
		IsNew:       item.PreviousRank == nil,
	}
	if item.PreviousRank != nil {
		delta := *item.PreviousRank - item.Rank
		if delta != 0 {
			entry.RankDelta = &delta
		}
		entry.IsNew = false
	}
	return entry
}

func (s *leaderboardStore) rankingProfiles(ctx context.Context, userIDs []string) (map[string]rankingProfile, error) {
	out := make(map[string]rankingProfile, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	query := `
		SELECT u.user_id,
		       ` + leaderboardHandleExpr() + ` AS handle,
		       ` + leaderboardDisplayNameExpr() + ` AS display_name,
		       COALESCE(p.avatar_url, u.avatar_url) AS avatar_url
		FROM users u
		LEFT JOIN public_user_profiles p ON p.user_id = u.user_id
		WHERE u.user_id IN (` + placeholders(len(userIDs)) + `)`
	args := make([]interface{}, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load ranking profiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var userID, handle, displayName string
		var avatar sql.NullString
		if err := rows.Scan(&userID, &handle, &displayName, &avatar); err != nil {
			return nil, fmt.Errorf("scan ranking profile: %w", err)
		}
		out[userID] = rankingProfile{
			Handle:      handle,
			DisplayName: displayName,
			AvatarURL:   ptrFromNullString(avatar),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ranking profiles: %w", err)
	}
	return out, nil
}
