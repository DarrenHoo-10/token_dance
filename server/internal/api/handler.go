package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/ingest"
	"github.com/tokendance/token-collector/server/internal/protocol"
	"github.com/tokendance/token-collector/server/internal/store"
)

// Handler holds all HTTP handlers for the API server.
type Handler struct {
	Users         store.UserStore
	Installations store.InstallationStore
	Batches       store.BatchStore
	Events        store.EventStore
	Leaderboards  store.LeaderboardStore
	Ingest        *ingest.Service
	IDGenerator   func() string
}

// RegisterInstallationRequest is the JSON body for POST /v1/installations/register.
type RegisterInstallationRequest struct {
	DevicePublicKey  string `json:"devicePublicKey"`
	DeviceName       string `json:"deviceName,omitempty"`
	OSType           string `json:"osType"`
	Architecture     string `json:"architecture"`
	CollectorVersion string `json:"collectorVersion"`
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
// Requires a valid user session (simplified: X-User-ID header for this slice).
func (h *Handler) HandleRegisterInstallation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		http.Error(w, "missing X-User-ID", http.StatusUnauthorized)
		return
	}

	// Verify user exists
	if _, err := h.Users.GetUser(r.Context(), userID); err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	var req RegisterInstallationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	pubKeyBytes, err := base64.StdEncoding.DecodeString(req.DevicePublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		http.Error(w, "invalid device public key", http.StatusBadRequest)
		return
	}

	instID := h.IDGenerator()
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
		http.Error(w, fmt.Sprintf("registration failed: %v", err), http.StatusConflict)
		return
	}

	resp := RegisterInstallationResponse{
		InstallationID: instID,
		Policy: UploadPolicy{
			MaxBatchEvents:       500,
			MaxBatchBytes:        524288,
			FlushIntervalSeconds: 30,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// HandleIngestBatch handles POST /v1/telemetry/batches.
// The installation_id comes from device auth context.
func (h *Handler) HandleIngestBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	installationID, _ := r.Context().Value("installation_id").(string)
	if installationID == "" {
		// Fallback for simplified testing: read from header
		installationID = r.Header.Get("X-Installation-ID")
	}
	if installationID == "" {
		http.Error(w, "missing installation identity", http.StatusUnauthorized)
		return
	}

	var batch protocol.UploadBatch
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	result, err := h.Ingest.ProcessBatch(r.Context(), installationID, batch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ack := protocol.UploadAck{
		BatchID:    result.BatchID,
		Accepted:   result.Accepted,
		Duplicates: result.Duplicates,
		ServerTime: result.ServerTime.Format(time.RFC3339Nano),
	}
	for _, rej := range result.Rejected {
		errorCode := protocol.RejectedEventErrorCodeSchemaInvalid
		if rej.Retryable {
			errorCode = protocol.RejectedEventErrorCodeInternalRetryable
		}
		ack.Rejected = append(ack.Rejected, protocol.RejectedEvent{
			EventID:   protocol.Base64Url32(rej.EventID),
			ErrorCode: errorCode,
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
	MetricKey        string                     `json:"metricKey"`
	WindowStart      string                     `json:"windowStart"`
	WindowEnd        string                     `json:"windowEnd"`
	ParticipantCount uint32                     `json:"participantCount"`
	GeneratedAt      string                     `json:"generatedAt"`
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

	snap, err := h.Leaderboards.LatestPublishedSnapshot(r.Context(), boardKey, scopeType, metricKey)
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
		MetricKey:        snap.MetricKey,
		WindowStart:      snap.WindowStart.Format(time.RFC3339),
		WindowEnd:        snap.WindowEnd.Format(time.RFC3339),
		ParticipantCount: snap.ParticipantCount,
		GeneratedAt:      snap.GeneratedAt.Format(time.RFC3339),
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

	entries, err := h.Leaderboards.ListEntries(r.Context(), snapshotID)
	if err != nil {
		http.Error(w, "failed to load entries", http.StatusInternalServerError)
		return
	}

	resp := LeaderboardSnapshotResponse{
		SnapshotID:       snap.SnapshotID,
		BoardKey:         snap.BoardKey,
		ScopeType:        snap.ScopeType,
		MetricKey:        snap.MetricKey,
		WindowStart:      snap.WindowStart.Format(time.RFC3339),
		WindowEnd:        snap.WindowEnd.Format(time.RFC3339),
		ParticipantCount: snap.ParticipantCount,
		GeneratedAt:      snap.GeneratedAt.Format(time.RFC3339),
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

// NewMux creates the HTTP router with all routes.
func NewMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.HandleHealthz)
	mux.HandleFunc("/v1/installations/register", h.HandleRegisterInstallation)
	mux.HandleFunc("/v1/telemetry/batches", h.HandleIngestBatch)
	mux.HandleFunc("/v1/leaderboard", h.HandleGetLeaderboard)
	mux.HandleFunc("/v1/leaderboard/snapshots", h.HandleGetSnapshot)
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
