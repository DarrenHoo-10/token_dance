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
		WHERE p.profile_status = 'published'
		  AND u.account_status = 'active'
		  AND (LOWER(p.handle) LIKE ? OR LOWER(p.display_name) LIKE ?)
		ORDER BY (LOWER(p.handle) = LOWER(?)) DESC, p.handle ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, userQuery, searchPattern, searchPattern, strings.ToLower(query), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search users: %w", err)
	}
	defer rows.Close()

	var users []domain.SearchUserResult
	rank := 1
	for rows.Next() {
		var u domain.SearchUserResult
		var avatarURL, bio sql.NullString
		if err := rows.Scan(&u.Handle, &u.DisplayName, &avatarURL, &bio); err != nil {
			return nil, fmt.Errorf("failed to scan search user row: %w", err)
		}
		u.AvatarURL = ptrFromNullString(avatarURL)
		u.Bio = ptrFromNullString(bio)
		rVal := rank
		u.Rank = &rVal
		rank++
		users = append(users, u)
	}

	agentCatalog := []domain.SearchAgentResult{
		{AgentID: "claude-code", Name: "Claude Code", Description: "Anthropic's terminal coding agent"},
		{AgentID: "cursor", Name: "Cursor", Description: "AI-first code editor"},
		{AgentID: "codex", Name: "Codex CLI", Description: "OpenAI terminal agent"},
	}

	var agents []domain.SearchAgentResult
	for _, a := range agentCatalog {
		if strings.Contains(strings.ToLower(a.AgentID), strings.ToLower(query)) || strings.Contains(strings.ToLower(a.Name), strings.ToLower(query)) {
			agents = append(agents, a)
		}
	}

	return &domain.SearchResponse{
		Users:  users,
		Agents: agents,
	}, nil
}
