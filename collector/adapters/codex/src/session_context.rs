use serde_json::{json, Value};
use std::collections::BTreeMap;
#[derive(Default)]
pub(crate) struct SessionContext {
    pub model: Option<String>,
    pending: BTreeMap<String, (u64, u64, u32)>,
}
impl SessionContext {
    pub fn observe(&mut self, v: &Value) -> Option<Value> {
        let p = v.get("payload")?;
        if v.get("type").and_then(Value::as_str) == Some("turn_context") {
            self.model = p.get("model").and_then(Value::as_str).map(str::to_owned)
        }
        let kind = p.get("type").and_then(Value::as_str).unwrap_or("");
        if matches!(kind, "custom_tool_call" | "function_call") {
            let name = p.get("name").and_then(Value::as_str).unwrap_or("");
            let input = p
                .get("input")
                .or_else(|| p.get("arguments"))
                .and_then(Value::as_str)
                .unwrap_or("");
            let patch = if matches!(name, "apply_patch" | "functions.apply_patch") {
                Some(input.to_string())
            } else if name == "exec" {
                literal_patch(input)
            } else {
                None
            };
            if let Some(counts) = patch.and_then(|p| patch_counts(&p)) {
                if let Some(id) = p.get("call_id").and_then(Value::as_str) {
                    self.pending.insert(id.into(), counts);
                }
            }
            if self.pending.len() > 256 {
                self.pending.clear()
            }
        }
        if matches!(kind, "custom_tool_call_output" | "function_call_output") {
            let id = p.get("call_id").and_then(Value::as_str)?;
            let (a, d, f) = self.pending.remove(id)?;
            let output = p.get("output")?;
            let success = if let Some(text) = output.as_str() {
                text.starts_with("Success. Updated the following files:")
            } else if let Some(parts) = output.as_array() {
                parts.iter().any(|v| {
                    v.get("text")
                        .and_then(Value::as_str)
                        .is_some_and(|s| s.starts_with("Script completed"))
                }) && parts
                    .get(1)
                    .and_then(|v| v.get("text"))
                    .and_then(Value::as_str)
                    == Some("{}")
            } else {
                false
            };
            if success {
                return Some(
                    json!({"type":"patch.applied","timestamp":v.get("timestamp"),"added_lines":a,"removed_lines":d,"generated_lines":a,"file_count":f,"tool_call_id":id}),
                );
            }
        }
        None
    }
}
fn literal_patch(input: &str) -> Option<String> {
    // Only parse a single literal argument; never execute JavaScript or retain code.
    if !input
        .trim_start()
        .starts_with("text(await tools.apply_patch(")
        || input.contains("catch")
        || input.contains("if (")
        || input.contains("if(")
        || input.matches("tools.apply_patch(").count() != 1
    {
        return None;
    }
    let (_, suffix) = input.split_once("tools.apply_patch(")?;
    let text = suffix.trim_start();
    if !text.starts_with('"') {
        return None;
    }
    let mut stream = serde_json::Deserializer::from_str(text).into_iter::<String>();
    let patch = stream.next()?.ok()?;
    if text[stream.byte_offset()..].trim_start().starts_with(')') {
        Some(patch)
    } else {
        None
    }
}
fn patch_counts(patch: &str) -> Option<(u64, u64, u32)> {
    if !patch.starts_with("*** Begin Patch") || !patch.trim_end().ends_with("*** End Patch") {
        return None;
    }
    let (mut a, mut d, mut f) = (0, 0, 0);
    let mut inside = false;
    for line in patch.lines() {
        if line.starts_with("*** Add File:") || line.starts_with("*** Update File:") {
            f += 1;
            inside = true
        } else if line.starts_with("*** Delete File:") {
            f += 1;
            inside = false
        } else if inside && line.starts_with('+') {
            a += 1
        } else if inside && line.starts_with('-') {
            d += 1
        }
    }
    if f > 0 {
        Some((a, d, f))
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn wrapped_patch_counts_only_verified_literal_result() {
        let patch = "*** Begin Patch\n*** Add File: secret\n+line1\n+line2\n*** End Patch";
        let input = format!(
            "text(await tools.apply_patch({}));",
            serde_json::to_string(patch).unwrap()
        );
        let mut ctx = SessionContext::default();
        ctx.observe(&json!({"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"c","input":input}}));
        let out=ctx.observe(&json!({"type":"response_item","timestamp":"2026-09-06T00:00:00Z","payload":{"type":"custom_tool_call_output","call_id":"c","output":[{"text":"Script completed\nOutput:"},{"text":"{}"}]}})).unwrap();
        assert_eq!(out["generated_lines"], 2);
        assert!(!out.to_string().contains("secret"));
        assert!(literal_patch("text(await tools.apply_patch(variable));").is_none());
        assert!(literal_patch(&format!("if(false){{{input}}}")).is_none());
    }
}
