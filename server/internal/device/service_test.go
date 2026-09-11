package device

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
	"tokendance/internal/store/memory"
)

func TestUSR023_DevicePauseResumeRevokeLifecycle(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	cfg := config.DefaultConfig()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, cfg, clk)

	userID := "usr_devicetest"
	now := clk.Now()
	_, sess, _ := st.SeedUserForTest(userID, "devuser", "dev@tokendance.dev", now)

	// 1. Create Binding Challenge
	res, err := svc.CreateBindingChallenge(ctx, userID, sess.SessionID)
	if err != nil {
		t.Fatalf("failed to create binding challenge: %v", err)
	}
	if len(res.Code) != 8 {
		t.Errorf("expected 8 char Crockford code, got %s", res.Code)
	}

	// 2. Collector Claims Installation
	pubKey := hex.EncodeToString([]byte("32-bytes-ed25519-public-key-here"))
	devName := "MacBook Pro M3"
	claimIn := ClaimInput{
		Code:             res.Code,
		PublicKey:        pubKey,
		DeviceName:       &devName,
		OSType:           "macos",
		Architecture:     "arm64",
		CollectorVersion: "1.0.0",
	}

	inst, err := svc.ClaimInstallation(ctx, claimIn)
	if err != nil {
		t.Fatalf("failed to claim installation: %v", err)
	}
	if inst.InstallationStatus != domain.InstallationStatusActive {
		t.Errorf("expected active status")
	}
	if _, _, err := svc.AuthorizeIngest(ctx, inst.InstallationID); err != nil {
		t.Fatalf("active device ingest should be authorized: %v", err)
	}

	// 3. List Devices
	devices, err := svc.ListDevices(ctx, userID)
	if err != nil {
		t.Fatalf("failed to list devices: %v", err)
	}
	if len(devices) != 1 || devices[0].InstallationID != inst.InstallationID {
		t.Errorf("expected 1 device in list")
	}

	// 4. Update Device Name
	renamed, err := svc.UpdateDeviceName(ctx, inst.InstallationID, userID, "Work Laptop")
	if err != nil {
		t.Fatalf("failed to update device name: %v", err)
	}
	if *renamed.DeviceName != "Work Laptop" {
		t.Errorf("expected Work Laptop, got %v", renamed.DeviceName)
	}

	// 5. Pause Device
	paused, err := svc.PauseDevice(ctx, inst.InstallationID, userID)
	if err != nil {
		t.Fatalf("failed to pause device: %v", err)
	}
	if paused.InstallationStatus != domain.InstallationStatusDisabled || *paused.DisabledReason != "user_paused" {
		t.Errorf("expected disabled/user_paused state")
	}
	if _, _, err := svc.AuthorizeIngest(ctx, inst.InstallationID); err == nil {
		t.Fatal("paused device ingest should be rejected")
	} else {
		var appErr *domain.AppError
		if !errors.As(err, &appErr) || appErr.Code != "DEVICE_DISABLED" {
			t.Fatalf("expected DEVICE_DISABLED while paused, got %v", err)
		}
	}

	// 6. Resume Device
	resumed, err := svc.ResumeDevice(ctx, inst.InstallationID, userID)
	if err != nil {
		t.Fatalf("failed to resume device: %v", err)
	}
	if resumed.InstallationStatus != domain.InstallationStatusActive {
		t.Errorf("expected active state after resume")
	}
	if _, _, err := svc.AuthorizeIngest(ctx, inst.InstallationID); err != nil {
		t.Fatalf("resumed device ingest should be authorized: %v", err)
	}

	// 7. Revoke Device
	revoked, err := svc.RevokeDevice(ctx, inst.InstallationID, userID)
	if err != nil {
		t.Fatalf("failed to revoke device: %v", err)
	}
	if revoked.InstallationStatus != domain.InstallationStatusRevoked {
		t.Errorf("expected revoked status")
	}
	if _, _, err := svc.AuthorizeIngest(ctx, inst.InstallationID); err == nil {
		t.Fatal("revoked device ingest should be rejected")
	} else {
		var appErr *domain.AppError
		if !errors.As(err, &appErr) || appErr.Code != "DEVICE_REVOKED" {
			t.Fatalf("expected DEVICE_REVOKED after revocation, got %v", err)
		}
	}
	if _, err := svc.ResumeDevice(ctx, inst.InstallationID, userID); err == nil {
		t.Fatal("revoked device must not be resumable")
	}
}

func TestAggregateRejectsCollectorsOlderThanFloor(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	cfg := config.DefaultConfig()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	svc := NewService(st, cfg, clock.NewMockClock(now))

	userID := "usr_aggversion"
	_, sess, _ := st.SeedUserForTest(userID, "aggver", "aggver@tokendance.dev", now)

	claim := func(version string) *domain.Installation {
		t.Helper()
		res, err := svc.CreateBindingChallenge(ctx, userID, sess.SessionID)
		if err != nil {
			t.Fatalf("binding challenge: %v", err)
		}
		inst, err := svc.ClaimInstallation(ctx, ClaimInput{
			Code: res.Code,
			PublicKey: func() string {
				sum := sha256.Sum256([]byte("aggregate-version-probe:" + version))
				return hex.EncodeToString(sum[:])
			}(),
			OSType:           "windows",
			Architecture:     "x86_64",
			CollectorVersion: version,
		})
		if err != nil {
			t.Fatalf("claim %q: %v", version, err)
		}
		return inst
	}

	commit := func(inst *domain.Installation) *domain.AppError {
		t.Helper()
		_, err := svc.CommitAggregate(ctx, domain.AggregateCommit{
			Snapshot: domain.AggregateSnapshot{
				SchemaVersion: 1,
				Revision:      1,
				Day:           "2026-09-11",
				Rows: []domain.AggregateRow{{
					Kind:    "agent",
					AgentID: "codex",
					Metrics: map[string]string{"exact_token_total": "1"},
				}},
			},
			InstallationID: inst.InstallationID,
			ReceivedAt:     now,
		})
		var appErr *domain.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected AppError, got %v", err)
		}
		return appErr
	}

	for _, version := range []string{"0.1.21", "0.1.7", "v0.1.21", "", "1.0.0-rc", "abc"} {
		if appErr := commit(claim(version)); appErr.Code != "CLIENT_VERSION_UNSUPPORTED" {
			t.Fatalf("version %q: expected CLIENT_VERSION_UNSUPPORTED, got %v", version, appErr.Code)
		}
	}
	for _, version := range []string{"0.1.22", "0.1.24", "1.0.0"} {
		appErr := commit(claim(version))
		if appErr.Code == "CLIENT_VERSION_UNSUPPORTED" {
			t.Fatalf("version %q should pass the version gate, got %v", version, appErr)
		}
	}
}

func TestAggregateCollectorVersionSupportedTable(t *testing.T) {
	cases := map[string]bool{
		"0.1.21": false, "0.1.7": false, "v0.1.21": false, "": false,
		"abc": false, "1.0.0-rc": false, "0.1.22": true, "0.1.24": true, "1.0.0": true,
	}
	for version, want := range cases {
		if got := aggregateCollectorVersionSupported(version); got != want {
			t.Errorf("%q: want %v got %v", version, want, got)
		}
	}
}
