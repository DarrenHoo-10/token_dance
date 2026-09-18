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

/// Snap remaining-minutes noise (e.g. 299) onto the 5-hour / weekly buckets
/// so the picker keeps stable `codex:primary:300m` / `codex:primary:10080m` ids.
pub(crate) fn classify_codex_minutes(minutes: u64) -> Option<(&'static str, u64)> {
    if (180..=360).contains(&minutes) {
        Some(("five_hour", 300))
    } else if (9000..=11_520).contains(&minutes) {
        Some(("weekly", 10080))
    } else {
        None
    }
}

pub(crate) fn decorate_codex_window(used: f64, minutes: u64, resets_at: Option<i64>) -> Option<QuotaWindow> {
    if !used.is_finite() || !(0.0..=100.0).contains(&used) || minutes == 0 { return None; }
    let (label, minutes) = classify_codex_minutes(minutes)?;
    Some(QuotaWindow {
        used_percent: used,
        window_minutes: minutes,
        resets_at,
        provider: None,
        label: Some(label.into()),
    })
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
    let mut windows = ["primary", "secondary"].into_iter().filter_map(|key| {
        let window = &limits[key];
        decorate_codex_window(window["used_percent"].as_f64()?, window["window_minutes"].as_u64()?, window["resets_at"].as_i64())
    }).collect::<Vec<_>>();
    windows.sort_by_key(|window| window.window_minutes);
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
    type Fingerprint = Vec<(std::time::SystemTime, PathBuf, u64)>;
    static TAIL_CACHE: Mutex<Option<(Fingerprint,Vec<AgentQuota>)>> = Mutex::new(None);
    let fingerprint:Fingerprint=files.iter().map(|(mtime,path)|(*mtime,path.clone(),fs::metadata(path).map(|m|m.len()).unwrap_or(0))).collect();
    if let Ok(cache)=TAIL_CACHE.lock() {
        if let Some((_, result)) = cache.as_ref().filter(|(old, _)| old == &fingerprint) {
            return result.clone();
        }
    }
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
    let result:Vec<_>=latest.into_iter().collect();
    if let Ok(mut cache)=TAIL_CACHE.lock() {*cache=Some((fingerprint,result.clone()));}
    result
}

use std::sync::atomic::{AtomicBool, Ordering};
static REMOTE_QUOTAS: Mutex<std::collections::BTreeMap<&'static str, AgentQuota>> = Mutex::new(std::collections::BTreeMap::new());
static ZCODE_REFRESH: AtomicBool = AtomicBool::new(false);
static GROK_REFRESH: AtomicBool = AtomicBool::new(false);
static CURSOR_REFRESH: AtomicBool = AtomicBool::new(false);
static CODEX_REFRESH: AtomicBool = AtomicBool::new(false);

fn refresh_remote(id: &'static str, inflight: &'static AtomicBool, future: impl std::future::Future<Output=Option<AgentQuota>> + Send + 'static) {
    if inflight.compare_exchange(false,true,Ordering::AcqRel,Ordering::Relaxed).is_err() { return; }
    tokio::spawn(async move {
        struct Reset(&'static AtomicBool);
        impl Drop for Reset { fn drop(&mut self) { self.0.store(false,Ordering::Release); } }
        let _reset=Reset(inflight);
        let value=future.await;
        if let Ok(mut cache)=REMOTE_QUOTAS.lock() {
            if let Some(quota)=value {cache.insert(id,quota);} else {cache.remove(id);}
        }
    });
}

#[tauri::command]
pub async fn get_agent_quotas() -> Result<Vec<AgentQuota>, String> {
    if crate::startup_error_smoke() { return Ok(Vec::new()); }
    static CACHE: Mutex<Option<(Instant, Vec<AgentQuota>)>> = Mutex::new(None);
    let mut result = tauri::async_runtime::spawn_blocking(|| -> Result<Vec<AgentQuota>, String> {
        let mut cache = CACHE.lock().map_err(|_| "Quota cache unavailable")?;
        if let Some((time, result)) = cache.as_ref() {
            if time.elapsed() < Duration::from_secs(3) { return Ok(result.clone()); }
        }
        let result = read_codex_quota();
        *cache = Some((Instant::now(), result.clone()));
        Ok(result)
    }).await.map_err(|error| error.to_string())??;
    // Local tests can inspect log-based quotas without using connected accounts.
    if crate::local_test::enabled() { return Ok(result); }
    // Publish local observations immediately; slow network providers refresh independently.
    refresh_remote("codex", &CODEX_REFRESH, connected::codex());
    refresh_remote("zcode", &ZCODE_REFRESH, zcode::read_quota());
    refresh_remote("grok-build", &GROK_REFRESH, connected::grok());
    refresh_remote("cursor", &CURSOR_REFRESH, connected::cursor());
    let remotes = REMOTE_QUOTAS.lock().map_err(|_| "Quota cache unavailable")?;
    if let Some(codex) = remotes.get("codex") {
        let replace = match codex.status.as_deref() {
            Some("ready" | "auth_required" | "no_quota" | "unlimited") => true,
            Some(_) if !codex.windows.is_empty() => true,
            _ => !result.iter().any(|quota| quota.agent_id == "codex"),
        };
        if replace {
            result.retain(|quota| quota.agent_id != "codex");
            result.push(codex.clone());
        }
    }
    result.extend(remotes.values().filter(|quota| quota.agent_id != "codex").cloned());
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
        assert_eq!(quota.windows[0].window_minutes, 300);
        assert_eq!(quota.windows[0].label.as_deref(), Some("five_hour"));
        assert!(parse_quota(&valid.replace("37", "-1")).is_none());
        assert!(parse_quota(&valid.replace("\"codex\"", "\"other\"")).is_none());
        assert!(parse_quota(&valid.replace("token_count", "agent_message")).is_none());
        assert!(parse_quota(&valid.replace("300", "30")).is_none());
    }

    #[test]
    fn five_hour_window_sorts_ahead_of_weekly_for_the_picker() {
        assert_eq!(classify_codex_minutes(299), Some(("five_hour", 300)));
        assert_eq!(classify_codex_minutes(10_080), Some(("weekly", 10_080)));
        assert_eq!(classify_codex_minutes(30), None);
        let both = r#"{"type":"event_msg","timestamp":"2026-09-05T12:00:00Z","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":49,"window_minutes":10080,"resets_at":1788613200},"secondary":{"used_percent":12,"window_minutes":299,"resets_at":1788600000}}}}"#;
        let quota = parse_quota(both).unwrap();
        assert_eq!(quota.windows[0].label.as_deref(), Some("five_hour"));
        assert_eq!(quota.windows[0].window_minutes, 300);
        assert_eq!(quota.windows[1].label.as_deref(), Some("weekly"));
        assert_eq!(quota.windows[1].window_minutes, 10080);
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
    fn appended_quota_replaces_cached_reading_even_with_unchanged_percentage() {
        use std::io::Write;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("rollout.jsonl");
        fs::write(&path, format!("{}\n", event(Some("codex"), 29.0, "2026-09-12T12:00:00Z"))).unwrap();
        let old = read_codex_quota_from(dir.path());
        assert_eq!(old[0].windows[0].used_percent, 29.0);
        assert_eq!(read_codex_quota_from(dir.path())[0].observed_at, old[0].observed_at);
        let mut file = fs::OpenOptions::new().append(true).open(path).unwrap();
        writeln!(file, "{}", event(Some("codex"), 29.0, "2026-09-12T13:00:00Z")).unwrap();
        assert_eq!(read_codex_quota_from(dir.path())[0].observed_at, "2026-09-12T13:00:00Z");
        writeln!(file, "{}", event(Some("codex"), 30.0, "2026-09-12T13:00:03Z")).unwrap();
        assert_eq!(read_codex_quota_from(dir.path())[0].windows[0].used_percent, 30.0);
        assert_eq!(read_codex_quota_from(dir.path())[0].windows[0].label.as_deref(), Some("weekly"));
        assert_eq!(read_codex_quota_from(dir.path())[0].windows[0].window_minutes, 10080);
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

#[cfg(test)]
mod refresh_tests {
    use super::*;
    #[tokio::test]
    async fn pending_provider_does_not_hold_other_results_and_is_single_flight() {
        static SLOW:AtomicBool=AtomicBool::new(false);
        static FAST:AtomicBool=AtomicBool::new(false);
        let (release,wait)=tokio::sync::oneshot::channel::<()>();
        refresh_remote("test-slow",&SLOW,async move {let _=wait.await;None});
        assert!(SLOW.load(Ordering::Acquire));
        refresh_remote("test-slow",&SLOW,async {panic!("duplicate refresh must not run")});
        refresh_remote("test-fast",&FAST,async {Some(AgentQuota{agent_id:"test-fast".into(),observed_at:"2026-09-13T00:00:00Z".into(),plan:None,windows:vec![],status:None})});
        while FAST.load(Ordering::Acquire) {tokio::task::yield_now().await;}
        assert!(REMOTE_QUOTAS.lock().unwrap().contains_key("test-fast"));
        assert!(SLOW.load(Ordering::Acquire));
        release.send(()).unwrap();
        while SLOW.load(Ordering::Acquire) {tokio::task::yield_now().await;}
        REMOTE_QUOTAS.lock().unwrap().remove("test-fast");
    }
}
