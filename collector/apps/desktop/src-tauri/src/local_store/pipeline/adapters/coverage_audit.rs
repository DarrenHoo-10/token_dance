//! Native contract regressions and opt-in, read-only local source diagnostics.
use super::*;
use crate::local_store::pipeline::runner::{
    DecodeOutcome, DecoderState, HarnessStrategy, RawRecord,
};
use serde_json::{json, Value};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    sync::Arc,
};

fn probe(strategy: &dyn HarnessStrategy, label: &str, records: Vec<Value>, sqlite: bool) {
    let count = records.len();
    assert!(count > 0, "empty audit fixture {label}");
    let mut state = DecoderState {
        version: 1,
        json: json!({}),
    };
    let mut events = BTreeMap::<String, u64>::new();
    let (mut context, mut ignored, mut errors, mut tokens, mut skills_success, mut skills_failure) =
        (0, 0, 0, 0, 0, 0);
    for (i, v) in records.iter().enumerate() {
        let row = RawRecord {
            ordinal: i as u64,
            byte_start: Some(i as u64),
            byte_end: Some(i as u64 + 1),
            native_rowid: sqlite.then_some(i as i64 + 1),
            payload: v.to_string().into_bytes(),
            file_mtime_ms: Some(1789142400000),
        };
        match strategy.decode(&row, &mut state, "audit-fixture") {
            Ok(DecodeOutcome::Emit(facts)) => {
                for fact in facts {
                    *events.entry(fact.event_type.clone()).or_default() += 1;
                    tokens += fact
                        .payload_sections
                        .pointer("/usage/token_total")
                        .and_then(Value::as_u64)
                        .unwrap_or(0);
                    if fact.event_type == "skill_invoked" {
                        match fact
                            .payload_sections
                            .pointer("/activity/success")
                            .and_then(Value::as_bool)
                        {
                            Some(true) => skills_success += 1,
                            Some(false) => skills_failure += 1,
                            _ => {}
                        }
                    }
                }
            }
            Ok(DecodeOutcome::ContextOnly) => context += 1,
            Ok(DecodeOutcome::Ignore(_)) => ignored += 1,
            Err(_) => errors += 1,
        }
    }
    assert_eq!(errors, 0, "{} {label} must decode", strategy.harness_id());
    let expected = match (strategy.harness_id(), label) {
        ("claude-code", "session.jsonl") => Some((77, 0)),
        ("claude-code" | "grok-build", "history.jsonl") => Some((35, 1)),
        ("deepseek-harness", "history.jsonl") => Some((35, 1)),
        ("grok-build", "session-updates.jsonl") => Some((170, 0)),
        ("pi", "known.json") => Some((9018, 0)),
        ("pi", "errors.json") => Some((310, 0)),
        ("workbuddy" | "doubao-work", "known.json") => Some((100, 0)),
        ("zcode", "actual-sql-projection") => Some((12, 1)),
        ("opencode", "actual-sql-projection") => Some((46, 1)),
        _ => None,
    };
    if let Some((total, skills)) = expected {
        assert_eq!(
            tokens,
            total,
            "{} {label} token composition",
            strategy.harness_id()
        );
        assert_eq!(
            skills_success,
            skills,
            "{} {label} successful skills",
            strategy.harness_id()
        );
    }
    println!(
        "AUDIT {}",
        json!({"harness":strategy.harness_id(),"sample":label,"records":count,"events":events,"context":context,"ignored":ignored,"errors":errors,"tokens":tokens,"skills_success":skills_success,"skills_failure":skills_failure})
    );
}
fn fixture(root: &Path, harness: &str, file: &str) -> Vec<Value> {
    let text =
        std::fs::read_to_string(root.join(harness).join("fixtures/contract").join(file)).unwrap();
    let mut values = match serde_json::from_str::<Value>(&text) {
        Ok(Value::Array(v)) => v,
        Ok(v) => vec![v],
        Err(_) => text
            .lines()
            .filter(|l| !l.trim().is_empty())
            .map(|l| serde_json::from_str(l).unwrap())
            .collect(),
    };
    if values.len() == 1 {
        for key in ["records", "events"] {
            if let Some(v) = values[0].get(key).and_then(Value::as_array) {
                return v.clone();
            }
        }
    }
    values.shrink_to_fit();
    values
}
#[test]
fn audit_all_harness_native_formats() {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../adapters");
    let allocator = Arc::new(|_key: [u8; 32], _name: &str| 1i64);
    let secret = b"audit-fixture-secret".to_vec();
    let codex = codex::CodexStrategy::new(
        secret.clone(),
        "unused",
        common::SkillBook::new(),
        allocator.clone(),
    );
    probe(
        &codex,
        "legacy-session-fixture",
        fixture(&root, "codex", "session.jsonl"),
        false,
    );
    probe(
        &codex,
        "native-session",
        vec![
            json!({"type":"session_meta","timestamp":1789142400000i64,"payload":{"id":"session"}}),
            json!({"type":"event_msg","timestamp":1789142400001i64,"payload":{"type":"task_started","turn_id":"t"}}),
            json!({"type":"event_msg","timestamp":1789142400002i64,"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110},"total_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}}),
            json!({"type":"event_msg","timestamp":1789142400101i64,"payload":{"type":"task_complete","turn_id":"t"}}),
        ],
        false,
    );
    for (profile, dir, files) in [
        (
            jsonl_harness::CLAUDE,
            "claude",
            vec!["session.jsonl", "history.jsonl"],
        ),
        (
            jsonl_harness::GROK,
            "grok-build",
            vec!["session-updates.jsonl", "history.jsonl"],
        ),
        (
            jsonl_harness::DEEPSEEK,
            "deepseek-harness",
            vec!["history.jsonl"],
        ),
        (jsonl_harness::PI, "pi", vec!["known.json", "errors.json"]),
        (jsonl_harness::WORKBUDDY, "workbuddy", vec!["known.json"]),
        (jsonl_harness::DOUBAO, "doubao-work", vec!["known.json"]),
    ] {
        let strategy = jsonl_harness::JsonlHarnessStrategy::new(
            profile,
            secret.clone(),
            "unused",
            common::SkillBook::new(),
            allocator.clone(),
        );
        for file in files {
            probe(&strategy, file, fixture(&root, dir, file), false);
        }
    }
    let cursor = cursor::CursorStrategy::new(
        secret.clone(),
        "unused",
        common::SkillBook::new(),
        allocator.clone(),
    )
    .with_usage(cursor_usage::CursorUsagePaths {
        chats: PathBuf::from("unused"),
        database: PathBuf::from("unused"),
        auth: PathBuf::from("unused"),
    });
    probe(
        &cursor,
        "api-enabled-transcript",
        vec![
            json!({"type":"user","timestamp":1789142400000i64,"text":"fixture"}),
            json!({"type":"skill","timestamp":1789142400000i64,"skill":"fixture"}),
        ],
        false,
    );
    let zcode = zcode::ZcodeStrategy::new(
        secret.clone(),
        "unused",
        common::SkillBook::new(),
        allocator.clone(),
    );
    let opencode =
        opencode::OpenCodeStrategy::new(secret, "unused", common::SkillBook::new(), allocator);
    for (strategy, dir) in [
        (&zcode as &dyn HarnessStrategy, "zcode"),
        (&opencode as &dyn HarnessStrategy, "opencode"),
    ] {
        probe(
            strategy,
            "legacy-record-projection",
            fixture(&root, dir, "known.json"),
            true,
        );
        let db = rusqlite::Connection::open_in_memory().unwrap();
        db.execute_batch("CREATE TABLE session(id TEXT,model TEXT,time_created INTEGER); CREATE TABLE part(session_id TEXT,time_created INTEGER,time_updated INTEGER,data TEXT); CREATE TABLE model_usage(session_id TEXT,completed_at INTEGER,status TEXT,provider_id TEXT,model_id TEXT,input_tokens INTEGER,output_tokens INTEGER,computed_total_tokens INTEGER); INSERT INTO session VALUES('s','m',1789142400000); INSERT INTO model_usage VALUES('s',1789142400000,'completed','p','m',10,2,12);").unwrap();
        db.execute_batch("ALTER TABLE model_usage ADD cache_read_input_tokens INTEGER; ALTER TABLE model_usage ADD cache_creation_input_tokens INTEGER; ALTER TABLE model_usage ADD reasoning_tokens INTEGER;").unwrap();
        for data in [
            json!({"type":"step-finish","tokens":{"input":10,"output":2,"cache":{"read":30,"write":4}}}),
            json!({"type":"tool","callID":"skill-call","tool":"skill","state":{"status":"completed","input":{"name":"fixture-skill"}}}),
            json!({"type":"tool","callID":"edit-call","tool":"edit","state":{"status":"completed","input":{"oldString":"a","newString":"b"}}}),
        ] {
            db.execute(
                "INSERT INTO part VALUES ('s',1789142400000,1789142400000,?1)",
                [data.to_string()],
            )
            .unwrap();
        }
        db.execute_batch(
            "ALTER TABLE part ADD message_id TEXT; CREATE TABLE message(id TEXT,data TEXT);",
        )
        .unwrap();
        let queries = if dir == "zcode" {
            vec![
                (zcode::SQL_SESSION, 1),
                (zcode::SQL_USAGE, 1),
                (zcode::SQL_CODE, 2),
            ]
        } else {
            vec![
                (opencode::SQL_SESSION, 1),
                (opencode::SQL_STEP, 1),
                (opencode::SQL_CODE, 2),
            ]
        };
        let mut records = Vec::new();
        for (sql, n) in queries {
            let mut q = db.prepare(sql).unwrap();
            let params = vec![0i64; n];
            let rows = q
                .query_map(rusqlite::params_from_iter(params), |r| {
                    r.get::<_, String>(3)
                })
                .unwrap();
            for row in rows {
                records.push(serde_json::from_str(&row.unwrap()).unwrap());
            }
        }
        probe(strategy, "actual-sql-projection", records, true);
    }
}

#[test]
#[ignore = "read-only native DSH audit; requires TOKENDANCE_DSH_AUDIT_ROOT"]
fn audit_local_deepseek_compressed_sources() {
    use crate::local_store::pipeline::runner::{CheckpointView, DiscoveryBudget, ReadBudget};
    let root =
        std::env::var("TOKENDANCE_DSH_AUDIT_ROOT").expect("explicit raw source root required");
    let strategy = jsonl_harness::JsonlHarnessStrategy::new(
        jsonl_harness::DEEPSEEK,
        vec![9; 32],
        root,
        SkillBook::new(),
        Arc::new(|_, _| 1),
    );
    let sources = strategy
        .discover(DiscoveryBudget::new(10000, 1000))
        .unwrap();
    assert!(!sources.is_empty());
    let mut records = 0;
    let mut usage = 0;
    let mut skills = 0;
    let mut failures = 0;
    for source in &sources {
        let mut cp = CheckpointView {
            cursor_json: json!({"offset":0}),
            decoder_state_version: 1,
            decoder_state_json: json!({}),
            observed_boundary_json: json!({}),
            commit_seq: 0,
        };
        let mut state = DecoderState {
            version: 1,
            json: json!({}),
        };
        loop {
            let batch = strategy
                .read(
                    &source.locator_ref,
                    &source.stream_key,
                    &cp,
                    ReadBudget::new(128, 1024 * 1024, 100),
                )
                .unwrap();
            records += batch.records.len();
            for record in &batch.records {
                if let DecodeOutcome::Emit(facts) = strategy
                    .decode(record, &mut state, &source.locator_ref)
                    .unwrap()
                {
                    for f in facts {
                        match f.event_type.as_str() {
                            "model_usage_recorded" => usage += 1,
                            "skill_invoked" => {
                                skills += 1;
                                if f.payload_sections["activity"]["success"] == false {
                                    failures += 1;
                                }
                            }
                            _ => {}
                        }
                    }
                }
            }
            if !batch.has_more {
                break;
            }
            assert_ne!(
                cp.cursor_json, batch.next_cursor_json,
                "reader failed to advance"
            );
            cp.cursor_json = batch.next_cursor_json;
            cp.observed_boundary_json = batch.next_observed_boundary_json;
        }
    }
    assert!(usage > 0);
    assert!(skills > 0);
    println!("DSH_LOCAL_AUDIT sources={} records={records} usage={usage} skills={skills} failures={failures}",sources.len());
}

#[test]
#[ignore = "read-only all installed harness raw files; needs TOKENDANCE_NATIVE_AUDIT_HOME"]
fn audit_local_remaining_sources() {
    use crate::local_store::pipeline::runner::{CheckpointView, DiscoveryBudget, ReadBudget};
    let home = PathBuf::from(
        std::env::var("TOKENDANCE_NATIVE_AUDIT_HOME").expect("explicit home required"),
    );
    let secret = vec![8; 32];
    let book = SkillBook::new();
    let alloc: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync> = Arc::new(|_, _| 1);
    let mut strategies: Vec<Box<dyn HarnessStrategy>> = Vec::new();
    for (profile, path) in [
        (jsonl_harness::CLAUDE, ".claude/projects"),
        (jsonl_harness::GROK, ".grok/sessions"),
        (jsonl_harness::PI, ".pi/agent/sessions"),
        (jsonl_harness::WORKBUDDY, ".workbuddy/projects"),
        (jsonl_harness::DOUBAO, ".doubao-work"),
    ] {
        strategies.push(Box::new(jsonl_harness::JsonlHarnessStrategy::new(
            profile,
            secret.clone(),
            home.join(path),
            book.clone(),
            alloc.clone(),
        )));
    }
    strategies.push(Box::new(cursor::CursorStrategy::new(
        secret.clone(),
        home.join(".cursor/projects"),
        book.clone(),
        alloc.clone(),
    )));
    strategies.push(Box::new(zcode::ZcodeStrategy::new(
        secret.clone(),
        home.join(".zcode/cli/db/db.sqlite"),
        book.clone(),
        alloc.clone(),
    )));
    let open = [
        home.join(".local/share/opencode/opencode.db"),
        home.join(".opencode/opencode.db"),
    ]
    .into_iter()
    .find(|p| p.is_file())
    .expect("opencode raw db");
    strategies.push(Box::new(opencode::OpenCodeStrategy::new(
        secret, open, book, alloc,
    )));
    for strategy in strategies {
        if std::env::var("TOKENDANCE_NATIVE_AUDIT_HARNESS")
            .ok()
            .is_some_and(|h| h != strategy.harness_id())
        {
            continue;
        }
        let sources = strategy
            .discover(DiscoveryBudget::new(10000, 1000))
            .unwrap();
        let mut records = 0;
        let mut events = BTreeMap::<String, usize>::new();
        let mut errors = 0;
        for source in &sources {
            let mut cp = CheckpointView {
                cursor_json: source.initial_cursor_json.clone(),
                decoder_state_version: 1,
                decoder_state_json: json!({}),
                observed_boundary_json: json!({}),
                commit_seq: 0,
            };
            let mut state = DecoderState {
                version: 1,
                json: json!({}),
            };
            loop {
                let batch = match strategy.read(
                    &source.locator_ref,
                    &source.stream_key,
                    &cp,
                    ReadBudget::new(512, 4 * 1024 * 1024, 100),
                ) {
                    Ok(b) => b,
                    Err(e) => {
                        eprintln!(
                            "native audit {} {}: {e}",
                            strategy.harness_id(),
                            source.stream_key
                        );
                        errors += 1;
                        break;
                    }
                };
                records += batch.records.len();
                for record in &batch.records {
                    match strategy.decode(record, &mut state, &source.locator_ref) {
                        Ok(DecodeOutcome::Emit(facts)) => {
                            for f in facts {
                                let mut payload = f.payload_sections.clone();
                                payload["meta"] = json!({"accuracy":f.accuracy.as_str(),"time_source":f.time_source.as_str()});
                                crate::local_store::pipeline::content_hash::compute_p0_content_hash(strategy.harness_id(),&f,&payload).expect("native fact must pass wire hash validation");
                                *events.entry(f.event_type).or_default() += 1;
                            }
                        }
                        Err(_) => errors += 1,
                        _ => {}
                    }
                }
                if !batch.has_more {
                    break;
                }
                assert_ne!(cp.cursor_json, batch.next_cursor_json);
                cp.cursor_json = batch.next_cursor_json;
                cp.observed_boundary_json = batch.next_observed_boundary_json;
            }
        }
        println!(
            "LOCAL_HARNESS_AUDIT {}",
            json!({"harness":strategy.harness_id(),"sources":sources.len(),"records":records,"events":events,"errors":errors})
        );
        assert_eq!(errors, 0, "{} raw read failed", strategy.harness_id());
    }
}

// Check persisted decoder state, result pairing, identity and upload validation together.
fn replay_facts(
    strategy: &dyn HarnessStrategy,
    rows: &[Value],
) -> Vec<crate::local_store::pipeline::runner::FactDraft> {
    let mut state = DecoderState {
        version: 1,
        json: json!({}),
    };
    let mut facts = Vec::new();
    for (i, value) in rows.iter().enumerate() {
        let record = RawRecord {
            ordinal: i as u64,
            byte_start: Some(i as u64),
            byte_end: Some(i as u64 + 1),
            native_rowid: None,
            payload: value.to_string().into_bytes(),
            file_mtime_ms: Some(1789142400000),
        };
        if let DecodeOutcome::Emit(mut emitted) = strategy
            .decode(&record, &mut state, "contract-source")
            .unwrap()
        {
            facts.append(&mut emitted);
        }
        // Every row can end a batch or precede an application restart.
        state.json = serde_json::from_slice(&serde_json::to_vec(&state.json).unwrap()).unwrap();
    }
    facts
}
fn wire_signature(strategy: &dyn HarnessStrategy, rows: &[Value]) -> Vec<String> {
    replay_facts(strategy, rows)
        .into_iter()
        .map(|f| {
            let mut payload = f.payload_sections.clone();
            payload["meta"] =
                json!({"accuracy":f.accuracy.as_str(),"time_source":f.time_source.as_str()});
            let hash = crate::local_store::pipeline::content_hash::compute_p0_content_hash(
                strategy.harness_id(),
                &f,
                &payload,
            )
            .unwrap();
            format!("{:?}:{hash:?}", f.event_id)
        })
        .collect()
}
#[test]
fn native_tools_pair_results_and_replay_without_identity_changes() {
    for profile in [
        jsonl_harness::CLAUDE,
        jsonl_harness::PI,
        jsonl_harness::WORKBUDDY,
        jsonl_harness::GROK,
    ] {
        let strategy = jsonl_harness::JsonlHarnessStrategy::new(
            profile,
            b"fixture-secret".to_vec(),
            "unused",
            common::SkillBook::new(),
            Arc::new(|_, _| 1),
        );
        let mut rows = Vec::new();
        for (id, name, args, success) in [
            ("call-a", "Skill", json!({"name":"fixture-skill"}), true),
            ("call-b", "Skill", json!({"name":"fixture-skill"}), false),
            (
                "call-c",
                "read",
                json!({"path":"/fixture/skill/SKILL.md"}),
                true,
            ),
            (
                "call-d",
                "read",
                json!({"path":"/fixture/skill/SKILL.md"}),
                false,
            ),
        ] {
            match strategy.harness_id() {
                "claude-code" => {
                    rows.push(json!({"type":"assistant","uuid":format!("request-{id}"),"timestamp":1789142400000i64,"message":{"role":"assistant","content":[{"type":"tool_use","id":id,"name":name,"input":args}]}}));
                    rows.push(json!({"type":"user","uuid":format!("result-{id}"),"timestamp":1789142400001i64,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":id,"is_error":!success}]}}));
                }
                "pi" => {
                    rows.push(json!({"type":"message","id":format!("request-{id}"),"timestamp":1789142400000i64,"message":{"role":"assistant","content":[{"type":"toolCall","id":id,"name":name,"arguments":args}]}}));
                    rows.push(json!({"type":"message","id":format!("result-{id}"),"timestamp":1789142400001i64,"message":{"role":"toolResult","toolCallId":id,"isError":!success}}));
                }
                "workbuddy" => {
                    rows.push(json!({"type":"function_call","callId":id,"timestamp":1789142400000i64,"name":name,"arguments":args.to_string()}));
                    rows.push(json!({"type":"function_call_result","callId":id,"timestamp":1789142400001i64,"status":if success{"completed"}else{"failed"}}));
                }
                "grok-build" => {
                    rows.push(json!({"method":"session/update","timestamp":"2026-09-12T00:00:00Z","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":id,"status":"in_progress","_meta":{"x.ai/tool":{"name":name}},"rawInput":args}}}));
                    rows.push(json!({"method":"session/update","timestamp":"2026-09-12T00:00:01Z","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":id,"status":if success{"completed"}else{"failed"}}}}));
                }
                _ => unreachable!(),
            }
        }
        let facts = replay_facts(&strategy, &rows);
        let skills: Vec<_> = facts
            .iter()
            .filter(|f| f.event_type == "skill_invoked")
            .collect();
        assert_eq!(
            skills.len(),
            3,
            "{}: successful read only, both explicit skill outcomes",
            strategy.harness_id()
        );
        assert_eq!(
            skills
                .iter()
                .filter(|f| f.payload_sections["activity"]["success"] == true)
                .count(),
            2
        );
        assert_eq!(
            skills
                .iter()
                .filter(|f| f.payload_sections["activity"]["success"] == false)
                .count(),
            1
        );
        assert_ne!(
            skills[0].fact_key, skills[1].fact_key,
            "same skill, different invocations"
        );
        assert!(skills
            .iter()
            .all(|f| f.payload_sections["activity"].get("duration_ms").is_none()));
        assert_eq!(
            wire_signature(&strategy, &rows),
            wire_signature(&strategy, &rows)
        );
    }
}
#[test]
fn cursor_api_leaves_native_activity_available() {
    let strategy = cursor::CursorStrategy::new(
        b"fixture-secret".to_vec(),
        "unused",
        common::SkillBook::new(),
        Arc::new(|_, _| 1),
    )
    .with_usage(cursor_usage::CursorUsagePaths {
        chats: "unused".into(),
        database: "unused".into(),
        auth: "unused".into(),
    });
    let rows = vec![
        json!({"type":"user","message":{"role":"user","content":"fixture"}}),
        json!({"type":"assistant","message":{"role":"assistant","content":"fixture"}}),
        json!({"type":"turn_ended"}),
    ];
    let facts = replay_facts(&strategy, &rows);
    for kind in ["session_started", "turn_started", "turn_completed"] {
        assert_eq!(facts.iter().filter(|f| f.event_type == kind).count(), 1);
    }
    assert!(!facts.iter().any(|f| f.event_type == "model_usage_recorded"));
    assert!(facts
        .iter()
        .find(|f| f.event_type == "turn_completed")
        .unwrap()
        .payload_sections["activity"]
        .get("success")
        .is_none());
    assert_eq!(
        wire_signature(&strategy, &rows),
        wire_signature(&strategy, &rows)
    );
}

#[test]
fn partial_usage_does_not_fabricate_total_or_missing_components() {
    let strategy = jsonl_harness::JsonlHarnessStrategy::new(
        jsonl_harness::CLAUDE,
        b"fixture-secret".to_vec(),
        "unused",
        common::SkillBook::new(),
        Arc::new(|_, _| 1),
    );
    let rows = vec![
        json!({"type":"assistant","uuid":"partial","timestamp":1789142400000i64,"message":{"role":"assistant","usage":{"input_tokens":8}}}),
    ];
    let facts = replay_facts(&strategy, &rows);
    let fact = facts
        .iter()
        .find(|f| f.event_type == "model_usage_recorded")
        .unwrap();
    assert_eq!(fact.payload_sections["usage"]["input_context_tokens"], 8);
    assert!(fact.payload_sections["usage"].get("token_total").is_none());
    assert!(fact.payload_sections["usage"]
        .get("output_tokens")
        .is_none());
    assert_eq!(
        wire_signature(&strategy, &rows),
        wire_signature(&strategy, &rows)
    );
}
