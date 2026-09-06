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
	if cfg.SessionIdleTTL != 720*time.Hour {
		t.Errorf("expected 720h, got %v", cfg.SessionIdleTTL)
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
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must fail without durable provider and secret configuration")
	}

	cfg.MySQLDSN = "root:pass@tcp(127.0.0.1:3306)/tokendance"
	cfg.MySQLDSNFile = "mysql.secret"
	for _, ring := range []*VersionedKeyring{
		&cfg.EmailLookupKeys, &cfg.AuthSubjectKeys, &cfg.SessionKeys, &cfg.CSRFKeys,
		&cfg.VerificationCodeKeys, &cfg.BindingCodeKeys, &cfg.GrantKeys, &cfg.IdempotencyKeys, &cfg.AEADKeys,
	} {
		ring.SecretFile = "keyring.secret"
	}
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

	cfg.TestAuthCode = "123456"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must fail when TestAuthCode is set")
	}
	cfg.TestAuthCode = ""
	cfg.EmailProvider = "sink"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must fail with sink email provider")
	}
	cfg.EmailProvider = "smtp"
	cfg.ObjectProvider = "memory"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must fail with memory object storage")
	}
	cfg.ObjectProvider = "s3"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid production config should pass: %v", err)
	}

	cfg.RedisAddr = "127.0.0.1:6379"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must fail when Redis is configured without TOKENDANCE_REDIS_URL_FILE")
	}
	cfg.RedisURL = "redis://:secret@127.0.0.1:6379/0"
	cfg.RedisURLFile = "redis.secret"
	cfg.RedisPassword = "secret"
	cfg.RedisDB = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid production config with Redis URL file should pass: %v", err)
	}
}

func TestRedisURLLoadsLikeMySQLDSN(t *testing.T) {
	dir := t.TempDir()
	urlFile := filepath.Join(dir, "redis_url")
	if err := os.WriteFile(urlFile, []byte("redis://:file-secret@www.nexorai.com.cn:6380/0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOKENDANCE_ENVIRONMENT", "test")
	t.Setenv("TOKENDANCE_REDIS_ADDR", "127.0.0.1:1")
	t.Setenv("TOKENDANCE_REDIS_DB", "7")
	t.Setenv("TOKENDANCE_REDIS_URL", "redis://:ignored@127.0.0.1:6379/0")
	t.Setenv("TOKENDANCE_REDIS_URL_FILE", urlFile)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load Redis URL file: %v", err)
	}
	if cfg.RedisURLFile != urlFile {
		t.Fatalf("expected Redis URL file path, got %q", cfg.RedisURLFile)
	}
	if cfg.RedisAddr != "www.nexorai.com.cn:6380" {
		t.Fatalf("expected redis_dev host from URL file, got %q", cfg.RedisAddr)
	}
	if cfg.RedisPassword != "file-secret" {
		t.Fatal("Redis URL file password was not loaded")
	}
	if cfg.RedisDB != 0 {
		t.Fatalf("expected Redis DB 0 on redis_dev, got %d", cfg.RedisDB)
	}
}

func TestRedisURLEnvParsesDevAndProdInstances(t *testing.T) {
	t.Setenv("TOKENDANCE_ENVIRONMENT", "development")
	t.Setenv("TOKENDANCE_REDIS_URL", "redis://:devpass@www.nexorai.com.cn:6380/0")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load Redis URL: %v", err)
	}
	if !cfg.RedisConfigured() || cfg.RedisAddr != "www.nexorai.com.cn:6380" || cfg.RedisPassword != "devpass" || cfg.RedisDB != 0 {
		t.Fatalf("dev Redis URL not parsed: addr=%q db=%d", cfg.RedisAddr, cfg.RedisDB)
	}

	t.Setenv("TOKENDANCE_REDIS_URL", "redis://:prodpass@127.0.0.1:6379/0")
	cfg, err = LoadFromEnv()
	if err != nil {
		t.Fatalf("load prod Redis URL: %v", err)
	}
	if cfg.RedisAddr != "127.0.0.1:6379" || cfg.RedisPassword != "prodpass" || cfg.RedisDB != 0 {
		t.Fatalf("prod Redis URL not parsed: addr=%q db=%d", cfg.RedisAddr, cfg.RedisDB)
	}
}

func TestInvalidRedisURLRejected(t *testing.T) {
	t.Setenv("TOKENDANCE_ENVIRONMENT", "test")
	t.Setenv("TOKENDANCE_REDIS_URL", "http://127.0.0.1:6379/0")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected invalid Redis URL scheme to fail")
	}
}

func TestVersionedKeyringsLoadFromSecretFiles(t *testing.T) {
	dir := t.TempDir()
	hmacFile := filepath.Join(dir, "hmac.json")
	aeadFile := filepath.Join(dir, "aead.json")
	if err := os.WriteFile(hmacFile, []byte(`{"currentVersion":2,"keys":{"1":"01234567890123456789012345678901","2":"11234567890123456789012345678901"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aeadFile, []byte(`{"currentVersion":3,"keys":{"2":"21234567890123456789012345678901","3":"31234567890123456789012345678901"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOKENDANCE_ENVIRONMENT", "test")
	for _, name := range []string{"TOKENDANCE_EMAIL_LOOKUP_HMAC_KEYRING_FILE", "TOKENDANCE_AUTH_SUBJECT_HMAC_KEYRING_FILE", "TOKENDANCE_SESSION_HMAC_KEYRING_FILE", "TOKENDANCE_CSRF_HMAC_KEYRING_FILE", "TOKENDANCE_VERIFICATION_CODE_HMAC_KEYRING_FILE", "TOKENDANCE_BINDING_CODE_HMAC_KEYRING_FILE", "TOKENDANCE_GRANT_HMAC_KEYRING_FILE", "TOKENDANCE_IDEMPOTENCY_HMAC_KEYRING_FILE"} {
		t.Setenv(name, hmacFile)
	}
	t.Setenv("TOKENDANCE_AEAD_KEYRING_FILE", aeadFile)
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionKeys.CurrentVersion != 2 || len(cfg.SessionKeys.Keys) != 2 || cfg.AEADKeys.CurrentVersion != 3 || len(cfg.AEADKeys.Keys) != 2 {
		t.Fatalf("keyrings not loaded: %+v %+v", cfg.SessionKeys, cfg.AEADKeys)
	}
}

func TestProviderSecretsLoadFromFiles(t *testing.T) {
	dir := t.TempDir()
	accessFile := filepath.Join(dir, "access")
	secretFile := filepath.Join(dir, "secret")
	passwordFile := filepath.Join(dir, "smtp-password")
	if err := os.WriteFile(accessFile, []byte("file-access\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte("file-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TOKENDANCE_ENVIRONMENT", "test")
	t.Setenv("TOKENDANCE_OBJECT_PROVIDER", "s3")
	t.Setenv("TOKENDANCE_OBJECT_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("TOKENDANCE_OBJECT_REGION", "us-east-1")
	t.Setenv("TOKENDANCE_OBJECT_BUCKET", "tokendance-test")
	t.Setenv("TOKENDANCE_OBJECT_ACCESS_KEY", "ignored-access")
	t.Setenv("TOKENDANCE_OBJECT_ACCESS_KEY_FILE", accessFile)
	t.Setenv("TOKENDANCE_OBJECT_SECRET_KEY_FILE", secretFile)
	t.Setenv("TOKENDANCE_EMAIL_PROVIDER", "smtp")
	t.Setenv("TOKENDANCE_SMTP_HOST", "localhost")
	t.Setenv("TOKENDANCE_SMTP_PORT", "2525")
	t.Setenv("TOKENDANCE_SMTP_USERNAME", "smtp-user")
	t.Setenv("TOKENDANCE_SMTP_PASSWORD_FILE", passwordFile)
	t.Setenv("TOKENDANCE_SMTP_FROM", "noreply@example.com")
	t.Setenv("TOKENDANCE_SMTP_TLS_MODE", "none")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load provider secrets: %v", err)
	}
	if cfg.ObjectAccessKey != "file-access" || cfg.ObjectSecretKey != "file-secret" || cfg.SMTPPassword != "file-password" {
		t.Fatal("provider secret files were not loaded or did not override direct values")
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
