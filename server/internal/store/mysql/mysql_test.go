package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"tokendance/internal/analytics"
	"tokendance/internal/auth"
	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/device"
	"tokendance/internal/domain"
	"tokendance/internal/export"
	"tokendance/internal/httpapi"
	"tokendance/internal/leaderboard"
	"tokendance/internal/media"
	"tokendance/internal/migrate"
	"tokendance/internal/privacy"
	"tokendance/internal/profile"
	"tokendance/internal/provider"
	"tokendance/internal/search"
	"tokendance/internal/store"
)

func getTestStore(t *testing.T) (*Store, *sql.DB, func()) {
	t.Helper()
	dsn := os.Getenv("TOKENDANCE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("skipping MySQL repository integration test: TOKENDANCE_TEST_MYSQL_DSN not set")
	}

	db, err := OpenDB(dsn, DefaultDBConfig())
	if err != nil {
		t.Fatalf("failed to connect to test MySQL: %v", err)
	}

	ctx := context.Background()
	// Acquire test lock to avoid concurrent schema reset when multiple test packages run
	_, _ = db.ExecContext(ctx, "SELECT GET_LOCK('tokendance_global_test_lock', 60)")

	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	st := NewStore(db)

	cleanup := func() {
		_ = runner.ResetCleanSchema(context.Background())
		_, _ = db.ExecContext(context.Background(), "SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		_ = db.Close()
	}

	return st, db, cleanup
}

func TestMySQL_AuthStoreLifecycle(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	now := time.Now().UTC()

	email := "alex@example.com"
	emailHash := crypto.SHA256([]byte(email))
	codeHash := crypto.SHA256([]byte("123456"))

	challenge := domain.EmailChallenge{
		ChallengeID:     "emc_01testauth",
		EmailLookupHash: emailHash,
		EmailCiphertext: []byte("encrypted:" + email),
		EmailKeyVersion: 1,
		ChallengeType:   domain.ChallengeTypeRegister,
		CodeHash:        codeHash,
		CodeKeyVersion:  1,
		ChallengeStatus: domain.ChallengeStatusPending,
		AttemptCount:    0,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(10 * time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	outbox := domain.EmailOutbox{
		EmailID:              "emb_01testauth",
		ChallengeID:          &challenge.ChallengeID,
		IdempotencyKey:       crypto.SHA256([]byte("idemp_01")),
		TemplateKey:          "auth_code",
		Locale:               "en-US",
		RecipientCiphertext:  []byte("encrypted:" + email),
		PayloadCiphertext:    []byte("{\"code\":\"123456\"}"),
		EncryptionKeyVersion: 1,
		DeliveryStatus:       "pending",
		AttemptCount:         0,
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(24 * time.Hour),
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	// 1. Create email challenge
	createdCh, err := auth.CreateOrReplaceEmailChallenge(ctx, challenge, outbox)
	if err != nil {
		t.Fatalf("failed to create email challenge: %v", err)
	}
	if createdCh.ChallengeID != challenge.ChallengeID {
		t.Fatalf("unexpected challenge ID: %s", createdCh.ChallengeID)
	}

	// 2. Find pending challenge
	foundCh, err := auth.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		t.Fatalf("failed to find pending challenge: %v", err)
	}
	if foundCh.ChallengeID != challenge.ChallengeID {
		t.Fatalf("expected challenge ID %s, got %s", challenge.ChallengeID, foundCh.ChallengeID)
	}

	// 3. Update attempt count
	if err := auth.UpdateEmailChallengeAttempts(ctx, challenge.ChallengeID, 1, domain.ChallengeStatusPending); err != nil {
		t.Fatalf("failed to update attempts: %v", err)
	}

	// 4. Complete registration transaction
	userID := "usr_01testauthuser"
	sessTokenHash := crypto.SHA256([]byte("session_token_raw"))
	csrfTokenHash := crypto.SHA256([]byte("csrf_token_raw"))

	regInput := store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("auth_sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte("encrypted:" + email),
			DisplayName:           "Alex Dancer",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "America/New_York",
			Locale:                "en-US",
			ProfileVersion:        1,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      "argon2id$v=19$m=65536,t=3,p=2$hash",
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
			PasswordChangedAt: now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		Privacy: domain.UserPrivacySettings{
			UserID:               userID,
			PublicProfileEnabled: false,
			PrivacyVersion:       1,
			CreatedAt:            now,
			UpdatedAt:            now,
		},
		Session: domain.UserSession{
			SessionID:         "ses_01testauthsess",
			UserID:            userID,
			SessionTokenHash:  sessTokenHash,
			CSRFTokenHash:     csrfTokenHash,
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			LastSeenAt:        now,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(30 * 24 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: challenge.ChallengeID,
		SecurityEvent: domain.UserSecurityEvent{
			EventID:   "evt_01testreg",
			UserID:    &userID,
			EventType: "user.register",
			Outcome:   "success",
			CreatedAt: now,
		},
	}

	sess, err := auth.CompleteRegistrationTx(ctx, regInput)
	if err != nil {
		t.Fatalf("failed to complete registration tx: %v", err)
	}
	if sess.SessionID != "ses_01testauthsess" {
		t.Fatalf("unexpected session ID: %s", sess.SessionID)
	}

	// 5. Resolve session
	resSess, resUser, err := auth.ResolveSession(ctx, sessTokenHash, now)
	if err != nil {
		t.Fatalf("failed to resolve session: %v", err)
	}
	if resUser.UserID != userID || resSess.SessionID != sess.SessionID {
		t.Fatalf("resolved session or user mismatch: user=%s, sess=%s", resUser.UserID, resSess.SessionID)
	}

	// 6. List user sessions
	sessions, err := auth.ListUserSessions(ctx, userID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d, err: %v", len(sessions), err)
	}

	// 7. Revoke session
	if err := auth.RevokeSession(ctx, sess.SessionID, "logout", now); err != nil {
		t.Fatalf("failed to revoke session: %v", err)
	}

	// Verify resolved session now fails
	_, _, err = auth.ResolveSession(ctx, sessTokenHash, now)
	if err != domain.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized after revoking session, got: %v", err)
	}
}

func TestUSR101_ExportIdempotencyAndKeyRotationMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	userID := "usr_export_rotation"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (user_id, auth_subject_hash, display_name, account_status, leaderboard_visibility, timezone_name, locale, created_at, updated_at)
		VALUES (?, UNHEX(SHA2('export-rotation', 256)), 'Export Rotation', 'active', 'private', 'UTC', 'en-US', ?, ?)`, userID, now, now); err != nil {
		t.Fatalf("seed export rotation user: %v", err)
	}

	oldKey := []byte("old-idempotency-key-material-00001")
	newKey := []byte("new-idempotency-key-material-00002")
	cfgV1 := config.DefaultConfig()
	cfgV1.IdempotencyKeys = config.VersionedKeyring{CurrentVersion: 1, Keys: map[uint16][]byte{1: oldKey}}
	cfgV2 := config.DefaultConfig()
	cfgV2.IdempotencyKeys = config.VersionedKeyring{CurrentVersion: 2, Keys: map[uint16][]byte{1: oldKey, 2: newKey}}
	clk := clock.NewMockClock(now)
	storage := provider.NewMemoryObjectStorage("")
	beforeRotation := export.NewServiceWithConfig(st, cfgV1, clk, storage)
	afterRotation := export.NewServiceWithConfig(st, cfgV2, clk, storage)
	input := export.CreateExportInput{IdempotencyKey: "rotation-retry", Scope: "summary", Format: "csv", Filter: map[string]interface{}{"range": "30d"}}

	first, err := beforeRotation.CreateJob(ctx, userID, input)
	if err != nil {
		t.Fatalf("create export before rotation: %v", err)
	}
	retried, err := afterRotation.CreateJob(ctx, userID, input)
	if err != nil {
		t.Fatalf("retry export after rotation: %v", err)
	}
	if retried.ExportID != first.ExportID {
		t.Fatalf("rotation retry created a different job: first=%s retried=%s", first.ExportID, retried.ExportID)
	}
	var count, storedLength int
	var storedKey string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(CHAR_LENGTH(idempotency_key)), MAX(idempotency_key) FROM data_export_jobs WHERE user_id = ?`, userID).Scan(&count, &storedLength, &storedKey); err != nil {
		t.Fatalf("inspect rotated export job: %v", err)
	}
	if count != 1 || storedLength > 64 || !strings.HasPrefix(storedKey, "v1:") {
		t.Fatalf("unexpected compact idempotency storage: count=%d length=%d key=%s", count, storedLength, storedKey)
	}
}

func TestMySQL_ProfileAndPrivacyLifecycle(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	profile := st.Profile()
	privacy := st.Privacy()
	now := time.Now().UTC()

	userID := "usr_02profileuser"
	email := "bob@example.com"
	emailHash := crypto.SHA256([]byte(email))
	sessTokenHash := crypto.SHA256([]byte("sess_token_02"))
	csrfTokenHash := crypto.SHA256([]byte("csrf_token_02"))

	ch := domain.EmailChallenge{
		ChallengeID:     "emc_02testprofile",
		EmailLookupHash: emailHash,
		EmailCiphertext: []byte("encrypted:" + email),
		EmailKeyVersion: 1,
		ChallengeType:   domain.ChallengeTypeRegister,
		CodeHash:        crypto.SHA256([]byte("654321")),
		CodeKeyVersion:  1,
		ChallengeStatus: domain.ChallengeStatusPending,
		AttemptCount:    0,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(10 * time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	outbox := domain.EmailOutbox{
		EmailID:              "emb_02testprofile",
		ChallengeID:          &ch.ChallengeID,
		IdempotencyKey:       crypto.SHA256([]byte("idemp_02")),
		TemplateKey:          "auth_code",
		Locale:               "en-US",
		RecipientCiphertext:  []byte("encrypted:" + email),
		PayloadCiphertext:    []byte("{}"),
		EncryptionKeyVersion: 1,
		DeliveryStatus:       "pending",
		AttemptCount:         0,
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(24 * time.Hour),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := auth.CreateOrReplaceEmailChallenge(ctx, ch, outbox); err != nil {
		t.Fatalf("failed to create challenge: %v", err)
	}

	_, err := auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte("encrypted:" + email),
			DisplayName:           "Bob Builder",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			ProfileVersion:        1,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      "hash",
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
			PasswordChangedAt: now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		Privacy: domain.UserPrivacySettings{
			UserID:         userID,
			PrivacyVersion: 1,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		Session: domain.UserSession{
			SessionID:         "ses_02testprofilesess",
			UserID:            userID,
			SessionTokenHash:  sessTokenHash,
			CSRFTokenHash:     csrfTokenHash,
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			LastSeenAt:        now,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(30 * 24 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: ch.ChallengeID,
	})
	if err != nil {
		t.Fatalf("failed registration: %v", err)
	}

	// 1. Check handle availability
	avail, err := profile.IsHandleAvailable(ctx, "bob_the_builder", "", now)
	if err != nil || !avail {
		t.Fatalf("expected handle bob_the_builder to be available, got: %v", err)
	}

	// 2. Complete onboarding
	u, priv, err := profile.CompleteOnboardingTx(ctx, userID, "bob_the_builder", "Bob The Builder", "America/Chicago", "en-US", domain.UserPrivacySettings{
		PublicProfileEnabled: true,
		ShowBio:              true,
		ShowTokenTotal:       true,
		ShowTrends:           true,
		ShowActivityCalendar: true,
		ShowAgentBreakdown:   true,
		ShowSkillRanking:     true,
		ShowAchievements:     true,
	}, domain.UserSecurityEvent{EventID: "evt_onb_01", UserID: &userID, EventType: "user.onboarding_completed", Outcome: "success", CreatedAt: now}, now)

	if err != nil {
		t.Fatalf("onboarding failed: %v", err)
	}
	if u.Handle == nil || *u.Handle != "bob_the_builder" {
		t.Fatalf("handle not set properly: %v", u.Handle)
	}
	if !priv.PublicProfileEnabled {
		t.Fatalf("expected privacy public profile enabled")
	}

	// 3. Query public profile projection
	pub, err := privacy.GetPublicProfileByHandle(ctx, "bob_the_builder", now)
	if err != nil {
		t.Fatalf("failed to get public profile: %v", err)
	}
	if pub.DisplayName != "Bob The Builder" {
		t.Fatalf("unexpected display name in public profile: %s", pub.DisplayName)
	}

	// 4. Update profile (handle change)
	newHandle := "bob_prime"
	updatedUser, err := profile.UpdateProfileTx(ctx, userID, nil, &newHandle, nil, nil, nil, u.ProfileVersion, domain.UserSecurityEvent{EventID: "evt_prof_upd", UserID: &userID, EventType: "user.profile_updated", Outcome: "success", CreatedAt: now}, now)
	if err != nil {
		t.Fatalf("failed to update handle: %v", err)
	}
	if *updatedUser.Handle != "bob_prime" {
		t.Fatalf("expected new handle bob_prime, got: %s", *updatedUser.Handle)
	}

	// 5. Test redirect handle
	redirectTarget, err := profile.GetRedirectHandle(ctx, "bob_the_builder", now)
	if err != nil {
		t.Fatalf("failed to get redirect handle: %v", err)
	}
	if redirectTarget != "bob_prime" {
		t.Fatalf("expected redirect to bob_prime, got: %s", redirectTarget)
	}
}

func TestMySQL_DeviceAndExportLifecycle(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	dev := st.Device()
	exp := st.Export()
	now := time.Now().UTC()

	userID := "usr_03devuser"
	sessTokenHash := crypto.SHA256([]byte("sess_03"))
	csrfTokenHash := crypto.SHA256([]byte("csrf_03"))
	emailHash := crypto.SHA256([]byte("dev@example.com"))

	ch := domain.EmailChallenge{
		ChallengeID:     "emc_03dev",
		EmailLookupHash: emailHash,
		EmailCiphertext: []byte("encrypted:dev@example.com"),
		EmailKeyVersion: 1,
		ChallengeType:   domain.ChallengeTypeRegister,
		CodeHash:        crypto.SHA256([]byte("111222")),
		CodeKeyVersion:  1,
		ChallengeStatus: domain.ChallengeStatusPending,
		AttemptCount:    0,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(10 * time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	outbox := domain.EmailOutbox{
		EmailID:              "emb_03dev",
		ChallengeID:          &ch.ChallengeID,
		IdempotencyKey:       crypto.SHA256([]byte("idemp_03")),
		TemplateKey:          "auth_code",
		Locale:               "en-US",
		RecipientCiphertext:  []byte("dev@example.com"),
		PayloadCiphertext:    []byte("{}"),
		EncryptionKeyVersion: 1,
		DeliveryStatus:       "pending",
		AttemptCount:         0,
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(24 * time.Hour),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := auth.CreateOrReplaceEmailChallenge(ctx, ch, outbox); err != nil {
		t.Fatalf("failed to create challenge: %v", err)
	}

	_, err := auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte("encrypted:dev@example.com"),
			DisplayName:           "Dev Tester",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			ProfileVersion:        1,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      "hash",
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
			PasswordChangedAt: now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		Privacy: domain.UserPrivacySettings{
			UserID:         userID,
			PrivacyVersion: 1,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		Session: domain.UserSession{
			SessionID:         "ses_03dev",
			UserID:            userID,
			SessionTokenHash:  sessTokenHash,
			CSRFTokenHash:     csrfTokenHash,
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			LastSeenAt:        now,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(30 * 24 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: ch.ChallengeID,
	})
	if err != nil {
		t.Fatalf("failed registration: %v", err)
	}

	// Complete onboarding so user is ready to bind devices
	prof := st.Profile()
	_, _, err = prof.CompleteOnboardingTx(ctx, userID, "alexdev", "Alex Developer", "UTC", "en-US", domain.UserPrivacySettings{UserID: userID}, domain.UserSecurityEvent{EventID: "sev_onb", CreatedAt: now}, now)
	if err != nil {
		t.Fatalf("failed onboarding: %v", err)
	}

	// 1. Create binding challenge
	bindingCodeHash := crypto.SHA256([]byte("ABCD2345"))
	sKey := "ses_03dev"
	bChallenge := domain.DeviceBindingChallenge{
		ChallengeID:      "dbc_01test",
		UserID:           userID,
		SessionID:        "ses_03dev",
		CodeLookupHash:   bindingCodeHash,
		CodeKeyVersion:   1,
		ChallengeStatus:  domain.ChallengeStatusPending,
		ExpiresAt:        now.Add(5 * time.Minute),
		ActiveSessionKey: &sKey,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	_, err = dev.CreateBindingChallenge(ctx, bChallenge)
	if err != nil {
		t.Fatalf("failed to create binding challenge: %v", err)
	}

	// 2. Claim installation
	devPubKey := crypto.SHA256([]byte("device_pub_key_01"))
	devName := "Alex's MacBook"
	inst := domain.Installation{
		InstallationID:   "ins_01test",
		UserID:           userID,
		DevicePublicKey:  devPubKey,
		DeviceName:       &devName,
		OSType:           "macos",
		Architecture:     "arm64",
		CollectorVersion: "1.0.0",
	}

	claimedInst, err := dev.ClaimInstallationTx(ctx, bindingCodeHash, inst, now)
	if err != nil {
		t.Fatalf("failed to claim installation: %v", err)
	}
	if claimedInst.InstallationID != "ins_01test" {
		t.Fatalf("unexpected installation ID: %s", claimedInst.InstallationID)
	}

	// 3. Pause & Resume device
	pausedInst, err := dev.PauseInstallation(ctx, "ins_01test", userID, "user_paused", now)
	if err != nil || pausedInst.InstallationStatus != domain.InstallationStatusDisabled {
		t.Fatalf("expected disabled status after pause, got %s, err: %v", pausedInst.InstallationStatus, err)
	}

	resumedInst, err := dev.ResumeInstallation(ctx, "ins_01test", userID, now)
	if err != nil || resumedInst.InstallationStatus != domain.InstallationStatusActive {
		t.Fatalf("expected active status after resume, got %s, err: %v", resumedInst.InstallationStatus, err)
	}

	// 4. Create Export Job
	expJob := domain.DataExportJob{
		ExportID:       "exp_01test",
		UserID:         userID,
		IdempotencyKey: "export_idemp_key_01",
		RequestHash:    crypto.SHA256([]byte("summary:csv:{}")),
		ExportScope:    "summary",
		ExportFormat:   "csv",
		JobStatus:      domain.ExportJobStatusPending,
		AttemptCount:   0,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	createdJob, err := exp.CreateJob(ctx, expJob, []string{expJob.IdempotencyKey})
	if err != nil {
		t.Fatalf("failed to create export job: %v", err)
	}
	if createdJob.ExportID != "exp_01test" {
		t.Fatalf("unexpected export ID: %s", createdJob.ExportID)
	}

	jobs, err := exp.ListJobs(ctx, userID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("expected 1 export job, got %d, err: %v", len(jobs), err)
	}
}

func seedTestUser(t *testing.T, db *sql.DB, st *Store, userID, handle, displayName, email string, public bool, now time.Time) {
	t.Helper()
	ctx := context.Background()
	emailHash := crypto.SHA256([]byte(email))
	sessTokenHash := crypto.SHA256([]byte("sess_" + userID))
	csrfTokenHash := crypto.SHA256([]byte("csrf_" + userID))

	visibility := domain.LeaderboardVisibilityPrivate
	if public {
		visibility = domain.LeaderboardVisibilityPublic
	}

	_, err := st.Auth().CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("auth_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte("encrypted:" + email),
			DisplayName:           displayName,
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: visibility,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			ProfileVersion:        1,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      "hash",
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
			PasswordChangedAt: now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		Privacy: domain.UserPrivacySettings{
			UserID:               userID,
			PublicProfileEnabled: public,
			PrivacyVersion:       1,
			CreatedAt:            now,
			UpdatedAt:            now,
		},
		Session: domain.UserSession{
			SessionID:         "ses_" + userID,
			UserID:            userID,
			SessionTokenHash:  sessTokenHash,
			CSRFTokenHash:     csrfTokenHash,
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			LastSeenAt:        now,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(30 * 24 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	})
	if err != nil {
		t.Fatalf("failed to seed user %s: %v", userID, err)
	}

	if handle != "" {
		_, _, err = st.Profile().CompleteOnboardingTx(ctx, userID, handle, displayName, "UTC", "en-US", domain.UserPrivacySettings{
			PublicProfileEnabled: public,
			ShowBio:              public,
			ShowTokenTotal:       public,
			ShowTrends:           public,
			ShowActivityCalendar: public,
			ShowAgentBreakdown:   public,
			ShowSkillRanking:     public,
			ShowAchievements:     public,
		}, domain.UserSecurityEvent{EventID: "evt_seed_" + userID, UserID: &userID, EventType: "user.onboarding_completed", Outcome: "success", CreatedAt: now}, now)
		if err != nil {
			t.Fatalf("failed to complete onboarding for %s: %v", userID, err)
		}
	}
}

func TestUSR011_TenMetricsMySQLSupportedVsZero(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	// User A: Version 2 complete metrics
	userA := "usr_metric_a"
	seedTestUser(t, db, st, userA, "user_a", "User A", "user_a@tokendance.dev", true, now)

	// User B: Version 1 metrics without extensions (null columns)
	userB := "usr_metric_b"
	seedTestUser(t, db, st, userB, "user_b", "User B", "user_b@tokendance.dev", true, now)

	// User C: Zero metrics (no rows)
	userC := "usr_metric_c"
	seedTestUser(t, db, st, userC, "user_c", "User C", "user_c@tokendance.dev", true, now)

	// Insert User A rows into daily_user_agent_metrics (agg_version = 2)
	metricDateStr := "2026-08-29"
	_, err := db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			code_generated_lines, active_duration_ms, message_count, user_message_count,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at
		) VALUES (
			?, ?, 'claude-code', 1000000, 500000,
			800000, 400000, 300000, 0, 50000,
			2500, 3600000, 120, 45,
			12.50000000, 'USD', 100, 2, ?
		)`, metricDateStr, userA, now)
	if err != nil {
		t.Fatalf("failed to insert user A metrics: %v", err)
	}

	// Insert User B rows into daily_user_agent_metrics (agg_version = 1, extensions NULL)
	_, err = db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			code_generated_lines, active_duration_ms, message_count, user_message_count,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at
		) VALUES (
			?, ?, 'cursor', 500000, 200000,
			NULL, NULL, NULL, NULL, NULL,
			1000, NULL, NULL, NULL,
			5.00000000, 'USD', 101, 1, ?
		)`, metricDateStr, userB, now)
	if err != nil {
		t.Fatalf("failed to insert user B metrics: %v", err)
	}

	r := domain.TimeRange{
		Key:      domain.TimeRange30d,
		From:     now.AddDate(0, 0, -30),
		To:       now,
		Timezone: "UTC",
	}

	// 1. Verify User A (Agg version 2 -> supported=true with correct formula outputs)
	sumA, err := st.Analytics().GetPersonalSummary(ctx, userA, r)
	if err != nil {
		t.Fatalf("failed to get personal summary for user A: %v", err)
	}

	if sumA.Metrics.TotalTokens.Value == nil || *sumA.Metrics.TotalTokens.Value != "1500000" {
		t.Errorf("expected user A total tokens 1500000, got %v", sumA.Metrics.TotalTokens.Value)
	}
	if sumA.Metrics.GeneratedCodeLines.Value == nil || *sumA.Metrics.GeneratedCodeLines.Value != "2500" {
		t.Errorf("expected user A code lines 2500, got %v", sumA.Metrics.GeneratedCodeLines.Value)
	}
	if sumA.Metrics.TokensPerCodeLine.Value == nil || *sumA.Metrics.TokensPerCodeLine.Value != "600.00" {
		t.Errorf("expected user A tokens per code line 600.00, got %v", sumA.Metrics.TokensPerCodeLine.Value)
	}
	if !sumA.Metrics.InputContextTokens.Supported || sumA.Metrics.InputContextTokens.Value == nil || *sumA.Metrics.InputContextTokens.Value != "1100000" { // 800000 + 300000
		t.Errorf("expected user A inputContextTokens 1100000, got %+v", sumA.Metrics.InputContextTokens)
	}
	if !sumA.Metrics.OutputTokens.Supported || sumA.Metrics.OutputTokens.Value == nil || *sumA.Metrics.OutputTokens.Value != "400000" {
		t.Errorf("expected user A outputTokens 400000, got %+v", sumA.Metrics.OutputTokens)
	}
	if !sumA.Metrics.CacheHitRate.Supported || sumA.Metrics.CacheHitRate.Value == nil || *sumA.Metrics.CacheHitRate.Value != "0.273" { // 300000 / 1100000 = 0.2727... -> 0.273
		t.Errorf("expected user A cacheHitRate 0.273, got %+v", sumA.Metrics.CacheHitRate)
	}
	if !sumA.Metrics.ActiveDurationMs.Supported || sumA.Metrics.ActiveDurationMs.Value == nil || *sumA.Metrics.ActiveDurationMs.Value != "3600000" {
		t.Errorf("expected user A activeDurationMs 3600000, got %+v", sumA.Metrics.ActiveDurationMs)
	}
	if !sumA.Metrics.MessageCount.Supported || sumA.Metrics.MessageCount.Value == nil || *sumA.Metrics.MessageCount.Value != "120" {
		t.Errorf("expected user A messageCount 120, got %+v", sumA.Metrics.MessageCount)
	}
	if !sumA.Metrics.UserMessageCount.Supported || sumA.Metrics.UserMessageCount.Value == nil || *sumA.Metrics.UserMessageCount.Value != "45" {
		t.Errorf("expected user A userMessageCount 45, got %+v", sumA.Metrics.UserMessageCount)
	}
	if !sumA.Metrics.EstimatedCost.Supported || sumA.Metrics.EstimatedCost.Amount == nil || *sumA.Metrics.EstimatedCost.Amount != "12.50000000" {
		t.Errorf("expected user A cost 12.50000000, got %+v", sumA.Metrics.EstimatedCost)
	}

	// 2. Verify User B (Agg version 1 -> supported=false with value=nil for extensions)
	sumB, err := st.Analytics().GetPersonalSummary(ctx, userB, r)
	if err != nil {
		t.Fatalf("failed to get personal summary for user B: %v", err)
	}

	if sumB.Metrics.TotalTokens.Value == nil || *sumB.Metrics.TotalTokens.Value != "700000" {
		t.Errorf("expected user B total tokens 700000, got %v", sumB.Metrics.TotalTokens.Value)
	}
	if sumB.Metrics.InputContextTokens.Supported || sumB.Metrics.InputContextTokens.Value != nil {
		t.Errorf("expected user B inputContextTokens supported=false and value=nil, got %+v", sumB.Metrics.InputContextTokens)
	}
	if sumB.Metrics.OutputTokens.Supported || sumB.Metrics.OutputTokens.Value != nil {
		t.Errorf("expected user B outputTokens supported=false and value=nil, got %+v", sumB.Metrics.OutputTokens)
	}
	if sumB.Metrics.CacheHitRate.Supported || sumB.Metrics.CacheHitRate.Value != nil {
		t.Errorf("expected user B cacheHitRate supported=false and value=nil, got %+v", sumB.Metrics.CacheHitRate)
	}
	if sumB.Metrics.ActiveDurationMs.Supported || sumB.Metrics.ActiveDurationMs.Value != nil {
		t.Errorf("expected user B activeDurationMs supported=false and value=nil, got %+v", sumB.Metrics.ActiveDurationMs)
	}
	if sumB.Metrics.MessageCount.Supported || sumB.Metrics.MessageCount.Value != nil {
		t.Errorf("expected user B messageCount supported=false and value=nil, got %+v", sumB.Metrics.MessageCount)
	}
	if sumB.Metrics.UserMessageCount.Supported || sumB.Metrics.UserMessageCount.Value != nil {
		t.Errorf("expected user B userMessageCount supported=false and value=nil, got %+v", sumB.Metrics.UserMessageCount)
	}

	// 3. Verify User C (Zero rows -> supported=true with real zero strings)
	sumC, err := st.Analytics().GetPersonalSummary(ctx, userC, r)
	if err != nil {
		t.Fatalf("failed to get personal summary for user C: %v", err)
	}

	if !sumC.Metrics.TotalTokens.Supported || sumC.Metrics.TotalTokens.Value == nil || *sumC.Metrics.TotalTokens.Value != "0" {
		t.Errorf("expected user C total tokens '0', got %+v", sumC.Metrics.TotalTokens)
	}
	if !sumC.Metrics.InputContextTokens.Supported || sumC.Metrics.InputContextTokens.Value == nil || *sumC.Metrics.InputContextTokens.Value != "0" {
		t.Errorf("expected user C inputContextTokens '0', got %+v", sumC.Metrics.InputContextTokens)
	}
	if !sumC.Metrics.OutputTokens.Supported || sumC.Metrics.OutputTokens.Value == nil || *sumC.Metrics.OutputTokens.Value != "0" {
		t.Errorf("expected user C outputTokens '0', got %+v", sumC.Metrics.OutputTokens)
	}
	if sumC.Metrics.TokensPerCodeLine.Value != nil {
		t.Errorf("expected user C tokensPerCodeLine nil, got %v", sumC.Metrics.TokensPerCodeLine.Value)
	}
}

func TestUSR012_TokenTrendFiltersAndBreakdownsMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := "usr_filter_test"
	seedTestUser(t, db, st, userID, "filteruser", "Filter User", "filter@tokendance.dev", true, now)

	// Seed daily_user_agent_metrics for 2 agents
	d1 := "2026-08-28"
	d2 := "2026-08-29"

	_, err := db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			code_generated_lines, active_duration_ms, message_count, user_message_count,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at
		) VALUES
		(?, ?, 'claude-code', 600000, 0, 400000, 150000, 50000, 0, 10000, 100, 1000, 10, 5, 6.0, 'USD', 1, 2, ?),
		(?, ?, 'cursor', 400000, 0, 250000, 100000, 50000, 0, 5000, 50, 800, 8, 4, 4.0, 'USD', 2, 2, ?),
		(?, ?, 'claude-code', 800000, 0, 500000, 200000, 100000, 0, 20000, 150, 1200, 15, 6, 8.0, 'USD', 3, 2, ?)`,
		d1, userID, now,
		d1, userID, now,
		d2, userID, now,
	)
	if err != nil {
		t.Fatalf("failed to insert daily_user_agent_metrics: %v", err)
	}

	// Seed daily_user_agent_model_metrics for 3 models across 2 providers
	_, err = db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_model_metrics (
			metric_date, user_id, agent_id, provider_id, model_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			model_request_count, cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at
		) VALUES
		(?, ?, 'claude-code', 'anthropic', 'claude-3-7-sonnet', 600000, 0, 400000, 150000, 50000, 0, 10000, 10, 6.0, 'USD', 1, 2, ?),
		(?, ?, 'cursor', 'openai', 'gpt-4o', 400000, 0, 250000, 100000, 50000, 0, 5000, 8, 4.0, 'USD', 2, 2, ?),
		(?, ?, 'claude-code', 'bedrock', 'claude-3-7-sonnet', 800000, 0, 500000, 200000, 100000, 0, 20000, 15, 8.0, 'USD', 3, 2, ?)`,
		d1, userID, now,
		d1, userID, now,
		d2, userID, now,
	)
	if err != nil {
		t.Fatalf("failed to insert daily_user_agent_model_metrics: %v", err)
	}

	r := domain.TimeRange{
		Key:      domain.TimeRange30d,
		From:     now.AddDate(0, 0, -30),
		To:       now,
		Timezone: "UTC",
	}

	// 1. Test FilterOptions
	opts, err := st.Analytics().GetFilterOptions(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get filter options: %v", err)
	}
	if len(opts.Agents) != 2 || opts.Agents[0] != "claude-code" || opts.Agents[1] != "cursor" {
		t.Errorf("unexpected agents in filter options: %+v", opts.Agents)
	}
	if len(opts.Providers) != 3 {
		t.Errorf("expected 3 providers in filter options, got %+v", opts.Providers)
	}
	if len(opts.Models) != 2 {
		t.Errorf("expected 2 models in filter options, got %+v", opts.Models)
	}

	// 2. Test TokenTrend with all filters vs dimension filters
	trendAll, err := st.Analytics().GetTokenTrend(ctx, userID, r, "total", nil, nil, nil)
	if err != nil || len(trendAll.Points) != 2 {
		t.Fatalf("expected 2 trend points for all agents, got %d, err: %v", len(trendAll.Points), err)
	}
	// Day 1 total should be 600000 + 400000 = 1000000
	if trendAll.Points[0].TokenTotal == nil || *trendAll.Points[0].TokenTotal != "1000000" {
		t.Errorf("expected day 1 total tokens 1000000, got %v", trendAll.Points[0].TokenTotal)
	}

	// Filter by agent = claude-code
	agentClaude := "claude-code"
	trendClaude, err := st.Analytics().GetTokenTrend(ctx, userID, r, "total", &agentClaude, nil, nil)
	if err != nil || len(trendClaude.Points) != 2 {
		t.Fatalf("expected 2 trend points for claude-code, got %d, err: %v", len(trendClaude.Points), err)
	}
	if trendClaude.Points[0].TokenTotal == nil || *trendClaude.Points[0].TokenTotal != "600000" {
		t.Errorf("expected day 1 claude-code tokens 600000, got %v", trendClaude.Points[0].TokenTotal)
	}

	// Filter by model = gpt-4o
	modelGpt := "gpt-4o"
	trendGpt, err := st.Analytics().GetTokenTrend(ctx, userID, r, "total", nil, nil, &modelGpt)
	if err != nil || len(trendGpt.Points) != 1 {
		t.Fatalf("expected 1 trend point for gpt-4o, got %d, err: %v", len(trendGpt.Points), err)
	}
	if trendGpt.Points[0].TokenTotal == nil || *trendGpt.Points[0].TokenTotal != "400000" {
		t.Errorf("expected day 1 gpt-4o tokens 400000, got %v", trendGpt.Points[0].TokenTotal)
	}

	// 3. Test AgentBreakdown
	ab, err := st.Analytics().GetAgentBreakdown(ctx, userID, r)
	if err != nil || len(ab.Items) != 2 {
		t.Fatalf("expected 2 agent breakdown items, got %d, err: %v", len(ab.Items), err)
	}
	// claude-code has 1400000 / 1800000 = 77.8%, cursor has 400000 / 1800000 = 22.2%
	if ab.Items[0].Key != "claude-code" || ab.Items[0].TokenTotal != "1400000" || ab.Items[0].Percentage != 77.8 {
		t.Errorf("unexpected agent breakdown item 0: %+v", ab.Items[0])
	}

	// 4. Test ModelBreakdown
	mb, err := st.Analytics().GetModelBreakdown(ctx, userID, r)
	if err != nil || len(mb.Items) != 2 {
		t.Fatalf("expected 2 model breakdown items, got %d, err: %v", len(mb.Items), err)
	}

	// 5. Test ActivityCalendar
	cal, err := st.Analytics().GetActivityCalendar(ctx, userID, r)
	if err != nil {
		t.Fatalf("failed to get activity calendar: %v", err)
	}
	if cal.TotalActiveDays != 2 {
		t.Errorf("expected 2 total active days, got %d", cal.TotalActiveDays)
	}
}

func TestUSR014_PersonalSkillRankingAcrossDaysAndAgentsMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := "usr_skill_ranking"
	seedTestUser(t, db, st, userID, "skill_ranker", "Skill Ranker", "skill-ranker@tokendance.dev", false, now)
	publicKey := crypto.SHA256([]byte("public-skill-key"))
	privateKey := crypto.SHA256([]byte("private-skill-key"))
	for index, date := range []string{"2026-08-27", "2026-08-28", "2026-08-29"} {
		agent := "claude-code"
		if index == 1 {
			agent = "cursor"
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO daily_skill_metrics (
				metric_date, user_id, agent_id, skill_key, skill_public_name,
				use_count, exact_use_count, success_count, failure_count,
				duration_ms, source_max_event_pk, aggregation_version, computed_at, updated_at
			) VALUES (?, ?, ?, ?, 'Public Review Skill', ?, ?, ?, 1, 100, ?, 2, ?, ?)`,
			date, userID, agent, publicKey[:], 10+index, 10+index, 9+index, index+1, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO daily_skill_metrics (
			metric_date, user_id, agent_id, skill_key, skill_public_name,
			use_count, exact_use_count, success_count, failure_count,
			duration_ms, source_max_event_pk, aggregation_version, computed_at, updated_at
		) VALUES ('2026-08-29', ?, 'cursor', ?, NULL, 5, 5, 5, 0, 50, 10, 2, ?, ?)`,
		userID, privateKey[:], now, now); err != nil {
		t.Fatal(err)
	}
	range30d := domain.TimeRange{Key: domain.TimeRange30d, From: now.AddDate(0, 0, -29), To: now, Timezone: "UTC"}
	result, err := st.Analytics().GetSkillRanking(ctx, userID, range30d)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 2 {
		t.Fatalf("expected public and private skill rows, got %+v", result.Skills)
	}
	if result.Skills[0].SkillPublicName != "Public Review Skill" || result.Skills[0].UseCount != "33" || result.Skills[0].ActiveDays != 3 {
		t.Fatalf("public skill aggregation mismatch: %+v", result.Skills[0])
	}
	if result.Skills[1].SkillPublicName != "Private Skill" || !strings.HasPrefix(result.Skills[1].SkillID, "skl_") || strings.Contains(strings.ToLower(result.Skills[1].SkillID), strings.ToLower(fmt.Sprintf("%x", privateKey[:]))) {
		t.Fatalf("private skill identity leaked or was not masked: %+v", result.Skills[1])
	}
}

func TestUSR015_ImmediatePrivacyClosureMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	aliceID := "usr_snap_alice"
	bobID := "usr_snap_bob"
	seedTestUser(t, db, st, aliceID, "alice_snap", "Alice Snap", "alice@snap.test", true, now)
	seedTestUser(t, db, st, bobID, "bob_snap", "Bob Snap", "bob@snap.test", true, now)
	// The live token board reads committed usage and only explicitly shared totals.
	if _, err := db.Exec("UPDATE user_privacy_settings SET show_token_total=TRUE WHERE user_id IN (?, ?)", aliceID, bobID); err != nil {
		t.Fatal(err)
	}
	seedRankUsage(t, db, aliceID, time.Now().UTC().Format("2006-01-02"), "codex", 5000000, 0, 0)
	seedRankUsage(t, db, bobID, time.Now().UTC().Format("2006-01-02"), "codex", 3000000, 0, 0)

	// Publish snapshot containing Alice (#1) and Bob (#2)
	snapID := "snp_closure_01"
	entries := []domain.LeaderboardEntry{
		{RankNo: 1, Handle: "alice_snap", DisplayName: "Alice Snap", MetricValue: "5000000"},
		{RankNo: 2, Handle: "bob_snap", DisplayName: "Bob Snap", MetricValue: "3000000"},
	}
	if err := st.Leaderboard().PublishSnapshot(ctx, snapID, "global", "30d", "tokens", entries, now); err != nil {
		t.Fatalf("failed to publish snapshot: %v", err)
	}

	// Query leaderboard: both users present
	lb1, err := st.Leaderboard().GetLeaderboard(ctx, "global", "30d", "tokens", nil, 50)
	if err != nil || len(lb1.Entries) != 2 {
		t.Fatalf("expected 2 entries in leaderboard, got %d, err: %v", len(lb1.Entries), err)
	}

	// Bob switches privacy to private (public_profile_enabled = false)
	_, err = st.Privacy().UpdatePrivacyTx(ctx, bobID, domain.UserPrivacySettings{
		PublicProfileEnabled: false,
	}, 0, domain.UserSecurityEvent{EventID: "evt_priv_bob", UserID: &bobID, EventType: "privacy_changed", CreatedAt: now}, now)
	if err != nil {
		t.Fatalf("failed to update Bob's privacy: %v", err)
	}

	// Query old snapshot again: Bob must be immediately closed out by privacy join!
	lb2, err := st.Leaderboard().GetLeaderboard(ctx, "global", "30d", "tokens", nil, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboard after privacy update: %v", err)
	}
	if len(lb2.Entries) != 1 || lb2.Entries[0].Handle != "alice_snap" {
		t.Fatalf("expected only Alice in leaderboard after Bob disabled public profile, got: %+v", lb2.Entries)
	}

	// Alice updates her display name and avatar in public profile
	newDisplay := "Alice Superstar"
	newAvatar := "https://cdn.example.com/alice_new.png"
	_, err = db.ExecContext(ctx, `
		UPDATE public_user_profiles
		SET display_name = ?, avatar_url = ?, projection_version = projection_version + 1
		WHERE user_id = ?`, newDisplay, newAvatar, aliceID)
	if err != nil {
		t.Fatalf("failed to update Alice public profile: %v", err)
	}

	// Leaderboard reads must join current published public projection
	lb3, err := st.Leaderboard().GetLeaderboard(ctx, "global", "30d", "tokens", nil, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboard: %v", err)
	}
	if len(lb3.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(lb3.Entries))
	}
	if lb3.Entries[0].DisplayName != "Alice Superstar" || lb3.Entries[0].AvatarURL == nil || *lb3.Entries[0].AvatarURL != newAvatar {
		t.Errorf("expected current projection display name and avatar, got: %+v", lb3.Entries[0])
	}

	// Alice suspends account
	_, err = db.ExecContext(ctx, "UPDATE users SET account_status = 'suspended' WHERE user_id = ?", aliceID)
	if err != nil {
		t.Fatalf("failed to suspend Alice: %v", err)
	}

	lb4, err := st.Leaderboard().GetLeaderboard(ctx, "global", "30d", "tokens", nil, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboard: %v", err)
	}
	if len(lb4.Entries) != 0 {
		t.Fatalf("expected 0 entries after Alice suspended account, got %d", len(lb4.Entries))
	}
}

func TestUSR016_PublicDTOWhitelistMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := "usr_whitelist_test"
	seedTestUser(t, db, st, userID, "whitelist_pilot", "Whitelist Pilot", "whitelist@tokendance.dev", true, now)

	// Add bio and token metrics
	bioText := "Security and privacy enthusiast"
	_, err := db.ExecContext(ctx, "UPDATE users SET bio = ? WHERE user_id = ?", bioText, userID)
	if err != nil {
		t.Fatalf("failed to update bio: %v", err)
	}
	_, err = db.ExecContext(ctx, "UPDATE public_user_profiles SET bio = ? WHERE user_id = ?", bioText, userID)
	if err != nil {
		t.Fatalf("failed to update public bio: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			code_generated_lines, active_duration_ms, message_count, user_message_count,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at
		) VALUES (
			'2026-08-29', ?, 'claude-code', 3000000, 0,
			2000000, 800000, 200000, 0, 50000,
			500, 1800000, 50, 20,
			30.0, 'USD', 1, 2, ?
		)`, userID, now)
	if err != nil {
		t.Fatalf("failed to insert metrics: %v", err)
	}

	cfg := config.DefaultConfig()
	clk := clock.NewMockClock(now)
	authSvc := auth.NewService(st, cfg, clk)
	profSvc := profile.NewService(st, clk)
	privSvc := privacy.NewService(st, clk)
	anSvc := analytics.NewService(st, clk)
	devSvc := device.NewService(st, cfg, clk)
	expSvc := export.NewService(st, clk, provider.NewMemoryObjectStorage(""))
	mediaSvc := media.NewService(st, cfg, clk, provider.NewMemoryObjectStorage(""))
	searchSvc := search.NewService(st, clk)
	lbSvc := leaderboard.NewService(st)

	router := httpapi.NewRouter(authSvc, profSvc, privSvc, anSvc, devSvc, expSvc, mediaSvc, searchSvc, lbSvc)

	// Call GET /api/v1/public/users/whitelist_pilot
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/users/whitelist_pilot", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from get public profile, got %d: %s", rec.Code, rec.Body.String())
	}

	bodyStr := rec.Body.String()
	// Whitelist verification: must contain public fields
	if !strings.Contains(bodyStr, `"handle":"whitelist_pilot"`) ||
		!strings.Contains(bodyStr, `"displayName":"Whitelist Pilot"`) ||
		!strings.Contains(bodyStr, `"bio":"Security and privacy enthusiast"`) ||
		!strings.Contains(bodyStr, `"tokenTotal":"3000000"`) {
		t.Errorf("missing expected whitelisted public fields in response: %s", bodyStr)
	}

	// Whitelist verification: must NEVER leak internal or sensitive fields
	if strings.Contains(bodyStr, userID) ||
		strings.Contains(bodyStr, "whitelist@tokendance.dev") ||
		strings.Contains(bodyStr, "email") ||
		strings.Contains(bodyStr, "timezone") ||
		strings.Contains(bodyStr, "locale") ||
		strings.Contains(bodyStr, "password") {
		t.Errorf("public response leaked private/internal fields: %s", bodyStr)
	}

	// Update privacy: hide bio and token total
	_, err = st.Privacy().UpdatePrivacyTx(ctx, userID, domain.UserPrivacySettings{
		PublicProfileEnabled: true,
		ShowBio:              false,
		ShowTokenTotal:       false,
	}, 0, domain.UserSecurityEvent{EventID: "evt_priv_hide", UserID: &userID, EventType: "privacy_changed", CreatedAt: now}, now)
	if err != nil {
		t.Fatalf("failed to update privacy: %v", err)
	}

	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var pubDTO domain.PublicProfileDTO
	if err := json.Unmarshal(rec2.Body.Bytes(), &pubDTO); err != nil {
		t.Fatalf("failed to unmarshal public profile: %v", err)
	}
	if pubDTO.Bio != nil {
		t.Errorf("expected Bio to be null/omitted when ShowBio is false, got %v", *pubDTO.Bio)
	}
	if pubDTO.TokenTotal != nil {
		t.Errorf("expected TokenTotal to be null/omitted when ShowTokenTotal is false, got %v", *pubDTO.TokenTotal)
	}
}

func TestUSR106_PublicSkillMinimumSampleMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	// Seed 6 public users
	for i := 1; i <= 6; i++ {
		uID := fmt.Sprintf("usr_sk_%d", i)
		hName := fmt.Sprintf("sk_user_%d", i)
		seedTestUser(t, db, st, uID, hName, fmt.Sprintf("Skill User %d", i), fmt.Sprintf("sk%d@tokendance.dev", i), true, now)
	}

	// Skill 1: Popular Skill -> Used by 5 public users across 3 distinct dates (>= 5 users & >= 3 days)
	skillKey1 := crypto.SHA256([]byte("skill.popular"))
	dates := []string{"2026-08-27", "2026-08-28", "2026-08-29"}
	for i := 1; i <= 5; i++ {
		uID := fmt.Sprintf("usr_sk_%d", i)
		for _, d := range dates {
			_, err := db.ExecContext(ctx, `
				INSERT INTO daily_skill_metrics (
					metric_date, user_id, agent_id, skill_key, skill_public_name,
					use_count, exact_use_count, success_count, failure_count,
					source_max_event_pk, aggregation_version, computed_at
				) VALUES (?, ?, 'claude-code', ?, 'Popular AST Parser', 100, 100, 95, 5, 1, 2, ?)`,
				d, uID, skillKey1[:], now)
			if err != nil {
				t.Fatalf("failed to insert skill metrics for popular skill: %v", err)
			}
		}
	}

	// Skill 2: Low Sample Skill -> Used by only 2 users (< 5 users)
	skillKey2 := crypto.SHA256([]byte("skill.lowsample"))
	for i := 1; i <= 2; i++ {
		uID := fmt.Sprintf("usr_sk_%d", i)
		_, err := db.ExecContext(ctx, `
			INSERT INTO daily_skill_metrics (
				metric_date, user_id, agent_id, skill_key, skill_public_name,
				use_count, exact_use_count, success_count, failure_count,
				source_max_event_pk, aggregation_version, computed_at
			) VALUES ('2026-08-29', ?, 'cursor', ?, 'Rare Test Generator', 50, 50, 50, 0, 2, 2, ?)`,
			uID, skillKey2[:], now)
		if err != nil {
			t.Fatalf("failed to insert skill metrics for low sample skill: %v", err)
		}
	}

	// 1. Search for skills -> Only Popular AST Parser should be returned
	res, err := st.Search().Search(ctx, "Parser", 20, now)
	if err != nil {
		t.Fatalf("failed to search skills: %v", err)
	}
	if len(res.Skills) != 1 || res.Skills[0].SkillPublicName != "Popular AST Parser" {
		t.Fatalf("expected only Popular AST Parser in search results, got: %+v", res.Skills)
	}
	if res.Skills[0].PublicUserCount != 5 || res.Skills[0].ActiveDays != 3 {
		t.Errorf("unexpected publicUserCount or activeDays: %+v", res.Skills[0])
	}

	// Search for rare skill -> 0 results
	resRare, err := st.Search().Search(ctx, "Generator", 20, now)
	if err != nil {
		t.Fatalf("failed to search rare skill: %v", err)
	}
	if len(resRare.Skills) != 0 {
		t.Fatalf("expected 0 skills for low sample skill, got %d", len(resRare.Skills))
	}

	// User 5 turns off public profile -> public users drops to 4 (< 5)
	user5ID := "usr_sk_5"
	_, err = st.Privacy().UpdatePrivacyTx(ctx, user5ID, domain.UserPrivacySettings{
		PublicProfileEnabled: false,
	}, 0, domain.UserSecurityEvent{EventID: "evt_priv_u5", UserID: &user5ID, EventType: "privacy_changed", CreatedAt: now}, now)
	if err != nil {
		t.Fatalf("failed to disable user 5 public profile: %v", err)
	}

	// Search again for Parser -> should now be EXCLUDED because public users is 4 < 5
	resAfter, err := st.Search().Search(ctx, "Parser", 20, now)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}
	if len(resAfter.Skills) != 0 {
		t.Fatalf("expected 0 skills after public user count dropped below 5, got %d", len(resAfter.Skills))
	}
}

func TestUSR107_CompareHiddenMetricPrivacyMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	// User 1: Public with full visibility
	u1 := "usr_cmp_full"
	seedTestUser(t, db, st, u1, "cmp_full", "Full User", "full@tokendance.dev", true, now)
	_, err := db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			code_generated_lines, active_duration_ms, message_count, user_message_count,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at
		) VALUES ('2026-08-29', ?, 'claude-code', 2000000, 0, 1200000, 600000, 200000, 0, 20000, 400, 1800000, 60, 25, 20.0, 'USD', 1, 2, ?)`,
		u1, now)
	if err != nil {
		t.Fatalf("failed to insert metrics for u1: %v", err)
	}

	// User 2: Public but all show_* disabled
	u2 := "usr_cmp_minimal"
	seedTestUser(t, db, st, u2, "cmp_minimal", "Minimal User", "min@tokendance.dev", true, now)
	_, err = st.Privacy().UpdatePrivacyTx(ctx, u2, domain.UserPrivacySettings{
		PublicProfileEnabled: true,
		ShowBio:              false,
		ShowTokenTotal:       false,
		ShowTrends:           false,
		ShowActivityCalendar: false,
		ShowAgentBreakdown:   false,
		ShowSkillRanking:     false,
		ShowAchievements:     false,
	}, 0, domain.UserSecurityEvent{EventID: "evt_min_priv", UserID: &u2, EventType: "privacy_changed", CreatedAt: now}, now)
	if err != nil {
		t.Fatalf("failed to update u2 privacy: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			code_generated_lines, active_duration_ms, message_count, user_message_count,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at
		) VALUES ('2026-08-29', ?, 'cursor', 5000000, 0, 3000000, 1500000, 500000, 0, 30000, 1000, 3600000, 100, 40, 50.0, 'USD', 2, 2, ?)`,
		u2, now)
	if err != nil {
		t.Fatalf("failed to insert metrics for u2: %v", err)
	}

	// User 3: Private user
	u3 := "usr_cmp_private"
	seedTestUser(t, db, st, u3, "cmp_private", "Private User", "priv@tokendance.dev", false, now)

	cfg := config.DefaultConfig()
	clk := clock.NewMockClock(now)
	authSvc := auth.NewService(st, cfg, clk)
	profSvc := profile.NewService(st, clk)
	privSvc := privacy.NewService(st, clk)
	anSvc := analytics.NewService(st, clk)
	devSvc := device.NewService(st, cfg, clk)
	expSvc := export.NewService(st, clk, provider.NewMemoryObjectStorage(""))
	mediaSvc := media.NewService(st, cfg, clk, provider.NewMemoryObjectStorage(""))
	searchSvc := search.NewService(st, clk)
	lbSvc := leaderboard.NewService(st)

	router := httpapi.NewRouter(authSvc, profSvc, privSvc, anSvc, devSvc, expSvc, mediaSvc, searchSvc, lbSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/compare?handles=cmp_full,cmp_minimal,cmp_private", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from compare, got %d: %s", rec.Code, rec.Body.String())
	}

	var cmpResp domain.CompareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cmpResp); err != nil {
		t.Fatalf("failed to unmarshal compare response: %v", err)
	}

	if len(cmpResp.Users) != 3 {
		t.Fatalf("expected 3 users in comparison, got %d", len(cmpResp.Users))
	}

	// User 1 (Full): visible=true and all metrics populated
	userFull := cmpResp.Users[0]
	if !userFull.Visible || userFull.Handle != "cmp_full" {
		t.Errorf("expected cmp_full visible=true, got %+v", userFull)
	}
	if userFull.TokenTotal == nil || *userFull.TokenTotal != "2000000" {
		t.Errorf("expected cmp_full tokenTotal 2000000, got %v", userFull.TokenTotal)
	}
	if len(userFull.AgentBreakdown) == 0 {
		t.Errorf("expected cmp_full agentBreakdown populated")
	}
	if userFull.ActiveDays == nil || *userFull.ActiveDays != 1 {
		t.Errorf("expected cmp_full activeDays 1, got %v", userFull.ActiveDays)
	}

	// User 2 (Minimal): visible=true, displayName/avatar populated, but all gated metrics NIL
	userMin := cmpResp.Users[1]
	if !userMin.Visible || userMin.Handle != "cmp_minimal" {
		t.Errorf("expected cmp_minimal visible=true, got %+v", userMin)
	}
	if userMin.DisplayName == nil || *userMin.DisplayName != "Minimal User" {
		t.Errorf("expected cmp_minimal displayName 'Minimal User', got %v", userMin.DisplayName)
	}
	if userMin.TokenTotal != nil {
		t.Errorf("expected cmp_minimal tokenTotal NIL when show_token_total=false, got %v", *userMin.TokenTotal)
	}
	if userMin.CodeLinesTotal != nil {
		t.Errorf("expected cmp_minimal codeLinesTotal NIL when show_token_total=false, got %v", *userMin.CodeLinesTotal)
	}
	if len(userMin.AgentBreakdown) > 0 {
		t.Errorf("expected cmp_minimal agentBreakdown empty when show_agent_breakdown=false, got %+v", userMin.AgentBreakdown)
	}
	if len(userMin.SkillRanking) > 0 {
		t.Errorf("expected cmp_minimal skillRanking empty when show_skill_ranking=false, got %+v", userMin.SkillRanking)
	}
	if userMin.ActiveDays != nil || userMin.CurrentStreak != nil {
		t.Errorf("expected cmp_minimal calendar stats NIL when show_activity_calendar=false")
	}

	// User 3 (Private): visible=false without any values
	userPriv := cmpResp.Users[2]
	if userPriv.Visible || userPriv.Handle != "cmp_private" {
		t.Errorf("expected cmp_private visible=false, got %+v", userPriv)
	}
	if userPriv.DisplayName != nil || userPriv.AvatarURL != nil || userPriv.TokenTotal != nil || userPriv.Rank != nil {
		t.Errorf("expected cmp_private to have no values leaked, got %+v", userPriv)
	}
}

func seedBoundaryAnalyticsFixture(t *testing.T, db *sql.DB, st *Store, userID string, now time.Time, dailyDates []string, dailyTokens []uint64, events []struct {
	at     time.Time
	tokens uint64
	agent  string
}) {
	t.Helper()
	seedTestUser(t, db, st, userID, userID[4:], "Boundary User", userID+"@example.test", false, now)
	ctx := context.Background()
	installationID := "ins_" + userID[4:]
	batchID := "bat_" + userID[4:]
	publicKey := crypto.SHA256([]byte(installationID))
	_, err := db.ExecContext(ctx, `INSERT INTO installations
		(installation_id, user_id, device_public_key, os_type, architecture, collector_version, installation_status, status_version, registered_at, updated_at)
		VALUES (?, ?, ?, 'windows', 'amd64', 'test', 'active', 1, ?, ?)`, installationID, userID, publicKey[:], now, now)
	if err != nil {
		t.Fatalf("insert boundary installation: %v", err)
	}
	requestHash := crypto.SHA256([]byte(batchID))
	_, err = db.ExecContext(ctx, `INSERT INTO ingest_batches
		(batch_id, installation_id, request_sha256, event_count, accepted_count, duplicate_count, rejected_count, batch_status, received_at, committed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, 'committed', ?, ?, ?)`, batchID, installationID, requestHash[:], len(events), len(events), now, now, now)
	if err != nil {
		t.Fatalf("insert boundary batch: %v", err)
	}
	for i, event := range events {
		eventHash := crypto.SHA256([]byte(fmt.Sprintf("%s-%d", userID, i)))
		_, err = db.ExecContext(ctx, `INSERT INTO usage_events
			(event_id, schema_version, batch_id, installation_id, user_id, adapter_id, adapter_version, agent_id,
			event_type, accuracy, source_kind, occurred_at, received_at, token_input, token_output, token_total,
			privacy_policy_version)
			VALUES (?, 1, ?, ?, ?, 'fixture', '1', ?, 'model_usage_recorded', 'exact', 'runtime_stream', ?, ?, ?, 0, ?, 1)`,
			eventHash[:], batchID, installationID, userID, event.agent, event.at, now, event.tokens, event.tokens)
		if err != nil {
			t.Fatalf("insert boundary usage event %d: %v", i, err)
		}
	}
	for i, date := range dailyDates {
		_, err = db.ExecContext(ctx, `INSERT INTO daily_user_agent_metrics
			(metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			token_input_total, token_output_total, token_cache_read_total, token_cache_write_total, token_reasoning_total,
			code_generated_lines, active_duration_ms, message_count, user_message_count,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at)
			VALUES (?, ?, 'claude-code', ?, 0, ?, 0, 0, 0, 0, 0, 0, 0, 0, 0, 'USD', ?, 2, ?)`,
			date, userID, dailyTokens[i], dailyTokens[i], i+1, now)
		if err != nil {
			t.Fatalf("insert boundary daily aggregate: %v", err)
		}
	}
}

func TestMySQL_NonUTCBoundaryCorrectionAsiaShanghai(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	userID := "usr_boundary_shanghai"
	seedBoundaryAnalyticsFixture(t, db, st, userID, now,
		[]string{"2026-08-30", "2026-08-31"}, []uint64{100, 200},
		[]struct {
			at     time.Time
			tokens uint64
			agent  string
		}{
			{time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC), 10, "codex"},
			{time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC), 100, "claude-code"},
			{time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC), 200, "claude-code"},
			{time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC), 20, "cursor"},
		})
	r := domain.TimeRange{Key: domain.TimeRangeCustom, From: time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 1, 15, 59, 59, 999999999, time.UTC), Timezone: "Asia/Shanghai"}

	summary, err := st.Analytics().GetPersonalSummary(context.Background(), userID, r)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Metrics.TotalTokens.Value == nil || *summary.Metrics.TotalTokens.Value != "330" {
		t.Fatalf("expected boundary-corrected total 330 without full-day double count, got %+v", summary.Metrics.TotalTokens)
	}
	breakdown, err := st.Analytics().GetAgentBreakdown(context.Background(), userID, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown.Items) != 3 || breakdown.Items[0].TokenTotal != "300" {
		t.Fatalf("unexpected boundary-corrected breakdown: %+v", breakdown.Items)
	}
	calendar, err := st.Analytics().GetActivityCalendar(context.Background(), userID, r)
	if err != nil {
		t.Fatal(err)
	}
	var calendarTotal uint64
	for _, day := range calendar.Days {
		value, _ := strconv.ParseUint(day.TokenTotal, 10, 64)
		calendarTotal += value
	}
	if calendarTotal != 330 {
		t.Fatalf("expected calendar total 330, got %d (%+v)", calendarTotal, calendar.Days)
	}
}

func TestMySQL_NonUTCBoundaryCorrectionDSTFallback(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	userID := "usr_boundary_dst"
	seedBoundaryAnalyticsFixture(t, db, st, userID, now,
		[]string{"2026-11-01", "2026-11-02"}, []uint64{500, 600},
		[]struct {
			at     time.Time
			tokens uint64
			agent  string
		}{
			{time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), 50, "codex"},
			{time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), 500, "claude-code"},
			{time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC), 600, "claude-code"},
			{time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC), 70, "cursor"},
		})
	r := domain.TimeRange{Key: domain.TimeRangeCustom, From: time.Date(2026, 10, 31, 4, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 3, 4, 59, 59, 999999999, time.UTC), Timezone: "America/New_York"}

	summary, err := st.Analytics().GetPersonalSummary(context.Background(), userID, r)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Metrics.TotalTokens.Value == nil || *summary.Metrics.TotalTokens.Value != "1220" {
		t.Fatalf("expected DST boundary-corrected total 1220, got %+v", summary.Metrics.TotalTokens)
	}
	trend, err := st.Analytics().GetTokenTrend(context.Background(), userID, r, "total", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var trendTotal uint64
	for _, point := range trend.Points {
		if point.TokenTotal != nil {
			value, _ := strconv.ParseUint(*point.TokenTotal, 10, 64)
			trendTotal += value
		}
	}
	if trendTotal != 1220 {
		t.Fatalf("expected DST trend total 1220, got %d (%+v)", trendTotal, trend.Points)
	}
}
