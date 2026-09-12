//! P0 content_hash: SHA-256 over the full canonical business envelope (no HMAC / no secret).

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use serde_json::{json, Map, Value};

use super::runner::FactDraft;

const USAGE_COUNT_KEYS: &[&str] = &[
    "token_total",
    "input_context_tokens",
    "output_tokens",
    "cache_read_tokens",
    "cache_write_tokens",
    "reasoning_tokens",
    "request_count",
];

const CODE_COUNT_KEYS: &[&str] = &[
    "generated",
    "accepted",
    "added",
    "removed",
    "file_touch_count",
];

/// Compute the frozen 32-byte content hash for a fact about to be persisted.
pub fn compute_p0_content_hash(
    harness_id: &str,
    draft: &FactDraft,
    local_payload: &Value,
) -> Result<[u8; 32], String> {
    let wire_payload = local_payload_to_hash_payload(local_payload)?;
    let mut event = Map::new();
    event.insert("eventId".into(), json!(b64_32(&draft.event_id)));
    event.insert("factKey".into(), json!(b64_32(&draft.fact_key)));
    event.insert(
        "factRevision".into(),
        json!(draft.fact_revision.to_string()),
    );
    event.insert("schemaVersion".into(), json!(draft.schema_version as u32));
    event.insert(
        "metricSemanticsVersion".into(),
        json!(draft.metric_semantics_version as u32),
    );
    event.insert("harnessId".into(), json!(harness_id));
    event.insert("eventType".into(), json!(draft.event_type.clone()));
    event.insert("occurredAt".into(), json!(draft.occurred_at.to_string()));
    if let Some((provider, model)) = &draft.model_identity {
        event.insert(
            "model".into(),
            json!({"providerId":provider,"modelId":model}),
        );
    }
    if let Some(skill_key) = draft.skill_key.as_ref() {
        event.insert("skill".into(), json!({ "skillKey": b64_32(skill_key) }));
    }
    if let Some(session) = draft.session_key.as_ref() {
        event.insert("sessionKey".into(), json!(b64_32(session)));
    }
    if let Some(turn) = draft.turn_key.as_ref() {
        event.insert("turnKey".into(), json!(b64_32(turn)));
    }
    if let Some(cost) = draft.cost_scope_key.as_ref() {
        event.insert("costScopeKey".into(), json!(b64_32(cost)));
    }
    event.insert("payload".into(), wire_payload);
    let hash_b64 =
        protocol::v2::compute_content_hash(&Value::Object(event)).map_err(|e| e.to_string())?;
    decode_b64_32(&hash_b64)
}

fn local_payload_to_hash_payload(local: &Value) -> Result<Value, String> {
    let Some(obj) = local.as_object() else {
        return Err("payload must be object".into());
    };
    let mut out = Map::new();
    for (key, value) in obj {
        if value.is_null() {
            continue;
        }
        match key.as_str() {
            "usage" => {
                out.insert(
                    "usage".into(),
                    stringify_allowed_counts(value, USAGE_COUNT_KEYS)?,
                );
            }
            "code" => {
                out.insert(
                    "code".into(),
                    stringify_allowed_counts(value, CODE_COUNT_KEYS)?,
                );
            }
            "cost" => {
                let mut cost = value.clone();
                if let Some(map) = cost.as_object_mut() {
                    stringify_uint_leaves(map);
                }
                out.insert("cost".into(), cost);
            }
            "activity" => {
                let mut activity = value.clone();
                if let Some(map) = activity.as_object_mut() {
                    if let Some(Value::Number(n)) = map.get("duration_ms").cloned() {
                        if let Some(u) = n.as_u64() {
                            map.insert("duration_ms".into(), Value::String(u.to_string()));
                        }
                    }
                }
                out.insert("activity".into(), activity);
            }
            "context" => {
                out.insert("context".into(), value.clone());
            }
            "meta" => {
                let Some(meta) = value.as_object() else {
                    return Err("meta must be object".into());
                };
                let mut projected = Map::new();
                for (mk, mv) in meta {
                    if mv.is_null() {
                        continue;
                    }
                    match mk.as_str() {
                        "accuracy" | "safe_tags" => {
                            projected.insert(mk.clone(), mv.clone());
                        }
                        "time_source" => {
                            let ts = mv.as_str().unwrap_or("");
                            let wire = if ts == "previous_record" {
                                "prior_record"
                            } else {
                                ts
                            };
                            projected.insert("time_source".into(), json!(wire));
                        }
                        other => {
                            return Err(format!("unknown meta field {other}"));
                        }
                    }
                }
                out.insert("meta".into(), Value::Object(projected));
            }
            other => return Err(format!("unknown payload section {other}")),
        }
    }
    Ok(Value::Object(out))
}

fn stringify_allowed_counts(value: &Value, allowed: &[&str]) -> Result<Value, String> {
    let Some(map) = value.as_object() else {
        return Err("count object required".into());
    };
    let allowed_set: std::collections::HashSet<&str> = allowed.iter().copied().collect();
    let mut out = Map::new();
    for (key, child) in map {
        if child.is_null() {
            continue;
        }
        if !allowed_set.contains(key.as_str()) {
            return Err(format!("unknown count field {key}"));
        }
        match child {
            Value::Number(n) => {
                if let Some(u) = n.as_u64() {
                    out.insert(key.clone(), Value::String(u.to_string()));
                } else if let Some(i) = n.as_i64() {
                    if i < 0 {
                        return Err(format!("negative count {key}"));
                    }
                    out.insert(key.clone(), Value::String(i.to_string()));
                } else {
                    return Err(format!("non-integer count {key}"));
                }
            }
            Value::String(s) => {
                out.insert(key.clone(), Value::String(s.clone()));
            }
            _ => return Err(format!("invalid count type for {key}")),
        }
    }
    Ok(Value::Object(out))
}

fn stringify_uint_leaves(map: &mut Map<String, Value>) {
    for (_k, v) in map.iter_mut() {
        match v {
            Value::Number(n) => {
                if let Some(u) = n.as_u64() {
                    *v = Value::String(u.to_string());
                } else if let Some(i) = n.as_i64() {
                    if i >= 0 {
                        *v = Value::String(i.to_string());
                    }
                }
            }
            Value::Object(nested) => stringify_uint_leaves(nested),
            _ => {}
        }
    }
}

fn b64_32(bytes: &[u8; 32]) -> String {
    URL_SAFE_NO_PAD.encode(bytes)
}

fn decode_b64_32(text: &str) -> Result<[u8; 32], String> {
    let bytes = URL_SAFE_NO_PAD
        .decode(text)
        .map_err(|e| format!("content_hash decode: {e}"))?;
    if bytes.len() != 32 {
        return Err(format!("content_hash length {}, expected 32", bytes.len()));
    }
    let mut out = [0u8; 32];
    out.copy_from_slice(&bytes);
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::local_store::pipeline::runner::{FactDraft, TimeSource, TokenAccuracy};

    fn draft_with_usage(tokens: u64) -> FactDraft {
        FactDraft {
            event_id: [1u8; 32],
            fact_key: [2u8; 32],
            fact_revision: 1,
            event_type: "model_usage_recorded".into(),
            schema_version: 2,
            metric_semantics_version: 1,
            occurred_at: 1_700_000_000_000,
            time_source: TimeSource::SourceRecord,
            model_key: 0,
            model_identity: None,
            skill_id: None,
            skill_key: None,
            session_key: Some([3u8; 32]),
            turn_key: None,
            cost_scope_key: None,
            accuracy: TokenAccuracy::Exact,
            payload_sections: json!({
                "usage": {
                    "token_total": tokens,
                    "input_context_tokens": tokens,
                    "output_tokens": 0
                }
            }),
        }
    }

    #[test]
    fn p0_hash_is_stable_and_secret_free() {
        let draft = draft_with_usage(42);
        let mut payload = draft.payload_sections.clone();
        payload.as_object_mut().unwrap().insert(
            "meta".into(),
            json!({"accuracy":"exact","time_source":"source_record"}),
        );
        let a = compute_p0_content_hash("codex", &draft, &payload).unwrap();
        let b = compute_p0_content_hash("codex", &draft, &payload).unwrap();
        assert_eq!(a, b);
        // Changing tokens changes hash.
        let draft2 = draft_with_usage(43);
        let mut payload2 = draft2.payload_sections.clone();
        payload2.as_object_mut().unwrap().insert(
            "meta".into(),
            json!({"accuracy":"exact","time_source":"source_record"}),
        );
        let c = compute_p0_content_hash("codex", &draft2, &payload2).unwrap();
        assert_ne!(a, c);
    }
}
