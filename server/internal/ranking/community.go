package ranking

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Precomputed whole-community daily totals. Two date-keyed hashes exist at any
// time (today and yesterday); the public read path and any other consumer read
// these instead of aggregating usage tables on the fly.
const CommunityStatsKeyPrefix = "community:stats:"

const CommunityStatsTTL = 48 * time.Hour

type CommunityStatsSnapshot struct {
	Date         string
	Tokens       uint64
	Developers   uint64
	CodeLines    uint64
	Interactions uint64
	CostAmount   float64
	ComputedAt   time.Time
}

// PublishCommunityStats overwrites the day hash. A nil index (Redis not
// configured) is a no-op: readers fall back to the MySQL precomputed row.
func (idx *Index) PublishCommunityStats(ctx context.Context, snapshot CommunityStatsSnapshot) error {
	if idx == nil || idx.rdb == nil {
		return nil
	}
	key := CommunityStatsKeyPrefix + snapshot.Date
	fields := map[string]interface{}{
		"tokens":      strconv.FormatUint(snapshot.Tokens, 10),
		"developers":  strconv.FormatUint(snapshot.Developers, 10),
		"codeLines":   strconv.FormatUint(snapshot.CodeLines, 10),
		"interactions": strconv.FormatUint(snapshot.Interactions, 10),
		"costAmount":  strconv.FormatFloat(snapshot.CostAmount, 'f', -1, 64),
		"computedAt":  snapshot.ComputedAt.UTC().Format(time.RFC3339Nano),
	}
	if err := idx.rdb.HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("publish community stats %s: %w", snapshot.Date, err)
	}
	if err := idx.rdb.Expire(ctx, key, CommunityStatsTTL).Err(); err != nil {
		return fmt.Errorf("expire community stats %s: %w", snapshot.Date, err)
	}
	return nil
}
