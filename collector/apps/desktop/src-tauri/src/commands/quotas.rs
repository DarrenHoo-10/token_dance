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
    // Current Codex uses limit_id; older clients used rate_limit_id. Never
    // interpret a model-specific bucket (e.g. Spark) as the account quota.
    // Reject conflicting or malformed IDs rather than falling back to full quota.
    for key in ["limit_id", "rate_limit_id"] {
        if let Some(id) = limits.get(key).filter(|id| !id.is_null()) {
            if id.as_str() != Some("codex") { return None; }
        }
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

// Inspect metadata only when discovering sessions. Creation-date filenames do
// not tell us which conversations are active: a resumed session stays in its
// original directory. Read only the 12 most recently modified bounded tails.
fn recent_files(dir: &Path, depth: usize, budget: &mut usize, files: &mut Vec<(std::time::SystemTime, PathBuf)>) {
    if depth > 4 || *budget == 0 { return; }
    let Ok(entries) = fs::read_dir(dir) else { return; };
    for entry in entries.filter_map(Result::ok) {
        if *budget == 0 { break; }
        *budget -= 1;
        let Ok(kind) = entry.file_type() else { continue; };
        if kind.is_dir() { recent_files(&entry.path(), depth + 1, budget, files); }
        else if kind.is_file() && entry.path().extension().is_some_and(|ext| ext == "jsonl") {
            let Ok(modified) = entry.metadata().and_then(|meta| meta.modified()) else { continue; };
            files.push((modified, entry.path()));
            files.sort_unstable_by(|a, b| b.cmp(a));
            files.truncate(12);
        }
    }
}

fn read_codex_quota() -> Vec<AgentQuota> {
    let home = std::env::var_os("CODEX_HOME").map(PathBuf::from).or_else(||
        std::env::var_os("USERPROFILE").or_else(|| std::env::var_os("HOME")).map(|home| PathBuf::from(home).join(".codex")));
    let Some(home) = home else { return Vec::new(); };
    read_codex_quota_from(&home.join("sessions"))
}

fn read_codex_quota_from(sessions: &Path) -> Vec<AgentQuota> {
    let mut files = Vec::new();
    recent_files(sessions, 0, &mut 100_000, &mut files);
    let mut latest: Option<AgentQuota> = None;
    for (_, path) in files {
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
    if crate::startup_error_smoke() { return Ok(Vec::new()); }
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
    // Local tests can inspect log-based quotas without using connected accounts.
    if crate::local_test::enabled() { return Ok(result); }
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

    fn event(id: Option<&str>, used: f64, timestamp: &str) -> String {
        let mut limits = serde_json::json!({
            "primary": {"used_percent": used, "window_minutes": 10080},
            "secondary": null
        });
        if let Some(id) = id { limits["limit_id"] = id.into(); }
        serde_json::json!({"type": "event_msg", "timestamp": timestamp,
            "payload": {"type": "token_count", "rate_limits": limits}}).to_string()
    }

    #[test]
    fn current_bucket_id_rejects_spark_and_conflicting_legacy_id() {
        let codex = event(Some("codex"), 14.0, "2026-09-12T12:00:00Z");
        assert_eq!(parse_quota(&codex).unwrap().windows[0].used_percent, 14.0);
        assert!(parse_quota(&event(Some("codex_bengalfox"), 0.0, "2026-09-12T12:00:01Z")).is_none());
        let mut conflicting: Value = serde_json::from_str(&codex).unwrap();
        conflicting["payload"]["rate_limits"]["rate_limit_id"] = "other".into();
        assert!(parse_quota(&conflicting.to_string()).is_none());
        conflicting["payload"]["rate_limits"]["rate_limit_id"] = "codex".into();
        conflicting["payload"]["rate_limits"]["limit_id"] = 42.into();
        assert!(parse_quota(&conflicting.to_string()).is_none());
        assert!(parse_quota(&event(None, 14.0, "2026-09-12T12:00:00Z")).is_some());
    }

    #[test]
    fn trailing_spark_update_does_not_replace_main_quota_or_refresh_its_age() {
        let dir = tempfile::tempdir().unwrap();
        let primary = event(Some("codex"), 14.0, "2026-09-12T12:00:00Z");
        let spark = event(Some("codex_bengalfox"), 0.0, "2026-09-12T13:00:00Z");
        fs::write(dir.path().join("session.jsonl"), format!("{primary}\n{spark}\n")).unwrap();
        let result = read_codex_quota_from(dir.path());
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].windows[0].used_percent, 14.0);
        assert_eq!(result[0].observed_at, "2026-09-12T12:00:00Z");
        fs::write(dir.path().join("session.jsonl"), spark).unwrap();
        assert!(read_codex_quota_from(dir.path()).is_empty());
    }

    #[test]
    fn resumed_old_session_is_read_before_newer_created_sessions() {
        use std::time::{Duration, SystemTime};
        let dir = tempfile::tempdir().unwrap();
        let older = dir.path().join("2026/08/01");
        let newer = dir.path().join("2026/09/12");
        fs::create_dir_all(&older).unwrap();
        fs::create_dir_all(&newer).unwrap();
        for index in 0..14 {
            let path = newer.join(format!("rollout-{index:02}.jsonl"));
            fs::write(&path, event(Some("codex"), 5.0, "2026-09-12T11:00:00Z")).unwrap();
            fs::OpenOptions::new().write(true).open(path).unwrap().set_modified(SystemTime::UNIX_EPOCH + Duration::from_secs(100)).unwrap();
        }
        fs::write(older.join("rollout-old.jsonl"), event(Some("codex"), 14.0, "2026-09-12T12:00:00Z")).unwrap();
        let result = read_codex_quota_from(dir.path());
        assert_eq!(result[0].windows[0].used_percent, 14.0);
        let mut files = Vec::new();
        recent_files(dir.path(), 0, &mut 100_000, &mut files);
        assert_eq!(files.len(), 12);
        assert!(files[0].1.ends_with("rollout-old.jsonl"));
    }
}
