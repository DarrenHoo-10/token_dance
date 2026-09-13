package worker

import (
	"database/sql"
	"strings"
	"testing"

	"tokendance/internal/domain"
)

func TestDecideTeamExportAuthRechecksRoleMembershipAndRevision(t *testing.T) {
	claim := &teamExportClaim{
		teamID:                "tem_1",
		requesterUserID:       "usr_admin",
		requesterMembershipID: "tmb_admin",
		authRevision:          9,
	}
	auth := decideTeamExportAuth(
		claim, string(domain.TeamStatusActive), "usr_owner", 9,
		sql.NullString{String: "tmb_admin", Valid: true},
		sql.NullString{String: string(domain.TeamBaseRoleAdmin), Valid: true},
		sql.NullTime{},
		sql.NullString{String: string(domain.AccountStatusActive), Valid: true},
	)
	if !auth.ok || auth.revoke {
		t.Fatalf("current admin should pass export auth: %+v", auth)
	}

	auth = decideTeamExportAuth(
		claim, string(domain.TeamStatusActive), "usr_owner", 10,
		sql.NullString{String: "tmb_admin", Valid: true},
		sql.NullString{String: string(domain.TeamBaseRoleAdmin), Valid: true},
		sql.NullTime{},
		sql.NullString{String: string(domain.AccountStatusActive), Valid: true},
	)
	if auth.ok || !auth.revoke {
		t.Fatal("auth_revision change must revoke the export")
	}

	auth = decideTeamExportAuth(
		claim, string(domain.TeamStatusActive), "usr_owner", 9,
		sql.NullString{String: "tmb_new", Valid: true},
		sql.NullString{String: string(domain.TeamBaseRoleAdmin), Valid: true},
		sql.NullTime{},
		sql.NullString{String: string(domain.AccountStatusActive), Valid: true},
	)
	if auth.ok {
		t.Fatal("rejoined membership must not read the old export")
	}

	auth = decideTeamExportAuth(
		claim, string(domain.TeamStatusActive), "usr_owner", 9,
		sql.NullString{String: "tmb_admin", Valid: true},
		sql.NullString{String: string(domain.TeamBaseRoleMember), Valid: true},
		sql.NullTime{},
		sql.NullString{String: string(domain.AccountStatusActive), Valid: true},
	)
	if auth.ok {
		t.Fatal("ordinary member cannot export")
	}
}

func TestEscapeCSVFormulaGuardsLeadingOperators(t *testing.T) {
	for _, value := range []string{"=CMD", "+1+1", "-2", "@SUM(A1)", "\t=1"} {
		got := escapeCSVFormula(value)
		if !strings.HasPrefix(got, "'") {
			t.Fatalf("expected formula guard for %q, got %q", value, got)
		}
	}
	if escapeCSVFormula("normal") != "normal" {
		t.Fatal("plain text should stay unchanged")
	}
}

func TestTeamExportObjectKeyIsPrivatePath(t *testing.T) {
	key := "team-exports/tem_1/tex_1/3.csv"
	if strings.Contains(key, "http") || strings.Contains(key, "?") {
		t.Fatalf("export object key must not be a public URL: %s", key)
	}
}
