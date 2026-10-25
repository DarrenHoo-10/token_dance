package telemetry

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

const (
	usageIdentityKeyLabel   = "tokendance:usage-identity-key:v1"
	usageIdentityHashLabel  = "tokendance:usage-identity:v1"
	usageIdentityBackfillV1 = "tokendance:usage-identity-backfill:v1"
)

// UsageIdentitySourceSSHMirror marks an event copy that comes from a Claude
// Code SSH remote session's local mirror transcript.
const UsageIdentitySourceSSHMirror = uint8(1)

// UsageIdentityKey derives the server-side HMAC key for usage identities from
// the deployment HMAC secret. Ingest and the backfill command must derive the
// same key, or identities will never match across old and new events.
func UsageIdentityKey(hmacSecret string) []byte {
	mac := hmac.New(sha256.New, []byte(hmacSecret))
	mac.Write([]byte(usageIdentityKeyLabel))
	return mac.Sum(nil)
}

// UsageIdentityHash binds harness + native session id + native message id into
// one opaque 32-byte identity shared by every copy of the same fact. The raw
// components are used only here and are never persisted.
func UsageIdentityHash(key []byte, harnessID, sessionID, messageID string) [32]byte {
	mac := hmac.New(sha256.New, key)
	writeIdentityField(mac, []byte(usageIdentityHashLabel))
	writeIdentityField(mac, []byte(harnessID))
	writeIdentityField(mac, []byte(sessionID))
	writeIdentityField(mac, []byte(messageID))
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// BackfillUsageIdentityHash derives a synthetic identity for one group of
// already-ingested duplicate events that predate wire usageIdentity support.
// The group signature is the exact tuple the dedup heuristic matched on, so a
// re-run of the backfill derives the same identity for the same group.
func BackfillUsageIdentityHash(key []byte, harnessID, eventType string, occurredAtMs, modelKey int64, payloadSignature string) [32]byte {
	mac := hmac.New(sha256.New, key)
	writeIdentityField(mac, []byte(usageIdentityBackfillV1))
	writeIdentityField(mac, []byte(harnessID))
	writeIdentityField(mac, []byte(eventType))
	var at [8]byte
	binary.BigEndian.PutUint64(at[:], uint64(occurredAtMs))
	writeIdentityField(mac, at[:])
	var mk [8]byte
	binary.BigEndian.PutUint64(mk[:], uint64(modelKey))
	writeIdentityField(mac, mk[:])
	writeIdentityField(mac, []byte(payloadSignature))
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// Length-prefix every field so distinct component boundaries cannot collide.
func writeIdentityField(mac interface{ Write([]byte) (int, error) }, field []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(field)))
	_, _ = mac.Write(lenBuf[:])
	_, _ = mac.Write(field)
}
