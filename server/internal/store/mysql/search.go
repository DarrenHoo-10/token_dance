package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/domain"
)

type searchStore struct {
	db *sql.DB
}

func (s *searchStore) Search(ctx context.Context, query string, limit int, now time.Time) (*domain.SearchResponse, error) {
	query = strings.TrimSpace(query)
	searchPattern := "%" + strings.ToLower(query) + "%"

	userQuery := `
		SELECT p.handle, p.display_name, p.avatar_url, p.bio
		FROM public_user_profiles p
		JOIN users u ON p.user_id = u.user_id
		JOIN user_privacy_settings priv ON p.user_id = priv.user_id
		WHERE p.profile_status = 'published'
		  AND u.account_status = 'active'
		  AND u.leaderboard_visibility = 'public'
		  AND priv.public_profile_enabled = TRUE
		  AND (LOWER(p.handle) LIKE ? OR LOWER(p.display_name) LIKE ?)
		ORDER BY (LOWER(p.handle) = LOWER(?)) DESC, p.handle ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, userQuery, searchPattern, searchPattern, strings.ToLower(query), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search users: %w", err)
	}
	defer rows.Close()

	users := make([]domain.SearchUserResult, 0)
	for rows.Next() {
		var u domain.SearchUserResult
		var avatarURL, bio sql.NullString
		if err := rows.Scan(&u.Handle, &u.DisplayName, &avatarURL, &bio); err != nil {
			return nil, fmt.Errorf("failed to scan search user row: %w", err)
		}
		u.AvatarURL = ptrFromNullString(avatarURL)
		u.Bio = ptrFromNullString(bio)
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search users iteration error: %w", err)
	}

	agentCatalog := []domain.SearchAgentResult{
		{AgentID: "claude-code", Name: "Claude Code", Description: "Anthropic's terminal coding agent"},
		{AgentID: "cursor", Name: "Cursor", Description: "AI-first code editor"},
		{AgentID: "codex", Name: "Codex CLI", Description: "OpenAI terminal agent"},
	}

	agents := make([]domain.SearchAgentResult, 0)
	for _, a := range agentCatalog {
		if strings.Contains(strings.ToLower(a.AgentID), strings.ToLower(query)) || strings.Contains(strings.ToLower(a.Name), strings.ToLower(query)) {
			agents = append(agents, a)
		}
	}

	// Search skills with minimum sample requirement (USR-106): min 5 public users & 3 active days
	skillQuery := `
		SELECT
			HEX(s.skill_key) AS skill_hex,
			s.skill_public_name,
			SUM(s.use_count) AS total_use_count,
			COUNT(DISTINCT s.user_id) AS public_user_count,
			COUNT(DISTINCT s.metric_date) AS active_days
		FROM daily_skill_metrics s
		JOIN users u ON s.user_id = u.user_id
		JOIN public_user_profiles p ON s.user_id = p.user_id
		JOIN user_privacy_settings priv ON s.user_id = priv.user_id
		WHERE u.account_status = 'active'
		  AND u.leaderboard_visibility = 'public'
		  AND p.profile_status = 'published'
		  AND priv.public_profile_enabled = TRUE
		  AND priv.show_skill_ranking = TRUE
		  AND s.skill_public_name IS NOT NULL
		  AND s.skill_public_name != ''
		  AND (LOWER(s.skill_public_name) LIKE ? OR LOWER(HEX(s.skill_key)) LIKE ?)
		GROUP BY s.skill_key, s.skill_public_name
		HAVING public_user_count >= 5 AND active_days >= 3
		ORDER BY total_use_count DESC
		LIMIT ?`

	skillRows, err := s.db.QueryContext(ctx, skillQuery, searchPattern, searchPattern, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search skills: %w", err)
	}
	defer skillRows.Close()

	skills := make([]domain.SearchSkillResult, 0)
	for skillRows.Next() {
		var skillHex, skillPubName string
		var totalUseCount, publicUserCount, activeDays uint64
		if err := skillRows.Scan(&skillHex, &skillPubName, &totalUseCount, &publicUserCount, &activeDays); err != nil {
			return nil, fmt.Errorf("failed to scan search skill row: %w", err)
		}
		var skillID string
		if len(skillHex) >= 8 {
			skillID = fmt.Sprintf("skl_%s", strings.ToLower(skillHex[:8]))
		} else {
			skillID = fmt.Sprintf("skl_%s", strings.ToLower(skillHex))
		}
		skills = append(skills, domain.SearchSkillResult{
			SkillID:         skillID,
			SkillPublicName: skillPubName,
			UseCount:        fmt.Sprintf("%d", totalUseCount),
			PublicUserCount: int(publicUserCount),
			ActiveDays:      int(activeDays),
		})
	}
	if err := skillRows.Err(); err != nil {
		return nil, fmt.Errorf("search skills iteration error: %w", err)
	}

	return &domain.SearchResponse{
		Users:  users,
		Agents: agents,
		Skills: skills,
	}, nil
}
