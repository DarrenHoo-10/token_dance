package worker

import (
	"database/sql"
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestGrantCoversTimeExcludesRevoked(t *testing.T) {
	start := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	revoked := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	grants := []teamGrantWindow{{
		membershipID: "tmb_1",
		dimension:    string(domain.SharingBase),
		startsAt:     start,
		endsAt:       sql.NullTime{Time: revoked, Valid: true},
		revokedAt:    sql.NullTime{Time: revoked, Valid: true},
	}}
	if grantCoversTime(grants, string(domain.SharingBase), start.Add(time.Minute)) {
		t.Fatal("revoked grant must never cover events")
	}
}

func TestBuildMemberAnalysisRowsRevokedBaseMustNotReappear(t *testing.T) {
	loc := time.UTC
	joined := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	revoked := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	member := teamMemberSource{membershipID: "tmb_1", userID: "usr_1", joinedAt: joined, accountOK: true}
	grants := []teamGrantWindow{{
		membershipID: "tmb_1",
		dimension:    string(domain.SharingBase),
		startsAt:     joined,
		endsAt:       sql.NullTime{Time: revoked, Valid: true},
		revokedAt:    sql.NullTime{Time: revoked, Valid: true},
	}}
	events := []teamFactEvent{{
		eventType:  "model_usage_recorded",
		accuracy:   "exact",
		occurredAt: time.Date(2026, 9, 6, 11, 0, 0, 0, time.UTC),
		tokenTotal: sql.NullInt64{Int64: 123, Valid: true},
	}}
	rows := buildMemberAnalysisRows(member, grants, events, loc)
	if len(rows) != 0 {
		t.Fatalf("revoked base history must not reappear, got %+v", rows)
	}
}

func TestCostOnlyRecordCoversAssociatedUsage(t *testing.T) {
	loc := time.UTC
	at := time.Date(2026, 9, 6, 11, 0, 0, 0, time.UTC)
	member := teamMemberSource{membershipID: "tmb_1", userID: "usr_1", joinedAt: at.Add(-time.Hour), accountOK: true}
	grants := []teamGrantWindow{
		{membershipID: "tmb_1", dimension: string(domain.SharingBase), startsAt: at.Add(-time.Hour)},
		{membershipID: "tmb_1", dimension: string(domain.SharingNamed), startsAt: at.Add(-time.Hour)},
		{membershipID: "tmb_1", dimension: string(domain.SharingClassification), startsAt: at.Add(-time.Hour)},
		{membershipID: "tmb_1", dimension: string(domain.SharingCost), startsAt: at.Add(-time.Hour)},
	}
	session := []byte{1, 2, 3, 4}
	turn := []byte{5, 6, 7, 8}
	rows := buildMemberAnalysisRows(member, grants, []teamFactEvent{
		{
			eventPK: 11, userID: "usr_1", installationID: "ins_1", agentID: "codex",
			eventType: "model_usage_recorded", accuracy: "exact", occurredAt: at,
			sessionHash: session, turnHash: turn,
			tokenTotal: sql.NullInt64{Int64: 123, Valid: true},
		},
		{
			eventPK: 12, userID: "usr_1", installationID: "ins_1", agentID: "codex",
			eventType: "cost_recorded", occurredAt: at,
			sessionHash: session, turnHash: turn,
			costAmount:   sql.NullString{String: "2.00000000", Valid: true},
			costCurrency: sql.NullString{String: "USD", Valid: true},
			costSource:   sql.NullString{String: "provider_reported", Valid: true},
		},
	}, loc)
	usage, covered := int64(0), int64(0)
	for _, row := range rows {
		usage += row.usageEvents.Int64()
		covered += row.reportedCovered.Int64()
	}
	if usage != 1 || covered != 1 {
		t.Fatalf("expected coverage 1/1, got %d/%d rows=%d", covered, usage, len(rows))
	}
}

func TestCurrentGrantCoversHistoricalUsage(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	grants := []teamGrantWindow{{dimension: "base", startsAt: now}}
	member := teamMemberSource{membershipID: "m", userID: "u", joinedAt: now, accountOK: true}
	at := now.Add(-7 * 24 * time.Hour)
	rows := buildMemberAnalysisRows(member, grants, []teamFactEvent{{eventType: "model_usage_recorded", accuracy: "exact", occurredAt: at, tokenTotal: sql.NullInt64{Int64: 123, Valid: true}}}, time.UTC)
	if len(rows) != 1 || rows[0].tokenExact.Int64() != 123 {
		t.Fatal("history before joining and sharing must be counted")
	}
	grants[0].endsAt = sql.NullTime{Time: now, Valid: true}
	if grantCoversTime(grants, "base", at) {
		t.Fatal("closed grant must not expose history")
	}
	if grantCoversTime(grants, "named", at) {
		t.Fatal("unshared dimension must not match")
	}
}

func TestAnalysisVisibilityMaskNamedFollowsBase(t *testing.T) {
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	grants := []teamGrantWindow{
		{dimension: string(domain.SharingBase), startsAt: at.Add(-time.Hour)},
		{dimension: string(domain.SharingCost), startsAt: at.Add(-time.Hour)},
	}
	mask := analysisVisibilityMask(grants, at)
	if mask&visNamed == 0 || mask&visCost == 0 || mask&visClassification != 0 {
		t.Fatalf("unexpected mask %b", mask)
	}
}

func TestDecideAnalysisPublishDiscardsAuthButAllowsSourceGrowth(t *testing.T) {
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	claim := &teamAnalysisClaim{
		snapshotID:      "tas_1",
		authRevision:    12,
		leaseToken:      "tls_abc",
		leaseGeneration: 3,
	}
	source := &teamAnalysisSource{capturedAuth: 12, capturedSource: 50, sourceGrew: true}
	decision := decideAnalysisPublish(
		string(domain.TeamStatusActive), 12, claim, source, string(domain.SnapshotBuilding),
		sql.NullString{String: "tls_abc", Valid: true}, 3, sql.NullTime{Time: now.Add(time.Minute), Valid: true}, 12, now,
	)
	if decision != analysisPublishReady {
		t.Fatalf("source growth should still publish, got %v", decision)
	}

	decision = decideAnalysisPublish(
		string(domain.TeamStatusActive), 13, claim, source, string(domain.SnapshotBuilding),
		sql.NullString{String: "tls_abc", Valid: true}, 3, sql.NullTime{Time: now.Add(time.Minute), Valid: true}, 12, now,
	)
	if decision != analysisPublishDiscardAuth {
		t.Fatalf("auth revision change must discard, got %v", decision)
	}
}

func TestTokenContributionDoesNotInventZeros(t *testing.T) {
	tokens, ok := tokenContribution(teamFactEvent{accuracy: "exact"})
	if ok || tokens.Sign() != 0 {
		t.Fatal("missing totals should be unsupported, not zero")
	}
	tokens, ok = tokenContribution(teamFactEvent{
		accuracy:   "exact",
		tokenTotal: sql.NullInt64{Int64: 12, Valid: true},
	})
	if !ok || tokens.Int64() != 12 {
		t.Fatalf("exact total not used: ok=%v tokens=%s", ok, tokens)
	}
}

func TestClassificationBucketsKeepUnsharedAndUnknownDistinct(t *testing.T) {
	agent, provider, model := classificationBuckets(teamFactEvent{agentID: "codex", providerID: sql.NullString{String: "openai", Valid: true}}, 0)
	if agent != unsharedClassificationBucket || provider != "" || model != "" {
		t.Fatalf("unshared classification leaked identifiers: %s %s %s", agent, provider, model)
	}
	agent, provider, model = classificationBuckets(teamFactEvent{agentID: "codex"}, visClassification)
	if agent != unknownClassificationBucket {
		t.Fatalf("authorized but unnamed source should be unknown, got %s", agent)
	}
}

func TestReadySnapshotNeverMatchesDifferentAuthRevision(t *testing.T) {
	now := time.Now().UTC()
	claim := &teamAnalysisClaim{authRevision: 4, leaseToken: "tls_x", leaseGeneration: 1}
	source := &teamAnalysisSource{capturedAuth: 5}
	decision := decideAnalysisPublish(
		string(domain.TeamStatusActive), 5, claim, source, string(domain.SnapshotBuilding),
		sql.NullString{String: "tls_x", Valid: true}, 1, sql.NullTime{Time: now.Add(time.Minute), Valid: true}, 4, now,
	)
	if decision != analysisPublishDiscardAuth {
		t.Fatalf("ready publish for a different auth_revision is forbidden, got %v", decision)
	}
}
