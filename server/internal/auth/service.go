package auth

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	emailpkg "tokendance/internal/email"
	"tokendance/internal/profile"
	"tokendance/internal/store"
)

type Service struct {
	store     store.AuthStore
	pStore    store.ProfileStore
	cfg       *config.Config
	clk       clock.Clock
	cipher    *crypto.AEADCipher
	emailSink *emailpkg.DeliverySink
	smtpMail  emailpkg.Provider

	dummyArgonHash string
}

func NewService(st store.Store, cfg *config.Config, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}

	// Precalculate a valid dummy Argon2 hash for constant-time dummy verification on missing accounts
	params := crypto.DefaultArgon2Params
	if cfg.Argon2MemoryKiB <= 1024 {
		params = crypto.FastArgon2Params
	}
	dummyHash, _ := crypto.HashPassword("tokendance-dummy-timing-password-constant", params)

	cipher, _ := crypto.NewAEADCipherKeyring(cfg.AEADKeys.Keys, cfg.AEADKeys.CurrentVersion)

	var sink *emailpkg.DeliverySink
	if cfg.Environment != "production" {
		sink = emailpkg.DefaultSink
	}

	return &Service{
		store:          st.Auth(),
		pStore:         st.Profile(),
		cfg:            cfg,
		clk:            clk,
		cipher:         cipher,
		emailSink:      sink,
		dummyArgonHash: dummyHash,
	}
}

func (s *Service) SetEmailSink(sink *emailpkg.DeliverySink) {
	s.emailSink = sink
}

// SetSMTPProvider enables direct in-request delivery through an SMTP provider.
// Used when the deployment configures a real mail transport; the durable
// worker outbox path remains the production alternative.
func (s *Service) SetSMTPProvider(p emailpkg.Provider) {
	s.smtpMail = p
}

func (s *Service) EmailSink() *emailpkg.DeliverySink {
	return s.emailSink
}

func (s *Service) Cipher() *crypto.AEADCipher {
	return s.cipher
}

func (s *Service) IsProduction() bool {
	return s.cfg != nil && s.cfg.Environment == "production"
}

func (s *Service) DecryptUserEmail(u *domain.User) (string, error) {
	if u == nil || len(u.EmailCiphertext) == 0 {
		return "", nil
	}
	if s.cipher == nil {
		return string(u.EmailCiphertext), nil
	}
	plain, err := s.cipher.Decrypt(u.EmailCiphertext, []byte("users.email"))
	if err != nil {
		return "", fmt.Errorf("failed to decrypt user email: %w", err)
	}
	return string(plain), nil
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

func hashWithVersion(ring config.VersionedKeyring, version uint16, value string) [32]byte {
	return crypto.HMACSHA256(ring.Keys[version], []byte(value))
}

func (s *Service) ComputeEmailLookupHash(normalizedEmail string) [32]byte {
	return hashWithVersion(s.cfg.EmailLookupKeys, s.cfg.EmailLookupKeys.CurrentVersion, normalizedEmail)
}

func (s *Service) ComputeTokenHash(token string) [32]byte {
	return hashWithVersion(s.cfg.VerificationCodeKeys, s.cfg.VerificationCodeKeys.CurrentVersion, token)
}

func (s *Service) ComputeCSRFHash(token string) [32]byte {
	return hashWithVersion(s.cfg.CSRFKeys, s.cfg.CSRFKeys.CurrentVersion, token)
}

func (s *Service) ValidateCSRFToken(token string, expected [32]byte) bool {
	for _, version := range s.cfg.CSRFKeys.Versions() {
		actual := hashWithVersion(s.cfg.CSRFKeys, version, token)
		if hmac.Equal(actual[:], expected[:]) {
			return true
		}
	}
	return false
}

func (s *Service) deriveCSRFToken(sessionToken string, version uint16) string {
	token := hashWithVersion(s.cfg.CSRFKeys, version, "csrf:"+sessionToken)
	return base64.RawURLEncoding.EncodeToString(token[:])
}

func (s *Service) computeSessionHashes(token string) [][32]byte {
	hashes := make([][32]byte, 0, len(s.cfg.SessionKeys.Keys))
	for _, version := range s.cfg.SessionKeys.Versions() {
		hashes = append(hashes, hashWithVersion(s.cfg.SessionKeys, version, token))
	}
	return hashes
}

func (s *Service) findUserByEmail(ctx context.Context, normalized string) (*domain.User, *domain.UserPasswordCredential, [32]byte, error) {
	for _, version := range s.cfg.EmailLookupKeys.Versions() {
		hash := hashWithVersion(s.cfg.EmailLookupKeys, version, normalized)
		user, credential, err := s.store.FindUserByEmailHash(ctx, hash)
		if err == nil {
			return user, credential, hash, nil
		}
	}
	return nil, nil, [32]byte{}, domain.ErrNotFound
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
	if _, _, _, err := s.findUserByEmail(ctx, normalized); err == nil {
		// Anti-enumeration: return success without creating register challenge
		return nil
	}

	code := ""
	if s.cfg.Environment != "production" && s.cfg.TestAuthCode != "" {
		code = s.cfg.TestAuthCode
	} else {
		var err error
		code, err = crypto.GenerateSixDigitCode()
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to generate verification code", nil, err)
		}
	}
	codeHash := s.ComputeTokenHash(code)

	challengeID, _ := crypto.GenerateOpaqueToken(13) // ~26 chars
	challengeID = "ech_" + challengeID
	emailID, _ := crypto.GenerateOpaqueToken(13)
	emailID = "eml_" + emailID

	var emailCiphertext []byte
	var emailKeyVersion uint16 = 1
	if s.cipher != nil {
		emailKeyVersion = s.cipher.KeyVersion()
		ct, err := s.cipher.Encrypt([]byte(normalized), []byte("email_challenges.email"))
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to encrypt email challenge", nil, err)
		}
		emailCiphertext = ct
	} else {
		emailCiphertext = []byte(normalized)
	}

	challenge := domain.EmailChallenge{
		ChallengeID:     challengeID,
		EmailLookupHash: emailHash,
		EmailCiphertext: emailCiphertext,
		EmailKeyVersion: emailKeyVersion,
		ChallengeType:   domain.ChallengeTypeRegister,
		CodeHash:        codeHash,
		CodeKeyVersion:  s.cfg.VerificationCodeKeys.CurrentVersion,
		ChallengeStatus: domain.ChallengeStatusPending,
		AttemptCount:    0,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(s.cfg.AuthCodeTTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	idempotencyKey := hashWithVersion(s.cfg.IdempotencyKeys, s.cfg.IdempotencyKeys.CurrentVersion, challengeID+now.Format(time.RFC3339))

	payloadJSON := fmt.Sprintf(`{"code":"%s"}`, code)
	var recipientCiphertext, payloadCiphertext []byte
	var outboxKeyVersion uint16 = 1
	if s.cipher != nil {
		outboxKeyVersion = s.cipher.KeyVersion()
		rcptCT, err := s.cipher.Encrypt([]byte(normalized), []byte("email_outbox.recipient"))
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to encrypt outbox recipient", nil, err)
		}
		recipientCiphertext = rcptCT

		payloadCT, err := s.cipher.Encrypt([]byte(payloadJSON), []byte("email_outbox.payload"))
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to encrypt outbox payload", nil, err)
		}
		payloadCiphertext = payloadCT
	} else {
		recipientCiphertext = []byte(normalized)
		payloadCiphertext = []byte(payloadJSON)
	}

	outbox := domain.EmailOutbox{
		EmailID:              emailID,
		ChallengeID:          &challengeID,
		IdempotencyKey:       idempotencyKey,
		TemplateKey:          "auth.register_code",
		Locale:               locale,
		RecipientCiphertext:  recipientCiphertext,
		PayloadCiphertext:    payloadCiphertext,
		EncryptionKeyVersion: outboxKeyVersion,
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

	if err := s.deliverCodeEmail(ctx, emailpkg.Message{
		EmailID:     emailID,
		Recipient:   normalized,
		TemplateKey: "auth.register_code",
		Locale:      locale,
		PayloadJSON: payloadJSON,
		CreatedAt:   now,
	}); err != nil {
		return domain.NewAppError(503, "API_EMAIL_UNAVAILABLE", "auth.emailDeliveryUnavailable", "failed to send verification email", nil, err)
	}

	return nil
}

// deliverCodeEmail sends a verification code email through the SMTP provider
// when one is configured, falling back to the dev/test delivery sink.
func (s *Service) deliverCodeEmail(ctx context.Context, msg emailpkg.Message) error {
	if s.smtpMail != nil {
		if _, err := s.smtpMail.Send(ctx, msg); err != nil {
			return err
		}
		return nil
	}
	if s.emailSink != nil {
		_, _ = s.emailSink.Send(ctx, msg)
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

// defaultHandleFromEmail derives a readable handle candidate from the email
// local part; ok is false when nothing usable remains after sanitizing.
func defaultHandleFromEmail(email string) (string, bool) {
	local := strings.ToLower(strings.SplitN(email, "@", 2)[0])
	var b strings.Builder
	for _, r := range local {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	stem := strings.Trim(b.String(), "_")
	stem = strings.TrimLeft(stem, "0123456789_")
	if len(stem) > 24 {
		stem = stem[:24]
	}
	if len(stem) < 3 {
		return "", false
	}
	return stem, true
}

func randomHandleSuffix(n int) string {
	b, err := crypto.GenerateRandomBytes(n)
	if err != nil {
		return strings.Repeat("x", n)
	}
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, n)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

// reserveDefaultHandle picks an available handle for a brand-new account so
// registration completes with usable defaults and no manual profile step.
func (s *Service) reserveDefaultHandle(ctx context.Context, email string, now time.Time) string {
	stem, ok := defaultHandleFromEmail(email)
	candidates := make([]string, 0, 5)
	if ok {
		candidates = append(candidates, stem)
	}
	for i := 0; i < 3; i++ {
		suffix := randomHandleSuffix(4)
		if ok {
			candidates = append(candidates, stem+"_"+suffix)
		} else {
			candidates = append(candidates, "dancer_"+suffix)
		}
	}
	for _, candidate := range candidates {
		if profile.ValidateHandle(candidate) != nil {
			continue
		}
		avail, err := s.pStore.IsHandleAvailable(ctx, candidate, "", now)
		if err == nil && avail {
			return candidate
		}
	}
	return "dancer_" + randomHandleSuffix(8)
}

func (s *Service) CompleteRegistration(ctx context.Context, email, code, password, returnTo, locale, timezone string) (*RegistrationResult, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidEmail", "invalid email", nil, err)
	}

	if len(password) < 8 || len(password) > 128 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "auth.invalidPasswordLength", "password must be between 8 and 128 characters", nil, nil)
	}

	if locale != "zh-CN" && locale != "en-US" {
		locale = "en-US"
	}
	timezone = strings.TrimSpace(timezone)
	if timezone != "" {
		if _, tzErr := time.LoadLocation(timezone); tzErr != nil {
			timezone = "UTC"
		}
	} else {
		timezone = "UTC"
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

	expectedCodeHash := hashWithVersion(s.cfg.VerificationCodeKeys, challenge.CodeKeyVersion, strings.TrimSpace(code))
	if !hmac.Equal(expectedCodeHash[:], challenge.CodeHash[:]) {
		failureErr := s.store.RecordEmailChallengeFailure(ctx, challenge.ChallengeID, now)
		if errors.Is(failureErr, domain.ErrChallengeLocked) {
			return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.codeLocked", "too many failed attempts, please request a new code", nil, failureErr)
		}
		if failureErr != nil && !errors.Is(failureErr, domain.ErrChallengeInvalid) {
			return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.invalidCode", "invalid verification code", nil, failureErr)
		}
		return nil, domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.invalidCode", "invalid verification code", nil, domain.ErrChallengeInvalid)
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

	defaultHandle := s.reserveDefaultHandle(ctx, normalized, now)

	sessionIDToken, _ := crypto.GenerateOpaqueToken(13)
	sessionID := "ses_" + sessionIDToken

	sessionToken, _ := crypto.GenerateOpaqueToken(32)
	csrfToken := s.deriveCSRFToken(sessionToken, s.cfg.CSRFKeys.CurrentVersion)

	sessionTokenHash := hashWithVersion(s.cfg.SessionKeys, s.cfg.SessionKeys.CurrentVersion, sessionToken)
	csrfTokenHash := s.ComputeCSRFHash(csrfToken)

	subjectHash := hashWithVersion(s.cfg.AuthSubjectKeys, s.cfg.AuthSubjectKeys.CurrentVersion, "email:"+normalized)

	var userEmailCiphertext []byte
	if s.cipher != nil {
		ct, err := s.cipher.Encrypt([]byte(normalized), []byte("users.email"))
		if err != nil {
			return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to encrypt user email", nil, err)
		}
		userEmailCiphertext = ct
	} else {
		userEmailCiphertext = []byte(normalized)
	}

	user := domain.User{
		UserID:                userID,
		AuthSubjectHash:       subjectHash,
		EmailLookupHash:       &emailHash,
		EmailCiphertext:       userEmailCiphertext,
		Handle:                &defaultHandle,
		DisplayName:           "Token Dancer",
		AccountStatus:         domain.AccountStatusActive,
		LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
		TimezoneName:          timezone,
		Locale:                locale,
		EmailVerifiedAt:       &now,
		OnboardingCompletedAt: &now,
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
		UserID:                userID,
		PublicProfileEnabled:  false,
		LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
		ShowBio:               false,
		ShowTokenTotal:        false,
		ShowTrends:            false,
		ShowActivityCalendar:  false,
		ShowAgentBreakdown:    false,
		ShowSkillRanking:      false,
		ShowAchievements:      false,
		PrivacyVersion:        1,
		CreatedAt:             now,
		UpdatedAt:             now,
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
		MetadataJSON: map[string]interface{}{"locale": locale, "handle": defaultHandle},
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

	now := s.clk.Now()

	user, cred, _, err := s.findUserByEmail(ctx, normalized)
	if err != nil {
		// Anti-enumeration dummy hash check
		_, _ = crypto.VerifyPassword(password, s.dummyArgonHash)
		return nil, domain.NewAppError(401, "AUTH_INVALID_CREDENTIALS", "auth.invalidCredentials", "invalid email or password", nil, nil)
	}

	if cred.LockedUntil != nil && now.Before(*cred.LockedUntil) {
		// Preserve the same password-work and response shape as unknown or bad-password accounts.
		_, _ = crypto.VerifyPassword(password, s.dummyArgonHash)
		return nil, domain.NewAppError(401, "AUTH_INVALID_CREDENTIALS", "auth.invalidCredentials", "invalid email or password", nil, nil)
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
	csrfToken := s.deriveCSRFToken(sessionToken, s.cfg.CSRFKeys.CurrentVersion)

	sessionTokenHash := hashWithVersion(s.cfg.SessionKeys, s.cfg.SessionKeys.CurrentVersion, sessionToken)
	csrfTokenHash := s.ComputeCSRFHash(csrfToken)

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

	now := s.clk.Now()
	for _, tokenHash := range s.computeSessionHashes(sessionToken) {
		sess, user, err := s.store.ResolveSession(ctx, tokenHash, now)
		if err == nil {
			for _, version := range s.cfg.CSRFKeys.Versions() {
				csrfToken := s.deriveCSRFToken(sessionToken, version)
				csrfHash := hashWithVersion(s.cfg.CSRFKeys, version, csrfToken)
				if hmac.Equal(csrfHash[:], sess.CSRFTokenHash[:]) {
					sess.CSRFToken = csrfToken
					break
				}
			}
			return sess, user, nil
		}
	}
	return nil, nil, domain.ErrUnauthorized
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

func (s *Service) Config() *config.Config {
	return s.cfg
}

func (s *Service) RotateCSRFToken(ctx context.Context, sessionID string) (string, error) {
	csrfToken, err := crypto.GenerateOpaqueToken(32)
	if err != nil {
		return "", err
	}
	csrfTokenHash := s.ComputeTokenHash(csrfToken)
	now := s.clk.Now()
	if err := s.store.RotateSessionCSRF(ctx, sessionID, csrfTokenHash, now); err != nil {
		return "", err
	}
	return csrfToken, nil
}

func (s *Service) RevokeSessionByID(ctx context.Context, sessionID string, userID string) error {
	now := s.clk.Now()
	err := s.store.RevokeUserSession(ctx, sessionID, userID, "logout", now)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewAppError(404, "RESOURCE_NOT_FOUND", "auth.sessionNotFound", "session not found", nil, err)
		}
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to revoke session", nil, err)
	}
	return nil
}

func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidEmail", "invalid email", nil, err)
	}

	now := s.clk.Now()

	user, _, emailHash, err := s.findUserByEmail(ctx, normalized)
	if err != nil {
		// Anti-enumeration: return success
		return nil
	}

	code := ""
	if s.cfg.Environment != "production" && s.cfg.TestAuthCode != "" {
		code = s.cfg.TestAuthCode
	} else {
		var err error
		code, err = crypto.GenerateSixDigitCode()
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to generate code", nil, err)
		}
	}
	codeHash := s.ComputeTokenHash(code)

	challengeID, _ := crypto.GenerateOpaqueToken(13)
	challengeID = "ech_" + challengeID
	emailID, _ := crypto.GenerateOpaqueToken(13)
	emailID = "eml_" + emailID

	var emailCiphertext []byte
	var emailKeyVersion uint16 = 1
	if s.cipher != nil {
		emailKeyVersion = s.cipher.KeyVersion()
		ct, err := s.cipher.Encrypt([]byte(normalized), []byte("email_challenges.email"))
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to encrypt email challenge", nil, err)
		}
		emailCiphertext = ct
	} else {
		emailCiphertext = []byte(normalized)
	}

	challenge := domain.EmailChallenge{
		ChallengeID:     challengeID,
		UserID:          &user.UserID,
		EmailLookupHash: emailHash,
		EmailCiphertext: emailCiphertext,
		EmailKeyVersion: emailKeyVersion,
		ChallengeType:   domain.ChallengeTypePasswordReset,
		CodeHash:        codeHash,
		CodeKeyVersion:  s.cfg.VerificationCodeKeys.CurrentVersion,
		ChallengeStatus: domain.ChallengeStatusPending,
		AttemptCount:    0,
		MaxAttempts:     6,
		SendCount:       1,
		ExpiresAt:       now.Add(s.cfg.AuthCodeTTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	idempotencyKey := hashWithVersion(s.cfg.IdempotencyKeys, s.cfg.IdempotencyKeys.CurrentVersion, challengeID+now.Format(time.RFC3339))

	payloadJSON := fmt.Sprintf(`{"code":"%s"}`, code)
	var recipientCiphertext, payloadCiphertext []byte
	var outboxKeyVersion uint16 = 1
	if s.cipher != nil {
		outboxKeyVersion = s.cipher.KeyVersion()
		rcptCT, err := s.cipher.Encrypt([]byte(normalized), []byte("email_outbox.recipient"))
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to encrypt outbox recipient", nil, err)
		}
		recipientCiphertext = rcptCT

		payloadCT, err := s.cipher.Encrypt([]byte(payloadJSON), []byte("email_outbox.payload"))
		if err != nil {
			return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to encrypt outbox payload", nil, err)
		}
		payloadCiphertext = payloadCT
	} else {
		recipientCiphertext = []byte(normalized)
		payloadCiphertext = []byte(payloadJSON)
	}

	outbox := domain.EmailOutbox{
		EmailID:              emailID,
		UserID:               &user.UserID,
		ChallengeID:          &challengeID,
		IdempotencyKey:       idempotencyKey,
		TemplateKey:          "auth.password_reset_code",
		Locale:               user.Locale,
		RecipientCiphertext:  recipientCiphertext,
		PayloadCiphertext:    payloadCiphertext,
		EncryptionKeyVersion: outboxKeyVersion,
		DeliveryStatus:       "pending",
		NextAttemptAt:        now,
		ExpiresAt:            now.Add(s.cfg.AuthCodeTTL),
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	_, _ = s.store.CreateOrReplaceEmailChallenge(ctx, challenge, outbox)

	if err := s.deliverCodeEmail(ctx, emailpkg.Message{
		EmailID:     emailID,
		Recipient:   normalized,
		TemplateKey: "auth.password_reset_code",
		Locale:      user.Locale,
		PayloadJSON: payloadJSON,
		CreatedAt:   now,
	}); err != nil {
		return domain.NewAppError(503, "API_EMAIL_UNAVAILABLE", "auth.emailDeliveryUnavailable", "failed to send verification email", nil, err)
	}

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

	_, _, emailHash, lookupErr := s.findUserByEmail(ctx, normalized)
	if lookupErr != nil {
		return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.challengeNotFound", "invalid or expired verification code", nil, nil)
	}
	challenge, challengeErr := s.store.FindPendingEmailChallenge(ctx, domain.ChallengeTypePasswordReset, emailHash)
	if challengeErr != nil {
		return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.challengeNotFound", "invalid or expired verification code", nil, nil)
	}
	codeHash := hashWithVersion(s.cfg.VerificationCodeKeys, challenge.CodeKeyVersion, strings.TrimSpace(code))
	now := s.clk.Now()

	params := crypto.DefaultArgon2Params
	if s.cfg.Argon2MemoryKiB <= 1024 {
		params = crypto.FastArgon2Params
	}
	newHash, err := crypto.HashPassword(newPassword, params)
	if err != nil {
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to hash password", nil, err)
	}

	// Fetch current credential version
	_, cred, err := s.store.FindUserByEmailHash(ctx, emailHash)
	var newVersion uint32 = 1
	if err == nil && cred != nil {
		newVersion = cred.CredentialVersion + 1
	}

	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:   "sev_" + eventID,
		EventType: "password_reset",
		Outcome:   "success",
		CreatedAt: now,
	}

	if err := s.store.ResetPasswordTx(ctx, emailHash, codeHash, newHash, newVersion, event, now); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.challengeNotFound", "invalid or expired verification code", nil, nil)
		}
		if errors.Is(err, domain.ErrChallengeExpired) {
			return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.codeExpired", "verification code expired", nil, nil)
		}
		if errors.Is(err, domain.ErrChallengeInvalid) || errors.Is(err, domain.ErrChallengeLocked) {
			return domain.NewAppError(400, "AUTH_INVALID_CREDENTIALS", "auth.invalidCode", "invalid verification code", nil, nil)
		}
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to reset password", nil, err)
	}

	return nil
}
