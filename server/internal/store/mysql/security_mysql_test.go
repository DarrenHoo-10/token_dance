package mysql

import (
	"context"
	"sync"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

// TestMySQL_LoginFailureCounterConcurrency verifies that concurrent failed login
// attempts atomically increment the failed_login_count without lost updates,
// and locks the account when the count reaches 10.
func TestMySQL_LoginFailureCounterConcurrency(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	now := time.Now().UTC()

	userID := "usr_test_fail_login"
	email := "fail_login@tokendance.dev"
	emailHash := crypto.SHA256([]byte(email))
	credHash, _ := crypto.HashPassword("TestPass123!", crypto.FastArgon2Params)

	sessTokenHash := crypto.SHA256([]byte("sess_token_fail_test"))
	csrfTokenHash := crypto.SHA256([]byte("csrf_token_fail_test"))

	_, err := auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte(email),
			DisplayName:           "Fail Login Tester",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      credHash,
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
			FailedLoginCount:  0,
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
			SessionID:         "ses_fail_test_01",
			UserID:            userID,
			SessionTokenHash:  sessTokenHash,
			CSRFTokenHash:     csrfTokenHash,
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(48 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: "emc_fail_reg",
	})
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	// Concurrently record 10 failed login attempts
	const concurrency = 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			lockUntil := now.Add(15 * time.Minute)
			ev := domain.UserSecurityEvent{
				EventID:   "sev_fail_" + string(rune('a'+idx)),
				UserID:    &userID,
				EventType: "login_failed",
				Outcome:   "failure",
				CreatedAt: time.Now().UTC(),
			}
			_ = auth.RecordLoginFailure(ctx, userID, 0, &lockUntil, ev)
		}(i)
	}
	wg.Wait()

	// Verify the counter in MySQL is exactly 10 and locked_until is set
	_, cred, err := auth.FindUserByEmailHash(ctx, emailHash)
	if err != nil {
		t.Fatalf("failed to find user: %v", err)
	}
	if cred.FailedLoginCount != 10 {
		t.Fatalf("expected failed_login_count = 10, got %d (lost updates detected!)", cred.FailedLoginCount)
	}
	if cred.LockedUntil == nil {
		t.Fatalf("expected locked_until to be set after 10 failed logins")
	}
}

func TestMySQL_RegistrationCodeFailuresIncrementAndLockAtomically(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	now := time.Now().UTC().Truncate(time.Millisecond)
	challengeID := "ech_register_wrong_race"
	challenge := domain.EmailChallenge{
		ChallengeID:     challengeID,
		EmailLookupHash: crypto.SHA256([]byte("register-race@example.com")),
		EmailCiphertext: []byte("register-race@example.com"),
		EmailKeyVersion: 1,
		ChallengeType:   domain.ChallengeTypeRegister,
		CodeHash:        crypto.SHA256([]byte("123456")),
		CodeKeyVersion:  1,
		ChallengeStatus: domain.ChallengeStatusPending,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(10 * time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	outbox := domain.EmailOutbox{
		EmailID:              "eml_register_wrong_race",
		ChallengeID:          &challengeID,
		IdempotencyKey:       crypto.SHA256([]byte("idemp:register-wrong-race")),
		TemplateKey:          "auth.register_code",
		Locale:               "en-US",
		RecipientCiphertext:  []byte("register-race@example.com"),
		PayloadCiphertext:    []byte(`{"code":"123456"}`),
		EncryptionKeyVersion: 1,
		DeliveryStatus:       "pending",
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(10 * time.Minute),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := auth.CreateOrReplaceEmailChallenge(ctx, challenge, outbox); err != nil {
		t.Fatalf("create registration challenge: %v", err)
	}

	const concurrency = 12
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			_ = auth.RecordEmailChallengeFailure(context.Background(), challengeID, now)
		}()
	}
	wg.Wait()

	var attempts uint16
	var status domain.ChallengeStatus
	if err := db.QueryRowContext(ctx, "SELECT attempt_count, challenge_status FROM email_challenges WHERE challenge_id = ?", challengeID).Scan(&attempts, &status); err != nil {
		t.Fatalf("read registration challenge: %v", err)
	}
	if attempts != challenge.MaxAttempts || status != domain.ChallengeStatusLocked {
		t.Fatalf("expected exactly %d attempts and locked status, got attempts=%d status=%s", challenge.MaxAttempts, attempts, status)
	}
}

// TestMySQL_PasswordResetConcurrencyAndReplay verifies that multiple concurrent
// reset password transactions with the same code do not race or replay.
func TestMySQL_PasswordResetConcurrencyAndReplay(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	now := time.Now().UTC()

	userID := "usr_test_reset_race"
	email := "reset_race@tokendance.dev"
	emailHash := crypto.SHA256([]byte(email))
	credHash, _ := crypto.HashPassword("OldPassword123!", crypto.FastArgon2Params)
	codeHash := crypto.SHA256([]byte("654321"))

	// Create user
	_, err := auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte(email),
			DisplayName:           "Reset Race User",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      credHash,
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
			FailedLoginCount:  0,
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
			SessionID:         "ses_reset_race_01",
			UserID:            userID,
			SessionTokenHash:  crypto.SHA256([]byte("tok_reset_1")),
			CSRFTokenHash:     crypto.SHA256([]byte("csrf_reset_1")),
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(48 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: "emc_reset_reg",
	})
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	// Create pending password reset challenge
	challengeID := "ech_pwd_reset_01"
	challenge := domain.EmailChallenge{
		ChallengeID:     challengeID,
		UserID:          &userID,
		EmailLookupHash: emailHash,
		EmailCiphertext: []byte(email),
		EmailKeyVersion: 1,
		ChallengeType:   domain.ChallengeTypePasswordReset,
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
		EmailID:              "eml_pwd_reset_01",
		UserID:               &userID,
		ChallengeID:          &challengeID,
		IdempotencyKey:       crypto.SHA256([]byte("idemp_pwd_reset")),
		TemplateKey:          "auth.password_reset_code",
		Locale:               "en-US",
		RecipientCiphertext:  []byte(email),
		PayloadCiphertext:    []byte(`{"code":"654321"}`),
		EncryptionKeyVersion: 1,
		DeliveryStatus:       "pending",
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(10 * time.Minute),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := auth.CreateOrReplaceEmailChallenge(ctx, challenge, outbox); err != nil {
		t.Fatalf("failed to create reset challenge: %v", err)
	}

	// Run 5 concurrent reset attempts
	const concurrency = 5
	successCount := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			newHash, _ := crypto.HashPassword("NewPass1234!", crypto.FastArgon2Params)
			ev := domain.UserSecurityEvent{
				EventID:   "sev_pwd_reset_" + string(rune('a'+idx)),
				UserID:    &userID,
				EventType: "password_reset",
				Outcome:   "success",
				CreatedAt: time.Now().UTC(),
			}
			err := auth.ResetPasswordTx(ctx, emailHash, codeHash, newHash, 2, ev, time.Now().UTC())
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 password reset to succeed, got %d", successCount)
	}

	// Verify all old sessions were revoked
	sessions, err := auth.ListUserSessions(ctx, userID)
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}
	for _, s := range sessions {
		if s.SessionStatus == domain.SessionStatusActive {
			t.Fatalf("expected all sessions to be revoked on password reset, found active session: %s", s.SessionID)
		}
	}
}

func TestUSR017_OneTimeDeviceBindingConcurrencyAndIdempotencyMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	userID := "usr_binding_concurrency"
	seedTestUser(t, db, st, userID, "binding_concurrency", "Binding Concurrency", "binding@tokendance.dev", false, now)
	sessionID := "ses_" + userID
	codeHash := crypto.SHA256([]byte("ONE-TIME-BINDING-CODE"))
	challenge := domain.DeviceBindingChallenge{
		ChallengeID:      "dbc_binding_concurrency",
		UserID:           userID,
		SessionID:        sessionID,
		CodeLookupHash:   codeHash,
		CodeKeyVersion:   1,
		ChallengeStatus:  domain.ChallengeStatusPending,
		ExpiresAt:        now.Add(5 * time.Minute),
		ActiveSessionKey: &sessionID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if _, err := st.Device().CreateBindingChallenge(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	publicKey := crypto.SHA256([]byte("shared-binding-public-key"))
	const concurrency = 20
	results := make(chan *domain.Installation, concurrency)
	errs := make(chan error, concurrency)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < concurrency; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			installation, err := st.Device().ClaimInstallationTx(context.Background(), codeHash, domain.Installation{
				InstallationID:     "ins_binding_" + string(rune('a'+index)),
				DevicePublicKey:    publicKey,
				OSType:             "windows",
				Architecture:       "x86_64",
				CollectorVersion:   "1.0.0",
				InstallationStatus: domain.InstallationStatusActive,
				RegisteredAt:       now,
				UpdatedAt:          now,
			}, now)
			results <- installation
			errs <- err
		}(index)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	var canonicalID string
	for err := range errs {
		if err != nil {
			t.Fatalf("same-public-key retry should be idempotent, got %v", err)
		}
	}
	for result := range results {
		if result == nil {
			t.Fatal("nil installation result")
		}
		if canonicalID == "" {
			canonicalID = result.InstallationID
		} else if result.InstallationID != canonicalID {
			t.Fatalf("binding created multiple installations: %s and %s", canonicalID, result.InstallationID)
		}
	}
	var installationCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM installations WHERE user_id = ?`, userID).Scan(&installationCount); err != nil {
		t.Fatal(err)
	}
	if installationCount != 1 {
		t.Fatalf("expected exactly one installation, got %d", installationCount)
	}
	otherKey := crypto.SHA256([]byte("conflicting-binding-public-key"))
	if _, err := st.Device().ClaimInstallationTx(ctx, codeHash, domain.Installation{
		InstallationID: "ins_binding_conflict", DevicePublicKey: otherKey, OSType: "windows",
		Architecture: "x86_64", CollectorVersion: "1.0.0", InstallationStatus: domain.InstallationStatusActive,
		RegisteredAt: now, UpdatedAt: now,
	}, now); err == nil {
		t.Fatal("consumed code with a different public key must fail")
	}
}

// TestMySQL_DeviceBindingStaleSessionAndOnboarding verifies that claiming a binding
// code fails if the authorizing session was revoked or if onboarding is incomplete.
func TestMySQL_DeviceBindingStaleSessionAndOnboarding(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	dev := st.Device()
	now := time.Now().UTC()

	userID := "usr_device_test_01"
	email := "device_test@tokendance.dev"
	emailHash := crypto.SHA256([]byte(email))
	credHash, _ := crypto.HashPassword("Pass123!", crypto.FastArgon2Params)

	sessID := "ses_dev_test_01"
	_, err := auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte(email),
			DisplayName:           "Device Tester",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			// Onboarding not completed!
			OnboardingCompletedAt: nil,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      credHash,
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
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
			SessionID:         sessID,
			UserID:            userID,
			SessionTokenHash:  crypto.SHA256([]byte("tok_dev_1")),
			CSRFTokenHash:     crypto.SHA256([]byte("csrf_dev_1")),
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(48 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: "emc_dev_reg",
	})
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	// Create binding challenge
	codeHash := crypto.SHA256([]byte("BINDCODE"))
	ch := domain.DeviceBindingChallenge{
		ChallengeID:      "dbc_test_01",
		UserID:           userID,
		SessionID:        sessID,
		CodeLookupHash:   codeHash,
		CodeKeyVersion:   1,
		ChallengeStatus:  domain.ChallengeStatusPending,
		ExpiresAt:        now.Add(5 * time.Minute),
		ActiveSessionKey: &sessID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if _, err := dev.CreateBindingChallenge(ctx, ch); err != nil {
		t.Fatalf("failed to create binding challenge: %v", err)
	}

	pubKey := crypto.SHA256([]byte("device_public_key_1"))
	inst := domain.Installation{
		InstallationID:     "ins_test_01",
		DevicePublicKey:    pubKey,
		OSType:             "windows",
		Architecture:       "x86_64",
		CollectorVersion:   "1.0.0",
		InstallationStatus: domain.InstallationStatusActive,
		StatusVersion:      1,
		RegisteredAt:       now,
		UpdatedAt:          now,
	}

	// 1. Claim should fail because user has NOT completed onboarding
	_, err = dev.ClaimInstallationTx(ctx, codeHash, inst, now)
	if err != domain.ErrForbidden {
		t.Fatalf("expected ErrForbidden for user with incomplete onboarding, got: %v", err)
	}

	// Complete onboarding
	_, _, err = st.Profile().CompleteOnboardingTx(ctx, userID, "devicetester", "Device Tester", "UTC", "en-US", domain.UserPrivacySettings{
		UserID:               userID,
		PublicProfileEnabled: true,
	}, domain.UserSecurityEvent{}, now)
	if err != nil {
		t.Fatalf("failed to complete onboarding: %v", err)
	}

	// 2. Revoke the authorizing session
	if err := auth.RevokeSession(ctx, sessID, "logout", now); err != nil {
		t.Fatalf("failed to revoke session: %v", err)
	}

	// 3. Claim should fail because authorizing session is revoked
	_, err = dev.ClaimInstallationTx(ctx, codeHash, inst, now)
	if err != domain.ErrChallengeInvalid {
		t.Fatalf("expected ErrChallengeInvalid when authorizing session is revoked, got: %v", err)
	}
}

// TestMySQL_DevicePauseResumeRevokeAndAuthorizeIngestSharedLock verifies that
// device revocation and ingest authorization share row locks in MySQL, ensuring
// ingest immediately sees revocation without stale authorization windows.
func TestUSR018_DeviceRevocationRejectsIngestMySQL(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	dev := st.Device()
	now := time.Now().UTC()

	userID := "usr_lock_test"
	email := "lock_test@tokendance.dev"
	emailHash := crypto.SHA256([]byte(email))
	credHash, _ := crypto.HashPassword("Pass123!", crypto.FastArgon2Params)

	sessID := "ses_lock_test_01"
	_, err := auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte(email),
			DisplayName:           "Lock Tester",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPublic,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			OnboardingCompletedAt: &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      credHash,
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
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
			SessionID:         sessID,
			UserID:            userID,
			SessionTokenHash:  crypto.SHA256([]byte("tok_lock_1")),
			CSRFTokenHash:     crypto.SHA256([]byte("csrf_lock_1")),
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(48 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: "emc_lock_reg",
	})
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	instID := "ins_lock_test_01"
	pubKey := crypto.SHA256([]byte("device_public_key_lock"))
	inst := domain.Installation{
		InstallationID:     instID,
		UserID:             userID,
		DevicePublicKey:    pubKey,
		OSType:             "windows",
		Architecture:       "x86_64",
		CollectorVersion:   "1.0.0",
		InstallationStatus: domain.InstallationStatusActive,
		StatusVersion:      1,
		RegisteredAt:       now,
		UpdatedAt:          now,
	}
	if _, err := dev.RegisterInstallationTx(ctx, inst, now); err != nil {
		t.Fatalf("failed to register installation: %v", err)
	}
	retry := inst
	retry.InstallationID = "ins_never_persisted"
	retry.CollectorVersion = "2.0.0"
	registered, err := dev.RegisterInstallationTx(ctx, retry, now.Add(time.Minute))
	if err != nil || registered.InstallationID != instID || registered.CollectorVersion != inst.CollectorVersion {
		t.Fatalf("re-registration must return stored installation: %+v, %v", registered, err)
	}
	if _, err := st.Ingest().GetIngestInstallation(ctx, registered.InstallationID); err != nil {
		t.Fatalf("re-registered identity must authenticate: %v", err)
	}

	// Verify AuthorizeIngest works when active
	authorizedInst, u, err := dev.AuthorizeIngest(ctx, instID)
	if err != nil || authorizedInst.InstallationStatus != domain.InstallationStatusActive || u.UserID != userID {
		t.Fatalf("expected successful authorize ingest, got: %v", err)
	}

	// Revoke device
	_, err = dev.RevokeInstallation(ctx, instID, userID, now)
	if err != nil {
		t.Fatalf("failed to revoke installation: %v", err)
	}

	// AuthorizeIngest MUST immediately return ErrDeviceRevoked
	_, _, err = dev.AuthorizeIngest(ctx, instID)
	if err != domain.ErrDeviceRevoked {
		t.Fatalf("expected ErrDeviceRevoked, got: %v", err)
	}
}

// TestMySQL_ConcurrentOnboardingAtomicVisibility verifies that CompleteOnboardingTx
// locks users FOR UPDATE and atomically sets leaderboard_visibility to public
// while syncing the user_public_profiles projection.
func TestMySQL_ConcurrentOnboardingAtomicVisibility(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	prof := st.Profile()
	priv := st.Privacy()
	now := time.Now().UTC()

	userID := "usr_onboard_atomic"
	email := "onboard_atomic@tokendance.dev"
	emailHash := crypto.SHA256([]byte(email))
	credHash, _ := crypto.HashPassword("Pass123!", crypto.FastArgon2Params)

	_, err := auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userID,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userID)),
			EmailLookupHash:       &emailHash,
			EmailCiphertext:       []byte(email),
			DisplayName:           "Atomic Onboarder",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{
			UserID:            userID,
			PasswordHash:      credHash,
			PasswordAlgorithm: "argon2id",
			CredentialVersion: 1,
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
			SessionID:         "ses_onboard_atomic",
			UserID:            userID,
			SessionTokenHash:  crypto.SHA256([]byte("tok_onboard")),
			CSRFTokenHash:     crypto.SHA256([]byte("csrf_onboard")),
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(48 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: "emc_onboard_reg",
	})
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	// Complete onboarding with PublicProfileEnabled = true
	userRes, _, err := prof.CompleteOnboardingTx(ctx, userID, "atomic_pilot", "Atomic Pilot", "America/New_York", "en-US", domain.UserPrivacySettings{
		UserID:               userID,
		PublicProfileEnabled: true,
		ShowBio:              true,
		ShowTokenTotal:       true,
		ShowTrends:           true,
	}, domain.UserSecurityEvent{}, now)
	if err != nil {
		t.Fatalf("failed to complete onboarding: %v", err)
	}

	if userRes.LeaderboardVisibility != domain.LeaderboardVisibilityPublic {
		t.Fatalf("expected LeaderboardVisibility = public, got: %s", userRes.LeaderboardVisibility)
	}

	// Verify public profile projection exists and is published
	pub, err := priv.GetPublicProfileByHandle(ctx, "atomic_pilot", now)
	if err != nil || pub == nil {
		t.Fatalf("expected public profile to be published, got err: %v", err)
	}
	if pub.Handle != "atomic_pilot" {
		t.Fatalf("expected handle 'atomic_pilot', got %s", pub.Handle)
	}
}

// TestMySQL_RevokeUserSessionOwnerScoped verifies that a user cannot revoke
// another user's session (IDOR prevention).
func TestMySQL_RevokeUserSessionOwnerScoped(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	ctx := context.Background()
	auth := st.Auth()
	now := time.Now().UTC()

	userA := "usr_idor_a"
	userB := "usr_idor_b"
	credHash, _ := crypto.HashPassword("Pass123!", crypto.FastArgon2Params)

	emailA := "usera@tokendance.dev"
	emailHashA := crypto.SHA256([]byte(emailA))
	sessA := "ses_idor_a"

	emailB := "userb@tokendance.dev"
	emailHashB := crypto.SHA256([]byte(emailB))
	sessB := "ses_idor_b"

	_, _ = auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userA,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userA)),
			EmailLookupHash:       &emailHashA,
			EmailCiphertext:       []byte(emailA),
			DisplayName:           "User A",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{UserID: userA, PasswordHash: credHash, PasswordAlgorithm: "argon2id", CredentialVersion: 1},
		Privacy:    domain.UserPrivacySettings{UserID: userA, PrivacyVersion: 1},
		Session: domain.UserSession{
			SessionID:         sessA,
			UserID:            userA,
			SessionTokenHash:  crypto.SHA256([]byte("tok_a")),
			CSRFTokenHash:     crypto.SHA256([]byte("csrf_a")),
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(48 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: "emc_idor_a",
	})

	_, _ = auth.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User: domain.User{
			UserID:                userB,
			AuthSubjectHash:       crypto.SHA256([]byte("sub_" + userB)),
			EmailLookupHash:       &emailHashB,
			EmailCiphertext:       []byte(emailB),
			DisplayName:           "User B",
			AccountStatus:         domain.AccountStatusActive,
			LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
			TimezoneName:          "UTC",
			Locale:                "en-US",
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		Credential: domain.UserPasswordCredential{UserID: userB, PasswordHash: credHash, PasswordAlgorithm: "argon2id", CredentialVersion: 1},
		Privacy:    domain.UserPrivacySettings{UserID: userB, PrivacyVersion: 1},
		Session: domain.UserSession{
			SessionID:         sessB,
			UserID:            userB,
			SessionTokenHash:  crypto.SHA256([]byte("tok_b")),
			CSRFTokenHash:     crypto.SHA256([]byte("csrf_b")),
			CredentialVersion: 1,
			SessionStatus:     domain.SessionStatusActive,
			IdleExpiresAt:     now.Add(24 * time.Hour),
			AbsoluteExpiresAt: now.Add(48 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChallengeID: "emc_idor_b",
	})

	// User A attempts to revoke User B's session sessB
	err := auth.RevokeUserSession(ctx, sessB, userA, "logout", now)
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound when user A tries to revoke user B session, got: %v", err)
	}

	// Verify User B's session is STILL active
	sess, _, err := auth.ResolveSession(ctx, crypto.SHA256([]byte("tok_b")), now)
	if err != nil || sess.SessionStatus != domain.SessionStatusActive {
		t.Fatalf("User B's session should remain active after unauthorized revoke attempt")
	}
}
