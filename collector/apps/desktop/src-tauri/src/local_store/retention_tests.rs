use super::*;
use chrono::{Duration, Utc};
use protocol::Accuracy;

fn event(id: &str, age: i64, n: u64) -> EventEnvelope {
    let at = (Utc::now().date_naive() - Duration::days(age))
        .and_hms_opt(12, 0, 0)
        .unwrap()
        .and_utc()
        .to_rfc3339();
    test_envelope_at("codex", id, &at, n, Accuracy::Exact)
}

#[test]
fn seven_days_keep_boundary_and_aggregates_survive_prune_restart_replay() {
    let dir = tempfile::tempdir().unwrap();
    let old = event("old", 7, 700);
    let keep = event("keep", 6, 600);
    let mut s = LocalStore::open(dir.path()).unwrap();
    s.commit_batch(&[old.clone(), keep.clone()], &[]).unwrap();
    assert_eq!(s.prune_details(Utc::now().date_naive()).unwrap(), 1);
    assert_eq!(s.event_count(), 1);
    drop(s);
    let mut s = LocalStore::open(dir.path()).unwrap();
    assert!(!s.commit_batch(&[old, keep], &[]).unwrap());
    assert_eq!(s.event_count(), 1);
    assert_eq!(
        s.agent_usage("codex", Local::now().date_naive())
            .unwrap()
            .total_tokens,
        1300
    );
    s.activate_account("user", "ins_test").unwrap();
    let pending = s.pending_aggregate().unwrap().unwrap();
    assert_eq!(pending.snapshot.rows[0].metrics["exact_token_total"], "700");
}

#[test]
fn ninety_days_offline_syncs_aggregates_without_event_details() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    for day in 7..97 {
        s.commit_batch(&[event(&format!("event-{day}"), day, 10)], &[])
            .unwrap();
    }
    s.prune_details(Utc::now().date_naive()).unwrap();
    assert_eq!(s.event_count(), 0);
    s.activate_account("user", "ins_test").unwrap();
    let mut total = 0;
    let mut days = 0;
    while let Some(p) = s.pending_aggregate().unwrap() {
        total += p
            .snapshot
            .rows
            .iter()
            .filter(|r| r.kind == "agent")
            .map(|r| r.metrics["exact_token_total"].parse::<u64>().unwrap())
            .sum::<u64>();
        s.ack_aggregate(&p).unwrap();
        days += 1;
    }
    assert_eq!((days, total), (90, 900));
    assert_eq!(s.aggregate_pending_count(), 0);
}

#[test]
fn stale_ack_cannot_clear_newer_revision_or_cross_accounts() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    s.activate_account("a", "ins_test").unwrap();
    s.commit_batch(&[event("a1", 0, 10)], &[]).unwrap();
    let p = s.pending_aggregate().unwrap().unwrap();
    s.commit_batch(&[event("a2", 0, 20)], &[]).unwrap();
    s.ack_aggregate(&p).unwrap();
    let newer = s.pending_aggregate().unwrap().unwrap();
    assert!(newer.snapshot.revision > p.snapshot.revision);
    assert_eq!(newer.snapshot.rows[0].metrics["exact_token_total"], "30");
    s.deactivate_account().unwrap();
    s.activate_account("b", "ins_b").unwrap();
    assert!(s.pending_aggregate().unwrap().is_none());
    assert!(s.ack_aggregate(&newer).is_err());
}

#[test]
fn failed_transaction_does_not_poison_pricing_deduplication() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    let e = event("rollback", 0, 200);
    assert!(s.commit_batch_inner(&[e.clone()], &[], &[], true).is_err());
    assert_eq!(s.event_count(), 0);
    s.commit_batch(&[e], &[]).unwrap();
    let usage = s.agent_usage("codex", Local::now().date_naive()).unwrap();
    assert_eq!(usage.total_tokens, 200);
    assert_eq!(usage.pricing.unpriced_requests, 1);
}

#[test]
fn pricing_archive_has_no_old_event_or_turn_metadata_and_preserves_cost() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    let catalog:Catalog=serde_json::from_value(serde_json::json!({"fetched_at":1,"data":[{"id":"test-model","pricing":{"prompt":"0.000002","completion":"0.00001"}}]})).unwrap();
    s.apply_prices(catalog).unwrap();
    let mut e = event("private-old-event-identity", 8, 15);
    e.turn_hash = Some("private-old-turn-identity".into());
    s.commit_batch(&[e], &[]).unwrap();
    let before = s
        .agent_usage("codex", Local::now().date_naive())
        .unwrap()
        .pricing;
    s.prune_details(Utc::now().date_naive()).unwrap();
    let json: String = s
        .conn
        .query_row("SELECT pricing_json FROM schema_meta", [], |r| r.get(0))
        .unwrap();
    assert!(!json.contains("private-old"));
    drop(s);
    let mut s = LocalStore::open(dir.path()).unwrap();
    assert_eq!(
        s.agent_usage("codex", Local::now().date_naive())
            .unwrap()
            .pricing,
        before
    );
    s.activate_account("user", "ins_test").unwrap();
    let p = s.pending_aggregate().unwrap().unwrap();
    assert_eq!(
        p.snapshot.rows[0].metrics["estimated_cost_usd_units"],
        before.estimated_usd.to_string()
    );
}

#[test]
fn offset_timestamps_are_cleaned_by_instant() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    let cutoff = (Utc::now().date_naive() - Duration::days(6))
        .and_hms_opt(0, 0, 0)
        .unwrap()
        .and_utc();
    let offset = chrono::FixedOffset::east_opt(8 * 3600).unwrap();
    let mut e = event("offset-before", 7, 1);
    e.occurred_at = (cutoff - Duration::seconds(1))
        .with_timezone(&offset)
        .to_rfc3339();
    s.commit_batch(&[e], &[]).unwrap();
    assert_eq!(s.prune_details(Utc::now().date_naive()).unwrap(), 1);
}

#[test]
fn offline_history_is_owned_atomically_even_over_one_assignment_page() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    for day in 0..105 {
        s.commit_batch(&[event(&format!("owned-{day}"), day, 1)], &[])
            .unwrap();
    }
    s.activate_account("a", "ins_a").unwrap();
    assert_eq!(s.aggregate_pending_count(), 105);
    s.deactivate_account().unwrap();
    s.activate_account("b", "ins_b").unwrap();
    assert_eq!(s.aggregate_pending_count(), 0);
    assert!(s.pending_aggregate().unwrap().is_none());
}

#[test]
fn deferred_day_does_not_block_other_days_and_new_data_resets_retry() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    s.activate_account("a", "ins_a").unwrap();
    s.commit_batch(&[event("first", 1, 10), event("second", 0, 20)], &[])
        .unwrap();
    let first = s.pending_aggregate().unwrap().unwrap();
    s.defer_aggregate(&first, "INVALID", 3600).unwrap();
    let second = s.pending_aggregate().unwrap().unwrap();
    assert_ne!(first.snapshot.day, second.snapshot.day);
    s.ack_aggregate(&second).unwrap();
    assert!(s.pending_aggregate().unwrap().is_none());
    assert_eq!(s.aggregate_pending_count(), 1);
    s.commit_batch(&[event("correction", 1, 5)], &[]).unwrap();
    assert!(s.pending_aggregate().unwrap().unwrap().snapshot.revision > first.snapshot.revision);
}

#[test]
fn activity_and_generated_code_survive_pruning_without_double_counting_duration() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = LocalStore::open(dir.path()).unwrap();
    let mut started = event("start", 8, 0);
    started.session_hash = Some("session".into());
    started.turn_hash = Some("turn".into());
    started.payload = EventPayload::TurnStarted(protocol::TurnStartedPayload {
        trigger: Some(protocol::TurnTrigger::User),
    });
    let mut completed = started.clone();
    completed.event_id = "completed".into();
    completed.payload = EventPayload::TurnCompleted(protocol::TurnCompletedPayload {
        success: true,
        duration_ms: Some("100".into()),
        error_class: None,
    });
    let mut ended = started.clone();
    ended.event_id = "ended".into();
    ended.turn_hash = None;
    ended.payload = EventPayload::SessionEnded(protocol::SessionEndedPayload {
        reason: protocol::SessionEndReason::Completed,
        duration_ms: Some("250".into()),
    });
    let mut code = started.clone();
    code.event_id = "code".into();
    code.payload = EventPayload::CodeChanged(protocol::CodeChangedPayload {
        added_lines: "1000".into(),
        removed_lines: "0".into(),
        generated_lines: Some("20".into()),
        accepted_lines: Some("10".into()),
        file_count: 1,
        language: None,
    });
    s.commit_batch(&[ended, completed.clone(), started, code], &[])
        .unwrap();
    completed.event_id = "another-completion-record".into();
    s.commit_batch(&[completed], &[]).unwrap();
    s.prune_details(Utc::now().date_naive()).unwrap();
    assert_eq!(s.event_count(), 0);
    s.activate_account("a", "ins_a").unwrap();
    let p = s.pending_aggregate().unwrap().unwrap();
    let row = p.snapshot.rows.iter().find(|r| r.kind == "agent").unwrap();
    for (k, v) in [
        ("active_duration_ms", "250"),
        ("session_count", "1"),
        ("interaction_turn_count", "1"),
        ("message_count", "2"),
        ("user_message_count", "1"),
        ("code_generated_lines", "20"),
        ("code_accepted_lines", "10"),
    ] {
        assert_eq!(row.metrics[k], v, "{k}");
    }
}
