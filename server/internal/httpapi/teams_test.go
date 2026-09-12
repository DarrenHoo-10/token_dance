package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tokendance/internal/analytics"
	"tokendance/internal/auth"
	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/device"
	"tokendance/internal/export"
	"tokendance/internal/leaderboard"
	"tokendance/internal/media"
	"tokendance/internal/privacy"
	"tokendance/internal/profile"
	"tokendance/internal/search"
	"tokendance/internal/store/memory"
	"tokendance/internal/teams"
)

type teamTestApp struct {
	router http.Handler
	cookie *http.Cookie
	csrf   string
	auth   *auth.Service
	store  *memory.MemoryStore
}

func setupTeamsApp(t *testing.T, enabled, create, join, analysis, exportOn bool) *teamTestApp {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Argon2MemoryKiB = 1024
	cfg.Argon2Time = 1
	cfg.Argon2Parallelism = 1
	cfg.TeamsEnabled = enabled
	cfg.TeamsCreateEnabled = create
	cfg.TeamsJoinEnabled = join
	cfg.TeamsAnalysisEnabled = analysis
	cfg.TeamsExportEnabled = exportOn
	cfg.TeamsPublicBaseURL = "https://tokendance.dev/token-dance"

	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	authSvc := auth.NewService(st, cfg, clk)
	teamsSvc := teams.NewService(st, cfg, clk, authSvc, testStorage)
	router := NewRouterWithTeams(
		authSvc,
		profile.NewService(st, clk),
		privacy.NewService(st, clk),
		analytics.NewService(st, clk),
		device.NewService(st, cfg, clk),
		export.NewService(st, clk, testStorage),
		media.NewService(st, cfg, clk, testStorage),
		search.NewService(st, clk),
		leaderboard.NewService(st),
		teamsSvc,
		nil,
	)

	app := &teamTestApp{router: router, auth: authSvc, store: st}
	app.registerAndOnboard(t, "pilot@tokendance.dev", "tokenpilot", "Token Pilot")
	return app
}

func (a *teamTestApp) registerAndOnboard(t *testing.T, emailAddr, handle, displayName string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": emailAddr, "locale": "en-US"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register code %d %s", rec.Code, rec.Body.String())
	}
	emailHash := a.auth.ComputeEmailLookupHash(emailAddr)
	ch, err := a.store.FindPendingEmailChallenge(nil, "register", emailHash)
	if err != nil {
		t.Fatal(err)
	}
	var validCode string
	for i := 0; i <= 999999; i++ {
		cStr := ""
		val := i
		for k := 0; k < 6; k++ {
			cStr = string(rune('0'+(val%10))) + cStr
			val /= 10
		}
		if a.auth.ComputeTokenHash(cStr) == ch.CodeHash {
			validCode = cStr
			break
		}
	}
	regBody, _ := json.Marshal(map[string]string{"email": emailAddr, "code": validCode, "password": "PilotPassword123!"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %d %s", rec.Code, rec.Body.String())
	}
	var regResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	for _, c := range rec.Result().Cookies() {
		if c.Name == DevSessionCookie || c.Name == SessionCookieName {
			a.cookie = c
			break
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(a.cookie)
	rec = httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	var sess struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	a.csrf = sess.CSRFToken
	if a.csrf == "" {
		a.csrf = regResp.CSRFToken
	}
	onboard, _ := json.Marshal(map[string]interface{}{
		"displayName": displayName, "handle": handle, "timezone": "UTC", "locale": "en-US",
		"privacy": map[string]interface{}{"publicProfileEnabled": false, "showTokenTotal": false},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", bytes.NewReader(onboard))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", a.csrf)
	req.AddCookie(a.cookie)
	rec = httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("onboarding %d %s", rec.Code, rec.Body.String())
	}
}

func (a *teamTestApp) do(method, path string, body []byte, csrf bool, extra map[string]string) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	if a.cookie != nil {
		req.AddCookie(a.cookie)
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func TestTeamsDisabled(t *testing.T) {
	app := setupTeamsApp(t, false, false, false, false, false)
	rec := app.do(http.MethodGet, "/api/v1/me/team", nil, false, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache-control %q", rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(rec.Body.String(), "TEAM_TEMPORARILY_UNAVAILABLE") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestCreateTeamCSRFAndValidation(t *testing.T) {
	app := setupTeamsApp(t, true, true, true, true, true)

	rec := app.do(http.MethodGet, "/api/v1/me/team", nil, false, nil)
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("GET cache-control %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"team":null`) {
		t.Fatalf("GET me/team without membership: %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"UTC"}`), false, map[string]string{"Idempotency-Key": "k1"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected CSRF 403, got %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"x","timezone":"UTC"}`), true, map[string]string{"Idempotency-Key": "k2"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "API_INVALID_ARGUMENT") {
		t.Fatalf("short name %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"UTC","sharing":{"base":false,"named":false,"classification":true,"cost":false}}`), true, map[string]string{"Idempotency-Key": "k3"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid sharing %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"UTC","unknownField":true}`), true, map[string]string{"Idempotency-Key": "k4"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"UTC"}`), true, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"UTC"}`), true, map[string]string{"Idempotency-Key": "k5"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid create %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("write cache-control %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Location") == "" {
		t.Fatal("create must set Location")
	}
	if !strings.Contains(rec.Body.String(), `"profileVersion":"1"`) {
		t.Fatalf("versions must be decimal strings: %s", rec.Body.String())
	}

	teamID := rec.Header().Get("Location")
	if strings.HasPrefix(teamID, "/api/v1") {
		rec = app.do(http.MethodGet, teamID, nil, false, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET created team %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestCreateDisabledJoinAnalysisExportFlags(t *testing.T) {
	app := setupTeamsApp(t, true, false, false, false, false)
	rec := app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"UTC"}`), true, map[string]string{"Idempotency-Key": "k6"})
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "TEAM_TEMPORARILY_UNAVAILABLE") {
		t.Fatalf("create disabled %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/team-invitations/tiv_xxxxxxxxxxxxxxxxxxxxxx/accept", []byte(`{"expectedInvitationVersion":"1"}`), true, map[string]string{"Idempotency-Key": "k7"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("join disabled %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodGet, "/api/v1/teams/tem_xxxxxxxxxxxxxxxxxxxxxx/analysis?range=7d", nil, false, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("analysis disabled %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams/tem_xxxxxxxxxxxxxxxxxxxxxx/exports", []byte(`{"snapshotId":"tas_x","kind":"daily"}`), true, map[string]string{"Idempotency-Key": "k8"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("export disabled %d %s", rec.Code, rec.Body.String())
	}

	rec = app.do(http.MethodPost, "/api/v1/teams/tem_xxxxxxxxxxxxxxxxxxxxxx/leave", []byte(`{"expectedAuthRevision":"1"}`), true, map[string]string{"Idempotency-Key": "k9"})
	if rec.Code == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), "teams.disabled") {
		t.Fatalf("leave must stay available when TeamsEnabled: %s", rec.Body.String())
	}
}

func TestTeamsAuthRequired(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Argon2MemoryKiB = 1024
	cfg.Argon2Time = 1
	cfg.Argon2Parallelism = 1
	cfg.TeamsEnabled = true
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Now().UTC())
	authSvc := auth.NewService(st, cfg, clk)
	router := NewRouterWithTeams(
		authSvc, profile.NewService(st, clk), privacy.NewService(st, clk), analytics.NewService(st, clk),
		device.NewService(st, cfg, clk), export.NewService(st, clk, testStorage), media.NewService(st, cfg, clk, testStorage),
		search.NewService(st, clk), leaderboard.NewService(st), teams.NewService(st, cfg, clk, authSvc, testStorage), nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/team", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestLeaveMissingVersion(t *testing.T) {
	app := setupTeamsApp(t, true, true, true, true, true)
	rec := app.do(http.MethodPost, "/api/v1/teams/tem_xxxxxxxxxxxxxxxxxxxxxx/leave", []byte(`{}`), true, map[string]string{"Idempotency-Key": "leave-1"})
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected leave status %d %s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "API_INVALID_ARGUMENT") {
		t.Fatalf("expected field error, got %s", rec.Body.String())
	}
}

func TestTeamMembersPayloadMatchesWeb(t *testing.T) {
	app := setupTeamsApp(t, true, true, true, true, true)
	rec := app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"Asia/Shanghai"}`), true, map[string]string{"Idempotency-Key": "members-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Team.ID == "" {
		t.Fatalf("decode create %v %s", err, rec.Body.String())
	}
	rec = app.do(http.MethodGet, "/api/v1/teams/"+created.Team.ID+"/members", nil, false, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list members %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Members []map[string]any `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Members) != 1 {
		t.Fatalf("members %+v", body.Members)
	}
	m := body.Members[0]
	if m["membershipId"] == nil || m["membershipId"] == "" {
		t.Fatalf("missing membershipId: %s", rec.Body.String())
	}
	sharing, _ := m["sharing"].(map[string]any)
	if sharing == nil {
		t.Fatalf("missing sharing: %s", rec.Body.String())
	}
	if _, ok := sharing["base"]; !ok {
		t.Fatalf("sharing.base missing: %s", rec.Body.String())
	}
	if m["syncStatus"] == nil || m["canOpenDetail"] == nil {
		t.Fatalf("missing sync/detail fields: %s", rec.Body.String())
	}
}

func TestInviteLinkIdempotentRetryReturnsUsableToken(t *testing.T) {
	app := setupTeamsApp(t, true, true, true, true, true)
	rec := app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"UTC"}`), true, map[string]string{"Idempotency-Key": "link-team"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	body := []byte(`{"expiresInDays":7,"maxUses":10}`)
	first := app.do(http.MethodPost, "/api/v1/teams/"+created.Team.ID+"/invite-links", body, true, map[string]string{"Idempotency-Key": "link-same"})
	second := app.do(http.MethodPost, "/api/v1/teams/"+created.Team.ID+"/invite-links", body, true, map[string]string{"Idempotency-Key": "link-same"})
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("create links %d %s / %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	var firstLink, secondLink struct {
		ID       string `json:"id"`
		ShareURL string `json:"shareUrl"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &firstLink)
	_ = json.Unmarshal(second.Body.Bytes(), &secondLink)
	if firstLink.ID == "" || firstLink.ID != secondLink.ID {
		t.Fatalf("replay should return same link %s %s", firstLink.ID, secondLink.ID)
	}
	token := shareURLToken(t, secondLink.ShareURL)
	preview := app.do(http.MethodPost, "/api/v1/team-invite-links/"+secondLink.ID+"/preview", []byte(`{"token":"`+token+`"}`), true, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("replayed link preview %d %s", preview.Code, preview.Body.String())
	}
}

func shareURLToken(t *testing.T, shareURL string) string {
	t.Helper()
	idx := strings.Index(shareURL, "#key=")
	if idx < 0 {
		t.Fatalf("shareUrl missing token: %s", shareURL)
	}
	return shareURL[idx+5:]
}

func TestEmailInvitationPreviewMatchesWeb(t *testing.T) {
	app := setupTeamsApp(t, true, true, true, true, true)
	rec := app.do(http.MethodPost, "/api/v1/teams", []byte(`{"name":"星河开发组","timezone":"Asia/Shanghai"}`), true, map[string]string{"Idempotency-Key": "invite-team"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Team.ID == "" {
		t.Fatalf("decode create %v %s", err, rec.Body.String())
	}
	rec = app.do(http.MethodPost, "/api/v1/teams/"+created.Team.ID+"/invitations", []byte(`{"email":"guest@tokendance.dev","role":"member"}`), true, map[string]string{"Idempotency-Key": "invite-mail"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invitation %d %s", rec.Code, rec.Body.String())
	}
	var invited struct {
		Invitation struct {
			ID string `json:"id"`
		} `json:"invitation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &invited); err != nil || invited.Invitation.ID == "" {
		t.Fatalf("decode invitation %v %s", err, rec.Body.String())
	}

	app.registerAndOnboard(t, "guest@tokendance.dev", "guestuser", "Guest User")
	preview := app.do(http.MethodGet, "/api/v1/team-invitations/"+invited.Invitation.ID, nil, false, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview %d %s", preview.Code, preview.Body.String())
	}
	var prev map[string]any
	if err := json.Unmarshal(preview.Body.Bytes(), &prev); err != nil {
		t.Fatal(err)
	}
	team, _ := prev["team"].(map[string]any)
	if team == nil || team["id"] != created.Team.ID || team["name"] != "星河开发组" {
		t.Fatalf("preview team mismatch: %s", preview.Body.String())
	}
	if prev["invitedRole"] != "member" {
		t.Fatalf("preview invitedRole mismatch: %s", preview.Body.String())
	}

	inbox := app.do(http.MethodGet, "/api/v1/me/team-invitations", nil, false, nil)
	if inbox.Code != http.StatusOK {
		t.Fatalf("inbox %d %s", inbox.Code, inbox.Body.String())
	}
	var box struct {
		Invitations []map[string]any `json:"invitations"`
	}
	if err := json.Unmarshal(inbox.Body.Bytes(), &box); err != nil {
		t.Fatal(err)
	}
	if len(box.Invitations) != 1 {
		t.Fatalf("inbox %+v", box.Invitations)
	}
	inboxTeam, _ := box.Invitations[0]["team"].(map[string]any)
	if inboxTeam == nil || inboxTeam["id"] != created.Team.ID || box.Invitations[0]["invitedRole"] != "member" {
		t.Fatalf("inbox contract mismatch: %s", inbox.Body.String())
	}
}
