//! Usage and turn lifecycle from Antigravity desktop and CLI's generated conversation transcripts.

use std::fs;
use std::path::{Path, PathBuf};
use std::time::UNIX_EPOCH;

use serde_json::{json, Value};

use super::common::{
    emit_activity_fact, emit_usage_fact, json_obj, parse_json_record, parse_timestamp,
    UsageFactArgs,
};
use super::identity::{source_key, TypedNativeKey};
use super::jsonl_io::read_jsonl_source;
use crate::local_store::pipeline::runner::*;
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

pub const HARNESS_ID: &str = "antigravity";
pub const STREAM_KEY: &str = "antigravity-transcript";
const TRANSCRIPT: &str = ".system_generated/logs/transcript.jsonl";

pub struct AntigravityStrategy {
    secret: Vec<u8>,
    roots: Vec<PathBuf>,
    model_allocator: Option<ModelAllocator>,
}

impl AntigravityStrategy {
    pub fn new(secret: Vec<u8>, roots: Vec<PathBuf>) -> Self {
        Self {
            secret,
            roots,
            model_allocator: None,
        }
    }

    fn transcripts(&self) -> impl Iterator<Item = PathBuf> + '_ {
        self.roots
            .iter()
            .filter_map(|root| fs::read_dir(root).ok())
            .flatten()
            .filter_map(Result::ok)
            .filter(|entry| entry.file_type().is_ok_and(|ty| ty.is_dir()))
            .filter_map(|entry| {
                let path = entry.path().join(TRANSCRIPT);
                (conversation_id(&path).is_some() && path.is_file()).then_some(path)
            })
    }
}

pub fn brain_roots(home: &Path) -> Vec<PathBuf> {
    ["antigravity", "antigravity-cli"]
        .map(|client| home.join(".gemini").join(client).join("brain"))
        .to_vec()
}

fn conversation_id(path: &Path) -> Option<&str> {
    let id = path.parent()?.parent()?.parent()?.file_name()?.to_str()?;
    uuid::Uuid::parse_str(id).ok()?;
    Some(id)
}

fn snapshot_stamp(path: &Path) -> Value {
    fs::metadata(path)
        .ok()
        .map(|meta| {
            let modified = meta
                .modified()
                .ok()
                .and_then(|time| time.duration_since(UNIX_EPOCH).ok());
            json!([
                meta.len(),
                modified.map(|duration| duration.as_nanos().to_string())
            ])
        })
        .unwrap_or_default()
}

impl HarnessStrategy for AntigravityStrategy {
    fn harness_id(&self) -> &str {
        HARNESS_ID
    }

    fn set_model_allocator(&mut self, allocator: ModelAllocator) {
        self.model_allocator = Some(allocator);
    }

    fn collection_status(&self) -> Option<&'static str> {
        if self.transcripts().next().is_some() {
            Some("ACTIVE")
        } else if self.roots.iter().any(|root| root.is_dir()) {
            Some("DETECTED")
        } else {
            Some("UNDETECTED")
        }
    }

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        let mut paths: Vec<String> = self
            .transcripts()
            .filter_map(|path| path.into_os_string().into_string().ok())
            .collect();
        paths.sort_unstable();
        paths.dedup();
        if let Some(after) = budget.resume_after {
            let start = paths.partition_point(|path| path <= &after);
            if start < paths.len() {
                paths.rotate_left(start);
            }
        }
        Ok(paths
            .into_iter()
            .take(budget.max_sources)
            .map(|path| SourceSpec {
                harness_id: HARNESS_ID.into(),
                source_key: source_key(&self.secret, HARNESS_ID, &path),
                source_kind: SourceKind::Jsonl,
                locator_ref: path,
                stream_key: STREAM_KEY.into(),
                cursor_kind: CursorKind::ByteOffset,
                initial_cursor_json: json!({"offset": 0}),
                initial_decoder_state_json: json!({}),
                observed_boundary_json: json!({}),
            })
            .collect())
    }

    fn read(
        &self,
        locator: &str,
        _: &str,
        committed: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError> {
        let path = Path::new(locator);
        // Generated transcripts are snapshots. Their offsets are valid for one file version.
        // A rewrite may grow the file or update rows in place. Stable step IDs make replay safe.
        let stamp = snapshot_stamp(path);
        let mut checkpoint = committed.clone();
        if checkpoint.observed_boundary_json["snapshot"] != stamp {
            checkpoint.cursor_json = json!({"offset": 0});
            checkpoint.observed_boundary_json = json!({});
        }
        let mut batch = read_jsonl_source(path, &checkpoint, budget)?;
        batch.next_observed_boundary_json["snapshot"] = stamp;
        Ok(batch)
    }

    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
        locator: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        let value = match parse_json_record(record) {
            Ok(value) => value,
            Err(outcome) => return Ok(outcome),
        };
        let Some(fields) = json_obj(&value) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let Some(session) = conversation_id(Path::new(locator)) else {
            return Ok(DecodeOutcome::ContextOnly);
        };
        if value["type"] == "USER_INPUT" {
            return self.decode_user_input(&value, fields, state, locator, session);
        }
        if value["type"] != "PLANNER_RESPONSE" || value["status"] != "DONE" {
            return Ok(DecodeOutcome::ContextOnly);
        }
        let (Some(input), Some(output)) = (
            value["input_tokens"].as_u64(),
            value["output_tokens"].as_u64(),
        ) else {
            return Ok(DecodeOutcome::ContextOnly);
        };
        let cache = match fields.get("cache_read_tokens") {
            Some(raw) if raw.as_u64().is_none() => {
                return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord))
            }
            value => value.and_then(|raw| raw.as_u64()),
        };
        let Some(context) = input.checked_add(cache.unwrap_or(0)) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let Some(total) = context
            .checked_add(output)
            .filter(|total| *total <= i64::MAX as u64)
        else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        if total == 0 {
            return Ok(DecodeOutcome::ContextOnly);
        }
        let (Some(step), Some(Ok(at))) = (
            value["step_index"].as_i64().filter(|step| *step >= 0),
            parse_timestamp(fields.get("created_at")),
        ) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let mut fact = emit_usage_fact(UsageFactArgs {
            secret: &self.secret,
            harness: HARNESS_ID,
            scope: session,
            native: TypedNativeKey::I64(step),
            fact_kind: "model_usage_recorded",
            occurred_at: at,
            time_source: TimeSource::SourceRecord,
            token_total: total,
            input_tokens: context,
            output_tokens: output,
            accuracy: TokenAccuracy::Exact,
            session_id: Some(session),
            turn_id: None,
            skill_id: None,
            skill_key: None,
            model_key: 0,
            cache_read_tokens: cache,
            reasoning_tokens: None,
        });
        if let Some(model) = value["model"].as_str().filter(|model| {
            !model.is_empty()
                && model.len() <= 128
                && model.bytes().all(|byte| {
                    byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b':')
                })
        }) {
            if let Some(allocate) = &self.model_allocator {
                fact.model_key = allocate(HARNESS_ID, model)?;
            }
            fact.model_identity = Some((HARNESS_ID.into(), model.into()));
        }
        Ok(DecodeOutcome::Emit(vec![fact]))
    }

    fn native_identity(&self, _: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}

impl AntigravityStrategy {
    /// Each DONE user input starts a turn; the next user input is the only
    /// end-of-turn evidence the transcript carries, so the previous open turn
    /// completes then and a conversation's final turn stays open. Facts are
    /// keyed by step, so snapshot replays dedup; the decoder's open-turn state
    /// is dropped whenever the transcript is rewritten to keep replays faithful.
    fn decode_user_input(
        &self,
        value: &Value,
        fields: &serde_json::Map<String, Value>,
        state: &mut DecoderState,
        locator: &str,
        session: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        if value["status"] != "DONE" {
            return Ok(DecodeOutcome::ContextOnly);
        }
        let (Some(step), Some(Ok(at))) = (
            value["step_index"].as_i64().filter(|step| *step >= 0),
            parse_timestamp(fields.get("created_at")),
        ) else {
            return Ok(DecodeOutcome::ContextOnly);
        };
        let stamp = snapshot_stamp(Path::new(locator));
        if state.json["antigravity_snapshot"] != stamp {
            state.json["antigravity_snapshot"] = stamp;
            state.json["antigravity_open_turn"] = json!(null);
        }
        let mut facts = Vec::new();
        if state.json["antigravity_session_started"].as_bool() != Some(true) {
            facts.push(emit_activity_fact(
                &self.secret,
                HARNESS_ID,
                session,
                TypedNativeKey::Str(format!("session:{session}")),
                "session_started",
                at,
                TimeSource::SourceRecord,
                session,
                None,
                json!({}),
            ));
            state.json["antigravity_session_started"] = json!(true);
        }
        if let Some(previous) = state.json["antigravity_open_turn"].as_i64() {
            facts.push(emit_activity_fact(
                &self.secret,
                HARNESS_ID,
                session,
                TypedNativeKey::I64(previous),
                "turn_completed",
                at,
                TimeSource::SourceRecord,
                session,
                Some(&previous.to_string()),
                json!({"success": true}),
            ));
        }
        let trigger = if value["source"] == "USER_EXPLICIT" {
            "user"
        } else {
            "system"
        };
        facts.push(emit_activity_fact(
            &self.secret,
            HARNESS_ID,
            session,
            TypedNativeKey::I64(step),
            "turn_started",
            at,
            TimeSource::SourceRecord,
            session,
            Some(&step.to_string()),
            json!({"trigger": trigger}),
        ));
        state.json["antigravity_open_turn"] = json!(step);
        Ok(DecodeOutcome::Emit(facts))
    }
}
