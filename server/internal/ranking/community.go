package ranking

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Precomputed whole-community daily totals. Two date-keyed hashes exist at any
// time (today and yesterday); the public read path and any other consumer read
// these instead of aggregating usage tables on the fly.
const CommunityStatsKeyPrefix = "community:stats:"

const CommunityStatsTTL = 48 * time.Hour

type CommunityCost struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type CommunityStatsSnapshot struct {
	Date         string
	Tokens       uint64
	Developers   uint64
	CodeLines    uint64
	Interactions uint64
	CostAmount   float64
	Costs        []CommunityCost
	ComputedAt   time.Time
}

// PublishCommunityStats overwrites the day hash. A nil index (Redis not
// configured) is a no-op: readers fall back to the MySQL precomputed row.
func (idx *Index) PublishCommunityStats(ctx context.Context, snapshot CommunityStatsSnapshot) error {
	if idx == nil || idx.rdb == nil {
		return nil
	}
	key := CommunityStatsKeyPrefix + snapshot.Date
	costsJSON, err := json.Marshal(snapshot.Costs)
	if err != nil {
		return fmt.Errorf("marshal community costs %s: %w", snapshot.Date, err)
	}
	if snapshot.Costs == nil {
		costsJSON = []byte("[]")
	}
	costAmount := ""
	if len(snapshot.Costs) == 1 {
		costAmount = strconv.FormatFloat(snapshot.Costs[0].Amount, 'f', -1, 64)
	} else if len(snapshot.Costs) == 0 {
		costAmount = strconv.FormatFloat(snapshot.CostAmount, 'f', -1, 64)
	}
	fields := map[string]interface{}{
		"tokens":       strconv.FormatUint(snapshot.Tokens, 10),
		"developers":   strconv.FormatUint(snapshot.Developers, 10),
		"codeLines":    strconv.FormatUint(snapshot.CodeLines, 10),
		"interactions": strconv.FormatUint(snapshot.Interactions, 10),
		"costAmount":   costAmount,
		"costs":        string(costsJSON),
		"computedAt":   snapshot.ComputedAt.UTC().Format(time.RFC3339Nano),
	}
	if err := idx.rdb.HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("publish community stats %s: %w", snapshot.Date, err)
	}
	if err := idx.rdb.Expire(ctx, key, CommunityStatsTTL).Err(); err != nil {
		return fmt.Errorf("expire community stats %s: %w", snapshot.Date, err)
	}
	return nil
}
