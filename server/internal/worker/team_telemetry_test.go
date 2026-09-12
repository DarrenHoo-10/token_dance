package worker

import (
	"database/sql"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

func TestTeamTelemetryTokenSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, usage, accuracy, total string
		supported                    bool
	}{
		{"exact", `{"token_total":"10"}`, "exact", "10", true},
		{"derived", `{"token_total":"12"}`, "derived", "12", true},
		{"uint64", `{"token_total":"18446744073709551615"}`, "exact", "18446744073709551615", true},
		{"known parts are not a total", `{"input_context_tokens":"10","output_tokens":"5"}`, "exact", "0", false},
		{"estimated is unsupported", `{"token_total":"10"}`, "estimated", "0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := teamFactEvent{eventType: "model_usage_recorded", tokenInput: sql.NullInt64{Int64: 10, Valid: true}, tokenOutput: sql.NullInt64{Int64: 5, Valid: true}}
			payload := `{"usage":` + tc.usage + `,"meta":{"accuracy":"` + tc.accuracy + `"}}`
			if err := decodeTeamTelemetryPayload(&ev, []byte(payload)); err != nil {
				t.Fatal(err)
			}
			total, supported := tokenContribution(ev)
			if total.String() != tc.total || supported != tc.supported {
				t.Fatalf("total=%s supported=%v", total, supported)
			}
		})
	}
}

func teamTestCost(t *testing.T, id uint64, at time.Time, scope []byte, units, source, model string) teamFactEvent {
	t.Helper()
	ev := teamFactEvent{eventPK: id, userID: "u", installationID: "i", agentID: "codex", eventType: "cost_recorded", occurredAt: at, receivedAt: at,
		costScopeKey: scope, providerID: sql.NullString{String: "openai", Valid: true}, modelID: sql.NullString{String: model, Valid: true}}
	data, _ := json.Marshal(map[string]any{"cost": map[string]any{"units": units, "source": source, "currency": "USD"}})
	if err := decodeTeamTelemetryPayload(&ev, data); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestTeamTelemetryCostScopeSelection(t *testing.T) {
	at := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	member := teamMemberSource{membershipID: "m", userID: "u", joinedAt: at.Add(-48 * time.Hour), accountOK: true}
	var grants []teamGrantWindow
	for _, dim := range []string{"base", "cost", "named", "classification"} {
		grants = append(grants, teamGrantWindow{dimension: dim, startsAt: member.joinedAt})
	}
	estimate := teamTestCost(t, 1, at.Add(-time.Minute), []byte{1}, "100000000", "calculated_price", "model-a")
	olderBill := teamTestCost(t, 2, at, []byte{1}, "200000000", "provider_reported", "model-b")
	bill := teamTestCost(t, 3, at, []byte{1}, "300000000", "provider_reported", "model-c")
	unscoped := teamTestCost(t, 4, at, nil, "1", "calculated_price", "model-a")
	for _, tc := range []struct {
		name                string
		events              []teamFactEvent
		reported, estimated string
	}{
		{"estimates add without a bill", []teamFactEvent{estimate, unscoped}, "0.00000000", "1.00000001"},
		{"latest bill replaces scope across models", []teamFactEvent{bill, estimate, olderBill, unscoped}, "3.00000000", "0.00000001"},
		{"unscoped costs stay independent", []teamFactEvent{unscoped, teamTestCost(t, 5, at, nil, "2", "provider_reported", "model-a")}, "0.00000002", "0.00000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reported, estimated := new(big.Rat), new(big.Rat)
			for _, row := range buildMemberAnalysisRows(member, grants, tc.events, time.UTC) {
				reported.Add(reported, row.reportedCost)
				estimated.Add(estimated, row.estimatedCost)
			}
			if reported.FloatString(8) != tc.reported || estimated.FloatString(8) != tc.estimated {
				t.Fatalf("reported=%s estimated=%s", reported.FloatString(8), estimated.FloatString(8))
			}
		})
	}
	bill.outsideAnalysisRange = true
	rows := buildMemberAnalysisRows(member, grants, []teamFactEvent{estimate, bill}, time.UTC)
	if len(rows) != 0 {
		t.Fatal("a bill outside the requested range must still replace the earlier estimate")
	}
}

func TestTeamTelemetryCostAuthorizationBoundaries(t *testing.T) {
	at := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	for _, dimension := range []string{"base", "cost"} {
		for _, boundary := range []string{"before start", "at end", "revoked", "before join", "at start"} {
			t.Run(dimension+"/"+boundary, func(t *testing.T) {
				member := teamMemberSource{membershipID: "m", userID: "u", joinedAt: at.Add(-time.Hour), accountOK: true}
				grants := []teamGrantWindow{{dimension: "base", startsAt: member.joinedAt}, {dimension: "cost", startsAt: member.joinedAt}}
				for i := range grants {
					if grants[i].dimension == dimension {
						switch boundary {
						case "before start":
							grants[i].startsAt = at.Add(time.Millisecond)
						case "at end":
							grants[i].endsAt = sql.NullTime{Time: at, Valid: true}
						case "revoked":
							grants[i].revokedAt = sql.NullTime{Time: at.Add(time.Minute), Valid: true}
						case "at start":
							grants[i].startsAt = at
						case "before join":
							member.joinedAt = at.Add(time.Millisecond)
						}
					}
				}
				cost := teamTestCost(t, 1, at, []byte{1}, "200000000", "provider_reported", "model-a")
				usage := teamFactEvent{eventPK: 2, installationID: "i", agentID: "codex", eventType: "model_usage_recorded", occurredAt: at.Add(time.Minute), telemetry: true, costScopeKey: []byte{1}}
				total := new(big.Rat)
				for _, row := range buildMemberAnalysisRows(member, grants, []teamFactEvent{cost, usage}, time.UTC) {
					total.Add(total, row.reportedCost)
				}
				want := "0"
				if boundary == "at start" || boundary == "before start" || boundary == "before join" {
					want = "2"
				}
				if total.RatString() != want {
					t.Fatalf("cost=%s want=%s", total.RatString(), want)
				}
			})
		}
	}
}

func TestTeamTelemetryCostsKeepTheirOwnVisibilityAndDate(t *testing.T) {
	at := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	member := teamMemberSource{membershipID: "m", userID: "u", joinedAt: at.Add(-time.Hour), accountOK: true}
	grants := []teamGrantWindow{{dimension: "base", startsAt: member.joinedAt}, {dimension: "cost", startsAt: member.joinedAt}, {dimension: "named", startsAt: at}, {dimension: "classification", startsAt: at}}
	cost := teamTestCost(t, 1, at.Add(-time.Minute), []byte{1}, "200000000", "provider_reported", "private-model")
	usage := cost
	usage.eventPK = 2
	usage.eventType = "model_usage_recorded"
	usage.occurredAt = at
	usage.costAmount = sql.NullString{}
	for _, row := range buildMemberAnalysisRows(member, grants, []teamFactEvent{cost, usage}, time.UTC) {
		if row.reportedCost.Sign() > 0 && (row.visibilityMask != visCost|visNamed|visClassification || deref(row.metricDate) != "2026-09-11" || row.reportedCovered.Sign() != 0) {
			t.Fatalf("cost borrowed usage visibility/date: %+v", row)
		}
	}
}
