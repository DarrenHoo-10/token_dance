package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

const (
	teamAnalysisRuleVersion    = "1"
	teamAnalysisLeaseDuration  = 120 * time.Second
	teamAnalysisLeaseRenew     = 30 * time.Second
	teamAnalysisMaxAttempts    = 8
	teamAnalysisRowBatch       = 200
	teamAnalysisRetryDelay     = 5 * time.Second
	visNamed                   = uint32(1 << 0)
	visClassification          = uint32(1 << 1)
	visCost                    = uint32(1 << 2)
	unsharedClassificationBucket = "unshared_classification"
	unknownClassificationBucket  = "unknown"
)

type teamAnalysisClaim struct {
	snapshotID      string
	teamID          string
	fromDate        time.Time
	toDateExclusive time.Time
	authRevision    uint64
	sourceRevision  uint64
	ruleVersion     string
	asOf            time.Time
	leaseToken      string
	leaseGeneration uint64
	attemptCount    uint16
}

type teamMemberSource struct {
	membershipID string
	userID       string
	joinedAt     time.Time
	accountOK    bool
}

type teamGrantWindow struct {
	membershipID string
	dimension    string
	startsAt     time.Time
	endsAt       sql.NullTime
	revokedAt    sql.NullTime
}

type teamFactEvent struct {
	eventPK        uint64
	userID         string
	installationID string
	agentID        string
	providerID     sql.NullString
	modelID        sql.NullString
	eventType      string
	accuracy       string
	occurredAt     time.Time
	receivedAt     time.Time
	sessionHash    []byte
	turnHash       []byte
	tokenInput     sql.NullInt64
	tokenOutput    sql.NullInt64
	tokenCacheRead sql.NullInt64
	tokenCacheWrite sql.NullInt64
	tokenReasoning sql.NullInt64
	tokenTotal     sql.NullInt64
	costAmount     sql.NullString
	costCurrency   sql.NullString
	costSource     sql.NullString
}

type analysisAggRow struct {
	membershipID               *string
	metricDate                 *string
	visibilityMask             uint32
	agentID                    *string
	providerID                 *string
	modelID                    *string
	currency                   *string
	tokenExact                 *big.Int
	tokenDerived               *big.Int
	usageEvents                *big.Int
	tokenSupported             *big.Int
	reportedCost               *big.Rat
	estimatedCost              *big.Rat
	reportedCostEvents         *big.Int
	estimatedCostEvents        *big.Int
	reportedCovered            *big.Int
	estimatedCovered           *big.Int
	unattributedCost           *big.Int
	maxReceivedAt              *time.Time
	usageIDsReported           map[uint64]struct{}
	usageIDsEstimated          map[uint64]struct{}
}

type analysisPublishDecision int

const (
	analysisPublishReady analysisPublishDecision = iota
	analysisPublishDiscardAuth
	analysisPublishBlocked
	analysisPublishStaleLease
	analysisPublishDissolved
)

func (w *Worker) ProcessTeamAnalysis(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	completed := 0
	var firstErr error
	for i := 0; i < 2; i++ {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		claim, err := w.claimTeamAnalysis(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		if err := w.executeTeamAnalysis(ctx, claim); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			if markErr := w.failTeamAnalysis(context.Background(), claim, "TEAM_ANALYSIS_BUILD_FAILED"); markErr != nil && firstErr == nil {
				firstErr = fmt.Errorf("team analysis failed: %v; persist failure: %w", err, markErr)
			} else if firstErr == nil {
				firstErr = err
			}
			continue
		}
		completed++
	}
	return completed, firstErr
}

func (w *Worker) claimTeamAnalysis(ctx context.Context) (*teamAnalysisClaim, error) {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin team analysis claim: %w", err)
	}
	defer tx.Rollback()

	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	row := tx.QueryRowContext(ctx, `
		SELECT snapshot_id, team_id, from_date, to_date_exclusive, auth_revision,
		       source_revision, rule_version, as_of, attempt_count
		FROM team_analysis_snapshots
		WHERE (
		    (status = 'queued' AND next_attempt_at <= ?)
		    OR (status = 'building' AND (lease_expires_at IS NULL OR lease_expires_at < ?))
		)
		ORDER BY next_attempt_at ASC, snapshot_id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now, now)

	claim := &teamAnalysisClaim{}
	if err := row.Scan(
		&claim.snapshotID,
		&claim.teamID,
		&claim.fromDate,
		&claim.toDateExclusive,
		&claim.authRevision,
		&claim.sourceRevision,
		&claim.ruleVersion,
		&claim.asOf,
		&claim.attemptCount,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("select team analysis claim: %w", err)
	}

	if claim.attemptCount >= teamAnalysisMaxAttempts {
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_analysis_snapshots
			SET status = 'failed', active_request_key = NULL, lease_token = NULL,
			    lease_expires_at = NULL, error_code = 'TEAM_ANALYSIS_MAX_ATTEMPTS',
			    next_attempt_at = ?
			WHERE snapshot_id = ?`, now, claim.snapshotID); err != nil {
			return nil, fmt.Errorf("mark team analysis exhausted: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit exhausted team analysis: %w", err)
		}
		return nil, sql.ErrNoRows
	}

	token, err := newTeamLeaseToken("tls_")
	if err != nil {
		return nil, err
	}
	claim.leaseToken = token
	leaseUntil := now.Add(teamAnalysisLeaseDuration)

	res, err := tx.ExecContext(ctx, `
		UPDATE team_analysis_snapshots
		SET status = 'building',
		    lease_token = ?,
		    lease_generation = lease_generation + 1,
		    lease_expires_at = ?,
		    attempt_count = attempt_count + 1,
		    error_code = NULL,
		    next_attempt_at = ?
		WHERE snapshot_id = ?
		  AND (
		    (status = 'queued' AND next_attempt_at <= ?)
		    OR (status = 'building' AND (lease_expires_at IS NULL OR lease_expires_at < ?))
		  )`, token, leaseUntil, now, claim.snapshotID, now, now)
	if err != nil {
		return nil, fmt.Errorf("update team analysis claim: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return nil, fmt.Errorf("team analysis claim lost: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT lease_generation, attempt_count FROM team_analysis_snapshots WHERE snapshot_id = ?",
		claim.snapshotID,
	).Scan(&claim.leaseGeneration, &claim.attemptCount); err != nil {
		return nil, fmt.Errorf("read team analysis generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit team analysis claim: %w", err)
	}
	return claim, nil
}

func (w *Worker) executeTeamAnalysis(ctx context.Context, claim *teamAnalysisClaim) error {
	source, err := w.readTeamAnalysisSource(ctx, claim)
	if err != nil {
		return err
	}
	if source.discardAuth {
		return w.discardTeamAnalysis(ctx, claim, "TEAM_SNAPSHOT_OBSOLETE")
	}
	if err := w.writeTeamAnalysisRows(ctx, claim, source.rows); err != nil {
		return err
	}
	published, err := w.publishTeamAnalysis(ctx, claim, source)
	if err != nil {
		return err
	}
	if published && source.sourceGrew {
		return w.queueTeamAnalysisRefresh(ctx, claim, source.capturedAuth)
	}
	return nil
}

type teamAnalysisSource struct {
	capturedAuth   uint64
	capturedSource uint64
	sourceGrew     bool
	discardAuth    bool
	rows           []analysisAggRow
}

func (w *Worker) readTeamAnalysisSource(ctx context.Context, claim *teamAnalysisClaim) (*teamAnalysisSource, error) {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("begin team analysis consistent read: %w", err)
	}
	defer tx.Rollback()

	var (
		teamStatus   string
		timezoneName string
		liveAuth     uint64
		liveSource   uint64
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT t.status, t.timezone_name, t.auth_revision, COALESCE(s.source_revision, 0)
		FROM teams t
		LEFT JOIN team_source_revisions s ON s.team_id = t.team_id
		WHERE t.team_id = ?`, claim.teamID).Scan(&teamStatus, &timezoneName, &liveAuth, &liveSource); err != nil {
		return nil, fmt.Errorf("read team analysis versions: %w", err)
	}
	out := &teamAnalysisSource{
		capturedAuth:   liveAuth,
		capturedSource: liveSource,
		sourceGrew:     liveSource > claim.sourceRevision,
		discardAuth:    liveAuth != claim.authRevision || teamStatus != string(domain.TeamStatusActive),
	}
	if out.discardAuth {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit discarded team analysis read: %w", err)
		}
		return out, nil
	}

	loc, err := time.LoadLocation(timezoneName)
	if err != nil {
		return nil, fmt.Errorf("load team timezone: %w", err)
	}
	fromLocal := time.Date(claim.fromDate.Year(), claim.fromDate.Month(), claim.fromDate.Day(), 0, 0, 0, 0, loc)
	toLocal := time.Date(claim.toDateExclusive.Year(), claim.toDateExclusive.Month(), claim.toDateExclusive.Day(), 0, 0, 0, 0, loc)
	dataEnd := toLocal
	if !claim.asOf.After(dataEnd) {
		dataEnd = claim.asOf.In(loc)
	}

	members, err := loadTeamAnalysisMembers(ctx, tx, claim.teamID)
	if err != nil {
		return nil, err
	}
	grants, err := loadTeamAnalysisGrants(ctx, tx, claim.teamID)
	if err != nil {
		return nil, err
	}

	rows := make([]analysisAggRow, 0)
	lastRenew := w.clk.Now()
	for i := range members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if w.clk.Now().Sub(lastRenew) >= teamAnalysisLeaseRenew {
			if err := w.renewTeamAnalysisLease(ctx, claim); err != nil {
				return nil, err
			}
			lastRenew = w.clk.Now()
		}
		memberRows, err := aggregateMemberAnalysis(ctx, tx, members[i], grants[members[i].membershipID], fromLocal, dataEnd, loc)
		if err != nil {
			return nil, err
		}
		rows = append(rows, memberRows...)
	}
	out.rows = rows
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit team analysis consistent read: %w", err)
	}
	return out, nil
}

func loadTeamAnalysisMembers(ctx context.Context, tx *sql.Tx, teamID string) ([]teamMemberSource, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.membership_id, m.user_id, m.joined_at, u.account_status
		FROM team_memberships m
		JOIN user_current_teams c
		  ON c.membership_id = m.membership_id AND c.team_id = m.team_id AND c.user_id = m.user_id
		JOIN users u ON u.user_id = m.user_id
		WHERE m.team_id = ? AND m.ended_at IS NULL
		ORDER BY m.user_id ASC, m.membership_id ASC`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list team analysis members: %w", err)
	}
	defer rows.Close()
	var members []teamMemberSource
	for rows.Next() {
		var m teamMemberSource
		var status string
		if err := rows.Scan(&m.membershipID, &m.userID, &m.joinedAt, &status); err != nil {
			return nil, fmt.Errorf("scan team analysis member: %w", err)
		}
		m.accountOK = status == string(domain.AccountStatusActive)
		members = append(members, m)
	}
	return members, rows.Err()
}

func loadTeamAnalysisGrants(ctx context.Context, tx *sql.Tx, teamID string) (map[string][]teamGrantWindow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT g.membership_id, g.dimension, g.starts_at, g.ends_at, g.revoked_at
		FROM team_sharing_grants g
		JOIN team_memberships m ON m.membership_id = g.membership_id
		WHERE m.team_id = ? AND g.revoked_at IS NULL`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list team analysis grants: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]teamGrantWindow)
	for rows.Next() {
		var g teamGrantWindow
		if err := rows.Scan(&g.membershipID, &g.dimension, &g.startsAt, &g.endsAt, &g.revokedAt); err != nil {
			return nil, fmt.Errorf("scan team analysis grant: %w", err)
		}
		if g.revokedAt.Valid {
			continue
		}
		out[g.membershipID] = append(out[g.membershipID], g)
	}
	return out, rows.Err()
}

func aggregateMemberAnalysis(ctx context.Context, tx *sql.Tx, member teamMemberSource, grants []teamGrantWindow, from, toExclusive time.Time, loc *time.Location) ([]analysisAggRow, error) {
	if !member.accountOK {
		return nil, nil
	}
	queryFrom := from
	if member.joinedAt.After(queryFrom) {
		queryFrom = member.joinedAt
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT event_pk, user_id, installation_id, agent_id, provider_id, model_id,
		       event_type, accuracy, occurred_at, received_at, session_hash, turn_hash,
		       token_input, token_output, token_cache_read, token_cache_write, token_reasoning, token_total,
		       CAST(cost_amount AS CHAR), cost_currency, cost_source
		FROM usage_events
		WHERE user_id = ? AND occurred_at >= ? AND occurred_at < ?
		ORDER BY occurred_at ASC, event_pk ASC`, member.userID, queryFrom.UTC(), toExclusive.UTC())
	if err != nil {
		return nil, fmt.Errorf("read team analysis facts: %w", err)
	}
	defer rows.Close()

	var events []teamFactEvent
	for rows.Next() {
		var ev teamFactEvent
		if err := rows.Scan(
			&ev.eventPK, &ev.userID, &ev.installationID, &ev.agentID, &ev.providerID, &ev.modelID,
			&ev.eventType, &ev.accuracy, &ev.occurredAt, &ev.receivedAt, &ev.sessionHash, &ev.turnHash,
			&ev.tokenInput, &ev.tokenOutput, &ev.tokenCacheRead, &ev.tokenCacheWrite, &ev.tokenReasoning, &ev.tokenTotal,
			&ev.costAmount, &ev.costCurrency, &ev.costSource,
		); err != nil {
			return nil, fmt.Errorf("scan team analysis fact: %w", err)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildMemberAnalysisRows(member, grants, events, loc), nil
}

func buildMemberAnalysisRows(member teamMemberSource, grants []teamGrantWindow, events []teamFactEvent, loc *time.Location) []analysisAggRow {
	grouped := groupTeamCostEvents(events)
	acc := make(map[string]*analysisAggRow)
	for _, ev := range events {
		if ev.occurredAt.Before(member.joinedAt) {
			continue
		}
		if !grantCoversTime(grants, string(domain.SharingBase), ev.occurredAt) {
			continue
		}
		mask := analysisVisibilityMask(grants, ev.occurredAt)
		metricDate := ev.occurredAt.In(loc).Format("2006-01-02")
		agent, provider, model := classificationBuckets(ev, mask)
		currency := costCurrency(ev, mask)

		if ev.eventType == "model_usage_recorded" {
			row := acc[analysisRowKey(member.membershipID, metricDate, mask, agent, provider, model, currency)]
			if row == nil {
				row = newAnalysisAggRow(member.membershipID, metricDate, mask, agent, provider, model, currency)
				acc[analysisRowKey(member.membershipID, metricDate, mask, agent, provider, model, currency)] = row
			}
			row.usageEvents.Add(row.usageEvents, big.NewInt(1))
			touchReceived(row, ev.receivedAt)
			tokens, supported := tokenContribution(ev)
			if supported {
				row.tokenSupported.Add(row.tokenSupported, big.NewInt(1))
				if ev.accuracy == "exact" {
					row.tokenExact.Add(row.tokenExact, tokens)
				} else if ev.accuracy == "derived" {
					row.tokenDerived.Add(row.tokenDerived, tokens)
				}
			}
		}
	}

	for _, group := range grouped {
		if len(group.events) == 0 {
			continue
		}
		sample := group.events[0]
		if sample.occurredAt.Before(member.joinedAt) || !grantCoversTime(grants, string(domain.SharingBase), sample.occurredAt) {
			continue
		}
		mask := analysisVisibilityMask(grants, sample.occurredAt)
		metricDate := sample.occurredAt.In(loc).Format("2006-01-02")
		agent, provider, model := classificationBuckets(sample, mask)
		currency := group.currency
		if mask&visCost == 0 {
			currency = ""
		}
		row := acc[analysisRowKey(member.membershipID, metricDate, mask, agent, provider, model, currency)]
		if row == nil {
			row = newAnalysisAggRow(member.membershipID, metricDate, mask, agent, provider, model, currency)
			acc[analysisRowKey(member.membershipID, metricDate, mask, agent, provider, model, currency)] = row
		}
		applyCostGroup(row, group, mask)
		for _, ev := range group.events {
			touchReceived(row, ev.receivedAt)
		}
	}

	out := make([]analysisAggRow, 0, len(acc))
	for _, row := range acc {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		return analysisRowKey(deref(out[i].membershipID), deref(out[i].metricDate), out[i].visibilityMask, deref(out[i].agentID), deref(out[i].providerID), deref(out[i].modelID), deref(out[i].currency)) <
			analysisRowKey(deref(out[j].membershipID), deref(out[j].metricDate), out[j].visibilityMask, deref(out[j].agentID), deref(out[j].providerID), deref(out[j].modelID), deref(out[j].currency))
	})
	return out
}

type teamCostGroup struct {
	currency string
	events   []teamFactEvent
}

func eventHasCostAmount(ev teamFactEvent) bool {
	return ev.costAmount.Valid && strings.TrimSpace(ev.costAmount.String) != ""
}

func groupTeamCostEvents(events []teamFactEvent) []teamCostGroup {
	type assoc struct {
		user, installation, agent, session, turn string
	}
	assocOf := func(ev teamFactEvent) (assoc, bool) {
		if len(ev.sessionHash) == 0 || len(ev.turnHash) == 0 {
			return assoc{}, false
		}
		return assoc{
			user: ev.userID, installation: ev.installationID, agent: ev.agentID,
			session: hex.EncodeToString(ev.sessionHash), turn: hex.EncodeToString(ev.turnHash),
		}, true
	}

	usages := map[assoc][]teamFactEvent{}
	costs := map[assoc]map[string][]teamFactEvent{}
	var unattributed []teamFactEvent
	for _, ev := range events {
		if ev.eventType != "model_usage_recorded" && ev.eventType != "cost_recorded" {
			continue
		}
		k, ok := assocOf(ev)
		currency := ""
		if ev.costCurrency.Valid {
			currency = ev.costCurrency.String
		}
		if ev.eventType == "model_usage_recorded" {
			if ok {
				usages[k] = append(usages[k], ev)
			}
			continue
		}
		if !eventHasCostAmount(ev) || !ok || currency == "" {
			unattributed = append(unattributed, ev)
			continue
		}
		if costs[k] == nil {
			costs[k] = map[string][]teamFactEvent{}
		}
		costs[k][currency] = append(costs[k][currency], ev)
	}

	out := make([]teamCostGroup, 0, len(costs)+len(usages)+len(unattributed))
	attached := map[assoc]bool{}
	for k, byCur := range costs {
		currencies := sortedKeys(byCur)
		for i, currency := range currencies {
			evs := append([]teamFactEvent{}, byCur[currency]...)
			if i == 0 {
				evs = append(append([]teamFactEvent{}, usages[k]...), evs...)
				attached[k] = true
			}
			out = append(out, teamCostGroup{currency: currency, events: evs})
		}
	}
	for k, evs := range usages {
		if attached[k] {
			continue
		}
		byCur := map[string][]teamFactEvent{}
		var noAmount []teamFactEvent
		for _, ev := range evs {
			if !eventHasCostAmount(ev) {
				noAmount = append(noAmount, ev)
				continue
			}
			currency := ""
			if ev.costCurrency.Valid {
				currency = ev.costCurrency.String
			}
			if currency == "" {
				unattributed = append(unattributed, ev)
				continue
			}
			byCur[currency] = append(byCur[currency], ev)
		}
		if len(byCur) == 0 {
			continue
		}
		currencies := sortedKeys(byCur)
		for i, currency := range currencies {
			group := append([]teamFactEvent{}, byCur[currency]...)
			if i == 0 {
				group = append(append([]teamFactEvent{}, noAmount...), group...)
			}
			out = append(out, teamCostGroup{currency: currency, events: group})
		}
	}
	for _, ev := range unattributed {
		currency := ""
		if ev.costCurrency.Valid {
			currency = ev.costCurrency.String
		}
		out = append(out, teamCostGroup{currency: currency, events: []teamFactEvent{ev}})
	}
	return out
}

func sortedKeys(m map[string][]teamFactEvent) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func applyCostGroup(row *analysisAggRow, group teamCostGroup, mask uint32) {
	hasReportedTotal := false
	hasUsageCost := false
	hasEstimated := false
	hasCostRecorded := false
	reported := new(big.Rat)
	estimated := new(big.Rat)
	var usageIDs []uint64
	for _, ev := range group.events {
		if ev.eventType == "model_usage_recorded" {
			usageIDs = append(usageIDs, ev.eventPK)
			if ev.costAmount.Valid {
				hasUsageCost = true
			}
		}
		if ev.eventType == "cost_recorded" {
			hasCostRecorded = true
		}
		amount, ok := parseDecimalRat(nullString(ev.costAmount))
		if !ok {
			if ev.eventType == "cost_recorded" || ev.costAmount.Valid {
				row.unattributedCost.Add(row.unattributedCost, big.NewInt(1))
			}
			continue
		}
		switch ev.costSource.String {
		case "provider_reported":
			if ev.eventType == "cost_recorded" {
				hasReportedTotal = true
			}
			reported.Add(reported, amount)
			row.reportedCostEvents.Add(row.reportedCostEvents, big.NewInt(1))
		case "estimated_price_table":
			hasEstimated = true
			estimated.Add(estimated, amount)
			row.estimatedCostEvents.Add(row.estimatedCostEvents, big.NewInt(1))
		default:
			row.unattributedCost.Add(row.unattributedCost, big.NewInt(1))
		}
	}

	attributed := len(group.events) > 0 && len(group.events[0].sessionHash) > 0 && len(group.events[0].turnHash) > 0
	if !attributed {
		row.unattributedCost.Add(row.unattributedCost, big.NewInt(int64(len(group.events))))
		return
	}
	if hasReportedTotal && hasUsageCost {
		row.unattributedCost.Add(row.unattributedCost, big.NewInt(1))
		return
	}
	if mask&visCost == 0 {
		return
	}
	if hasReportedTotal || (!hasEstimated && reported.Sign() > 0) {
		row.reportedCost.Add(row.reportedCost, reported)
		for _, id := range usageIDs {
			if _, ok := row.usageIDsReported[id]; !ok {
				row.usageIDsReported[id] = struct{}{}
				row.reportedCovered.Add(row.reportedCovered, big.NewInt(1))
			}
		}
		return
	}
	if hasEstimated && !hasCostRecorded && reported.Sign() == 0 {
		row.estimatedCost.Add(row.estimatedCost, estimated)
		for _, id := range usageIDs {
			if _, ok := row.usageIDsEstimated[id]; !ok {
				row.usageIDsEstimated[id] = struct{}{}
				row.estimatedCovered.Add(row.estimatedCovered, big.NewInt(1))
			}
		}
	}
}

func (w *Worker) writeTeamAnalysisRows(ctx context.Context, claim *teamAnalysisClaim, rows []analysisAggRow) error {
	if len(rows) == 0 {
		return nil
	}
	for start := 0; start < len(rows); start += teamAnalysisRowBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + teamAnalysisRowBatch
		if end > len(rows) {
			end = len(rows)
		}
		if err := w.insertTeamAnalysisRowBatch(ctx, claim, rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) insertTeamAnalysisRowBatch(ctx context.Context, claim *teamAnalysisClaim, rows []analysisAggRow) error {
	var b strings.Builder
	b.WriteString(`INSERT INTO team_analysis_rows (
		snapshot_id, build_generation, row_key, membership_id, metric_date, visibility_mask,
		agent_id, provider_id, model_id, currency, token_exact_total, token_derived_total,
		usage_event_count, token_supported_event_count, reported_cost_amount, estimated_cost_amount,
		reported_cost_event_count, estimated_cost_event_count, reported_covered_usage_count,
		estimated_covered_usage_count, unattributed_cost_count, max_received_at
	) VALUES `)
	args := make([]interface{}, 0, len(rows)*22)
	for i, row := range rows {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
		args = append(args,
			claim.snapshotID,
			claim.leaseGeneration,
			analysisRowKey(deref(row.membershipID), deref(row.metricDate), row.visibilityMask, deref(row.agentID), deref(row.providerID), deref(row.modelID), deref(row.currency)),
			nullStringPtr(row.membershipID),
			nullStringPtr(row.metricDate),
			row.visibilityMask,
			nullStringPtr(row.agentID),
			nullStringPtr(row.providerID),
			nullStringPtr(row.modelID),
			nullStringPtr(row.currency),
			formatBigInt(row.tokenExact),
			formatBigInt(row.tokenDerived),
			formatBigInt(row.usageEvents),
			formatBigInt(row.tokenSupported),
			formatMoney(row.reportedCost),
			formatMoney(row.estimatedCost),
			formatBigInt(row.reportedCostEvents),
			formatBigInt(row.estimatedCostEvents),
			formatBigInt(row.reportedCovered),
			formatBigInt(row.estimatedCovered),
			formatBigInt(row.unattributedCost),
			nullTimePtr(row.maxReceivedAt),
		)
	}
	if _, err := w.db.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("insert team analysis rows: %w", err)
	}
	return nil
}

func (w *Worker) publishTeamAnalysis(ctx context.Context, claim *teamAnalysisClaim, source *teamAnalysisSource) (bool, error) {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, fmt.Errorf("begin team analysis publish: %w", err)
	}
	defer tx.Rollback()

	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	var teamStatus string
	var liveAuth uint64
	if err := tx.QueryRowContext(ctx, `
		SELECT status, auth_revision
		FROM teams
		WHERE team_id = ?
		FOR UPDATE`, claim.teamID).Scan(&teamStatus, &liveAuth); err != nil {
		return false, fmt.Errorf("lock team for analysis publish: %w", err)
	}

	var (
		status          string
		leaseToken      sql.NullString
		leaseGeneration uint64
		leaseExpiresAt  sql.NullTime
		rowAuth         uint64
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT status, lease_token, lease_generation, lease_expires_at, auth_revision
		FROM team_analysis_snapshots
		WHERE snapshot_id = ?
		FOR UPDATE`, claim.snapshotID).Scan(&status, &leaseToken, &leaseGeneration, &leaseExpiresAt, &rowAuth); err != nil {
		return false, fmt.Errorf("lock analysis snapshot for publish: %w", err)
	}

	decision := decideAnalysisPublish(teamStatus, liveAuth, claim, source, status, leaseToken, leaseGeneration, leaseExpiresAt, rowAuth, now)
	switch decision {
	case analysisPublishDiscardAuth, analysisPublishDissolved:
		if err := obsoleteTeamAnalysisTx(ctx, tx, claim.snapshotID, now, "TEAM_SNAPSHOT_OBSOLETE"); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit obsolete team analysis: %w", err)
		}
		return false, nil
	case analysisPublishStaleLease:
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit stale team analysis lease: %w", err)
		}
		return false, fmt.Errorf("team analysis lease fenced")
	case analysisPublishBlocked:
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_analysis_snapshots
			SET next_attempt_at = ?, error_code = 'TEAM_DELETION_BARRIER'
			WHERE snapshot_id = ? AND lease_token = ? AND lease_generation = ?`,
			now.Add(teamAnalysisRetryDelay), claim.snapshotID, claim.leaseToken, claim.leaseGeneration); err != nil {
			return false, fmt.Errorf("defer barred team analysis: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit barred team analysis: %w", err)
		}
		return false, nil
	}

	var openBarriers int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM team_deletion_barriers
		WHERE team_id = ? AND released_at IS NULL`, claim.teamID).Scan(&openBarriers); err != nil {
		return false, fmt.Errorf("count team deletion barriers: %w", err)
	}
	if openBarriers > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_analysis_snapshots
			SET next_attempt_at = ?, error_code = 'TEAM_DELETION_BARRIER'
			WHERE snapshot_id = ? AND lease_token = ? AND lease_generation = ?`,
			now.Add(teamAnalysisRetryDelay), claim.snapshotID, claim.leaseToken, claim.leaseGeneration); err != nil {
			return false, fmt.Errorf("defer barred team analysis: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit barred team analysis: %w", err)
		}
		return false, nil
	}

	expiresAt := now.Add(30 * time.Minute)
	res, err := tx.ExecContext(ctx, `
		UPDATE team_analysis_snapshots
		SET status = 'ready',
		    source_revision = ?,
		    published_generation = ?,
		    active_request_key = NULL,
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    error_code = NULL,
		    as_of = ?,
		    expires_at = ?
		WHERE snapshot_id = ?
		  AND lease_token = ?
		  AND lease_generation = ?
		  AND auth_revision = ?`,
		source.capturedSource, claim.leaseGeneration, now, expiresAt,
		claim.snapshotID, claim.leaseToken, claim.leaseGeneration, claim.authRevision)
	if err != nil {
		return false, fmt.Errorf("publish team analysis: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return false, fmt.Errorf("publish team analysis: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit team analysis publish: %w", err)
	}
	return true, nil
}

func decideAnalysisPublish(
	teamStatus string,
	liveAuth uint64,
	claim *teamAnalysisClaim,
	source *teamAnalysisSource,
	status string,
	leaseToken sql.NullString,
	leaseGeneration uint64,
	leaseExpiresAt sql.NullTime,
	rowAuth uint64,
	now time.Time,
) analysisPublishDecision {
	if teamStatus != string(domain.TeamStatusActive) {
		return analysisPublishDissolved
	}
	if liveAuth != claim.authRevision || rowAuth != claim.authRevision || source.capturedAuth != claim.authRevision {
		return analysisPublishDiscardAuth
	}
	if status != string(domain.SnapshotBuilding) || !leaseToken.Valid || leaseToken.String != claim.leaseToken || leaseGeneration != claim.leaseGeneration {
		return analysisPublishStaleLease
	}
	if !leaseExpiresAt.Valid || !leaseExpiresAt.Time.After(now) {
		return analysisPublishStaleLease
	}
	return analysisPublishReady
}

func (w *Worker) discardTeamAnalysis(ctx context.Context, claim *teamAnalysisClaim, code string) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE team_analysis_snapshots
		SET status = 'obsolete', active_request_key = NULL, lease_token = NULL,
		    lease_expires_at = NULL, error_code = ?
		WHERE snapshot_id = ? AND lease_token = ? AND lease_generation = ?`,
		code, claim.snapshotID, claim.leaseToken, claim.leaseGeneration)
	if err != nil {
		return fmt.Errorf("discard team analysis: %w", err)
	}
	return nil
}

func obsoleteTeamAnalysisTx(ctx context.Context, tx *sql.Tx, snapshotID string, now time.Time, code string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE team_analysis_snapshots
		SET status = 'obsolete', active_request_key = NULL, lease_token = NULL,
		    lease_expires_at = NULL, error_code = ?, next_attempt_at = ?
		WHERE snapshot_id = ?`, code, now, snapshotID)
	if err != nil {
		return fmt.Errorf("obsolete team analysis: %w", err)
	}
	return nil
}

func (w *Worker) failTeamAnalysis(ctx context.Context, claim *teamAnalysisClaim, code string) error {
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	status := "queued"
	activeKeyExpr := "active_request_key"
	if claim.attemptCount >= teamAnalysisMaxAttempts {
		status = "failed"
		activeKeyExpr = "NULL"
	}
	query := fmt.Sprintf(`
		UPDATE team_analysis_snapshots
		SET status = ?, active_request_key = %s, lease_token = NULL, lease_expires_at = NULL,
		    error_code = ?, next_attempt_at = ?
		WHERE snapshot_id = ? AND lease_token = ? AND lease_generation = ?`, activeKeyExpr)
	_, err := w.db.ExecContext(ctx, query, status, code, now.Add(teamAnalysisRetryDelay),
		claim.snapshotID, claim.leaseToken, claim.leaseGeneration)
	if err != nil {
		return fmt.Errorf("fail team analysis: %w", err)
	}
	return nil
}

func (w *Worker) queueTeamAnalysisRefresh(ctx context.Context, claim *teamAnalysisClaim, authRevision uint64) error {
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	requestKey := teamAnalysisRequestKey(claim.teamID, claim.fromDate, claim.toDateExclusive, authRevision, claim.ruleVersion)
	var existing string
	err := w.db.QueryRowContext(ctx, `
		SELECT snapshot_id FROM team_analysis_snapshots
		WHERE active_request_key = ?`, requestKey).Scan(&existing)
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup team analysis refresh: %w", err)
	}
	snapshotID, err := newPrefixedTeamID(domain.SnapshotIDPrefix)
	if err != nil {
		return fmt.Errorf("allocate refresh snapshot id: %w", err)
	}
	_, err = w.db.ExecContext(ctx, `
		INSERT INTO team_analysis_snapshots (
			snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision,
			rule_version, status, active_request_key, as_of, next_attempt_at, expires_at
		) VALUES (?, ?, ?, ?, ?, 0, ?, 'queued', ?, ?, ?, ?)`,
		snapshotID, claim.teamID, claim.fromDate.Format("2006-01-02"), claim.toDateExclusive.Format("2006-01-02"),
		authRevision, claim.ruleVersion, requestKey, now, now, now.Add(30*time.Minute))
	if err != nil && !isDuplicateKeyError(err) {
		return fmt.Errorf("queue team analysis refresh: %w", err)
	}
	return nil
}

func (w *Worker) renewTeamAnalysisLease(ctx context.Context, claim *teamAnalysisClaim) error {
	until := w.clk.Now().UTC().Add(teamAnalysisLeaseDuration)
	res, err := w.db.ExecContext(ctx, `
		UPDATE team_analysis_snapshots
		SET lease_expires_at = ?
		WHERE snapshot_id = ? AND lease_token = ? AND lease_generation = ? AND status = 'building'`,
		until, claim.snapshotID, claim.leaseToken, claim.leaseGeneration)
	if err != nil {
		return fmt.Errorf("renew team analysis lease: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return fmt.Errorf("renew team analysis lease: %w", err)
	}
	return nil
}

func analysisVisibilityMask(grants []teamGrantWindow, at time.Time) uint32 {
	var mask uint32
	if grantCoversTime(grants, string(domain.SharingNamed), at) {
		mask |= visNamed
	}
	if grantCoversTime(grants, string(domain.SharingClassification), at) {
		mask |= visClassification
	}
	if grantCoversTime(grants, string(domain.SharingCost), at) {
		mask |= visCost
	}
	return mask
}

func grantCoversTime(grants []teamGrantWindow, dimension string, at time.Time) bool {
	for _, g := range grants {
		if g.dimension != dimension {
			continue
		}
		if g.revokedAt.Valid {
			continue
		}
		if g.startsAt.After(at) {
			continue
		}
		if g.endsAt.Valid && !at.Before(g.endsAt.Time) {
			continue
		}
		return true
	}
	return false
}

func tokenContribution(ev teamFactEvent) (*big.Int, bool) {
	if ev.accuracy != "exact" && ev.accuracy != "derived" {
		return big.NewInt(0), false
	}
	if ev.tokenTotal.Valid && ev.tokenTotal.Int64 >= 0 {
		return big.NewInt(ev.tokenTotal.Int64), true
	}
	if ev.tokenInput.Valid && ev.tokenOutput.Valid && ev.tokenInput.Int64 >= 0 && ev.tokenOutput.Int64 >= 0 {
		return big.NewInt(ev.tokenInput.Int64 + ev.tokenOutput.Int64), true
	}
	return big.NewInt(0), false
}

func classificationBuckets(ev teamFactEvent, mask uint32) (agent, provider, model string) {
	if mask&visClassification == 0 {
		return unsharedClassificationBucket, "", ""
	}
	agent = ev.agentID
	if ev.providerID.Valid {
		provider = ev.providerID.String
	}
	if ev.modelID.Valid {
		model = ev.modelID.String
	}
	if provider == "" && model == "" {
		return unknownClassificationBucket, "", ""
	}
	return agent, provider, model
}

func costCurrency(ev teamFactEvent, mask uint32) string {
	if mask&visCost == 0 || !ev.costCurrency.Valid {
		return ""
	}
	return ev.costCurrency.String
}

func analysisRowKey(membershipID, metricDate string, mask uint32, agent, provider, model, currency string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00%s\x00%s\x00%s\x00%s", membershipID, metricDate, mask, agent, provider, model, currency)
	return hex.EncodeToString(h.Sum(nil))
}

func teamAnalysisRequestKey(teamID string, from, toExclusive time.Time, auth uint64, ruleVersion string) string {
	return fmt.Sprintf("%s|%s|%s|%d|%s", teamID, from.Format("2006-01-02"), toExclusive.Format("2006-01-02"), auth, ruleVersion)
}

func newAnalysisAggRow(membershipID, metricDate string, mask uint32, agent, provider, model, currency string) *analysisAggRow {
	row := &analysisAggRow{
		visibilityMask:    mask,
		tokenExact:        big.NewInt(0),
		tokenDerived:      big.NewInt(0),
		usageEvents:       big.NewInt(0),
		tokenSupported:    big.NewInt(0),
		reportedCost:      new(big.Rat),
		estimatedCost:     new(big.Rat),
		reportedCostEvents: big.NewInt(0),
		estimatedCostEvents: big.NewInt(0),
		reportedCovered:   big.NewInt(0),
		estimatedCovered:  big.NewInt(0),
		unattributedCost:  big.NewInt(0),
		usageIDsReported:  map[uint64]struct{}{},
		usageIDsEstimated: map[uint64]struct{}{},
	}
	if membershipID != "" {
		row.membershipID = &membershipID
	}
	if metricDate != "" {
		row.metricDate = &metricDate
	}
	if agent != "" {
		row.agentID = &agent
	}
	if provider != "" {
		row.providerID = &provider
	}
	if model != "" {
		row.modelID = &model
	}
	if currency != "" {
		row.currency = &currency
	}
	return row
}

func touchReceived(row *analysisAggRow, at time.Time) {
	if row.maxReceivedAt == nil || at.After(*row.maxReceivedAt) {
		copied := at
		row.maxReceivedAt = &copied
	}
}

func newPrefixedTeamID(prefix string) (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", err
	}
	id := prefix + token
	if len(id) != 30 {
		return "", fmt.Errorf("team id length %d != 30", len(id))
	}
	return id, nil
}

func newTeamLeaseToken(prefix string) (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate team lease token: %w", err)
	}
	id := prefix + token
	if len(id) > 30 {
		id = id[:30]
	}
	return id, nil
}

func formatBigInt(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}

func formatMoney(v *big.Rat) string {
	if v == nil {
		return "0.00000000"
	}
	return v.FloatString(8)
}

func parseDecimalRat(value string) (*big.Rat, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(value)
	return r, ok
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func nullStringPtr(v *string) interface{} {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func nullTimePtr(v *time.Time) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Duplicate") || strings.Contains(err.Error(), "1062")
}
