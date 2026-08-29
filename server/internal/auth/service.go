package auth

import (
	"context"
	"crypto/hmac"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store  store.AuthStore
	pStore store.ProfileStore
	cfg    *config.Config
	clk    clock.Clock

	hmacKey        []byte
	dummyArgonHash string
}

func NewService(st store.Store, cfg *config.Config, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}

	key := []byte(cfg.HMACSecret)
	// Precalculate a valid dummy Argon2 hash for constant-time dummy verification on missing accounts
	params := crypto.DefaultArgon2Params
	if cfg.Argon2MemoryKiB <= 1024 {
		params = crypto.FastArgon2Params
	}
	dummyHash, _ := crypto.HashPassword("tokendance-dummy-timing-password-constant", params)

	return &Service{
		store:          st.Auth(),
		pStore:         st.Profile(),
		cfg:            cfg,
		clk:            clk,
		hmacKey:        key,
		dummyArgonHash: dummyHash,
	}
}

// NormalizeEmail performs standard email normalization
func NormalizeEmail(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 254 {
		return "", domain.ErrInvalidArgument
	}

	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", domain.ErrInvalidArgument
	}

	parts := strings.Split(addr.Address, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", domain.ErrInvalidArgument
	}

	// Lowercase address
	normalized := strings.ToLower(parts[0]) + "@" + strings.ToLower(parts[1])
	return normalized, nil
}

func (s *Service) ComputeEmailLookupHash(normalizedEmail string) [32]byte {
	return crypto.HMACSHA256(s.hmacKey, []byte(normalizedEmail))
}

func (s *Service) ComputeTokenHash(token string) [32]byte {
	return crypto.HMACSHA256(s.hmacKey, []byte(token))
}

func (s *Service) SanitizeReturnTo(returnTo string) string {
	if returnTo == "" {
		return "/"
	}
	trimmed := strings.TrimSpace(returnTo)
	if len(trimmed) > 2048 {
		return "/"
	}

	// Reject null bytes and raw control characters
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] < 32 || trimmed[i] == 127 {
			return "/"
		}
	}

	// Reject backslashes in raw input
	if strings.Contains(trimmed, "\\") {
		return "/"
	}

	// Reject protocol-relative leading slashes
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/\\") || strings.HasPrefix(trimmed, "\\/") || strings.HasPrefix(trimmed, "\\\\") {
		return "/"
	}

	// Unescape once to catch encoded bypasses like %2f%2f, %5c, %00, %09
	unescaped, err := url.QueryUnescape(trimmed)
	if err != nil {
		return "/"
	}
	if strings.Contains(unescaped, "\\") || strings.HasPrefix(unescaped, "//") || strings.HasPrefix(unescaped, "/\\") {
		return "/"
	}
	for i := 0; i < len(unescaped); i++ {
		if unescaped[i] < 32 || unescaped[i] == 127 {
			return "/"
		}
	}

	// Reject dangerous schemes in unescaped or raw input
	lowerUnesc := strings.ToLower(unescaped)
	if strings.Contains(lowerUnesc, "javascript:") || strings.Contains(lowerUnesc, "data:") ||
		strings.Contains(lowerUnesc, "vbscript:") || strings.Contains(lowerUnesc, "file:") {
		return "/"
	}

	// Parse relative URI
	u, err := url.Parse(trimmed)
	if err != nil || u.IsAbs() || u.Host != "" || u.Scheme != "" {
		return "/"
	}

	// Must start with exactly single slash
	if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}

	// Reject auth loops and internal API paths
	pathLower := strings.ToLower(u.Path)
	if pathLower == "/login" || strings.HasPrefix(pathLower, "/login/") ||
		pathLower == "/register" || strings.HasPrefix(pathLower, "/register/") ||
		pathLower == "/logout" || strings.HasPrefix(pathLower, "/logout/") ||
		pathLower == "/auth" || strings.HasPrefix(pathLower, "/auth/") ||
		pathLower == "/api" || strings.HasPrefix(pathLower, "/api/") {
		return "/"
	}

	// Rebuild clean relative URL
	cleanPath := u.Path
	if u.RawQuery != "" {
		cleanPath += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		cleanPath += "#" + u.Fragment
	}

	if !strings.HasPrefix(cleanPath, "/") || strings.HasPrefix(cleanPath, "//") {
		return "/"
	}

	return cleanPath
}

func (s *Service) RequestRegistrationCode(ctx context.Context, email, locale string) error {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidEmail", "invalid email format", nil, err)
	}

	if locale != "zh-CN" && locale != "en-US" {
		locale = "en-US"
	}

	emailHash := s.ComputeEmailLookupHash(normalized)
	now := s.clk.Now()

	// Check if user already exists
	if _, _, err := s.store.FindUserByEmailHash(ctx, emailHash); err == nil {
		// Anti-enumeration: return success without creating register challenge
		return nil
	}

	code, err := crypto.GenerateSixDigitCode()
	if err != nil {
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to generate verification code", nil, err)
	}
	codeHash := s.ComputeTokenHash(code)

	challengeID, _ := crypto.GenerateOpaqueToken(13) // ~26 chars
	challengeID = "ech_" + challengeID
	emailID, _ := crypto.GenerateOpaqueToken(13)
	emailID = "eml_" + emailID

	challenge := domain.EmailChallenge{
		ChallengeID:     challengeID,
		EmailLookupHash: emailHash,
		EmailCiphertext: []byte(normalized), // In memory/prototype
		EmailKeyVersion: 1,
		ChallengeType:   domain.ChallengeTypeRegister,
		CodeHash:        codeHash,
		CodeKeyVersion:  1,
		ChallengeStatus: domain.ChallengeStatusPending,
		AttemptCount:    0,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(s.cfg.AuthCodeTTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	idempotencyKey := crypto.HMACSHA256(s.hmacKey, []byte(challengeID+now.Format(time.RFC3339)))

	outbox := domain.EmailOutbox{
		EmailID:              emailID,
		ChallengeID:          &challengeID,
		IdempotencyKey:       idempotencyKey,
		TemplateKey:          "auth.register_code",
		Locale:               locale,
		RecipientCiphertext:  []byte(normalized),
		PayloadCiphertext:    []byte(fmt.Sprintf(`{"code":"%s"}`, code)),
		EncryptionKeyVersion: 1,
		DeliveryStatus:       "pending",
		AttemptCount:         0,
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(s.cfg.AuthCodeTTL),
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if _, err := s.store.CreateOrReplaceEmailChallenge(ctx, challenge, outbox); err != nil {
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to create email challenge", nil, err)
	}

	return nil
}

type RegistrationResult struct {
	Session      *domain.UserSession
	SessionToken string
	CSRFToken    string
	User         *domain.User
	ReturnTo     string
}

func (s *Service) CompleteRegistration(ctx context.Context, email, code, password, returnTo string) (*RegistrationResult, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidEmail", "invalid email", nil, err)
	}

	if len(password) < 8 || len(password) > 128 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "auth.invalidPasswordLength", "password must be between 8 and 128 characters", nil, nil)
	}

	emailHash := s.ComputeEmailLookupHash(normalized)
	now := s.clk.Now()

	challenge, err := s.store.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.challengeNotFound", "invalid or expired verification code", nil, err)
	}

	if now.After(challenge.ExpiresAt) {
		_ = s.store.UpdateEmailChallengeAttempts(ctx, challenge.ChallengeID, challenge.AttemptCount, domain.ChallengeStatusExpired)
		return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.codeExpired", "verification code expired", nil, nil)
	}

	expectedCodeHash := s.ComputeTokenHash(strings.TrimSpace(code))
	if !hmac.Equal(expectedCodeHash[:], challenge.CodeHash[:]) {
		newAttempts := challenge.AttemptCount + 1
		newStatus := domain.ChallengeStatusPending
		if newAttempts >= challenge.MaxAttempts {
			newStatus = domain.ChallengeStatusLocked
		}
		_ = s.store.UpdateEmailChallengeAttempts(ctx, challenge.ChallengeID, newAttempts, newStatus)
		if newStatus == domain.ChallengeStatusLocked {
			return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.codeLocked", "too many failed attempts, please request a new code", nil, nil)
		}
		return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.invalidCode", "invalid verification code", nil, nil)
	}

	// Check password hash
	params := crypto.DefaultArgon2Params
	if s.cfg.Argon2MemoryKiB <= 1024 {
		params = crypto.FastArgon2Params
	}
	pwdHash, err := crypto.HashPassword(password, params)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to hash password", nil, err)
	}

	userIDToken, _ := crypto.GenerateOpaqueToken(13)
	userID := "usr_" + userIDToken

	sessionIDToken, _ := crypto.GenerateOpaqueToken(13)
	sessionID := "ses_" + sessionIDToken

	sessionToken, _ := crypto.GenerateOpaqueToken(32)
	csrfToken, _ := crypto.GenerateOpaqueToken(32)

	sessionTokenHash := s.ComputeTokenHash(sessionToken)
	csrfTokenHash := s.ComputeTokenHash(csrfToken)

	subjectHash := crypto.SHA256([]byte("email:" + normalized))

	user := domain.User{
		UserID:                userID,
		AuthSubjectHash:       subjectHash,
		EmailLookupHash:       &emailHash,
		EmailCiphertext:       []byte(normalized),
		DisplayName:           "Token Dancer",
		AccountStatus:         domain.AccountStatusActive,
		LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
		TimezoneName:          "UTC",
		Locale:                "en-US",
		EmailVerifiedAt:       &now,
		ProfileVersion:        1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	credential := domain.UserPasswordCredential{
		UserID:            userID,
		PasswordHash:      pwdHash,
		PasswordAlgorithm: "argon2id",
		CredentialVersion: 1,
		FailedLoginCount:  0,
		PasswordChangedAt: now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	privacy := domain.UserPrivacySettings{
		UserID:               userID,
		PublicProfileEnabled: false,
		ShowBio:              false,
		ShowTokenTotal:       false,
		ShowTrends:           false,
		ShowActivityCalendar: false,
		ShowAgentBreakdown:   false,
		ShowSkillRanking:     false,
		ShowAchievements:     false,
		PrivacyVersion:       1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	session := domain.UserSession{
		SessionID:         sessionID,
		UserID:            userID,
		SessionTokenHash:  sessionTokenHash,
		CSRFTokenHash:     csrfTokenHash,
		CredentialVersion: 1,
		SessionStatus:     domain.SessionStatusActive,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(s.cfg.SessionIdleTTL),
		AbsoluteExpiresAt: now.Add(s.cfg.SessionAbsoluteTTL),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:      "sev_" + eventID,
		UserID:       &userID,
		SessionID:    &sessionID,
		EventType:    "registration_completed",
		Outcome:      "success",
		CreatedAt:    now,
		MetadataJSON: map[string]interface{}{"locale": "en-US"},
	}

	createdSession, err := s.store.CompleteRegistrationTx(ctx, store.RegistrationTxInput{
		User:          user,
		Credential:    credential,
		Privacy:       privacy,
		Session:       session,
		ChallengeID:   challenge.ChallengeID,
		SecurityEvent: event,
	})
	if err != nil {
		if err == domain.ErrAccountExists || err == domain.ErrConflict {
			return nil, domain.NewAppError(409, "AUTH_ACCOUNT_EXISTS", "auth.accountExists", "an account with this email already exists", nil, err)
		}
		if err == domain.ErrChallengeInvalid || err == domain.ErrChallengeExpired {
			return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.codeReused", "verification code has already been used or expired", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to complete registration", nil, err)
	}

	return &RegistrationResult{
		Session:      createdSession,
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
		User:         &user,
		ReturnTo:     s.SanitizeReturnTo(returnTo),
	}, nil
}

type LoginResult struct {
	Session      *domain.UserSession
	SessionToken string
	CSRFToken    string
	User         *domain.User
	ReturnTo     string
}

func (s *Service) Login(ctx context.Context, email, password, returnTo, deviceLabel string) (*LoginResult, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return nil, domain.NewAppError(401, "AUTH_INVALID_CREDENTIALS", "auth.invalidCredentials", "invalid email or password", nil, nil)
	}

	emailHash := s.ComputeEmailLookupHash(normalized)
	now := s.clk.Now()

	user, cred, err := s.store.FindUserByEmailHash(ctx, emailHash)
	if err != nil {
		// Anti-enumeration dummy hash check
		_, _ = crypto.VerifyPassword(password, s.dummyArgonHash)
		return nil, domain.NewAppError(401, "AUTH_INVALID_CREDENTIALS", "auth.invalidCredentials", "invalid email or password", nil, nil)
	}

	if cred.LockedUntil != nil && now.Before(*cred.LockedUntil) {
		return nil, domain.NewAppError(401, "AUTH_INVALID_CREDENTIALS", "auth.accountLocked", "account temporarily locked, please try again later", nil, nil)
	}

	match, verifyErr := crypto.VerifyPassword(password, cred.PasswordHash)
	if verifyErr != nil || !match {
		newFailed := cred.FailedLoginCount + 1
		var lockedUntil *time.Time
		if newFailed >= 10 {
			lockTime := now.Add(15 * time.Minute)
			lockedUntil = &lockTime
		}
		eventID, _ := crypto.GenerateOpaqueToken(13)
		event := domain.UserSecurityEvent{
			EventID:   "sev_" + eventID,
			UserID:    &user.UserID,
			EventType: "login_failed",
			Outcome:   "failure",
			CreatedAt: now,
		}
		_ = s.store.RecordLoginFailure(ctx, user.UserID, newFailed, lockedUntil, event)
		return nil, domain.NewAppError(401, "AUTH_INVALID_CREDENTIALS", "auth.invalidCredentials", "invalid email or password", nil, nil)
	}

	if user.AccountStatus == domain.AccountStatusDeleted {
		return nil, domain.NewAppError(401, "AUTH_INVALID_CREDENTIALS", "auth.accountDeleted", "account has been deleted", nil, nil)
	}

	sessionIDToken, _ := crypto.GenerateOpaqueToken(13)
	sessionID := "ses_" + sessionIDToken
	sessionToken, _ := crypto.GenerateOpaqueToken(32)
	csrfToken, _ := crypto.GenerateOpaqueToken(32)

	sessionTokenHash := s.ComputeTokenHash(sessionToken)
	csrfTokenHash := s.ComputeTokenHash(csrfToken)

	var label *string
	if deviceLabel != "" {
		label = &deviceLabel
	}

	session := domain.UserSession{
		SessionID:         sessionID,
		UserID:            user.UserID,
		SessionTokenHash:  sessionTokenHash,
		CSRFTokenHash:     csrfTokenHash,
		CredentialVersion: cred.CredentialVersion,
		SessionStatus:     domain.SessionStatusActive,
		DeviceLabel:       label,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(s.cfg.SessionIdleTTL),
		AbsoluteExpiresAt: now.Add(s.cfg.SessionAbsoluteTTL),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:      "sev_" + eventID,
		UserID:       &user.UserID,
		SessionID:    &sessionID,
		EventType:    "login_succeeded",
		Outcome:      "success",
		CreatedAt:    now,
		MetadataJSON: map[string]interface{}{"device": deviceLabel},
	}

	createdSession, err := s.store.CreateSessionTx(ctx, session, event)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to create session", nil, err)
	}

	return &LoginResult{
		Session:      createdSession,
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
		User:         user,
		ReturnTo:     s.SanitizeReturnTo(returnTo),
	}, nil
}

func (s *Service) ResolveSession(ctx context.Context, sessionToken string) (*domain.UserSession, *domain.User, error) {
	if sessionToken == "" {
		return nil, nil, domain.ErrUnauthorized
	}

	tokenHash := s.ComputeTokenHash(sessionToken)
	now := s.clk.Now()

	sess, user, err := s.store.ResolveSession(ctx, tokenHash, now)
	if err != nil {
		return nil, nil, domain.ErrUnauthorized
	}

	return sess, user, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	now := s.clk.Now()
	return s.store.RevokeSession(ctx, sessionID, "logout", now)
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID, currentSessionID string) error {
	now := s.clk.Now()
	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:   "sev_" + eventID,
		UserID:    &userID,
		SessionID: &currentSessionID,
		EventType: "sessions_revoked_others",
		Outcome:   "success",
		CreatedAt: now,
	}
	return s.store.RevokeOtherSessions(ctx, userID, currentSessionID, "logout_others", now, event)
}

func (s *Service) ListUserSessions(ctx context.Context, userID string) ([]domain.UserSession, error) {
	return s.store.ListUserSessions(ctx, userID)
}

func (s *Service) RevokeSessionByID(ctx context.Context, sessionID string, userID string) error {
	now := s.clk.Now()
	return s.store.RevokeSession(ctx, sessionID, "user_revoked", now)
}

func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidEmail", "invalid email", nil, err)
	}

	emailHash := s.ComputeEmailLookupHash(normalized)
	now := s.clk.Now()

	user, _, err := s.store.FindUserByEmailHash(ctx, emailHash)
	if err != nil {
		// Anti-enumeration: return success
		return nil
	}

	code, err := crypto.GenerateSixDigitCode()
	if err != nil {
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to generate code", nil, err)
	}
	codeHash := s.ComputeTokenHash(code)

	challengeID, _ := crypto.GenerateOpaqueToken(13)
	challengeID = "ech_" + challengeID
	emailID, _ := crypto.GenerateOpaqueToken(13)
	emailID = "eml_" + emailID

	challenge := domain.EmailChallenge{
		ChallengeID:     challengeID,
		UserID:          &user.UserID,
		EmailLookupHash: emailHash,
		EmailCiphertext: []byte(normalized),
		EmailKeyVersion: 1,
		ChallengeType:   domain.ChallengeTypePasswordReset,
		CodeHash:        codeHash,
		CodeKeyVersion:  1,
		ChallengeStatus: domain.ChallengeStatusPending,
		AttemptCount:    0,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(s.cfg.AuthCodeTTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	idempotencyKey := crypto.HMACSHA256(s.hmacKey, []byte(challengeID+now.Format(time.RFC3339)))

	outbox := domain.EmailOutbox{
		EmailID:              emailID,
		UserID:               &user.UserID,
		ChallengeID:          &challengeID,
		IdempotencyKey:       idempotencyKey,
		TemplateKey:          "auth.password_reset_code",
		Locale:               user.Locale,
		RecipientCiphertext:  []byte(normalized),
		PayloadCiphertext:    []byte(fmt.Sprintf(`{"code":"%s"}`, code)),
		EncryptionKeyVersion: 1,
		DeliveryStatus:       "pending",
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(s.cfg.AuthCodeTTL),
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	_, _ = s.store.CreateOrReplaceEmailChallenge(ctx, challenge, outbox)
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidEmail", "invalid email", nil, err)
	}

	if len(newPassword) < 8 || len(newPassword) > 128 {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "auth.invalidPasswordLength", "password must be between 8 and 128 characters", nil, nil)
	}

	emailHash := s.ComputeEmailLookupHash(normalized)
	now := s.clk.Now()

	user, cred, err := s.store.FindUserByEmailHash(ctx, emailHash)
	if err != nil {
		return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.challengeNotFound", "invalid or expired verification code", nil, nil)
	}

	challenge, err := s.store.FindPendingEmailChallenge(ctx, domain.ChallengeTypePasswordReset, emailHash)
	if err != nil {
		return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.challengeNotFound", "invalid or expired verification code", nil, nil)
	}

	if now.After(challenge.ExpiresAt) {
		_ = s.store.UpdateEmailChallengeAttempts(ctx, challenge.ChallengeID, challenge.AttemptCount, domain.ChallengeStatusExpired)
		return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.codeExpired", "verification code expired", nil, nil)
	}

	expectedCodeHash := s.ComputeTokenHash(strings.TrimSpace(code))
	if !hmac.Equal(expectedCodeHash[:], challenge.CodeHash[:]) {
		newAttempts := challenge.AttemptCount + 1
		newStatus := domain.ChallengeStatusPending
		if newAttempts >= challenge.MaxAttempts {
			newStatus = domain.ChallengeStatusLocked
		}
		_ = s.store.UpdateEmailChallengeAttempts(ctx, challenge.ChallengeID, newAttempts, newStatus)
		return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.invalidCode", "invalid verification code", nil, nil)
	}

	params := crypto.DefaultArgon2Params
	if s.cfg.Argon2MemoryKiB <= 1024 {
		params = crypto.FastArgon2Params
	}
	newHash, err := crypto.HashPassword(newPassword, params)
	if err != nil {
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to hash password", nil, err)
	}

	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:   "sev_" + eventID,
		UserID:    &user.UserID,
		EventType: "password_reset",
		Outcome:   "success",
		CreatedAt: now,
	}

	if err := s.store.ResetPasswordTx(ctx, user.UserID, challenge.ChallengeID, newHash, cred.CredentialVersion+1, event, now); err != nil {
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to reset password", nil, err)
	}

	return nil
}
