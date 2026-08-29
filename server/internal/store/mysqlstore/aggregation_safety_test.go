package mysqlstore_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tokendance/token-collector/server/internal/aggregator"
	"github.com/tokendance/token-collector/server/internal/store"
	"github.com/tokendance/token-collector/server/internal/store/mysqlstore"
)

const committedWatermark = "daily_metrics_committed_v2"

func TestCommittedWatermarkDoesNotLoseOutOfOrderPK(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(60), id(61))

	insertCommittedEventWithPK(t, s, 200, id(63), id(61), id(60), 20)
	worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: time.Millisecond}
	time.Sleep(3 * time.Millisecond)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertExactTokens(t, s, id(60), 20)
	if worker.LastProcessedPK != 200 {
		t.Fatalf("high watermark=%d want=200", worker.LastProcessedPK)
	}

	insertCommittedEventWithPK(t, s, 100, id(62), id(61), id(60), 10)
	time.Sleep(3 * time.Millisecond)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertExactTokens(t, s, id(60), 30)
}

func TestCompositeReceivedAtEventPKCheckpointBatchesTies(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(98), id(99))
	insertCommittedEventWithPK(t, s, 300, id(100), id(99), id(98), 10)
	insertCommittedEventWithPK(t, s, 301, id(101), id(99), id(98), 20)
	receivedAt := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	if _, err := s.DB().ExecContext(ctx, `UPDATE usage_events SET received_at=? WHERE event_pk IN (300,301)`, receivedAt); err != nil {
		t.Fatal(err)
	}
	first, err := s.AggregateCommittedMetrics(ctx, committedWatermark, receivedAt.Add(time.Second), 1)
	if err != nil || first.EventCount != 1 || first.SourceMaxEventPK != 300 || !first.CommittedThrough.Equal(receivedAt) {
		t.Fatalf("first batch=%+v err=%v", first, err)
	}
	second, err := s.AggregateCommittedMetrics(ctx, committedWatermark, receivedAt.Add(time.Second), 1)
	if err != nil || second.EventCount != 1 || second.SourceMaxEventPK != 301 || !second.CommittedThrough.Equal(receivedAt) {
		t.Fatalf("second batch=%+v err=%v", second, err)
	}
	assertExactTokens(t, s, id(98), 30)
}

func TestAggregationIncludesTransactionCommittedAfterSafeLag(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(95), id(96))

	tx, err := s.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	batchID := id(97)
	hash := sha256.Sum256([]byte("late-commit-batch"))
	eventID := sha256.Sum256([]byte("late-commit-event"))
	if _, err = tx.ExecContext(ctx, `INSERT INTO ingest_batches(batch_id,installation_id,request_sha256,event_count,accepted_count,batch_status,received_at,committed_at) VALUES(?,?,?,?,1,'committed',?,?)`, batchID, id(96), hash[:], 1, receivedAt, receivedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO usage_events(event_id,schema_version,batch_id,installation_id,user_id,adapter_id,adapter_version,agent_id,event_type,accuracy,source_kind,source_cursor_hmac,raw_fingerprint_hmac,occurred_at,received_at,token_total,privacy_policy_version) VALUES(?,'1.0.0',?,?,?,?,?,'agent','model_usage_recorded','exact','runtime_stream',?,?,?,?,?,1)`, eventID[:], batchID, id(96), id(95), "test", "1.0.0", hash[:], hash[:], receivedAt, receivedAt, 23); err != nil {
		t.Fatal(err)
	}

	const safeLag = 20 * time.Millisecond
	worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: safeLag}
	time.Sleep(2 * safeLag)
	if n, err := worker.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("scan while transaction open beyond safe lag: n=%d err=%v", n, err)
	}
	progress, err := s.GetAggregationProgress(ctx, committedWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.CommittedThrough.Equal(time.Unix(0, 0).UTC()) || progress.SourceMaxEventPK != 0 {
		t.Fatalf("empty scan advanced checkpoint: %+v", progress)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, err := worker.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("scan after late commit: n=%d err=%v", n, err)
	}
	assertExactTokens(t, s, id(95), 23)
}

func TestCommittedBatchTimestampCannotFallBehindCheckpoint(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(102), id(103))

	checkpoint := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO aggregation_watermarks(watermark_name,source_max_event_pk,committed_through_at) VALUES(?,0,?)`, committedWatermark, checkpoint); err != nil {
		t.Fatal(err)
	}
	batchID := id(104)
	hash := sha256.Sum256([]byte("behind-checkpoint"))
	b := batch(batchID, id(103), hash)
	b.ReceivedAt = checkpoint.Add(-time.Second)
	e := event(batchID, id(103), id(102), sha256.Sum256([]byte("behind-checkpoint-event")), 31)
	e.ReceivedAt = b.ReceivedAt
	if _, err := s.CommitBatch(ctx, b, []*store.IngestEvent{{Event: e}}, nil); err != nil {
		t.Fatal(err)
	}
	if !b.ReceivedAt.After(checkpoint) || !e.ReceivedAt.Equal(b.ReceivedAt) {
		t.Fatalf("committed timestamps batch=%s event=%s checkpoint=%s", b.ReceivedAt, e.ReceivedAt, checkpoint)
	}
	progress, err := s.AggregateCommittedMetrics(ctx, committedWatermark, b.ReceivedAt.Add(time.Second), 1000)
	if err != nil || progress.EventCount != 1 {
		t.Fatalf("aggregate late committed batch: progress=%+v err=%v", progress, err)
	}
	assertExactTokens(t, s, id(102), 31)
}

func TestAggregationFailureRollsBackMetricsAndWatermark(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(64), id(65))
	commitExactEvent(t, s, id(66), id(65), id(64), 10)
	worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: time.Millisecond}
	time.Sleep(3 * time.Millisecond)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetAggregationProgress(ctx, committedWatermark)
	if err != nil {
		t.Fatal(err)
	}
	commitExactEvent(t, s, id(67), id(65), id(64), 7)
	if _, err = s.DB().ExecContext(ctx, `CREATE TRIGGER fail_daily_aggregation BEFORE INSERT ON daily_user_agent_metrics FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected aggregation failure'`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if _, err = worker.RunOnce(ctx); err == nil {
		t.Fatal("injected aggregation failure was ignored")
	}
	after, err := s.GetAggregationProgress(ctx, committedWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if after.SourceMaxEventPK != before.SourceMaxEventPK || !after.CommittedThrough.Equal(before.CommittedThrough) {
		t.Fatalf("watermark advanced on failed aggregation: before=%+v after=%+v", before, after)
	}
	assertExactTokens(t, s, id(64), 10)
	if _, err = s.DB().ExecContext(ctx, `DROP TRIGGER fail_daily_aggregation`); err != nil {
		t.Fatal(err)
	}
}

func TestTypedModelSkillCostDailyAggregation(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(68), id(69))
	batchID := id(70)
	hash := sha256.Sum256([]byte("typed-dimensions"))
	provider, model := "provider", "model"
	modelEvent := event(batchID, id(69), id(68), sha256.Sum256([]byte("model")), 13)
	modelEvent.ProviderID, modelEvent.ModelID = &provider, &model
	skillEvent := event(batchID, id(69), id(68), sha256.Sum256([]byte("skill")), 0)
	skillEvent.EventType = "skill_invoked"
	success := true
	skillEvent.Success = &success
	duration := uint64(25)
	skillEvent.DurationMs = &duration
	skillKey := sha256.Sum256([]byte("skill-key"))
	invokeType := "explicit"
	costEvent := event(batchID, id(69), id(68), sha256.Sum256([]byte("cost")), 0)
	costEvent.EventType = "cost_recorded"
	amount, currency, source := "1.25000000", "USD", "provider_reported"
	input := []*store.IngestEvent{
		{Event: modelEvent},
		{Event: skillEvent, SkillKey: &skillKey, SkillInvokeType: &invokeType},
		{Event: costEvent, CostAmount: &amount, CostCurrency: &currency, CostSource: &source},
	}
	b := batch(batchID, id(69), hash)
	b.EventCount = uint32(len(input))
	if _, err := s.CommitBatch(ctx, b, input, nil); err != nil {
		t.Fatal(err)
	}
	worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: time.Millisecond}
	time.Sleep(3 * time.Millisecond)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var modelTokens, modelRequests uint64
	if err := s.DB().QueryRowContext(ctx, `SELECT exact_token_total,model_request_count FROM daily_user_agent_model_metrics WHERE user_id=? AND provider_id=? AND model_id=?`, id(68), provider, model).Scan(&modelTokens, &modelRequests); err != nil || modelTokens != 13 || modelRequests != 1 {
		t.Fatalf("model aggregate tokens=%d requests=%d err=%v", modelTokens, modelRequests, err)
	}
	var uses, successes, durationTotal uint64
	if err := s.DB().QueryRowContext(ctx, `SELECT use_count,success_count,duration_ms FROM daily_skill_metrics WHERE user_id=? AND skill_key=?`, id(68), skillKey[:]).Scan(&uses, &successes, &durationTotal); err != nil || uses != 1 || successes != 1 || durationTotal != 25 {
		t.Fatalf("skill aggregate uses=%d success=%d duration=%d err=%v", uses, successes, durationTotal, err)
	}
	var cost string
	if err := s.DB().QueryRowContext(ctx, `SELECT CAST(cost_amount AS CHAR) FROM daily_cost_metrics WHERE user_id=? AND cost_currency=? AND cost_source=?`, id(68), currency, source).Scan(&cost); err != nil || cost != "1.25000000" {
		t.Fatalf("cost aggregate=%q err=%v", cost, err)
	}
}

func TestDeletionScopesInstallationTimeAndAccount(t *testing.T) {
	t.Run("installation", func(t *testing.T) {
		s := resetStore(t)
		createUserAndInstallation(t, s, id(82), id(83))
		commitExactEvent(t, s, id(84), id(83), id(82), 5)
		request := mysqlstore.DeletionRequest{RequestID: id(85), UserID: id(82), InstallationID: id(83), Scope: "installation", RequestedAt: time.Now().UTC()}
		if err := s.CreateDeletionRequest(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ProcessNextDeletion(context.Background(), committedWatermark); err != nil {
			t.Fatal(err)
		}
		installation, err := s.GetInstallation(context.Background(), id(83))
		if err != nil || installation.InstallationStatus != "disabled" {
			t.Fatalf("installation=%+v err=%v", installation, err)
		}
	})
	t.Run("time", func(t *testing.T) {
		s := resetStore(t)
		createUserAndInstallation(t, s, id(86), id(87))
		inside := time.Now().UTC().Add(-time.Hour)
		outside := inside.Add(-48 * time.Hour)
		commitEventAt(t, s, id(88), id(87), id(86), 7, inside)
		commitEventAt(t, s, id(89), id(87), id(86), 11, outside)
		worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: -time.Nanosecond}
		if _, err := worker.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		start, end := inside.Add(-time.Minute), inside.Add(time.Minute)
		request := mysqlstore.DeletionRequest{RequestID: id(90), UserID: id(86), Scope: "time", RangeStart: &start, RangeEnd: &end, RequestedAt: time.Now().UTC()}
		if err := s.CreateDeletionRequest(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ProcessNextDeletion(context.Background(), committedWatermark); err != nil {
			t.Fatal(err)
		}
		assertExactTokens(t, s, id(86), 11)
	})
	t.Run("account", func(t *testing.T) {
		s := resetStore(t)
		createUserAndInstallation(t, s, id(91), id(92))
		ctx := context.Background()
		originalAuth := sha256.Sum256([]byte(id(91)))
		emailHash := sha256.Sum256([]byte("account@example.com"))
		if _, err := s.DB().ExecContext(ctx, `UPDATE users SET email_lookup_hash=?,email_ciphertext=?,display_name='Visible Name',avatar_url='https://example.com/avatar.png' WHERE user_id=?`, emailHash[:], []byte("encrypted-email"), id(91)); err != nil {
			t.Fatal(err)
		}
		commitExactEvent(t, s, id(93), id(92), id(91), 13)
		request := mysqlstore.DeletionRequest{RequestID: id(94), UserID: id(91), Scope: "account", RequestedAt: time.Now().UTC()}
		if err := s.CreateDeletionRequest(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ProcessNextDeletion(context.Background(), committedWatermark); err != nil {
			t.Fatal(err)
		}
		user, err := s.GetUser(ctx, id(91))
		if err != nil || user.AccountStatus != "deleted" || user.AuthSubjectHash == originalAuth || user.DisplayName != "Deleted User" || user.AvatarURL != "" {
			t.Fatalf("user=%+v err=%v", user, err)
		}
		var emailLookup, emailCiphertext []byte
		if err = s.DB().QueryRowContext(ctx, `SELECT email_lookup_hash,email_ciphertext FROM users WHERE user_id=?`, id(91)).Scan(&emailLookup, &emailCiphertext); err != nil || emailLookup != nil || emailCiphertext != nil {
			t.Fatalf("email fields lookup=%x ciphertext=%x err=%v", emailLookup, emailCiphertext, err)
		}
	})
}

func TestDeletionFailureIsRecordedInIndependentTransaction(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(95), id(96))
	request := mysqlstore.DeletionRequest{RequestID: id(97), UserID: id(95), Scope: "account", RequestedAt: time.Now().UTC()}
	if err := s.CreateDeletionRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `CREATE TRIGGER fail_account_anonymization BEFORE UPDATE ON users FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected account deletion failure'`); err != nil {
		t.Fatal(err)
	}
	processed, err := s.ProcessNextDeletion(ctx, committedWatermark)
	if err == nil || processed != request.RequestID {
		t.Fatalf("processed=%q err=%v", processed, err)
	}
	if _, dropErr := s.DB().ExecContext(ctx, `DROP TRIGGER fail_account_anonymization`); dropErr != nil {
		t.Fatal(dropErr)
	}
	got, err := s.GetDeletionRequest(ctx, request.RequestID, request.UserID)
	if err != nil || got.Status != "failed" || got.ErrorCode != "DELETION_PROCESSING_FAILED" {
		t.Fatalf("deletion request=%+v err=%v", got, err)
	}
	user, err := s.GetUser(ctx, request.UserID)
	if err != nil || user.AccountStatus != "active" {
		t.Fatalf("user changed despite rollback: user=%+v err=%v", user, err)
	}
}

func TestPrivateAndTeamScopeAuthorization(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(78), id(79))
	createUserAndInstallation(t, s, id(80), id(81))
	if _, err := s.DB().ExecContext(ctx, `UPDATE users SET leaderboard_visibility='team' WHERE user_id=?`, id(78)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO teams(team_id,team_name) VALUES('team-a','Team A')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO team_memberships(team_id,user_id) VALUES('team-a',?)`, id(78)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		userID, scopeType, scopeKey string
		want                        bool
	}{
		{"", "global", "all", true},
		{id(78), "private", id(78), true},
		{id(80), "private", id(78), false},
		{id(78), "team", "team-a", true},
		{id(80), "team", "team-a", false},
	}
	for _, tc := range cases {
		got, err := s.AuthorizeLeaderboardScope(ctx, tc.userID, tc.scopeType, tc.scopeKey)
		if err != nil || got != tc.want {
			t.Fatalf("authorize %s/%s/%s=%v want=%v err=%v", tc.userID, tc.scopeType, tc.scopeKey, got, tc.want, err)
		}
	}
}

func TestConcurrentSnapshotPublishAndDeletionRebuild(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	createUserAndInstallation(t, s, id(71), id(72))
	commitExactEvent(t, s, id(73), id(72), id(71), 50)
	worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: time.Millisecond}
	time.Sleep(3 * time.Millisecond)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- worker.BuildLeaderboardSnapshotScoped(ctx, s, id(74+i), "today", "global", "all", "total_tokens", now.Add(-24*time.Hour), now.Add(time.Second))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var published int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM leaderboard_snapshots WHERE snapshot_status='published'`).Scan(&published); err != nil || published != 1 {
		t.Fatalf("published snapshots=%d err=%v", published, err)
	}
	request := mysqlstore.DeletionRequest{RequestID: id(76), UserID: id(71), Scope: "all_usage", RequestedAt: time.Now().UTC()}
	if err := s.CreateDeletionRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	if processed, err := s.ProcessNextDeletion(ctx, committedWatermark); err != nil || processed != request.RequestID {
		t.Fatalf("process deletion=%q err=%v", processed, err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM leaderboard_snapshots WHERE snapshot_status='published'`).Scan(&published); err != nil || published != 0 {
		t.Fatalf("deletion left published snapshots=%d err=%v", published, err)
	}
	if err := worker.BuildLeaderboardSnapshotScoped(ctx, s, id(77), "today", "global", "all", "total_tokens", now.Add(-24*time.Hour), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.LatestPublishedSnapshotScoped(ctx, "today", "global", "all", "total_tokens")
	if err != nil || snapshot.ParticipantCount != 0 {
		t.Fatalf("rebuilt snapshot=%+v err=%v", snapshot, err)
	}
	progress, err := s.GetAggregationProgress(ctx, committedWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.DataWatermarkAt.Equal(progress.CommittedThrough) {
		t.Fatalf("snapshot watermark=%s committedThrough=%s", snapshot.DataWatermarkAt, progress.CommittedThrough)
	}
}

func insertCommittedEventWithPK(t *testing.T, s *mysqlstore.Store, eventPK uint64, batchID, installationID, userID string, tokens uint64) {
	t.Helper()
	ctx := context.Background()
	hash := sha256.Sum256([]byte("raw-" + batchID))
	eventID := sha256.Sum256([]byte("raw-event-" + batchID))
	now := time.Now().UTC()
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO ingest_batches(batch_id,installation_id,request_sha256,event_count,accepted_count,batch_status,received_at,committed_at) VALUES(?,?,?,?,1,'committed',?,?)`, batchID, installationID, hash[:], 1, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO usage_events(event_pk,event_id,schema_version,batch_id,installation_id,user_id,adapter_id,adapter_version,agent_id,event_type,accuracy,source_kind,source_cursor_hmac,raw_fingerprint_hmac,occurred_at,received_at,token_total,privacy_policy_version) VALUES(?,?,'1.0.0',?,?,?,?,?,'agent','model_usage_recorded','exact','runtime_stream',?,?,?,?,?,1)`, eventPK, eventID[:], batchID, installationID, userID, "test", "1.0.0", hash[:], hash[:], now, now, tokens); err != nil {
		t.Fatal(err)
	}
}

func commitExactEvent(t *testing.T, s *mysqlstore.Store, batchID, installationID, userID string, tokens uint64) {
	t.Helper()
	commitEventAt(t, s, batchID, installationID, userID, tokens, time.Now().UTC())
}

func commitEventAt(t *testing.T, s *mysqlstore.Store, batchID, installationID, userID string, tokens uint64, occurredAt time.Time) {
	t.Helper()
	hash := sha256.Sum256([]byte("batch-" + batchID))
	eventID := sha256.Sum256([]byte("event-" + batchID))
	e := event(batchID, installationID, userID, eventID, tokens)
	e.OccurredAt = occurredAt
	e.OccurredDate = occurredAt
	if _, err := s.CommitBatch(context.Background(), batch(batchID, installationID, hash), []*store.IngestEvent{{Event: e}}, nil); err != nil {
		t.Fatal(err)
	}
}

func assertExactTokens(t *testing.T, s *mysqlstore.Store, userID string, expected uint64) {
	t.Helper()
	metrics, err := s.GetDailyMetrics(context.Background(), userID, time.Now().AddDate(-1, 0, 0), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, metric := range metrics {
		total += metric.ExactTokenTotal
	}
	if total != expected {
		t.Fatalf("exact token total=%d want=%d metrics=%s", total, expected, fmt.Sprint(metrics))
	}
}
