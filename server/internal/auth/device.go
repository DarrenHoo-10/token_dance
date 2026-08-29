package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/tokendance/token-collector/server/internal/store"
)

type contextKey string

const InstallationIDKey contextKey = "installation_id"

const (
	maxTimestampSkew = 5 * time.Minute
	nonceExpiry      = 10 * time.Minute
)

// DeviceAuth provides Ed25519 signature verification for device requests.
// Authorization header format: Device <installation_id>:<base64(signature)>
// Required headers: X-Timestamp (RFC3339), X-Nonce (base64, 16+ bytes)
// Signature input: METHOD\nPATH\nTIMESTAMP\nNONCE\nBODY_SHA256
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
	sigBytes, err := base64.StdEncoding.DecodeString(parts[1])
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
	nonceBytes, err := base64.StdEncoding.DecodeString(nonceB64)
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

	// build signature message
	bodyHashB64 := base64.StdEncoding.EncodeToString(bodyHash[:])
	msg := strings.Join([]string{method, path, tsStr, nonceB64, bodyHashB64}, "\n")

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
		// For simplicity in this slice, body hash is computed from Content-Length.
		// Full implementation reads and hashes the body.
		bodyHash := sha256.Sum256(nil) // placeholder for empty/streamed
		installationID, err := d.VerifyRequest(r.Context(), r.Method, r.URL.Path, bodyHash, r.Header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), InstallationIDKey, installationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SignRequest creates the Authorization, X-Timestamp, and X-Nonce headers for
// a client request. Used in tests and the collector client.
func SignRequest(installationID string, privKey ed25519.PrivateKey, method, path string, bodyHash [32]byte, nonce []byte) (http.Header, error) {
	if len(nonce) < 16 {
		return nil, fmt.Errorf("nonce must be at least 16 bytes")
	}
	ts := time.Now().UTC().Truncate(time.Second)
	if ts.Unix() > math.MaxInt64-1 {
		return nil, fmt.Errorf("timestamp overflow")
	}
	tsStr := ts.Format(time.RFC3339)
	nonceB64 := base64.StdEncoding.EncodeToString(nonce)
	bodyHashB64 := base64.StdEncoding.EncodeToString(bodyHash[:])

	msg := strings.Join([]string{method, path, tsStr, nonceB64, bodyHashB64}, "\n")
	sig := ed25519.Sign(privKey, []byte(msg))

	hdr := make(http.Header)
	hdr.Set("Authorization", "Device "+installationID+":"+base64.StdEncoding.EncodeToString(sig))
	hdr.Set("X-Timestamp", tsStr)
	hdr.Set("X-Nonce", nonceB64)
	return hdr, nil
}
