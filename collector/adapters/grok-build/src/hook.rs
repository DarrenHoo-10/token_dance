use std::fs;
use std::path::{Path, PathBuf};

use adapter_sdk::keyed_hmac;
use serde_json::{json, Map, Value};

pub const HOOK_SOURCE_ID: &str = "grok-build-hooks";
pub const HOOK_PORT: u16 = 17418;
pub const HOOK_PATH: &str = "/v1/hooks/grok";
const HOOK_RELATIVE: &str = ".grok/hooks/tokendance.json";

pub fn hook_auth_token(hmac_key: &[u8]) -> String {
    keyed_hmac(hmac_key, &["grok-session-end-hook"])
}

pub fn hook_url(hmac_key: &[u8]) -> String {
    format!(
        "http://127.0.0.1:{HOOK_PORT}{HOOK_PATH}?token={}",
        hook_auth_token(hmac_key)
    )
}

pub fn session_end_hook_document(hmac_key: &[u8]) -> Value {
    json!({
        "hooks": {
            "SessionEnd": [{
                "hooks": [{
                    "type": "http",
                    "url": hook_url(hmac_key),
                    "timeout": 2
                }]
            }]
        }
    })
}

pub fn write_session_end_hook(user_home: &Path, hmac_key: &[u8]) -> std::io::Result<PathBuf> {
    let dir = user_home.join(".grok").join("hooks");
    fs::create_dir_all(&dir)?;
    let path = user_home.join(HOOK_RELATIVE);
    let body = serde_json::to_vec_pretty(&session_end_hook_document(hmac_key))?;
    fs::write(&path, body)?;
    Ok(path)
}

pub struct SessionEndNotice {
    pub session_id: String,
    pub occurred_at: Option<String>,
}

pub fn parse_session_end(body: &Value) -> Option<SessionEndNotice> {
    let object = body.as_object()?;
    let event = object
        .get("hookEventName")
        .or_else(|| object.get("hook_event_name"))
        .and_then(Value::as_str)
        .unwrap_or("");
    if !event.eq_ignore_ascii_case("session_end") && event != "SessionEnd" {
        return None;
    }
    if object.get("subagentType").and_then(Value::as_str).is_some()
        || object.get("subagent_type").and_then(Value::as_str).is_some()
    {
        return None;
    }
    let session_id = object
        .get("sessionId")
        .or_else(|| object.get("session_id"))
        .and_then(Value::as_str)
        .filter(|id| !id.is_empty())?;
    let occurred_at = object
        .get("timestamp")
        .and_then(Value::as_str)
        .map(str::to_owned);
    Some(SessionEndNotice {
        session_id: session_id.to_owned(),
        occurred_at,
    })
}

pub fn find_signals_json(sessions_root: &Path, session_id: &str) -> Option<PathBuf> {
    let mut stack = vec![sessions_root.to_path_buf()];
    let mut visited = 0usize;
    while let Some(dir) = stack.pop() {
        visited += 1;
        if visited > 4096 {
            break;
        }
        let Ok(entries) = fs::read_dir(&dir) else {
            continue;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_dir() {
                if path.file_name().and_then(|name| name.to_str()) == Some(session_id) {
                    let signals = path.join("signals.json");
                    if signals.is_file() {
                        return Some(signals);
                    }
                }
                stack.push(path);
            }
        }
    }
    None
}

pub fn code_record_from_signals(
    signals: &Value,
    session_id: &str,
    occurred_at: Option<&str>,
) -> Option<Value> {
    let object = signals.as_object()?;
    let added = json_u64(object, "agentLinesAdded");
    let removed = json_u64(object, "agentLinesRemoved");
    if added == 0 && removed == 0 {
        return None;
    }
    let files = json_u64(object, "agentFilesTouched").max(1);
    Some(json!({
        "type": "code_changed",
        "occurredAt": occurred_at.unwrap_or("1970-01-01T00:00:00Z"),
        "sessionId": session_id,
        "added": added,
        "removed": removed,
        "generated": added,
        "fileCount": files,
        "semanticEventId": format!("grok-signals:{session_id}")
    }))
}

fn json_u64(object: &Map<String, Value>, key: &str) -> u64 {
    match object.get(key) {
        Some(Value::Number(number)) => number.as_u64().unwrap_or(0),
        Some(Value::String(text)) => text.parse().unwrap_or(0),
        _ => 0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn session_end_parser_skips_subagents_and_other_events() {
        let end = serde_json::json!({
            "hookEventName": "session_end",
            "sessionId": "abc",
            "timestamp": "2026-09-10T00:00:00Z"
        });
        assert_eq!(parse_session_end(&end).unwrap().session_id, "abc");
        let child = serde_json::json!({
            "hook_event_name": "SessionEnd",
            "sessionId": "abc",
            "subagentType": "explore"
        });
        assert!(parse_session_end(&child).is_none());
        let prompt = serde_json::json!({"hookEventName": "user_prompt_submit", "sessionId": "abc"});
        assert!(parse_session_end(&prompt).is_none());
    }

    #[test]
    fn signals_without_agent_lines_are_ignored() {
        let empty = serde_json::json!({"agentLinesAdded": 0, "agentLinesRemoved": 0});
        assert!(code_record_from_signals(&empty, "s", None).is_none());
        let record = code_record_from_signals(
            &serde_json::json!({"agentLinesAdded": 12, "agentLinesRemoved": 3, "agentFilesTouched": 2}),
            "s",
            Some("2026-09-10T00:00:00Z"),
        )
        .unwrap();
        assert_eq!(record["generated"], 12);
        assert_eq!(record["fileCount"], 2);
        assert_eq!(record["semanticEventId"], "grok-signals:s");
    }

    #[test]
    fn hook_document_is_loopback_session_end_only() {
        let doc = session_end_hook_document(b"test-key");
        let encoded = doc.to_string();
        assert!(encoded.contains("127.0.0.1"));
        assert!(encoded.contains("SessionEnd"));
        assert!(!encoded.contains("PostToolUse"));
        assert!(!encoded.contains("UserPromptSubmit"));
    }
}
