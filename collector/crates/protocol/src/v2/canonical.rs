use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use thiserror::Error;

#[derive(Debug, Error, PartialEq, Eq)]
pub enum ProtocolError {
    #[error("{0}")]
    Message(String),
}

impl ProtocolError {
    fn msg(message: impl Into<String>) -> Self {
        Self::Message(message.into())
    }
}

fn compare_code_points(a: &str, b: &str) -> std::cmp::Ordering {
    let mut a_chars = a.chars();
    let mut b_chars = b.chars();
    loop {
        match (a_chars.next(), b_chars.next()) {
            (None, None) => return std::cmp::Ordering::Equal,
            (None, Some(_)) => return std::cmp::Ordering::Less,
            (Some(_), None) => return std::cmp::Ordering::Greater,
            (Some(ca), Some(cb)) => {
                if ca != cb {
                    return ca.cmp(&cb);
                }
            }
        }
    }
}

pub fn canonicalize(value: &Value) -> Result<String, ProtocolError> {
    match value {
        Value::Null => Ok("null".into()),
        Value::Bool(true) => Ok("true".into()),
        Value::Bool(false) => Ok("false".into()),
        Value::Number(number) => {
            if let Some(n) = number.as_u64() {
                Ok(n.to_string())
            } else if let Some(n) = number.as_i64() {
                if n < 0 {
                    return Err(ProtocolError::msg("negative counts are not allowed"));
                }
                Ok(n.to_string())
            } else {
                Err(ProtocolError::msg("non-integer JSON numbers are not allowed"))
            }
        }
        Value::String(text) => Ok(serde_json::to_string(text).map_err(|err| ProtocolError::msg(err.to_string()))?),
        Value::Array(items) => {
            let mut out = String::from("[");
            for (index, item) in items.iter().enumerate() {
                if index > 0 {
                    out.push(',');
                }
                out.push_str(&canonicalize(item)?);
            }
            out.push(']');
            Ok(out)
        }
        Value::Object(map) => {
            let mut keys: Vec<&String> = map.keys().collect();
            keys.sort_by(|a, b| compare_code_points(a, b));
            let mut out = String::from("{");
            for (index, key) in keys.iter().enumerate() {
                if index > 0 {
                    out.push(',');
                }
                out.push_str(&serde_json::to_string(key).map_err(|err| ProtocolError::msg(err.to_string()))?);
                out.push(':');
                out.push_str(&canonicalize(map.get(*key).expect("key present"))?);
            }
            out.push('}');
            Ok(out)
        }
    }
}

fn assert_uint64_string(value: &Value, path: &str) -> Result<(), ProtocolError> {
    let Some(text) = value.as_str() else {
        return Err(ProtocolError::msg(format!("{path} must be an unsigned decimal string")));
    };
    if text.starts_with('-') {
        return Err(ProtocolError::msg(format!("negative counts are not allowed: {path}")));
    }
    if !matches_uint64(text) {
        return Err(ProtocolError::msg(format!("{path} must be an unsigned decimal string")));
    }
    Ok(())
}

fn matches_uint64(text: &str) -> bool {
    if text.is_empty() || text.len() > 20 {
        return false;
    }
    if text == "0" {
        return true;
    }
    let mut chars = text.chars();
    match chars.next() {
        Some(c @ '1'..='9') => {
            let _ = c;
            chars.all(|c| c.is_ascii_digit())
        }
        _ => false,
    }
}

fn project_object(map: &Map<String, Value>, allowed: &[&str], path: &str) -> Result<Map<String, Value>, ProtocolError> {
    let allowed_set: std::collections::HashSet<&str> = allowed.iter().copied().collect();
    let mut out = Map::new();
    for (key, value) in map {
        if !allowed_set.contains(key.as_str()) {
            return Err(ProtocolError::msg(format!("unknown business field: {path}{key}")));
        }
        if value.is_null() {
            continue;
        }
        out.insert(key.clone(), value.clone());
    }
    Ok(out)
}

fn project_for_hash(event: &Value) -> Result<Value, ProtocolError> {
    let Some(map) = event.as_object() else {
        return Err(ProtocolError::msg("event must be an object"));
    };
    let root_allowed = [
        "eventId",
        "factKey",
        "factRevision",
        "schemaVersion",
        "metricSemanticsVersion",
        "harnessId",
        "eventType",
        "occurredAt",
        "model",
        "skill",
        "sessionKey",
        "turnKey",
        "costScopeKey",
        "payload",
    ];
    let allowed_set: std::collections::HashSet<&str> = root_allowed.into_iter().collect();
    let mut projected = Map::new();
    for (key, value) in map {
        if key == "contentHash" {
            continue;
        }
        if !allowed_set.contains(key.as_str()) {
            return Err(ProtocolError::msg(format!("unknown business field: {key}")));
        }
        if value.is_null() {
            continue;
        }
        match key.as_str() {
            "skill" => {
                let Some(skill) = value.as_object() else {
                    return Err(ProtocolError::msg("skill must be an object"));
                };
                let Some(skill_key) = skill.get("skillKey") else {
                    return Err(ProtocolError::msg("skill.skillKey is required"));
                };
                let mut only = Map::new();
                only.insert("skillKey".into(), skill_key.clone());
                projected.insert("skill".into(), Value::Object(only));
            }
            "model" => {
                let Some(model) = value.as_object() else {
                    return Err(ProtocolError::msg("model must be an object"));
                };
                let mut only = Map::new();
                if let Some(provider) = model.get("providerId") {
                    only.insert("providerId".into(), provider.clone());
                }
                if let Some(model_id) = model.get("modelId") {
                    only.insert("modelId".into(), model_id.clone());
                }
                projected.insert("model".into(), Value::Object(only));
            }
            "payload" => {
                projected.insert("payload".into(), project_payload(value)?);
            }
            "factRevision" | "occurredAt" => {
                assert_uint64_string(value, key)?;
                projected.insert(key.clone(), value.clone());
            }
            _ => {
                projected.insert(key.clone(), value.clone());
            }
        }
    }
    if !projected.contains_key("payload") {
        return Err(ProtocolError::msg("payload is required"));
    }
    Ok(Value::Object(projected))
}

fn project_payload(payload: &Value) -> Result<Value, ProtocolError> {
    let Some(map) = payload.as_object() else {
        return Err(ProtocolError::msg("payload must be an object"));
    };
    let mut out = Map::new();
    for (key, value) in map {
        if value.is_null() {
            continue;
        }
        match key.as_str() {
            "usage" => out.insert("usage".into(), Value::Object(project_counts(value, &[
                "token_total", "input_context_tokens", "output_tokens", "cache_read_tokens",
                "cache_write_tokens", "reasoning_tokens", "request_count"
            ], "usage.")?)),
            "cost" => out.insert("cost".into(), project_cost(value)?),
            "code" => out.insert("code".into(), Value::Object(project_counts(value, &[
                "generated", "accepted", "added", "removed", "file_touch_count"
            ], "code.")?)),
            "activity" => out.insert("activity".into(), project_activity(value)?),
            "context" => out.insert("context".into(), value.clone()),
            "meta" => out.insert("meta".into(), Value::Object(project_object(value.as_object().ok_or_else(|| ProtocolError::msg("meta must be an object"))?, &["accuracy", "time_source", "safe_tags"], "meta.")?)),
            other => return Err(ProtocolError::msg(format!("unknown business field: payload.{other}"))),
        };
    }
    Ok(Value::Object(out))
}

fn project_counts(value: &Value, allowed: &[&str], path: &str) -> Result<Map<String, Value>, ProtocolError> {
    let Some(map) = value.as_object() else {
        return Err(ProtocolError::msg(format!("{} must be an object", path.trim_end_matches('.'))));
    };
    let out = project_object(map, allowed, path)?;
    for (key, child) in &out {
        assert_uint64_string(child, &format!("{path}{key}"))?;
    }
    // reject negative encoded as string with '-'
    for (key, child) in map {
        if let Some(text) = child.as_str() {
            if text.starts_with('-') {
                return Err(ProtocolError::msg(format!("negative counts are not allowed: {path}{key}")));
            }
        }
    }
    Ok(out)
}

fn project_cost(value: &Value) -> Result<Value, ProtocolError> {
    let Some(map) = value.as_object() else {
        return Err(ProtocolError::msg("cost must be an object"));
    };
    let out = project_object(map, &["units", "currency", "source", "price_basis_id", "coverage"], "cost.")?;
    if let Some(units) = out.get("units") {
        assert_uint64_string(units, "cost.units")?;
    }
    Ok(Value::Object(out))
}

fn project_activity(value: &Value) -> Result<Value, ProtocolError> {
    let Some(map) = value.as_object() else {
        return Err(ProtocolError::msg("activity must be an object"));
    };
    let out = project_object(map, &["duration_ms", "success", "trigger", "reason", "tool_category"], "activity.")?;
    if let Some(duration) = out.get("duration_ms") {
        assert_uint64_string(duration, "activity.duration_ms")?;
    }
    Ok(Value::Object(out))
}

pub fn content_hash_canonical_json(event: &Value) -> Result<String, ProtocolError> {
    canonicalize(&project_for_hash(event)?)
}

pub fn compute_content_hash(event: &Value) -> Result<String, ProtocolError> {
    let canonical = content_hash_canonical_json(event)?;
    let digest = Sha256::digest(canonical.as_bytes());
    Ok(URL_SAFE_NO_PAD.encode(digest))
}

pub fn classify_ack(existing_hash: Option<&str>, candidate_hash: &str) -> &'static str {
    match existing_hash {
        None => "accepted",
        Some(existing) if existing == candidate_hash => "duplicate",
        Some(_) => "conflict",
    }
}

/// Reject duplicate keys by scanning object key sequences in raw JSON text.
pub fn reject_duplicate_keys(text: &str) -> Result<(), ProtocolError> {
    let mut i = 0;
    let bytes = text.as_bytes();
    fn skip_ws(bytes: &[u8], i: &mut usize) {
        while *i < bytes.len() && matches!(bytes[*i], b' ' | b'\t' | b'\n' | b'\r') {
            *i += 1;
        }
    }
    fn parse_string<'a>(bytes: &'a [u8], i: &mut usize) -> Result<&'a str, ProtocolError> {
        if *i >= bytes.len() || bytes[*i] != b'"' {
            return Err(ProtocolError::msg("expected string"));
        }
        *i += 1;
        let start = *i;
        while *i < bytes.len() {
            match bytes[*i] {
                b'"' => {
                    let s = std::str::from_utf8(&bytes[start..*i]).map_err(|err| ProtocolError::msg(err.to_string()))?;
                    *i += 1;
                    return Ok(s);
                }
                b'\\' => {
                    *i += 2;
                }
                _ => *i += 1,
            }
        }
        Err(ProtocolError::msg("unterminated string"))
    }
    fn walk(bytes: &[u8], i: &mut usize) -> Result<(), ProtocolError> {
        skip_ws(bytes, i);
        if *i >= bytes.len() {
            return Err(ProtocolError::msg("unexpected eof"));
        }
        match bytes[*i] {
            b'{' => {
                *i += 1;
                skip_ws(bytes, i);
                let mut seen = std::collections::HashSet::new();
                if *i < bytes.len() && bytes[*i] == b'}' {
                    *i += 1;
                    return Ok(());
                }
                loop {
                    skip_ws(bytes, i);
                    let key = parse_string(bytes, i)?;
                    if !seen.insert(key.to_string()) {
                        return Err(ProtocolError::msg(format!("duplicate key \"{key}\"")));
                    }
                    skip_ws(bytes, i);
                    if *i >= bytes.len() || bytes[*i] != b':' {
                        return Err(ProtocolError::msg("expected ':'"));
                    }
                    *i += 1;
                    walk(bytes, i)?;
                    skip_ws(bytes, i);
                    if *i < bytes.len() && bytes[*i] == b',' {
                        *i += 1;
                        continue;
                    }
                    if *i < bytes.len() && bytes[*i] == b'}' {
                        *i += 1;
                        return Ok(());
                    }
                    return Err(ProtocolError::msg("expected ',' or '}'"));
                }
            }
            b'[' => {
                *i += 1;
                skip_ws(bytes, i);
                if *i < bytes.len() && bytes[*i] == b']' {
                    *i += 1;
                    return Ok(());
                }
                loop {
                    walk(bytes, i)?;
                    skip_ws(bytes, i);
                    if *i < bytes.len() && bytes[*i] == b',' {
                        *i += 1;
                        continue;
                    }
                    if *i < bytes.len() && bytes[*i] == b']' {
                        *i += 1;
                        return Ok(());
                    }
                    return Err(ProtocolError::msg("expected ',' or ']'"));
                }
            }
            b'"' => {
                parse_string(bytes, i)?;
                Ok(())
            }
            b't' | b'f' | b'n' | b'-' | b'0'..=b'9' => {
                // skip literal / number coarsely
                if bytes[*i] == b't' {
                    *i += 4;
                } else if bytes[*i] == b'f' {
                    *i += 5;
                } else if bytes[*i] == b'n' {
                    *i += 4;
                } else {
                    while *i < bytes.len() && matches!(bytes[*i], b'0'..=b'9' | b'-' | b'+' | b'.' | b'e' | b'E') {
                        *i += 1;
                    }
                }
                Ok(())
            }
            other => Err(ProtocolError::msg(format!("unexpected token {}", other as char))),
        }
    }
    walk(bytes, &mut i)?;
    skip_ws(bytes, &mut i);
    if i != bytes.len() {
        return Err(ProtocolError::msg("trailing content"));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn rejects_negative_count_strings() {
        let event = json!({
            "eventId": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "factKey": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            "factRevision": "1",
            "schemaVersion": 2,
            "metricSemanticsVersion": 1,
            "harnessId": "codex",
            "eventType": "model_usage_recorded",
            "occurredAt": "1",
            "payload": {"usage": {"token_total": "-1"}, "meta": {"accuracy": "exact", "time_source": "source_record"}}
        });
        let err = compute_content_hash(&event).unwrap_err();
        assert!(err.to_string().contains("unsigned") || err.to_string().contains("negative"));
    }
}
