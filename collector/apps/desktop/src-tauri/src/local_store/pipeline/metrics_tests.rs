//! P4 local metrics + query facade golden tests.

use chrono::{FixedOffset, TimeZone};
use rusqlite::params;

use super::*;
use crate::local_store::pipeline::types::{
    Consumer, CursorKind, EventCandidate, RegisterSource, SourceCommitBatch, SourceKind,
    DEFAULT_LEASE_MS,
};

fn blob(seed: u8) -> [u8; 32] {
    [seed; 32]
}

fn beijing() -> FixedOffset {
    FixedOffset::east_opt(8 * 3600).unwrap()
}

fn ms(y: i32, m: u32, d: u32, h: u32, min: u32) -> i64 {
    beijing()
        .with_ymd_and_hms(y, m, d, h, min, 0)
        .single()
        .unwrap()
        .timestamp_millis()
}

fn open_store() -> PipelineStore {
    let mut store = PipelineStore::open_in_memory().expect("open");
    store.set_clock_ms(ms(2024, 6, 15, 12, 0));
    store
}

fn register(store: &mut PipelineStore, harness: &str, source_seed: u8) -> (i64, String) {
    let id = store
        .register_source(&RegisterSource {
            harness_id: harness.into(),
            source_key: blob(source_seed),
            source_kind: SourceKind::Jsonl,
            locator_ref: format!("local:{harness}/session.jsonl"),
            stream_key: "main".into(),
            cursor_kind: CursorKind::ByteOffset,
            cursor_json: r#"{"offset":0}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: r#"{"baseline":0}"#.into(),
            observed_boundary_json: r#"{"len":0}"#.into(),
            next_poll_at: Some(ms(2024, 6, 15, 12, 0)),
        })
        .unwrap();
    let (token, _, _) = store.lease_source(id, DEFAULT_LEASE_MS).unwrap();
    (id, token)
}

fn commit(
    store: &mut PipelineStore,
    source_id: i64,
    token: &str,
    seq: i64,
    events: Vec<EventCandidate>,
) {
    store
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: seq,
            lease_token: token.into(),
            cursor_json: format!(r#"{{"offset":{seq}}}"#),
            decoder_state_version: 1,
            decoder_state_json: r#"{"baseline":1}"#.into(),
            observed_boundary_json: r#"{"len":10}"#.into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: Some(ms(2024, 6, 15, 13, 0)),
            events,
            created_at_override: None,
        })
        .unwrap();
}

fn drain_all(store: &mut PipelineStore) {
    for c in [Consumer::Hour, Consumer::Day, Consumer::Month] {
        loop {
            let stats = store
                .drain_metrics_consumer(c, 64, DEFAULT_LEASE_MS)
                .unwrap();
            if stats.claimed == 0 {
                break;
            }
            assert_eq!(stats.failed, 0, "consumer {:?} apply failures", c);
        }
    }
}

fn harness_active(store: &mut PipelineStore, grain: &str, bucket: i64, harness: &str) -> i64 {
    store
        .with_connection(|conn| {
            Ok(conn
                .query_row(
                    "SELECT active_duration_ms FROM harness_metrics
                     WHERE grain=?1 AND bucket_start=?2 AND harness_id=?3",
                    params![grain, bucket, harness],
                    |r| r.get(0),
                )
                .unwrap_or(0))
        })
        .unwrap()
}

fn harness_sessions(store: &mut PipelineStore, grain: &str, bucket: i64, harness: &str) -> i64 {
    store
        .with_connection(|conn| {
            Ok(conn
                .query_row(
                    "SELECT session_count FROM harness_metrics
                     WHERE grain=?1 AND bucket_start=?2 AND harness_id=?3",
                    params![grain, bucket, harness],
                    |r| r.get(0),
                )
                .unwrap_or(0))
        })
        .unwrap()
}

fn base_event(seed: u8, event_type: &str, occurred_at: i64, payload: &str) -> EventCandidate {
    EventCandidate {
        event_id: blob(seed),
        fact_key: blob(seed.wrapping_add(40)),
        fact_revision: 1,
        event_type: event_type.into(),
        schema_version: 2,
        metric_semantics_version: 1,
        content_hash: blob(seed.wrapping_add(80)),
        occurred_at,
        model_key: 0,
        skill_id: None,
        session_key: Some(blob(7)),
        turn_key: None,
        cost_scope_key: None,
        payload_json: payload.into(),
        applicable_consumers: vec![Consumer::Hour, Consumer::Day, Consumer::Month],
    }
}

#[test]
fn duration_out_of_order_converges() {
    // Turns then session_end, and reverse order → same day duration.
    for reverse in [false, true] {
        let mut store = open_store();
        let (sid, token) = register(&mut store, "codex", 1);
        let day = bucket_start(Grain::Day, ms(2024, 6, 15, 9, 0));
        let t1 = base_event(
            1,
            "turn_completed",
            ms(2024, 6, 15, 9, 0),
            r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{"duration_ms":100,"trigger":"user"}}"#,
        );
        let mut t1 = t1;
        t1.turn_key = Some(blob(11));
        let t2 = base_event(
            2,
            "turn_completed",
            ms(2024, 6, 15, 10, 0),
            r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{"duration_ms":200,"trigger":"user"}}"#,
        );
        let mut t2 = t2;
        t2.turn_key = Some(blob(12));
        let end = base_event(
            3,
            "session_ended",
            ms(2024, 6, 15, 11, 0),
            r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{"duration_ms":500}}"#,
        );
        let events = if reverse {
            vec![end, t1, t2]
        } else {
            vec![t1, t2, end]
        };
        commit(&mut store, sid, &token, 0, events);
        drain_all(&mut store);
        assert_eq!(harness_active(&mut store,"day",day,"codex"),7_200_000,"reverse={reverse}");
        for (hour,expected) in [(9,3_600_000),(10,3_600_000),(11,0)] {let bucket=bucket_start(Grain::Hour,ms(2024,6,15,hour,0));assert_eq!(harness_active(&mut store,"hour",bucket,"codex"),expected,"reverse={reverse}");}

    }
}

#[test]
fn cross_day_session_month_dedupes() {
    let mut store = open_store();
    let (sid, token) = register(&mut store, "codex", 1);
    let d1 = bucket_start(Grain::Day, ms(2024, 6, 15, 23, 0));
    let d2 = bucket_start(Grain::Day, ms(2024, 6, 16, 1, 0));
    let month = bucket_start(Grain::Month, ms(2024, 6, 15, 23, 0));
    let e1 = base_event(
        1,
        "session_started",
        ms(2024, 6, 15, 23, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{"trigger":"user"}}"#,
    );
    let e2 = base_event(
        2,
        "session_started",
        ms(2024, 6, 16, 1, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{"trigger":"user"}}"#,
    );
    // Same session_key across days (identity already on base_event).
    commit(&mut store, sid, &token, 0, vec![e1, e2]);
    drain_all(&mut store);
    assert_eq!(harness_sessions(&mut store, "day", d1, "codex"), 1);
    assert_eq!(harness_sessions(&mut store, "day", d2, "codex"), 1);
    assert_eq!(harness_sessions(&mut store, "month", month, "codex"), 1);
    assert_ne!(
        harness_sessions(&mut store, "day", d1, "codex")
            + harness_sessions(&mut store, "day", d2, "codex"),
        harness_sessions(&mut store, "month", month, "codex")
    );
}

#[test]
fn cost_bill_replaces_price_table() {
    let mut store = open_store();
    let (sid, token) = register(&mut store, "codex", 1);
    let day = bucket_start(Grain::Day, ms(2024, 6, 15, 12, 0));
    let scope = blob(9);
    let mut c1 = base_event(
        1,
        "cost_recorded",
        ms(2024, 6, 15, 12, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"cost":{"units":200000000,"currency":"USD","source":"estimated_price_table"}}"#,
    );
    c1.cost_scope_key = Some(scope);
    c1.session_key = None;
    let mut c2 = base_event(
        2,
        "cost_recorded",
        ms(2024, 6, 15, 12, 5),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"cost":{"units":300000000,"currency":"USD","source":"estimated_price_table"}}"#,
    );
    c2.cost_scope_key = Some(scope);
    c2.session_key = None;
    let mut bill = base_event(
        3,
        "cost_recorded",
        ms(2024, 6, 15, 12, 10),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"cost":{"units":400000000,"currency":"USD","source":"provider_reported"}}"#,
    );
    bill.cost_scope_key = Some(scope);
    bill.session_key = None;
    commit(&mut store, sid, &token, 0, vec![c1, c2]);
    drain_all(&mut store);
    let (est, rep) = store
        .with_connection(|conn| {
            Ok(conn.query_row(
                "SELECT estimated_cost_units, reported_cost_units FROM cost_metrics
                 WHERE grain='day' AND bucket_start=?1 AND harness_id='codex' AND currency='USD'",
                params![day],
                |r| Ok((r.get::<_, i64>(0)?, r.get::<_, i64>(1)?)),
            )?)
        })
        .unwrap();
    assert_eq!(est, 500_000_000);
    assert_eq!(rep, 0);

    let (token2, _, seq) = store.lease_source(sid, DEFAULT_LEASE_MS).unwrap();
    commit(&mut store, sid, &token2, seq, vec![bill]);
    drain_all(&mut store);
    let (est, rep) = store
        .with_connection(|conn| {
            Ok(conn.query_row(
                "SELECT estimated_cost_units, reported_cost_units FROM cost_metrics
                 WHERE grain='day' AND bucket_start=?1 AND harness_id='codex' AND currency='USD'",
                params![day],
                |r| Ok((r.get::<_, i64>(0)?, r.get::<_, i64>(1)?)),
            )?)
        })
        .unwrap();
    assert_eq!(est, 0);
    assert_eq!(rep, 400_000_000);
}

#[test]
fn skill_multi_harness_one_active_day() {
    let mut store = open_store();
    let skill_key = blob(55);
    let skill_id = store.register_skill(&skill_key, Some("search")).unwrap();
    // Re-register must not change name / create second row.
    assert_eq!(
        store.register_skill(&skill_key, Some("other")).unwrap(),
        skill_id
    );

    let (sid_a, token_a) = register(&mut store, "codex", 1);
    let (sid_b, token_b) = register(&mut store, "opencode", 2);
    let day = bucket_start(Grain::Day, ms(2024, 6, 15, 12, 0));
    let mut e1 = base_event(
        1,
        "skill_invoked",
        ms(2024, 6, 15, 12, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{"success":true,"duration_ms":10}}"#,
    );
    e1.skill_id = Some(skill_id);
    e1.session_key = None;
    let mut e2 = base_event(
        2,
        "skill_invoked",
        ms(2024, 6, 15, 13, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{"success":false,"duration_ms":5}}"#,
    );
    e2.skill_id = Some(skill_id);
    e2.session_key = None;
    // e2 uses different event_id; commit on harness B needs its own event seeds.
    let mut e2b = e2.clone();
    e2b.event_id = blob(22);
    e2b.fact_key = blob(62);
    e2b.content_hash = blob(102);

    commit(&mut store, sid_a, &token_a, 0, vec![e1]);
    commit(&mut store, sid_b, &token_b, 0, vec![e2b]);
    drain_all(&mut store);

    let rows: i64 = store
        .with_connection(|conn| {
            Ok(conn.query_row(
                "SELECT COUNT(*) FROM skill_metrics
                 WHERE grain='day' AND bucket_start=?1 AND skill_id=?2",
                params![day, skill_id],
                |r| r.get(0),
            )?)
        })
        .unwrap();
    assert_eq!(rows, 2, "one metrics row per harness");

    let ranks = store
        .with_connection(|conn| query_skill_ranks(conn, day, day + 86_400_000))
        .unwrap();
    assert_eq!(ranks.len(), 1);
    assert_eq!(ranks[0].active_days, 1);
    assert_eq!(ranks[0].use_count, 2);
    assert_eq!(ranks[0].success_rate.numerator, 1);
    assert_eq!(ranks[0].success_rate.denominator, 2);
}

#[test]
fn independent_consumer_failure_does_not_clobber_others() {
    let mut store = open_store();
    let (sid, token) = register(&mut store, "codex", 1);
    let ev = base_event(
        1,
        "model_usage_recorded",
        ms(2024, 6, 15, 12, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":10,"input_context_tokens":8,"cache_read_tokens":2,"output_tokens":2}}"#,
    );
    commit(&mut store, sid, &token, 0, vec![ev]);

    // Apply hour + month; leave day pending by only draining those lanes.
    store
        .drain_metrics_consumer(Consumer::Hour, 16, DEFAULT_LEASE_MS)
        .unwrap();
    store
        .drain_metrics_consumer(Consumer::Month, 16, DEFAULT_LEASE_MS)
        .unwrap();

    let row_id = store.event_row_id_by_event_id(&blob(1)).unwrap().unwrap();
    let status = store.event_status_json(row_id).unwrap();
    let v: serde_json::Value = serde_json::from_str(&status).unwrap();
    assert_eq!(v["hour"], 3);
    assert_eq!(v["day"], 0);
    assert_eq!(v["month"], 3);

    // Force a day apply by draining; should succeed independently.
    store
        .drain_metrics_consumer(Consumer::Day, 16, DEFAULT_LEASE_MS)
        .unwrap();
    let status = store.event_status_json(row_id).unwrap();
    let v: serde_json::Value = serde_json::from_str(&status).unwrap();
    assert_eq!(v["hour"], 3);
    assert_eq!(v["day"], 3);
    assert_eq!(v["month"], 3);
}

#[test]
fn cache_hit_rate_sums_then_divides() {
    let mut store = open_store();
    let (sid, token) = register(&mut store, "codex", 1);
    let day = bucket_start(Grain::Day, ms(2024, 6, 15, 12, 0));
    let e1 = base_event(
        1,
        "model_usage_recorded",
        ms(2024, 6, 15, 12, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":100,"input_context_tokens":100,"cache_read_tokens":90}}"#,
    );
    let e2 = base_event(
        2,
        "model_usage_recorded",
        ms(2024, 6, 15, 13, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":1000,"input_context_tokens":1000,"cache_read_tokens":10}}"#,
    );
    commit(&mut store, sid, &token, 0, vec![e1, e2]);
    drain_all(&mut store);

    let summary = store
        .with_connection(|conn| query_usage_summary(conn, Grain::Day, day, day + 86_400_000, None))
        .unwrap();
    assert_eq!(summary.cache_hit_rate.numerator, 100);
    assert_eq!(summary.cache_hit_rate.denominator, 1100);
    let rate = summary.cache_hit_rate.value.unwrap();
    assert!((rate - 100.0 / 1100.0).abs() < 1e-12);
    // Denom 0 → null
    let empty = store
        .with_connection(|conn| {
            query_usage_summary(
                conn,
                Grain::Day,
                day + 86_400_000,
                day + 2 * 86_400_000,
                None,
            )
        })
        .unwrap();
    assert!(empty.cache_hit_rate.value.is_none());
}

#[test]
fn duplicate_apply_rejected_no_double_count() {
    let mut store = open_store();
    let (sid, token) = register(&mut store, "codex", 1);
    let ev = base_event(
        1,
        "model_usage_recorded",
        ms(2024, 6, 15, 12, 0),
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":10}}"#,
    );
    commit(&mut store, sid, &token, 0, vec![ev]);
    let leased = store
        .claim_tasks(Consumer::Day, 8, DEFAULT_LEASE_MS)
        .unwrap();
    assert_eq!(leased.len(), 1);
    store.apply_and_complete_metrics(&leased[0]).unwrap();
    // Stale second apply with same lease must fail.
    let err = store.apply_and_complete_metrics(&leased[0]).unwrap_err();
    assert!(matches!(
        err,
        PipelineError::TaskNotRunnable | PipelineError::TaskLeaseMismatch
    ));
    let day = bucket_start(Grain::Day, ms(2024, 6, 15, 12, 0));
    let tokens: i64 = store
        .with_connection(|conn| {
            Ok(conn.query_row(
                "SELECT exact_token_total FROM model_metrics
                 WHERE grain='day' AND bucket_start=?1 AND harness_id='codex'",
                params![day],
                |r| r.get(0),
            )?)
        })
        .unwrap();
    assert_eq!(tokens, 10);
}

#[test]
fn usage_revision_replaces_prior_contribution_in_either_arrival_order() {
    for reversed in [false, true] {
        let mut store = open_store();
        let (sid, token) = register(&mut store, "codex", 1);
        let mut old = base_event(
            61,
            "model_usage_recorded",
            ms(2024, 6, 15, 12, 0),
            r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":10,"input_context_tokens":8,"output_tokens":2}}"#,
        );
        old.fact_revision = 1;
        let mut new = old.clone();
        new.event_id = blob(62);
        new.fact_revision = 2;
        new.payload_json=r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":20,"input_context_tokens":18,"output_tokens":2}}"#.into();
        commit(&mut store, sid, &token, 0, vec![old, new]);
        for c in [Consumer::Hour, Consumer::Day, Consumer::Month] {
            let mut tasks = store.claim_tasks(c, 8, DEFAULT_LEASE_MS).unwrap();
            assert_eq!(tasks.len(), 2);
            if reversed {
                tasks.reverse();
            }
            for task in tasks {
                store.apply_and_complete_metrics(&task).unwrap();
            }
        }
        store.with_connection(|conn|{
            let mut q=conn.prepare("SELECT exact_token_total,token_total_known_count FROM model_metrics WHERE delete_at IS NULL")?;
            let rows=q.query_map([],|r|Ok((r.get::<_,i64>(0)?,r.get::<_,i64>(1)?)))?.collect::<Result<Vec<_>,_>>()?;
            assert_eq!(rows.len(),3);for row in rows{assert_eq!(row,(20,1),"reversed={reversed}");}Ok(())
        }).unwrap();
    }
}

#[test]
fn code_revision_replaces_prior_contribution_in_either_arrival_order() {
    for reversed in [false, true] {
        let mut store = open_store();
        let (sid, token) = register(&mut store, "codex", 1);
        let mut old = base_event(
            61,
            "code_changed",
            ms(2024, 6, 15, 12, 0),
            r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"code":{"generated":10,"file_touch_count":1}}"#,
        );
        old.fact_revision = 1;
        let mut new = old.clone();
        new.event_id = blob(62);
        new.fact_revision = 2;
        new.payload_json=r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"code":{"generated":20,"file_touch_count":1}}"#.into();
        commit(&mut store, sid, &token, 0, vec![old, new]);
        for c in [Consumer::Hour, Consumer::Day, Consumer::Month] {
            let mut tasks = store.claim_tasks(c, 8, DEFAULT_LEASE_MS).unwrap();
            assert_eq!(tasks.len(), 2);
            if reversed {
                tasks.reverse();
            }
            for task in tasks {
                store.apply_and_complete_metrics(&task).unwrap();
            }
        }
        store.with_connection(|conn|{
            let mut q=conn.prepare("SELECT code_generated_lines,code_known_count FROM harness_metrics WHERE delete_at IS NULL")?;
            let rows=q.query_map([],|r|Ok((r.get::<_,i64>(0)?,r.get::<_,i64>(1)?)))?.collect::<Result<Vec<_>,_>>()?;
            assert_eq!(rows.len(),3);for row in rows{assert_eq!(row,(20,1),"reversed={reversed}");}Ok(())
        }).unwrap();
    }
}

#[test]
fn session_span_crosses_month_boundary_and_backfill_is_idempotent() {
    let mut store=open_store();
    let (sid,token)=register(&mut store,"codex",1);
    let mut start=base_event(71,"session_started",ms(2024,5,31,23,30),r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"activity":{}}"#);
    start.session_key=Some(blob(99));
    let mut end=start.clone(); end.event_id=blob(72);end.fact_key=blob(72);end.event_type="session_ended".into();end.occurred_at=ms(2024,6,1,0,30);
    commit(&mut store,sid,&token,0,vec![end,start]);
    for consumer in [Consumer::Hour,Consumer::Day,Consumer::Month] {
        for task in store.claim_tasks(consumer,8,DEFAULT_LEASE_MS).unwrap() {store.apply_and_complete_metrics(&task).unwrap();}
    }
    store.backfill_derived_metrics(128).unwrap();
    store.with_connection(|conn| {
        let mut q=conn.prepare("SELECT active_duration_ms,duration_known_count FROM harness_metrics WHERE delete_at IS NULL")?;
        let rows=q.query_map([],|r|Ok((r.get::<_,i64>(0)?,r.get::<_,i64>(1)?)))?.collect::<Result<Vec<_>,_>>()?;
        assert_eq!(rows.len(),6);for row in rows{assert_eq!(row,(1_800_000,1));}Ok(())
    }).unwrap();
}
