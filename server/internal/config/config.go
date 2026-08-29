package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultDevHMACSecret    = "tokendance-dev-hmac-secret-at-least-32-bytes-long"
	DefaultDevEncryptionKey = "tokendance-dev-32-byte-aead-key!"
)

type Config struct {
	HTTPAddr               string        `json:"httpAddr"`
	Environment            string        `json:"environment"`
	MySQLDSN               string        `json:"-"`
	MySQLDSNFile           string        `json:"mysqlDsnFile,omitempty"`
	RedisAddr              string        `json:"redisAddr,omitempty"`
	SessionIdleTTL         time.Duration `json:"sessionIdleTTL"`
	SessionAbsoluteTTL     time.Duration `json:"sessionAbsoluteTTL"`
	AuthCodeTTL            time.Duration `json:"authCodeTTL"`
	AuthBindCodeTTL        time.Duration `json:"authBindCodeTTL"`
	Argon2MemoryKiB        uint32        `json:"argon2MemoryKiB"`
	Argon2Time             uint32        `json:"argon2Time"`
	Argon2Parallelism      uint8         `json:"argon2Parallelism"`
	DeletionCancelWindow   time.Duration `json:"deletionCancelWindow"`
	ExportObjectTTL        time.Duration `json:"exportObjectTTL"`
	PublicSkillMinUsers    int           `json:"publicSkillMinUsers"`
	MediaAvatarMaxBytes    int64         `json:"mediaAvatarMaxBytes"`
	MediaAvatarMaxPixels   int64         `json:"mediaAvatarMaxPixels"`
	ObjectProvider         string        `json:"objectProvider,omitempty"`
	ObjectEndpoint         string        `json:"objectEndpoint,omitempty"`
	ObjectRegion           string        `json:"objectRegion,omitempty"`
	ObjectBucket           string        `json:"objectBucket,omitempty"`
	ObjectAccessKey        string        `json:"-"`
	ObjectAccessKeyFile    string        `json:"objectAccessKeyFile,omitempty"`
	ObjectSecretKey        string        `json:"-"`
	ObjectSecretKeyFile    string        `json:"objectSecretKeyFile,omitempty"`
	ObjectSessionToken     string        `json:"-"`
	ObjectSessionTokenFile string        `json:"objectSessionTokenFile,omitempty"`
	ObjectUsePathStyle     bool          `json:"objectUsePathStyle"`
	HMACSecret             string        `json:"-"`
	HMACSecretFile         string        `json:"hmacSecretFile,omitempty"`
	EncryptionKey          string        `json:"-"`
	EncryptionKeyFile      string        `json:"encryptionKeyFile,omitempty"`
	EncryptionKeyVersion   uint16        `json:"encryptionKeyVersion"`
	EmailProvider          string        `json:"emailProvider,omitempty"`
	SMTPHost               string        `json:"smtpHost,omitempty"`
	SMTPPort               int           `json:"smtpPort,omitempty"`
	SMTPUsername           string        `json:"smtpUsername,omitempty"`
	SMTPPassword           string        `json:"-"`
	SMTPPasswordFile       string        `json:"smtpPasswordFile,omitempty"`
	SMTPFrom               string        `json:"smtpFrom,omitempty"`
	SMTPTLSMode            string        `json:"smtpTlsMode,omitempty"`
	SMTPEHLOName           string        `json:"smtpEhloName,omitempty"`
	TestAuthCode           string        `json:"-"`
}

func DefaultConfig() *Config {
	return &Config{
		HTTPAddr:             ":8080",
		Environment:          "development",
		SessionIdleTTL:       14 * 24 * time.Hour, // 336h
		SessionAbsoluteTTL:   30 * 24 * time.Hour, // 720h
		AuthCodeTTL:          10 * time.Minute,    // 10m
		AuthBindCodeTTL:      5 * time.Minute,     // 5m
		Argon2MemoryKiB:      65536,               // 64 MiB
		Argon2Time:           3,
		Argon2Parallelism:    2,
		DeletionCancelWindow: 7 * 24 * time.Hour, // 168h
		ExportObjectTTL:      24 * time.Hour,     // 24h
		PublicSkillMinUsers:  5,
		MediaAvatarMaxBytes:  5 * 1024 * 1024, // 5 MiB
		MediaAvatarMaxPixels: 16000000,        // 16M pixels
		ObjectProvider:       "memory",
		ObjectRegion:         "us-east-1",
		ObjectUsePathStyle:   true,
		HMACSecret:           DefaultDevHMACSecret,
		EncryptionKey:        DefaultDevEncryptionKey,
		EncryptionKeyVersion: 1,
		EmailProvider:        "sink",
		SMTPPort:             587,
		SMTPTLSMode:          "starttls",
	}
}

// ParseEncryptionKey decodes a 32-byte encryption key from hex, base64, or raw 32-byte string
func ParseEncryptionKey(keyStr string) ([32]byte, error) {
	trimmed := strings.TrimSpace(keyStr)
	var key [32]byte

	if len(trimmed) == 64 {
		if b, err := hex.DecodeString(trimmed); err == nil && len(b) == 32 {
			copy(key[:], b)
			return key, nil
		}
	}
	if len(trimmed) == 32 {
		copy(key[:], []byte(trimmed))
		return key, nil
	}
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(b) == 32 {
		copy(key[:], b)
		return key, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil && len(b) == 32 {
		copy(key[:], b)
		return key, nil
	}

	return key, fmt.Errorf("encryption key must be exactly 32 bytes (or 64 hex characters)")
}

func readSecretEnv(valueName, fileName string) (string, string, error) {
	value := os.Getenv(valueName)
	path := os.Getenv(fileName)
	if path == "" {
		return value, "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", path, fmt.Errorf("failed to read %s file at %s: %w", valueName, path, err)
	}
	return strings.TrimSpace(string(data)), path, nil
}

func LoadFromEnv() (*Config, error) {
	cfg := DefaultConfig()

	if v := os.Getenv("TOKENDANCE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("TOKENDANCE_ENVIRONMENT"); v != "" {
		cfg.Environment = v
	}
	var err error
	if cfg.MySQLDSN, cfg.MySQLDSNFile, err = readSecretEnv("TOKENDANCE_MYSQL_DSN", "TOKENDANCE_MYSQL_DSN_FILE"); err != nil {
		return nil, err
	}
	if v := os.Getenv("TOKENDANCE_REDIS_ADDR"); v != "" {
		cfg.RedisAddr = v
	}
	if v := os.Getenv("TOKENDANCE_AUTH_SESSION_IDLE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionIdleTTL = d
		}
	}
	if v := os.Getenv("TOKENDANCE_AUTH_SESSION_ABSOLUTE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionAbsoluteTTL = d
		}
	}
	if v := os.Getenv("TOKENDANCE_AUTH_CODE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.AuthCodeTTL = d
		}
	}
	if v := os.Getenv("TOKENDANCE_AUTH_BIND_CODE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.AuthBindCodeTTL = d
		}
	}
	if v := os.Getenv("TOKENDANCE_AUTH_ARGON2_MEMORY_KIB"); v != "" {
		if val, err := strconv.ParseUint(v, 10, 32); err == nil {
			cfg.Argon2MemoryKiB = uint32(val)
		}
	}
	if v := os.Getenv("TOKENDANCE_AUTH_ARGON2_TIME"); v != "" {
		if val, err := strconv.ParseUint(v, 10, 32); err == nil {
			cfg.Argon2Time = uint32(val)
		}
	}
	if v := os.Getenv("TOKENDANCE_AUTH_ARGON2_PARALLELISM"); v != "" {
		if val, err := strconv.ParseUint(v, 10, 8); err == nil {
			cfg.Argon2Parallelism = uint8(val)
		}
	}
	if v := os.Getenv("TOKENDANCE_DELETION_CANCEL_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.DeletionCancelWindow = d
		}
	}
	if v := os.Getenv("TOKENDANCE_EXPORT_OBJECT_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ExportObjectTTL = d
		}
	}
	if v := os.Getenv("TOKENDANCE_PUBLIC_SKILL_MIN_USERS"); v != "" {
		if val, err := strconv.Atoi(v); err == nil {
			cfg.PublicSkillMinUsers = val
		}
	}
	if v := os.Getenv("TOKENDANCE_MEDIA_AVATAR_MAX_BYTES"); v != "" {
		if val, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MediaAvatarMaxBytes = val
		}
	}
	if v := os.Getenv("TOKENDANCE_MEDIA_AVATAR_MAX_PIXELS"); v != "" {
		if val, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MediaAvatarMaxPixels = val
		}
	}
	if v := os.Getenv("TOKENDANCE_OBJECT_PROVIDER"); v != "" {
		cfg.ObjectProvider = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_OBJECT_ENDPOINT"); v != "" {
		cfg.ObjectEndpoint = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_OBJECT_REGION"); v != "" {
		cfg.ObjectRegion = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_OBJECT_BUCKET"); v != "" {
		cfg.ObjectBucket = strings.TrimSpace(v)
	}
	if cfg.ObjectAccessKey, cfg.ObjectAccessKeyFile, err = readSecretEnv("TOKENDANCE_OBJECT_ACCESS_KEY", "TOKENDANCE_OBJECT_ACCESS_KEY_FILE"); err != nil {
		return nil, err
	}
	if cfg.ObjectSecretKey, cfg.ObjectSecretKeyFile, err = readSecretEnv("TOKENDANCE_OBJECT_SECRET_KEY", "TOKENDANCE_OBJECT_SECRET_KEY_FILE"); err != nil {
		return nil, err
	}
	if cfg.ObjectSessionToken, cfg.ObjectSessionTokenFile, err = readSecretEnv("TOKENDANCE_OBJECT_SESSION_TOKEN", "TOKENDANCE_OBJECT_SESSION_TOKEN_FILE"); err != nil {
		return nil, err
	}
	if cfg.HMACSecret, cfg.HMACSecretFile, err = readSecretEnv("TOKENDANCE_HMAC_SECRET", "TOKENDANCE_HMAC_SECRET_FILE"); err != nil {
		return nil, err
	}
	if cfg.HMACSecret == "" {
		cfg.HMACSecret = DefaultDevHMACSecret
	}
	if cfg.EncryptionKey, cfg.EncryptionKeyFile, err = readSecretEnv("TOKENDANCE_ENCRYPTION_KEY", "TOKENDANCE_ENCRYPTION_KEY_FILE"); err != nil {
		return nil, err
	}
	if cfg.EncryptionKey == "" {
		cfg.EncryptionKey = DefaultDevEncryptionKey
	}
	if v := os.Getenv("TOKENDANCE_ENCRYPTION_KEY_VERSION"); v != "" {
		if val, err := strconv.ParseUint(v, 10, 16); err == nil {
			cfg.EncryptionKeyVersion = uint16(val)
		}
	}
	if v := os.Getenv("TOKENDANCE_EMAIL_PROVIDER"); v != "" {
		cfg.EmailProvider = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_SMTP_HOST"); v != "" {
		cfg.SMTPHost = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_SMTP_USERNAME"); v != "" {
		cfg.SMTPUsername = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_SMTP_FROM"); v != "" {
		cfg.SMTPFrom = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_SMTP_TLS_MODE"); v != "" {
		cfg.SMTPTLSMode = strings.TrimSpace(v)
	}
	if v := os.Getenv("TOKENDANCE_SMTP_EHLO_NAME"); v != "" {
		cfg.SMTPEHLOName = strings.TrimSpace(v)
	}
	if cfg.SMTPPassword, cfg.SMTPPasswordFile, err = readSecretEnv("TOKENDANCE_SMTP_PASSWORD", "TOKENDANCE_SMTP_PASSWORD_FILE"); err != nil {
		return nil, err
	}
	if v := os.Getenv("TOKENDANCE_SMTP_PORT"); v != "" {
		val, parseErr := strconv.Atoi(v)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid TOKENDANCE_SMTP_PORT: %w", parseErr)
		}
		cfg.SMTPPort = val
	}
	if v := os.Getenv("TOKENDANCE_OBJECT_USE_PATH_STYLE"); v != "" {
		val, parseErr := strconv.ParseBool(v)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid TOKENDANCE_OBJECT_USE_PATH_STYLE: %w", parseErr)
		}
		cfg.ObjectUsePathStyle = val
	}
	if v := os.Getenv("TOKENDANCE_TEST_AUTH_CODE"); v != "" {
		cfg.TestAuthCode = v
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.HTTPAddr == "" {
		return fmt.Errorf("HTTPAddr cannot be empty")
	}

	// Validate HTTPAddr host:port or :port
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		if !strings.HasPrefix(c.HTTPAddr, ":") {
			return fmt.Errorf("invalid HTTPAddr format: %s", c.HTTPAddr)
		}
	}

	if c.SessionIdleTTL <= 0 {
		return fmt.Errorf("SessionIdleTTL must be positive")
	}
	if c.SessionAbsoluteTTL < c.SessionIdleTTL {
		return fmt.Errorf("SessionAbsoluteTTL must be greater than or equal to SessionIdleTTL")
	}
	if c.AuthCodeTTL <= 0 {
		return fmt.Errorf("AuthCodeTTL must be positive")
	}
	if c.AuthBindCodeTTL <= 0 {
		return fmt.Errorf("AuthBindCodeTTL must be positive")
	}
	if c.Argon2MemoryKiB < 1024 {
		return fmt.Errorf("Argon2MemoryKiB must be at least 1024")
	}
	if c.Argon2Time < 1 {
		return fmt.Errorf("Argon2Time must be at least 1")
	}
	if c.Argon2Parallelism < 1 {
		return fmt.Errorf("Argon2Parallelism must be at least 1")
	}
	if c.DeletionCancelWindow <= 0 {
		return fmt.Errorf("DeletionCancelWindow must be positive")
	}
	if c.ExportObjectTTL <= 0 {
		return fmt.Errorf("ExportObjectTTL must be positive")
	}
	if c.MediaAvatarMaxBytes <= 0 {
		return fmt.Errorf("MediaAvatarMaxBytes must be positive")
	}
	if c.MediaAvatarMaxPixels <= 0 {
		return fmt.Errorf("MediaAvatarMaxPixels must be positive")
	}
	if len(c.HMACSecret) < 16 {
		return fmt.Errorf("HMACSecret must be at least 16 bytes")
	}

	local := c.Environment == "development" || c.Environment == "test"
	if c.ObjectProvider == "memory" && !local {
		return fmt.Errorf("memory object storage is only allowed in explicit development or test environments")
	}
	if c.EmailProvider == "sink" && !local {
		return fmt.Errorf("email sink is only allowed in explicit development or test environments")
	}
	if c.ObjectProvider != "memory" && c.ObjectProvider != "s3" {
		return fmt.Errorf("unsupported object provider %q", c.ObjectProvider)
	}
	if c.EmailProvider != "sink" && c.EmailProvider != "smtp" {
		return fmt.Errorf("unsupported email provider %q", c.EmailProvider)
	}
	if c.ObjectProvider == "s3" {
		if c.ObjectEndpoint == "" {
			return fmt.Errorf("TOKENDANCE_OBJECT_ENDPOINT is required for S3 object storage")
		}
		u, err := url.Parse(c.ObjectEndpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return fmt.Errorf("TOKENDANCE_OBJECT_ENDPOINT must be an http(s) URL without embedded credentials")
		}
		if c.ObjectRegion == "" || c.ObjectBucket == "" {
			return fmt.Errorf("S3 region and bucket are required")
		}
		if c.ObjectAccessKey == "" || c.ObjectSecretKey == "" {
			return fmt.Errorf("S3 object storage credentials are required")
		}
	}
	if c.EmailProvider == "smtp" {
		if c.SMTPHost == "" || c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return fmt.Errorf("valid SMTP host and port are required")
		}
		if c.SMTPUsername == "" || c.SMTPPassword == "" {
			return fmt.Errorf("SMTP credentials are required")
		}
		if _, err := mail.ParseAddress(c.SMTPFrom); err != nil {
			return fmt.Errorf("TOKENDANCE_SMTP_FROM must be a valid email address")
		}
		if c.SMTPTLSMode != "starttls" && c.SMTPTLSMode != "tls" && c.SMTPTLSMode != "none" {
			return fmt.Errorf("invalid SMTP TLS mode")
		}
		if !local && c.SMTPTLSMode == "none" {
			return fmt.Errorf("SMTP TLS cannot be disabled outside development or test")
		}
	}

	// Environment specific rules
	if c.Environment == "production" {
		if c.MySQLDSN == "" {
			return fmt.Errorf("TOKENDANCE_MYSQL_DSN or TOKENDANCE_MYSQL_DSN_FILE is required in production environment")
		}
		if c.EncryptionKey == "" || c.EncryptionKey == DefaultDevEncryptionKey {
			return fmt.Errorf("production requires explicit non-default TOKENDANCE_ENCRYPTION_KEY or TOKENDANCE_ENCRYPTION_KEY_FILE")
		}
		if c.HMACSecret == "" || c.HMACSecret == DefaultDevHMACSecret || len(c.HMACSecret) < 32 {
			return fmt.Errorf("production requires explicit non-default TOKENDANCE_HMAC_SECRET with at least 32 bytes")
		}
		if c.TestAuthCode != "" {
			return fmt.Errorf("TOKENDANCE_TEST_AUTH_CODE is strictly prohibited in production environment")
		}
		if c.EmailProvider != "smtp" {
			return fmt.Errorf("production environment requires SMTP email delivery")
		}
		if c.ObjectProvider != "s3" {
			return fmt.Errorf("production environment requires S3-compatible object storage")
		}
	}

	if c.EncryptionKey != "" {
		if _, err := ParseEncryptionKey(c.EncryptionKey); err != nil {
			return fmt.Errorf("invalid EncryptionKey: %w", err)
		}
	}

	return nil
}
