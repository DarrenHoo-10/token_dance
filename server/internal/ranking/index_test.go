package ranking

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func applyUser(t *testing.T, idx *Index, window, gen, userID string, tokens, rev uint64, at time.Time, op string) *ApplyResult {
	t.Helper()
	res, err := idx.Apply(context.Background(), ApplyInput{
		Window:       window,
		Generation:   gen,
		UserID:       userID,
		Tokens:       tokens,
		Revision:     rev,
		RegisteredAt: at,
		Op:           op,
		Now:          at,
	})
	if err != nil {
		t.Fatalf("apply %s rev %d: %v", userID, rev, err)
	}
	return res
}

func TestApplyLuaIdempotentOutOfOrderAndTombstone(t *testing.T) {
	idx := testIndex(t)
	ctx := context.Background()
	window, gen := "today", "2026-09-06"
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	first := applyUser(t, idx, window, gen, "usr_a", 10, 1, at, OpUpsert)
	if first.Status != StatusApplied {
		t.Fatalf("first apply: %+v", first)
	}
	dup := applyUser(t, idx, window, gen, "usr_a", 10, 1, at, OpUpsert)
	if dup.Status != StatusDuplicate {
		t.Fatalf("duplicate: %+v", dup)
	}
	newer := applyUser(t, idx, window, gen, "usr_a", 50, 3, at, OpUpsert)
	if newer.Status != StatusApplied {
		t.Fatalf("out-of-order newer: %+v", newer)
	}
	stale := applyUser(t, idx, window, gen, "usr_a", 99, 2, at, OpUpsert)
	if stale.Status != StatusStale {
		t.Fatalf("old revision: %+v", stale)
	}

	gk := genKeys(window, gen)
	card, err := idx.rdb.ZCard(ctx, gk.All).Result()
	if err != nil || card != 1 {
		t.Fatalf("zcard after upserts: %d %v", card, err)
	}
	payload, err := idx.rdb.HGet(ctx, gk.Users, "usr_a").Result()
	if err != nil {
		t.Fatal(err)
	}
	_, tokens, member, err := parseUserPayload(payload)
	if err != nil || tokens != 50 {
		t.Fatalf("payload tokens=%d err=%v", tokens, err)
	}
	score, err := idx.rdb.ZScore(ctx, gk.All, member).Result()
	if err != nil || score != 0 {
		t.Fatalf("zset score must be 0, got %v %v", score, err)
	}

	removed := applyUser(t, idx, window, gen, "usr_a", 50, 4, at, OpRemove)
	if removed.Status != StatusApplied {
		t.Fatalf("remove: %+v", removed)
	}
	card, _ = idx.rdb.ZCard(ctx, gk.All).Result()
	if card != 0 {
		t.Fatalf("expected empty zset after tombstone, got %d", card)
	}
	ver, err := idx.rdb.HGet(ctx, gk.Versions, "usr_a").Result()
	if err != nil || ver != "4|D" {
		t.Fatalf("tombstone: %q %v", ver, err)
	}
	resurrect := applyUser(t, idx, window, gen, "usr_a", 1, 3, at, OpUpsert)
	if resurrect.Status != StatusStale {
		t.Fatalf("tombstone blocked old upsert: %+v", resurrect)
	}
	card, _ = idx.rdb.ZCard(ctx, gk.All).Result()
	if card != 0 {
		t.Fatalf("member resurrected from tombstone")
	}
	dupRemove := applyUser(t, idx, window, gen, "usr_a", 50, 4, at, OpRemove)
	if dupRemove.Status != StatusDuplicate {
		t.Fatalf("duplicate remove: %+v", dupRemove)
	}
	again := applyUser(t, idx, window, gen, "usr_a", 7, 5, at, OpUpsert)
	if again.Status != StatusApplied {
		t.Fatalf("reactivate after tombstone: %+v", again)
	}
	card, _ = idx.rdb.ZCard(ctx, gk.All).Result()
	if card != 1 {
		t.Fatalf("expected re-add after newer revision, got %d", card)
	}
}

func TestApplySkipsOlderGeneration(t *testing.T) {
	idx := testIndex(t)
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	applyUser(t, idx, "today", "2026-09-06", "usr_a", 1, 1, at, OpUpsert)
	skipped := applyUser(t, idx, "today", "2026-09-05", "usr_a", 9, 9, at, OpUpsert)
	if skipped.Status != StatusSkippedGeneration {
		t.Fatalf("expected skipped generation, got %+v", skipped)
	}
	n, err := idx.Cardinality(context.Background(), "today", "2026-09-06")
	if err != nil || n != 1 {
		t.Fatalf("current generation mutated: %d %v", n, err)
	}
}

func TestZRangeOrderMatchesTokenDesc(t *testing.T) {
	idx := testIndex(t)
	ctx := context.Background()
	window, gen := "7d", "2026-09-06"
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	applyUser(t, idx, window, gen, "usr_low", 9, 1, at, OpUpsert)
	applyUser(t, idx, window, gen, "usr_high", 10, 1, at.Add(time.Hour), OpUpsert)
	applyUser(t, idx, window, gen, "usr_zero", 0, 1, at.Add(-time.Hour), OpUpsert)
	applyUser(t, idx, window, gen, "usr_tie_a", 5, 1, at, OpUpsert)
	applyUser(t, idx, window, gen, "usr_tie_b", 5, 1, at, OpUpsert)
	members, err := idx.rdb.ZRange(ctx, genKeys(window, gen).All, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, member := range members {
		_, _, id, err := DecodeMember(member)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	want := []string{"usr_high", "usr_low", "usr_tie_a", "usr_tie_b", "usr_zero"}
	if len(ids) != len(want) {
		t.Fatalf("members=%v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order %v want %v", ids, want)
		}
	}
}

func TestHotPublishFailureLeavesOldSnapshot(t *testing.T) {
	idx := testIndex(t)
	ctx := context.Background()
	window, gen := "today", "2026-09-06"
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	applyUser(t, idx, window, gen, "usr_a", 20, 1, at, OpUpsert)
	applyUser(t, idx, window, gen, "usr_b", 10, 1, at, OpUpsert)
	firstID, err := idx.PublishHot(ctx, window, at)
	if err != nil || firstID == "" {
		t.Fatalf("first publish: %q %v", firstID, err)
	}

	applyUser(t, idx, window, gen, "usr_c", 30, 1, at, OpUpsert)
	badID := "hs_incomplete_test"
	wk := keysForWindow(window)
	fence, err := idx.rdb.Incr(ctx, wk.PubSeq).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.rdb.RPush(ctx, hotRowsKey(window, badID), "usr_c\t30\t").Err(); err != nil {
		t.Fatal(err)
	}
	if err := idx.rdb.HSet(ctx, hotMetaKey(window, badID), map[string]interface{}{
		"generation":   gen,
		"revision":     "99",
		"participants": "3",
		"rowCount":     "3",
		"fence":        strconv.FormatInt(fence, 10),
		"window":       window,
	}).Err(); err != nil {
		t.Fatal(err)
	}
	status, err := idx.publishSnapshotID(ctx, window, badID, gen, "99", 3, fence)
	if err != nil {
		t.Fatal(err)
	}
	if status != "incomplete" {
		t.Fatalf("expected incomplete publish, got %s", status)
	}
	current, err := idx.rdb.Get(ctx, wk.HotCurrent).Result()
	if err != nil || current != firstID {
		t.Fatalf("current snapshot mutated: %s %v", current, err)
	}
	page, err := idx.Read(ctx, ReadRequest{Window: window, Limit: 50})
	if err != nil || page.Miss || page.SnapshotID != firstID || page.Participants != 2 || len(page.Entries) != 2 {
		t.Fatalf("old snapshot not served: %+v %v", page, err)
	}
	if page.Kind != ViewHot {
		t.Fatalf("kind=%s", page.Kind)
	}

	okID, err := idx.PublishHot(ctx, window, at.Add(time.Second))
	if err != nil || okID == "" || okID == firstID {
		t.Fatalf("successful republish: %q %v", okID, err)
	}
	page, err = idx.Read(ctx, ReadRequest{Window: window, SnapshotID: okID, Limit: 50})
	if err != nil || page.Participants != 3 || page.Entries[0].UserID != "usr_c" {
		t.Fatalf("new snapshot: %+v %v", page, err)
	}
}

func TestAuthenticatedReadDoesNotMixVersions(t *testing.T) {
	idx := testIndex(t)
	ctx := context.Background()
	window, gen := "30d", "2026-09-06"
	at := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	applyUser(t, idx, window, gen, "usr_top", 100, 1, at, OpUpsert)
	applyUser(t, idx, window, gen, "usr_own", 1, 1, at.Add(time.Second), OpUpsert)
	if _, err := idx.PublishHot(ctx, window, at); err != nil {
		t.Fatal(err)
	}
	matched, err := idx.Read(ctx, ReadRequest{Window: window, Limit: 50, UserID: "usr_own"})
	if err != nil || matched.Kind != ViewHot || matched.Own == nil || matched.Own.Rank != 2 {
		t.Fatalf("matched hot+own: %+v %v", matched, err)
	}

	applyUser(t, idx, window, gen, "usr_new", 50, 1, at, OpUpsert)
	live, err := idx.Read(ctx, ReadRequest{Window: window, Limit: 50, UserID: "usr_own"})
	if err != nil || live.Kind != ViewLive || live.Own == nil || live.Own.Rank != 3 {
		t.Fatalf("live own after dirty index: %+v %v", live, err)
	}
	if live.Participants != 3 || len(live.Entries) != 3 {
		t.Fatalf("live page mixed: %+v", live)
	}
	public, err := idx.Read(ctx, ReadRequest{Window: window, SnapshotID: matched.SnapshotID, Limit: 50})
	if err != nil || public.Kind != ViewHot || public.Participants != 2 || public.Own != nil {
		t.Fatalf("public snapshot stayed frozen: %+v %v", public, err)
	}
}

func TestPublicPageUsesSnapshotParticipantCount(t *testing.T) {
	idx := testIndex(t)
	ctx := context.Background()
	window, gen := "all", "2026-09-06"
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := "usr_" + strconv.Itoa(i)
		applyUser(t, idx, window, gen, id, uint64(100-i), 1, at.Add(time.Duration(i)*time.Second), OpUpsert)
	}
	if _, err := idx.PublishHot(ctx, window, at); err != nil {
		t.Fatal(err)
	}
	page, err := idx.Read(ctx, ReadRequest{Window: window, Offset: 0, Limit: 2})
	if err != nil || page.Miss {
		t.Fatalf("read: %+v %v", page, err)
	}
	if page.Participants != 5 {
		t.Fatalf("participants capped or wrong: %d", page.Participants)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("page size: %d", len(page.Entries))
	}
}
