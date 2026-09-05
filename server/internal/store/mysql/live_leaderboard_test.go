package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
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
	for window, total := range map[string]string{"today": "500", "7d": "550", "30d": "10550", "all": "15550"} {
		board, err := st.Leaderboard().GetLeaderboard(ctx, "global", window, "tokens", nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		if *board.TotalTokens != total || *board.TotalEntries != 2 || len(board.Entries) != 1 || board.NextCursor == nil {
			t.Fatalf("%s: %+v", window, board)
		}
		if board.Entries[0].Handle != "bob" || board.Entries[0].RankNo != 1 {
			t.Fatalf("incorrect rank: %+v", board.Entries)
		}
		page, err := st.Leaderboard().GetLeaderboard(ctx, "global", window, "tokens", board.NextCursor, 1)
		if err != nil || len(page.Entries) != 1 || page.Entries[0].Handle != "alice" || page.Entries[0].RankNo != 2 || page.NextCursor != nil {
			t.Fatalf("pagination: %+v %v", page, err)
		}
	}
	if _, err := db.Exec("UPDATE user_privacy_settings SET public_profile_enabled=FALSE WHERE user_id='usr_live_bob'"); err != nil {
		t.Fatal(err)
	}
	board, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil || len(board.Entries) != 1 || board.Entries[0].RankNo != 1 || *board.TotalTokens != "200" {
		t.Fatalf("privacy withdrawal: %+v %v", board, err)
	}
	rank, _, err := (&leaderboardStore{db: db}).liveTokenRank(ctx, "usr_live_alice", "today", now)
	if err != nil || rank == nil || *rank != 1 {
		t.Fatalf("personal rank mismatch: %v %v", rank, err)
	}
	if _, err := db.Exec("UPDATE user_privacy_settings SET show_token_total=FALSE WHERE user_id='usr_live_alice'"); err != nil {
		t.Fatal(err)
	}
	board, err = st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil || len(board.Entries) != 0 || *board.TotalEntries != 0 || *board.TotalTokens != "0" {
		t.Fatalf("empty board: %+v %v", board, err)
	}
}
