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

func liveEligibleTotalsSQL() string {
	return `
	SELECT e.user_id, e.handle, e.display_name, e.avatar_url, e.registered_at,
	       CAST(CASE WHEN s.revision > 0 THEN s.token_total ELSE COALESCE(m.tokens, 0) END AS UNSIGNED) AS tokens,
	       COALESCE(s.updated_at, m.watermark) AS watermark
	FROM (
		SELECT u.user_id,
		       ` + leaderboardHandleExpr() + ` AS handle,
		       ` + leaderboardDisplayNameExpr() + ` AS display_name,
		       COALESCE(p.avatar_url, u.avatar_url) AS avatar_url,
		       u.created_at AS registered_at
		FROM users u
		LEFT JOIN public_user_profiles p ON p.user_id = u.user_id
		WHERE u.account_status = 'active'
	) e
	LEFT JOIN user_window_scores s
	  ON s.user_id = e.user_id AND s.window_key = ? AND s.generation = ? AND s.eligible = TRUE
	LEFT JOIN (
		SELECT user_id,
		       SUM(exact_token_total + derived_token_total) AS tokens,
		       MAX(computed_at) AS watermark
		FROM daily_user_agent_metrics
		WHERE metric_date >= ? AND metric_date <= ?
		GROUP BY user_id
	) m ON m.user_id = e.user_id`
}

func liveTokenRankingSQL() string {
	return `WITH totals AS (` + liveEligibleTotalsSQL() + `), ranked AS (
	SELECT totals.*, ROW_NUMBER() OVER (ORDER BY tokens DESC, registered_at ASC, user_id ASC) AS rank_no
	FROM totals
), stats AS (
	SELECT COUNT(*) AS participants, CAST(COALESCE(SUM(tokens), 0) AS UNSIGNED) AS tokens,
	       MAX(watermark) AS watermark FROM totals
)
`
}

func savedPreviousTotalsSQL() string {
	return `
	SELECT s.user_id, s.token_total AS tokens, s.registered_at
	FROM user_window_scores s
	JOIN users u ON u.user_id = s.user_id AND u.account_status = 'active'
	WHERE s.window_key = ? AND s.generation = ? AND s.eligible = TRUE`
}

func liveTokenComparisonSQL(previousSQL string) string {
	return liveTokenRankingSQL() + `, previous_totals AS (` + previousSQL + `), previous_ranked AS (
    SELECT user_id, ROW_NUMBER() OVER (ORDER BY tokens DESC, registered_at ASC, user_id ASC) AS rank_no
    FROM previous_totals
)
`
}

func (s *leaderboardStore) previousTotalsQuery(ctx context.Context, window string, now time.Time) (string, []interface{}, error) {
	prevNow := now.UTC().AddDate(0, 0, -1)
	prevGen := WindowGeneration(prevNow)
	hasSaved, err := savedPreviousWindowExists(ctx, s.db, window, prevGen)
	if err != nil {
		return "", nil, err
	}
	if hasSaved {
		return savedPreviousTotalsSQL(), []interface{}{window, prevGen}, nil
	}
	from, to, err := leaderboardDates(window, prevNow)
	if err != nil {
		return "", nil, err
	}
	return liveEligibleTotalsSQL(), []interface{}{window, prevGen, from, to}, nil
}

func (s *leaderboardStore) getLiveTokenLeaderboard(ctx context.Context, window string, cursor *string, limit int, now time.Time) (*domain.LeaderboardResponse, error) {
	from, to, err := leaderboardDates(window, now)
	if err != nil {
		return nil, err
	}
	generation := WindowGeneration(now)
	previousSQL, previousArgs, err := s.previousTotalsQuery(ctx, window, now)
	if err != nil {
		return nil, err
	}
	after := 0
	if cursor != nil {
		after, err = strconv.Atoi(*cursor)
		if err != nil || after < 0 || after >= 1000 {
			return nil, domain.ErrInvalidArgument
		}
	}
	endRank := after + limit
	if endRank > 1000 {
		endRank = 1000
	}
	args := []interface{}{window, generation, from, to}
	args = append(args, previousArgs...)
	args = append(args, after, endRank)
	rows, err := s.db.QueryContext(ctx, liveTokenComparisonSQL(previousSQL)+`
	SELECT stats.participants, stats.tokens, stats.watermark,
	       ranked.rank_no, ranked.handle, ranked.display_name, ranked.avatar_url, ranked.tokens, previous_ranked.rank_no
	FROM stats LEFT JOIN ranked ON ranked.rank_no > ? AND ranked.rank_no <= ?
	LEFT JOIN previous_ranked ON previous_ranked.user_id = ranked.user_id
	ORDER BY ranked.rank_no`, args...)
	if err != nil {
		return nil, fmt.Errorf("query live token leaderboard: %w", err)
	}
	defer rows.Close()
	count, total := 0, "0"
	response := &domain.LeaderboardResponse{
		BoardKey:          "global",
		Window:            window,
		Metric:            "tokens",
		Timezone:          "UTC",
		Generation:        generation,
		Entries:           []domain.LeaderboardEntry{},
		TotalEntries:      &count,
		TotalParticipants: &count,
		TotalTokens:       &total,
	}
	for rows.Next() {
		var watermark sql.NullTime
		var rank, previousRank sql.NullInt64
		var handle, name, avatar, tokenValue sql.NullString
		var totalTokens sql.NullString
		if err := rows.Scan(&count, &totalTokens, &watermark, &rank, &handle, &name, &avatar, &tokenValue, &previousRank); err != nil {
			return nil, err
		}
		if totalTokens.Valid {
			total = formatLeaderboardTokens(totalTokens.String)
		}
		if watermark.Valid {
			value := watermark.Time
			response.DataWatermarkAt = &value
		}
		if rank.Valid {
			entry := domain.LeaderboardEntry{
				RankNo:      int(rank.Int64),
				Handle:      handle.String,
				DisplayName: displayNameOrDefault(name.String, ""),
				AvatarURL:   ptrFromNullString(avatar),
				MetricValue: formatLeaderboardTokens(tokenValue.String),
				IsNew:       !previousRank.Valid,
			}
			if previousRank.Valid {
				delta := int(previousRank.Int64 - rank.Int64)
				entry.RankDelta = &delta
			}
			response.Entries = append(response.Entries, entry)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	response.TotalEntries = &count
	response.TotalParticipants = &count
	response.TotalTokens = &total
	if len(response.Entries) > 0 && after+len(response.Entries) < count && after+len(response.Entries) < 1000 {
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
	generation := WindowGeneration(now)
	var rank, count int
	err = s.db.QueryRowContext(ctx, liveTokenRankingSQL()+`SELECT rank_no, participants FROM ranked CROSS JOIN stats WHERE user_id = ?`,
		window, generation, from, to, userID).Scan(&rank, &count)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	percentile := float64(count-rank+1) / float64(count) * 100
	return &rank, &percentile, nil
}

// This is used only by authenticated personal summaries. The public list stays
// capped at 1000; callers never supply a different account's user ID.
func (s *leaderboardStore) liveOwnTokenEntry(ctx context.Context, userID, window string, now time.Time) (*domain.LeaderboardEntry, *float64, error) {
	from, to, err := leaderboardDates(window, now)
	if err != nil {
		return nil, nil, nil
	}
	generation := WindowGeneration(now)
	previousSQL, previousArgs, err := s.previousTotalsQuery(ctx, window, now)
	if err != nil {
		return nil, nil, err
	}
	args := []interface{}{window, generation, from, to}
	args = append(args, previousArgs...)
	args = append(args, userID)
	var entry domain.LeaderboardEntry
	var avatar sql.NullString
	var previousRank sql.NullInt64
	var tokenValue sql.NullString
	var count int
	err = s.db.QueryRowContext(ctx, liveTokenComparisonSQL(previousSQL)+`
	SELECT ranked.rank_no, ranked.handle, ranked.display_name, ranked.avatar_url, ranked.tokens, previous_ranked.rank_no, stats.participants
	FROM ranked CROSS JOIN stats LEFT JOIN previous_ranked ON previous_ranked.user_id = ranked.user_id
	WHERE ranked.user_id = ?`, args...).Scan(&entry.RankNo, &entry.Handle, &entry.DisplayName, &avatar, &tokenValue, &previousRank, &count)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	entry.DisplayName = displayNameOrDefault(entry.DisplayName, "")
	entry.MetricValue = formatLeaderboardTokens(tokenValue.String)
	entry.AvatarURL = ptrFromNullString(avatar)
	entry.IsNew = !previousRank.Valid
	if previousRank.Valid {
		delta := int(previousRank.Int64) - entry.RankNo
		entry.RankDelta = &delta
	}
	percentile := float64(count-entry.RankNo+1) / float64(count) * 100
	return &entry, &percentile, nil
}

func displayNameOrDefault(name, locale string) string {
	if name != "" {
		return name
	}
	if locale == "zh-CN" {
		return "开发者"
	}
	return "Developer"
}

func formatLeaderboardTokens(value string) string {
	if value == "" {
		return "0"
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return strconv.FormatInt(int64(parsed), 10)
	}
	return value
}
