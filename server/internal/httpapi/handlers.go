package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	stdsha256 "crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"tokendance/internal/analytics"
	"tokendance/internal/auth"
	"tokendance/internal/device"
	"tokendance/internal/domain"
	"tokendance/internal/export"
	"tokendance/internal/leaderboard"
	"tokendance/internal/media"
	"tokendance/internal/privacy"
	"tokendance/internal/profile"
	"tokendance/internal/search"
	"tokendance/internal/telemetry"
)

type Handlers struct {
	auth             *auth.Service
	profile          *profile.Service
	privacy          *privacy.Service
	analytics        *analytics.Service
	device           *device.Service
	export           *export.Service
	media            *media.Service
	search           *search.Service
	leaderboard      *leaderboard.Service
	readinessChecker func(ctx context.Context) error
}

func NewHandlers(
	authService *auth.Service,
	profileService *profile.Service,
	privacyService *privacy.Service,
	analyticsService *analytics.Service,
	deviceService *device.Service,
	exportService *export.Service,
	mediaService *media.Service,
	searchService *search.Service,
	leaderboardService *leaderboard.Service,
) *Handlers {
	return NewHandlersWithReadiness(
		authService,
		profileService,
		privacyService,
		analyticsService,
		deviceService,
		exportService,
		mediaService,
		searchService,
		leaderboardService,
		nil,
	)
}

func NewHandlersWithReadiness(
	authService *auth.Service,
	profileService *profile.Service,
	privacyService *privacy.Service,
	analyticsService *analytics.Service,
	deviceService *device.Service,
	exportService *export.Service,
	mediaService *media.Service,
	searchService *search.Service,
	leaderboardService *leaderboard.Service,
	readinessChecker func(ctx context.Context) error,
) *Handlers {
	return &Handlers{
		auth:             authService,
		profile:          profileService,
		privacy:          privacyService,
		analytics:        analyticsService,
		device:           deviceService,
		export:           exportService,
		media:            mediaService,
		search:           searchService,
		leaderboard:      leaderboardService,
		readinessChecker: readinessChecker,
	}
}

// --- Health / Readiness ---

func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handlers) Readyz(w http.ResponseWriter, r *http.Request) {
	if h.readinessChecker != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := h.readinessChecker(ctx); err != nil {
			log.Printf("readiness check failed: %v", err)
			WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"status":    "not_ready",
				"errorCode": "DEPENDENCY_NOT_READY",
				"error":     "dependency unavailable",
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ready",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid request body: "+err.Error(), nil, err)
	}
	return nil
}

// --- Auth Handlers ---

type RegisterCodeRequest struct {
	Email  string `json:"email"`
	Locale string `json:"locale"`
}

func (h *Handlers) RequestRegisterCode(w http.ResponseWriter, r *http.Request) {
	var req RegisterCodeRequest
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	if err := h.auth.RequestRegistrationCode(r.Context(), req.Email, req.Locale); err != nil {
		WriteError(w, r, err)
		return
	}

	response := map[string]interface{}{
		"status":          "pending",
		"cooldownSeconds": 60,
	}
	if cfg := h.auth.Config(); cfg.Environment == "test" && cfg.TestAuthCode != "" {
		response["testCode"] = cfg.TestAuthCode
	}
	WriteJSON(w, http.StatusAccepted, response)
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
	ReturnTo string `json:"returnTo"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	result, err := h.auth.CompleteRegistration(r.Context(), req.Email, req.Code, req.Password, req.ReturnTo, req.Locale, req.Timezone)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	h.setSessionCookie(w, result.SessionToken, result.Session.AbsoluteExpiresAt)

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"user": map[string]interface{}{
			"userId":             result.User.UserID,
			"handle":             result.User.Handle,
			"displayName":        result.User.DisplayName,
			"locale":             result.User.Locale,
			"onboardingRequired": result.User.OnboardingCompletedAt == nil,
			"productState":       result.User.ProductState(),
		},
		"csrfToken": result.CSRFToken,
		"returnTo":  result.ReturnTo,
	})
}

type LoginRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	ReturnTo     string `json:"returnTo"`
	DeviceLabel  string `json:"deviceLabel"`
	KeepSignedIn *bool  `json:"keepSignedIn"`
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	keepSignedIn := true
	if req.KeepSignedIn != nil {
		keepSignedIn = *req.KeepSignedIn
	}

	result, err := h.auth.Login(r.Context(), req.Email, req.Password, req.ReturnTo, req.DeviceLabel, keepSignedIn)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	h.setSessionCookie(w, result.SessionToken, result.Session.AbsoluteExpiresAt)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"userId":             result.User.UserID,
			"handle":             result.User.Handle,
			"displayName":        result.User.DisplayName,
			"avatarUrl":          result.User.AvatarURL,
			"locale":             result.User.Locale,
			"onboardingRequired": result.User.OnboardingCompletedAt == nil,
			"productState":       result.User.ProductState(),
		},
		"csrfToken": result.CSRFToken,
		"returnTo":  result.ReturnTo,
	})
}

func (h *Handlers) GetSession(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	sess := GetSessionFromContext(r.Context())

	if user == nil || sess == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if sess.CSRFToken == "" {
		WriteError(w, r, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "session CSRF token unavailable", nil, nil))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"user": map[string]interface{}{
			"userId":             user.UserID,
			"handle":             user.Handle,
			"displayName":        user.DisplayName,
			"avatarUrl":          user.AvatarURL,
			"locale":             user.Locale,
			"onboardingRequired": user.OnboardingCompletedAt == nil,
			"productState":       user.ProductState(),
		},
		"csrfToken":         sess.CSRFToken,
		"idleExpiresAt":     sess.IdleExpiresAt.Format(time.RFC3339),
		"absoluteExpiresAt": sess.AbsoluteExpiresAt.Format(time.RFC3339),
	})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	sess := GetSessionFromContext(r.Context())
	if sess != nil {
		_ = h.auth.Logout(r.Context(), sess.SessionID)
	}

	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	sessions, err := h.auth.ListUserSessions(r.Context(), user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	type SessionItem struct {
		SessionID         string    `json:"sessionId"`
		DeviceLabel       *string   `json:"deviceLabel"`
		SessionStatus     string    `json:"sessionStatus"`
		LastSeenAt        time.Time `json:"lastSeenAt"`
		CreatedAt         time.Time `json:"createdAt"`
		AbsoluteExpiresAt time.Time `json:"absoluteExpiresAt"`
	}

	var items []SessionItem
	for _, s := range sessions {
		items = append(items, SessionItem{
			SessionID:         s.SessionID,
			DeviceLabel:       s.DeviceLabel,
			SessionStatus:     string(s.SessionStatus),
			LastSeenAt:        s.LastSeenAt,
			CreatedAt:         s.CreatedAt,
			AbsoluteExpiresAt: s.AbsoluteExpiresAt,
		})
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": items,
	})
}

func (h *Handlers) RevokeSessionByID(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	sessionID := chi.URLParam(r, "id")

	if err := h.auth.RevokeSessionByID(r.Context(), sessionID, user.UserID); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	sess := GetSessionFromContext(r.Context())

	if err := h.auth.RevokeOtherSessions(r.Context(), user.UserID, sess.SessionID); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type PasswordCodeRequest struct {
	Email string `json:"email"`
}

func (h *Handlers) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req PasswordCodeRequest
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	_ = h.auth.RequestPasswordReset(r.Context(), req.Email)
	WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":          "pending",
		"cooldownSeconds": 60,
	})
}

type PasswordResetRequest struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
}

func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req PasswordResetRequest
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	if err := h.auth.ResetPassword(r.Context(), req.Email, req.Code, req.NewPassword); err != nil {
		WriteError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Me / Profile / Privacy Handlers ---

func (h *Handlers) CompleteOnboarding(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var in profile.OnboardingInput
	if err := decodeJSON(w, r, 1024*1024, &in); err != nil {
		WriteError(w, r, err)
		return
	}

	u, p, err := h.profile.CompleteOnboarding(r.Context(), user.UserID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	returnTo := "/me"
	if in.ReturnTo != "" {
		returnTo = h.auth.SanitizeReturnTo(in.ReturnTo)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"user":     u,
		"privacy":  p,
		"returnTo": returnTo,
	})
}

func (h *Handlers) GetProfile(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	u, err := h.profile.GetProfile(r.Context(), user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	w.Header().Set("ETag", `"`+strconv.FormatUint(u.ProfileVersion, 10)+`"`)
	WriteJSON(w, http.StatusOK, u)
}

type UpdateProfileReq struct {
	DisplayName *string `json:"displayName,omitempty"`
	Handle      *string `json:"handle,omitempty"`
	Bio         *string `json:"bio,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	Locale      *string `json:"locale,omitempty"`
}

func (h *Handlers) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var req UpdateProfileReq
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	var expectedVersion uint64
	if match := r.Header.Get("If-Match"); match != "" {
		vStr := strings.Trim(match, `" `)
		val, err := strconv.ParseUint(vStr, 10, 64)
		if err != nil {
			WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidIfMatch", "invalid If-Match version header", nil, err))
			return
		}
		expectedVersion = val
	}

	in := profile.UpdateProfileInput{
		DisplayName:     req.DisplayName,
		Handle:          req.Handle,
		Bio:             req.Bio,
		Timezone:        req.Timezone,
		Locale:          req.Locale,
		ExpectedVersion: expectedVersion,
	}

	u, err := h.profile.UpdateProfile(r.Context(), user.UserID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	w.Header().Set("ETag", `"`+strconv.FormatUint(u.ProfileVersion, 10)+`"`)
	WriteJSON(w, http.StatusOK, u)
}

func (h *Handlers) GetPrivacy(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	p, err := h.privacy.GetPrivacy(r.Context(), user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	w.Header().Set("ETag", `"`+strconv.FormatUint(p.PrivacyVersion, 10)+`"`)
	WriteJSON(w, http.StatusOK, p)
}

type UpdatePrivacyReq struct {
	PublicProfileEnabled  bool                         `json:"publicProfileEnabled"`
	LeaderboardVisibility domain.LeaderboardVisibility `json:"leaderboardVisibility"`
	ShowBio               bool                         `json:"showBio"`
	ShowTokenTotal        bool                         `json:"showTokenTotal"`
	ShowTrends            bool                         `json:"showTrends"`
	ShowActivityCalendar  bool                         `json:"showActivityCalendar"`
	ShowAgentBreakdown    bool                         `json:"showAgentBreakdown"`
	ShowSkillRanking      bool                         `json:"showSkillRanking"`
	ShowAchievements      bool                         `json:"showAchievements"`
}

func (h *Handlers) UpdatePrivacy(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var req UpdatePrivacyReq
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	var expectedVersion uint64
	if match := r.Header.Get("If-Match"); match != "" {
		vStr := strings.Trim(match, `" `)
		val, err := strconv.ParseUint(vStr, 10, 64)
		if err != nil {
			WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidIfMatch", "invalid If-Match version header", nil, err))
			return
		}
		expectedVersion = val
	}

	in := privacy.UpdatePrivacyInput{
		PublicProfileEnabled:  req.PublicProfileEnabled,
		LeaderboardVisibility: req.LeaderboardVisibility,
		ShowBio:               req.ShowBio,
		ShowTokenTotal:        req.ShowTokenTotal,
		ShowTrends:            req.ShowTrends,
		ShowActivityCalendar:  req.ShowActivityCalendar,
		ShowAgentBreakdown:    req.ShowAgentBreakdown,
		ShowSkillRanking:      req.ShowSkillRanking,
		ShowAchievements:      req.ShowAchievements,
		ExpectedVersion:       expectedVersion,
	}

	p, err := h.privacy.UpdatePrivacy(r.Context(), user.UserID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	w.Header().Set("ETag", `"`+strconv.FormatUint(p.PrivacyVersion, 10)+`"`)
	WriteJSON(w, http.StatusOK, p)
}

func (h *Handlers) GetPublicPreview(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	preview, err := h.privacy.GetPublicPreview(r.Context(), user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, preview)
}

func (h *Handlers) CreateAvatarIntent(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var in media.CreateAvatarIntentInput
	if err := decodeJSON(w, r, 1024*1024, &in); err != nil {
		WriteError(w, r, err)
		return
	}

	res, err := h.media.CreateAvatarIntent(r.Context(), user.UserID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, res)
}

func (h *Handlers) CompleteAvatarIntent(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	objectID := chi.URLParam(r, "id")

	obj, err := h.media.CompleteAvatarIntent(r.Context(), objectID, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, obj)
}

func (h *Handlers) ClearAvatar(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if err := h.media.ClearAvatar(r.Context(), user.UserID); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Analytics Handlers ---

func (h *Handlers) GetSummary(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	summary, err := h.analytics.GetPersonalSummaryRange(r.Context(), user.UserID, rangeKey, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, summary)
}

func (h *Handlers) GetTokenTrends(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")
	mode := r.URL.Query().Get("mode")

	var agentID, providerID, modelID *string
	if v := r.URL.Query().Get("agent"); v != "" && v != "all" {
		agentID = &v
	}
	if v := r.URL.Query().Get("provider"); v != "" && v != "all" {
		providerID = &v
	}
	if v := r.URL.Query().Get("model"); v != "" && v != "all" {
		modelID = &v
	}

	trend, err := h.analytics.GetTokenTrendRange(r.Context(), user.UserID, rangeKey, r.URL.Query().Get("from"), r.URL.Query().Get("to"), mode, agentID, providerID, modelID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, trend)
}

func (h *Handlers) GetAgentBreakdowns(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetAgentBreakdownRange(r.Context(), user.UserID, rangeKey, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetModelBreakdowns(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetModelBreakdownRange(r.Context(), user.UserID, rangeKey, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetSkills(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetSkillRankingRange(r.Context(), user.UserID, rangeKey, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetCalendar(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetActivityCalendarRange(r.Context(), user.UserID, rangeKey, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetActivity(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidLimit", "limit must be an integer", nil, err))
			return
		}
		limit = parsed
	}
	resp, err := h.analytics.GetActivity(r.Context(), user.UserID,
		r.URL.Query().Get("range"), r.URL.Query().Get("from"), r.URL.Query().Get("to"),
		r.URL.Query().Get("agent"), r.URL.Query().Get("provider"), r.URL.Query().Get("model"),
		r.URL.Query().Get("cursor"), limit)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetFilterOptions(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	opts, err := h.analytics.GetFilterOptions(r.Context(), user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, opts)
}

// --- Devices Handlers ---

func (h *Handlers) ListDevices(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	devices, err := h.device.ListDevices(r.Context(), user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	type DeviceItem struct {
		InstallationID     string     `json:"installationId"`
		DeviceName         *string    `json:"deviceName"`
		OSType             string     `json:"osType"`
		OSVersion          *string    `json:"osVersion"`
		Architecture       string     `json:"architecture"`
		CollectorVersion   string     `json:"collectorVersion"`
		InstallationStatus string     `json:"installationStatus"`
		DisabledAt         *time.Time `json:"disabledAt,omitempty"`
		DisabledReason     *string    `json:"disabledReason,omitempty"`
		RegisteredAt       time.Time  `json:"registeredAt"`
		LastSeenAt         *time.Time `json:"lastSeenAt,omitempty"`
	}

	var items []DeviceItem
	for _, d := range devices {
		items = append(items, DeviceItem{
			InstallationID:     d.InstallationID,
			DeviceName:         d.DeviceName,
			OSType:             d.OSType,
			OSVersion:          d.OSVersion,
			Architecture:       d.Architecture,
			CollectorVersion:   d.CollectorVersion,
			InstallationStatus: string(d.InstallationStatus),
			DisabledAt:         d.DisabledAt,
			DisabledReason:     d.DisabledReason,
			RegisteredAt:       d.RegisteredAt,
			LastSeenAt:         d.LastSeenAt,
		})
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"devices": items,
	})
}

func (h *Handlers) CreateDeviceBinding(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	sess := GetSessionFromContext(r.Context())

	res, err := h.device.CreateBindingChallenge(r.Context(), user.UserID, sess.SessionID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, res)
}

func (h *Handlers) CancelDeviceBinding(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.device.CancelBindingChallenge(r.Context(), id, user.UserID); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type UpdateDeviceReq struct {
	DeviceName string `json:"deviceName"`
}

func (h *Handlers) UpdateDevice(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	var req UpdateDeviceReq
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	inst, err := h.device.UpdateDeviceName(r.Context(), id, user.UserID, req.DeviceName)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, inst)
}

func (h *Handlers) PauseDevice(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	inst, err := h.device.PauseDevice(r.Context(), id, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, inst)
}

func (h *Handlers) ResumeDevice(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	inst, err := h.device.ResumeDevice(r.Context(), id, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, inst)
}

func (h *Handlers) RevokeDevice(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	inst, err := h.device.RevokeDevice(r.Context(), id, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, inst)
}

func (h *Handlers) CreateDeviceGrant(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	sess := GetSessionFromContext(r.Context())
	if user == nil || sess == nil {
		WriteError(w, r, domain.NewAppError(401, "AUTH_REQUIRED", "auth.required", "authentication required", nil, domain.ErrUnauthorized))
		return
	}

	grant, err := h.device.CreateDeviceGrant(r.Context(), user.UserID, sess.SessionID)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusCreated, grant)
}

// --- Collector /v1 Handlers ---

func (h *Handlers) ClaimInstallation(w http.ResponseWriter, r *http.Request) {
	var in device.ClaimInput
	if err := decodeJSON(w, r, 1024*1024, &in); err != nil {
		WriteError(w, r, err)
		return
	}

	inst, err := h.device.ClaimInstallation(r.Context(), in)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"installationId": inst.InstallationID,
		"status":         inst.InstallationStatus,
		"uploadPolicy": map[string]interface{}{
			"maxBatchEvents": 1000,
			"minIntervalSec": 10,
		},
	})
}

func (h *Handlers) RegisterInstallation(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		WriteError(w, r, domain.NewAppError(401, "AUTH_REQUIRED", "auth.grantRequired", "scoped grant required (Bearer dgt_...); web session bearer not permitted", nil, domain.ErrUnauthorized))
		return
	}
	grantToken := strings.TrimPrefix(authHeader, "Bearer ")
	if !strings.HasPrefix(grantToken, "dgt_") {
		WriteError(w, r, domain.NewAppError(401, "AUTH_INVALID_GRANT_SCOPE", "auth.invalidGrantScope", "scoped grant required (dgt_...); web session bearer not permitted", nil, domain.ErrUnauthorized))
		return
	}

	userID, _, err := h.device.ValidateDeviceGrant(r.Context(), grantToken)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	var in device.ClaimInput
	if err := decodeJSON(w, r, 1024*1024, &in); err != nil {
		WriteError(w, r, err)
		return
	}

	inst, err := h.device.RegisterInstallation(r.Context(), userID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"installationId": inst.InstallationID,
		"status":         inst.InstallationStatus,
		"uploadPolicy": map[string]interface{}{
			"maxBatchEvents": 1000,
			"minIntervalSec": 10,
		},
	})
}

const (
	maxTelemetryBodyBytes = 512 * 1024
	maxTelemetryEvents    = 1000
	telemetryClockSkew    = 5 * time.Minute
	telemetryNonceTTL     = 10 * time.Minute
)

type TelemetryBatchInput struct {
	BatchID string                `json:"batchId,omitempty"`
	Events  []TelemetryEventInput `json:"events"`
}

type TelemetryEventInput struct {
	EventID              string             `json:"eventId"`
	SchemaVersion        uint16             `json:"schemaVersion"`
	AdapterID            string             `json:"adapterId"`
	AdapterVersion       string             `json:"adapterVersion"`
	AgentID              string             `json:"agentId"`
	AgentVersion         *string            `json:"agentVersion,omitempty"`
	ProviderID           *string            `json:"providerId,omitempty"`
	ModelID              *string            `json:"modelId,omitempty"`
	EventType            string             `json:"eventType"`
	Accuracy             string             `json:"accuracy"`
	SourceKind           string             `json:"sourceKind"`
	OccurredAt           string             `json:"occurredAt"`
	SessionHash          *string            `json:"sessionHash,omitempty"`
	ParentSessionHash    *string            `json:"parentSessionHash,omitempty"`
	TurnHash             *string            `json:"turnHash,omitempty"`
	TurnTrigger          *string            `json:"turnTrigger,omitempty"`
	ToolCallHash         *string            `json:"toolCallHash,omitempty"`
	TokenInput           *uint64            `json:"tokenInput,omitempty"`
	TokenOutput          *uint64            `json:"tokenOutput,omitempty"`
	TokenCacheRead       *uint64            `json:"tokenCacheRead,omitempty"`
	TokenCacheWrite      *uint64            `json:"tokenCacheWrite,omitempty"`
	TokenReasoning       *uint64            `json:"tokenReasoning,omitempty"`
	TokenTotal           *uint64            `json:"tokenTotal,omitempty"`
	DurationMS           *uint64            `json:"durationMs,omitempty"`
	Success              *bool              `json:"success,omitempty"`
	ToolCategory         *string            `json:"toolCategory,omitempty"`
	SkillKey             *string            `json:"skillKey,omitempty"`
	SkillPublicName      *string            `json:"skillPublicName,omitempty"`
	SkillInvokeType      *string            `json:"skillInvokeType,omitempty"`
	PluginKey            *string            `json:"pluginKey,omitempty"`
	CodeGeneratedLines   *uint64            `json:"codeGeneratedLines,omitempty"`
	CodeAcceptedLines    *uint64            `json:"codeAcceptedLines,omitempty"`
	CodeAddedLines       *uint64            `json:"codeAddedLines,omitempty"`
	CodeDeletedLines     *uint64            `json:"codeDeletedLines,omitempty"`
	CodeFileCount        *uint32            `json:"codeFileCount,omitempty"`
	CostAmount           *string            `json:"costAmount,omitempty"`
	CostCurrency         *string            `json:"costCurrency,omitempty"`
	CostSource           *string            `json:"costSource,omitempty"`
	PrivacyPolicyVersion uint16             `json:"privacyPolicyVersion"`
	Metadata             telemetry.Metadata `json:"metadata,omitempty"`
}

type telemetryRejection struct {
	EventID string `json:"eventId,omitempty"`
	Code    string `json:"code"`
}

func (h *Handlers) IngestTelemetry(w http.ResponseWriter, r *http.Request) {
	installationID, signature, err := parseDeviceAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		WriteError(w, r, err)
		return
	}

	timestampValue := strings.TrimSpace(r.Header.Get("X-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-Nonce"))
	bodyHashValue := strings.TrimSpace(r.Header.Get("X-Body-SHA256"))
	requestTime, err := validateTelemetryHeaders(timestampValue, nonce, bodyHashValue, time.Now().UTC())
	if err != nil {
		WriteError(w, r, err)
		return
	}

	rawBody, body, err := readTelemetryBody(w, r)
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
	canonical := telemetryCanonicalRequest(r.Method, r.URL.EscapedPath(), timestampValue, nonce, hex.EncodeToString(requestHash[:]))
	if !ed25519.Verify(ed25519.PublicKey(inst.DevicePublicKey[:]), []byte(canonical), signature) {
		WriteError(w, r, domain.NewAppError(401, "DEVICE_SIGNATURE_INVALID", "device.invalidSignature", "invalid device request signature", nil, domain.ErrUnauthorized))
		return
	}

	var in TelemetryBatchInput
	if err := decodeTelemetryJSON(body, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	if len(in.Events) == 0 || len(in.Events) > maxTelemetryEvents {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "ingest.invalidBatchSize", "batch must contain between 1 and 1000 events", nil, domain.ErrInvalidArgument))
		return
	}

	batchID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if batchID == "" {
		batchID = strings.TrimSpace(in.BatchID)
	} else if in.BatchID != "" && batchID != in.BatchID {
		WriteError(w, r, domain.NewAppError(400, "INGEST_BATCH_ID_MISMATCH", "ingest.batchIdMismatch", "Idempotency-Key and batchId must match", nil, domain.ErrInvalidArgument))
		return
	}
	if !validTelemetryID(batchID, "bat_") {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "ingest.invalidBatchId", "a valid batch id is required in Idempotency-Key or batchId", nil, domain.ErrInvalidArgument))
		return
	}

	events := make([]domain.UsageEvent, 0, len(in.Events))
	rejected := make([]telemetryRejection, 0)
	for i := range in.Events {
		event, code := normalizeTelemetryEvent(&in.Events[i])
		if code != "" {
			rejected = append(rejected, telemetryRejection{EventID: in.Events[i].EventID, Code: code})
			continue
		}
		events = append(events, *event)
	}

	now := time.Now().UTC()
	nonceHash := stdsha256.Sum256([]byte(nonce))
	result, err := h.device.CommitIngest(r.Context(), domain.IngestBatch{
		BatchID:        batchID,
		InstallationID: installationID,
		RequestSHA256:  requestHash,
		NonceHash:      nonceHash,
		NonceExpiresAt: requestTime.Add(telemetryNonceTTL),
		EventCount:     uint32(len(in.Events)),
		RejectedCount:  uint32(len(rejected)),
		Events:         events,
		ReceivedAt:     now,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"batchId":        result.BatchID,
		"installationId": installationID,
		"accepted":       result.AcceptedCount,
		"duplicates":     result.DuplicateCount,
		"rejected":       rejected,
		"serverTime":     now.Format(time.RFC3339Nano),
	})
}

func parseDeviceAuthorization(value string) (string, []byte, error) {
	if !strings.HasPrefix(value, "Device ") {
		return "", nil, domain.NewAppError(401, "AUTH_REQUIRED", "auth.required", "Authorization: Device <installation-id>:<signature> is required", nil, domain.ErrUnauthorized)
	}
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(value, "Device ")), ":")
	if len(parts) != 2 || !validTelemetryID(parts[0], "ins_") {
		return "", nil, domain.NewAppError(401, "DEVICE_AUTH_INVALID", "device.invalidSignature", "invalid device authorization format", nil, domain.ErrUnauthorized)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return "", nil, domain.NewAppError(401, "DEVICE_AUTH_INVALID", "device.invalidSignature", "invalid device signature encoding", nil, domain.ErrUnauthorized)
	}
	return parts[0], signature, nil
}

func validateTelemetryHeaders(timestampValue, nonce, bodyHash string, now time.Time) (time.Time, error) {
	requestTime, err := time.Parse(time.RFC3339Nano, timestampValue)
	if err != nil {
		return time.Time{}, domain.NewAppError(400, "INGEST_TIMESTAMP_INVALID", "ingest.invalidTimestamp", "X-Timestamp must be an RFC3339 timestamp", nil, domain.ErrInvalidArgument)
	}
	if requestTime.Before(now.Add(-telemetryClockSkew)) || requestTime.After(now.Add(telemetryClockSkew)) {
		return time.Time{}, domain.NewAppError(401, "INGEST_TIMESTAMP_EXPIRED", "ingest.timestampExpired", "telemetry timestamp is outside the allowed clock skew", nil, domain.ErrUnauthorized)
	}
	if len(nonce) < 16 || len(nonce) > 128 || strings.ContainsAny(nonce, "\r\n") {
		return time.Time{}, domain.NewAppError(400, "INGEST_NONCE_INVALID", "ingest.invalidNonce", "X-Nonce must contain 16 to 128 characters", nil, domain.ErrInvalidArgument)
	}
	if len(bodyHash) != stdsha256.Size*2 {
		return time.Time{}, domain.NewAppError(400, "INGEST_BODY_HASH_INVALID", "ingest.invalidBodyHash", "X-Body-SHA256 must be a 64-character hexadecimal SHA-256", nil, domain.ErrInvalidArgument)
	}
	return requestTime.UTC(), nil
}

func telemetryCanonicalRequest(method, path, timestamp, nonce, bodyHash string) string {
	if path == "" {
		path = "/"
	}
	return strings.ToUpper(method) + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + strings.ToLower(bodyHash)
}

func readTelemetryBody(w http.ResponseWriter, r *http.Request) ([]byte, []byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTelemetryBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, nil, domain.NewAppError(413, "INGEST_BODY_TOO_LARGE", "ingest.bodyTooLarge", "telemetry request body exceeds 512 KiB", nil, domain.ErrInvalidArgument)
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
	decoded, err := io.ReadAll(io.LimitReader(zr, maxTelemetryBodyBytes+1))
	if err != nil || len(decoded) > maxTelemetryBodyBytes {
		return nil, nil, domain.NewAppError(413, "INGEST_BODY_TOO_LARGE", "ingest.bodyTooLarge", "decoded telemetry request body exceeds 512 KiB", nil, domain.ErrInvalidArgument)
	}
	return raw, decoded, nil
}

func decodeTelemetryJSON(body []byte, dst *TelemetryBatchInput) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid telemetry request body: "+err.Error(), nil, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "telemetry request body must contain exactly one JSON value", nil, domain.ErrInvalidArgument)
	}
	return nil
}

func normalizeTelemetryEvent(in *TelemetryEventInput) (*domain.UsageEvent, string) {
	eventID, ok := decodeTelemetryHash(in.EventID)
	if !ok {
		return nil, "INVALID_EVENT_ID"
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if err != nil {
		return nil, "INVALID_OCCURRED_AT"
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	if in.PrivacyPolicyVersion == 0 {
		in.PrivacyPolicyVersion = 1
	}
	if in.AdapterID == "" || in.AdapterVersion == "" || in.AgentID == "" {
		return nil, "MISSING_EVENT_SOURCE"
	}
	if !telemetry.ValidIdentifier(in.AdapterID, 64) || !telemetry.ValidVersion(in.AdapterVersion) || !telemetry.ValidIdentifier(in.AgentID, 64) {
		return nil, "INVALID_EVENT_SOURCE"
	}
	if !validOptionalTelemetryVersion(in.AgentVersion) || !validOptionalTelemetryIdentifier(in.ProviderID, 64) || !validOptionalTelemetryIdentifier(in.ModelID, 128) {
		return nil, "INVALID_EVENT_SOURCE"
	}
	if !validOptionalTelemetryIdentifier(in.ToolCategory, 64) || !validOptionalTelemetryIdentifier(in.SkillPublicName, 120) {
		return nil, "INVALID_EVENT_CLASSIFICATION"
	}
	if in.SkillInvokeType != nil && !telemetry.ValidSkillInvokeType(*in.SkillInvokeType) {
		return nil, "INVALID_EVENT_CLASSIFICATION"
	}
	if in.CostAmount != nil && !telemetry.ValidCostAmount(*in.CostAmount) {
		return nil, "INVALID_COST"
	}
	if in.CostCurrency != nil && !telemetry.ValidCurrency(*in.CostCurrency) {
		return nil, "INVALID_COST"
	}
	if in.CostSource != nil && !telemetry.ValidCostSource(*in.CostSource) {
		return nil, "INVALID_COST"
	}
	if !validTelemetryEventType(in.EventType) {
		return nil, "INVALID_EVENT_TYPE"
	}
	if !validTelemetryAccuracy(in.Accuracy) || !validTelemetrySourceKind(in.SourceKind) {
		return nil, "INVALID_EVENT_CLASSIFICATION"
	}
	if in.TurnTrigger != nil {
		switch *in.TurnTrigger {
		case "user", "system", "automation", "resume", "unknown":
		default:
			return nil, "INVALID_EVENT_CLASSIFICATION"
		}
	}
	for _, value := range []*string{in.SessionHash, in.ParentSessionHash, in.TurnHash, in.ToolCallHash, in.SkillKey, in.PluginKey} {
		if value != nil {
			if _, ok := decodeTelemetryHash(*value); !ok {
				return nil, "INVALID_HASH_FIELD"
			}
		}
	}
	metadata, code := telemetry.NormalizeMetadata(in.EventType, in.Metadata)
	if code != "" {
		return nil, code
	}
	return &domain.UsageEvent{
		EventID: eventID, SchemaVersion: in.SchemaVersion, AdapterID: in.AdapterID, AdapterVersion: in.AdapterVersion,
		AgentID: in.AgentID, AgentVersion: in.AgentVersion, ProviderID: in.ProviderID, ModelID: in.ModelID,
		EventType: in.EventType, Accuracy: in.Accuracy, SourceKind: in.SourceKind, OccurredAt: occurredAt.UTC(),
		SessionHash: decodeOptionalTelemetryHash(in.SessionHash), ParentSessionHash: decodeOptionalTelemetryHash(in.ParentSessionHash),
		TurnHash: decodeOptionalTelemetryHash(in.TurnHash), TurnTrigger: in.TurnTrigger, ToolCallHash: decodeOptionalTelemetryHash(in.ToolCallHash),
		TokenInput: in.TokenInput, TokenOutput: in.TokenOutput, TokenCacheRead: in.TokenCacheRead, TokenCacheWrite: in.TokenCacheWrite,
		TokenReasoning: in.TokenReasoning, TokenTotal: in.TokenTotal, DurationMS: in.DurationMS, Success: in.Success,
		ToolCategory: in.ToolCategory, SkillKey: decodeOptionalTelemetryHash(in.SkillKey), SkillPublicName: in.SkillPublicName,
		SkillInvokeType: in.SkillInvokeType, PluginKey: decodeOptionalTelemetryHash(in.PluginKey), CodeGeneratedLines: in.CodeGeneratedLines,
		CodeAcceptedLines: in.CodeAcceptedLines, CodeAddedLines: in.CodeAddedLines, CodeDeletedLines: in.CodeDeletedLines,
		CodeFileCount: in.CodeFileCount, CostAmount: in.CostAmount, CostCurrency: in.CostCurrency, CostSource: in.CostSource,
		PrivacyPolicyVersion: in.PrivacyPolicyVersion, SafeExtensionJSON: metadata,
	}, ""
}

func decodeTelemetryHash(value string) ([32]byte, bool) {
	var result [32]byte
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "sha256:"), "hmac-sha256:"))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(result) {
		decoded, err = base64.RawURLEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) != len(result) {
		return result, false
	}
	copy(result[:], decoded)
	return result, true
}

func decodeOptionalTelemetryHash(value *string) *[32]byte {
	if value == nil {
		return nil
	}
	decoded, ok := decodeTelemetryHash(*value)
	if !ok {
		return nil
	}
	return &decoded
}

func validOptionalTelemetryIdentifier(value *string, maxBytes int) bool {
	return value == nil || telemetry.ValidIdentifier(*value, maxBytes)
}

func validOptionalTelemetryVersion(value *string) bool {
	return value == nil || telemetry.ValidVersion(*value)
}

func validTelemetryID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) > 30 || len(value) <= len(prefix) {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' && ch != '-' {
			return false
		}
	}
	return true
}

func validTelemetryEventType(value string) bool {
	switch value {
	case "session_started", "session_ended", "turn_started", "turn_completed", "model_usage_recorded", "tool_invoked", "skill_invoked", "code_changed", "cost_recorded", "agent_spawned":
		return true
	default:
		return false
	}
}

func validTelemetryAccuracy(value string) bool {
	return value == "exact" || value == "derived" || value == "estimated" || value == "correlated"
}

func validTelemetrySourceKind(value string) bool {
	switch value {
	case "otlp", "jsonl", "sqlite_snapshot", "file_snapshot", "runtime_stream", "local_http_api", "command_snapshot", "remote_api":
		return true
	default:
		return false
	}
}

// --- Export & Deletion Handlers ---

type CreateExportReq struct {
	Scope  string                 `json:"scope"`
	Format string                 `json:"format"`
	Filter map[string]interface{} `json:"filter,omitempty"`
}

func (h *Handlers) CreateExport(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	idempKey := r.Header.Get("Idempotency-Key")

	var req CreateExportReq
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	in := export.CreateExportInput{
		IdempotencyKey: idempKey,
		Scope:          req.Scope,
		Format:         req.Format,
		Filter:         req.Filter,
	}

	job, err := h.export.CreateJob(r.Context(), user.UserID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, job)
}

func (h *Handlers) ListExports(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	jobs, err := h.export.ListJobs(r.Context(), user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"exports": jobs,
	})
}

func (h *Handlers) GetExport(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	job, err := h.export.GetJob(r.Context(), id, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, job)
}

func (h *Handlers) DownloadExport(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	dl, err := h.export.GetDownloadURL(r.Context(), id, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, dl)
}

type CreateDeletionReq struct {
	Scope          string     `json:"scope"`
	Confirmation   bool       `json:"confirmation"`
	InstallationID string     `json:"installationId"`
	From           *time.Time `json:"from"`
	To             *time.Time `json:"to"`
}

func (h *Handlers) RequestDeletion(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var req CreateDeletionReq
	if err := decodeJSON(w, r, 1024*1024, &req); err != nil {
		WriteError(w, r, err)
		return
	}

	delReq, err := h.privacy.RequestDeletionWithFilter(r.Context(), user.UserID, privacy.DeletionRequestInput{
		Scope:          req.Scope,
		Confirmation:   req.Confirmation,
		InstallationID: req.InstallationID,
		From:           req.From,
		To:             req.To,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, delReq)
}

func (h *Handlers) GetDeletion(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	delReq, err := h.privacy.GetDeletionRequest(r.Context(), id, user.UserID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, delReq)
}

func (h *Handlers) CancelDeletion(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.privacy.CancelDeletion(r.Context(), id, user.UserID); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status": "cancelled",
	})
}

// --- Public Handlers ---

func (h *Handlers) GetPublicProfile(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	pub, redirectHandle, err := h.privacy.GetPublicProfileByHandle(r.Context(), handle)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	if redirectHandle != "" {
		http.Redirect(w, r, "/api/v1/public/users/"+redirectHandle, http.StatusPermanentRedirect) // 308
		return
	}

	w.Header().Set("ETag", `"`+strconv.FormatUint(pub.ProjectionVersion, 10)+`"`)
	now := time.Now().UTC()

	var bioPtr *string
	if pub.ShowBio && pub.Bio != nil {
		bioPtr = pub.Bio
	}

	dto := domain.PublicProfileDTO{
		Handle:               pub.Handle,
		DisplayName:          pub.DisplayName,
		AvatarURL:            pub.AvatarURL,
		Bio:                  bioPtr,
		GeneratedAt:          now,
		ProjectionVersion:    pub.ProjectionVersion,
		ShowBio:              pub.ShowBio,
		ShowTokenTotal:       pub.ShowTokenTotal,
		ShowTrends:           pub.ShowTrends,
		ShowActivityCalendar: pub.ShowActivityCalendar,
		ShowAgentBreakdown:   pub.ShowAgentBreakdown,
		ShowSkillRanking:     pub.ShowSkillRanking,
		ShowAchievements:     pub.ShowAchievements,
	}

	if pub.ShowTokenTotal {
		sum, errSum := h.analytics.GetPersonalSummary(r.Context(), pub.UserID, "all")
		if errSum == nil && sum != nil {
			dto.TokenTotal = sum.Metrics.TotalTokens.Value
			dto.DataWatermarkAt = sum.DataWatermarkAt
			if pub.ShowAchievements && sum.Ranking.Rank != nil {
				dto.Rank = sum.Ranking.Rank
				dto.RankDelta = sum.Ranking.Delta
				dto.Percentile = sum.Ranking.Percentile
			}
		}
	}

	if pub.ShowActivityCalendar {
		cal, errCal := h.analytics.GetActivityCalendar(r.Context(), pub.UserID, "30d")
		if errCal == nil && cal != nil {
			dto.ActiveDays = &cal.TotalActiveDays
			dto.CurrentStreak = &cal.CurrentStreak
		}
	}

	WriteJSON(w, http.StatusOK, dto)
}

func (h *Handlers) GetPublicTrends(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	pub, _, err := h.privacy.GetPublicProfileByHandle(r.Context(), handle)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	if !pub.ShowTrends {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"visible": false,
		})
		return
	}

	rangeKey := r.URL.Query().Get("range")
	mode := r.URL.Query().Get("mode")

	var agentID, providerID, modelID *string
	if v := r.URL.Query().Get("agent"); v != "" && v != "all" {
		agentID = &v
	}
	if v := r.URL.Query().Get("provider"); v != "" && v != "all" {
		providerID = &v
	}
	if v := r.URL.Query().Get("model"); v != "" && v != "all" {
		modelID = &v
	}

	trend, err := h.analytics.GetTokenTrend(r.Context(), pub.UserID, rangeKey, mode, agentID, providerID, modelID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, trend)
}

func (h *Handlers) GetPublicSkills(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	pub, _, err := h.privacy.GetPublicProfileByHandle(r.Context(), handle)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	if !pub.ShowSkillRanking {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"visible": false,
		})
		return
	}

	rangeKey := r.URL.Query().Get("range")
	skills, err := h.analytics.GetSkillRanking(r.Context(), pub.UserID, rangeKey)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, skills)
}

func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
		limit = val
	}

	res, err := h.search.Search(r.Context(), q, limit)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

func (h *Handlers) GetLeaderboards(w http.ResponseWriter, r *http.Request) {
	boardKey := r.URL.Query().Get("board")
	window := r.URL.Query().Get("window")
	metric := r.URL.Query().Get("metric")
	cursor := r.URL.Query().Get("cursor")
	var curPtr *string
	if cursor != "" {
		curPtr = &cursor
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}

	res, err := h.leaderboard.GetLeaderboards(r.Context(), boardKey, window, metric, curPtr, limit)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

func (h *Handlers) CompareUsers(w http.ResponseWriter, r *http.Request) {
	handlesStr := r.URL.Query().Get("handles")
	rangeKey := r.URL.Query().Get("range")
	if rangeKey == "" {
		rangeKey = "30d"
	}
	rawHandles := strings.Split(handlesStr, ",")
	var handles []string
	for _, hName := range rawHandles {
		hName = strings.TrimSpace(hName)
		if hName != "" {
			handles = append(handles, hName)
		}
	}
	if len(handles) > 3 {
		handles = handles[:3]
	}

	var results []domain.CompareUserItem
	now := time.Now().UTC()
	for _, hName := range handles {
		pub, _, err := h.privacy.GetPublicProfileByHandle(r.Context(), hName)
		if err != nil || pub == nil || pub.ProfileStatus != domain.ProfileStatusPublished {
			results = append(results, domain.CompareUserItem{
				Handle:  hName,
				Visible: false,
			})
			continue
		}

		item := domain.CompareUserItem{
			Handle:      pub.Handle,
			DisplayName: &pub.DisplayName,
			AvatarURL:   pub.AvatarURL,
			Visible:     true,
		}

		if pub.ShowTokenTotal {
			sum, errSum := h.analytics.GetPersonalSummary(r.Context(), pub.UserID, rangeKey)
			if errSum == nil && sum != nil {
				item.TokenTotal = sum.Metrics.TotalTokens.Value
				item.DataWatermarkAt = sum.DataWatermarkAt
				if sum.Metrics.GeneratedCodeLines.Value != nil {
					item.CodeLinesTotal = sum.Metrics.GeneratedCodeLines.Value
				}
				if pub.ShowAchievements && sum.Ranking.Rank != nil {
					item.Rank = sum.Ranking.Rank
					item.Percentile = sum.Ranking.Percentile
				}
			}
		}

		if pub.ShowAgentBreakdown {
			ab, errAb := h.analytics.GetAgentBreakdown(r.Context(), pub.UserID, rangeKey)
			if errAb == nil && ab != nil && len(ab.Items) > 0 {
				item.AgentBreakdown = ab.Items
			}
		}

		if pub.ShowSkillRanking {
			sk, errSk := h.analytics.GetSkillRanking(r.Context(), pub.UserID, rangeKey)
			if errSk == nil && sk != nil && len(sk.Skills) > 0 {
				item.SkillRanking = sk.Skills
			}
		}

		if pub.ShowActivityCalendar {
			cal, errCal := h.analytics.GetActivityCalendar(r.Context(), pub.UserID, rangeKey)
			if errCal == nil && cal != nil {
				item.CurrentStreak = &cal.CurrentStreak
				item.ActiveDays = &cal.TotalActiveDays
			}
		}

		results = append(results, item)
	}

	WriteJSON(w, http.StatusOK, domain.CompareResponse{
		Users:       results,
		GeneratedAt: now,
	})
}

// --- Cookie Helper ---

func sessionCookieMaxAge(expiresAt time.Time) int {
	seconds := int(time.Until(expiresAt).Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (h *Handlers) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	maxAge := sessionCookieMaxAge(expiresAt)
	if h.auth.IsProduction() {
		http.SetCookie(w, &http.Cookie{
			Name:     SessionCookieName,
			Value:    token,
			Path:     "/",
			Expires:  expiresAt,
			MaxAge:   maxAge,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   true,
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     DevSessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
}

func (h *Handlers) clearSessionCookie(w http.ResponseWriter) {
	if h.auth.IsProduction() {
		http.SetCookie(w, &http.Cookie{
			Name:     SessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   true,
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     DevSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
}
