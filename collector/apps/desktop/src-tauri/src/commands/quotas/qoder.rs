use super::{AgentQuota, QuotaWindow};
use platform_credentials::DESKTOP_SERVICE;
use reqwest::{header::HeaderValue, Client};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::time::{Duration, Instant};
use tokio::sync::Mutex;

const RESPONSE_LIMIT: usize = 256 * 1024;
const REFRESH: Duration = Duration::from_secs(300);
const COOKIE_LIMIT: usize = 8 * 1024;
const ENDPOINT: &str = "/api/v2/me/usages/big_model_credits";
const HOSTS: [&str; 2] = ["qoder.com", "qoder.com.cn"];
const COOKIE_ACCOUNT: &str = "qoder-quota-cookie";
// A snapshot cannot reveal the full cycle length; Qoder credits reset on a
// monthly boundary, so pin the canonical 30-day bucket instead of inventing
// a period from the remaining time.
const MONTHLY_MINUTES: u64 = 30 * 24 * 60;

// Never serialize or log the credential. Only normalized quota data crosses IPC.
enum Auth {
    Bearer(HeaderValue),
    Cookie(HeaderValue),
}

struct Candidate {
    host: &'static str,
    auth: Auth,
}

fn static_host(host: &str) -> Option<&'static str> {
    HOSTS.iter().copied().find(|allowed| *allowed == host)
}

#[derive(Serialize, Deserialize)]
struct StoredCookie {
    version: u32,
    host: String,
    cookie: String,
}

/// Build the credential candidates: a personal access token (preferred, zero
/// input) covers both regional hosts, then a manually pasted browser cookie
/// stays bound to the single host it was captured on.
fn build_candidates(token: Option<&str>, stored: Option<&StoredCookie>) -> Vec<Candidate> {
    let mut candidates = vec![];
    if let Some(token) = token.map(str::trim).filter(|token| (8..=4096).contains(&token.len())) {
        if let Ok(mut value) = HeaderValue::from_str(&format!("Bearer {token}")) {
            value.set_sensitive(true);
            for host in HOSTS {
                candidates.push(Candidate { host, auth: Auth::Bearer(value.clone()) });
            }
        }
    }
    if let Some(stored) = stored.filter(|stored| stored.cookie.len() <= COOKIE_LIMIT) {
        if let Some(host) = static_host(&stored.host) {
            if let Ok(mut value) = HeaderValue::from_str(stored.cookie.trim()) {
                value.set_sensitive(true);
                candidates.push(Candidate { host, auth: Auth::Cookie(value) });
            }
        }
    }
    candidates
}

fn read_stored_cookie() -> Option<StoredCookie> {
    let secret = platform_credentials::get(DESKTOP_SERVICE, COOKIE_ACCOUNT).ok()?;
    let stored: StoredCookie = serde_json::from_str(&secret).ok()?;
    (stored.version == 1 && static_host(&stored.host).is_some()).then_some(stored)
}

/// Validate a pasted Cookie header value. HeaderValue rejects control bytes
/// later; reject early with actionable errors so the settings card can show them.
fn validate_pasted_cookie(input: &str) -> Result<String, String> {
    let cookie = input.trim();
    if cookie.is_empty() { return Err("is empty".into()); }
    if cookie.len() > COOKIE_LIMIT { return Err("is too long".into()); }
    if !cookie.is_ascii() { return Err("must be ASCII".into()); }
    if cookie.chars().any(char::is_control) { return Err("must not contain control characters".into()); }
    if !cookie.contains('=') { return Err("must look like a Cookie header, e.g. k1=v1; k2=v2".into()); }
    Ok(cookie.to_string())
}

fn empty_quota(status: &str) -> AgentQuota {
    AgentQuota { agent_id: "qoder".into(), observed_at: chrono::Utc::now().to_rfc3339(),
        plan: None, windows: vec![], status: Some(status.into()) }
}

/// Read `camelCase` or `snake_case`, treating null as absent.
fn field<'a>(row: &'a Value, camel: &str, snake: &str) -> Option<&'a Value> {
    row.get(camel).filter(|value| !value.is_null()).or_else(|| row.get(snake).filter(|value| !value.is_null()))
}

fn quota_summary<'a>(root: &'a Value, camel: &str, snake: &str) -> Option<&'a Value> {
    field(root, camel, snake).and_then(|quota| field(quota, "quotaSummary", "quota_summary"))
}

fn finite(row: &Value, camel: &str, snake: &str, min: f64) -> Option<f64> {
    field(row, camel, snake).and_then(Value::as_f64).filter(|value| value.is_finite() && *value >= min)
}

/// Qoder reports epoch resets as seconds or milliseconds; ISO strings appear
/// in some deployments. Normalize to epoch seconds.
fn normalize_reset(value: &Value) -> Option<i64> {
    if let Some(number) = value.as_f64().filter(|number| number.is_finite() && *number > 0.0) {
        let seconds = if number >= 1e10 { number / 1000.0 } else { number };
        return (seconds >= 1.0).then_some(seconds as i64);
    }
    chrono::DateTime::parse_from_rfc3339(value.as_str()?).ok()
        .map(|time| time.timestamp()).filter(|seconds| *seconds > 0)
}

fn parse_response(value: &Value) -> Result<AgentQuota, &'static str> {
    if value["success"] == false || value.get("code").is_some_and(|code| code != 0 && code != 200) {
        return Err(if matches!(value["code"].as_i64(), Some(401 | 403)) { "auth_required" } else { "unavailable" });
    }
    let base = quota_summary(value, "totalQuota", "total_quota").ok_or("no_quota")?;
    let mut used = finite(base, "usedValue", "used_value", 0.0);
    let mut total = finite(base, "limitValue", "limit_value", 0.0).filter(|total| *total > 0.0);
    let mut percent = finite(base, "usagePercentage", "usage_percentage", 0.0).filter(|p| *p <= 100.0);
    // Shared team credits ride the same monthly cycle; merge additively
    // (CodexBar's reading) instead of showing two rings for one cycle.
    if let Some(shared) = quota_summary(value, "sharedQuota", "shared_quota") {
        if let (Some(shared_used), Some(shared_total)) = (
            finite(shared, "usedValue", "used_value", 0.0),
            finite(shared, "limitValue", "limit_value", 0.0).filter(|total| *total > 0.0),
        ) {
            used = used.map(|used| used + shared_used);
            total = total.map(|total| total + shared_total);
            // The base percentage no longer describes the merged pool.
            percent = None;
        }
    }
    let used_percent = match (percent, used, total) {
        (Some(percent), _, _) => percent,
        (None, Some(used), Some(total)) => used / total * 100.0,
        _ => return Err("no_quota"),
    };
    if !used_percent.is_finite() || !(0.0..=100.0).contains(&used_percent) { return Err("no_quota"); }
    let resets_at = field(value, "nextResetAt", "next_reset_at").and_then(normalize_reset);
    Ok(AgentQuota { agent_id: "qoder".into(), observed_at: chrono::Utc::now().to_rfc3339(), plan: None,
        windows: vec![QuotaWindow { used_percent, window_minutes: MONTHLY_MINUTES, resets_at,
            provider: None, label: Some("credits".into()) }],
        status: Some("ready".into()) })
}

async fn fetch(client: &Client, url: &str, auth: &Auth) -> Result<AgentQuota, &'static str> {
    let mut request = client.get(url);
    request = match auth {
        Auth::Bearer(value) => request.header(reqwest::header::AUTHORIZATION, value),
        Auth::Cookie(value) => request.header(reqwest::header::COOKIE, value),
    };
    let mut response = request.send().await.map_err(|_| "network_error")?;
    if matches!(response.status().as_u16(), 401 | 403) { return Err("auth_required"); }
    if !response.status().is_success() || response.content_length().is_some_and(|n| n > RESPONSE_LIMIT as u64) { return Err("unavailable"); }
    let mut body = vec![];
    while let Some(chunk) = response.chunk().await.map_err(|_| "network_error")? {
        if body.len() + chunk.len() > RESPONSE_LIMIT { return Err("unavailable"); }
        body.extend_from_slice(&chunk);
    }
    let value = serde_json::from_slice(&body).map_err(|_| "unavailable")?;
    parse_response(&value)
}

struct Cached {
    identity: Vec<u8>,
    checked: Instant,
    quota: AgentQuota,
}

static CACHE: Mutex<Option<Cached>> = Mutex::const_new(None);

fn failed_refresh(previous: Option<&AgentQuota>, status: &str) -> AgentQuota {
    let mut quota = previous.cloned().unwrap_or_else(|| empty_quota(status));
    // Preserve the original observation time so failures never make stale data fresh.
    quota.status = Some(status.into());
    quota
}

/// The most actionable failure wins: a bad credential tells the user to paste
/// a fresh cookie, while a generic failure only says "retry later".
fn worst_status(current: Option<&'static str>, next: &'static str) -> &'static str {
    let rank = |status: &str| match status {
        "auth_required" => 0, "network_error" => 1, "no_quota" => 2, _ => 3,
    };
    current.map_or(next, |current| if rank(current) <= rank(next) { current } else { next })
}

pub(super) async fn read_quota() -> Option<AgentQuota> {
    let token = std::env::var("QODER_PERSONAL_ACCESS_TOKEN").ok();
    let stored = tokio::task::spawn_blocking(read_stored_cookie).await.ok().flatten();
    let candidates = build_candidates(token.as_deref(), stored.as_ref());
    if candidates.is_empty() { *CACHE.lock().await = None; return None; }
    let mut hash = Sha256::new();
    for candidate in &candidates {
        let (kind, value) = match &candidate.auth {
            Auth::Bearer(value) => (b'b', value.as_bytes()),
            Auth::Cookie(value) => (b'c', value.as_bytes()),
        };
        hash.update([kind]); hash.update(candidate.host); hash.update([0]); hash.update(value); hash.update([0]);
    }
    let identity = hash.finalize().to_vec();
    let mut cache = CACHE.lock().await;
    // Do not show an old account's quota after the token or cookie changes.
    if cache.as_ref().is_some_and(|old| old.identity != identity) { *cache = None; }
    if let Some(old) = cache.as_ref().filter(|old| old.checked.elapsed() < REFRESH) { return Some(old.quota.clone()); }
    let client = Client::builder().redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(8)).connect_timeout(Duration::from_secs(4)).build().ok()?;
    let mut worst: Option<&'static str> = None;
    // First success wins, so a dead personal token falls through to the cookie.
    for candidate in &candidates {
        let url = format!("https://{}{}", candidate.host, ENDPOINT);
        match fetch(&client, &url, &candidate.auth).await {
            Ok(quota) => {
                *cache = Some(Cached { identity, checked: Instant::now(), quota: quota.clone() });
                return Some(quota);
            }
            Err(status) => worst = Some(worst_status(worst, status)),
        }
    }
    let quota = failed_refresh(cache.as_ref().map(|old| &old.quota), worst.unwrap_or("unavailable"));
    *cache = Some(Cached { identity, checked: Instant::now(), quota: quota.clone() });
    Some(quota)
}

/// Credential presence for the settings card. The cookie value itself never
/// crosses IPC after being stored.
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QoderQuotaCredential {
    personal_token: bool,
    saved_cookie_host: Option<String>,
}

async fn credential_status() -> QoderQuotaCredential {
    let personal_token = std::env::var("QODER_PERSONAL_ACCESS_TOKEN")
        .map(|token| (8..=4096).contains(&token.trim().len())).unwrap_or(false);
    let saved_cookie_host = tokio::task::spawn_blocking(|| {
        read_stored_cookie().and_then(|stored| static_host(&stored.host).map(str::to_string))
    }).await.ok().flatten();
    QoderQuotaCredential { personal_token, saved_cookie_host }
}

#[tauri::command]
pub async fn get_qoder_quota_credential() -> Result<QoderQuotaCredential, String> {
    Ok(credential_status().await)
}

#[tauri::command]
pub async fn set_qoder_quota_cookie(cookie: String, host: String) -> Result<QoderQuotaCredential, String> {
    let cookie = validate_pasted_cookie(&cookie)?;
    let Some(host) = static_host(&host) else { return Err("unsupported host".into()) };
    let secret = serde_json::to_string(&StoredCookie { version: 1, host: host.into(), cookie }).map_err(|_| "storage error".to_string())?;
    let stored = secret.clone();
    tokio::task::spawn_blocking(move || -> Result<(), String> {
        platform_credentials::put(DESKTOP_SERVICE, COOKIE_ACCOUNT, &stored).map_err(|error| error.to_string())?;
        match platform_credentials::get(DESKTOP_SERVICE, COOKIE_ACCOUNT) {
            Ok(readback) if readback == stored => Ok(()),
            _ => Err("credential readback failed".into()),
        }
    }).await.map_err(|error| error.to_string())??;
    *CACHE.lock().await = None;
    Ok(credential_status().await)
}

#[tauri::command]
pub async fn clear_qoder_quota_cookie() -> Result<QoderQuotaCredential, String> {
    let _ = tokio::task::spawn_blocking(|| platform_credentials::delete(DESKTOP_SERVICE, COOKIE_ACCOUNT)).await;
    *CACHE.lock().await = None;
    Ok(credential_status().await)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[tokio::test]
    #[ignore = "explicit credential-based read-only release verification"]
    async fn live_qoder_quota() {
        assert_eq!(std::env::var("TOKENDANCE_VERIFY_QODER_QUOTA").as_deref(), Ok("1"));
        match read_quota().await {
            Some(quota) => println!("{}", serde_json::to_string(&quota).unwrap()),
            None => eprintln!("no qoder credential configured"),
        }
    }

    fn fixture() -> Value {
        json!({"totalQuota":{"quotaSummary":{"usedValue":120.5,"limitValue":2000,
            "remainingValue":1879.5,"usagePercentage":6.03,"unit":"credit"}},
            "nextResetAt":1788667260268i64})
    }

    #[test]
    fn reads_camel_case_and_prefers_reported_percentage() {
        let quota = parse_response(&fixture()).unwrap();
        assert_eq!(quota.status.as_deref(), Some("ready"));
        assert_eq!(quota.windows.len(), 1);
        assert_eq!(quota.windows[0].used_percent, 6.03);
        assert_eq!(quota.windows[0].window_minutes, MONTHLY_MINUTES);
        assert_eq!(quota.windows[0].resets_at, Some(1788667260));
        assert_eq!(quota.windows[0].label.as_deref(), Some("credits"));
        assert_eq!(quota.plan, None);
    }

    #[test]
    fn reads_snake_case_and_computes_from_used_and_limit() {
        let value = json!({"total_quota":{"quota_summary":{"used_value":500,"limit_value":1000}},
            "next_reset_at":"2026-10-01T00:00:00Z"});
        let quota = parse_response(&value).unwrap();
        assert_eq!(quota.windows[0].used_percent, 50.0);
        assert_eq!(quota.windows[0].resets_at,
            Some(chrono::DateTime::parse_from_rfc3339("2026-10-01T00:00:00Z").unwrap().timestamp()));
    }

    #[test]
    fn shared_quota_merges_additively_and_recomputes_percentage() {
        let mut value = fixture();
        value["totalQuota"]["quotaSummary"]["usedValue"] = json!(100);
        value["totalQuota"]["quotaSummary"]["limitValue"] = json!(200);
        value["totalQuota"]["quotaSummary"]["usagePercentage"] = json!(50.0);
        value["sharedQuota"] = json!({"quotaSummary":{"used_value":50,"limit_value":300}});
        let quota = parse_response(&value).unwrap();
        // (100+50)/(200+300) = 30%, not the stale base 50%.
        assert!((quota.windows[0].used_percent - 30.0).abs() < 1e-9);
    }

    #[test]
    fn shared_quota_with_partial_fields_is_ignored() {
        let mut value = fixture();
        value["sharedQuota"] = json!({"quotaSummary":{"used_value":50}});
        let quota = parse_response(&value).unwrap();
        assert_eq!(quota.windows[0].used_percent, 6.03);
    }

    #[test]
    fn malformed_and_failed_responses_never_become_quota() {
        for value in [
            json!({}),
            json!({"totalQuota": {}}),
            json!({"totalQuota": {"quotaSummary": {"limitValue": 100}}}),
            json!({"success": false, "data": fixture()["totalQuota"]}),
            json!({"code": 500, "totalQuota": fixture()["totalQuota"]}),
            json!({"totalQuota": {"quotaSummary": {"usedValue": 10, "limitValue": 0, "usagePercentage": 101}}}),
        ] {
            assert!(parse_response(&value).is_err(), "should reject {value}");
        }
        let mut unauthorized = json!({"code": 401});
        assert_eq!(parse_response(&unauthorized).unwrap_err(), "auth_required");
        unauthorized["success"] = json!(false);
        assert_eq!(parse_response(&unauthorized).unwrap_err(), "auth_required");
    }

    #[test]
    fn reset_values_normalize_across_units() {
        for (input, expected) in [
            (json!(1788667260268i64), Some(1788667260)),
            (json!(1788667260i64), Some(1788667260)),
            (json!(0), None),
            (json!(-5), None),
            (json!("2026-10-01T00:00:00Z"), Some(1_790_812_800)),
            (json!("not a date"), None),
        ] {
            assert_eq!(normalize_reset(&input), expected, "for {input}");
        }
    }

    #[test]
    fn pasted_cookies_must_look_like_cookie_headers() {
        assert!(validate_pasted_cookie("  sid=abc; csrf=xyz ").is_ok());
        for bad in [String::new(), "   ".into(), "no-equals-sign".into(), "sid=abc\nx".into(), "k=\u{e9}".into(), "x".repeat(COOKIE_LIMIT + 1)] {
            assert!(validate_pasted_cookie(&bad).is_err(), "should reject {bad:?}");
        }
    }

    #[test]
    fn candidates_prefer_token_then_host_bound_cookie() {
        let stored = StoredCookie { version: 1, host: "qoder.com.cn".into(), cookie: "sid=abc".into() };
        let candidates = build_candidates(Some("test-token-only"), Some(&stored));
        assert_eq!(candidates.len(), 3);
        assert_eq!(candidates[0].host, "qoder.com");
        assert_eq!(candidates[1].host, "qoder.com.cn");
        assert_eq!(candidates[2].host, "qoder.com.cn");
        for item in &candidates {
            let value = match &item.auth {
                Auth::Bearer(value) => value,
                Auth::Cookie(value) => value,
            };
            assert!(value.is_sensitive());
        }
        // Unknown hosts and malformed tokens contribute nothing.
        let orphan = StoredCookie { version: 1, host: "evil.test".into(), cookie: "sid=abc".into() };
        assert!(build_candidates(None, Some(&orphan)).is_empty());
        assert!(build_candidates(Some("short"), None).is_empty());
    }

    #[test]
    fn failure_keeps_last_observation_and_marks_it_stale() {
        let old = parse_response(&fixture()).unwrap();
        let failed = failed_refresh(Some(&old), "auth_required");
        assert_eq!(failed.observed_at, old.observed_at);
        assert_eq!(failed.windows[0].used_percent, 6.03);
        assert_eq!(failed.status.as_deref(), Some("auth_required"));
    }

    #[test]
    fn worst_status_ranks_actionable_errors_first() {
        assert_eq!(worst_status(None, "network_error"), "network_error");
        assert_eq!(worst_status(Some("unavailable"), "auth_required"), "auth_required");
        assert_eq!(worst_status(Some("auth_required"), "network_error"), "auth_required");
        assert_eq!(worst_status(Some("network_error"), "no_quota"), "network_error");
    }

    #[tokio::test]
    async fn quota_timeout_is_a_network_error() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let client = Client::builder().no_proxy().timeout(Duration::from_millis(100)).build().unwrap();
        let url = format!("http://{}/quota", listener.local_addr().unwrap());
        let auth = Auth::Cookie(HeaderValue::from_static("sid=test-cookie-only"));
        assert_eq!(fetch(&client, &url, &auth).await.unwrap_err(), "network_error");
        drop(listener);
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
            assert!(String::from_utf8_lossy(&buf[..n]).contains("test-cookie-only"));
            stream.write_all(b"HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/leak\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").await.unwrap();
        });
        let client = Client::builder().redirect(reqwest::redirect::Policy::none()).build().unwrap();
        let auth = Auth::Cookie(HeaderValue::from_static("sid=test-cookie-only"));
        assert!(fetch(&client, &url, &auth).await.is_err());
        server.await.unwrap();
    }
}
