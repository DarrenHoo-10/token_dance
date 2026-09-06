package teams

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type stubTeamsStore struct{}

func stubErr() error {
	return domain.NewAppError(503, "TEAM_TEMPORARILY_UNAVAILABLE", "teams.unavailable", "teams store is not implemented yet", nil, domain.ErrInternal)
}

func (stubTeamsStore) GetCurrentTeam(context.Context, string) (*domain.UserCurrentTeam, *domain.Team, *domain.TeamMembership, error) {
	return nil, nil, nil, stubErr()
}
func (stubTeamsStore) GetTeam(context.Context, string) (*domain.Team, error) {
	return nil, stubErr()
}
func (stubTeamsStore) GetMembership(context.Context, string, string) (*domain.TeamMembership, error) {
	return nil, stubErr()
}
func (stubTeamsStore) ListMembers(context.Context, string, string, string, int) ([]domain.TeamMembership, []domain.User, string, error) {
	return nil, nil, "", stubErr()
}
func (stubTeamsStore) ListInvitationsForEmail(context.Context, [32]byte, string, int) ([]domain.TeamInvitation, string, error) {
	return nil, "", stubErr()
}
func (stubTeamsStore) ListTeamInvitations(context.Context, string, string, int) ([]domain.TeamInvitation, string, error) {
	return nil, "", stubErr()
}
func (stubTeamsStore) GetInvitation(context.Context, string) (*domain.TeamInvitation, error) {
	return nil, stubErr()
}
func (stubTeamsStore) ListInviteLinks(context.Context, string, string, int) ([]domain.TeamInviteLink, string, error) {
	return nil, "", stubErr()
}
func (stubTeamsStore) GetInviteLink(context.Context, string) (*domain.TeamInviteLink, error) {
	return nil, stubErr()
}
func (stubTeamsStore) GetInviteLinkByTokenHash(context.Context, [32]byte) (*domain.TeamInviteLink, error) {
	return nil, stubErr()
}
func (stubTeamsStore) GetMySharing(context.Context, string, string) (*domain.TeamSharingState, error) {
	return nil, stubErr()
}
func (stubTeamsStore) ListAuditEvents(context.Context, string, string, int) ([]domain.TeamAuditEvent, string, error) {
	return nil, "", stubErr()
}
func (stubTeamsStore) ListExports(context.Context, string, string) ([]domain.TeamExportJob, error) {
	return nil, stubErr()
}
func (stubTeamsStore) GetExport(context.Context, string, string) (*domain.TeamExportJob, error) {
	return nil, stubErr()
}
func (stubTeamsStore) GetAvatarObject(context.Context, string, string) (*domain.TeamUploadObject, error) {
	return nil, stubErr()
}
func (stubTeamsStore) HasOpenDeletionBarrier(context.Context, string) (bool, error) {
	return false, stubErr()
}
func (stubTeamsStore) CreateTeamTx(context.Context, store.CreateTeamTxInput) (*store.CreateTeamTxResult, error) {
	return nil, stubErr()
}
func (stubTeamsStore) UpdateTeamProfileTx(context.Context, store.UpdateTeamProfileTxInput) (*domain.Team, error) {
	return nil, stubErr()
}
func (stubTeamsStore) CreateInvitationTx(context.Context, store.CreateInvitationTxInput) (*store.CreateInvitationTxResult, error) {
	return nil, stubErr()
}
func (stubTeamsStore) RevokeInvitationTx(context.Context, string, string, string, uint64, store.TeamsIdempotency, time.Time) error {
	return stubErr()
}
func (stubTeamsStore) ResendInvitationTx(context.Context, string, string, string, uint64, domain.TeamInvitation, domain.EmailOutbox, store.TeamsIdempotency, time.Time) (*domain.TeamInvitation, error) {
	return nil, stubErr()
}
func (stubTeamsStore) AcceptInvitationTx(context.Context, store.AcceptInvitationTxInput) (*store.AcceptInvitationTxResult, error) {
	return nil, stubErr()
}
func (stubTeamsStore) CreateInviteLinkTx(context.Context, store.CreateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	return nil, stubErr()
}
func (stubTeamsStore) RevokeInviteLinkTx(context.Context, store.RevokeInviteLinkTxInput) error {
	return stubErr()
}
func (stubTeamsStore) RegenerateInviteLinkTx(context.Context, store.RegenerateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	return nil, stubErr()
}
func (stubTeamsStore) AcceptInviteLinkTx(context.Context, store.AcceptInviteLinkTxInput) (*store.AcceptInviteLinkTxResult, error) {
	return nil, stubErr()
}
func (stubTeamsStore) UpdateSharingTx(context.Context, store.UpdateSharingTxInput) (*domain.TeamSharingState, error) {
	return nil, stubErr()
}
func (stubTeamsStore) ChangeMemberRoleTx(context.Context, store.ChangeMemberRoleTxInput) error {
	return stubErr()
}
func (stubTeamsStore) RemoveMemberTx(context.Context, store.RemoveMemberTxInput) error {
	return stubErr()
}
func (stubTeamsStore) LeaveTeamTx(context.Context, store.LeaveTeamTxInput) error {
	return stubErr()
}
func (stubTeamsStore) TransferOwnershipTx(context.Context, store.TransferOwnershipTxInput) (*domain.TeamContext, error) {
	return nil, stubErr()
}
func (stubTeamsStore) DissolveTeamTx(context.Context, store.DissolveTeamTxInput) error {
	return stubErr()
}
func (stubTeamsStore) QueueTeamExportTx(context.Context, store.QueueTeamExportTxInput) (*domain.TeamExportJob, error) {
	return nil, stubErr()
}
func (stubTeamsStore) CreateTeamAvatarUploadIntent(context.Context, domain.TeamUploadObject) (*domain.TeamUploadObject, error) {
	return nil, stubErr()
}
func (stubTeamsStore) CompleteTeamAvatarUpload(context.Context, string, string, string, store.AvatarReadyMeta, uint64, time.Time) (*domain.Team, error) {
	return nil, stubErr()
}
func (stubTeamsStore) ClearTeamAvatar(context.Context, string, string, uint64, time.Time) error {
	return stubErr()
}
func (stubTeamsStore) GetOrQueueAnalysis(context.Context, string, time.Time, time.Time, uint64, string, time.Time) (*domain.TeamAnalysisSnapshot, bool, error) {
	return nil, false, stubErr()
}
func (stubTeamsStore) GetReadySnapshot(context.Context, string, string) (*domain.TeamAnalysisSnapshot, error) {
	return nil, stubErr()
}
func (stubTeamsStore) ListAnalysisRows(context.Context, string, uint64) ([]domain.TeamAnalysisRow, error) {
	return nil, stubErr()
}
func (stubTeamsStore) ClaimAnalysis(context.Context, string, time.Duration, time.Time) (*domain.TeamAnalysisSnapshot, error) {
	return nil, stubErr()
}
func (stubTeamsStore) PublishAnalysis(context.Context, string, string, uint64, uint64, uint64, uint64, time.Time) error {
	return stubErr()
}
func (stubTeamsStore) MarkAnalysisFailed(context.Context, string, string, uint64, string, time.Time) error {
	return stubErr()
}
func (stubTeamsStore) BumpSourceRevision(context.Context, string, time.Time) (uint64, error) {
	return 0, stubErr()
}
func (stubTeamsStore) RegisterDeletionBarrier(context.Context, string, string, time.Time) error {
	return stubErr()
}
func (stubTeamsStore) ReleaseDeletionBarrier(context.Context, string, string, time.Time) error {
	return stubErr()
}
func (stubTeamsStore) ClaimTeamExport(context.Context, string, time.Duration, time.Time) (*domain.TeamExportJob, error) {
	return nil, stubErr()
}
func (stubTeamsStore) CompleteTeamExport(context.Context, string, string, uint64, string, [32]byte, uint64, time.Time) error {
	return stubErr()
}
func (stubTeamsStore) FailTeamExport(context.Context, string, string, uint64, string, time.Time) error {
	return stubErr()
}

func testService(t *testing.T, enabled bool) *Service {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.TeamsEnabled = enabled
	cfg.TeamsCreateEnabled = enabled
	cfg.TeamsJoinEnabled = enabled
	cfg.TeamsAnalysisEnabled = enabled
	cfg.TeamsExportEnabled = enabled
	cfg.TeamsPublicBaseURL = "https://tokendance.dev/token-dance"
	return &Service{
		teams: stubTeamsStore{},
		cfg:   cfg,
		clk:   clock.NewMockClock(time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)),
	}
}

func TestCreateTeamValidation(t *testing.T) {
	svc := testService(t, true)
	user := &domain.User{UserID: "usr_test", AccountStatus: domain.AccountStatusActive, EmailVerifiedAt: ptrTime(svc.clk.Now())}

	t.Run("invalid name", func(t *testing.T) {
		_, err := svc.CreateTeam(context.Background(), user, CreateTeamInput{Name: " ", Timezone: "Asia/Shanghai"}, "idemp-1")
		if err == nil {
			t.Fatal("expected name error")
		}
		var app *domain.AppError
		if !errors.As(err, &app) || app.Code != "API_INVALID_ARGUMENT" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("invalid timezone", func(t *testing.T) {
		_, err := svc.CreateTeam(context.Background(), user, CreateTeamInput{Name: "星河开发组", Timezone: "Not/AZone"}, "idemp-2")
		if err == nil {
			t.Fatal("expected timezone error")
		}
	})

	t.Run("sharing base false with named true", func(t *testing.T) {
		_, err := svc.CreateTeam(context.Background(), user, CreateTeamInput{
			Name: "星河开发组", Timezone: "Asia/Shanghai",
			Sharing: &domain.SharingFlags{Named: true},
		}, "idemp-3")
		if err == nil {
			t.Fatal("expected sharing error")
		}
		var app *domain.AppError
		if !errors.As(err, &app) || app.Code != "API_INVALID_ARGUMENT" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing idempotency", func(t *testing.T) {
		_, err := svc.CreateTeam(context.Background(), user, CreateTeamInput{Name: "星河开发组", Timezone: "UTC"}, "")
		if err == nil {
			t.Fatal("expected idempotency error")
		}
	})

	t.Run("default sharing is all false and store 503", func(t *testing.T) {
		_, err := svc.CreateTeam(context.Background(), user, CreateTeamInput{Name: "星河开发组", Timezone: "UTC"}, "idemp-ok")
		if err == nil {
			t.Fatal("stub store should return 503")
		}
		var app *domain.AppError
		if !errors.As(err, &app) || app.Code != "TEAM_TEMPORARILY_UNAVAILABLE" {
			t.Fatalf("expected store 503 mapping, got %v", err)
		}
	})
}

func TestFeatureFlags(t *testing.T) {
	svc := testService(t, false)
	user := &domain.User{UserID: "usr_test", AccountStatus: domain.AccountStatusActive, EmailVerifiedAt: ptrTime(time.Now().UTC())}

	_, _, err := svc.GetMyTeam(context.Background(), user.UserID)
	if !isUnavailable(err) {
		t.Fatalf("disabled master should 503, got %v", err)
	}

	svc.cfg.TeamsEnabled = true
	svc.cfg.TeamsCreateEnabled = false
	_, err = svc.CreateTeam(context.Background(), user, CreateTeamInput{Name: "星河开发组", Timezone: "UTC"}, "k")
	if !isUnavailable(err) {
		t.Fatalf("create disabled should 503, got %v", err)
	}

	svc.cfg.TeamsJoinEnabled = false
	_, err = svc.AcceptInvitation(context.Background(), user, "tiv_x", AcceptInvitationInput{ExpectedInvitationVersion: "1"}, "k")
	if !isUnavailable(err) {
		t.Fatalf("join disabled should 503, got %v", err)
	}

	svc.cfg.TeamsAnalysisEnabled = false
	_, _, err = svc.GetAnalysis(context.Background(), user.UserID, "tem_x", AnalysisQuery{RangeKey: "7d"})
	if !isUnavailable(err) {
		t.Fatalf("analysis disabled should 503, got %v", err)
	}

	svc.cfg.TeamsExportEnabled = false
	_, err = svc.CreateExport(context.Background(), user.UserID, "tem_x", CreateExportInput{SnapshotID: "tas_x", Kind: "daily"}, "k")
	if !isUnavailable(err) {
		t.Fatalf("export disabled should 503, got %v", err)
	}

	svc.cfg.TeamsEnabled = true
	err = svc.LeaveTeam(context.Background(), user.UserID, "tem_x", "1", "k")
	if err == nil {
		t.Fatal("leave should still run against store")
	}
	var app *domain.AppError
	if !errors.As(err, &app) {
		t.Fatalf("unexpected %v", err)
	}
	if app.Code == "TEAM_TEMPORARILY_UNAVAILABLE" && strings.Contains(app.MessageKey, "disabled") && app.MessageKey != "teams.unavailable" {
		t.Fatalf("leave must remain available when TeamsEnabled: %s", app.MessageKey)
	}
}

func TestFormatTeamCalendarDateShanghai(t *testing.T) {
	from := time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC)
	if got := domain.FormatTeamCalendarDate(from, "Asia/Shanghai"); got != "2026-09-06" {
		t.Fatalf("got %s", got)
	}
	if got := domain.FormatTeamCalendarDate(from, "UTC"); got != "2026-09-05" {
		t.Fatalf("utc %s", got)
	}
}

func TestResolveTeamRangeShanghaiCustomStaysOnRequestedDay(t *testing.T) {
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	from, toEx, err := ResolveTeamRange("Asia/Shanghai", "custom", "2026-09-06", "2026-09-06", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := domain.FormatTeamCalendarDate(from, "Asia/Shanghai"); got != "2026-09-06" {
		t.Fatalf("from local date %s", got)
	}
	if got := domain.FormatTeamCalendarDate(toEx, "Asia/Shanghai"); got != "2026-09-07" {
		t.Fatalf("toExclusive local date %s", got)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	fromLocal := time.Date(2026, 9, 6, 0, 0, 0, 0, loc)
	if !from.Equal(fromLocal.UTC()) {
		t.Fatalf("from UTC %s want %s", from, fromLocal.UTC())
	}
}

func TestAssembleAnalysisPreservesEstimatedCostAndCoverage(t *testing.T) {
	currency := "USD"
	mem := "tmb_1"
	date := "2026-09-06"
	usage := "1"
	row := domain.TeamAnalysisRow{
		MembershipID: &mem, MetricDate: &date, Currency: &currency,
		TokenExactTotal: "10", TokenDerivedTotal: "0", UsageEventCount: usage,
		TokenSupportedEventCount: usage, EstimatedCostAmount: "3.50000000",
		EstimatedCostEventCount: "1", ReportedCoveredUsageCount: "0",
		VisibilityMask: VisibilityNamed,
	}
	team := &domain.Team{TimezoneName: "Asia/Shanghai"}
	snap := &domain.TeamAnalysisSnapshot{SnapshotID: "tas_1", AuthRevision: 1, AsOf: time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)}
	dto := assembleAnalysis(team, snap, []domain.TeamAnalysisRow{row}, nil, nil, 1, snap.AsOf, snap.AsOf.Add(time.Hour), domain.TeamAnalysisFilters{}, AnalysisQuery{})
	if len(dto.Costs.EstimatedUncovered) != 1 || dto.Costs.EstimatedUncovered[0].Amount != "3.50000000" {
		t.Fatalf("estimated %+v", dto.Costs.EstimatedUncovered)
	}
	if dto.Costs.Coverage.EligibleUsageEvents != "1" || dto.Costs.Coverage.ReportedUsageEvents != "0" {
		t.Fatalf("coverage %+v", dto.Costs.Coverage)
	}
}

func TestAssembleAnalysisCollectionsMatchWeb(t *testing.T) {
	mem, agent, provider, model, date := "tmb_1", "codex", "openai", "gpt-test", "2026-09-06"
	row := domain.TeamAnalysisRow{
		MembershipID: &mem, MetricDate: &date, AgentID: &agent, ProviderID: &provider, ModelID: &model,
		TokenExactTotal: "123", TokenDerivedTotal: "0", UsageEventCount: "1",
		VisibilityMask: VisibilityNamed | VisibilityClassification,
	}
	team := &domain.Team{TimezoneName: "Asia/Shanghai"}
	snap := &domain.TeamAnalysisSnapshot{SnapshotID: "tas_1", AuthRevision: 1, AsOf: time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)}
	handle := "ada"
	dto := assembleAnalysis(team, snap, []domain.TeamAnalysisRow{row},
		[]domain.TeamMembership{{MembershipID: mem, UserID: "usr_1"}},
		[]domain.User{{UserID: "usr_1", DisplayName: "Ada", Handle: &handle}},
		1, snap.AsOf, snap.AsOf.Add(time.Hour), domain.TeamAnalysisFilters{}, AnalysisQuery{})
	if len(dto.Agents.Items) != 1 || dto.Agents.Items[0]["id"] != "codex" || dto.Agents.Items[0]["label"] != "codex" {
		t.Fatalf("agents %+v", dto.Agents.Items)
	}
	if dto.Models.Items[0]["id"] != "gpt-test" || dto.Models.Items[0]["label"] != "openai/gpt-test" {
		t.Fatalf("models %+v", dto.Models.Items)
	}
	if dto.Contributions.Items[0]["displayName"] != "Ada" || dto.Contributions.Items[0]["rank"] != "1" || dto.Contributions.Items[0]["membershipId"] != mem {
		t.Fatalf("contributions %+v", dto.Contributions.Items)
	}
	opts := setToOptions(map[string]struct{}{"codex": {}})
	if len(opts) != 1 || opts[0]["id"] != "codex" || opts[0]["label"] != "codex" {
		t.Fatalf("filter options %+v", opts)
	}
}

func TestAssembleMemberDetailPreservesCosts(t *testing.T) {
	mem, currency, date := "tmb_1", "USD", "2026-09-06"
	row := domain.TeamAnalysisRow{
		MembershipID: &mem, MetricDate: &date, Currency: &currency,
		TokenExactTotal: "10", UsageEventCount: "1",
		ReportedCostAmount: "2.00000000", ReportedCoveredUsageCount: "1",
		VisibilityMask: VisibilityNamed | VisibilityClassification | VisibilityCost,
	}
	target := &domain.TeamMembership{MembershipID: mem, UserID: "usr_1", JoinedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	user := &domain.User{UserID: "usr_1", DisplayName: "Ada"}
	team := &domain.Team{TimezoneName: "UTC", OwnerUserID: "usr_1"}
	snap := &domain.TeamAnalysisSnapshot{
		SnapshotID: "tas_1",
		FromDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		ToDateExclusive: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC),
		AsOf: time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC),
	}
	got := assembleMemberDetail(target, user, team, snap, []domain.TeamAnalysisRow{row}, domain.SharingFlags{Base: true, Named: true, Classification: true, Cost: true})
	costs := got["costs"].(domain.TeamAnalysisCosts)
	if len(costs.Reported) != 1 || costs.Reported[0].Amount != "2.00000000" {
		t.Fatalf("costs %+v", costs)
	}
	if costs.Coverage.ReportedUsageEvents != "1" || costs.Coverage.EligibleUsageEvents != "1" {
		t.Fatalf("coverage %+v", costs.Coverage)
	}
	if got["dimensions"].(map[string]string)["cost"] != "available" {
		t.Fatalf("dims %+v", got["dimensions"])
	}
	hidden := assembleMemberDetail(target, user, team, snap, []domain.TeamAnalysisRow{row}, domain.SharingFlags{Base: true, Named: true})
	hiddenCosts := hidden["costs"].(domain.TeamAnalysisCosts)
	if len(hiddenCosts.Reported) != 0 {
		t.Fatalf("unshared cost should be empty: %+v", hiddenCosts)
	}
	if hidden["dimensions"].(map[string]string)["cost"] != "unavailable" || hidden["dimensions"].(map[string]string)["classification"] != "unavailable" {
		t.Fatalf("unshared dims %+v", hidden["dimensions"])
	}
}

func TestSortKVIsNumeric(t *testing.T) {
	got := sortKV(map[string]string{"a": "9", "b": "100"})
	if len(got) != 2 || got[0].Value != "100" || got[1].Value != "9" {
		t.Fatalf("numeric sort %v", got)
	}
}

func TestMaskEmail(t *testing.T) {
	if got := maskEmail("ada@example.com"); got != "a***@example.com" {
		t.Fatalf("mask %s", got)
	}
}

func TestResolveTeamRange(t *testing.T) {
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	from, toEx, err := ResolveTeamRange("Asia/Shanghai", "7d", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if from.In(loc).Day() != 31 || from.In(loc).Month() != time.August {
		t.Fatalf("7d from=%s", from.In(loc))
	}
	if toEx.In(loc).Day() != 7 {
		t.Fatalf("7d toExclusive=%s", toEx.In(loc))
	}

	_, _, err = ResolveTeamRange("UTC", "custom", "2026-01-01", "2026-04-10", now)
	if err == nil {
		t.Fatal("91 days must fail")
	}
	_, _, err = ResolveTeamRange("UTC", "custom", "2026-10-01", "2026-10-02", now)
	if err == nil {
		t.Fatal("future dates must fail")
	}
	from, toEx, err = ResolveTeamRange("UTC", "custom", "2026-08-01", "2026-08-01", now)
	if err != nil {
		t.Fatal(err)
	}
	if !toEx.After(from) || toEx.Sub(from) != 24*time.Hour {
		t.Fatalf("single day exclusive end: %s %s", from, toEx)
	}
}

func TestMapStoreError(t *testing.T) {
	got := mapStoreError(domain.ErrPreconditionFailed, "TEAM_NOT_FOUND")
	var app *domain.AppError
	if !errors.As(got, &app) || app.Code != "TEAM_VERSION_CONFLICT" {
		t.Fatalf("version mapping %v", got)
	}
	if got := mapStoreError(domain.ErrNotFound, "RESOURCE_NOT_FOUND"); !isResource(got) {
		t.Fatalf("resource mapping %v", got)
	}
	if !errors.As(mapStoreError(domain.ErrIdempotencyReused, ""), &app) || app.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("expected IDEMPOTENCY_KEY_REUSED")
	}
}

func TestSharingDefaultValid(t *testing.T) {
	got, err := normalizeSharing(nil)
	if err != nil || got.Base || got.Named || got.Classification || got.Cost {
		t.Fatalf("default sharing %+v err=%v", got, err)
	}
}

func TestInviteLinkParams(t *testing.T) {
	d, u, err := normalizeInviteLinkParams(0, 0)
	if err != nil || d != 7 || u != 50 {
		t.Fatalf("defaults %d %d %v", d, u, err)
	}
	if _, _, err := normalizeInviteLinkParams(14, 10); err == nil {
		t.Fatal("14 days is invalid")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func isUnavailable(err error) bool {
	var app *domain.AppError
	return errors.As(err, &app) && app.Code == "TEAM_TEMPORARILY_UNAVAILABLE"
}

func isResource(err error) bool {
	var app *domain.AppError
	return errors.As(err, &app) && app.Code == "RESOURCE_NOT_FOUND"
}
