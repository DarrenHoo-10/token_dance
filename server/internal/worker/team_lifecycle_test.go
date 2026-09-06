package worker

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	"tokendance/internal/provider"
)

func seedTeamGraph(t *testing.T, db *sql.DB, userID, teamID, membershipID string, now time.Time) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO teams (
			team_id, name, timezone_name, owner_user_id, status, profile_version, auth_revision, created_at
		) VALUES (?, 'Team', 'UTC', ?, 'active', 1, 1, ?)`, teamID, userID, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_memberships (
			membership_id, team_id, user_id, base_role, sharing_version, joined_at
		) VALUES (?, ?, ?, 'admin', 1, ?)`, membershipID, teamID, userID, now); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_current_teams (user_id, team_id, membership_id, joined_at)
		VALUES (?, ?, ?, ?)`, userID, teamID, membershipID, now); err != nil {
		t.Fatalf("seed current team: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_source_revisions (team_id, source_revision, changed_at)
		VALUES (?, 0, ?)`, teamID, now); err != nil {
		t.Fatalf("seed source revision: %v", err)
	}
}

func TestTeamAnalysisClaimAuthDiscardAndSourceRefreshMySQL(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()
	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	userID := "usr_team_analysis_owner000001"
	teamID := "tem_team_analysis_00000000001"
	membershipID := "tmb_team_analysis_00000000001"
	seedDeletionUser(t, db, userID, "active")
	seedTeamGraph(t, db, userID, teamID, membershipID, now)
	if _, err := db.Exec(`
		INSERT INTO team_sharing_grants (
			grant_id, membership_id, dimension, starts_at, active_dimension
		) VALUES ('tgr_team_analysis_base0000001', ?, 'base', ?, 'base')`, membershipID, now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_analysis_snapshots (
			snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision,
			rule_version, status, active_request_key, as_of, next_attempt_at, expires_at
		) VALUES (
			'tas_team_analysis_00000000001', ?, '2026-09-01', '2026-09-07', 1, 0,
			'1', 'queued', 'analysis-key-1', ?, ?, ?
		)`, teamID, now, now, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	w := NewWorkerWithFull(db, clock.NewMockClock(now), nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	processed, err := w.ProcessTeamAnalysis(ctx)
	if err != nil {
		t.Fatalf("process analysis: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 published snapshot, got %d", processed)
	}
	var status string
	var published uint64
	var auth uint64
	if err := db.QueryRow(`SELECT status, published_generation, auth_revision FROM team_analysis_snapshots WHERE snapshot_id = 'tas_team_analysis_00000000001'`).
		Scan(&status, &published, &auth); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.SnapshotReady) || published == 0 || auth != 1 {
		t.Fatalf("published snapshot mismatch: status=%s gen=%d auth=%d", status, published, auth)
	}

	if _, err := db.Exec(`UPDATE teams SET auth_revision = 2 WHERE team_id = ?`, teamID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_analysis_snapshots (
			snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision,
			rule_version, status, active_request_key, as_of, next_attempt_at, expires_at
		) VALUES (
			'tas_team_analysis_00000000002', ?, '2026-09-01', '2026-09-07', 1, 0,
			'1', 'queued', 'analysis-key-stale', ?, ?, ?
		)`, teamID, now, now, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed stale snapshot: %v", err)
	}
	if _, err := w.ProcessTeamAnalysis(ctx); err != nil {
		t.Fatalf("process stale analysis: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM team_analysis_snapshots WHERE snapshot_id = 'tas_team_analysis_00000000002'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.SnapshotObsolete) {
		t.Fatalf("stale auth snapshot should be obsolete, got %s", status)
	}
}

func TestDeletionBarriersPersistAcrossFailureMySQL(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()
	resetDeletionTestSchema(t, db)

	now := time.Now().UTC()
	userID := "usr_team_barrier_owner0000001"
	teamID := "tem_team_barrier_000000000001"
	membershipID := "tmb_team_barrier_00000000001"
	seedDeletionUser(t, db, userID, "active")
	seedTeamGraph(t, db, userID, teamID, membershipID, now)
	seedDeletionUsage(t, db, userID, "ins_team_barrier_device000001", now)
	seedDeletionRequest(t, db, "del_team_barrier_req0000000001", userID, "", "all_usage", "{}", nil)
	if _, err := db.Exec(`CREATE TRIGGER inject_team_barrier_failure BEFORE DELETE ON usage_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected delete failure'`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	w := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	if _, err := w.ProcessDeletionRequests(context.Background()); err == nil {
		t.Fatal("expected injected deletion failure")
	}

	var blocked int
	var released sql.NullTime
	if err := db.QueryRow(`
		SELECT COUNT(*), MAX(released_at)
		FROM team_deletion_barriers
		WHERE deletion_request_id = 'del_team_barrier_req0000000001' AND team_id = ?`, teamID).
		Scan(&blocked, &released); err != nil {
		t.Fatalf("read barrier: %v", err)
	}
	if blocked != 1 || released.Valid {
		t.Fatalf("barrier must persist unreleased after crash/failure: count=%d released=%v", blocked, released)
	}

	if _, err := db.Exec("DROP TRIGGER inject_team_barrier_failure"); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := db.Exec("UPDATE data_deletion_requests SET next_attempt_at = CURRENT_TIMESTAMP(3) WHERE request_id = 'del_team_barrier_req0000000001'"); err != nil {
		t.Fatal(err)
	}
	if processed, err := w.ProcessDeletionRequests(context.Background()); err != nil || processed != 1 {
		t.Fatalf("retry after barrier: processed=%d err=%v", processed, err)
	}
	if err := db.QueryRow(`
		SELECT released_at FROM team_deletion_barriers
		WHERE deletion_request_id = 'del_team_barrier_req0000000001' AND team_id = ?`, teamID).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if !released.Valid {
		t.Fatal("successful completion must release every barrier")
	}
}

func TestNilDBTeamWorkerLoopsAreNoops(t *testing.T) {
	w := NewWorker(nil, clock.RealClock{})
	if n, err := w.ProcessTeamAnalysis(context.Background()); err != nil || n != 0 {
		t.Fatalf("nil db analysis: %d %v", n, err)
	}
	if n, err := w.ProcessTeamExports(context.Background()); err != nil || n != 0 {
		t.Fatalf("nil db export: %d %v", n, err)
	}
	if err := w.ProcessTeamCleanup(context.Background()); err != nil {
		t.Fatalf("nil db cleanup: %v", err)
	}
}
