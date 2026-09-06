use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet};

#[derive(Default)]
pub(crate) struct SkillReads {
    session: String,
    turn: String,
    pending: BTreeMap<String, (String, String, String)>,
    counted: BTreeSet<String>,
}

impl SkillReads {
    pub fn observe(&mut self, value: &Value) -> Option<Value> {
        let p = value.get("payload")?;
        let outer = value.get("type")?.as_str()?;
        if outer == "session_meta" {
            self.session = p
                .get("id")
                .or_else(|| p.get("session_id"))
                .and_then(Value::as_str)
                .unwrap_or("")
                .into();
        }
        if outer == "turn_context" || (outer == "event_msg" && p["type"] == "task_started") {
            if let Some(turn) = p.get("turn_id").and_then(Value::as_str) {
                self.turn = turn.into();
            }
        }
        if outer != "response_item" {
            return None;
        }
        let kind = p.get("type")?.as_str()?;
        let id = p.get("call_id")?.as_str()?;
        if matches!(kind, "function_call" | "custom_tool_call") {
            let name = p.get("name")?.as_str()?;
            let input = p.get("arguments").or_else(|| p.get("input"))?.as_str()?;
            let command = if matches!(
                name,
                "exec_command" | "functions.exec_command" | "shell_command"
            ) {
                let args: Value = serde_json::from_str(input).ok()?;
                args.get("cmd")
                    .or_else(|| args.get("command"))
                    .and_then(Value::as_str)
                    .map(str::to_owned)
            } else if matches!(name, "exec" | "functions.exec") {
                wrapped_command(input)
            } else {
                None
            }?;
            let path = skill_read_path(&command)?;
            if self.pending.len() >= 256 {
                self.pending.clear();
            }
            self.pending
                .insert(id.into(), (path, self.session.clone(), self.turn.clone()));
            return None;
        }
        if !matches!(kind, "function_call_output" | "custom_tool_call_output") {
            return None;
        }
        let (path, session, turn) = self.pending.remove(id)?;
        if !successful_output(p.get("output")?) {
            return None;
        }
        let scope = if turn.is_empty() { id } else { &turn };
        let identity = format!("{session}\x1f{scope}\x1f{path}");
        if !self.counted.insert(identity.clone()) {
            return None;
        }
        if self.counted.len() > 8192 {
            self.counted.clear();
            self.counted.insert(identity.clone());
        }
        let name = path.split('/').rev().nth(1)?;
        // Only globally installed names may leave the device. Project-local
        // skills still contribute under their HMAC identity.
        let global = path
            .split("/.codex/")
            .next()
            .filter(|_| path.contains("/.codex/"))
            .or_else(|| {
                path.split("/.agents/")
                    .next()
                    .filter(|_| path.contains("/.agents/"))
            })
            .is_some_and(|home| {
                home == "/root"
                    || (home.starts_with("/home/") && home.matches('/').count() == 2)
                    || (home.len() > 3
                        && home.as_bytes()[1] == b':'
                        && home[2..].starts_with("/users/")
                        && home.matches('/').count() == 2)
            });
        let public_name = global.then_some(name).filter(|name| {
            !name.is_empty()
                && name.len() <= 100
                && name
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
        });
        Some(
            json!({"type":"skill.read.completed", "timestamp":value.get("timestamp"),
            "thread_id":session, "turn_id":turn, "invocation_id":identity,
            "skill_name":path, "skill_public_name":public_name, "tool_call_id":id}),
        )
    }
}

fn wrapped_command(input: &str) -> Option<String> {
    // Parse literals only. Never execute the wrapper or count conditional reads.
    if input.matches("tools.exec_command(").count() != 1
        || input.contains("if (")
        || input.contains("if(")
        || input.contains("catch")
    {
        return None;
    }
    let after = input.split_once("tools.exec_command(")?.1.trim_start();
    let after = after.strip_prefix('{')?.trim_start();
    let after = after
        .strip_prefix("cmd")
        .or_else(|| after.strip_prefix("\"cmd\""))?
        .trim_start()
        .strip_prefix(':')?
        .trim_start();
    let mut decoder = serde_json::Deserializer::from_str(after).into_iter::<String>();
    decoder.next()?.ok()
}

fn skill_read_path(command: &str) -> Option<String> {
    // A single direct read, optionally piped through Select-Object. Discovery,
    // search, writes, compound commands and arbitrary SKILL.md mentions don't count.
    let command = command.trim();
    if command.contains(['\n', '\r', ';', '`', '$'])
        || command.contains("&&")
        || command.contains("||")
    {
        return None;
    }
    let mut words = Vec::new();
    let mut word = String::new();
    let mut quote = None;
    for c in command.chars() {
        if let Some(q) = quote {
            if c == q {
                quote = None;
            } else {
                word.push(c);
            }
        } else if c == '\'' || c == '"' {
            quote = Some(c);
        } else if c == '|' {
            if !word.is_empty() {
                words.push(word);
                word = String::new();
            }
            break;
        } else if c.is_whitespace() {
            if !word.is_empty() {
                words.push(std::mem::take(&mut word));
            }
        } else {
            word.push(c);
        }
    }
    if quote.is_some() {
        return None;
    }
    if !word.is_empty() {
        words.push(word);
    }
    let tool = words.first()?.to_ascii_lowercase();
    if !matches!(tool.as_str(), "get-content" | "cat" | "type") {
        return None;
    }
    let mut paths = Vec::new();
    let mut args = words.iter().skip(1);
    while let Some(arg) = args.next() {
        match arg.to_ascii_lowercase().as_str() {
            "-path" | "-literalpath" | "-raw" | "--" | "-n" => {}
            "-totalcount" | "-head" | "-tail" => {
                args.next()?.parse::<usize>().ok()?;
            }
            "-encoding" => {
                let encoding = args.next()?.to_ascii_lowercase();
                if !matches!(
                    encoding.as_str(),
                    "utf8" | "utf8bom" | "utf8nobom" | "unicode" | "ascii"
                ) {
                    return None;
                }
            }
            _ => {
                if !arg
                    .replace('\\', "/")
                    .to_ascii_lowercase()
                    .ends_with("/skill.md")
                {
                    return None;
                }
                paths.push(arg);
            }
        }
    }
    if paths.len() != 1 {
        return None;
    }
    let mut path = paths[0].replace('\\', "/");
    if path.contains(['*', '?', ',']) || path.split('/').any(|part| part == "..") {
        return None;
    }
    if path.as_bytes().get(1) == Some(&b':') {
        path = path.to_ascii_lowercase();
    }
    Some(path)
}

fn successful_output(output: &Value) -> bool {
    let texts: Vec<&str> = if let Some(text) = output.as_str() {
        vec![text]
    } else if let Some(parts) = output.as_array() {
        parts
            .iter()
            .filter_map(|p| p.get("text").and_then(Value::as_str))
            .collect()
    } else {
        return false;
    };
    let results: Vec<Value> = texts
        .iter()
        .filter_map(|text| serde_json::from_str::<Value>(text).ok())
        .filter(|v| v.get("exit_code").is_some())
        .collect();
    if results.len() == 1 {
        let body = results[0]["output"].as_str().unwrap_or("");
        return results[0]["exit_code"].as_i64() == Some(0)
            && !body.is_empty()
            && (body.contains("name:") || body.trim_start().starts_with('#'));
    }
    results.is_empty()
        && texts.len() == 1
        && texts[0]
            .lines()
            .any(|line| line.trim() == "Process exited with code 0")
}

#[cfg(test)]
mod tests {
    use super::*;
    fn call(id: &str, command: &str) -> Value {
        json!({"type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":id,"arguments":json!({"cmd":command}).to_string()}})
    }
    fn output(id: &str, code: i32) -> Value {
        json!({"type":"response_item","timestamp":"2026-09-06T12:00:00Z","payload":{"type":"function_call_output","call_id":id,"output":json!({"exit_code":code,"output":"---\nname: browser\ndescription: Browser skill\n---"}).to_string()}})
    }
    #[test]
    fn successful_reads_dedupe_per_turn_and_keep_failed_reads_out() {
        let mut s = SkillReads::default();
        s.observe(&json!({"type":"session_meta","payload":{"id":"session"}}));
        s.observe(&json!({"type":"turn_context","payload":{"turn_id":"t1"}}));
        let cmd = "Get-Content C:/Users/Me/.codex/skills/browser/SKILL.md";
        s.observe(&call("a", cmd));
        assert!(s.observe(&output("a", 1)).is_none());
        s.observe(&call("b", cmd));
        let first = s.observe(&output("b", 0)).unwrap();
        assert_eq!(first["skill_public_name"], "browser");
        s.observe(&call("c", cmd));
        assert!(s.observe(&output("c", 0)).is_none());
        s.observe(&json!({"type":"turn_context","payload":{"turn_id":"t2"}}));
        s.observe(&call("d", cmd));
        assert!(s.observe(&output("d", 0)).is_some());
        let mut replay = SkillReads::default();
        replay.observe(&json!({"type":"session_meta","payload":{"id":"session"}}));
        replay.observe(&json!({"type":"turn_context","payload":{"turn_id":"t1"}}));
        replay.observe(&call("b", cmd));
        assert_eq!(
            replay.observe(&output("b", 0)).unwrap()["invocation_id"],
            first["invocation_id"]
        );
    }
    #[test]
    fn only_literal_successful_reads_are_eligible() {
        for cmd in [
            "rg SKILL.md .",
            "echo /tmp/skills/demo/SKILL.md",
            "Get-Content x/SKILL.md; echo ok",
            "Get-Content $path/SKILL.md",
            "Get-Content a/SKILL.md b/SKILL.md",
            "Get-Content missing/SKILL.md README.md",
        ] {
            assert!(skill_read_path(cmd).is_none(), "{cmd}");
        }
        assert!(wrapped_command(
            "text(await tools.exec_command({cmd:\"cat /root/.codex/skills/browser/SKILL.md\"}));"
        )
        .is_some());
        assert!(wrapped_command("if(false){text(await tools.exec_command({cmd:\"cat /root/.codex/skills/browser/SKILL.md\"}));}").is_none());
        let mut s = SkillReads::default();
        s.observe(&call(
            "private",
            "cat /repo/.agents/skills/private/SKILL.md",
        ));
        assert!(s.observe(&output("private", 0)).unwrap()["skill_public_name"].is_null());
    }
}
