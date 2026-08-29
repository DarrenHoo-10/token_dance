package device

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store       store.DeviceStore
	ingestStore store.IngestStore
	authStore   store.AuthStore
	cfg         *config.Config
	clk         clock.Clock
}

func NewService(st store.Store, cfg *config.Config, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store:       st.Device(),
		ingestStore: st.Ingest(),
		authStore:   st.Auth(),
		cfg:         cfg,
		clk:         clk,
	}
}

type DeviceGrantClaims struct {
	UserID     string `json:"userId"`
	SessionID  string `json:"sessionId"`
	Scope      string `json:"scope"`
	KeyVersion uint16 `json:"keyVersion"`
	ExpiresAt  int64  `json:"exp"`
}

type DeviceGrantResult struct {
	GrantToken string `json:"grantToken"`
	ExpiresAt  string `json:"expiresAt"`
}

func (s *Service) CreateDeviceGrant(ctx context.Context, userID, sessionID string) (*DeviceGrantResult, error) {
	u, err := s.authStore.FindUserByID(ctx, userID)
	if err != nil || u.AccountStatus != domain.AccountStatusActive || u.OnboardingCompletedAt == nil {
		return nil, domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.onboardingRequired", "active user with completed onboarding required", nil, domain.ErrForbidden)
	}

	now := s.clk.Now()
	exp := now.Add(5 * time.Minute)

	claims := DeviceGrantClaims{
		UserID:     userID,
		SessionID:  sessionID,
		Scope:      "installation:register",
		KeyVersion: s.cfg.GrantKeys.CurrentVersion,
		ExpiresAt:  exp.Unix(),
	}

	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to serialize grant claims", nil, err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)
	sig := crypto.HMACSHA256(s.cfg.GrantKeys.Current(), []byte(payloadB64))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig[:])

	token := "dgt_" + payloadB64 + "." + sigB64
	return &DeviceGrantResult{
		GrantToken: token,
		ExpiresAt:  exp.Format(time.RFC3339),
	}, nil
}

func (s *Service) ValidateDeviceGrant(ctx context.Context, grantToken string) (string, string, error) {
	if !strings.HasPrefix(grantToken, "dgt_") {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT_SCOPE", "auth.invalidGrantScope", "scoped grant required (dgt_...); web session bearer not permitted", nil, domain.ErrUnauthorized)
	}

	raw := strings.TrimPrefix(grantToken, "dgt_")
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.invalidGrant", "invalid grant token format", nil, domain.ErrUnauthorized)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.invalidGrant", "invalid grant payload", nil, domain.ErrUnauthorized)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(sigBytes) != 32 {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.invalidGrant", "invalid grant signature", nil, domain.ErrUnauthorized)
	}

	var claims DeviceGrantClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.invalidGrant", "failed to parse grant claims", nil, domain.ErrUnauthorized)
	}
	grantKey, ok := s.cfg.GrantKeys.Keys[claims.KeyVersion]
	if !ok {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.invalidGrant", "unknown grant key version", nil, domain.ErrUnauthorized)
	}
	expectedSig := crypto.HMACSHA256(grantKey, []byte(parts[0]))
	if subtle.ConstantTimeCompare(sigBytes, expectedSig[:]) != 1 {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.invalidGrant", "grant signature mismatch", nil, domain.ErrUnauthorized)
	}

	if claims.Scope != "installation:register" {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT_SCOPE", "auth.invalidGrantScope", "scoped grant required with installation:register scope", nil, domain.ErrUnauthorized)
	}

	now := s.clk.Now()
	if now.Unix() > claims.ExpiresAt {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.grantExpired", "grant token expired", nil, domain.ErrUnauthorized)
	}

	// Verify user active & onboarding completed
	u, err := s.authStore.FindUserByID(ctx, claims.UserID)
	if err != nil || u.AccountStatus != domain.AccountStatusActive || u.OnboardingCompletedAt == nil {
		return "", "", domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.onboardingRequired", "active user with completed onboarding required", nil, domain.ErrForbidden)
	}

	// Verify authorizing session is active
	sessions, err := s.authStore.ListUserSessions(ctx, claims.UserID)
	if err != nil {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.invalidGrant", "failed to list sessions for grant", nil, domain.ErrUnauthorized)
	}

	var sessValid bool
	for _, sess := range sessions {
		if sess.SessionID == claims.SessionID && sess.SessionStatus == domain.SessionStatusActive && now.Before(sess.AbsoluteExpiresAt) && now.Before(sess.IdleExpiresAt) {
			sessValid = true
			break
		}
	}
	if !sessValid {
		return "", "", domain.NewAppError(401, "AUTH_INVALID_GRANT", "auth.grantSessionExpired", "grant authorizing session revoked or expired", nil, domain.ErrUnauthorized)
	}

	return claims.UserID, claims.SessionID, nil
}

func (s *Service) ListDevices(ctx context.Context, userID string) ([]domain.Installation, error) {
	return s.store.ListInstallations(ctx, userID)
}

type CreateBindingResult struct {
	ChallengeID string `json:"challengeId"`
	Code        string `json:"code"`
	ExpiresAt   string `json:"expiresAt"`
}

func (s *Service) CreateBindingChallenge(ctx context.Context, userID, sessionID string) (*CreateBindingResult, error) {
	code, err := crypto.GenerateCrockfordCode(8)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to generate binding code", nil, err)
	}

	normCode := crypto.NormalizeCrockfordCode(code)
	codeHash := crypto.HMACSHA256(s.cfg.BindingCodeKeys.Current(), []byte(normCode))

	now := s.clk.Now()
	expiresAt := now.Add(s.cfg.AuthBindCodeTTL)

	challengeIDToken, _ := crypto.GenerateOpaqueToken(13)
	challengeID := "dbc_" + challengeIDToken

	challenge := domain.DeviceBindingChallenge{
		ChallengeID:      challengeID,
		UserID:           userID,
		SessionID:        sessionID,
		CodeLookupHash:   codeHash,
		CodeKeyVersion:   s.cfg.BindingCodeKeys.CurrentVersion,
		ChallengeStatus:  domain.ChallengeStatusPending,
		ExpiresAt:        expiresAt,
		ActiveSessionKey: &sessionID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	created, err := s.store.CreateBindingChallenge(ctx, challenge)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to create binding challenge", nil, err)
	}

	return &CreateBindingResult{
		ChallengeID: created.ChallengeID,
		Code:        code,
		ExpiresAt:   expiresAt.Format("2006-01-02T15:04:05.000Z"),
	}, nil
}

func (s *Service) CancelBindingChallenge(ctx context.Context, challengeID, userID string) error {
	return s.store.CancelBindingChallenge(ctx, challengeID, userID)
}

type ClaimInput struct {
	Code             string  `json:"code"`
	PublicKey        string  `json:"publicKey"`
	DeviceName       *string `json:"deviceName,omitempty"`
	OSType           string  `json:"osType"`
	OSVersion        *string `json:"osVersion,omitempty"`
	Architecture     string  `json:"architecture"`
	CollectorVersion string  `json:"collectorVersion"`
}

func (s *Service) ClaimInstallation(ctx context.Context, in ClaimInput) (*domain.Installation, error) {
	normCode := crypto.NormalizeCrockfordCode(in.Code)
	if len(normCode) != 8 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "device.invalidCode", "invalid binding code", nil, domain.ErrInvalidArgument)
	}

	pubKeyBytes, err := hex.DecodeString(strings.TrimSpace(in.PublicKey))
	if err != nil || len(pubKeyBytes) != 32 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "device.invalidPublicKey", "publicKey must be 32-byte hex encoded", nil, domain.ErrInvalidArgument)
	}

	var pubKey [32]byte
	copy(pubKey[:], pubKeyBytes)

	if in.OSType != "windows" && in.OSType != "macos" && in.OSType != "linux" {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "device.invalidOSType", "invalid osType", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()

	instIDToken, _ := crypto.GenerateOpaqueToken(13)
	instID := "ins_" + instIDToken

	inst := domain.Installation{
		InstallationID:     instID,
		DevicePublicKey:    pubKey,
		DeviceName:         in.DeviceName,
		OSType:             in.OSType,
		OSVersion:          in.OSVersion,
		Architecture:       in.Architecture,
		CollectorVersion:   in.CollectorVersion,
		InstallationStatus: domain.InstallationStatusActive,
		StatusVersion:      1,
		RegisteredAt:       now,
		UpdatedAt:          now,
	}

	var claimed *domain.Installation
	var claimErr error = domain.ErrChallengeInvalid
	for _, version := range s.cfg.BindingCodeKeys.Versions() {
		codeHash := crypto.HMACSHA256(s.cfg.BindingCodeKeys.Keys[version], []byte(normCode))
		claimed, claimErr = s.store.ClaimInstallationTx(ctx, codeHash, inst, now)
		if claimErr == nil || claimErr != domain.ErrChallengeInvalid {
			break
		}
	}
	if claimErr != nil {
		if claimErr == domain.ErrChallengeInvalid {
			return nil, domain.NewAppError(400, "DEVICE_BINDING_INVALID", "device.invalidBinding", "invalid or expired binding code", nil, claimErr)
		}
		if claimErr == domain.ErrChallengeExpired {
			return nil, domain.NewAppError(400, "DEVICE_BINDING_EXPIRED", "device.bindingExpired", "binding code expired", nil, claimErr)
		}
		if claimErr == domain.ErrPublicKeyConflict {
			return nil, domain.NewAppError(409, "DEVICE_PUBLIC_KEY_CONFLICT", "device.publicKeyConflict", "device public key already registered to another user", nil, claimErr)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to claim installation", nil, claimErr)
	}

	return claimed, nil
}

func (s *Service) RegisterInstallation(ctx context.Context, userID string, in ClaimInput) (*domain.Installation, error) {
	pubKeyBytes, err := hex.DecodeString(strings.TrimSpace(in.PublicKey))
	if err != nil || len(pubKeyBytes) != 32 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "device.invalidPublicKey", "publicKey must be 32-byte hex encoded", nil, domain.ErrInvalidArgument)
	}

	var pubKey [32]byte
	copy(pubKey[:], pubKeyBytes)

	now := s.clk.Now()
	instIDToken, _ := crypto.GenerateOpaqueToken(13)
	instID := "ins_" + instIDToken

	inst := domain.Installation{
		InstallationID:     instID,
		UserID:             userID,
		DevicePublicKey:    pubKey,
		DeviceName:         in.DeviceName,
		OSType:             in.OSType,
		OSVersion:          in.OSVersion,
		Architecture:       in.Architecture,
		CollectorVersion:   in.CollectorVersion,
		InstallationStatus: domain.InstallationStatusActive,
		StatusVersion:      1,
		RegisteredAt:       now,
		UpdatedAt:          now,
	}

	registered, err := s.store.RegisterInstallationTx(ctx, inst, now)
	if err != nil {
		if err == domain.ErrPublicKeyConflict {
			return nil, domain.NewAppError(409, "DEVICE_PUBLIC_KEY_CONFLICT", "device.publicKeyConflict", "device public key conflict", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to register installation", nil, err)
	}

	return registered, nil
}

func (s *Service) UpdateDeviceName(ctx context.Context, installationID, userID, name string) (*domain.Installation, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 120 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "device.invalidName", "device name must be 1-120 chars", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()
	inst, err := s.store.UpdateInstallationName(ctx, installationID, userID, name, now)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "device.notFound", "device not found", nil, err)
	}
	return inst, nil
}

func (s *Service) PauseDevice(ctx context.Context, installationID, userID string) (*domain.Installation, error) {
	now := s.clk.Now()
	inst, err := s.store.PauseInstallation(ctx, installationID, userID, "user_paused", now)
	if err != nil {
		if err == domain.ErrDeviceRevoked {
			return nil, domain.NewAppError(403, "DEVICE_REVOKED", "device.revoked", "device is revoked", nil, err)
		}
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "device.notFound", "device not found", nil, err)
	}
	return inst, nil
}

func (s *Service) ResumeDevice(ctx context.Context, installationID, userID string) (*domain.Installation, error) {
	now := s.clk.Now()
	inst, err := s.store.ResumeInstallation(ctx, installationID, userID, now)
	if err != nil {
		if err == domain.ErrDeviceRevoked {
			return nil, domain.NewAppError(403, "DEVICE_REVOKED", "device.revoked", "device is revoked", nil, err)
		}
		if err == domain.ErrForbidden {
			return nil, domain.NewAppError(403, "DEVICE_DISABLED", "device.policyDisabled", "device cannot be resumed by user", nil, err)
		}
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "device.notFound", "device not found", nil, err)
	}
	return inst, nil
}

func (s *Service) RevokeDevice(ctx context.Context, installationID, userID string) (*domain.Installation, error) {
	now := s.clk.Now()
	inst, err := s.store.RevokeInstallation(ctx, installationID, userID, now)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "device.notFound", "device not found", nil, err)
	}
	return inst, nil
}

func (s *Service) GetIngestInstallation(ctx context.Context, installationID string) (*domain.Installation, error) {
	inst, err := s.ingestStore.GetIngestInstallation(ctx, installationID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.NewAppError(401, "DEVICE_AUTH_INVALID", "device.invalidSignature", "invalid device authentication", nil, domain.ErrUnauthorized)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to load ingest installation", nil, err)
	}
	return inst, nil
}

func (s *Service) CommitIngest(ctx context.Context, batch domain.IngestBatch) (*domain.IngestResult, error) {
	result, err := s.ingestStore.CommitIngest(ctx, batch)
	if err == nil {
		return result, nil
	}
	switch err {
	case domain.ErrNotFound:
		return nil, domain.NewAppError(401, "DEVICE_AUTH_INVALID", "device.invalidSignature", "invalid device authentication", nil, domain.ErrUnauthorized)
	case domain.ErrDeviceRevoked:
		return nil, domain.NewAppError(403, "DEVICE_REVOKED", "device.revoked", "device is revoked", nil, err)
	case domain.ErrDeviceDisabled:
		return nil, domain.NewAppError(403, "DEVICE_DISABLED", "device.disabled", "device is disabled", nil, err)
	case domain.ErrAccountSuspended:
		return nil, domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.accountSuspended", "user account is not active", nil, err)
	case domain.ErrNonceReplay:
		return nil, domain.NewAppError(409, "INGEST_NONCE_REPLAY", "ingest.nonceReplay", "telemetry nonce has already been used", nil, err)
	case domain.ErrBatchHashConflict:
		return nil, domain.NewAppError(409, "INGEST_BATCH_HASH_CONFLICT", "ingest.batchHashConflict", "batch id was already used with a different request body", nil, err)
	default:
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to commit telemetry batch", nil, err)
	}
}

func (s *Service) AuthorizeIngest(ctx context.Context, installationID string) (*domain.Installation, *domain.User, error) {
	inst, user, err := s.store.AuthorizeIngest(ctx, installationID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, nil, domain.NewAppError(404, "DEVICE_NOT_FOUND", "device.notFound", "installation not found", nil, err)
		}
		if err == domain.ErrDeviceRevoked {
			return nil, nil, domain.NewAppError(403, "DEVICE_REVOKED", "device.revoked", "device is revoked", nil, err)
		}
		if err == domain.ErrDeviceDisabled {
			return nil, nil, domain.NewAppError(403, "DEVICE_DISABLED", "device.disabled", "device is disabled", nil, err)
		}
		if err == domain.ErrAccountSuspended {
			return nil, nil, domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.accountSuspended", "user account is not active", nil, err)
		}
		return nil, nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to authorize ingest", nil, err)
	}
	return inst, user, nil
}
