package device

import (
	"context"
	"encoding/hex"
	"strings"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store store.DeviceStore
	cfg   *config.Config
	clk   clock.Clock
}

func NewService(st store.Store, cfg *config.Config, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store: st.Device(),
		cfg:   cfg,
		clk:   clk,
	}
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
	codeHash := crypto.HMACSHA256([]byte(s.cfg.HMACSecret), []byte(normCode))

	now := s.clk.Now()
	expiresAt := now.Add(s.cfg.AuthBindCodeTTL)

	challengeIDToken, _ := crypto.GenerateOpaqueToken(13)
	challengeID := "dbc_" + challengeIDToken

	challenge := domain.DeviceBindingChallenge{
		ChallengeID:      challengeID,
		UserID:           userID,
		SessionID:        sessionID,
		CodeLookupHash:   codeHash,
		CodeKeyVersion:   1,
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

	codeHash := crypto.HMACSHA256([]byte(s.cfg.HMACSecret), []byte(normCode))
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

	claimed, err := s.store.ClaimInstallationTx(ctx, codeHash, inst, now)
	if err != nil {
		if err == domain.ErrChallengeInvalid {
			return nil, domain.NewAppError(400, "DEVICE_BINDING_INVALID", "device.invalidBinding", "invalid or expired binding code", nil, err)
		}
		if err == domain.ErrChallengeExpired {
			return nil, domain.NewAppError(400, "DEVICE_BINDING_EXPIRED", "device.bindingExpired", "binding code expired", nil, err)
		}
		if err == domain.ErrPublicKeyConflict {
			return nil, domain.NewAppError(409, "DEVICE_PUBLIC_KEY_CONFLICT", "device.publicKeyConflict", "device public key already registered to another user", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to claim installation", nil, err)
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
