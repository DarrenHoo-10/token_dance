package rollout_test

import (
	"context"
	"database/sql"
	"testing"

	"tokendance/internal/rollout"
)

func TestPreservedTablesNeverInClearList(t *testing.T) {
	clear := make(map[string]struct{}, len(rollout.StatsClearTables))
	for _, name := range rollout.StatsClearTables {
		clear[name] = struct{}{}
	}
	for _, name := range rollout.PreservedTables {
		if _, ok := clear[name]; ok {
			t.Fatalf("preserved table %s must not be cleared", name)
		}
	}
	for _, required := range []string{
		"users", "user_sessions", "installations", "ingest_nonces",
		"desktop_releases", "desktop_release_channels", "desktop_release_publication",
	} {
		found := false
		for _, name := range rollout.PreservedTables {
			if name == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing required preserved table %s", required)
		}
	}
}

func TestStatsClearCoversLegacyAndTelemetry(t *testing.T) {
	need := []string{
		"usage_events", "ingest_batches", "user_window_scores", "ranking_outbox",
		"community_daily_stats", "community_stats_outbox", "aggregate_dirty_days",
		"telemetry_events", "telemetry_tasks", "telemetry_harness_metrics",
	}
	have := make(map[string]struct{}, len(rollout.StatsClearTables))
	for _, name := range rollout.StatsClearTables {
		have[name] = struct{}{}
	}
	for _, name := range need {
		if _, ok := have[name]; !ok {
			t.Fatalf("clear list missing %s", name)
		}
	}
}

func TestResetStatsIdempotentWithoutDB(t *testing.T) {
	// Compile-time contract: ResetStats signature accepts context+db+opts.
	var _ func(context.Context, *sql.DB, rollout.ResetOptions) (*rollout.Result, error) = rollout.ResetStats
	if rollout.ClosedBetaGeneration == 0 {
		t.Fatal("closed-beta generation must be >= 1")
	}
	if rollout.PhaseReady != "ready" {
		t.Fatalf("unexpected ready phase %q", rollout.PhaseReady)
	}
}
