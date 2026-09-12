package worker

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
)

// Team analysis reads the same immutable v2 facts as personal aggregation.
// Legacy usage_events are deliberately not combined with this stream.
func readTeamTelemetry(ctx context.Context, tx *sql.Tx, userID string, from, to, asOf time.Time) ([]teamFactEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.user_id, e.installation_id, e.harness_id, m.provider_id, m.model_id,
		       e.event_type, e.occurred_at, e.created_at, e.cost_scope_key, e.payload_json
		FROM telemetry_events e
		LEFT JOIN telemetry_models m ON m.id = e.model_key
		WHERE e.user_id = ? AND e.delete_at IS NULL
		  AND e.occurred_at < ?
		  AND (e.event_type = 'cost_recorded' OR (e.occurred_at >= ? AND e.occurred_at < ?))
		  AND e.schema_version = 2 AND e.metric_semantics_version = 1
		ORDER BY e.occurred_at, e.id`, userID, asOf.UnixMilli(), from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("read team telemetry: %w", err)
	}
	defer rows.Close()
	var events []teamFactEvent
	for rows.Next() {
		var ev teamFactEvent
		var occurred, received int64
		var payloadJSON []byte
		if err := rows.Scan(&ev.eventPK, &ev.userID, &ev.installationID, &ev.agentID,
			&ev.providerID, &ev.modelID, &ev.eventType, &occurred, &received, &ev.costScopeKey, &payloadJSON); err != nil {
			return nil, fmt.Errorf("scan team telemetry: %w", err)
		}
		ev.occurredAt, ev.receivedAt = time.UnixMilli(occurred).UTC(), time.UnixMilli(received).UTC()
		// Read the full cost scope before applying the requested date range: a
		// later bill can replace an estimate in an earlier day.
		ev.outsideAnalysisRange = ev.occurredAt.Before(from) || !ev.occurredAt.Before(to)
		if err := decodeTeamTelemetryPayload(&ev, payloadJSON); err != nil {
			return nil, fmt.Errorf("decode team telemetry %d: %w", ev.eventPK, err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func decodeTeamTelemetryPayload(ev *teamFactEvent, data []byte) error {
	var payload v2.EventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	ev.telemetry = true
	if payload.Meta != nil {
		ev.accuracy = string(payload.Meta.Accuracy)
	}
	if payload.Usage != nil && payload.Usage.TokenTotal != nil {
		n, ok := new(big.Int).SetString(string(*payload.Usage.TokenTotal), 10)
		if !ok || n.Sign() < 0 || n.BitLen() > 64 {
			return fmt.Errorf("invalid token_total")
		}
		ev.telemetryTotal = n
	}
	if ev.eventType == "cost_recorded" && payload.Cost != nil {
		n, ok := new(big.Int).SetString(string(payload.Cost.Units), 10)
		if !ok || n.Sign() < 0 || n.BitLen() > 64 {
			return fmt.Errorf("invalid cost units")
		}
		ev.costAmount = sql.NullString{String: new(big.Rat).SetFrac(n, big.NewInt(100000000)).FloatString(8), Valid: true}
		ev.costCurrency = sql.NullString{String: payload.Cost.Currency, Valid: true}
		ev.costSource = sql.NullString{String: string(payload.Cost.Source), Valid: true}
	}
	return nil
}

func teamFactAuthorized(member teamMemberSource, grants []teamGrantWindow, ev teamFactEvent) bool {
	return !ev.occurredAt.Before(member.joinedAt) && grantCoversTime(grants, string(domain.SharingBase), ev.occurredAt)
}

// Never let an associated usage event lend its timestamp, identity or
// classification authorization to a legacy cost event.
func groupAuthorizedTeamCosts(member teamMemberSource, grants []teamGrantWindow, events []teamFactEvent, loc *time.Location) []teamCostGroup {
	partitions := make(map[string][]teamFactEvent)
	for _, ev := range events {
		if ev.telemetry || !teamFactAuthorized(member, grants, ev) {
			continue
		}
		mask := analysisVisibilityMask(grants, ev.occurredAt)
		if mask&visCost == 0 {
			continue
		}
		agent, provider, model := classificationBuckets(ev, mask)
		key := analysisRowKey(member.membershipID, ev.occurredAt.In(loc).Format("2006-01-02"), mask, agent, provider, model, "")
		partitions[key] = append(partitions[key], ev)
	}
	var groups []teamCostGroup
	for _, events := range partitions {
		groups = append(groups, groupTeamCostEvents(events)...)
	}
	return groups
}

type teamTelemetryScope struct {
	installation, harness, scope string
	// A cost without a scope is independent even when session/turn keys match.
	unscopedEvent uint64
}

func telemetryScope(ev teamFactEvent) teamTelemetryScope {
	k := teamTelemetryScope{installation: ev.installationID, harness: ev.agentID, scope: hex.EncodeToString(ev.costScopeKey)}
	if len(ev.costScopeKey) == 0 {
		k.unscopedEvent = ev.eventPK
	}
	return k
}

// Apply v2 billing semantics after authorization of every individual event:
// the latest provider bill replaces all estimates in its scope, across models
// and currencies. Each selected cost keeps its own date and visibility mask.
func addTeamTelemetryCosts(acc map[string]*analysisAggRow, member teamMemberSource, grants []teamGrantWindow, events []teamFactEvent, loc *time.Location) {
	costs := make(map[teamTelemetryScope][]teamFactEvent)
	usages := make(map[teamTelemetryScope][]teamFactEvent)
	for _, ev := range events {
		if !ev.telemetry || !teamFactAuthorized(member, grants, ev) || analysisVisibilityMask(grants, ev.occurredAt)&visCost == 0 {
			continue
		}
		scope := telemetryScope(ev)
		if ev.eventType == "model_usage_recorded" && len(ev.costScopeKey) != 0 {
			usages[scope] = append(usages[scope], ev)
		}
		if ev.eventType == "cost_recorded" && eventHasCostAmount(ev) {
			costs[scope] = append(costs[scope], ev)
		}
	}
	for scope, facts := range costs {
		var bill *teamFactEvent
		for i := range facts {
			ev := &facts[i]
			if ev.costSource.String == string(v2.CostSourceProviderReported) && (bill == nil || ev.occurredAt.After(bill.occurredAt) || ev.occurredAt.Equal(bill.occurredAt) && ev.eventPK > bill.eventPK) {
				bill = ev
			}
		}
		selected := facts
		if bill != nil {
			selected = []teamFactEvent{*bill}
		}
		for _, ev := range selected {
			if ev.outsideAnalysisRange {
				continue
			}
			reported := ev.costSource.String == string(v2.CostSourceProviderReported)
			if !reported && ev.costSource.String != string(v2.CostSourceCalculatedPrice) {
				continue
			}
			amount, ok := parseDecimalRat(ev.costAmount.String)
			if !ok {
				continue
			}
			mask := analysisVisibilityMask(grants, ev.occurredAt)
			date := ev.occurredAt.In(loc).Format("2006-01-02")
			agent, provider, model := classificationBuckets(ev, mask)
			key := analysisRowKey(member.membershipID, date, mask, agent, provider, model, ev.costCurrency.String)
			row := acc[key]
			if row == nil {
				row = newAnalysisAggRow(member.membershipID, date, mask, agent, provider, model, ev.costCurrency.String)
				acc[key] = row
			}
			ids, covered := row.usageIDsEstimated, row.estimatedCovered
			if reported {
				row.reportedCost.Add(row.reportedCost, amount)
				row.reportedCostEvents.Add(row.reportedCostEvents, big.NewInt(1))
				ids, covered = row.usageIDsReported, row.reportedCovered
			} else {
				row.estimatedCost.Add(row.estimatedCost, amount)
				row.estimatedCostEvents.Add(row.estimatedCostEvents, big.NewInt(1))
			}
			touchReceived(row, ev.receivedAt)
			for _, usage := range usages[scope] {
				usageMask := analysisVisibilityMask(grants, usage.occurredAt)
				ua, up, um := classificationBuckets(usage, usageMask)
				if usageMask != mask || usage.occurredAt.In(loc).Format("2006-01-02") != date || ua != agent || up != provider || um != model {
					continue
				}
				if _, exists := ids[usage.eventPK]; !exists {
					ids[usage.eventPK] = struct{}{}
					covered.Add(covered, big.NewInt(1))
				}
			}
		}
	}
}
