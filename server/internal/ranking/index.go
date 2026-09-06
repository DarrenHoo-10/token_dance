package ranking

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"tokendance/internal/crypto"
)

type RedisClient interface {
	redis.Cmdable
	redis.Scripter
}

type Index struct {
	rdb RedisClient
}

func NewIndex(rdb RedisClient) *Index {
	if rdb == nil {
		return nil
	}
	return &Index{rdb: rdb}
}

const (
	StatusApplied           = "applied"
	StatusDuplicate         = "duplicate"
	StatusStale             = "stale"
	StatusSkippedGeneration = "skipped_generation"
	OpUpsert                = "upsert"
	OpRemove                = "remove"
	ViewHot                 = "hot"
	ViewLive                = "live"
)

type ApplyInput struct {
	Window       string
	Generation   string
	UserID       string
	Tokens       uint64
	Revision     uint64
	RegisteredAt time.Time
	Op           string
	Now          time.Time
}

type ApplyResult struct {
	Status        string
	IndexRevision string
	CurrentGen    string
}

func (idx *Index) Apply(ctx context.Context, in ApplyInput) (*ApplyResult, error) {
	if idx == nil || idx.rdb == nil {
		return nil, fmt.Errorf("ranking redis is not configured")
	}
	if !ValidWindow(in.Window) {
		return nil, ErrInvalidWindow
	}
	if in.Generation == "" {
		return nil, fmt.Errorf("ranking generation is required")
	}
	if in.Op != OpUpsert && in.Op != OpRemove {
		return nil, fmt.Errorf("invalid ranking op %q", in.Op)
	}
	member := ""
	if in.Op == OpUpsert {
		encoded, err := EncodeMember(in.Tokens, in.RegisteredAt, in.UserID)
		if err != nil {
			return nil, err
		}
		member = encoded
	} else if err := validateUserID(in.UserID); err != nil {
		return nil, err
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	wk := keysForWindow(in.Window)
	gk := genKeys(in.Window, in.Generation)
	raw, err := applyScript.Run(ctx, idx.rdb, []string{
		wk.Current, gk.All, gk.Users, gk.Versions, gk.Meta, wk.Dirty,
	}, in.Generation, in.UserID, member, strconv.FormatUint(in.Tokens, 10), strconv.FormatUint(in.Revision, 10), in.Op, strconv.FormatInt(now.UnixMilli(), 10), RuleVersion).Slice()
	if err != nil {
		return nil, fmt.Errorf("apply ranking lua: %w", err)
	}
	status := scriptString(raw, 0)
	extra := scriptString(raw, 1)
	return &ApplyResult{Status: status, IndexRevision: extra, CurrentGen: extra}, nil
}

func (idx *Index) CurrentGeneration(ctx context.Context, window string) (string, error) {
	if idx == nil || idx.rdb == nil {
		return "", nil
	}
	if !ValidWindow(window) {
		return "", ErrInvalidWindow
	}
	val, err := idx.rdb.HGet(ctx, keysForWindow(window).Current, "generation").Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

func (idx *Index) Cardinality(ctx context.Context, window, generation string) (int64, error) {
	if idx == nil || idx.rdb == nil {
		return 0, nil
	}
	if !ValidWindow(window) {
		return 0, ErrInvalidWindow
	}
	return idx.rdb.ZCard(ctx, genKeys(window, generation).All).Result()
}

func (idx *Index) PromoteGeneration(ctx context.Context, window, generation string) (string, error) {
	if idx == nil || idx.rdb == nil {
		return "", fmt.Errorf("ranking redis is not configured")
	}
	if !ValidWindow(window) {
		return "", ErrInvalidWindow
	}
	wk := keysForWindow(window)
	gk := genKeys(window, generation)
	res, err := promoteScript.Run(ctx, idx.rdb, []string{wk.Current, gk.All, wk.Dirty}, generation, RuleVersion).Text()
	if err != nil {
		return "", fmt.Errorf("promote ranking generation: %w", err)
	}
	return res, nil
}

type hotCapture struct {
	Generation   string
	Revision     string
	Participants int
	Rows         []string
}

func (idx *Index) captureHot(ctx context.Context, window string) (*hotCapture, error) {
	wk := keysForWindow(window)
	gen, err := idx.CurrentGeneration(ctx, window)
	if err != nil {
		return nil, err
	}
	if gen == "" {
		return &hotCapture{}, nil
	}
	prev, err := idx.rdb.HGet(ctx, wk.Current, "previousGeneration").Result()
	if err == redis.Nil {
		prev = ""
		err = nil
	}
	if err != nil {
		return nil, err
	}
	gk := genKeys(window, gen)
	pk := genKeys(window, prev)
	raw, err := captureScript.Run(ctx, idx.rdb, []string{
		wk.Current, gk.All, gk.Users, gk.Versions, gk.Meta, pk.Users, pk.All,
	}, HotSize-1).Slice()
	if err != nil {
		return nil, fmt.Errorf("capture hot snapshot: %w", err)
	}
	if scriptString(raw, 0) == "empty" {
		return &hotCapture{}, nil
	}
	participants, _ := strconv.Atoi(scriptString(raw, 2))
	rows := make([]string, 0, len(raw))
	if len(raw) > 3 {
		for i := 3; i < len(raw); i++ {
			rows = append(rows, scriptString(raw, i))
		}
	}
	return &hotCapture{
		Generation:   scriptString(raw, 0),
		Revision:     scriptString(raw, 1),
		Participants: participants,
		Rows:         rows,
	}, nil
}

func (idx *Index) writeSnapshot(ctx context.Context, window, snapshotID string, cap *hotCapture, fence int64, now time.Time) error {
	rowsKey := hotRowsKey(window, snapshotID)
	metaKey := hotMetaKey(window, snapshotID)
	pipe := idx.rdb.TxPipeline()
	pipe.Del(ctx, rowsKey, metaKey)
	if len(cap.Rows) > 0 {
		args := make([]interface{}, len(cap.Rows))
		for i, row := range cap.Rows {
			args[i] = row
		}
		pipe.RPush(ctx, rowsKey, args...)
	}
	pipe.HSet(ctx, metaKey, map[string]interface{}{
		"generation":   cap.Generation,
		"revision":     cap.Revision,
		"participants": strconv.Itoa(cap.Participants),
		"rowCount":     strconv.Itoa(len(cap.Rows)),
		"capturedAt":   now.UTC().Format(time.RFC3339Nano),
		"ruleVersion":  RuleVersion,
		"fence":        strconv.FormatInt(fence, 10),
		"window":       window,
	})
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("write hot snapshot: %w", err)
	}
	return nil
}

func (idx *Index) publishSnapshotID(ctx context.Context, window, snapshotID, generation, revision string, rowCount int, fence int64) (string, error) {
	wk := keysForWindow(window)
	status, err := publishScript.Run(ctx, idx.rdb, []string{
		wk.HotCurrent, hotMetaKey(window, snapshotID), hotRowsKey(window, snapshotID),
	}, snapshotID, strconv.FormatInt(fence, 10), generation, revision, strconv.Itoa(rowCount)).Text()
	if err != nil {
		return "", fmt.Errorf("publish hot snapshot: %w", err)
	}
	return status, nil
}

func (idx *Index) PublishHot(ctx context.Context, window string, now time.Time) (string, error) {
	if idx == nil || idx.rdb == nil {
		return "", fmt.Errorf("ranking redis is not configured")
	}
	if !ValidWindow(window) {
		return "", ErrInvalidWindow
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cap, err := idx.captureHot(ctx, window)
	if err != nil {
		return "", err
	}
	if cap.Generation == "" {
		return "", nil
	}
	wk := keysForWindow(window)
	currentID, err := idx.rdb.Get(ctx, wk.HotCurrent).Result()
	if err == redis.Nil {
		currentID = ""
		err = nil
	}
	if err != nil {
		return "", err
	}
	if currentID != "" {
		oldRev, err := idx.rdb.HGet(ctx, hotMetaKey(window, currentID), "revision").Result()
		if err == nil && oldRev == cap.Revision {
			oldGen, genErr := idx.rdb.HGet(ctx, hotMetaKey(window, currentID), "generation").Result()
			if genErr == nil && oldGen == cap.Generation {
				return currentID, nil
			}
		}
	}
	snapshotID, err := newHotSnapshotID()
	if err != nil {
		return "", err
	}
	fence, err := idx.rdb.Incr(ctx, wk.PubSeq).Result()
	if err != nil {
		return "", fmt.Errorf("ranking publish fence: %w", err)
	}
	if err := idx.writeSnapshot(ctx, window, snapshotID, cap, fence, now); err != nil {
		return "", err
	}
	status, err := idx.publishSnapshotID(ctx, window, snapshotID, cap.Generation, cap.Revision, len(cap.Rows), fence)
	if err != nil {
		return "", err
	}
	if status != "ok" {
		return "", fmt.Errorf("hot snapshot publish rejected: %s", status)
	}
	if currentID != "" && currentID != snapshotID {
		_ = idx.rdb.Expire(ctx, hotRowsKey(window, currentID), oldSnapshotTTL).Err()
		_ = idx.rdb.Expire(ctx, hotMetaKey(window, currentID), oldSnapshotTTL).Err()
	}
	return snapshotID, nil
}

func (idx *Index) PublishDirtyWindows(ctx context.Context, now time.Time) error {
	if idx == nil || idx.rdb == nil {
		return nil
	}
	var first error
	for _, window := range Windows {
		wk := keysForWindow(window)
		dirty, err := idx.rdb.Get(ctx, wk.Dirty).Result()
		hasDirty := err == nil && dirty == "1"
		if err != nil && err != redis.Nil {
			if first == nil {
				first = err
			}
			continue
		}
		_, hotErr := idx.rdb.Get(ctx, wk.HotCurrent).Result()
		hasHot := hotErr == nil
		if hotErr != nil && hotErr != redis.Nil {
			if first == nil {
				first = hotErr
			}
			continue
		}
		if !hasDirty && hasHot {
			continue
		}
		if hasDirty {
			_ = idx.rdb.Del(ctx, wk.Dirty).Err()
		}
		if _, err := idx.PublishHot(ctx, window, now); err != nil {
			if hasDirty {
				_ = idx.rdb.Set(ctx, wk.Dirty, "1", 0).Err()
			}
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func newHotSnapshotID() (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate hot snapshot id: %w", err)
	}
	return "hs_" + token, nil
}

func scriptString(raw []interface{}, i int) string {
	if i < 0 || i >= len(raw) || raw[i] == nil {
		return ""
	}
	switch v := raw[i].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
