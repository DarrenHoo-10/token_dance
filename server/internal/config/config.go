package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultDevHMACSecret    = "tokendance-dev-hmac-secret-at-least-32-bytes-long"
	DefaultDevEncryptionKey = "tokendance-dev-32-byte-aead-key!"
)

type VersionedKeyring struct {
	CurrentVersion uint16            `json:"currentVersion"`
	Keys           map[uint16][]byte `json:"-"`
	SecretFile     string            `json:"secretFile,omitempty"`
}

func (k VersionedKeyring) Current() []byte {
	return k.Keys[k.CurrentVersion]
}

func (k VersionedKeyring) Versions() []uint16 {
	versions := make([]uint16, 0, len(k.Keys))
	for version := range k.Keys {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i] == k.CurrentVersion {
			return true
		}
		if versions[j] == k.CurrentVersion {
			return false
		}
		return versions[i] > versions[j]
	})
	return versions
}

type keyringFile struct {
	CurrentVersion uint16            `json:"currentVersion"`
	Keys           map[string]string `json:"keys"`
}

type Config struct {
	HTTPAddr             string        `json:"httpAddr"`
	Environment          string        `json:"environment"`
	MySQLDSN             string        `json:"-"`
	MySQLDSNFile         string        `json:"mysqlDsnFile,omitempty"`
	RedisAddr            string        `json:"redisAddr,omitempty"`
	TrustedProxyCIDRs    []string      `json:"trustedProxyCidrs,omitempty"`
	RateLimitMaxEntries  int           `json:"rateLimitMaxEntries"`
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

	ObjectProvider         string `json:"objectProvider,omitempty"`
	ObjectEndpoint         string `json:"objectEndpoint,omitempty"`
	ObjectRegion           string `json:"objectRegion,omitempty"`
	ObjectBucket           string `json:"objectBucket,omitempty"`
	ObjectAccessKey        string `json:"-"`
	ObjectAccessKeyFile    string `json:"objectAccessKeyFile,omitempty"`
	ObjectSecretKey        string `json:"-"`
	ObjectSecretKeyFile    string `json:"objectSecretKeyFile,omitempty"`
	ObjectSessionToken     string `json:"-"`
	ObjectSessionTokenFile string `json:"objectSessionTokenFile,omitempty"`
	ObjectUsePathStyle     bool   `json:"objectUsePathStyle"`

	HMACSecret           string           `json:"-"`
	HMACSecretFile       string           `json:"hmacSecretFile,omitempty"`
	EmailLookupKeys      VersionedKeyring `json:"emailLookupKeys"`
	AuthSubjectKeys      VersionedKeyring `json:"authSubjectKeys"`
	SessionKeys          VersionedKeyring `json:"sessionKeys"`
	CSRFKeys             VersionedKeyring `json:"csrfKeys"`
	VerificationCodeKeys VersionedKeyring `json:"verificationCodeKeys"`
	BindingCodeKeys      VersionedKeyring `json:"bindingCodeKeys"`
	GrantKeys            VersionedKeyring `json:"grantKeys"`
	IdempotencyKeys      VersionedKeyring `json:"idempotencyKeys"`

	EncryptionKey        string           `json:"-"`
	EncryptionKeyFile    string           `json:"encryptionKeyFile,omitempty"`
	EncryptionKeyVersion uint16           `json:"encryptionKeyVersion"`
	AEADKeys             VersionedKeyring `json:"aeadKeys"`

	EmailProvider    string `json:"emailProvider,omitempty"`
	SMTPHost         string `json:"smtpHost,omitempty"`
	SMTPPort         int    `json:"smtpPort,omitempty"`
	SMTPUsername     string `json:"smtpUsername,omitempty"`
	SMTPPassword     string `json:"-"`
	SMTPPasswordFile string `json:"smtpPasswordFile,omitempty"`
	SMTPFrom         string `json:"smtpFrom,omitempty"`
	SMTPTLSMode      string `json:"smtpTlsMode,omitempty"`
	SMTPEHLOName     string `json:"smtpEhloName,omitempty"`
	TestAuthCode     string `json:"-"`
}

func deriveDevKey(purpose string) []byte {
	h := hmac.New(sha256.New, []byte(DefaultDevHMACSecret))
	_, _ = h.Write([]byte(purpose))
	return h.Sum(nil)
}

func devKeyring(purpose string) VersionedKeyring {
	return VersionedKeyring{CurrentVersion: 1, Keys: map[uint16][]byte{1: deriveDevKey(purpose)}}
}

func DefaultConfig() *Config {
	return &Config{
		HTTPAddr:             ":8080",
		Environment:          "development",
		RateLimitMaxEntries:  10000,
		SessionIdleTTL:       14 * 24 * time.Hour,
		SessionAbsoluteTTL:   30 * 24 * time.Hour,
		AuthCodeTTL:          10 * time.Minute,
		AuthBindCodeTTL:      5 * time.Minute,
		Argon2MemoryKiB:      65536,
		Argon2Time:           3,
		Argon2Parallelism:    2,
		DeletionCancelWindow: 7 * 24 * time.Hour,
		ExportObjectTTL:      24 * time.Hour,
		PublicSkillMinUsers:  5,
		MediaAvatarMaxBytes:  5 * 1024 * 1024,
		MediaAvatarMaxPixels: 16000000,
		ObjectProvider:       "memory",
		ObjectRegion:         "us-east-1",
		ObjectUsePathStyle:   true,
		HMACSecret:           DefaultDevHMACSecret,
		EmailLookupKeys:      devKeyring("email_lookup"),
		AuthSubjectKeys:      devKeyring("auth_subject"),
		SessionKeys:          devKeyring("session"),
		CSRFKeys:             devKeyring("csrf"),
		VerificationCodeKeys: devKeyring("verification_code"),
		BindingCodeKeys:      devKeyring("binding_code"),
		GrantKeys:            devKeyring("grant"),
		IdempotencyKeys:      devKeyring("idempotency"),
		EncryptionKey:        DefaultDevEncryptionKey,
		EncryptionKeyVersion: 1,
		AEADKeys: VersionedKeyring{CurrentVersion: 1, Keys: map[uint16][]byte{
			1: []byte(DefaultDevEncryptionKey),
		}},
		EmailProvider: "sink",
		SMTPPort:      587,
		SMTPTLSMode:   "starttls",
	}
}

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

func parseHMACKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 64 {
		if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) >= 32 {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(decoded) >= 32 {
		return decoded, nil
	}
	if len(raw) >= 32 {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("HMAC key must be at least 32 bytes")
}

func loadKeyring(path string, aead bool) (VersionedKeyring, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VersionedKeyring{}, fmt.Errorf("failed to read keyring file at %s: %w", path, err)
	}
	var file keyringFile
	if err := json.Unmarshal(data, &file); err != nil {
		return VersionedKeyring{}, fmt.Errorf("invalid keyring JSON at %s: %w", path, err)
	}
	keys := make(map[uint16][]byte, len(file.Keys))
	for rawVersion, rawKey := range file.Keys {
		version64, err := strconv.ParseUint(rawVersion, 10, 16)
		if err != nil || version64 == 0 {
			return VersionedKeyring{}, fmt.Errorf("invalid key version %q in %s", rawVersion, path)
		}
		var key []byte
		if aead {
			parsed, err := ParseEncryptionKey(rawKey)
			if err != nil {
				return VersionedKeyring{}, fmt.Errorf("invalid AEAD key version %s in %s: %w", rawVersion, path, err)
			}
			key = append([]byte(nil), parsed[:]...)
		} else {
			key, err = parseHMACKey(rawKey)
			if err != nil {
				return VersionedKeyring{}, fmt.Errorf("invalid HMAC key version %s in %s: %w", rawVersion, path, err)
			}
		}
		keys[uint16(version64)] = key
	}
	if file.CurrentVersion == 0 || len(keys[file.CurrentVersion]) == 0 {
		return VersionedKeyring{}, fmt.Errorf("currentVersion must identify a configured key in %s", path)
	}
	return VersionedKeyring{CurrentVersion: file.CurrentVersion, Keys: keys, SecretFile: path}, nil
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
	setString := func(name string, dst *string) {
		if v := os.Getenv(name); v != "" {
			*dst = strings.TrimSpace(v)
		}
	}
	setString("TOKENDANCE_HTTP_ADDR", &cfg.HTTPAddr)
	setString("TOKENDANCE_ENVIRONMENT", &cfg.Environment)
	setString("TOKENDANCE_REDIS_ADDR", &cfg.RedisAddr)
	setString("TOKENDANCE_OBJECT_PROVIDER", &cfg.ObjectProvider)
	setString("TOKENDANCE_OBJECT_ENDPOINT", &cfg.ObjectEndpoint)
	setString("TOKENDANCE_OBJECT_REGION", &cfg.ObjectRegion)
	setString("TOKENDANCE_OBJECT_BUCKET", &cfg.ObjectBucket)
	setString("TOKENDANCE_EMAIL_PROVIDER", &cfg.EmailProvider)
	setString("TOKENDANCE_SMTP_HOST", &cfg.SMTPHost)
	setString("TOKENDANCE_SMTP_USERNAME", &cfg.SMTPUsername)
	setString("TOKENDANCE_SMTP_FROM", &cfg.SMTPFrom)
	setString("TOKENDANCE_SMTP_TLS_MODE", &cfg.SMTPTLSMode)
	setString("TOKENDANCE_SMTP_EHLO_NAME", &cfg.SMTPEHLOName)
	setString("TOKENDANCE_TEST_AUTH_CODE", &cfg.TestAuthCode)
	if v := os.Getenv("TOKENDANCE_TRUSTED_PROXY_CIDRS"); v != "" {
		for _, cidr := range strings.Split(v, ",") {
			if cidr = strings.TrimSpace(cidr); cidr != "" {
				cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, cidr)
			}
		}
	}

	var err error
	if cfg.MySQLDSN, cfg.MySQLDSNFile, err = readSecretEnv("TOKENDANCE_MYSQL_DSN", "TOKENDANCE_MYSQL_DSN_FILE"); err != nil {
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
	if cfg.ObjectAccessKey, cfg.ObjectAccessKeyFile, err = readSecretEnv("TOKENDANCE_OBJECT_ACCESS_KEY", "TOKENDANCE_OBJECT_ACCESS_KEY_FILE"); err != nil {
		return nil, err
	}
	if cfg.ObjectSecretKey, cfg.ObjectSecretKeyFile, err = readSecretEnv("TOKENDANCE_OBJECT_SECRET_KEY", "TOKENDANCE_OBJECT_SECRET_KEY_FILE"); err != nil {
		return nil, err
	}
	if cfg.ObjectSessionToken, cfg.ObjectSessionTokenFile, err = readSecretEnv("TOKENDANCE_OBJECT_SESSION_TOKEN", "TOKENDANCE_OBJECT_SESSION_TOKEN_FILE"); err != nil {
		return nil, err
	}
	if cfg.SMTPPassword, cfg.SMTPPasswordFile, err = readSecretEnv("TOKENDANCE_SMTP_PASSWORD", "TOKENDANCE_SMTP_PASSWORD_FILE"); err != nil {
		return nil, err
	}

	keyringEnvs := []struct {
		name string
		dst  *VersionedKeyring
		aead bool
	}{
		{"TOKENDANCE_EMAIL_LOOKUP_HMAC_KEYRING_FILE", &cfg.EmailLookupKeys, false},
		{"TOKENDANCE_AUTH_SUBJECT_HMAC_KEYRING_FILE", &cfg.AuthSubjectKeys, false},
		{"TOKENDANCE_SESSION_HMAC_KEYRING_FILE", &cfg.SessionKeys, false},
		{"TOKENDANCE_CSRF_HMAC_KEYRING_FILE", &cfg.CSRFKeys, false},
		{"TOKENDANCE_VERIFICATION_CODE_HMAC_KEYRING_FILE", &cfg.VerificationCodeKeys, false},
		{"TOKENDANCE_BINDING_CODE_HMAC_KEYRING_FILE", &cfg.BindingCodeKeys, false},
		{"TOKENDANCE_GRANT_HMAC_KEYRING_FILE", &cfg.GrantKeys, false},
		{"TOKENDANCE_IDEMPOTENCY_HMAC_KEYRING_FILE", &cfg.IdempotencyKeys, false},
		{"TOKENDANCE_AEAD_KEYRING_FILE", &cfg.AEADKeys, true},
	}
	for _, item := range keyringEnvs {
		if path := strings.TrimSpace(os.Getenv(item.name)); path != "" {
			loaded, err := loadKeyring(path, item.aead)
			if err != nil {
				return nil, err
			}
			*item.dst = loaded
		}
	}
	// Legacy single-key settings remain development-only compatibility inputs.
	if cfg.Environment != "production" && cfg.HMACSecret != DefaultDevHMACSecret {
		for purpose, dst := range map[string]*VersionedKeyring{
			"email_lookup": &cfg.EmailLookupKeys, "auth_subject": &cfg.AuthSubjectKeys,
			"session": &cfg.SessionKeys, "csrf": &cfg.CSRFKeys,
			"verification_code": &cfg.VerificationCodeKeys, "binding_code": &cfg.BindingCodeKeys,
			"grant": &cfg.GrantKeys, "idempotency": &cfg.IdempotencyKeys,
		} {
			h := hmac.New(sha256.New, []byte(cfg.HMACSecret))
			_, _ = h.Write([]byte(purpose))
			*dst = VersionedKeyring{CurrentVersion: 1, Keys: map[uint16][]byte{1: h.Sum(nil)}}
		}
	}
	if cfg.Environment != "production" && cfg.EncryptionKey != DefaultDevEncryptionKey {
		key, err := ParseEncryptionKey(cfg.EncryptionKey)
		if err != nil {
			return nil, err
		}
		cfg.AEADKeys = VersionedKeyring{CurrentVersion: cfg.EncryptionKeyVersion, Keys: map[uint16][]byte{cfg.EncryptionKeyVersion: append([]byte(nil), key[:]...)}}
	}

	parseDuration := func(name string, dst *time.Duration) error {
		if v := os.Getenv(name); v != "" {
			d, e := time.ParseDuration(v)
			if e != nil {
				return fmt.Errorf("invalid %s: %w", name, e)
			}
			*dst = d
		}
		return nil
	}
	for _, item := range []struct {
		name string
		dst  *time.Duration
	}{
		{"TOKENDANCE_AUTH_SESSION_IDLE_TTL", &cfg.SessionIdleTTL},
		{"TOKENDANCE_AUTH_SESSION_ABSOLUTE_TTL", &cfg.SessionAbsoluteTTL},
		{"TOKENDANCE_AUTH_CODE_TTL", &cfg.AuthCodeTTL},
		{"TOKENDANCE_AUTH_BIND_CODE_TTL", &cfg.AuthBindCodeTTL},
		{"TOKENDANCE_DELETION_CANCEL_WINDOW", &cfg.DeletionCancelWindow},
		{"TOKENDANCE_EXPORT_OBJECT_TTL", &cfg.ExportObjectTTL},
	} {
		if err := parseDuration(item.name, item.dst); err != nil {
			return nil, err
		}
	}

	parseUint := func(name string, bits int, set func(uint64)) error {
		if v := os.Getenv(name); v != "" {
			n, e := strconv.ParseUint(v, 10, bits)
			if e != nil {
				return fmt.Errorf("invalid %s: %w", name, e)
			}
			set(n)
		}
		return nil
	}
	if err := parseUint("TOKENDANCE_AUTH_ARGON2_MEMORY_KIB", 32, func(v uint64) { cfg.Argon2MemoryKiB = uint32(v) }); err != nil {
		return nil, err
	}
	if err := parseUint("TOKENDANCE_AUTH_ARGON2_TIME", 32, func(v uint64) { cfg.Argon2Time = uint32(v) }); err != nil {
		return nil, err
	}
	if err := parseUint("TOKENDANCE_AUTH_ARGON2_PARALLELISM", 8, func(v uint64) { cfg.Argon2Parallelism = uint8(v) }); err != nil {
		return nil, err
	}

	parseInt := func(name string, bits int, set func(int64)) error {
		if v := os.Getenv(name); v != "" {
			n, e := strconv.ParseInt(v, 10, bits)
			if e != nil {
				return fmt.Errorf("invalid %s: %w", name, e)
			}
			set(n)
		}
		return nil
	}
	if err := parseInt("TOKENDANCE_PUBLIC_SKILL_MIN_USERS", 32, func(v int64) { cfg.PublicSkillMinUsers = int(v) }); err != nil {
		return nil, err
	}
	if err := parseInt("TOKENDANCE_MEDIA_AVATAR_MAX_BYTES", 64, func(v int64) { cfg.MediaAvatarMaxBytes = v }); err != nil {
		return nil, err
	}
	if err := parseInt("TOKENDANCE_MEDIA_AVATAR_MAX_PIXELS", 64, func(v int64) { cfg.MediaAvatarMaxPixels = v }); err != nil {
		return nil, err
	}
	if err := parseInt("TOKENDANCE_SMTP_PORT", 32, func(v int64) { cfg.SMTPPort = int(v) }); err != nil {
		return nil, err
	}
	if err := parseInt("TOKENDANCE_RATE_LIMIT_MAX_ENTRIES", 32, func(v int64) { cfg.RateLimitMaxEntries = int(v) }); err != nil {
		return nil, err
	}
	if v := os.Getenv("TOKENDANCE_OBJECT_USE_PATH_STYLE"); v != "" {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return nil, fmt.Errorf("invalid TOKENDANCE_OBJECT_USE_PATH_STYLE: %w", e)
		}
		cfg.ObjectUsePathStyle = b
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	return cfg, nil
}

func isExplicitLocalEnvironment(environment string) bool {
	return environment == "development" || environment == "test"
}

func validateKeyring(name string, ring VersionedKeyring, exact32 bool) error {
	if ring.CurrentVersion == 0 || len(ring.Keys[ring.CurrentVersion]) == 0 {
		return fmt.Errorf("%s current version is missing", name)
	}
	for version, key := range ring.Keys {
		if version == 0 || len(key) < 32 || (exact32 && len(key) != 32) {
			return fmt.Errorf("%s version %d has invalid key length", name, version)
		}
	}
	return nil
}

func (c *Config) Validate() error {
	if c.HTTPAddr == "" {
		return fmt.Errorf("HTTPAddr cannot be empty")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil && !strings.HasPrefix(c.HTTPAddr, ":") {
		return fmt.Errorf("invalid HTTPAddr format: %s", c.HTTPAddr)
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
	if c.RateLimitMaxEntries < 100 {
		return fmt.Errorf("RateLimitMaxEntries must be at least 100")
	}
	if c.HMACSecret != "" && len(c.HMACSecret) < 16 {
		return fmt.Errorf("HMACSecret must be at least 16 bytes")
	}
	if c.EncryptionKey != "" {
		if _, err := ParseEncryptionKey(c.EncryptionKey); err != nil {
			return fmt.Errorf("invalid EncryptionKey: %w", err)
		}
	}
	if len(c.HMACSecret) < 16 {
		return fmt.Errorf("HMACSecret must be at least 16 bytes")
	}
	if c.EncryptionKey != "" {
		if _, err := ParseEncryptionKey(c.EncryptionKey); err != nil {
			return fmt.Errorf("invalid EncryptionKey: %w", err)
		}
	}
	for _, cidr := range c.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid trusted proxy CIDR %q", cidr)
		}
	}
	for name, ring := range map[string]VersionedKeyring{
		"email lookup HMAC": c.EmailLookupKeys, "auth subject HMAC": c.AuthSubjectKeys,
		"session HMAC": c.SessionKeys, "CSRF HMAC": c.CSRFKeys,
		"verification code HMAC": c.VerificationCodeKeys, "binding code HMAC": c.BindingCodeKeys,
		"grant HMAC": c.GrantKeys, "idempotency HMAC": c.IdempotencyKeys,
	} {
		if err := validateKeyring(name, ring, false); err != nil {
			return err
		}
	}
	if err := validateKeyring("AEAD", c.AEADKeys, true); err != nil {
		return err
	}

	local := isExplicitLocalEnvironment(c.Environment)
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
		if c.ObjectRegion == "" {
			return fmt.Errorf("TOKENDANCE_OBJECT_REGION is required for S3 object storage")
		}
		if c.ObjectBucket == "" {
			return fmt.Errorf("TOKENDANCE_OBJECT_BUCKET is required for S3 object storage")
		}
		if c.ObjectAccessKey == "" || c.ObjectSecretKey == "" {
			return fmt.Errorf("S3 object storage credentials are required")
		}
	}
	if c.EmailProvider == "smtp" {
		if c.SMTPHost == "" {
			return fmt.Errorf("TOKENDANCE_SMTP_HOST is required for SMTP")
		}
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return fmt.Errorf("TOKENDANCE_SMTP_PORT must be between 1 and 65535")
		}
		if c.SMTPUsername == "" || c.SMTPPassword == "" {
			return fmt.Errorf("SMTP credentials are required")
		}
		if _, err := mail.ParseAddress(c.SMTPFrom); err != nil {
			return fmt.Errorf("TOKENDANCE_SMTP_FROM must be a valid email address")
		}
		if c.SMTPTLSMode != "starttls" && c.SMTPTLSMode != "tls" && c.SMTPTLSMode != "none" {
			return fmt.Errorf("TOKENDANCE_SMTP_TLS_MODE must be starttls, tls, or none")
		}
		if !local && c.SMTPTLSMode == "none" {
			return fmt.Errorf("SMTP TLS cannot be disabled outside development or test")
		}
	}
	if c.Environment == "production" {
		if c.MySQLDSN == "" {
			return fmt.Errorf("TOKENDANCE_MYSQL_DSN_FILE is required in production environment")
		}
		if c.MySQLDSNFile == "" {
			return fmt.Errorf("production MySQL DSN must be loaded from TOKENDANCE_MYSQL_DSN_FILE")
		}
		for name, ring := range map[string]VersionedKeyring{
			"email lookup HMAC": c.EmailLookupKeys, "auth subject HMAC": c.AuthSubjectKeys,
			"session HMAC": c.SessionKeys, "CSRF HMAC": c.CSRFKeys,
			"verification code HMAC": c.VerificationCodeKeys, "binding code HMAC": c.BindingCodeKeys,
			"grant HMAC": c.GrantKeys, "idempotency HMAC": c.IdempotencyKeys, "AEAD": c.AEADKeys,
		} {
			if ring.SecretFile == "" {
				return fmt.Errorf("production requires %s keyring from its secret file", name)
			}
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
	return nil
}
