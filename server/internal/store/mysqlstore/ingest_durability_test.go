package mysqlstore_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
)

type successfulCache struct {
	consumed int
}

func (c *successfulCache) ConsumeNonce(context.Context, string, [32]byte, time.Time) (bool, error) {
	c.consumed++
	return true, nil
}

func (c *successfulCache) PruneExpired(context.Context, time.Time) (int, error) {
	return 0, nil
}

func TestRedisNonceSuccessIsDurableInMySQL(t *testing.T) {
	s := resetStore(t)
	createUserAndInstallation(t, s, id(40), id(41))
	cache := &successfulCache{}
	nonces := &store.FallbackNonceStore{Cache: cache, Fallback: s}
	nonceHash := sha256.Sum256([]byte("redis-success-durable"))
	expiresAt := time.Now().UTC().Add(time.Minute)

	fresh, err := nonces.ConsumeNonce(context.Background(), id(41), nonceHash, expiresAt)
	if err != nil || !fresh || cache.consumed != 1 {
		t.Fatalf("consume nonce: fresh=%v cache=%d err=%v", fresh, cache.consumed, err)
	}
	fresh, err = s.ConsumeNonce(context.Background(), id(41), nonceHash, expiresAt)
	if err != nil || fresh {
		t.Fatalf("nonce was not durable: fresh=%v err=%v", fresh, err)
	}
}

func TestTwentyConcurrentEventsAreDeduplicatedWithoutBlocking(t *testing.T) {
	s := resetStore(t)
	createUserAndInstallation(t, s, id(51), id(52))
	ctx := context.Background()
	sharedEventID := sha256.Sum256([]byte("twenty-concurrent-events"))
	const workers = 20
	results := make(chan *store.IngestCommitResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			batchID := id(53 + i)
			requestHash := sha256.Sum256([]byte(fmt.Sprintf("twenty-%d", i)))
			result, err := s.CommitBatch(ctx, batch(batchID, id(52), requestHash), []*store.IngestEvent{{Event: event(batchID, id(52), id(51), sharedEventID, 1)}}, nil)
			results <- result
			errs <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var accepted, duplicates uint32
	for result := range results {
		accepted += result.Batch.AcceptedCount
		duplicates += result.Batch.DuplicateCount
	}
	if accepted != 1 || duplicates != workers-1 {
		t.Fatalf("accepted=%d duplicates=%d", accepted, duplicates)
	}
}

func TestIngestEventFieldsArePersistedWithoutLoss(t *testing.T) {
	s := resetStore(t)
	createUserAndInstallation(t, s, id(73), id(74))
	ctx := context.Background()
	batchID := id(75)
	eventID := sha256.Sum256([]byte("all-ingest-fields"))
	usageEvent := event(batchID, id(74), id(73), eventID, 1)
	toolTokens := uint64(17)
	usageEvent.TokenTool = &toolTokens
	toolCategory, skillInvokeType := "shell", "native"
	costAmount, costCurrency, costSource, costDiscount := "2.50000000", "USD", "provider_reported", "0.50000000"
	spawnedAgentType, codeLanguage := "subagent", "go"
	skillKey := sha256.Sum256([]byte("skill"))
	pluginKey := sha256.Sum256([]byte("plugin"))
	childSessionHash := sha256.Sum256([]byte("child"))
	workspaceHash := sha256.Sum256([]byte("workspace"))
	sessionEndReason := domain.SessionEndReason("timeout")
	turnTrigger := domain.TurnTrigger("subagent")
	usageEvent.WorkspaceHash = &workspaceHash
	usageEvent.SessionEndReason = &sessionEndReason
	usageEvent.TurnTrigger = &turnTrigger
	ingestEvent := &store.IngestEvent{
		Event: usageEvent, ToolCategory: &toolCategory, SkillKey: &skillKey, SkillInvokeType: &skillInvokeType, PluginKey: &pluginKey,
		CostAmount: &costAmount, CostCurrency: &costCurrency, CostSource: &costSource, CostDiscountAmount: &costDiscount,
		ChildSessionHash: &childSessionHash, SpawnedAgentType: &spawnedAgentType, CodeLanguage: &codeLanguage,
	}
	if _, err := s.CommitBatch(ctx, batch(batchID, id(74), sha256.Sum256([]byte("all-fields-batch"))), []*store.IngestEvent{ingestEvent}, nil); err != nil {
		t.Fatal(err)
	}
	var gotToolCategory, gotSkillInvokeType, gotCostAmount, gotCostCurrency, gotCostSource, gotCostDiscount, gotSpawnedAgentType, gotCodeLanguage string
	var gotSkillKey, gotPluginKey, gotChildSessionHash, gotWorkspaceHash []byte
	var gotSessionEndReason, gotTurnTrigger string
	var gotTokenTool uint64
	err := s.DB().QueryRowContext(ctx, `SELECT tool_category,skill_key,skill_invoke_type,plugin_key,cost_amount,cost_currency,cost_source,cost_discount_amount,child_session_hash,spawned_agent_type,code_language,token_tool,workspace_hash,session_end_reason,turn_trigger FROM usage_events WHERE batch_id=?`, batchID).Scan(
		&gotToolCategory, &gotSkillKey, &gotSkillInvokeType, &gotPluginKey, &gotCostAmount, &gotCostCurrency, &gotCostSource, &gotCostDiscount, &gotChildSessionHash, &gotSpawnedAgentType, &gotCodeLanguage, &gotTokenTool, &gotWorkspaceHash, &gotSessionEndReason, &gotTurnTrigger,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotToolCategory != toolCategory || string(gotSkillKey) != string(skillKey[:]) || gotSkillInvokeType != skillInvokeType || string(gotPluginKey) != string(pluginKey[:]) || gotCostAmount != costAmount || gotCostCurrency != costCurrency || gotCostSource != costSource || gotCostDiscount != costDiscount || string(gotChildSessionHash) != string(childSessionHash[:]) || gotSpawnedAgentType != spawnedAgentType || gotCodeLanguage != codeLanguage || gotTokenTool != toolTokens || string(gotWorkspaceHash) != string(workspaceHash[:]) || gotSessionEndReason != string(sessionEndReason) || gotTurnTrigger != string(turnTrigger) {
		t.Fatalf("persisted fields changed: tool=%q skillType=%q cost=%q/%q/%q/%q spawned=%q language=%q tokenTool=%d workspace=%x reason=%q trigger=%q", gotToolCategory, gotSkillInvokeType, gotCostAmount, gotCostCurrency, gotCostSource, gotCostDiscount, gotSpawnedAgentType, gotCodeLanguage, gotTokenTool, gotWorkspaceHash, gotSessionEndReason, gotTurnTrigger)
	}
	events, err := s.GetEventsByBatch(ctx, batchID)
	if err != nil || len(events) != 1 || events[0].WorkspaceHash == nil || *events[0].WorkspaceHash != workspaceHash || events[0].SessionEndReason == nil || *events[0].SessionEndReason != sessionEndReason || events[0].TurnTrigger == nil || *events[0].TurnTrigger != turnTrigger {
		t.Fatalf("typed event scan changed: events=%+v err=%v", events, err)
	}
}

func TestPartialBatchReplayReturnsDurableRejectedACK(t *testing.T) {
	s := resetStore(t)
	createUserAndInstallation(t, s, id(42), id(43))
	ctx := context.Background()
	batchID := id(44)
	requestHash := sha256.Sum256([]byte("partial-batch"))
	eventID := sha256.Sum256([]byte("accepted-event"))
	rejected := []store.BatchRejection{{Ordinal: 1, EventID: "", ErrorCode: "SCHEMA_INVALID", Retryable: false}}

	partialBatch := batch(batchID, id(43), requestHash)
	partialBatch.EventCount = 2
	first, err := s.CommitBatch(ctx, partialBatch, []*store.IngestEvent{{Event: event(batchID, id(43), id(42), eventID, 9)}}, rejected)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CommitBatch(ctx, batch(batchID, id(43), requestHash), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Batch.AcceptedCount != 1 || first.Batch.RejectedCount != 1 || len(first.Rejected) != 1 {
		t.Fatalf("unexpected first ACK: %+v rejected=%+v", first.Batch, first.Rejected)
	}
	if replay.Batch.AcceptedCount != first.Batch.AcceptedCount || replay.Batch.RejectedCount != first.Batch.RejectedCount || len(replay.Rejected) != 1 || replay.Rejected[0] != rejected[0] {
		t.Fatalf("replay ACK changed: first=%+v replay=%+v", first, replay)
	}
}

func TestCommitFailureBeforeCommitReturnsNoACKAndRollsBack(t *testing.T) {
	s := resetStore(t)
	createUserAndInstallation(t, s, id(45), id(46))
	ctx := context.Background()
	batchID := id(47)
	requestHash := sha256.Sum256([]byte("fail-before-commit"))
	eventID := sha256.Sum256([]byte("rolled-back-event"))

	if _, err := s.DB().ExecContext(ctx, `CREATE TRIGGER fail_ingest_commit BEFORE UPDATE ON ingest_batches FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected pre-commit failure'`); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitBatch(ctx, batch(batchID, id(46), requestHash), []*store.IngestEvent{{Event: event(batchID, id(46), id(45), eventID, 11)}}, nil)
	if err == nil || result != nil {
		t.Fatalf("failure produced ACK: result=%+v err=%v", result, err)
	}
	if _, lookupErr := s.GetBatch(ctx, batchID); lookupErr == nil {
		t.Fatal("failed transaction left a batch row")
	}
	events, lookupErr := s.GetEventsByBatch(ctx, batchID)
	if lookupErr != nil || len(events) != 0 {
		t.Fatalf("failed transaction left events: count=%d err=%v", len(events), lookupErr)
	}
	if _, err = s.DB().ExecContext(ctx, `DROP TRIGGER fail_ingest_commit`); err != nil {
		t.Fatal(err)
	}
	result, err = s.CommitBatch(ctx, batch(batchID, id(46), requestHash), []*store.IngestEvent{{Event: event(batchID, id(46), id(45), eventID, 11)}}, nil)
	if err != nil || result == nil || result.Batch.CommittedAt == nil || result.Batch.AcceptedCount != 1 {
		t.Fatalf("retry did not commit: result=%+v err=%v", result, err)
	}
}

func TestBatchHashConflictSentinel(t *testing.T) {
	s := resetStore(t)
	createUserAndInstallation(t, s, id(48), id(49))
	ctx := context.Background()
	batchID := id(50)
	firstHash := sha256.Sum256([]byte("first"))
	secondHash := sha256.Sum256([]byte("second"))
	if _, err := s.CommitBatch(ctx, batch(batchID, id(49), firstHash), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitBatch(ctx, batch(batchID, id(49), secondHash), nil, nil); !errors.Is(err, store.ErrBatchHashConflict) {
		t.Fatalf("expected conflict sentinel, got %v", err)
	}
}
