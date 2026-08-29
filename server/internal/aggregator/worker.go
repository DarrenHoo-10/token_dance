package aggregator

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
	"github.com/tokendance/token-collector/server/internal/store/mysqlstore"
)

const (
	AggregationVersion = 2
	batchSize          = 1000
	watermarkName      = "daily_metrics_committed_v2"
	defaultSafeLag     = 5 * time.Second
)

// Worker performs incremental aggregation of usage events into daily metrics.
type Worker struct {
	Events     store.EventStore
	Metrics    store.MetricStore
	Users      store.UserStore
	Watermarks store.WatermarkStore
	SafeLag    time.Duration

	// LastProcessedPK exposes the committed aggregation source watermark.
	LastProcessedPK uint64
}

// RunOnce processes a single batch of new events since LastProcessedPK.
// Returns the number of events processed.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	if committed, ok := w.Metrics.(interface {
		AggregateCommittedMetrics(context.Context, string, time.Time, int) (mysqlstore.AggregationProgress, error)
	}); ok {
		lag := w.SafeLag
		if lag == 0 {
			lag = defaultSafeLag
		} else if lag < 0 {
			lag = 0
		}
		progress, err := committed.AggregateCommittedMetrics(ctx, watermarkName, time.Now().UTC().Add(-lag), batchSize)
		if err != nil {
			return 0, err
		}
		w.LastProcessedPK = progress.SourceMaxEventPK
		return progress.EventCount, nil
	}
	if w.Watermarks != nil && w.LastProcessedPK == 0 {
		persisted, err := w.Watermarks.GetWatermark(ctx, watermarkName)
		if err != nil {
			return 0, err
		}
		w.LastProcessedPK = persisted
	}
	if recomputer, ok := w.Metrics.(store.RecomputeMetricStore); ok {
		maxPK, err := w.Events.MaxEventPK(ctx)
		if err != nil {
			return 0, err
		}
		if maxPK <= w.LastProcessedPK {
			return 0, nil
		}
		if err := recomputer.RecomputeMetrics(ctx, watermarkName, maxPK); err != nil {
			return 0, err
		}
		processed := int(maxPK - w.LastProcessedPK)
		w.LastProcessedPK = maxPK
		return processed, nil
	}
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
	if w.Watermarks != nil {
		if err := w.Watermarks.SetWatermark(ctx, watermarkName, w.LastProcessedPK, now); err != nil {
			return 0, err
		}
	}
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

// BuildLeaderboardSnapshot creates a global snapshot for compatibility.
func (w *Worker) BuildLeaderboardSnapshot(ctx context.Context, leaderboards store.LeaderboardStore, snapshotID, boardKey, scopeType, metricKey string, windowStart, windowEnd time.Time) error {
	return w.BuildLeaderboardSnapshotScoped(ctx, leaderboards, snapshotID, boardKey, scopeType, defaultScopeKey(scopeType), metricKey, windowStart, windowEnd)
}

// BuildLeaderboardSnapshotScoped publishes entries and snapshot state atomically.
func (w *Worker) BuildLeaderboardSnapshotScoped(ctx context.Context, leaderboards store.LeaderboardStore, snapshotID, boardKey, scopeType, scopeKey, metricKey string, windowStart, windowEnd time.Time) error {
	publisher, ok := leaderboards.(interface {
		PublishSnapshotAtomic(context.Context, string, mysqlstore.AggregationProgress, *domain.LeaderboardSnapshot, []*domain.LeaderboardEntry) error
	})
	progressStore, progressOK := w.Metrics.(interface {
		GetAggregationProgress(context.Context, string) (mysqlstore.AggregationProgress, error)
	})
	scopeStore, scopeOK := w.Users.(interface {
		UserAllowedInScope(context.Context, string, string, string) (bool, error)
	})
	if !ok || !progressOK || !scopeOK {
		if scopeKey != defaultScopeKey(scopeType) {
			return fmt.Errorf("scoped snapshot store is unavailable")
		}
		return w.buildLeaderboardSnapshotLegacy(ctx, leaderboards, snapshotID, boardKey, scopeType, metricKey, windowStart, windowEnd)
	}
	progress, err := progressStore.GetAggregationProgress(ctx, watermarkName)
	if err != nil {
		return err
	}
	metrics, err := w.Metrics.GetDailyMetricsAllUsers(ctx, windowStart, windowEnd)
	if err != nil {
		return err
	}
	type userAgg struct {
		userID      string
		metricValue float64
		displayName string
		avatarURL   string
	}
	users := make(map[string]*userAgg)
	for _, metric := range metrics {
		allowed, err := scopeStore.UserAllowedInScope(ctx, metric.UserID, scopeType, scopeKey)
		if err != nil || !allowed {
			continue
		}
		user, err := w.Users.GetUser(ctx, metric.UserID)
		if err != nil {
			continue
		}
		agg := users[metric.UserID]
		if agg == nil {
			agg = &userAgg{userID: metric.UserID, displayName: user.DisplayName, avatarURL: user.AvatarURL}
			users[metric.UserID] = agg
		}
		switch metricKey {
		case "sessions":
			agg.metricValue += float64(metric.SessionCount)
		case "turns":
			agg.metricValue += float64(metric.InteractionTurnCount)
		default:
			agg.metricValue += float64(metric.ExactTokenTotal + metric.DerivedTokenTotal)
		}
	}
	sorted := make([]*userAgg, 0, len(users))
	for _, user := range users {
		sorted = append(sorted, user)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].metricValue == sorted[j].metricValue {
			return sorted[i].userID < sorted[j].userID
		}
		return sorted[i].metricValue > sorted[j].metricValue
	})
	now := time.Now().UTC()
	snapshot := &domain.LeaderboardSnapshot{SnapshotID: snapshotID, BoardKey: boardKey, ScopeType: scopeType, ScopeKey: scopeKey, MetricKey: metricKey, WindowStart: windowStart, WindowEnd: windowEnd, TimezoneName: "UTC", RankingRuleVersion: 2, ParticipantCount: uint32(len(sorted)), SourceMaxEventPK: progress.SourceMaxEventPK, DataWatermarkAt: progress.CommittedThrough, SnapshotStatus: "building", GeneratedAt: now}
	entries := make([]*domain.LeaderboardEntry, 0, len(sorted))
	for i, user := range sorted {
		entries = append(entries, &domain.LeaderboardEntry{SnapshotID: snapshotID, RankNo: uint32(i + 1), UserID: user.userID, MetricValue: user.metricValue, DisplayNameSnapshot: user.displayName, AvatarURLSnapshot: user.avatarURL})
	}
	return publisher.PublishSnapshotAtomic(ctx, watermarkName, progress, snapshot, entries)
}

func defaultScopeKey(scopeType string) string {
	if scopeType == "global" {
		return "all"
	}
	return ""
}

// buildLeaderboardSnapshotLegacy supports non-transactional stores.
func (w *Worker) buildLeaderboardSnapshotLegacy(
	ctx context.Context,
	leaderboards store.LeaderboardStore,
	snapshotID, boardKey, scopeType, metricKey string,
	windowStart, windowEnd time.Time,
) error {
	prev, _ := leaderboards.LatestPublishedSnapshot(ctx, boardKey, scopeType, metricKey)

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
	if prev != nil && prev.SnapshotID != snapshotID {
		_ = leaderboards.SupersedeSnapshot(ctx, prev.SnapshotID)
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

// RecomputeAll catches the committed checkpoint up in bounded batches.
func (w *Worker) RecomputeAll(ctx context.Context) (int, error) {
	if committed, ok := w.Metrics.(interface {
		AggregateCommittedMetrics(context.Context, string, time.Time, int) (mysqlstore.AggregationProgress, error)
	}); ok {
		total := 0
		safeBefore := time.Now().UTC()
		for {
			progress, err := committed.AggregateCommittedMetrics(ctx, watermarkName, safeBefore, batchSize)
			if err != nil {
				return total, err
			}
			total += progress.EventCount
			w.LastProcessedPK = progress.SourceMaxEventPK
			if progress.EventCount < batchSize {
				return total, nil
			}
		}
	}
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
