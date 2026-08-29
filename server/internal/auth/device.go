package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tokendance/token-collector/server/internal/store"
)

type contextKey string

const (
	InstallationIDKey contextKey = "installation_id"
	BodyHashKey       contextKey = "body_sha256"
)

func InstallationIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(InstallationIDKey).(string)
	return id
}

func BodyHashFromContext(ctx context.Context) ([32]byte, bool) {
	hash, ok := ctx.Value(BodyHashKey).([32]byte)
	return hash, ok
}

const (
	maxTimestampSkew      = 5 * time.Minute
	nonceExpiry           = 10 * time.Minute
	maxCompressedBodySize = 512 << 10
)

// DeviceAuth provides Ed25519 signature verification for device requests.
// Authorization header format: Device <installation_id>:<base64url(signature)>.
// Required headers: X-Timestamp (RFC3339), X-Nonce (base64url, 16+ bytes).
// Signature input: METHOD\nPATH\nTIMESTAMP\nNONCE\nBODY_SHA256.
type DeviceAuth struct {
	Installations store.InstallationStore
	Nonces        store.NonceStore
}

// VerifyRequest validates the device signature, timestamp, and nonce.
// On success, returns the installation ID. The caller should inject it into
// the request context.
func (d *DeviceAuth) VerifyRequest(ctx context.Context, method, path string, bodyHash [32]byte, hdr http.Header) (string, error) {
	authHeader := hdr.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("missing Authorization header")
	}
	if !strings.HasPrefix(authHeader, "Device ") {
		return "", fmt.Errorf("unsupported auth scheme")
	}
	parts := strings.SplitN(authHeader[7:], ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed Authorization header")
	}
	installationID := parts[0]
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid signature encoding: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return "", fmt.Errorf("invalid signature length")
	}

	tsStr := hdr.Get("X-Timestamp")
	if tsStr == "" {
		return "", fmt.Errorf("missing X-Timestamp header")
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		return "", fmt.Errorf("invalid X-Timestamp: %w", err)
	}
	skew := time.Since(ts)
	if skew < 0 {
		skew = -skew
	}
	if skew > maxTimestampSkew {
		return "", fmt.Errorf("timestamp skew %v exceeds maximum %v", skew, maxTimestampSkew)
	}

	nonceB64 := hdr.Get("X-Nonce")
	if nonceB64 == "" {
		return "", fmt.Errorf("missing X-Nonce header")
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", fmt.Errorf("invalid nonce encoding: %w", err)
	}
	if len(nonceBytes) < 16 {
		return "", fmt.Errorf("nonce too short (minimum 16 bytes)")
	}

	inst, err := d.Installations.GetInstallation(ctx, installationID)
	if err != nil {
		return "", fmt.Errorf("unknown installation: %w", err)
	}
	if inst.InstallationStatus != "active" {
		return "", fmt.Errorf("installation %s is %s", installationID, inst.InstallationStatus)
	}

	msg := CanonicalRequest(method, path, tsStr, nonceB64, bodyHash)
	if !ed25519.Verify(inst.DevicePublicKey, []byte(msg), sigBytes) {
		return "", fmt.Errorf("signature verification failed")
	}

	// anti-replay: consume nonce
	nonceHash := sha256.Sum256(nonceBytes)
	fresh, err := d.Nonces.ConsumeNonce(ctx, installationID, nonceHash, time.Now().UTC().Add(nonceExpiry))
	if err != nil {
		return "", fmt.Errorf("nonce check failed: %w", err)
	}
	if !fresh {
		return "", fmt.Errorf("nonce replay detected")
	}

	return installationID, nil
}

// Middleware returns an http.Handler that enforces device authentication.
func (d *DeviceAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCompressedBodySize))
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		bodyHash := sha256.Sum256(body)
		installationID, err := d.VerifyRequest(r.Context(), r.Method, r.URL.EscapedPath(), bodyHash, r.Header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), InstallationIDKey, installationID)
		ctx = context.WithValue(ctx, BodyHashKey, bodyHash)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func CanonicalRequest(method, path, timestamp, nonce string, bodyHash [32]byte) string {
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		timestamp,
		nonce,
		base64.RawURLEncoding.EncodeToString(bodyHash[:]),
	}, "\n")
}

// SignRequest creates the Authorization, X-Timestamp, and X-Nonce headers.
func SignRequest(installationID string, privKey ed25519.PrivateKey, method, path string, bodyHash [32]byte, nonce []byte) (http.Header, error) {
	if len(nonce) < 16 {
		return nil, fmt.Errorf("nonce must be at least 16 bytes")
	}
	tsStr := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	nonceB64 := base64.RawURLEncoding.EncodeToString(nonce)
	msg := CanonicalRequest(method, path, tsStr, nonceB64, bodyHash)
	sig := ed25519.Sign(privKey, []byte(msg))

	hdr := make(http.Header)
	hdr.Set("Authorization", "Device "+installationID+":"+base64.RawURLEncoding.EncodeToString(sig))
	hdr.Set("X-Timestamp", tsStr)
	hdr.Set("X-Nonce", nonceB64)
	return hdr, nil
}
