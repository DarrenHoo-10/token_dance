package mysql

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/migrate"
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

	createdJob, err := exp.CreateJob(ctx, expJob)
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
