package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
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
	HTTPAddr             string        `json:"httpAddr"`
	Environment          string        `json:"environment"`
	MySQLDSN             string        `json:"-"`
	MySQLDSNFile         string        `json:"mysqlDsnFile,omitempty"`
	RedisAddr            string        `json:"redisAddr,omitempty"`
	SessionIdleTTL       time.Duration `json:"sessionIdleTTL"`
	SessionAbsoluteTTL   time.Duration `json:"sessionAbsoluteTTL"`
	AuthCodeTTL          time.Duration `json:"authCodeTTL"`
	AuthBindCodeTTL      time.Duration `json:"authBindCodeTTL"`
	Argon2MemoryKiB      uint32        `json:"argon2MemoryKiB"`
	Argon2Time           uint32        `json:"argon2Time"`
	Argon2Parallelism    uint8         `json:"argon2Parallelism"`
	DeletionCancelWindow time.Duration `json:"deletionCancelWindow"`
	ExportObjectTTL      time.Duration `json:"exportObjectTTL"`
	PublicSkillMinUsers  int           `json:"publicSkillMinUsers"`
	MediaAvatarMaxBytes  int64         `json:"mediaAvatarMaxBytes"`
	MediaAvatarMaxPixels int64         `json:"mediaAvatarMaxPixels"`
	ObjectBucket         string        `json:"objectBucket,omitempty"`
	HMACSecret           string        `json:"-"`
	EncryptionKey        string        `json:"-"`
	EncryptionKeyFile    string        `json:"encryptionKeyFile,omitempty"`
	EncryptionKeyVersion uint16        `json:"encryptionKeyVersion"`
	EmailProvider        string        `json:"emailProvider,omitempty"`
	TestAuthCode         string        `json:"-"`
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
		HMACSecret:           DefaultDevHMACSecret,
		EncryptionKey:        DefaultDevEncryptionKey,
		EncryptionKeyVersion: 1,
		EmailProvider:        "sink",
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

func LoadFromEnv() (*Config, error) {
	cfg := DefaultConfig()

	if v := os.Getenv("TOKENDANCE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("TOKENDANCE_ENVIRONMENT"); v != "" {
		cfg.Environment = v
	}
	if v := os.Getenv("TOKENDANCE_MYSQL_DSN"); v != "" {
		cfg.MySQLDSN = v
	}
	if v := os.Getenv("TOKENDANCE_MYSQL_DSN_FILE"); v != "" {
		cfg.MySQLDSNFile = v
		data, err := os.ReadFile(v)
		if err != nil {
			return nil, fmt.Errorf("failed to read MySQL DSN file at %s: %w", v, err)
		}
		cfg.MySQLDSN = strings.TrimSpace(string(data))
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
	if v := os.Getenv("TOKENDANCE_OBJECT_BUCKET"); v != "" {
		cfg.ObjectBucket = v
	}
	if v := os.Getenv("TOKENDANCE_HMAC_SECRET"); v != "" {
		cfg.HMACSecret = v
	}
	if v := os.Getenv("TOKENDANCE_ENCRYPTION_KEY"); v != "" {
		cfg.EncryptionKey = v
	}
	if v := os.Getenv("TOKENDANCE_ENCRYPTION_KEY_FILE"); v != "" {
		cfg.EncryptionKeyFile = v
		data, err := os.ReadFile(v)
		if err != nil {
			return nil, fmt.Errorf("failed to read encryption key file at %s: %w", v, err)
		}
		cfg.EncryptionKey = strings.TrimSpace(string(data))
	}
	if v := os.Getenv("TOKENDANCE_ENCRYPTION_KEY_VERSION"); v != "" {
		if val, err := strconv.ParseUint(v, 10, 16); err == nil {
			cfg.EncryptionKeyVersion = uint16(val)
		}
	}
	if v := os.Getenv("TOKENDANCE_EMAIL_PROVIDER"); v != "" {
		cfg.EmailProvider = v
	}
	if v := os.Getenv("TOKENDANCE_TEST_AUTH_CODE"); v != "" {
		cfg.TestAuthCode = v
	}

	if cfg.Environment == "production" {
		if cfg.EmailProvider == "sink" {
			cfg.EmailProvider = "worker"
		}
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
		if c.EmailProvider != "worker" && c.EmailProvider != "smtp" {
			return fmt.Errorf("production environment requires a durable email provider (e.g. 'worker' or 'smtp'), got '%s'", c.EmailProvider)
		}
	}

	if c.EncryptionKey != "" {
		if _, err := ParseEncryptionKey(c.EncryptionKey); err != nil {
			return fmt.Errorf("invalid EncryptionKey: %w", err)
		}
	}

	return nil
}
