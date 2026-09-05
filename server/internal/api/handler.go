package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tokendance/token-collector/server/internal/auth"
	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/ingest"
	"github.com/tokendance/token-collector/server/internal/protocol"
	"github.com/tokendance/token-collector/server/internal/store"
	"github.com/tokendance/token-collector/server/internal/store/mysqlstore"
)

// Handler holds all HTTP handlers for the API server.
type Handler struct {
	Users         store.UserStore
	Installations store.InstallationStore
	Batches       store.BatchStore
	Events        store.EventStore
	Leaderboards  store.LeaderboardStore
	Ingest        *ingest.Service
	DeviceAuth    *auth.DeviceAuth
	UserSessions  auth.UserSessionResolver
	IDGenerator   func() string
}

// RegisterInstallationRequest is the JSON body for POST /v1/installations/register.
type RegisterInstallationRequest struct {
	DevicePublicKey  string `json:"devicePublicKey"`
	DeviceName       string `json:"deviceName,omitempty"`
	OSType           string `json:"osType"`
	Architecture     string `json:"architecture"`
	CollectorVersion string `json:"collectorVersion"`
	InstallationID   string `json:"installationId,omitempty"`
}

type RegisterInstallationResponse struct {
	InstallationID string       `json:"installationId"`
	Policy         UploadPolicy `json:"policy"`
}

type UploadPolicy struct {
	MaxBatchEvents       int `json:"maxBatchEvents"`
	MaxBatchBytes        int `json:"maxBatchBytes"`
	FlushIntervalSeconds int `json:"flushIntervalSeconds"`
}

// HandleRegisterInstallation handles POST /v1/installations/register.
func (h *Handler) HandleRegisterInstallation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.UserSessions == nil {
		http.Error(w, "user session authentication unavailable", http.StatusUnauthorized)
		return
	}
	userID, err := h.UserSessions.ResolveUserSession(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		http.Error(w, "invalid user session", http.StatusUnauthorized)
		return
	}

	var req RegisterInstallationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	pubKeyBytes, err := base64.RawURLEncoding.DecodeString(req.DevicePublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		http.Error(w, "invalid device public key", http.StatusBadRequest)
		return
	}
	if req.OSType != "windows" && req.OSType != "macos" {
		http.Error(w, "unsupported osType", http.StatusBadRequest)
		return
	}
	if req.Architecture == "" || req.CollectorVersion == "" {
		http.Error(w, "architecture and collectorVersion are required", http.StatusBadRequest)
		return
	}
	if existing, lookupErr := h.Installations.GetInstallationByPublicKey(r.Context(), pubKeyBytes); lookupErr == nil {
		if existing.UserID != userID {
			http.Error(w, "device public key already registered", http.StatusConflict)
			return
		}
		h.writeRegistration(w, http.StatusOK, existing.InstallationID)
		return
	}

	instID := strings.TrimSpace(req.InstallationID)
	if instID == "" {
		instID = h.IDGenerator()
	} else if !validCollectorInstallationID(instID) {
		http.Error(w, "invalid installationId", http.StatusBadRequest)
		return
	} else if existing, lookupErr := h.Installations.GetInstallation(r.Context(), instID); lookupErr == nil {
		if existing.UserID != userID {
			http.Error(w, "installation already registered", http.StatusConflict)
			return
		}
		h.writeRegistration(w, http.StatusOK, existing.InstallationID)
		return
	}
	now := time.Now().UTC()

	inst := &domain.Installation{
		InstallationID:     instID,
		UserID:             userID,
		DevicePublicKey:    ed25519.PublicKey(pubKeyBytes),
		DeviceName:         req.DeviceName,
		OSType:             req.OSType,
		Architecture:       req.Architecture,
		CollectorVersion:   req.CollectorVersion,
		InstallationStatus: "active",
		RegisteredAt:       now,
	}

	if err := h.Installations.CreateInstallation(r.Context(), inst); err != nil {
		winner, lookupErr := h.Installations.GetInstallationByPublicKey(r.Context(), pubKeyBytes)
		if lookupErr == nil {
			if winner.UserID != userID {
				http.Error(w, "device public key already registered", http.StatusConflict)
				return
			}
			h.writeRegistration(w, http.StatusOK, winner.InstallationID)
			return
		}
		http.Error(w, fmt.Sprintf("registration failed: %v", err), http.StatusConflict)
		return
	}

	h.writeRegistration(w, http.StatusCreated, instID)
}

func validCollectorInstallationID(id string) bool {
	if len(id) != 30 || !strings.HasPrefix(id, "ins_") {
		return false
	}
	for _, char := range id[4:] {
		if (char < '0' || char > '9') && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
			return false
		}
	}
	return true
}

func (h *Handler) writeRegistration(w http.ResponseWriter, status int, installationID string) {
	resp := RegisterInstallationResponse{
		InstallationID: installationID,
		Policy: UploadPolicy{
			MaxBatchEvents:       500,
			MaxBatchBytes:        524288,
			FlushIntervalSeconds: 30,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

const maxDecompressedIngestBodySize = 4 << 20

// HandleIngestBatch handles POST /v1/telemetry/batches.
// The installation_id comes from device auth context.
func (h *Handler) HandleIngestBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	installationID := auth.InstallationIDFromContext(r.Context())
	if installationID == "" {
		http.Error(w, "missing installation identity", http.StatusUnauthorized)
		return
	}

	if r.Header.Get("Content-Encoding") != "gzip" {
		http.Error(w, "content encoding must be gzip", http.StatusUnsupportedMediaType)
		return
	}
	compressed, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, "invalid gzip body", http.StatusBadRequest)
		return
	}
	decompressed, err := io.ReadAll(io.LimitReader(compressed, maxDecompressedIngestBodySize+1))
	closeErr := compressed.Close()
	if err != nil || closeErr != nil {
		http.Error(w, "invalid gzip body", http.StatusBadRequest)
		return
	}
	if len(decompressed) > maxDecompressedIngestBodySize {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var batch protocol.UploadBatch
	decoder := json.NewDecoder(bytes.NewReader(decompressed))
	if err := decoder.Decode(&batch); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	bodyHash, ok := auth.BodyHashFromContext(r.Context())
	if !ok {
		http.Error(w, "missing authenticated body hash", http.StatusUnauthorized)
		return
	}
	result, err := h.Ingest.ProcessBatchWithHash(r.Context(), installationID, batch, bodyHash)
	if err != nil {
		if errors.Is(err, store.ErrBatchHashConflict) {
			http.Error(w, "BATCH_HASH_CONFLICT", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ack := protocol.UploadAck{
		BatchID:    result.BatchID,
		Accepted:   result.Accepted,
		Duplicates: result.Duplicates,
		Rejected:   []protocol.RejectedEvent{},
		ServerTime: result.ServerTime.Format(time.RFC3339Nano),
	}
	for _, rej := range result.Rejected {
		ack.Rejected = append(ack.Rejected, protocol.RejectedEvent{
			EventID:   protocol.Base64Url32(rej.EventID),
			ErrorCode: protocol.RejectedEventErrorCode(rej.ErrorCode),
			Retryable: rej.Retryable,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ack)
}

// LeaderboardSnapshotResponse is the public API response for a leaderboard.
type LeaderboardSnapshotResponse struct {
	SnapshotID       string                     `json:"snapshotId"`
	BoardKey         string                     `json:"boardKey"`
	ScopeType        string                     `json:"scopeType"`
	ScopeKey         string                     `json:"scopeKey"`
	MetricKey        string                     `json:"metricKey"`
	WindowStart      string                     `json:"windowStart"`
	WindowEnd        string                     `json:"windowEnd"`
	ParticipantCount uint32                     `json:"participantCount"`
	GeneratedAt      string                     `json:"generatedAt"`
	DataWatermarkAt  string                     `json:"dataWatermarkAt"`
	LagSeconds       int64                      `json:"lagSeconds"`
	Entries          []LeaderboardEntryResponse `json:"entries"`
}

type LeaderboardEntryResponse struct {
	Rank        uint32  `json:"rank"`
	DisplayName string  `json:"displayName"`
	AvatarURL   string  `json:"avatarUrl,omitempty"`
	MetricValue float64 `json:"metricValue"`
	RankChange  *int32  `json:"rankChange,omitempty"`
}

// HandleGetLeaderboard handles GET /v1/leaderboard?board=...&scope=...&metric=...
func (h *Handler) HandleGetLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	boardKey := r.URL.Query().Get("board")
	if boardKey == "" {
		boardKey = "default"
	}
	scopeType := r.URL.Query().Get("scope")
	if scopeType == "" {
		scopeType = "global"
	}
	metricKey := r.URL.Query().Get("metric")
	if metricKey == "" {
		metricKey = "total_tokens"
	}
	scopeKey := r.URL.Query().Get("scopeKey")
	if scopeType == "global" {
		scopeKey = "all"
	}
	if scopeKey == "" {
		http.Error(w, "missing scopeKey", http.StatusBadRequest)
		return
	}
	if !h.authorizeScope(r, scopeType, scopeKey) {
		http.Error(w, "forbidden leaderboard scope", http.StatusForbidden)
		return
	}

	var snap *domain.LeaderboardSnapshot
	var err error
	if scoped, ok := h.Leaderboards.(interface {
		LatestPublishedSnapshotScoped(context.Context, string, string, string, string) (*domain.LeaderboardSnapshot, error)
	}); ok {
		snap, err = scoped.LatestPublishedSnapshotScoped(r.Context(), boardKey, scopeType, scopeKey, metricKey)
	} else {
		snap, err = h.Leaderboards.LatestPublishedSnapshot(r.Context(), boardKey, scopeType, metricKey)
	}
	if err != nil {
		http.Error(w, "no leaderboard available", http.StatusNotFound)
		return
	}

	entries, err := h.Leaderboards.ListEntries(r.Context(), snap.SnapshotID)
	if err != nil {
		http.Error(w, "failed to load entries", http.StatusInternalServerError)
		return
	}

	resp := LeaderboardSnapshotResponse{
		SnapshotID:       snap.SnapshotID,
		BoardKey:         snap.BoardKey,
		ScopeType:        snap.ScopeType,
		ScopeKey:         snap.ScopeKey,
		MetricKey:        snap.MetricKey,
		WindowStart:      snap.WindowStart.Format(time.RFC3339),
		WindowEnd:        snap.WindowEnd.Format(time.RFC3339),
		ParticipantCount: snap.ParticipantCount,
		GeneratedAt:      snap.GeneratedAt.Format(time.RFC3339),
		DataWatermarkAt:  snap.DataWatermarkAt.Format(time.RFC3339),
		LagSeconds:       watermarkLagSeconds(snap.DataWatermarkAt),
	}
	for _, e := range entries {
		entry := LeaderboardEntryResponse{
			Rank:        e.RankNo,
			DisplayName: e.DisplayNameSnapshot,
			AvatarURL:   e.AvatarURLSnapshot,
			MetricValue: e.MetricValue,
		}
		if e.PreviousRankNo != nil {
			change := int32(*e.PreviousRankNo) - int32(e.RankNo)
			entry.RankChange = &change
		}
		resp.Entries = append(resp.Entries, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleGetSnapshot handles GET /v1/leaderboard/snapshots/{id}
func (h *Handler) HandleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshotID := r.URL.Query().Get("id")
	if snapshotID == "" {
		http.Error(w, "missing snapshot id", http.StatusBadRequest)
		return
	}

	snap, err := h.Leaderboards.GetSnapshot(r.Context(), snapshotID)
	if err != nil {
		http.Error(w, "snapshot not found", http.StatusNotFound)
		return
	}
	if snap.SnapshotStatus != "published" {
		http.Error(w, "snapshot not published", http.StatusNotFound)
		return
	}
	if !h.authorizeScope(r, snap.ScopeType, snap.ScopeKey) {
		http.Error(w, "forbidden leaderboard scope", http.StatusForbidden)
		return
	}

	entries, err := h.Leaderboards.ListEntries(r.Context(), snapshotID)
	if err != nil {
		http.Error(w, "failed to load entries", http.StatusInternalServerError)
		return
	}

	resp := LeaderboardSnapshotResponse{
		SnapshotID:       snap.SnapshotID,
		BoardKey:         snap.BoardKey,
		ScopeType:        snap.ScopeType,
		ScopeKey:         snap.ScopeKey,
		MetricKey:        snap.MetricKey,
		WindowStart:      snap.WindowStart.Format(time.RFC3339),
		WindowEnd:        snap.WindowEnd.Format(time.RFC3339),
		ParticipantCount: snap.ParticipantCount,
		GeneratedAt:      snap.GeneratedAt.Format(time.RFC3339),
		DataWatermarkAt:  snap.DataWatermarkAt.Format(time.RFC3339),
		LagSeconds:       watermarkLagSeconds(snap.DataWatermarkAt),
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, LeaderboardEntryResponse{
			Rank:        e.RankNo,
			DisplayName: e.DisplayNameSnapshot,
			AvatarURL:   e.AvatarURLSnapshot,
			MetricValue: e.MetricValue,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func watermarkLagSeconds(watermark time.Time) int64 {
	lag := time.Since(watermark).Seconds()
	if lag < 0 {
		return 0
	}
	return int64(lag)
}

func (h *Handler) authorizeScope(r *http.Request, scopeType, scopeKey string) bool {
	authorizer, ok := h.Leaderboards.(interface {
		AuthorizeLeaderboardScope(context.Context, string, string, string) (bool, error)
	})
	if !ok {
		return scopeType == "global" && scopeKey == "all"
	}
	userID := ""
	if scopeType != "global" {
		if h.UserSessions == nil {
			return false
		}
		var err error
		userID, err = h.UserSessions.ResolveUserSession(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			return false
		}
	}
	allowed, err := authorizer.AuthorizeLeaderboardScope(r.Context(), userID, scopeType, scopeKey)
	return err == nil && allowed
}

type deletionRequestBody struct {
	Scope          string `json:"scope"`
	InstallationID string `json:"installationId,omitempty"`
	RangeStart     string `json:"rangeStart,omitempty"`
	RangeEnd       string `json:"rangeEnd,omitempty"`
}

type deletionResponse struct {
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func (h *Handler) HandleDeletionRequests(w http.ResponseWriter, r *http.Request) {
	if h.UserSessions == nil {
		http.Error(w, "user session authentication unavailable", http.StatusUnauthorized)
		return
	}
	userID, err := h.UserSessions.ResolveUserSession(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		http.Error(w, "invalid user session", http.StatusUnauthorized)
		return
	}
	deletions, ok := h.Events.(interface {
		CreateDeletionRequest(context.Context, mysqlstore.DeletionRequest) error
		GetDeletionRequest(context.Context, string, string) (mysqlstore.DeletionRequest, error)
	})
	if !ok {
		http.Error(w, "deletion workflow unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodGet {
		requestID := r.URL.Query().Get("id")
		request, err := deletions.GetDeletionRequest(r.Context(), requestID, userID)
		if err != nil {
			http.Error(w, "deletion request not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, deletionResponse{RequestID: request.RequestID, Status: request.Status, ErrorCode: request.ErrorCode})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body deletionRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	request := mysqlstore.DeletionRequest{RequestID: h.IDGenerator(), UserID: userID, InstallationID: body.InstallationID, Scope: body.Scope, Status: "pending", RequestedAt: time.Now().UTC()}
	if body.RangeStart != "" {
		value, err := time.Parse(time.RFC3339, body.RangeStart)
		if err != nil {
			http.Error(w, "invalid rangeStart", http.StatusBadRequest)
			return
		}
		request.RangeStart = &value
	}
	if body.RangeEnd != "" {
		value, err := time.Parse(time.RFC3339, body.RangeEnd)
		if err != nil {
			http.Error(w, "invalid rangeEnd", http.StatusBadRequest)
			return
		}
		request.RangeEnd = &value
	}
	if err := deletions.CreateDeletionRequest(r.Context(), request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, deletionResponse{RequestID: request.RequestID, Status: "pending"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const ingestHTTPDeadline = 5 * time.Second

func withIngestDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), ingestHTTPDeadline)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// NewMux creates the HTTP router with all routes.
func NewMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.HandleHealthz)
	mux.HandleFunc("/v1/installations/register", h.HandleRegisterInstallation)
	ingestHandler := http.Handler(http.HandlerFunc(h.HandleIngestBatch))
	if h.DeviceAuth != nil {
		ingestHandler = h.DeviceAuth.Middleware(ingestHandler)
	}
	mux.Handle("/v1/telemetry/batches", withIngestDeadline(ingestHandler))
	mux.HandleFunc("/v1/leaderboard", h.HandleGetLeaderboard)
	mux.HandleFunc("/v1/leaderboard/snapshots", h.HandleGetSnapshot)
	mux.HandleFunc("/v1/data-deletion-requests", h.HandleDeletionRequests)
	return mux
}

// HandleHealthz returns 200 with {"status":"ok"}.
func (h *Handler) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
