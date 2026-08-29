package main

import (
	"sort"
	"testing"
	"time"
)

func TestTodayLeaderboardSimulatedP95Freshness(t *testing.T) {
	var latencies []time.Duration
	for eventSecond := 0; eventSecond < int(todayInterval/time.Second); eventSecond++ {
		for aggregationPhase := 0; aggregationPhase < int(aggregationInterval/time.Second); aggregationPhase++ {
			for todayPhase := 0; todayPhase < int(todayInterval/time.Second); todayPhase++ {
				eventAt := time.Duration(eventSecond) * time.Second
				readyAt := eventAt + aggregationSafeLag
				aggregatedAt := nextScheduledTick(readyAt, time.Duration(aggregationPhase)*time.Second, aggregationInterval)
				publishedAt := nextScheduledTick(aggregatedAt, time.Duration(todayPhase)*time.Second, todayInterval)
				latencies = append(latencies, publishedAt-eventAt)
			}
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[(len(latencies)*95+99)/100-1]
	if p95 > 65*time.Second {
		t.Fatalf("simulated today freshness p95=%s exceeds 65s", p95)
	}
}

func nextScheduledTick(at, phase, interval time.Duration) time.Duration {
	if at <= phase {
		return phase
	}
	steps := (at - phase + interval - 1) / interval
	return phase + steps*interval
}
