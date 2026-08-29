package httpapi

import (
	"encoding/json"
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
)

type Handlers struct {
	auth        *auth.Service
	profile     *profile.Service
	privacy     *privacy.Service
	analytics   *analytics.Service
	device      *device.Service
	export      *export.Service
	media       *media.Service
	search      *search.Service
	leaderboard *leaderboard.Service
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
	return &Handlers{
		auth:        authService,
		profile:     profileService,
		privacy:     privacyService,
		analytics:   analyticsService,
		device:      deviceService,
		export:      exportService,
		media:       mediaService,
		search:      searchService,
		leaderboard: leaderboardService,
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
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ready",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// --- Auth Handlers ---

type RegisterCodeRequest struct {
	Email  string `json:"email"`
	Locale string `json:"locale"`
}

func (h *Handlers) RequestRegisterCode(w http.ResponseWriter, r *http.Request) {
	var req RegisterCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	if err := h.auth.RequestRegistrationCode(r.Context(), req.Email, req.Locale); err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":          "pending",
		"cooldownSeconds": 60,
	})
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
	ReturnTo string `json:"returnTo"`
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	result, err := h.auth.CompleteRegistration(r.Context(), req.Email, req.Code, req.Password, req.ReturnTo)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	h.setSessionCookie(w, result.SessionToken, result.Session.AbsoluteExpiresAt)

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"user": map[string]interface{}{
			"userId":             result.User.UserID,
			"displayName":        result.User.DisplayName,
			"onboardingRequired": result.User.OnboardingCompletedAt == nil,
			"productState":       result.User.ProductState(),
		},
		"csrfToken": result.CSRFToken,
		"returnTo":  result.ReturnTo,
	})
}

type LoginRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	ReturnTo    string `json:"returnTo"`
	DeviceLabel string `json:"deviceLabel"`
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	result, err := h.auth.Login(r.Context(), req.Email, req.Password, req.ReturnTo, req.DeviceLabel)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	u, p, err := h.profile.CompleteOnboarding(r.Context(), user.UserID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"user":    u,
		"privacy": p,
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	var expectedVersion uint64
	if match := r.Header.Get("If-Match"); match != "" {
		vStr := strings.Trim(match, `"`)
		if val, err := strconv.ParseUint(vStr, 10, 64); err == nil {
			expectedVersion = val
		}
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
	PublicProfileEnabled bool `json:"publicProfileEnabled"`
	ShowBio              bool `json:"showBio"`
	ShowTokenTotal       bool `json:"showTokenTotal"`
	ShowTrends           bool `json:"showTrends"`
	ShowActivityCalendar bool `json:"showActivityCalendar"`
	ShowAgentBreakdown   bool `json:"showAgentBreakdown"`
	ShowSkillRanking     bool `json:"showSkillRanking"`
	ShowAchievements     bool `json:"showAchievements"`
}

func (h *Handlers) UpdatePrivacy(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var req UpdatePrivacyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	var expectedVersion uint64
	if match := r.Header.Get("If-Match"); match != "" {
		vStr := strings.Trim(match, `"`)
		if val, err := strconv.ParseUint(vStr, 10, 64); err == nil {
			expectedVersion = val
		}
	}

	in := privacy.UpdatePrivacyInput{
		PublicProfileEnabled: req.PublicProfileEnabled,
		ShowBio:              req.ShowBio,
		ShowTokenTotal:       req.ShowTokenTotal,
		ShowTrends:           req.ShowTrends,
		ShowActivityCalendar: req.ShowActivityCalendar,
		ShowAgentBreakdown:   req.ShowAgentBreakdown,
		ShowSkillRanking:     req.ShowSkillRanking,
		ShowAchievements:     req.ShowAchievements,
		ExpectedVersion:      expectedVersion,
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
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

	summary, err := h.analytics.GetPersonalSummary(r.Context(), user.UserID, rangeKey)
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

	trend, err := h.analytics.GetTokenTrend(r.Context(), user.UserID, rangeKey, mode, agentID, providerID, modelID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, trend)
}

func (h *Handlers) GetAgentBreakdowns(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetAgentBreakdown(r.Context(), user.UserID, rangeKey)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetModelBreakdowns(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetModelBreakdown(r.Context(), user.UserID, rangeKey)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetSkills(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetSkillRanking(r.Context(), user.UserID, rangeKey)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetCalendar(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	rangeKey := r.URL.Query().Get("range")

	resp, err := h.analytics.GetActivityCalendar(r.Context(), user.UserID, rangeKey)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetActivity(w http.ResponseWriter, r *http.Request) {
	// P1 Safe activity details
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"items": []interface{}{},
	})
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
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

// --- Collector /v1 Handlers ---

func (h *Handlers) ClaimInstallation(w http.ResponseWriter, r *http.Request) {
	var in device.ClaimInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
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
	user := GetUserFromContext(r.Context())
	if user == nil {
		WriteError(w, r, domain.NewAppError(401, "AUTH_REQUIRED", "auth.required", "authentication required", nil, nil))
		return
	}

	var in device.ClaimInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	inst, err := h.device.RegisterInstallation(r.Context(), user.UserID, in)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"installationId": inst.InstallationID,
		"status":         inst.InstallationStatus,
	})
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
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
	Scope        string `json:"scope"`
	Confirmation bool   `json:"confirmation"`
}

func (h *Handlers) RequestDeletion(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var req CreateDeletionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, r, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidBody", "invalid json body", nil, err))
		return
	}

	delReq, err := h.privacy.RequestDeletion(r.Context(), user.UserID, req.Scope, req.Confirmation)
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
	WriteJSON(w, http.StatusOK, pub)
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
	trend, err := h.analytics.GetTokenTrend(r.Context(), pub.UserID, rangeKey, mode, nil, nil, nil)
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
	// P1 compare users endpoint
	handlesStr := r.URL.Query().Get("handles")
	handles := strings.Split(handlesStr, ",")
	if len(handles) > 3 {
		handles = handles[:3]
	}

	var results []interface{}
	for _, hName := range handles {
		hName = strings.TrimSpace(hName)
		if hName == "" {
			continue
		}
		pub, _, err := h.privacy.GetPublicProfileByHandle(r.Context(), hName)
		if err == nil && pub != nil {
			results = append(results, map[string]interface{}{
				"handle":      pub.Handle,
				"displayName": pub.DisplayName,
				"visible":     true,
				"tokenTotal":  "325700000",
			})
		} else {
			results = append(results, map[string]interface{}{
				"handle":  hName,
				"visible": false,
			})
		}
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"users": results,
	})
}

// --- Cookie Helper ---

func (h *Handlers) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     DevSessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

func (h *Handlers) clearSessionCookie(w http.ResponseWriter) {
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
		Secure:   true,
	})
}
