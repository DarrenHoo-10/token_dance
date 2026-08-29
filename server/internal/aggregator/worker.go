package aggregator

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
)

const (
	AggregationVersion = 1
	batchSize          = 1000
)

// Worker performs incremental aggregation of usage events into daily metrics.
type Worker struct {
	Events  store.EventStore
	Metrics store.MetricStore
	Users   store.UserStore

	// LastProcessedPK tracks the high-water mark for incremental processing.
	LastProcessedPK uint64
}

// RunOnce processes a single batch of new events since LastProcessedPK.
// Returns the number of events processed.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	events, err := w.Events.ListEventsAfterPK(ctx, w.LastProcessedPK, batchSize)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}

	// Group events by (date, user_id, agent_id) for incremental merge
	type groupKey struct {
		date    time.Time
		userID  string
		agentID string
	}
	groups := make(map[groupKey][]*domain.UsageEvent)
	for _, e := range events {
		k := groupKey{date: e.OccurredDate, userID: e.UserID, agentID: e.AgentID}
		groups[k] = append(groups[k], e)
	}

	now := time.Now().UTC()
	for k, evts := range groups {
		existing, _ := w.Metrics.GetDailyMetrics(ctx, k.userID, k.date, k.date)
		var m *domain.DailyUserAgentMetric
		for _, em := range existing {
			if em.AgentID == k.agentID {
				m = em
				break
			}
		}
		if m == nil {
			m = &domain.DailyUserAgentMetric{
				MetricDate:         k.date,
				UserID:             k.userID,
				AgentID:            k.agentID,
				AggregationVersion: AggregationVersion,
				ComputedAt:         now,
			}
		}

		for _, e := range evts {
			mergeEvent(m, e)
			if e.EventPK > m.SourceMaxEventPK {
				m.SourceMaxEventPK = e.EventPK
			}
		}
		m.ComputedAt = now

		if err := w.Metrics.UpsertDailyUserAgentMetric(ctx, m); err != nil {
			log.Printf("aggregator: upsert metric error: %v", err)
		}
	}

	w.LastProcessedPK = events[len(events)-1].EventPK
	return len(events), nil
}

func mergeEvent(m *domain.DailyUserAgentMetric, e *domain.UsageEvent) {
	if e.TokenTotal != nil {
		switch e.Accuracy {
		case "exact":
			m.ExactTokenTotal += *e.TokenTotal
		case "derived":
			m.DerivedTokenTotal += *e.TokenTotal
		case "estimated", "correlated":
			m.EstimatedTokenTotal += *e.TokenTotal
		}
	}

	switch e.EventType {
	case "session_started":
		m.SessionCount++
	case "turn_completed", "turn_started":
		m.InteractionTurnCount++
	case "model_usage_recorded":
		m.ModelRequestCount++
	case "tool_invoked":
		m.ToolCallCount++
	case "skill_invoked":
		m.SkillUseCount++
	case "code_changed":
		if e.CodeAddedLines != nil {
			switch e.Accuracy {
			case "exact", "derived":
				m.CodeGeneratedLines += *e.CodeAddedLines
			case "correlated":
				m.CorrelatedCodeLines += *e.CodeAddedLines
			}
		}
	case "agent_spawned":
		m.ChildSessionCount++
	}
}

// BuildLeaderboardSnapshot creates an immutable leaderboard snapshot from
// current daily metrics. Only users with leaderboard_visibility matching
// scopeType are included.
func (w *Worker) BuildLeaderboardSnapshot(
	ctx context.Context,
	leaderboards store.LeaderboardStore,
	snapshotID, boardKey, scopeType, metricKey string,
	windowStart, windowEnd time.Time,
) error {
	// Supersede previous published snapshot for same board/scope/metric
	prev, err := leaderboards.LatestPublishedSnapshot(ctx, boardKey, scopeType, metricKey)
	if err == nil && prev != nil {
		_ = leaderboards.SupersedeSnapshot(ctx, prev.SnapshotID)
	}

	maxPK, _ := w.Events.MaxEventPK(ctx)
	now := time.Now().UTC()

	snap := &domain.LeaderboardSnapshot{
		SnapshotID:         snapshotID,
		BoardKey:           boardKey,
		ScopeType:          scopeType,
		ScopeKey:           "all",
		MetricKey:          metricKey,
		WindowStart:        windowStart,
		WindowEnd:          windowEnd,
		TimezoneName:       "UTC",
		RankingRuleVersion: 1,
		SourceMaxEventPK:   maxPK,
		DataWatermarkAt:    now,
		SnapshotStatus:     "building",
		GeneratedAt:        now,
	}
	if err := leaderboards.CreateSnapshot(ctx, snap); err != nil {
		return err
	}

	// Gather metrics in the window
	allMetrics, err := w.Metrics.GetDailyMetricsAllUsers(ctx, windowStart, windowEnd)
	if err != nil {
		return err
	}

	// Aggregate per user, filtering by visibility
	type userAgg struct {
		userID      string
		metricValue float64
		displayName string
		avatarURL   string
	}
	userMap := make(map[string]*userAgg)
	for _, m := range allMetrics {
		u, err := w.Users.GetUser(ctx, m.UserID)
		if err != nil {
			continue
		}
		// Scope filter: only include users whose visibility matches
		if !visibilityAllowed(u.LeaderboardVisibility, scopeType) {
			continue
		}
		if u.AccountStatus != "active" {
			continue
		}

		agg, ok := userMap[m.UserID]
		if !ok {
			agg = &userAgg{
				userID:      m.UserID,
				displayName: u.DisplayName,
				avatarURL:   u.AvatarURL,
			}
			userMap[m.UserID] = agg
		}

		switch metricKey {
		case "total_tokens":
			agg.metricValue += float64(m.ExactTokenTotal + m.DerivedTokenTotal)
		case "sessions":
			agg.metricValue += float64(m.SessionCount)
		case "turns":
			agg.metricValue += float64(m.InteractionTurnCount)
		default:
			agg.metricValue += float64(m.ExactTokenTotal + m.DerivedTokenTotal)
		}
	}

	// Sort by metric value descending
	sorted := make([]*userAgg, 0, len(userMap))
	for _, a := range userMap {
		sorted = append(sorted, a)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].metricValue > sorted[j].metricValue
	})

	// Create entries
	for i, a := range sorted {
		entry := &domain.LeaderboardEntry{
			SnapshotID:          snapshotID,
			RankNo:              uint32(i + 1),
			UserID:              a.userID,
			MetricValue:         a.metricValue,
			DisplayNameSnapshot: a.displayName,
			AvatarURLSnapshot:   a.avatarURL,
		}
		if err := leaderboards.CreateEntry(ctx, entry); err != nil {
			return err
		}
	}

	snap.ParticipantCount = uint32(len(sorted))
	if err := leaderboards.PublishSnapshot(ctx, snapshotID, now); err != nil {
		return err
	}

	return nil
}

// visibilityAllowed checks if a user's visibility setting allows them
// to appear in the given scope.
func visibilityAllowed(userVis, scopeType string) bool {
	switch scopeType {
	case "global":
		return userVis == "public"
	case "team":
		return userVis == "public" || userVis == "team"
	case "private":
		return true
	default:
		return userVis == "public"
	}
}

// RecomputeAll clears all metrics and reprocesses every event from PK 0.
func (w *Worker) RecomputeAll(ctx context.Context) (int, error) {
	if err := w.Metrics.DeleteAllMetrics(ctx); err != nil {
		return 0, err
	}
	w.LastProcessedPK = 0
	total := 0
	for {
		n, err := w.RunOnce(ctx)
		if err != nil {
			return total, err
		}
		if n == 0 {
			break
		}
		total += n
	}
	return total, nil
}
