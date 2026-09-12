package grayscale

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"tokendance/internal/migrate"
	mysqlstore "tokendance/internal/store/mysql"
)

func TestMirrorReconcilesHistoryMySQL(t *testing.T) {
	sourceDSN, targetDSN := os.Getenv("TOKENDANCE_TEST_MIRROR_SOURCE_DSN"), os.Getenv("TOKENDANCE_TEST_MYSQL_DSN")
	if sourceDSN == "" || targetDSN == "" {
		t.Skip("isolated source and target MySQL DSNs required")
	}
	sc, err := parseDSN(sourceDSN)
	if err != nil {
		t.Fatal("invalid source test DSN")
	}
	tc, err := parseDSN(targetDSN)
	if err != nil {
		t.Fatal("invalid target test DSN")
	}
	// This test resets both schemas. Never run it against deployed databases.
	for _, name := range []string{sc.DBName, tc.DBName} {
		if len(name) < len("tokendance_legacy_cr_") || name[:len("tokendance_legacy_cr_")] != "tokendance_legacy_cr_" {
			t.Fatal("dedicated legacy review schemas required")
		}
	}
	ctx := context.Background()
	open := func(dsn string) *sql.DB {
		t.Helper()
		db, err := sql.Open("mysql", mysqlstore.NormalizeDSN(dsn))
		if err != nil {
			t.Fatal("open isolated MySQL")
		}
		t.Cleanup(func() { db.Close() })
		runner := migrate.NewRunner(db)
		if err := runner.ResetCleanSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := runner.RunMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		return db
	}
	source, target := open(sourceDSN), open(targetDSN)
	now := time.Now().UTC().Truncate(time.Millisecond)
	user := "usr_mirror_history"
	exec := func(db *sql.DB, query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(source, `INSERT INTO users (user_id,auth_subject_hash,display_name,handle,created_at) VALUES (?,UNHEX(SHA2('source',256)),'Mirror Test','mirror_test',?)`, user, now)
	exec(source, `INSERT INTO user_window_scores (user_id,window_key,generation,token_total,revision,registered_at) VALUES (?,'all',?,300,1,?)`, user, now.Format("2006-01-02"), now)
	for _, day := range []string{"2026-01-01", "2026-08-01"} {
		exec(source, `INSERT INTO daily_user_agent_metrics (metric_date,user_id,agent_id,exact_token_total,aggregation_version,computed_at,updated_at) VALUES (?,?,'codex',150,2,?,?)`, day, user, now, now)
	}
	m, err := New(Config{SourceDSN: sourceDSN, TargetDSN: targetDSN, SourceSchema: sc.DBName, TargetSchema: tc.DBName, AllowTarget: true}, source, target)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return now }
	run := func() {
		t.Helper()
		r, err := m.Run(ctx)
		if err != nil || r.Copied["daily_user_agent_metrics"] != 2 {
			t.Fatalf("copy=%+v err=%v", r.Copied, err)
		}
	}
	run()
	exec(target, `UPDATE users SET email_lookup_hash=UNHEX(SHA2('local-email',256)),email_ciphertext='local-cipher' WHERE user_id=?`, user)
	exec(target, `INSERT INTO teams (team_id,name,timezone_name,owner_user_id,status,created_at) VALUES ('tem_mirror','Mirror','UTC',?,'active',?)`, user, now)
	exec(target, `INSERT INTO team_memberships (membership_id,team_id,user_id,base_role,joined_at) VALUES ('tmb_mirror','tem_mirror',?,'admin',?)`, user, now)
	exec(target, `INSERT INTO user_current_teams (user_id,team_id,membership_id,joined_at) VALUES (?,'tem_mirror','tmb_mirror',?)`, user, now)
	// State already says this cohort was copied; historical damage must heal.
	exec(target, `DELETE FROM daily_user_agent_metrics WHERE metric_date='2026-01-01'`)
	run()
	var count, rev int
	if err := target.QueryRow(`SELECT COUNT(*) FROM daily_user_agent_metrics`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("history=%d err=%v", count, err)
	}
	if err := target.QueryRow(`SELECT source_revision FROM team_source_revisions WHERE team_id='tem_mirror'`).Scan(&rev); err != nil || rev != 1 {
		t.Fatalf("revision=%d err=%v", rev, err)
	}
	run()
	if err := target.QueryRow(`SELECT source_revision FROM team_source_revisions WHERE team_id='tem_mirror'`).Scan(&rev); err != nil || rev != 1 {
		t.Fatalf("unchanged mirror invalidated cache: revision=%d err=%v", rev, err)
	}
	var local string
	if err := target.QueryRow(`SELECT email_ciphertext FROM users WHERE user_id=?`, user).Scan(&local); err != nil || local != "local-cipher" {
		t.Fatal("test login overwritten")
	}
	exec(source, `DELETE FROM daily_user_agent_metrics WHERE metric_date='2026-01-01'`)
	if _, err := m.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(`SELECT COUNT(*) FROM daily_user_agent_metrics`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("removed source summary resurrected: %d err=%v", count, err)
	}
	if err := target.QueryRow(`SELECT source_revision FROM team_source_revisions WHERE team_id='tem_mirror'`).Scan(&rev); err != nil || rev != 2 {
		t.Fatalf("source removal not invalidated: %d err=%v", rev, err)
	}
}
