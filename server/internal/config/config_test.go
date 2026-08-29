package config

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default config should be valid: %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("expected :8080, got %s", cfg.HTTPAddr)
	}
	if cfg.SessionIdleTTL != 336*time.Hour {
		t.Errorf("expected 336h, got %v", cfg.SessionIdleTTL)
	}
	if cfg.SessionAbsoluteTTL != 720*time.Hour {
		t.Errorf("expected 720h, got %v", cfg.SessionAbsoluteTTL)
	}
}

func TestConfigValidationErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HTTPAddr = ""
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for empty HTTPAddr")
	}

	cfg = DefaultConfig()
	cfg.SessionAbsoluteTTL = 1 * time.Hour
	cfg.SessionIdleTTL = 2 * time.Hour
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error when absolute TTL < idle TTL")
	}

	cfg = DefaultConfig()
	cfg.Argon2MemoryKiB = 512
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error when Argon2MemoryKiB < 1024")
	}

	cfg = DefaultConfig()
	cfg.HMACSecret = "short"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error when HMACSecret < 16 bytes")
	}
}

func TestLoadFromEnv(t *testing.T) {
	_ = os.Setenv("TOKENDANCE_HTTP_ADDR", ":9090")
	_ = os.Setenv("TOKENDANCE_ENVIRONMENT", "production")
	defer func() {
		_ = os.Unsetenv("TOKENDANCE_HTTP_ADDR")
		_ = os.Unsetenv("TOKENDANCE_ENVIRONMENT")
	}()

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error loading env: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("expected :9090, got %s", cfg.HTTPAddr)
	}
	if cfg.Environment != "production" {
		t.Errorf("expected production, got %s", cfg.Environment)
	}
}
