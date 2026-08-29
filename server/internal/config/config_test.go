package config

import (
	"os"
	"path/filepath"
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
	cfg := DefaultConfig()
	cfg.Environment = "production"
	cfg.MySQLDSN = "root:pass@tcp(127.0.0.1:3306)/tokendance"
	cfg.EncryptionKey = "prod-32-byte-encryption-key-0001"
	cfg.HMACSecret = "prod-32-byte-hmac-secret-tokendance-01"
	cfg.EmailProvider = "smtp"
	cfg.SMTPHost = "smtp.example.com"
	cfg.SMTPPort = 587
	cfg.SMTPUsername = "smtp-user"
	cfg.SMTPPassword = "smtp-password"
	cfg.SMTPFrom = "noreply@example.com"
	cfg.SMTPTLSMode = "starttls"
	cfg.ObjectProvider = "s3"
	cfg.ObjectEndpoint = "https://objects.example.com"
	cfg.ObjectRegion = "us-east-1"
	cfg.ObjectBucket = "tokendance"
	cfg.ObjectAccessKey = "access-key"
	cfg.ObjectSecretKey = "secret-key"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid production config should pass: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"object endpoint":    func(c *Config) { c.ObjectEndpoint = "" },
		"object bucket":      func(c *Config) { c.ObjectBucket = "" },
		"object credentials": func(c *Config) { c.ObjectSecretKey = "" },
		"SMTP host":          func(c *Config) { c.SMTPHost = "" },
		"SMTP credentials":   func(c *Config) { c.SMTPPassword = "" },
		"SMTP sender":        func(c *Config) { c.SMTPFrom = "" },
	} {
		t.Run("missing "+name, func(t *testing.T) {
			invalid := *cfg
			mutate(&invalid)
			if err := invalid.Validate(); err == nil {
				t.Fatalf("production must fail with missing %s", name)
			}
		})
	}
	cfg.EmailProvider = "sink"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must fail with sink email provider")
	}
	cfg.EmailProvider = "smtp"
	cfg.ObjectProvider = "memory"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must fail with memory object storage")
	}
}

func TestProviderSecretsLoadFromFiles(t *testing.T) {
	dir := t.TempDir()
	accessFile, secretFile, passwordFile := filepath.Join(dir, "access"), filepath.Join(dir, "secret"), filepath.Join(dir, "smtp-password")
	for path, value := range map[string]string{accessFile: "file-access\n", secretFile: "file-secret\n", passwordFile: "file-password\n"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TOKENDANCE_ENVIRONMENT", "test")
	t.Setenv("TOKENDANCE_OBJECT_PROVIDER", "s3")
	t.Setenv("TOKENDANCE_OBJECT_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("TOKENDANCE_OBJECT_REGION", "us-east-1")
	t.Setenv("TOKENDANCE_OBJECT_BUCKET", "bucket")
	t.Setenv("TOKENDANCE_OBJECT_ACCESS_KEY_FILE", accessFile)
	t.Setenv("TOKENDANCE_OBJECT_SECRET_KEY_FILE", secretFile)
	t.Setenv("TOKENDANCE_EMAIL_PROVIDER", "smtp")
	t.Setenv("TOKENDANCE_SMTP_HOST", "localhost")
	t.Setenv("TOKENDANCE_SMTP_PORT", "2525")
	t.Setenv("TOKENDANCE_SMTP_USERNAME", "user")
	t.Setenv("TOKENDANCE_SMTP_PASSWORD_FILE", passwordFile)
	t.Setenv("TOKENDANCE_SMTP_FROM", "noreply@example.com")
	t.Setenv("TOKENDANCE_SMTP_TLS_MODE", "none")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ObjectAccessKey != "file-access" || cfg.ObjectSecretKey != "file-secret" || cfg.SMTPPassword != "file-password" {
		t.Fatal("secret files were not loaded")
	}
}

func TestLoadFromEnv(t *testing.T) {
	_ = os.Setenv("TOKENDANCE_HTTP_ADDR", ":9090")
	_ = os.Setenv("TOKENDANCE_ENVIRONMENT", "test")
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
	if cfg.Environment != "test" {
		t.Errorf("expected test, got %s", cfg.Environment)
	}
}
