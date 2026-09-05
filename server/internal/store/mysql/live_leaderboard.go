package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"tokendance/internal/domain"
)

// All participants share UTC calendar boundaries. Read committed aggregates so
// first publication, new uploads and privacy changes do not need a snapshot job.
func leaderboardDates(window string, now time.Time) (string, string, error) {
	end := now.UTC()
	start := end
	switch window {
	case "today":
	case "7d":
		start = end.AddDate(0, 0, -6)
	case "30d":
		start = end.AddDate(0, 0, -29)
	case "all":
		return "1000-01-01", end.Format("2006-01-02"), nil
	default:
		return "", "", domain.ErrInvalidArgument
	}
	return start.Format("2006-01-02"), end.Format("2006-01-02"), nil
}

const liveTokenRanking = `WITH totals AS (
	SELECT u.user_id, p.handle, p.display_name, p.avatar_url,
	       SUM(m.exact_token_total + m.derived_token_total) AS tokens,
	       MAX(m.computed_at) AS watermark
	FROM daily_user_agent_metrics m
	JOIN users u ON u.user_id = m.user_id
	JOIN public_user_profiles p ON p.user_id = u.user_id
	JOIN user_privacy_settings priv ON priv.user_id = u.user_id
	WHERE m.metric_date >= ? AND m.metric_date <= ?
	  AND u.account_status = 'active' AND u.leaderboard_visibility = 'public'
	  AND p.profile_status = 'published' AND priv.public_profile_enabled = TRUE
	  AND priv.show_token_total = TRUE
	GROUP BY u.user_id, p.handle, p.display_name, p.avatar_url
	HAVING tokens > 0
), ranked AS (
	SELECT totals.*, ROW_NUMBER() OVER (ORDER BY tokens DESC, user_id ASC) AS rank_no
	FROM totals
), stats AS (
	SELECT COUNT(*) AS participants, COALESCE(SUM(tokens), 0) AS tokens,
	       MAX(watermark) AS watermark FROM totals
)
`

func (s *leaderboardStore) getLiveTokenLeaderboard(ctx context.Context, window string, cursor *string, limit int, now time.Time) (*domain.LeaderboardResponse, error) {
	from, to, err := leaderboardDates(window, now)
	if err != nil {
		return nil, err
	}
	after := 0
	if cursor != nil {
		after, err = strconv.Atoi(*cursor)
		if err != nil || after < 0 || after > 10000000 {
			return nil, domain.ErrInvalidArgument
		}
	}
	rows, err := s.db.QueryContext(ctx, liveTokenRanking+`
	SELECT stats.participants, stats.tokens, stats.watermark,
	       ranked.rank_no, ranked.handle, ranked.display_name, ranked.avatar_url, ranked.tokens
	FROM stats LEFT JOIN ranked ON ranked.rank_no > ? AND ranked.rank_no <= ?
	ORDER BY ranked.rank_no`, from, to, after, after+limit)
	if err != nil {
		return nil, fmt.Errorf("query live token leaderboard: %w", err)
	}
	defer rows.Close()
	count, total := 0, "0"
	response := &domain.LeaderboardResponse{BoardKey: "global", Window: window, Metric: "tokens", Timezone: "UTC", Entries: []domain.LeaderboardEntry{}, TotalEntries: &count, TotalTokens: &total}
	for rows.Next() {
		var watermark sql.NullTime
		var rank sql.NullInt64
		var handle, name, avatar, tokens sql.NullString
		if err := rows.Scan(&count, &total, &watermark, &rank, &handle, &name, &avatar, &tokens); err != nil {
			return nil, err
		}
		if watermark.Valid {
			value := watermark.Time
			response.DataWatermarkAt = &value
		}
		if rank.Valid {
			response.Entries = append(response.Entries, domain.LeaderboardEntry{RankNo: int(rank.Int64), Handle: handle.String, DisplayName: name.String, AvatarURL: ptrFromNullString(avatar), MetricValue: tokens.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(response.Entries) > 0 && after+len(response.Entries) < count {
		next := strconv.Itoa(after + len(response.Entries))
		response.NextCursor = &next
	}
	return response, nil
}

func (s *leaderboardStore) liveTokenRank(ctx context.Context, userID, window string, now time.Time) (*int, *float64, error) {
	from, to, err := leaderboardDates(window, now)
	if err != nil {
		return nil, nil, nil
	}
	var rank, count int
	err = s.db.QueryRowContext(ctx, liveTokenRanking+`SELECT rank_no, participants FROM ranked CROSS JOIN stats WHERE user_id = ?`, from, to, userID).Scan(&rank, &count)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	percentile := float64(count-rank+1) / float64(count) * 100
	return &rank, &percentile, nil
}
