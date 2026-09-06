use serde::Serialize;
use serde_json::Value;
mod zcode;
mod connected;
use std::{fs, io::{Read, Seek, SeekFrom}, path::{Path, PathBuf}, sync::Mutex, time::{Duration, Instant}};

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuotaWindow {
    pub(crate) used_percent: f64,
    pub(crate) window_minutes: u64,
    pub(crate) resets_at: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) provider: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) label: Option<String>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentQuota {
    pub(crate) agent_id: String,
    pub(crate) observed_at: String,
    pub(crate) plan: Option<String>,
    pub(crate) windows: Vec<QuotaWindow>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) status: Option<String>,
}

fn parse_quota(line: &str) -> Option<AgentQuota> {
    let value: Value = serde_json::from_str(line).ok()?;
    if value["type"] != "event_msg" || value["payload"]["type"] != "token_count" { return None; }
    let limits = &value["payload"]["rate_limits"];
    if let Some(id) = limits["rate_limit_id"].as_str() {
        if id != "codex" { return None; }
    }
    let observed_at = value["timestamp"].as_str()?;
    chrono::DateTime::parse_from_rfc3339(observed_at).ok()?;
    let windows = ["primary", "secondary"].into_iter().filter_map(|key| {
        let window = &limits[key];
        let used_percent = window["used_percent"].as_f64()?;
        let window_minutes = window["window_minutes"].as_u64()?;
        if !used_percent.is_finite() || !(0.0..=100.0).contains(&used_percent) || window_minutes == 0 { return None; }
        Some(QuotaWindow { used_percent, window_minutes, resets_at: window["resets_at"].as_i64(), provider: None, label: None })
    }).collect::<Vec<_>>();
    if windows.is_empty() { return None; }
    Some(AgentQuota {
        agent_id: "codex".into(), observed_at: observed_at.into(),
        plan: limits["plan_type"].as_str().map(str::to_owned), windows, status: None,
    })
}

// Visit recent session directories only, never auth files or transcript contents
// beyond a bounded tail. Only quota fields cross the IPC boundary.
fn recent_files(dir: &Path, depth: usize, budget: &mut usize, files: &mut Vec<PathBuf>) {
    if depth > 4 || *budget == 0 || files.len() >= 12 { return; }
    *budget -= 1;
    let Ok(entries) = fs::read_dir(dir) else { return; };
    let mut entries = entries.filter_map(Result::ok).collect::<Vec<_>>();
    entries.sort_by_key(|entry| std::cmp::Reverse(entry.file_name()));
    for entry in entries {
        if files.len() >= 12 { break; }
        let Ok(kind) = entry.file_type() else { continue; };
        if kind.is_dir() { recent_files(&entry.path(), depth + 1, budget, files); }
        else if kind.is_file() && entry.path().extension().is_some_and(|ext| ext == "jsonl") { files.push(entry.path()); }
    }
}

fn read_codex_quota() -> Vec<AgentQuota> {
    let home = std::env::var_os("CODEX_HOME").map(PathBuf::from).or_else(||
        std::env::var_os("USERPROFILE").or_else(|| std::env::var_os("HOME")).map(|home| PathBuf::from(home).join(".codex")));
    let Some(home) = home else { return Vec::new(); };
    let mut files = Vec::new();
    recent_files(&home.join("sessions"), 0, &mut 128, &mut files);
    let mut latest: Option<AgentQuota> = None;
    for path in files {
        let Ok(mut file) = fs::File::open(path) else { continue; };
        let Ok(meta) = file.metadata() else { continue; };
        if file.seek(SeekFrom::Start(meta.len().saturating_sub(1024 * 1024))).is_err() { continue; }
        let mut bytes = Vec::new();
        if file.take(1024 * 1024).read_to_end(&mut bytes).is_err() { continue; }
        for line in String::from_utf8_lossy(&bytes).lines().rev() {
            let Some(quota) = parse_quota(line) else { continue; };
            let time = chrono::DateTime::parse_from_rfc3339(&quota.observed_at).unwrap();
            if latest.as_ref().is_none_or(|old| time > chrono::DateTime::parse_from_rfc3339(&old.observed_at).unwrap()) { latest = Some(quota); }
            break;
        }
    }
    latest.into_iter().collect()
}

#[tauri::command]
pub async fn get_agent_quotas() -> Result<Vec<AgentQuota>, String> {
    static CACHE: Mutex<Option<(Instant, Vec<AgentQuota>)>> = Mutex::new(None);
    let mut result = tauri::async_runtime::spawn_blocking(|| -> Result<Vec<AgentQuota>, String> {
        let mut cache = CACHE.lock().map_err(|_| "Quota cache unavailable")?;
        if let Some((time, result)) = cache.as_ref() {
            if time.elapsed() < Duration::from_secs(60) { return Ok(result.clone()); }
        }
        let result = read_codex_quota();
        *cache = Some((Instant::now(), result.clone()));
        Ok(result)
    }).await.map_err(|error| error.to_string())??;
    let (zcode, grok, cursor) = tokio::join!(zcode::read_quota(), connected::grok(), connected::cursor());
    result.extend([zcode, grok, cursor].into_iter().flatten());
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn reads_only_valid_codex_windows() {
        let valid = r#"{"type":"event_msg","timestamp":"2026-09-05T12:00:00Z","payload":{"type":"token_count","rate_limits":{"rate_limit_id":"codex","primary":{"used_percent":37,"window_minutes":300,"resets_at":1788613200}}}}"#;
        let quota = parse_quota(valid).unwrap();
        assert_eq!(quota.windows[0].used_percent, 37.0);
        assert!(parse_quota(&valid.replace("37", "-1")).is_none());
        assert!(parse_quota(&valid.replace("\"codex\"", "\"other\"")).is_none());
        assert!(parse_quota(&valid.replace("token_count", "agent_message")).is_none());
    }
}
