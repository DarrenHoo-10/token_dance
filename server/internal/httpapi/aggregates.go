package httpapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"tokendance/internal/domain"
)

func (h *Handlers) IngestAggregate(w http.ResponseWriter, r *http.Request) {
	id, signature, err := parseDeviceAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	timestamp, nonce, bodyHash := strings.TrimSpace(r.Header.Get("X-Timestamp")), strings.TrimSpace(r.Header.Get("X-Nonce")), strings.TrimSpace(r.Header.Get("X-Body-SHA256"))
	now := time.Now().UTC()
	if _, err = validateTelemetryHeaders(timestamp, nonce, bodyHash, now); err != nil {
		WriteError(w, r, err)
		return
	}
	raw, body, err := readTelemetryBody(w, r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	digest := sha256.Sum256(raw)
	expected, err := hex.DecodeString(bodyHash)
	if err != nil || subtle.ConstantTimeCompare(expected, digest[:]) != 1 {
		WriteError(w, r, domain.NewAppError(400, "INGEST_BODY_HASH_MISMATCH", "ingest.bodyHashMismatch", "body hash mismatch", nil, domain.ErrInvalidArgument))
		return
	}
	inst, err := h.device.GetIngestInstallation(r.Context(), id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	canonical := telemetryCanonicalRequest(r.Method, r.URL.EscapedPath(), timestamp, nonce, bodyHash)
	if !ed25519.Verify(ed25519.PublicKey(inst.DevicePublicKey[:]), []byte(canonical), signature) {
		WriteError(w, r, domain.NewAppError(401, "DEVICE_SIGNATURE_INVALID", "device.invalidSignature", "invalid signature", nil, domain.ErrUnauthorized))
		return
	}
	var snapshot domain.AggregateSnapshot
	if err = decodeTelemetryJSON(body, &snapshot); err != nil {
		WriteError(w, r, err)
		return
	}
	ack, err := h.device.CommitAggregate(r.Context(), domain.AggregateCommit{Snapshot: snapshot, InstallationID: id, Digest: sha256.Sum256(body), NonceHash: sha256.Sum256([]byte(nonce)), ReceivedAt: now})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, ack)
}
