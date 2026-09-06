package worker

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	"tokendance/internal/provider"
)

func TestTeamCleanupExpiresInvitationsAndReceiptsMySQL(t *testing.T) {
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

	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	ownerID := "usr_team_cleanup_owner"
	seedDeletionUser(t, db, ownerID, "active")
	if _, err := db.Exec(`
		INSERT INTO teams (
			team_id, name, timezone_name, owner_user_id, status, profile_version, auth_revision, created_at
		) VALUES ('tem_cleanup_team_000000000001', 'Cleanup', 'UTC', ?, 'active', 1, 1, ?)`, ownerID, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_memberships (
			membership_id, team_id, user_id, base_role, sharing_version, joined_at
		) VALUES ('tmb_cleanup_owner_00000000001', 'tem_cleanup_team_000000000001', ?, 'admin', 1, ?)`, ownerID, now); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_invitations (
			invitation_id, team_id, inviter_user_id, invited_role, recipient_lookup_hash,
			lookup_key_version, recipient_ciphertext, encryption_key_version, status,
			active_recipient_hash, created_at, expires_at, version
		) VALUES (
			'tiv_cleanup_invite_00000000001', 'tem_cleanup_team_000000000001', ?, 'member',
			UNHEX(SHA2('invitee@example.com', 256)), 1, 'cipher', 1, 'pending',
			UNHEX(SHA2('invitee@example.com', 256)), ?, ?, 1
		)`, ownerID, now.Add(-8*24*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed invitation: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_command_receipts (
			actor_user_id, operation_scope, idempotency_key_hash, request_hash,
			result_type, result_id, created_at, expires_at
		) VALUES (?, 'teams.create', UNHEX(SHA2('receipt', 256)), UNHEX(SHA2('req', 256)),
		          'team', 'tem_cleanup_team_000000000001', ?, ?)`, ownerID, now.Add(-8*24*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}

	w := NewWorkerWithFull(db, clock.NewMockClock(now), nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	if err := w.ProcessTeamCleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var status string
	var activeHash []byte
	if err := db.QueryRow(`SELECT status, active_recipient_hash FROM team_invitations WHERE invitation_id = 'tiv_cleanup_invite_00000000001'`).
		Scan(&status, &activeHash); err != nil {
		t.Fatalf("read invitation: %v", err)
	}
	if status != string(domain.InvitationExpired) || len(activeHash) != 0 {
		t.Fatalf("invitation was not expired logically: status=%s hash=%x", status, activeHash)
	}
	var receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM team_command_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 {
		t.Fatalf("expired receipts remained: %d", receipts)
	}
}

func TestTeamCleanupConstantsMatchSpec(t *testing.T) {
	if teamSnapshotReadyTTL != 30*time.Minute {
		t.Fatalf("ready snapshot ttl %s", teamSnapshotReadyTTL)
	}
	if teamExportObjectTTL != 24*time.Hour {
		t.Fatalf("export object ttl %s", teamExportObjectTTL)
	}
	if teamTerminalCipherRetention != 30*24*time.Hour {
		t.Fatalf("cipher retention %s", teamTerminalCipherRetention)
	}
}

func TestTeamCleanupKeepsSnapshotsReferencedByExportsMySQL(t *testing.T) {
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

	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	ownerID := "usr_team_cleanup_export_owner"
	teamID := "tem_cleanup_export_00000000001"
	membershipID := "tmb_cleanup_export_0000000001"
	seedDeletionUser(t, db, ownerID, "active")
	seedTeamGraph(t, db, ownerID, teamID, membershipID, now)

	if _, err := db.Exec(`
		INSERT INTO team_analysis_snapshots (
			snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision,
			rule_version, status, active_request_key, as_of, next_attempt_at, expires_at
		) VALUES
			('tas_cleanup_ref_00000000000001', ?, '2026-09-01', '2026-09-07', 1, 1, '1', 'expired', NULL, ?, ?, ?),
			('tas_cleanup_free_0000000000001', ?, '2026-09-01', '2026-09-07', 1, 1, '1', 'expired', NULL, ?, ?, ?)`,
		teamID, now, now, now.Add(-time.Hour),
		teamID, now, now, now.Add(-time.Hour),
	); err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO team_export_jobs (
			export_id, team_id, requester_user_id, requester_membership_id, snapshot_id,
			auth_revision, export_kind, filter_json, status, created_at, expires_at
		) VALUES (
			'txj_cleanup_fail_0000000000001', ?, ?, ?, 'tas_cleanup_ref_00000000000001',
			1, 'daily', CAST('{}' AS JSON), 'failed', ?, ?
		)`, teamID, ownerID, membershipID, now.Add(-time.Hour), now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed export: %v", err)
	}

	w := NewWorkerWithFull(db, clock.NewMockClock(now), nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	if err := w.ProcessTeamCleanup(ctx); err != nil {
		t.Fatalf("cleanup while export still referenced: %v", err)
	}

	var referenced, free int
	if err := db.QueryRow(`SELECT COUNT(*) FROM team_analysis_snapshots WHERE snapshot_id = 'tas_cleanup_ref_00000000000001'`).Scan(&referenced); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM team_analysis_snapshots WHERE snapshot_id = 'tas_cleanup_free_0000000000001'`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if referenced != 1 {
		t.Fatalf("referenced snapshot should remain while export metadata exists, count=%d", referenced)
	}
	if free != 0 {
		t.Fatalf("unreferenced expired snapshot should be deleted, count=%d", free)
	}

	if _, err := db.Exec(`
		UPDATE team_export_jobs
		SET created_at = ?
		WHERE export_id = 'txj_cleanup_fail_0000000000001'`, now.Add(-31*24*time.Hour)); err != nil {
		t.Fatalf("age export metadata: %v", err)
	}
	if err := w.ProcessTeamCleanup(ctx); err != nil {
		t.Fatalf("cleanup after export metadata retention: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM team_export_jobs WHERE export_id = 'txj_cleanup_fail_0000000000001'`).Scan(&referenced); err != nil {
		t.Fatal(err)
	}
	if referenced != 0 {
		t.Fatalf("aged export metadata should be deleted, count=%d", referenced)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM team_analysis_snapshots WHERE snapshot_id = 'tas_cleanup_ref_00000000000001'`).Scan(&referenced); err != nil {
		t.Fatal(err)
	}
	if referenced != 0 {
		t.Fatalf("snapshot should delete after export metadata is gone, count=%d", referenced)
	}
}
