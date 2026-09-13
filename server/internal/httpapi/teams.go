package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"tokendance/internal/domain"
	"tokendance/internal/teams"
)

const (
	teamCacheControl = "private, no-store"
	teamJSONMaxBytes = 16 * 1024
)

func writeTeamJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Cache-Control", teamCacheControl)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func writeTeamError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", teamCacheControl)
	WriteError(w, r, err)
}

func (h *Handlers) teamsOrUnavailable(w http.ResponseWriter, r *http.Request) *teams.Service {
	if h.teams == nil {
		writeTeamError(w, r, domain.NewAppError(503, "TEAM_TEMPORARILY_UNAVAILABLE", "teams.unavailable", "teams service is unavailable", nil, domain.ErrInternal))
		return nil
	}
	return h.teams
}

func decodeTeamJSON(w http.ResponseWriter, r *http.Request, dst interface{}) error {
	return decodeJSON(w, r, teamJSONMaxBytes, dst)
}

func idempotencyKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key"))
}

func pageQuery(r *http.Request) teams.PageQuery {
	q := teams.PageQuery{Cursor: r.URL.Query().Get("cursor")}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			q.Limit = n
		}
	}
	return q
}

func (h *Handlers) GetMyTeam(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, ok, err := svc.GetMyTeam(r.Context(), user.UserID)
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	if !ok {
		writeTeamJSON(w, http.StatusOK, map[string]interface{}{"team": nil})
		return
	}
	writeTeamJSON(w, http.StatusOK, ctx)
}

func (h *Handlers) GetMyTeamInvitations(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	items, next, err := svc.ListMyInvitations(r.Context(), user, pageQuery(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, map[string]interface{}{"invitations": items, "nextCursor": emptyToNil(next)})
}

type createTeamBody struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Timezone    string               `json:"timezone"`
	Sharing     *domain.SharingFlags `json:"sharing"`
}

func (h *Handlers) CreateTeam(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body createTeamBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, err := svc.CreateTeam(r.Context(), user, teams.CreateTeamInput{
		Name: body.Name, Description: body.Description, Timezone: body.Timezone, Sharing: body.Sharing,
	}, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/teams/"+ctx.Team.ID)
	writeTeamJSON(w, http.StatusCreated, ctx)
}

func (h *Handlers) GetTeam(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, err := svc.GetTeam(r.Context(), user.UserID, chi.URLParam(r, "teamId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, ctx)
}

type patchTeamBody struct {
	Name                   *string `json:"name"`
	Description            *string `json:"description"`
	ExpectedProfileVersion string  `json:"expectedProfileVersion"`
}

func (h *Handlers) PatchTeam(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body patchTeamBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, err := svc.PatchTeam(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.PatchTeamInput{
		Name: body.Name, Description: body.Description, ExpectedProfileVersion: body.ExpectedProfileVersion,
	})
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, ctx)
}

func (h *Handlers) ListTeamMembers(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	q := pageQuery(r)
	user := GetUserFromContext(r.Context())
	items, next, err := svc.ListMembers(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.MemberListQuery{
		Q: r.URL.Query().Get("q"), Cursor: q.Cursor, Limit: q.Limit, SnapshotID: r.URL.Query().Get("snapshotId"),
	})
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, map[string]interface{}{"members": items, "nextCursor": emptyToNil(next)})
}

func (h *Handlers) GetTeamMember(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	item, err := svc.GetMember(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "membershipId"), r.URL.Query().Get("snapshotId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, item)
}

type changeRoleBody struct {
	Role                 string `json:"role"`
	ExpectedAuthRevision string `json:"expectedAuthRevision"`
}

func (h *Handlers) ChangeMemberRole(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body changeRoleBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, err := svc.ChangeMemberRole(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "membershipId"), teams.ChangeRoleInput{
		Role: body.Role, ExpectedAuthRevision: body.ExpectedAuthRevision,
	})
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, ctx)
}

type versionedAuthBody struct {
	ExpectedAuthRevision string `json:"expectedAuthRevision"`
}

func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body versionedAuthBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	if err := svc.RemoveMember(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "membershipId"), body.ExpectedAuthRevision, idempotencyKey(r)); err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ListTeamInvitations(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	items, next, err := svc.ListInvitations(r.Context(), user.UserID, chi.URLParam(r, "teamId"), pageQuery(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, map[string]interface{}{"invitations": items, "nextCursor": emptyToNil(next)})
}

type createInvitationBody struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *Handlers) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body createInvitationBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	inv, err := svc.CreateInvitation(r.Context(), user, chi.URLParam(r, "teamId"), teams.CreateInvitationInput{Email: body.Email, Role: body.Role}, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusCreated, map[string]interface{}{"invitation": inv, "deliveryState": inv.DeliveryState})
}

type invitationVersionBody struct {
	ExpectedInvitationVersion string `json:"expectedInvitationVersion"`
}

func (h *Handlers) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body invitationVersionBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	if err := svc.RevokeInvitation(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "invitationId"), body.ExpectedInvitationVersion, idempotencyKey(r)); err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ResendInvitation(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body invitationVersionBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	inv, err := svc.ResendInvitation(r.Context(), user, chi.URLParam(r, "teamId"), chi.URLParam(r, "invitationId"), body.ExpectedInvitationVersion, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusCreated, inv)
}

func (h *Handlers) GetInvitationPreview(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	prev, err := svc.PreviewInvitation(r.Context(), user, chi.URLParam(r, "invitationId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, prev)
}

type acceptInvitationBody struct {
	ExpectedInvitationVersion string               `json:"expectedInvitationVersion"`
	Sharing                   *domain.SharingFlags `json:"sharing"`
}

func (h *Handlers) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body acceptInvitationBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, err := svc.AcceptInvitation(r.Context(), user, chi.URLParam(r, "invitationId"), teams.AcceptInvitationInput{
		ExpectedInvitationVersion: body.ExpectedInvitationVersion, Sharing: body.Sharing,
	}, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, ctx)
}

func (h *Handlers) ListInviteLinks(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	items, next, err := svc.ListInviteLinks(r.Context(), user.UserID, chi.URLParam(r, "teamId"), pageQuery(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, map[string]interface{}{"links": items, "nextCursor": emptyToNil(next)})
}

type createInviteLinkBody struct {
	ExpiresInDays int `json:"expiresInDays"`
	MaxUses       int `json:"maxUses"`
}

func (h *Handlers) CreateInviteLink(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body createInviteLinkBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	link, err := svc.CreateInviteLink(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.CreateInviteLinkInput{
		ExpiresInDays: body.ExpiresInDays, MaxUses: body.MaxUses,
	}, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusCreated, link)
}

func (h *Handlers) InviteLinkShareURL(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	url, err := svc.InviteLinkShareURL(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "linkId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, map[string]interface{}{"shareUrl": url})
}

type inviteLinkVersionBody struct {
	ExpectedLinkVersion string `json:"expectedLinkVersion"`
	ExpiresInDays       int    `json:"expiresInDays"`
	MaxUses             int    `json:"maxUses"`
}

func (h *Handlers) RevokeInviteLink(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body inviteLinkVersionBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	if err := svc.RevokeInviteLink(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "linkId"), body.ExpectedLinkVersion, idempotencyKey(r)); err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) RegenerateInviteLink(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body inviteLinkVersionBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	link, err := svc.RegenerateInviteLink(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "linkId"), teams.CreateInviteLinkInput{
		ExpiresInDays: body.ExpiresInDays, MaxUses: body.MaxUses,
	}, body.ExpectedLinkVersion, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusCreated, link)
}

type inviteLinkTokenBody struct {
	Token               string               `json:"token"`
	ExpectedLinkVersion string               `json:"expectedLinkVersion"`
	Sharing             *domain.SharingFlags `json:"sharing"`
}

func (h *Handlers) PreviewInviteLink(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body inviteLinkTokenBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	prev, err := svc.PreviewInviteLink(r.Context(), user, chi.URLParam(r, "linkId"), body.Token)
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, prev)
}

func (h *Handlers) AcceptInviteLink(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body inviteLinkTokenBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, err := svc.AcceptInviteLink(r.Context(), user, chi.URLParam(r, "linkId"), teams.AcceptInviteLinkInput{
		Token: body.Token, ExpectedLinkVersion: body.ExpectedLinkVersion, Sharing: body.Sharing,
	}, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, ctx)
}

func (h *Handlers) GetMySharing(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	st, err := svc.GetMySharing(r.Context(), user.UserID, chi.URLParam(r, "teamId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, st)
}

type patchSharingBody struct {
	ExpectedSharingVersion string              `json:"expectedSharingVersion"`
	Sharing                domain.SharingFlags `json:"sharing"`
}

func (h *Handlers) UpdateMySharing(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body patchSharingBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	st, err := svc.UpdateMySharing(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.SharingPatchInput{
		ExpectedSharingVersion: body.ExpectedSharingVersion, Sharing: body.Sharing,
	})
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, st)
}

func (h *Handlers) GetTeamAnalysis(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	started := time.Now()
	dto, retryAfter, err := svc.GetAnalysis(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.AnalysisQuery{
		RangeKey: r.URL.Query().Get("range"), From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"),
		Agent: r.URL.Query().Get("agent"), Provider: r.URL.Query().Get("provider"), Model: r.URL.Query().Get("model"),
		SnapshotID: r.URL.Query().Get("snapshotId"), Collection: r.URL.Query().Get("collection"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	w.Header().Set("Server-Timing", "team_analysis;dur="+strconv.FormatFloat(float64(time.Since(started).Microseconds())/1000, 'f', 3, 64))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa((retryAfter+999)/1000))
		writeTeamJSON(w, http.StatusAccepted, dto)
		return
	}
	writeTeamJSON(w, http.StatusOK, dto)
}

func (h *Handlers) GetTeamFilterOptions(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	opts, err := svc.GetFilterOptions(r.Context(), user.UserID, chi.URLParam(r, "teamId"), r.URL.Query().Get("snapshotId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, opts)
}

type createExportBody struct {
	SnapshotID string  `json:"snapshotId"`
	Kind       string  `json:"kind"`
	Agent      *string `json:"agent"`
	Provider   *string `json:"provider"`
	Model      *string `json:"model"`
}

func (h *Handlers) CreateTeamExport(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body createExportBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	job, err := svc.CreateExport(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.CreateExportInput{
		SnapshotID: body.SnapshotID, Kind: body.Kind, Agent: body.Agent, Provider: body.Provider, Model: body.Model,
	}, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusAccepted, job)
}

func (h *Handlers) ListTeamExports(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	jobs, err := svc.ListExports(r.Context(), user.UserID, chi.URLParam(r, "teamId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, map[string]interface{}{"exports": jobs})
}

func (h *Handlers) GetTeamExport(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	job, err := svc.GetExport(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "exportId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, job)
}

func (h *Handlers) DownloadTeamExport(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	content, err := svc.OpenExportContent(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "exportId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	defer content.Body.Close()
	w.Header().Set("Cache-Control", teamCacheControl)
	w.Header().Set("Content-Type", content.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="`+content.Filename+`"`)
	if content.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(content.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, content.Body)
}

func (h *Handlers) LeaveTeam(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body versionedAuthBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	if err := svc.LeaveTeam(r.Context(), user.UserID, chi.URLParam(r, "teamId"), body.ExpectedAuthRevision, idempotencyKey(r)); err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

type transferBody struct {
	TargetMembershipID   string `json:"targetMembershipId"`
	ConfirmTeamName      string `json:"confirmTeamName"`
	ExpectedAuthRevision string `json:"expectedAuthRevision"`
}

func (h *Handlers) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body transferBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	ctx, err := svc.TransferOwnership(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.TransferInput{
		TargetMembershipID: body.TargetMembershipID, ConfirmTeamName: body.ConfirmTeamName, ExpectedAuthRevision: body.ExpectedAuthRevision,
	}, idempotencyKey(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, ctx)
}

type dissolveBody struct {
	ConfirmTeamName      string `json:"confirmTeamName"`
	ExpectedAuthRevision string `json:"expectedAuthRevision"`
}

func (h *Handlers) DissolveTeam(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body dissolveBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	if err := svc.DissolveTeam(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.DissolveInput{
		ConfirmTeamName: body.ConfirmTeamName, ExpectedAuthRevision: body.ExpectedAuthRevision,
	}, idempotencyKey(r)); err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ListTeamAuditEvents(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	items, next, err := svc.ListAuditEvents(r.Context(), user.UserID, chi.URLParam(r, "teamId"), pageQuery(r))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, map[string]interface{}{"events": items, "nextCursor": emptyToNil(next)})
}

type avatarIntentBody struct {
	ContentType string `json:"contentType"`
	ByteSize    uint64 `json:"byteSize"`
	Sha256      string `json:"sha256"`
}

func (h *Handlers) CreateTeamAvatarIntent(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body avatarIntentBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	intent, err := svc.CreateAvatarIntent(r.Context(), user.UserID, chi.URLParam(r, "teamId"), teams.CreateAvatarIntentInput{
		ContentType: body.ContentType, ByteSize: body.ByteSize, Sha256: body.Sha256,
	})
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusCreated, intent)
}

func (h *Handlers) UploadTeamAvatarContent(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024+1)
	if err := svc.UploadAvatarContent(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "objectId"), r.Body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

type completeAvatarBody struct {
	ExpectedProfileVersion string `json:"expectedProfileVersion"`
}

func (h *Handlers) CompleteTeamAvatar(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body completeAvatarBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	team, err := svc.CompleteAvatar(r.Context(), user.UserID, chi.URLParam(r, "teamId"), chi.URLParam(r, "objectId"), body.ExpectedProfileVersion)
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeTeamJSON(w, http.StatusOK, team)
}

func (h *Handlers) ClearTeamAvatar(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	var body completeAvatarBody
	if err := decodeTeamJSON(w, r, &body); err != nil {
		writeTeamError(w, r, err)
		return
	}
	user := GetUserFromContext(r.Context())
	if err := svc.ClearAvatar(r.Context(), user.UserID, chi.URLParam(r, "teamId"), body.ExpectedProfileVersion); err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) GetTeamAvatarContent(w http.ResponseWriter, r *http.Request) {
	svc := h.teamsOrUnavailable(w, r)
	if svc == nil {
		return
	}
	user := GetUserFromContext(r.Context())
	content, err := svc.ReadAvatar(r.Context(), user.UserID, chi.URLParam(r, "teamId"))
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", teamCacheControl)
	w.Header().Set("Content-Type", content.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Body)
}

func emptyToNil(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
