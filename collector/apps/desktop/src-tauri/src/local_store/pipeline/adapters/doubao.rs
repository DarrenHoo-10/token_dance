//! Doubao desktop activity from Chromium caches. No Token estimates or context-window totals.
use super::{
    common::{self, SkillBook},
    doubao_storage,
    identity::{source_key, TypedNativeKey},
    jsonl_harness::{JsonlHarnessStrategy, DOUBAO},
};
use crate::local_store::pipeline::{
    runner::*,
    types::{CursorKind, SourceKind},
};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::{
    collections::{BTreeMap, HashMap},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::UNIX_EPOCH,
};
const ID: &str = "doubao-work";
const STREAM: &str = "desktop-activity";
#[derive(Clone)]
struct CachedFile {
    stamp: (u64, u128),
    rows: Vec<Value>,
}
#[derive(Default)]
struct Cache {
    files: HashMap<PathBuf, CachedFile>,
    snapshots: HashMap<String, (String, Vec<Value>)>,
}
pub struct DoubaoStrategy {
    legacy: JsonlHarnessStrategy,
    cache: Mutex<Cache>,
}
impl DoubaoStrategy {
    pub fn new(
        secret: Vec<u8>,
        root: PathBuf,
        book: SkillBook,
        allocate: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        Self {
            legacy: JsonlHarnessStrategy::new(DOUBAO, secret, root, book, allocate),
            cache: Mutex::new(Cache::default()),
        }
    }
    fn profiles(&self) -> Vec<PathBuf> {
        collector_service::detect::doubao_activity_profiles(&self.legacy.root)
    }
}
fn string(v: &Value) -> Option<String> {
    v.as_str()
        .map(str::to_owned)
        .or_else(|| v.as_i64().map(|n| n.to_string()))
}
fn object(v: &Value) -> Value {
    if let Some(s) = v.as_str() {
        serde_json::from_str(s).unwrap_or(Value::Null)
    } else {
        v.clone()
    }
}
fn collect(v: &Value, out: &mut Vec<Value>, depth: usize) {
    if depth > 100 {
        return;
    }
    match v {
        Value::Object(map) => {
            let ext = object(&v["ext"]);
            let params = object(&ext["general_task_param"]);
            let mode = params["runtime_type"].as_i64();
            if matches!(mode, Some(1 | 2)) {
                let raw_time = v.get("create_time").cloned().unwrap_or(Value::Null);
                let raw_time = if let Some(n) = raw_time
                    .as_f64()
                    .filter(|n| n.is_finite() && n.fract() == 0.0 && n.abs() < 9e15)
                {
                    json!(n as i64)
                } else {
                    raw_time
                };
                if let (Some(id), Some(session), Some(Ok(at)), Some(role)) = (
                    string(&v["message_id"]),
                    string(&v["conversation_id"]),
                    common::parse_timestamp(Some(&raw_time)),
                    v["user_type"].as_i64(),
                ) {
                    let skills = object(&ext["moa_skill_usage"])
                        .as_array()
                        .map(|a| {
                            a.iter()
                                .filter_map(|s| {
                                    s["name"]
                                        .as_str()
                                        .filter(|s| !s.is_empty())
                                        .map(str::to_owned)
                                })
                                .collect::<Vec<_>>()
                        })
                        .unwrap_or_default();
                    out.push(json!({"id":id,"session":session,"at":at,"role":role,"index":string(&v["index"]).and_then(|s|s.parse::<i64>().ok()),"turn":if role==1{Some(id.clone())}else{string(&v["reply_id"]).filter(|s|!s.is_empty()&&s!="0")},"finished":ext["is_finish"]=="1"||ext["is_finish"]==true,"skills":skills}));
                }
            }
            for (k, value) in map {
                if matches!(k.as_str(), "content" | "downlink_body") && value.is_string() {
                    let parsed = object(value);
                    if parsed.is_object() || parsed.is_array() {
                        collect(&parsed, out, depth + 1)
                    }
                } else {
                    collect(value, out, depth + 1)
                }
            }
        }
        Value::Array(values) => {
            for value in values {
                collect(value, out, depth + 1)
            }
        }
        _ => {}
    }
}
fn merge(rows: &mut BTreeMap<String, Value>, v: Value) {
    let key = format!(
        "{}:{}",
        v["session"].as_str().unwrap_or(""),
        v["id"].as_str().unwrap_or("")
    );
    if let Some(old) = rows.get_mut(&key) {
        if v["finished"] == true {
            old["finished"] = json!(true)
        }
        if old["turn"].is_null() && v["turn"].is_string() {
            old["turn"] = v["turn"].clone();
        }
        let mut skills = old["skills"].as_array().cloned().unwrap_or_default();
        for s in v["skills"].as_array().into_iter().flatten() {
            if !skills.contains(s) {
                skills.push(s.clone())
            }
        }
        skills.sort_by_key(Value::to_string);
        old["skills"] = json!(skills);
    } else {
        rows.insert(key, v);
    }
}
impl HarnessStrategy for DoubaoStrategy {
    fn harness_id(&self) -> &str {
        ID
    }
    fn collection_status(&self) -> Option<&'static str> {
        if !self.profiles().is_empty() {
            Some("ACTIVE")
        } else {
            None
        }
    }
    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        let paths = self.profiles();
        if paths.is_empty() {
            return self.legacy.discover(budget);
        }
        let mut specs = paths
            .into_iter()
            .map(|p| {
                let scope = p.to_string_lossy().into_owned();
                SourceSpec {
                    harness_id: ID.into(),
                    source_key: source_key(&self.legacy.identity_secret, ID, &scope),
                    source_kind: SourceKind::Other,
                    locator_ref: scope,
                    stream_key: STREAM.into(),
                    cursor_kind: CursorKind::Opaque,
                    initial_cursor_json: json!({}),
                    initial_decoder_state_json: json!({}),
                    observed_boundary_json: json!({}),
                }
            })
            .collect::<Vec<_>>();
        if let Some(after) = budget.resume_after {
            let pos = specs
                .iter()
                .position(|s| s.locator_ref > after)
                .unwrap_or(0);
            specs.rotate_left(pos);
        }
        specs.truncate(budget.max_sources);
        Ok(specs)
    }
    fn read(
        &self,
        locator: &str,
        stream: &str,
        cp: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError> {
        if stream != STREAM {
            return self.legacy.read(locator, stream, cp, budget);
        }
        if budget.max_records == 0 || budget.max_bytes == 0 {
            return Err(RunnerError::Budget);
        }
        let mut cache = self
            .cache
            .lock()
            .map_err(|_| RunnerError::Io("cache lock".into()))?;
        let mut files = Vec::new();
        for db in collector_service::detect::DOUBAO_ACTIVITY_DATABASES {
            let dir = Path::new(locator).join("IndexedDB").join(db);
            if let Ok(entries) = std::fs::read_dir(dir) {
                for e in entries.flatten() {
                    let p = e.path();
                    if p.extension()
                        .is_some_and(|s| s == "ldb" || s == "log" || s == "sst")
                    {
                        let m = p
                            .metadata()
                            .map_err(|_| RunnerError::Io("cache metadata".into()))?;
                        let stamp = (
                            m.len(),
                            m.modified()
                                .ok()
                                .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
                                .map(|d| d.as_nanos())
                                .unwrap_or(0),
                        );
                        files.push((p, stamp));
                    }
                }
            }
        }
        files.sort_by(|a, b| a.0.cmp(&b.0));
        let mut hash = Sha256::new();
        for (p, stamp) in &files {
            hash.update(p.to_string_lossy().as_bytes());
            hash.update(stamp.0.to_le_bytes());
            hash.update(stamp.1.to_le_bytes());
        }
        let fingerprint = format!("{:x}", hash.finalize());
        let continuing = cp.cursor_json["done"] != true
            && cache
                .snapshots
                .get(locator)
                .is_some_and(|(f, _)| cp.cursor_json["fingerprint"].as_str() == Some(f));
        if !continuing
            && cache
                .snapshots
                .get(locator)
                .is_none_or(|(f, _)| f != &fingerprint)
        {
            if files.len() > 512 {
                return Err(RunnerError::DecodeBlocked(
                    "too many Doubao cache files".into(),
                ));
            }
            if files.iter().map(|(_, s)| s.0).sum::<u64>() > 128 * 1024 * 1024 {
                return Err(RunnerError::DecodeBlocked("Doubao cache size limit".into()));
            }
            let mut rows = BTreeMap::new();
            // These are historical message observations, not a database restore. Cache-key
            // eviction must not retract previously observed activity; facts dedupe by message.
            for (path, stamp) in &files {
                if cache.files.get(path).is_none_or(|c| c.stamp != *stamp) {
                    let entries =
                        doubao_storage::read_file(path).map_err(RunnerError::DecodeBlocked)?;
                    let mut normalized = BTreeMap::new();
                    for (_, _, deleted, value) in entries {
                        if deleted {
                            continue;
                        }
                        for object in doubao_storage::objects(&value) {
                            let mut found = Vec::new();
                            collect(&object, &mut found, 0);
                            for row in found {
                                merge(&mut normalized, row);
                            }
                        }
                    }
                    cache.files.insert(
                        path.clone(),
                        CachedFile {
                            stamp: *stamp,
                            rows: normalized.into_values().collect(),
                        },
                    );
                }
                for row in &cache.files[path].rows {
                    merge(&mut rows, row.clone());
                }
            }
            cache
                .files
                .retain(|p, _| !p.starts_with(locator) || files.iter().any(|(path, _)| path == p));
            cache
                .snapshots
                .insert(locator.into(), (fingerprint, rows.into_values().collect()));
        }
        let (fingerprint, rows) = cache
            .snapshots
            .get(locator)
            .ok_or_else(|| RunnerError::Io("missing snapshot".into()))?;
        let start = if cp.cursor_json["fingerprint"].as_str() == Some(fingerprint) {
            cp.cursor_json["offset"].as_u64().unwrap_or(0) as usize
        } else {
            0
        };
        let mut records = Vec::new();
        let mut bytes = 0;
        let mut end = start;
        for (i, row) in rows.iter().enumerate().skip(start) {
            let payload = serde_json::to_vec(row)
                .map_err(|_| RunnerError::DecodeBlocked("activity JSON".into()))?;
            if !records.is_empty()
                && (records.len() >= budget.max_records || bytes + payload.len() > budget.max_bytes)
            {
                break;
            }
            bytes += payload.len();
            records.push(RawRecord {
                ordinal: i as u64,
                byte_start: None,
                byte_end: None,
                native_rowid: None,
                payload,
                file_mtime_ms: None,
            });
            end = i + 1;
        }
        let more = end < rows.len();
        Ok(RawBatch {
            records,
            next_cursor_json: json!({"fingerprint":fingerprint,"offset":end,"done":!more}),
            next_observed_boundary_json: json!({"records":rows.len()}),
            has_more: more,
            bytes_read: bytes,
            ignored_incomplete_tail: false,
        })
    }
    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
        scope: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        let value: Value = serde_json::from_slice(&record.payload)
            .map_err(|_| RunnerError::DecodeBlocked("activity JSON".into()))?;
        if value.get("session").is_none() || value.get("at").is_none() {
            return self.legacy.decode(record, state, scope);
        }
        let id = value["id"].as_str().unwrap_or("");
        let session = value["session"].as_str().unwrap_or("");
        let at = value["at"]
            .as_i64()
            .ok_or_else(|| RunnerError::DecodeBlocked("activity timestamp".into()))?;
        let native = format!("{session}:{id}");
        let mut out = Vec::new();
        let mut emit = |kind: &str, payload: Value| {
            out.push(common::emit_activity_fact(
                &self.legacy.identity_secret,
                ID,
                "desktop-messages",
                TypedNativeKey::Str(native.clone()),
                kind,
                at,
                TimeSource::SourceRecord,
                session,
                value["turn"].as_str().or(Some(id)),
                payload,
            ))
        };
        if value["role"] == 1 {
            emit("turn_started", json!({"trigger":"user"}));
            if value["index"] == 1 {
                emit("session_started", json!({}));
            }
        }
        if value["role"] == 2 && value["finished"] == true && value["turn"].is_string() {
            emit("turn_completed", json!({}));
        }
        let mut names = value["skills"]
            .as_array()
            .into_iter()
            .flatten()
            .filter_map(Value::as_str)
            .collect::<Vec<_>>();
        names.sort();
        names.dedup();
        for name in names {
            let allocator = |key, name: &str| (self.legacy.skill_allocator)(key, name);
            let mut fact = common::emit_skill_fact(
                &self.legacy.identity_secret,
                ID,
                "desktop-messages",
                TypedNativeKey::Str(format!("{native}:skill:{name}")),
                at,
                TimeSource::SourceRecord,
                name,
                &self.legacy.skill_book,
                &allocator,
                Some(session),
            );
            fact.accuracy = TokenAccuracy::Correlated;
            out.push(fact);
        }
        Ok(if out.is_empty() {
            DecodeOutcome::ContextOnly
        } else {
            DecodeOutcome::Emit(out)
        })
    }
    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn reads_macos_and_windows_cache_layouts_without_duplicate_activity() {
        for (layout, database) in [
            ("DoubaoWork", "chrome_doubaowork-chat_0.indexeddb.leveldb"),
            (
                "DoubaoWork",
                "chrome_doubaowork-launcher_0.indexeddb.leveldb",
            ),
            ("Doubao/User Data", "chrome_doubao-chat_0.indexeddb.leveldb"),
            (
                "Doubao/User Data",
                "chrome_doubao-launcher_0.indexeddb.leveldb",
            ),
        ] {
            let dir = tempfile::tempdir().unwrap();
            let root = dir.path().join(layout);
            let cache = root.join("Default/IndexedDB").join(database);
            std::fs::create_dir_all(&cache).unwrap();
            // Synthetic LevelDB WAL: one V8-wrapped work message with a Skill.
            let fixture = include_bytes!("fixtures/doubao-activity.bin");
            for name in ["000001.log", "000002.log"] {
                std::fs::write(cache.join(name), fixture).unwrap();
            }
            let configured = if layout.ends_with("User Data") {
                root.parent().unwrap()
            } else {
                &root
            };
            let strategy = DoubaoStrategy::new(
                vec![1; 32],
                configured.into(),
                SkillBook::new(),
                Arc::new(|_, _| 1),
            );
            let sources = strategy.discover(DiscoveryBudget::default()).unwrap();
            assert_eq!(sources.len(), 1, "{layout} {database}");
            assert_eq!(strategy.collection_status(), Some("ACTIVE"));
            let source = &sources[0];
            let mut cp = CheckpointView {
                cursor_json: json!({}),
                decoder_state_version: 1,
                decoder_state_json: json!({}),
                observed_boundary_json: json!({}),
                commit_seq: 0,
            };
            let batch = strategy
                .read(
                    &source.locator_ref,
                    &source.stream_key,
                    &cp,
                    ReadBudget::new(100, 1048576, 1000),
                )
                .unwrap();
            assert_eq!(
                batch.records.len(),
                1,
                "duplicate file must not duplicate the message"
            );
            let DecodeOutcome::Emit(facts) = strategy
                .decode(
                    &batch.records[0],
                    &mut DecoderState::default(),
                    &source.locator_ref,
                )
                .unwrap()
            else {
                panic!("missing activity")
            };
            assert!(facts.iter().any(|f| f.event_type == "skill_invoked"));
            assert!(facts.iter().any(|f| f.event_type == "turn_started"));
            assert!(!facts.iter().any(|f| f.event_type == "model_usage_recorded"));
            cp.cursor_json = batch.next_cursor_json;
            assert!(strategy
                .read(
                    &source.locator_ref,
                    &source.stream_key,
                    &cp,
                    ReadBudget::new(100, 1048576, 1000)
                )
                .unwrap()
                .records
                .is_empty());
        }
    }

    #[test]
    fn emits_only_evidenced_activity_and_skill() {
        let s = DoubaoStrategy::new(
            vec![1; 32],
            PathBuf::new(),
            SkillBook::new(),
            Arc::new(|_, _| 1),
        );
        let r=RawRecord{ordinal:0,byte_start:None,byte_end:None,native_rowid:None,file_mtime_ms:None,payload:serde_json::to_vec(&json!({"id":"m","session":"c","at":1789257600000i64,"role":1,"index":1,"finished":true,"skills":["example","example"]})).unwrap()};
        let mut state = DecoderState::default();
        let DecodeOutcome::Emit(f) = s.decode(&r, &mut state, "profile-a").unwrap() else {
            panic!()
        };
        assert_eq!(f.len(), 3);
        assert!(!f.iter().any(|f| f.event_type == "model_usage_recorded"));
        let DecodeOutcome::Emit(g) = s.decode(&r, &mut state, "profile-b").unwrap() else {
            panic!()
        };
        assert_eq!(
            f.iter().map(|f| f.event_id).collect::<Vec<_>>(),
            g.iter().map(|f| f.event_id).collect::<Vec<_>>()
        );
        for f in f {
            let mut p = f.payload_sections.clone();
            p["meta"] =
                json!({"accuracy":f.accuracy.as_str(),"time_source":f.time_source.as_str()});
            crate::local_store::pipeline::content_hash::compute_p0_content_hash(ID, &f, &p)
                .unwrap();
        }
    }
    #[test]
    fn native_snapshots_pair_turns_and_keep_partial_tokens_unknown() {
        let ext = json!({"general_task_param":"{\"runtime_type\":2}","is_finish":"1","context_window_usage":"{\"messages\":12345}","input_skill":["selected-only"]});
        let mut found = Vec::new();
        collect(
            &json!([
            {"message_id":"u","conversation_id":"c","create_time":1789257600000f64,"user_type":1,"index":"1","ext":ext},
            {"message_id":"a","reply_id":"u","conversation_id":"c","create_time":1789257600100i64,"user_type":2,"index":2,"ext":ext},
            {"message_id":"ordinary","conversation_id":"c","create_time":1789257600000i64,"user_type":1,"ext":{}}
            ]),
            &mut found,
            0,
        );
        assert_eq!(found.len(), 2);
        assert_eq!(found[0]["turn"], found[1]["turn"]);
        assert_eq!(found[0]["index"], 1);
        assert!(found[0]["skills"].as_array().unwrap().is_empty());
        let mut rows = BTreeMap::new();
        for row in found.clone() {
            merge(&mut rows, row.clone());
            merge(&mut rows, row);
        }
        assert_eq!(rows.len(), 2);
        let s = DoubaoStrategy::new(
            vec![1; 32],
            PathBuf::new(),
            SkillBook::new(),
            Arc::new(|_, _| 1),
        );
        let mut keys = Vec::new();
        for value in found {
            let r = RawRecord {
                ordinal: 0,
                byte_start: None,
                byte_end: None,
                native_rowid: None,
                file_mtime_ms: None,
                payload: serde_json::to_vec(&value).unwrap(),
            };
            if let DecodeOutcome::Emit(facts) =
                s.decode(&r, &mut DecoderState::default(), "p").unwrap()
            {
                for f in facts {
                    assert!(!matches!(
                        f.event_type.as_str(),
                        "model_usage_recorded" | "skill_invoked"
                    ));
                    if f.event_type.starts_with("turn_") {
                        keys.push(f.turn_key);
                    }
                }
            }
        }
        assert_eq!(keys.len(), 2);
        assert_eq!(keys[0], keys[1]);
    }
    #[test]
    #[ignore = "explicit local read-only validation"]
    fn local_cache() {
        let root = std::env::var("TOKENDANCE_DOUBAO_AUDIT_ROOT").unwrap();
        let s = DoubaoStrategy::new(
            vec![1; 32],
            root.into(),
            SkillBook::new(),
            Arc::new(|_, _| 1),
        );
        let sources = s.discover(DiscoveryBudget::default()).unwrap();
        assert!(!sources.is_empty());
        let mut kinds = BTreeMap::new();
        for source in sources {
            let mut cp = CheckpointView {
                cursor_json: json!({}),
                decoder_state_version: 1,
                decoder_state_json: json!({}),
                observed_boundary_json: json!({}),
                commit_seq: 0,
            };
            let mut state = DecoderState::default();
            loop {
                let batch = s
                    .read(
                        &source.locator_ref,
                        &source.stream_key,
                        &cp,
                        ReadBudget::new(2, 1048576, 50),
                    )
                    .unwrap();
                for r in &batch.records {
                    if let DecodeOutcome::Emit(facts) =
                        s.decode(r, &mut state, &source.locator_ref).unwrap()
                    {
                        for f in facts {
                            *kinds.entry(f.event_type).or_insert(0) += 1;
                        }
                    }
                }
                cp.cursor_json = batch.next_cursor_json;
                if !batch.has_more {
                    assert!(s
                        .read(
                            &source.locator_ref,
                            &source.stream_key,
                            &cp,
                            ReadBudget::new(2, 1048576, 50)
                        )
                        .unwrap()
                        .records
                        .is_empty());
                    break;
                }
            }
        }
        println!("ACTIVITY_COUNTS {kinds:?}");
        assert!(!kinds.is_empty());
        assert!(!kinds.contains_key("model_usage_recorded"));
    }
}
