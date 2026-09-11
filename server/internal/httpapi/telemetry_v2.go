package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	stdsha256 "crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
)

const (
	maxTelemetryV2BodyBytes = int(v2.DefaultMaxBatchBytes)
	maxTelemetryV2Events    = int(v2.DefaultMaxBatchEvents)
)

func clientUpgradeRequired() *domain.AppError {
	return domain.NewAppError(
		http.StatusUpgradeRequired,
		"CLIENT_UPGRADE_REQUIRED",
		"client.upgradeRequired",
		"this telemetry endpoint requires a client upgrade to protocol v2",
		nil,
		nil,
	)
}

func (h *Handlers) TelemetryUpgradeRequired(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, clientUpgradeRequired())
}

func (h *Handlers) GetTelemetryCapabilities(w http.ResponseWriter, r *http.Request) {
	if cfg := h.auth.Config(); cfg != nil && !cfg.EventPipelineV2Ingest {
		WriteError(w, r, domain.NewAppError(
			http.StatusServiceUnavailable,
			"EVENT_PIPELINE_V2_PAUSED",
			"ingest.pipelinePaused",
			"event pipeline v2 ingest is paused; sync is suspended without reopening legacy upload",
			nil,
			nil,
		))
		return
	}
	now := time.Now().UTC()
	lower := domain.StartOfDay(now).AddDate(0, 0, -14).UnixMilli()
	WriteJSON(w, http.StatusOK, v2.TelemetryCapabilities{
		ProtocolVersion:                  v2.ProtocolVersionNumber,
		SupportedSchemaVersions:          []uint32{v2.SchemaVersion},
		SupportedMetricSemanticsVersions: []uint32{v2.MetricSemanticsVersion},
		MaxBatchEvents:                   v2.DefaultMaxBatchEvents,
		MaxBatchBytes:                    v2.DefaultMaxBatchBytes,
		ServerTimeMs:                     v2.UInt64String(strconv.FormatInt(now.UnixMilli(), 10)),
		EventReceiveLowerBoundMs:         v2.UInt64String(strconv.FormatInt(lower, 10)),
	})
}

func (h *Handlers) IngestTelemetryEventsV2(w http.ResponseWriter, r *http.Request) {
	if cfg := h.auth.Config(); cfg != nil && !cfg.EventPipelineV2Ingest {
		WriteError(w, r, domain.NewAppError(
			http.StatusServiceUnavailable,
			"EVENT_PIPELINE_V2_PAUSED",
			"ingest.pipelinePaused",
			"event pipeline v2 ingest is paused; sync is suspended without reopening legacy upload",
			nil,
			nil,
		))
		return
	}
	user := GetUserFromContext(r.Context())
	if user == nil {
		WriteError(w, r, domain.NewAppError(401, "AUTH_REQUIRED", "auth.required", "authentication required", nil, domain.ErrUnauthorized))
		return
	}
	if user.AccountStatus != domain.AccountStatusActive {
		WriteError(w, r, domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.accountSuspended", "user account is not active", nil, domain.ErrAccountSuspended))
		return
	}

	installationID, signature, err := parseDeviceAuthorization(r.Header.Get("X-Device-Authorization"))
	if err != nil {
		WriteError(w, r, err)
		return
	}

	timestampValue := strings.TrimSpace(r.Header.Get("X-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-Nonce"))
	bodyHashValue := strings.TrimSpace(r.Header.Get("X-Body-SHA256"))
	bindingVersionRaw := strings.TrimSpace(r.Header.Get("X-Binding-Status-Version"))
	bindingVersion, err := strconv.ParseUint(bindingVersionRaw, 10, 64)
	if err != nil || bindingVersion == 0 {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "device.invalidBindingVersion", "X-Binding-Status-Version must be a positive integer", nil, domain.ErrInvalidArgument))
		return
	}

	requestTime, err := validateTelemetryHeaders(timestampValue, nonce, bodyHashValue, time.Now().UTC())
	if err != nil {
		WriteError(w, r, err)
		return
	}

	rawBody, body, err := readTelemetryV2Body(w, r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	requestHash := stdsha256.Sum256(rawBody)
	headerHash, err := hex.DecodeString(bodyHashValue)
	if err != nil || len(headerHash) != stdsha256.Size || subtle.ConstantTimeCompare(headerHash, requestHash[:]) != 1 {
		WriteError(w, r, domain.NewAppError(400, "INGEST_BODY_HASH_MISMATCH", "ingest.bodyHashMismatch", "X-Body-SHA256 does not match the transmitted request body", nil, domain.ErrInvalidArgument))
		return
	}

	inst, err := h.device.GetIngestInstallation(r.Context(), installationID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	canonical := telemetryCanonicalRequestV2(
		r.Method,
		r.URL.EscapedPath(),
		timestampValue,
		nonce,
		hex.EncodeToString(requestHash[:]),
		installationID,
		bindingVersionRaw,
	)
	if !ed25519.Verify(ed25519.PublicKey(inst.DevicePublicKey[:]), []byte(canonical), signature) {
		WriteError(w, r, domain.NewAppError(401, "DEVICE_SIGNATURE_INVALID", "device.invalidSignature", "invalid device request signature", nil, domain.ErrUnauthorized))
		return
	}

	var in v2.TelemetryEventsRequest
	if err := decodeTelemetryJSON(body, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	if in.ProtocolVersion != v2.ProtocolVersionNumber {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "ingest.unsupportedProtocol", "protocolVersion must be 2", nil, domain.ErrInvalidArgument))
		return
	}
	if strings.TrimSpace(in.RequestID) == "" {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "ingest.invalidRequestId", "requestId is required", nil, domain.ErrInvalidArgument))
		return
	}
	if len(in.Events) == 0 || len(in.Events) > maxTelemetryV2Events {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "ingest.invalidBatchSize", "batch must contain between 1 and 500 events", nil, domain.ErrInvalidArgument))
		return
	}

	now := time.Now().UTC()
	nonceHash := stdsha256.Sum256([]byte(nonce))
	result, err := h.device.CommitTelemetryEventsV2(r.Context(), domain.TelemetryEventsV2Input{
		InstallationID:       installationID,
		UserID:               user.UserID,
		BindingStatusVersion: bindingVersion,
		NonceHash:            nonceHash,
		NonceExpiresAt:       requestTime.Add(telemetryNonceTTL),
		RequestID:            in.RequestID,
		Events:               in.Events,
		ReceivedAt:           now,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, v2.TelemetryEventsResponse{
		RequestID:    result.RequestID,
		ServerTimeMs: v2.UInt64String(strconv.FormatUint(result.ServerTimeMs, 10)),
		Acks:         result.Acks,
	})
}

func (h *Handlers) RebindInstallation(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		WriteError(w, r, domain.NewAppError(401, "AUTH_REQUIRED", "auth.required", "authentication required", nil, domain.ErrUnauthorized))
		return
	}
	if user.AccountStatus != domain.AccountStatusActive {
		WriteError(w, r, domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.accountSuspended", "user account is not active", nil, domain.ErrAccountSuspended))
		return
	}

	deviceAuth := r.Header.Get("X-Device-Authorization")
	installationID, signature, err := parseDeviceAuthorization(deviceAuth)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	timestampValue := strings.TrimSpace(r.Header.Get("X-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-Nonce"))
	bodyHashValue := strings.TrimSpace(r.Header.Get("X-Body-SHA256"))
	bindingVersionRaw := strings.TrimSpace(r.Header.Get("X-Binding-Status-Version"))
	if bindingVersionRaw == "" {
		bindingVersionRaw = "0"
	}

	if _, err := validateTelemetryHeaders(timestampValue, nonce, bodyHashValue, time.Now().UTC()); err != nil {
		WriteError(w, r, err)
		return
	}

	rawBody, _, err := readTelemetryV2Body(w, r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	requestHash := stdsha256.Sum256(rawBody)
	headerHash, err := hex.DecodeString(bodyHashValue)
	if err != nil || len(headerHash) != stdsha256.Size || subtle.ConstantTimeCompare(headerHash, requestHash[:]) != 1 {
		WriteError(w, r, domain.NewAppError(400, "INGEST_BODY_HASH_MISMATCH", "ingest.bodyHashMismatch", "X-Body-SHA256 does not match the transmitted request body", nil, domain.ErrInvalidArgument))
		return
	}

	inst, err := h.device.GetIngestInstallation(r.Context(), installationID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	canonical := telemetryCanonicalRequestV2(
		r.Method,
		r.URL.EscapedPath(),
		timestampValue,
		nonce,
		hex.EncodeToString(requestHash[:]),
		installationID,
		bindingVersionRaw,
	)
	if !ed25519.Verify(ed25519.PublicKey(inst.DevicePublicKey[:]), []byte(canonical), signature) {
		WriteError(w, r, domain.NewAppError(401, "DEVICE_SIGNATURE_INVALID", "device.invalidSignature", "invalid device request signature", nil, domain.ErrUnauthorized))
		return
	}
	if bindingVersionRaw != "0" {
		expected := strconv.FormatUint(inst.StatusVersion, 10)
		if bindingVersionRaw != expected {
			WriteError(w, r, domain.NewAppError(409, "DEVICE_BINDING_VERSION_MISMATCH", "device.bindingVersionMismatch", "installation binding status version mismatch", nil, domain.ErrBindingVersionMismatch))
			return
		}
	}

	updated, err := h.device.RebindInstallation(r.Context(), installationID, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, updated)
}

func telemetryCanonicalRequestV2(method, path, timestamp, nonce, bodyHash, installationID, bindingStatusVersion string) string {
	if path == "" {
		path = "/"
	}
	return strings.ToUpper(method) + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + strings.ToLower(bodyHash) + "\n" + installationID + "\n" + bindingStatusVersion
}

func readTelemetryV2Body(w http.ResponseWriter, r *http.Request) ([]byte, []byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxTelemetryV2BodyBytes))
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, nil, domain.NewAppError(413, "INGEST_BODY_TOO_LARGE", "ingest.bodyTooLarge", "telemetry request body exceeds 1 MiB", nil, domain.ErrInvalidArgument)
		}
		return nil, nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "failed to read telemetry request body", nil, err)
	}
	if r.Header.Get("Content-Encoding") == "" {
		return raw, raw, nil
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		return nil, nil, domain.NewAppError(415, "INGEST_CONTENT_ENCODING_UNSUPPORTED", "ingest.unsupportedEncoding", "only gzip content encoding is supported", nil, domain.ErrInvalidArgument)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid gzip telemetry body", nil, err)
	}
	defer zr.Close()
	decoded, err := io.ReadAll(io.LimitReader(zr, int64(maxTelemetryV2BodyBytes)+1))
	if err != nil {
		return nil, nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid gzip telemetry body", nil, err)
	}
	if len(decoded) > maxTelemetryV2BodyBytes {
		return nil, nil, domain.NewAppError(413, "INGEST_BODY_TOO_LARGE", "ingest.bodyTooLarge", "decoded telemetry request body exceeds 1 MiB", nil, domain.ErrInvalidArgument)
	}
	return raw, decoded, nil
}
