//! Native event shapes. Queueing, admission, persistence and upload stay in the runner.
use super::common::{self, SkillBook, UsageFactArgs};
use super::identity::TypedNativeKey;
use crate::local_store::pipeline::runner::{
    DecoderState, FactDraft, RawRecord, TimeSource, TokenAccuracy,
};
use serde_json::{json, Value};

pub struct Context<'a> {
    pub harness: &'a str,
    pub secret: &'a [u8],
    pub scope: &'a str,
    pub now: i64,
    pub time_source: TimeSource,
    pub record: &'a RawRecord,
    pub book: &'a SkillBook,
    pub allocator: &'a dyn Fn([u8; 32], &str) -> i64,
}
fn text(v: &Value, keys: &[&str]) -> Option<String> {
    keys.iter().find_map(|k| {
        v.get(*k)
            .and_then(Value::as_str)
            .filter(|s| !s.is_empty())
            .map(str::to_owned)
    })
}
fn number(v: &Value, keys: &[&str]) -> Option<u64> {
    keys.iter().find_map(|k| {
        v.get(*k)
            .and_then(|n| n.as_u64().or_else(|| n.as_str()?.parse().ok()))
    })
}
fn native_id(v: &Value, c: &Context) -> String {
    text(
        v,
        &[
            "uuid",
            "id",
            "messageId",
            "stepId",
            "turnId",
            "turn_id",
            "invocationId",
            "invocation_id",
        ],
    )
    .unwrap_or_else(|| format!("offset:{}", c.record.byte_start.unwrap_or(c.record.ordinal)))
}
fn event(
    c: &Context,
    kind: &str,
    id: &str,
    session: &str,
    turn: Option<&str>,
    activity: Value,
) -> FactDraft {
    common::emit_activity_fact(
        c.secret,
        c.harness,
        c.scope,
        TypedNativeKey::Str(id.into()),
        kind,
        c.now,
        c.time_source,
        session,
        turn,
        activity,
    )
}
fn usage(
    c: &Context,
    id: &str,
    session: &str,
    turn: Option<&str>,
    u: &Value,
    separate_cache: bool,
    revision: i64,
) -> Option<FactDraft> {
    let input = number(u, &["input_tokens", "inputTokens", "input"]);
    let output = number(u, &["output_tokens", "outputTokens", "output"]);
    let read = number(
        u,
        &[
            "cache_read_input_tokens",
            "cache_read_tokens",
            "cacheReadTokens",
            "cacheRead",
        ],
    );
    let write = number(
        u,
        &[
            "cache_creation_input_tokens",
            "cache_write_tokens",
            "cacheWriteTokens",
            "cacheWrite",
        ],
    );
    let total = number(u, &["total_tokens", "totalTokens", "total"]);
    if input.is_none() && output.is_none() && total.is_none() {
        return None;
    }
    let input_context = if separate_cache {
        input
            .unwrap_or(0)
            .checked_add(read.unwrap_or(0))?
            .checked_add(write.unwrap_or(0))?
    } else {
        input.unwrap_or(0)
    };
    let total = total.or_else(|| {
        // A partial response is not evidence that the missing component is zero.
        input
            .and(output)
            .and_then(|output| input_context.checked_add(output))
    });
    let mut fact = common::emit_usage_fact(UsageFactArgs {
        secret: c.secret,
        harness: c.harness,
        scope: c.scope,
        native: TypedNativeKey::Str(id.into()),
        fact_kind: "model_usage_recorded",
        occurred_at: c.now,
        time_source: c.time_source,
        token_total: total.unwrap_or(0),
        input_tokens: input_context,
        output_tokens: output.unwrap_or(0),
        accuracy: TokenAccuracy::Exact,
        session_id: Some(session),
        turn_id: turn,
        skill_id: None,
        skill_key: None,
        model_key: 0,
        cache_read_tokens: read,
        reasoning_tokens: number(u, &["reasoning_tokens", "reasoningTokens", "reasoning"]),
    });
    if total.is_none() {
        fact.payload_sections["usage"]
            .as_object_mut()?
            .remove("token_total");
    }
    if input.is_none() {
        fact.payload_sections["usage"]
            .as_object_mut()?
            .remove("input_context_tokens");
    }
    if output.is_none() {
        fact.payload_sections["usage"]
            .as_object_mut()?
            .remove("output_tokens");
    }
    if let Some(v) = write {
        fact.payload_sections["usage"]["cache_write_tokens"] = json!(v);
    }
    fact.fact_revision = revision;
    fact.event_id = super::identity::event_id(c.secret, &fact.fact_key, revision);
    Some(fact)
}
fn skill(c: &Context, id: &str, name: &str, session: &str, success: bool) -> FactDraft {
    // Preserve the observed name; display metadata is separate from event identity.
    let alloc = |key, name: &str| (c.allocator)(key, name);
    let mut f = common::emit_skill_fact(
        c.secret,
        c.harness,
        c.scope,
        TypedNativeKey::Str(id.into()),
        c.now,
        c.time_source,
        name,
        c.book,
        &alloc,
        Some(session),
    );
    f.payload_sections = json!({"activity":{"success":success}});
    f
}
fn pending_call(state: &mut DecoderState, id: &str, name: &str, input: &Value) {
    let name = name.to_ascii_lowercase();
    let pending = if name == "skill" {
        text(input, &["skill", "name", "skillName"]).map(|name| json!({"skill":name}))
    } else if matches!(name.as_str(), "read" | "read_file") {
        text(input, &["file_path", "path", "filePath", "target_file"])
            .map(|p| p.replace('\\', "/"))
            .filter(|p| {
                p.rsplit('/')
                    .next()
                    .is_some_and(|s| s.eq_ignore_ascii_case("SKILL.md"))
            })
            .map(|path| json!({"skill":path,"skill_read":true}))
    } else {
        common::completed_code_payload(
            &json!({"tool":name,"state":{"status":"completed","input":input}}),
        )
        .map(|code| json!({"code":code}))
    };
    if let Some(pending) = pending {
        if !state.json["native_pending"].is_object() {
            state.json["native_pending"] = json!({});
        }
        let map = state.json["native_pending"].as_object_mut().unwrap();
        if map.len() < 256 {
            map.insert(id.into(), pending);
        }
    }
}
fn finish_call(
    c: &Context,
    state: &mut DecoderState,
    id: &str,
    session: &str,
    success: bool,
    out: &mut Vec<FactDraft>,
) {
    let pending = state.json["native_pending"]
        .as_object_mut()
        .and_then(|m| m.remove(id));
    let Some(pending) = pending else {
        return;
    };
    if let Some(name) = pending.get("skill").and_then(Value::as_str) {
        if pending["skill_read"] != true || success {
            let mut fact = skill(c, id, name, session, success);
            if pending["skill_read"] == true {
                fact.accuracy = TokenAccuracy::Correlated;
            }
            out.push(fact);
        }
    }
    if success {
        if let Some(code) = pending.get("code") {
            let mut f = common::emit_code_fact(
                c.secret,
                c.harness,
                c.scope,
                TypedNativeKey::Str(id.into()),
                c.now,
                c.time_source,
                Some(session),
                0,
                0,
            );
            f.payload_sections = json!({"code":code});
            out.push(f);
        }
    }
}
/// None means this is a legacy profile record, so keep its existing identity path.
pub fn decode(c: &Context, value: &Value, state: &mut DecoderState) -> Option<Vec<FactDraft>> {
    if let Some(facts) = decode_compat(c, value, state) {
        return Some(facts);
    }
    if c.harness == "deepseek-harness" {
        if let Some(facts) = decode_deepseek(c, value, state) {
            return Some(facts);
        }
    }
    let kind = text(value, &["type"])
        .or_else(|| {
            if c.harness == "cursor" {
                text(value, &["role"])
            } else {
                None
            }
        })
        .unwrap_or_default();
    let message = value
        .get("message")
        .filter(|m| m.is_object())
        .unwrap_or(value);
    let role = text(message, &["role"])
        .or_else(|| text(value, &["role"]))
        .unwrap_or_else(|| kind.clone());
    let is_native = match c.harness {
        "claude-code" => matches!(kind.as_str(), "user" | "assistant" | "system"),
        "cursor" => matches!(kind.as_str(), "user" | "assistant" | "turn_ended"),
        "pi" => matches!(kind.as_str(), "session" | "model_change" | "message"),
        "workbuddy" => matches!(
            kind.as_str(),
            "message" | "reasoning" | "function_call" | "function_call_result" | "session"
        ),
        _ => false,
    };
    if !is_native {
        return None;
    }
    let session = text(value, &["sessionId", "session_id"])
        .or_else(|| state.json["native_session"].as_str().map(str::to_owned))
        .unwrap_or_else(|| c.scope.to_string());
    let session = if kind == "session" {
        text(value, &["id", "sessionId"]).unwrap_or(session)
    } else {
        session
    };
    state.json["native_session"] = json!(session);
    let id = native_id(value, c);
    let mut out = Vec::new();
    if state.json["native_session_emitted"] != true {
        out.push(event(
            c,
            "session_started",
            &format!("session:{session}"),
            &session,
            None,
            json!({}),
        ));
        state.json["native_session_emitted"] = json!(true);
    }
    if kind == "model_change" {
        state.json["native_model"] = json!({"model":value.get("model").or_else(||value.get("modelId")),"provider":value.get("provider")});
        return Some(out);
    }
    if let Some(u) = message.get("usage") {
        // Existing Claude primary facts used top-level uuid. Keep it on correction.
        let revision = if c.harness == "claude-code" { 2 } else { 1 };
        if let Some(f) = usage(
            c,
            &id,
            &session,
            Some(&id),
            u,
            c.harness != "workbuddy",
            revision,
        ) {
            out.push(f);
        }
    }
    let content = message.get("content").or_else(|| value.get("content"));
    let parts = content
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let has_tool_results = parts.iter().any(|p| p["type"] == "tool_result");
    if role == "user" && !has_tool_results {
        state.json["native_turn"] = json!(id);
        state.json["native_turn_start"] = json!(c.now);
        out.push(event(
            c,
            "turn_started",
            &id,
            &session,
            Some(&id),
            json!({"trigger":"user"}),
        ));
    }
    for part in &parts {
        if matches!(part["type"].as_str(), Some("tool_use" | "toolCall")) {
            if let (Some(call), Some(name)) = (text(part, &["id", "callId"]), text(part, &["name"]))
            {
                pending_call(
                    state,
                    &call,
                    &name,
                    part.get("input")
                        .or_else(|| part.get("arguments"))
                        .unwrap_or(&Value::Null),
                );
            }
        }
        if part["type"] == "tool_result" {
            if let Some(call) = text(part, &["tool_use_id"]) {
                finish_call(
                    c,
                    state,
                    &call,
                    &session,
                    part["is_error"] != true,
                    &mut out,
                );
            }
        }
    }
    if role == "toolResult" {
        if let Some(call) = text(message, &["toolCallId"]) {
            finish_call(
                c,
                state,
                &call,
                &session,
                message["isError"] != true,
                &mut out,
            );
        }
    }
    if kind == "function_call" {
        if let (Some(call), Some(name)) = (text(value, &["callId", "id"]), text(value, &["name"])) {
            let args = value.get("arguments").cloned().unwrap_or(Value::Null);
            let input = args
                .as_str()
                .and_then(|s| serde_json::from_str::<Value>(s).ok())
                .unwrap_or(args);
            pending_call(state, &call, &name, &input);
        }
    }
    if kind == "function_call_result" {
        if let Some(call) = text(value, &["callId", "id"]) {
            let success = matches!(value["status"].as_str(), Some("completed" | "success"));
            let failure = matches!(value["status"].as_str(), Some("error" | "failed"));
            if success || failure {
                finish_call(c, state, &call, &session, success, &mut out);
            }
        }
    }
    let terminal = text(message, &["stop_reason", "stopReason"])
        .is_some_and(|s| matches!(s.as_str(), "end_turn" | "stop" | "length"));
    if (role == "assistant" && terminal) || (c.harness == "cursor" && kind == "turn_ended") {
        let turn = state.json["native_turn"]
            .as_str()
            .unwrap_or(&id)
            .to_string();
        // User-to-response wall time includes waits; do not pretend it is active duration.
        out.push(event(
            c,
            "turn_completed",
            &turn,
            &session,
            Some(&turn),
            if c.harness == "cursor" {
                json!({})
            } else {
                json!({"success":true})
            },
        ));
        state.json["native_turn"] = Value::Null;
        state.json["native_turn_start"] = Value::Null;
    }
    Some(out)
}

/// Current DSH session events have a session header followed by seq/time/data envelopes.
/// Consume finalized messages only; streaming usage is another view of the same step.
fn decode_deepseek(c: &Context, value: &Value, state: &mut DecoderState) -> Option<Vec<FactDraft>> {
    let kind = value.get("type")?.as_str()?;
    if kind != "session" && value.get("seq").and_then(Value::as_u64).is_none() {
        return None;
    }
    let data = value.get("data").unwrap_or(&Value::Null);
    let session = state.json["dsh_session"]
        .as_str()
        .unwrap_or(c.scope)
        .to_owned();
    let mut out = Vec::new();
    if kind == "session" {
        let session = text(value, &["id"])?;
        state.json["dsh_session"] = json!(session);
        // Only the durable fork boundary excludes inherited calls. A resume marker
        // also precedes this session's own history and must not erase its usage.
        state.json["dsh_seed_length"] = value.get("seedLength").cloned().unwrap_or(json!(0));
        out.push(event(
            c,
            "session_started",
            &session,
            &session,
            None,
            json!({}),
        ));
        return Some(out);
    }
    let seq = value["seq"].as_u64()?;
    if seq < state.json["dsh_seed_length"].as_u64().unwrap_or(0) {
        return Some(out);
    }
    let turn = data
        .get("turn")
        .and_then(Value::as_u64)
        .map(|v| v.to_string());
    match kind {
        "model/selection" | "request/context" => {
            state.json["native_model"] =
                json!({"model":data.get("model"),"provider":data.get("provider")});
        }
        "assistant/message" => {
            if let (Some(turn), Some(step), Some(u)) = (
                turn.as_deref(),
                data.get("step").and_then(Value::as_u64),
                data.get("usage"),
            ) {
                // Corrections for the same step retain a fact key. seq orders revisions
                // independently of how compressed frames were paged or replayed.
                if let Some(revision) = seq.checked_add(1).and_then(|v| i64::try_from(v).ok()) {
                    if let Some(f) = usage(
                        c,
                        &format!("step:{turn}:{step}"),
                        &session,
                        Some(turn),
                        u,
                        true,
                        revision,
                    ) {
                        out.push(f);
                    }
                }
            }
        }
        "turn/start" => {
            if let Some(turn) = turn.as_deref() {
                state.json["dsh_turn"] = json!(turn);
            }
        }
        "user/message" => {
            // Session turn/start can represent automated turns. Count only actual user messages.
            let source = data.get("source");
            if source.and_then(|v| v.get("kind")).and_then(Value::as_str) == Some("user") {
                let turn = state.json["dsh_turn"]
                    .as_str()
                    .map(str::to_owned)
                    .unwrap_or_else(|| format!("user-seq:{seq}"));
                out.push(event(
                    c,
                    "turn_started",
                    &turn,
                    &session,
                    Some(&turn),
                    json!({"trigger":"user"}),
                ));
            }
        }
        "turn/end" => {
            if let Some(turn) = turn.as_deref() {
                let status = data.pointer("/reason/kind").and_then(Value::as_str);
                let mut activity = json!({});
                match status {
                    Some("completed") => activity["success"] = json!(true),
                    Some("error" | "failed") => activity["success"] = json!(false),
                    _ => {}
                }
                out.push(event(
                    c,
                    "turn_completed",
                    turn,
                    &session,
                    Some(turn),
                    activity,
                ));
            }
        }
        "tool/call" => {
            if let (Some(call), Some(name)) = (text(data, &["callId"]), text(data, &["name"])) {
                let args = data.get("arguments").cloned().unwrap_or(Value::Null);
                let input = args
                    .as_str()
                    .and_then(|s| serde_json::from_str::<Value>(s).ok())
                    .unwrap_or(args);
                pending_call(state, &call, &name, &input);
            }
        }
        "tool/result" => {
            if let Some(parts) = data.pointer("/message/content").and_then(Value::as_array) {
                for part in parts {
                    if part["type"] == "tool-result" {
                        if let (Some(call), Some(error)) = (
                            text(part, &["toolCallId"]),
                            part.get("isError").and_then(Value::as_bool),
                        ) {
                            finish_call(c, state, &call, &session, !error, &mut out);
                        }
                    }
                }
            }
        }
        _ => {}
    }
    Some(out)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::local_store::pipeline::runner::{DecodeOutcome, HarnessStrategy};
    use std::sync::Arc;
    fn deepseek_facts(rows: Vec<Value>) -> Vec<FactDraft> {
        let strategy = super::super::jsonl_harness::JsonlHarnessStrategy::new(
            super::super::jsonl_harness::DEEPSEEK,
            vec![7; 32],
            "unused",
            SkillBook::new(),
            Arc::new(|_, _| 1),
        );
        let mut state = DecoderState {
            version: 1,
            json: json!({}),
        };
        let mut facts = Vec::new();
        for (i, v) in rows.into_iter().enumerate() {
            let row = RawRecord {
                ordinal: i as u64,
                byte_start: Some(i as u64),
                byte_end: Some(i as u64 + 1),
                native_rowid: None,
                payload: v.to_string().into_bytes(),
                file_mtime_ms: Some(1789257600000),
            };
            if let DecodeOutcome::Emit(mut f) = strategy
                .decode(&row, &mut state, "fixture-session")
                .unwrap()
            {
                facts.append(&mut f);
            }
        }
        facts
    }
    #[test]
    fn deepseek_native_usage_and_skill_result_preserve_semantics() {
        let facts = deepseek_facts(vec![
            json!({"type":"session","id":"session-a","createdAt":1789257600000_i64}),
            json!({"type":"assistant/chunk","seq":1,"time":1789257600010_i64,"data":{"turn":1,"step":1,"chunk":{"type":"usage","usage":{"inputTokens":3,"outputTokens":4,"cacheReadTokens":10}}}}),
            json!({"type":"assistant/message","seq":2,"time":1789257600020_i64,"data":{"turn":1,"step":1,"usage":{"inputTokens":3,"outputTokens":4,"cacheReadTokens":10}}}),
            json!({"type":"tool/call","seq":3,"time":1789257600030_i64,"data":{"callId":"call-a","name":"skill","arguments":"{\"name\":\"fixture-skill\"}"}}),
            json!({"type":"tool/result","seq":4,"time":1789257600040_i64,"data":{"message":{"content":[{"type":"tool-result","toolCallId":"call-a","isError":true}]}}}),
        ]);
        let usage: Vec<_> = facts
            .iter()
            .filter(|f| f.event_type == "model_usage_recorded")
            .collect();
        assert_eq!(usage.len(), 1);
        assert_eq!(usage[0].payload_sections["usage"]["token_total"], 17);
        assert_eq!(
            usage[0].payload_sections["usage"]["input_context_tokens"],
            13
        );
        assert_eq!(usage[0].occurred_at, 1789257600020);
        let skills: Vec<_> = facts
            .iter()
            .filter(|f| f.event_type == "skill_invoked")
            .collect();
        assert_eq!(skills.len(), 1);
        assert_eq!(skills[0].payload_sections["activity"]["success"], false);
    }
    #[test]
    fn deepseek_final_message_correction_keeps_fact_key() {
        let facts = deepseek_facts(vec![
            json!({"type":"session","id":"session-a","createdAt":1789257600000_i64}),
            json!({"type":"assistant/message","seq":2,"time":1789257600020_i64,"data":{"turn":1,"step":1,"usage":{"inputTokens":3,"outputTokens":4}}}),
            json!({"type":"assistant/message","seq":3,"time":1789257600020_i64,"data":{"turn":1,"step":1,"usage":{"inputTokens":3,"outputTokens":5}}}),
        ]);
        let usage: Vec<_> = facts
            .iter()
            .filter(|f| f.event_type == "model_usage_recorded")
            .collect();
        assert_eq!(usage.len(), 2);
        assert_eq!(usage[0].fact_key, usage[1].fact_key);
        assert_ne!(usage[0].event_id, usage[1].event_id);
        assert!(usage[1].fact_revision > usage[0].fact_revision);
    }
}

/// Named telemetry and stable older on-disk protocols. These enter the same fact
/// constructors as native records; only format extraction differs.
fn decode_compat(c: &Context, v: &Value, state: &mut DecoderState) -> Option<Vec<FactDraft>> {
    if c.harness == "grok-build" && v.get("method").is_some() {
        let mut wrapped = v.clone();
        if wrapped.get("timestamp").is_none() {
            wrapped["timestamp"] =
                json!(chrono::DateTime::from_timestamp_millis(c.now)?.to_rfc3339());
        }
        let normalized = adapter_grok_build::normalize_jsonl_record(wrapped).ok()?;
        let mut facts = Vec::new();
        for record in normalized {
            if let Some(mut rows) = decode_compat(c, &record, state) {
                facts.append(&mut rows);
            }
        }
        let update = v.pointer("/params/update");
        if let Some(u) = update {
            let session = v
                .pointer("/params/sessionId")
                .and_then(Value::as_str)
                .unwrap_or(c.scope);
            if u["sessionUpdate"] == "turn_completed" {
                let id = text(u, &["prompt_id", "promptId"]).unwrap_or_else(|| native_id(v, c));
                facts.push(event(
                    c,
                    "turn_completed",
                    &id,
                    session,
                    Some(&id),
                    json!({}),
                ));
            }
            if matches!(
                u["sessionUpdate"].as_str(),
                Some("tool_call" | "tool_call_update")
            ) {
                if let Some(id) = text(u, &["toolCallId", "tool_call_id"]) {
                    let tool = u
                        .pointer("/_meta/x.ai~1tool/name")
                        .and_then(Value::as_str)
                        .or_else(|| u.get("kind").and_then(Value::as_str));
                    // Code already comes from the normalized completed edit record.
                    if let (Some(tool), Some(input)) = (tool, u.get("rawInput")) {
                        if matches!(
                            tool.to_ascii_lowercase().as_str(),
                            "skill" | "read_file" | "read"
                        ) {
                            pending_call(state, &id, tool, input);
                        }
                    }
                    match u["status"].as_str() {
                        Some("completed") => finish_call(c, state, &id, session, true, &mut facts),
                        Some("failed") => finish_call(c, state, &id, session, false, &mut facts),
                        _ => {}
                    }
                }
            }
        }
        return Some(facts);
    }
    let name = text(v, &["name", "event", "type"])?;
    let attrs = v
        .get("attributes")
        .or_else(|| v.get("data"))
        .filter(|a| a.is_object())
        .unwrap_or(v);
    let kind = match name.as_str() {
        "claude_code.token.usage"
        | "grok_code.token.usage"
        | "model/usage"
        | "model_usage_recorded" => "usage",
        "step_finish" if matches!(c.harness, "workbuddy" | "doubao-work") => "usage",
        "claude_code.session.started"
        | "grok_code.session.started"
        | "session/start"
        | "session/started"
        | "session_started" => "session_started",
        "claude_code.turn.completed"
        | "grok_code.turn.completed"
        | "turn/completed"
        | "turn_completed" => "turn_completed",
        "claude_code.skill.execution.completed"
        | "grok_code.skill.execution.completed"
        | "skill/execution/completed"
        | "skill.execution.completed" => "skill_success",
        "claude_code.skill.execution.failed"
        | "grok_code.skill.execution.failed"
        | "skill/execution/failed"
        | "skill.execution.failed" => "skill_failure",
        "claude_code.skill.loaded"
        | "grok_code.skill.loaded"
        | "skill/injected"
        | "skill/loaded"
        | "skill/execution/started" => return Some(Vec::new()),
        "code_changed" | "file/changed" => "code",
        _ => return None,
    };
    let session = text(attrs, &["sessionId", "session.id", "session_id"])
        .or_else(|| text(v, &["sessionId", "session_id"]))
        .unwrap_or_else(|| c.scope.into());
    let turn = text(attrs, &["turnId", "turn.id", "turn_id"]);
    let id = text(
        attrs,
        &[
            "semanticEventId",
            "skill.invocation.id",
            "invocationId",
            "stepId",
            "id",
        ],
    )
    .or_else(|| text(v, &["semanticEventId", "id"]))
    .unwrap_or_else(|| native_id(v, c));
    let mut out = Vec::new();
    match kind {
        "usage" => {
            let u = v
                .get("tokens")
                .or_else(|| attrs.get("usage"))
                .unwrap_or(attrs);
            if let Some(mut f) = usage(
                c,
                &id,
                &session,
                turn.as_deref(),
                u,
                !matches!(c.harness, "workbuddy" | "doubao-work" | "grok-build"),
                if c.harness == "grok-build" { 2 } else { 1 },
            ) {
                if let Some(model) =
                    text(v, &["modelId", "model"]).or_else(|| text(attrs, &["model", "model.id"]))
                {
                    f.model_identity = Some((
                        text(v, &["providerId", "provider"])
                            .or_else(|| text(attrs, &["provider"]))
                            .unwrap_or_else(|| c.harness.into()),
                        model,
                    ));
                }
                out.push(f);
            }
        }
        "skill_success" | "skill_failure" => {
            if let Some(name) = text(attrs, &["skill.name", "skillName", "skill_name", "skill"]) {
                out.push(skill(c, &id, &name, &session, kind == "skill_success"));
            }
        }
        "code" => {
            let mut payload = json!({});
            for (target, keys) in [
                ("generated", &["generated", "generatedLines"][..]),
                ("added", &["added", "addedLines"][..]),
                ("removed", &["removed", "removedLines"][..]),
                ("file_touch_count", &["fileCount", "filesChanged"][..]),
            ] {
                if let Some(n) = number(attrs, keys) {
                    payload[target] = json!(n);
                }
            }
            if !payload.as_object()?.is_empty() {
                let mut f = common::emit_code_fact(
                    c.secret,
                    c.harness,
                    c.scope,
                    TypedNativeKey::Str(id),
                    c.now,
                    c.time_source,
                    Some(&session),
                    0,
                    0,
                );
                f.payload_sections = json!({"code":payload});
                out.push(f);
            }
        }
        event_kind => {
            let mut activity = json!({});
            if let Some(n) = number(attrs, &["durationMs", "duration_ms", "duration.ms"]) {
                activity["duration_ms"] = json!(n);
            }
            if let Some(success) = attrs.get("success").and_then(Value::as_bool) {
                activity["success"] = json!(success);
            }
            out.push(event(
                c,
                event_kind,
                &id,
                &session,
                turn.as_deref(),
                activity,
            ));
        }
    }
    Some(out)
}
