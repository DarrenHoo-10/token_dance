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
	if cfg.EncryptionKey == "" {
		t.Errorf("expected default dev encryption key")
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

	cfg = DefaultConfig()
	cfg.EncryptionKey = "not-32-bytes"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error when EncryptionKey is invalid")
	}
}

func TestProductionValidationSafety(t *testing.T) {
	// Production with missing keys / defaults must fail
	cfg := DefaultConfig()
	cfg.Environment = "production"

	// 1. Missing MySQL DSN
	if err := cfg.Validate(); err == nil {
		t.Errorf("production must fail without MySQL DSN")
	}

	// Set MySQL DSN
	cfg.MySQLDSN = "root:pass@tcp(127.0.0.1:3306)/tokendance"

	// 2. Default Dev Encryption Key in production must fail
	if err := cfg.Validate(); err == nil {
		t.Errorf("production must fail with default dev encryption key")
	}

	// Set valid 32-byte production encryption key
	cfg.EncryptionKey = "prod-32-byte-encryption-key-0001"

	// 3. Default Dev HMAC Secret in production must fail
	if err := cfg.Validate(); err == nil {
		t.Errorf("production must fail with default dev HMAC secret")
	}

	// Set valid 32-byte production HMAC secret
	cfg.HMACSecret = "prod-32-byte-hmac-secret-tokendance-01"

	// 4. TestAuthCode in production must fail
	cfg.TestAuthCode = "123456"
	if err := cfg.Validate(); err == nil {
		t.Errorf("production must fail when TestAuthCode is set")
	}
	cfg.TestAuthCode = ""

	// 5. Insecure email provider in production must fail
	cfg.EmailProvider = "sink"
	if err := cfg.Validate(); err == nil {
		t.Errorf("production must fail with sink email provider")
	}

	// 6. Valid production config
	cfg.EmailProvider = "worker"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid production config should pass: %v", err)
	}
}

func TestLoadFromEnv(t *testing.T) {
	_ = os.Setenv("TOKENDANCE_HTTP_ADDR", ":9090")
	_ = os.Setenv("TOKENDANCE_ENVIRONMENT", "staging")
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
	if cfg.Environment != "staging" {
		t.Errorf("expected staging, got %s", cfg.Environment)
	}
}
