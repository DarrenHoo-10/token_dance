package domain

import (
	"time"

	v2 "tokendance/internal/protocol/v2"
)

// TelemetryUsageIdentity is the server-computed, opaque cross-device usage
// identity for one event. It is aligned with TelemetryEventsV2Input.Events by
// index; events without a wire usageIdentity have no entry.
type TelemetryUsageIdentity struct {
	Hash   [32]byte
	Source uint8 // 0 original/unspecified, 1 ssh_mirror
}

// TelemetryEventsV2Input is the authenticated, validated batch handed to the
// ingest transaction. user_id comes from the login session, never the payload.
type TelemetryEventsV2Input struct {
	Reconstruction       bool
	InstallationID       string
	UserID               string
	BindingStatusVersion uint64
	NonceHash            [32]byte
	NonceExpiresAt       time.Time
	RequestID            string
	Events               []v2.EventEnvelope
	UsageIdentities      []TelemetryUsageIdentity
	ReceivedAt           time.Time
}

// TelemetryEventsV2Result is returned only after a successful COMMIT.
type TelemetryEventsV2Result struct {
	RequestID    string
	ServerTimeMs uint64
	Acks         []v2.EventAck
}

// ErrBindingVersionMismatch is returned when the signed binding status version
// does not match the locked installation row.
var ErrBindingVersionMismatch = NewSentinel("binding status version mismatch")

// ErrInstallationUserMismatch is returned when the session user is not the
// current owner of the installation.
var ErrInstallationUserMismatch = NewSentinel("installation is bound to a different user")

// NewSentinel builds a plain sentinel error without pulling extra deps into
// domain for one-off P5 codes. Prefer typed vars above over string compares.
func NewSentinel(msg string) error {
	return sentinelError(msg)
}

type sentinelError string

func (e sentinelError) Error() string { return string(e) }
