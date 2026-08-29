package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store/sqlcgen"
)

type searchStore struct {
	db *sql.DB
}

func sqlValueString(value any) string {
	switch value := value.(type) {
	case []byte:
		return string(value)
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}

func (s *searchStore) Search(ctx context.Context, query string, limit int, now time.Time) (*domain.SearchResponse, error) {
	query = strings.TrimSpace(query)
	searchPattern := "%" + strings.ToLower(query) + "%"

	queries := sqlcgen.New(s.db)
	userRows, err := queries.SearchPublicUsers(ctx, sqlcgen.SearchPublicUsersParams{
		SearchPattern: searchPattern,
		ExactHandle:   strings.ToLower(query),
		Limit:         int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search users: %w", err)
	}

	users := make([]domain.SearchUserResult, 0, len(userRows))
	for _, row := range userRows {
		users = append(users, domain.SearchUserResult{
			Handle:      row.Handle,
			DisplayName: row.DisplayName,
			AvatarURL:   ptrFromNullString(row.AvatarUrl),
			Bio:         ptrFromNullString(row.Bio),
		})
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
	skillRows, err := queries.SearchPublicSkills(ctx, sqlcgen.SearchPublicSkillsParams{
		NamePattern: sql.NullString{String: searchPattern, Valid: true},
		KeyPattern:  []byte(searchPattern),
		Limit:       int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search skills: %w", err)
	}

	skills := make([]domain.SearchSkillResult, 0, len(skillRows))
	for _, row := range skillRows {
		skillIDPart := strings.ToLower(row.SkillHex)
		if len(skillIDPart) >= 8 {
			skillIDPart = skillIDPart[:8]
		}
		skills = append(skills, domain.SearchSkillResult{
			SkillID:         fmt.Sprintf("skl_%s", skillIDPart),
			SkillPublicName: row.SkillPublicName.String,
			UseCount:        sqlValueString(row.TotalUseCount),
			PublicUserCount: int(row.PublicUserCount),
			ActiveDays:      int(row.ActiveDays),
		})
	}

	return &domain.SearchResponse{
		Users:  users,
		Agents: agents,
		Skills: skills,
	}, nil
}
