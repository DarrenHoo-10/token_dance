use super::{AgentQuota, QuotaWindow};
use reqwest::{header::HeaderValue, Client};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{path::PathBuf, time::{Duration, Instant}};
use tokio::sync::Mutex;

const CONFIG_LIMIT: u64 = 2 * 1024 * 1024;
const RESPONSE_LIMIT: usize = 256 * 1024;
const REFRESH: Duration = Duration::from_secs(300);
const PROVIDERS: [(&str, &str, &str); 2] = [
    ("builtin:bigmodel-coding-plan", "open.bigmodel.cn", "GLM"),
    ("builtin:zai-coding-plan", "api.z.ai", "Z.ai"),
];

// Never serialize or log the provider key. Only normalized quota data crosses IPC.
struct Credential {
    host: &'static str,
    label: &'static str,
    authorization: HeaderValue,
}

fn credentials(config: &Value) -> Vec<Credential> {
    PROVIDERS.iter().filter_map(|(id, host, label)| {
        let row = &config["provider"][*id];
        if row["enabled"].as_bool() != Some(true) { return None; }
        let url = reqwest::Url::parse(row["options"]["baseURL"].as_str()?).ok()?;
        // Bind a built-in provider's credential to its verified official host.
        // Custom hosts, URL credentials, ports, and environment URL overrides
        // cannot redirect credentials to another service.
        if url.scheme() != "https" || url.host_str() != Some(*host) ||
            !url.username().is_empty() || url.password().is_some() || url.port_or_known_default() != Some(443) { return None; }
        let key = row["options"]["apiKey"].as_str()?.trim();
        let key = if key.get(..7).is_some_and(|p| p.eq_ignore_ascii_case("bearer ")) { key[7..].trim() } else { key };
        if !(8..=4096).contains(&key.len()) { return None; }
        let mut authorization = HeaderValue::from_str(key).ok()?;
        authorization.set_sensitive(true);
        Some(Credential { host, label, authorization })
    }).collect()
}

fn unavailable(status: &str) -> AgentQuota {
    AgentQuota { agent_id: "zcode".into(), observed_at: chrono::Utc::now().to_rfc3339(),
        plan: None, windows: vec![], status: Some(status.into()) }
}

fn parse_response(value: &Value, provider: &str) -> Result<AgentQuota, &'static str> {
    if value["success"] == false || value.get("code").is_some_and(|v| v != 0 && v != 200) {
        return Err(if matches!(value["code"].as_i64(), Some(401 | 403)) { "auth_required" } else { "unavailable" });
    }
    let limits = value["data"]["limits"].as_array().ok_or("unavailable")?;
    let mut windows = vec![];
    for row in limits {
        // TIME_LIMIT is a separate monthly tool/call allowance, not token quota.
        // Unknown units must not become an invented period or a zero allowance.
        if row["type"] != "TOKENS_LIMIT" { continue; }
        let minutes = match (row["unit"].as_u64(), row["number"].as_u64()) {
            (Some(3), Some(5)) => 300,
            (Some(6), Some(1)) => 10080,
            _ => continue,
        };
        let Some(used) = row["percentage"].as_f64().filter(|v| v.is_finite() && (0.0..=100.0).contains(v)) else { continue; };
        let reset = row["nextResetTime"].as_i64().filter(|v| *v > 0)
            .map(|v| if v >= 1_000_000_000_000 { v / 1000 } else { v });
        if windows.iter().any(|w: &QuotaWindow| w.window_minutes == minutes) { continue; }
        windows.push(QuotaWindow { used_percent: used, window_minutes: minutes,
            resets_at: reset, provider: Some(provider.into()) });
    }
    if windows.is_empty() { return Err("no_quota"); }
    windows.sort_by_key(|window| window.window_minutes);
    let level = match value["data"]["level"].as_str().unwrap_or("").to_ascii_lowercase().as_str() {
        "lite" => " Lite", "pro" => " Pro", "max" => " Max", "free" => " Free", _ => "",
    };
    Ok(AgentQuota { agent_id: "zcode".into(), observed_at: chrono::Utc::now().to_rfc3339(),
        plan: Some(format!("{provider}{level}")), windows, status: Some("ready".into()) })
}

async fn fetch(client: &Client, url: &str, authorization: HeaderValue, label: &str) -> Result<AgentQuota, &'static str> {
    let mut response = client.get(url).header(reqwest::header::AUTHORIZATION, authorization)
        .send().await.map_err(|_| "unavailable")?;
    if matches!(response.status().as_u16(), 401 | 403) { return Err("auth_required"); }
    if !response.status().is_success() || response.content_length().is_some_and(|n| n > RESPONSE_LIMIT as u64) { return Err("unavailable"); }
    let mut body = vec![];
    while let Some(chunk) = response.chunk().await.map_err(|_| "unavailable")? {
        if body.len() + chunk.len() > RESPONSE_LIMIT { return Err("unavailable"); }
        body.extend_from_slice(&chunk);
    }
    let value = serde_json::from_slice(&body).map_err(|_| "unavailable")?;
    parse_response(&value, label)
}

struct Cached {
    identity: Vec<u8>,
    checked: Instant,
    quota: AgentQuota,
}

fn failed_refresh(previous: Option<&AgentQuota>, status: &str) -> AgentQuota {
    let mut quota = previous.cloned().unwrap_or_else(|| unavailable(status));
    // Preserve the original observation time so failures never make stale data fresh.
    quota.status = Some(status.into());
    quota
}

pub(super) async fn read_quota() -> Option<AgentQuota> {
    static CACHE: Mutex<Option<Cached>> = Mutex::const_new(None);
    let home = std::env::var_os("USERPROFILE").or_else(|| std::env::var_os("HOME"))?;
    let path = PathBuf::from(home).join(".zcode/v2/config.json");
    if !path.exists() { *CACHE.lock().await = None; return None; }
    let config = tokio::task::spawn_blocking(move || {
        use std::io::Read;
        let file = std::fs::File::open(path).ok()?;
        let mut bytes = vec![];
        file.take(CONFIG_LIMIT + 1).read_to_end(&mut bytes).ok()?;
        if bytes.len() as u64 > CONFIG_LIMIT { return None; }
        serde_json::from_slice::<Value>(&bytes).ok()
    }).await.ok().flatten();
    let mut cache = CACHE.lock().await;
    let Some(config) = config else { *cache = None; return Some(unavailable("unavailable")); };
    let auth = credentials(&config);
    if auth.is_empty() { *cache = None; return Some(unavailable("not_connected")); }
    let mut hash = Sha256::new();
    for item in &auth { hash.update(item.host); hash.update([0]); hash.update(item.authorization.as_bytes()); hash.update([0]); }
    let identity = hash.finalize().to_vec();
    // Do not show an old account's quota after a key/account/provider changes.
    if cache.as_ref().is_some_and(|old| old.identity != identity) { *cache = None; }
    if let Some(old) = cache.as_ref().filter(|old| old.checked.elapsed() < REFRESH) { return Some(old.quota.clone()); }
    let client = Client::builder().redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(8)).connect_timeout(Duration::from_secs(4)).build().ok()?;
    let mut results = vec![];
    let mut error = None;
    for item in auth {
        let url = format!("https://{}/api/monitor/usage/quota/limit", item.host);
        match fetch(&client, &url, item.authorization, item.label).await {
            Ok(quota) => results.push(quota),
            Err(status) => { error = Some(status); }
        }
    }
    // All-or-nothing refresh prevents one provider's old windows from being
    // accidentally stamped with another provider's successful observation time.
    let quota = if let Some(status) = error {
        failed_refresh(cache.as_ref().map(|old| &old.quota), status)
    } else {
        let mut combined = unavailable("ready");
        combined.plan = Some(results.iter().filter_map(|q| q.plan.clone()).collect::<Vec<_>>().join(" / "));
        for result in results { combined.windows.extend(result.windows); }
        combined
    };
    *cache = Some(Cached { identity, checked: Instant::now(), quota: quota.clone() });
    Some(quota)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[tokio::test]
    #[ignore = "explicit local-account read-only release verification"]
    async fn live_zcode_quota() {
        assert_eq!(std::env::var("TOKENDANCE_VERIFY_ZCODE_QUOTA").as_deref(), Ok("1"));
        let quota = read_quota().await.expect("local ZCode configuration");
        assert_eq!(quota.status.as_deref(), Some("ready"));
        assert!(!quota.windows.is_empty());
        // AgentQuota contains only safe display fields; never credentials.
        println!("{}", serde_json::to_string(&quota).unwrap());
    }

    fn fixture() -> Value {
        json!({"code":200,"success":true,"data":{"level":"pro","limits":[
            {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":3,"nextResetTime":1788667260268i64},
            {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":52,"nextResetTime":1788832902994i64},
            {"type":"TIME_LIMIT","unit":5,"number":1,"percentage":5}
        ]}})
    }

    #[test]
    fn normalizes_provider_used_percent_and_reset_units() {
        let quota = parse_response(&fixture(), "GLM").unwrap();
        assert_eq!(quota.plan.as_deref(), Some("GLM Pro"));
        assert_eq!(quota.windows.len(), 2);
        assert_eq!(quota.windows[0].used_percent, 3.0);
        assert_eq!(quota.windows[0].window_minutes, 300);
        assert_eq!(quota.windows[0].resets_at, Some(1788667260));
        assert_eq!(quota.windows[1].window_minutes, 10080);
        assert_eq!(quota.windows[1].used_percent, 52.0);
    }

    #[test]
    fn malformed_and_failed_responses_never_become_zero_quota() {
        for value in [json!({"code":401}), json!({"success":false,"data":fixture()["data"]}), json!({"data":{"limits":[]}})] {
            assert!(parse_response(&value, "GLM").is_err());
        }
        let mut value = fixture();
        value["data"]["limits"][0]["percentage"] = json!(101);
        value["data"]["limits"][1]["unit"] = json!(999);
        assert!(parse_response(&value, "GLM").is_err());
    }

    #[test]
    fn only_enabled_official_personal_providers_supply_credentials() {
        let mut value = json!({"provider":{"builtin:bigmodel-coding-plan":{"enabled":true,"options":{"baseURL":"https://open.bigmodel.cn/api/anthropic","apiKey":"Bearer test-key-only"}}}});
        assert_eq!(credentials(&value).len(), 1);
        assert!(credentials(&value)[0].authorization.is_sensitive());
        for url in ["http://open.bigmodel.cn", "https://evil.test", "https://open.bigmodel.cn.evil.test", "https://user@open.bigmodel.cn", "https://open.bigmodel.cn:444"] {
            value["provider"]["builtin:bigmodel-coding-plan"]["options"]["baseURL"] = json!(url);
            assert!(credentials(&value).is_empty());
        }
    }

    #[test]
    fn failure_keeps_last_observation_and_marks_it_stale() {
        let old = parse_response(&fixture(), "GLM").unwrap();
        let failed = failed_refresh(Some(&old), "auth_required");
        assert_eq!(failed.observed_at, old.observed_at);
        assert_eq!(failed.windows[0].used_percent, 3.0);
        assert_eq!(failed.status.as_deref(), Some("auth_required"));
    }

    #[tokio::test]
    async fn quota_request_does_not_follow_redirects_with_credentials() {
        use tokio::{io::{AsyncReadExt, AsyncWriteExt}, net::TcpListener};
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}/quota", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut buf = [0; 2048];
            let n = stream.read(&mut buf).await.unwrap();
            assert!(String::from_utf8_lossy(&buf[..n]).contains("test-key-only"));
            stream.write_all(b"HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/leak\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").await.unwrap();
        });
        let client = Client::builder().redirect(reqwest::redirect::Policy::none()).build().unwrap();
        assert!(fetch(&client, &url, HeaderValue::from_static("test-key-only"), "GLM").await.is_err());
        server.await.unwrap();
    }
}
