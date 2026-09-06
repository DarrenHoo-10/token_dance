package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"tokendance/internal/domain"
)

func seedRankUsage(t *testing.T, db *sql.DB, userID, date, agent string, exact, derived, estimated int) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO daily_user_agent_metrics
	(metric_date,user_id,agent_id,exact_token_total,derived_token_total,estimated_token_total,aggregation_version)
	VALUES (?,?,?,?,?,?,2)`, date, userID, agent, exact, derived, estimated)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLeaderboardTop1000MySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	for n := 1; n <= 1001; n++ {
		id := fmt.Sprintf("usr_cap_%04d", n)
		handle := fmt.Sprintf("cap_%04d", n)
		seedTestUser(t, db, st, id, handle, handle, handle+"@cap.test", true, now)
		if _, err := db.Exec("UPDATE user_privacy_settings SET show_token_total=TRUE WHERE user_id=?", id); err != nil {
			t.Fatal(err)
		}
		seedRankUsage(t, db, id, date, "codex", 2000-n, 0, 0)
	}
	cursor := "980"
	board, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", &cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Entries) != 20 || board.Entries[19].RankNo != 1000 || board.NextCursor != nil {
		t.Fatalf("top 1000 boundary failed: %+v", board)
	}
	if board.TotalEntries == nil || *board.TotalEntries != 1001 {
		t.Fatal("total participant count was capped")
	}
	entry, _, err := (&leaderboardStore{db: db}).liveOwnTokenEntry(ctx, "usr_cap_1001", "today", now)
	if err != nil || entry == nil || entry.RankNo != 1001 || entry.Handle != "cap_1001" || entry.MetricValue != "999" {
		t.Fatalf("own entry outside top 1000: %+v %v", entry, err)
	}
	if _, err := db.Exec("UPDATE user_privacy_settings SET public_profile_enabled=FALSE WHERE user_id='usr_cap_1001'"); err != nil {
		t.Fatal(err)
	}
	entry, _, err = (&leaderboardStore{db: db}).liveOwnTokenEntry(ctx, "usr_cap_1001", "today", now)
	if err != nil || entry == nil || entry.RankNo != 1001 {
		t.Fatalf("privacy toggle removed own rank: %+v %v", entry, err)
	}
	cursor = "1000"
	if _, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", &cursor, 20); err == nil {
		t.Fatal("cursor beyond top 1000 accepted")
	}
}

func TestLeaderboardUTCDates(t *testing.T) {
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	for window, want := range map[string]string{"today": "2025-12-31", "7d": "2025-12-25", "30d": "2025-12-02", "all": "1000-01-01"} {
		from, to, err := leaderboardDates(window, now)
		if err != nil || from != want || to != "2025-12-31" {
			t.Fatalf("%s: %s..%s %v", window, from, to, err)
		}
	}
	if _, _, err := leaderboardDates("bad", now); err == nil {
		t.Fatal("invalid window accepted")
	}
}

func TestLiveLeaderboardRankChangesMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		name             string
		today, yesterday int
	}{
		{"alice", 300, 100}, {"bob", 50, 200}, {"carol", 20, 0}, {"dave", 100, 50},
	} {
		id := "usr_delta_" + row.name
		seedTestUser(t, db, st, id, row.name, row.name, row.name+"@delta.test", true, now)
		if _, err := db.Exec("UPDATE user_privacy_settings SET show_token_total=TRUE WHERE user_id=?", id); err != nil {
			t.Fatal(err)
		}
		seedRankUsage(t, db, id, "2026-09-06", "codex", row.today, 0, 0)
		if row.yesterday > 0 {
			seedRankUsage(t, db, id, "2026-09-05", "codex", row.yesterday, 0, 0)
		}
	}
	s := &leaderboardStore{db: db}
	for _, window := range []string{"today", "7d", "30d", "all"} {
		wanted := map[string]int{"alice": 1, "bob": -1, "dave": 0, "carol": 0}
		if window == "today" {
			wanted["bob"] = -2
			wanted["dave"] = 1
		}
		var cursor *string
		seen := 0
		for {
			board, err := s.getLiveTokenLeaderboard(context.Background(), window, cursor, 2, now)
			if err != nil {
				t.Fatal(err)
			}
			if board.TotalParticipants == nil || *board.TotalParticipants != 4 {
				t.Fatalf("%s participant count: %+v", window, board.TotalParticipants)
			}
			for _, entry := range board.Entries {
				seen++
				if entry.IsNew || entry.RankDelta == nil || *entry.RankDelta != wanted[entry.Handle] {
					t.Fatalf("%s change: %+v want %d", window, entry, wanted[entry.Handle])
				}
			}
			if board.NextCursor == nil {
				break
			}
			cursor = board.NextCursor
		}
		if seen != 4 {
			t.Fatalf("missing paginated entries: %d", seen)
		}
	}
	if _, err := db.Exec("UPDATE user_privacy_settings SET public_profile_enabled=FALSE WHERE user_id='usr_delta_bob'"); err != nil {
		t.Fatal(err)
	}
	board, err := s.getLiveTokenLeaderboard(context.Background(), "today", nil, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Entries) != 4 || board.TotalParticipants == nil || *board.TotalParticipants != 4 {
		t.Fatalf("privacy toggle changed membership: %+v", board.Entries)
	}
}

func TestLiveTokenLeaderboardMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	date := func(days int) string { return now.AddDate(0, 0, days).Format("2006-01-02") }
	for _, name := range []string{"alice", "bob", "private", "hidden"} {
		seedTestUser(t, db, st, "usr_live_"+name, name, name, name+"@live.test", name != "private", now)
		if _, err := db.Exec("UPDATE user_privacy_settings SET show_token_total=? WHERE user_id=?", name != "hidden", "usr_live_"+name); err != nil {
			t.Fatal(err)
		}
	}
	seedRankUsage(t, db, "usr_live_alice", date(0), "codex", 100, 10, 100000)
	seedRankUsage(t, db, "usr_live_alice", date(0), "claude-code", 90, 0, 0)
	seedRankUsage(t, db, "usr_live_alice", date(-6), "codex", 50, 0, 0)
	seedRankUsage(t, db, "usr_live_alice", date(-35), "codex", 5000, 0, 0)
	seedRankUsage(t, db, "usr_live_alice", date(1), "codex", 999999, 0, 0)
	seedRankUsage(t, db, "usr_live_bob", date(0), "codex", 300, 0, 0)
	seedRankUsage(t, db, "usr_live_bob", date(-7), "codex", 10000, 0, 0)
	seedRankUsage(t, db, "usr_live_private", date(0), "codex", 1000000, 0, 0)
	seedRankUsage(t, db, "usr_live_hidden", date(0), "codex", 1000000, 0, 0)
	board, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if board.TotalEntries == nil || *board.TotalEntries != 4 || board.TotalParticipants == nil || *board.TotalParticipants != 4 {
		t.Fatalf("private/unpublished users missing from totals: %+v", board)
	}
	if len(board.Entries) != 4 || board.Entries[0].Handle != "hidden" || board.Entries[1].Handle != "private" {
		t.Fatalf("unpublished users must still rank: %+v", board.Entries)
	}
	if _, err := db.Exec("UPDATE user_privacy_settings SET public_profile_enabled=FALSE WHERE user_id='usr_live_bob'"); err != nil {
		t.Fatal(err)
	}
	board, err = st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil || len(board.Entries) != 4 || *board.TotalEntries != 4 {
		t.Fatalf("privacy withdrawal changed membership: %+v %v", board, err)
	}
	rank, _, err := (&leaderboardStore{db: db}).liveTokenRank(ctx, "usr_live_alice", "today", now)
	if err != nil || rank == nil || *rank != 4 {
		t.Fatalf("personal rank mismatch: %v %v", rank, err)
	}
	if _, err := db.Exec("UPDATE user_privacy_settings SET show_token_total=FALSE WHERE user_id='usr_live_alice'"); err != nil {
		t.Fatal(err)
	}
	board, err = st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil || len(board.Entries) != 4 || *board.TotalEntries != 4 {
		t.Fatalf("show_token_total must not affect membership: %+v %v", board, err)
	}
}

func TestRegisterJoinsZeroWindowScoresMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	seedTestUser(t, db, st, "usr_zero_private", "", "", "zero@live.test", false, now)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_window_scores WHERE user_id='usr_zero_private'`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("expected 4 window scores, got %d %v", count, err)
	}
	var tokens int
	var windows int
	if err := db.QueryRow(`SELECT SUM(token_total), COUNT(DISTINCT window_key) FROM user_window_scores WHERE user_id='usr_zero_private'`).Scan(&tokens, &windows); err != nil || tokens != 0 || windows != 4 {
		t.Fatalf("zero scores missing: tokens=%d windows=%d err=%v", tokens, windows, err)
	}
	var outbox int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ranking_outbox WHERE user_id='usr_zero_private' AND task_status='pending'`).Scan(&outbox); err != nil || outbox != 4 {
		t.Fatalf("expected 4 pending outbox tasks, got %d %v", outbox, err)
	}
	board, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if board.TotalParticipants == nil || *board.TotalParticipants != 1 {
		t.Fatalf("zero-token private user missing from totals: %+v", board)
	}
	if len(board.Entries) != 1 || board.Entries[0].MetricValue != "0" || board.Entries[0].RankNo != 1 {
		t.Fatalf("zero-token private user missing rank: %+v", board.Entries)
	}
	if board.Entries[0].DisplayName == "" || strings.Contains(board.Entries[0].DisplayName, "@") {
		t.Fatalf("display name leaked email: %+v", board.Entries[0])
	}
}

func TestLeaderboardPrivacyToggleDoesNotChangeTokensMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	seedTestUser(t, db, st, "usr_priv_a", "priv_a", "Priv A", "a@priv.test", true, now)
	seedTestUser(t, db, st, "usr_priv_b", "priv_b", "Priv B", "b@priv.test", false, now)
	seedRankUsage(t, db, "usr_priv_a", now.Format("2006-01-02"), "codex", 50, 0, 0)
	before, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Privacy().UpdatePrivacyTx(ctx, "usr_priv_a", domain.UserPrivacySettings{PublicProfileEnabled: false}, 0, domain.UserSecurityEvent{EventID: "evt_priv_a", UserID: strPtr("usr_priv_a"), EventType: "privacy_changed", CreatedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	after, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if *before.TotalParticipants != *after.TotalParticipants || len(before.Entries) != len(after.Entries) {
		t.Fatalf("privacy toggle changed membership: before=%+v after=%+v", before, after)
	}
	if before.Entries[0].MetricValue != after.Entries[0].MetricValue {
		t.Fatalf("privacy toggle changed tokens: %s -> %s", before.Entries[0].MetricValue, after.Entries[0].MetricValue)
	}
}

func strPtr(v string) *string { return &v }
