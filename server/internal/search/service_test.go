package search

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/store/memory"
)

func TestSearchService(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk)

	now := clk.Now()
	_, _, _ = st.SeedUserForTest("usr_search1", "searchpilot", "search@tokendance.dev", now)
	_, _ = st.UpdatePrivacyTx(ctx, "usr_search1", domain.UserPrivacySettings{PublicProfileEnabled: true}, 0, domain.UserSecurityEvent{}, now)

	// Seed skills with threshold passing and failing
	st.SeedUserSkillFixture("usr_search1", []memory.UserSkillFixture{
		{
			SkillID:         "skl_popular",
			SkillPublicName: "Popular Search Skill",
			UseCount:        3000,
			ActiveDays:      10,
			PublicUserCount: 15,
		},
		{
			SkillID:         "skl_unpopular",
			SkillPublicName: "Hidden Low Sample Skill",
			UseCount:        5,
			ActiveDays:      1,
			PublicUserCount: 2,
		},
	})

	// 1. Search for user
	resUser, err := svc.Search(ctx, "pilot", 20)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}
	if len(resUser.Users) != 1 || resUser.Users[0].Handle != "searchpilot" {
		t.Errorf("expected searchpilot user in search results: %+v", resUser.Users)
	}

	// 2. Search for agent
	resAgent, err := svc.Search(ctx, "claude", 20)
	if err != nil {
		t.Fatalf("failed to search agent: %v", err)
	}
	if len(resAgent.Agents) == 0 {
		t.Errorf("expected claude agent in search results")
	}

	// 3. Search for skills (USR-106 minimum sample check)
	resSkill, err := svc.Search(ctx, "Skill", 20)
	if err != nil {
		t.Fatalf("failed to search skills: %v", err)
	}
	if len(resSkill.Skills) != 1 || resSkill.Skills[0].SkillPublicName != "Popular Search Skill" {
		t.Errorf("expected only Popular Search Skill in skill search results, got %+v", resSkill.Skills)
	}
}
