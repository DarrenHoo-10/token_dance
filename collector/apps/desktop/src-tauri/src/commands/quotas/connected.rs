//! Read-only queries using the clients' own sessions. Never serialize credentials,
//! refresh tokens, account identifiers, raw responses, or request errors to IPC.
use super::{AgentQuota, QuotaWindow};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use reqwest::{
    header::{HeaderMap, HeaderValue, AUTHORIZATION, ORIGIN, REFERER},
    Client,
};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{
    io::Read,
    path::{Path, PathBuf},
    time::{Duration, Instant},
};
use tokio::sync::Mutex;

const LIMIT: usize = 256 * 1024;
const REFRESH: Duration = Duration::from_secs(300);
const GROK_BILLING: &str = "https://cli-chat-proxy.grok.com/v1/billing?format=credits";
const GROK_SETTINGS: &str = "https://cli-chat-proxy.grok.com/v1/settings";
const CURSOR_USAGE: &str =
    "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage";
const CURSOR_PLAN: &str = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetPlanInfo";
const CODEX_USAGE: &str = "https://chatgpt.com/backend-api/wham/usage";

#[derive(Clone, Copy)]
enum Source {
    Grok,
    Cursor,
    Codex,
}
impl Source {
    fn id(self) -> &'static str {
        match self {
            Self::Grok => "grok-build",
            Self::Cursor => "cursor",
            Self::Codex => "codex",
        }
    }
}
struct Credential {
    headers: HeaderMap,
    identity: Vec<u8>,
}
struct Cached {
    identity: Vec<u8>,
    checked: Instant,
    quota: AgentQuota,
}

fn empty(source: Source, status: &str) -> AgentQuota {
    AgentQuota {
        agent_id: source.id().into(),
        observed_at: chrono::Utc::now().to_rfc3339(),
        plan: None,
        windows: vec![],
        status: Some(status.into()),
    }
}
fn timestamp(value: &Value) -> Option<i64> {
    chrono::DateTime::parse_from_rfc3339(value.as_str()?)
        .ok()
        .map(|date| date.timestamp())
}
fn percent(value: &Value) -> Option<f64> {
    value.as_f64().filter(|v| v.is_finite() && *v >= 0.0)
}
fn window(value: f64, minutes: u64, reset: Option<i64>, label: &str) -> QuotaWindow {
    QuotaWindow {
        used_percent: value,
        window_minutes: minutes,
        resets_at: reset,
        provider: None,
        label: Some(label.into()),
    }
}
fn period(start: &Value, end: &Value) -> u64 {
    timestamp(start)
        .zip(timestamp(end))
        .and_then(|(start, end)| {
            let minutes = (end - start) / 60;
            (1..=527040).contains(&minutes).then_some(minutes as u64)
        })
        .unwrap_or(0)
}

fn parse_grok(value: &Value) -> Result<AgentQuota, &'static str> {
    let config = &value["config"];
    // On-demand spend is a different allowance: never substitute it for the
    // subscription's shared percentage when that field is absent.
    let used = percent(&config["creditUsagePercent"]).ok_or("no_quota")?;
    let weekly = config["currentPeriod"]["type"] == "USAGE_PERIOD_TYPE_WEEKLY";
    let reset = timestamp(&config["currentPeriod"]["end"])
        .or_else(|| timestamp(&config["billingPeriodEnd"]));
    let minutes = if weekly {
        10080
    } else {
        period(
            &config["currentPeriod"]["start"],
            &config["currentPeriod"]["end"],
        )
    };
    let mut quota = empty(Source::Grok, "ready");
    quota.windows.push(window(
        used,
        minutes,
        reset,
        if weekly {
            "shared_week"
        } else {
            "shared_quota"
        },
    ));
    Ok(quota)
}
fn parse_cursor(value: &Value) -> Result<AgentQuota, &'static str> {
    let mut quota = empty(Source::Cursor, "ready");
    quota.plan = match value["membershipType"].as_str().unwrap_or("") {
        "ultra" => Some("Ultra"),
        "pro" => Some("Pro"),
        "pro-plus" => Some("Pro+"),
        "free" => Some("Free"),
        "enterprise" => Some("Enterprise"),
        "team" => Some("Teams"),
        _ => None,
    }
    .map(str::to_owned);
    let minutes = period(&value["billingCycleStart"], &value["billingCycleEnd"]);
    let reset = timestamp(&value["billingCycleEnd"]);
    let plan = &value["individualUsage"]["plan"];
    if plan["enabled"] != false {
        for (key, label) in [("autoPercentUsed", "auto"), ("apiPercentUsed", "api")] {
            if let Some(used) = percent(&plan[key]) {
                quota.windows.push(window(used, minutes, reset, label));
            }
        }
        if quota.windows.is_empty() {
            let used = percent(&plan["totalPercentUsed"]).or_else(|| ratio(plan));
            if let Some(used) = used {
                quota.windows.push(window(used, minutes, reset, "plan"));
            }
        }
    }
    let overall = &value["individualUsage"]["overall"];
    if overall["enabled"] != false {
        if let Some(used) = ratio(overall) {
            quota
                .windows
                .push(window(used, minutes, reset, "personal_limit"));
        }
    }
    if quota.windows.is_empty() {
        return Err(if value["isUnlimited"] == true {
            "unlimited"
        } else {
            "no_quota"
        });
    }
    Ok(quota)
}
fn ratio(value: &Value) -> Option<f64> {
    let used = percent(&value["used"])?;
    let limit = percent(&value["limit"]).filter(|n| *n > 0.0)?;
    let percent = used / limit * 100.0;
    percent.is_finite().then_some(percent)
}
fn rpc_date(value: &Value) -> Value {
    let millis = value
        .as_str()
        .and_then(|s| s.parse::<i64>().ok())
        .or_else(|| value.as_i64());
    millis
        .and_then(chrono::DateTime::from_timestamp_millis)
        .map(|date| Value::String(date.to_rfc3339()))
        .unwrap_or(Value::Null)
}
fn parse_cursor_rpc(value: &Value, plan: Option<&Value>) -> Result<AgentQuota, &'static str> {
    let usage = &value["planUsage"];
    let spending = &value["spendLimitUsage"];
    // Connect JSON omits zero-valued non-optional protobuf scalars. A missing
    // limit still stays unknown; only default the documented spend scalar.
    let mut normalized = serde_json::json!({
        "billingCycleStart":rpc_date(&value["billingCycleStart"]), "billingCycleEnd":rpc_date(&value["billingCycleEnd"]),
        "membershipType":plan.and_then(|p| p["planInfo"]["planName"].as_str()).unwrap_or("").to_ascii_lowercase(),
        "individualUsage": {
            "plan": {"enabled":value["enabled"], "used":usage.get("includedSpend").cloned().unwrap_or(Value::from(0)),
                "limit":usage["limit"], "autoPercentUsed":usage["autoPercentUsed"], "apiPercentUsed":usage["apiPercentUsed"],
                "totalPercentUsed":usage["totalPercentUsed"]},
            "overall":{"used":spending["overallUsed"],"limit":spending["overallLimit"]}
        }
    });
    if usage.is_null() {
        normalized["individualUsage"]["plan"] = Value::Null;
    }
    parse_cursor(&normalized)
}

fn json_number(value: &Value) -> Option<f64> {
    value
        .as_f64()
        .or_else(|| value.as_i64().map(|n| n as f64))
        .or_else(|| value.as_u64().map(|n| n as f64))
        .filter(|n| n.is_finite())
}

fn jwt_payload(token: &str) -> Option<Value> {
    let parts = token.split('.').collect::<Vec<_>>();
    if parts.len() != 3 {
        return None;
    }
    let bytes = URL_SAFE_NO_PAD.decode(parts[1]).ok()?;
    serde_json::from_slice(&bytes).ok()
}

fn limit_id(value: &Value) -> Option<&str> {
    value
        .get("limit_id")
        .or_else(|| value.get("limitId"))
        .and_then(Value::as_str)
}

fn codex_limits(value: &Value) -> Option<&Value> {
    let by_id = value
        .get("rateLimitsByLimitId")
        .or_else(|| value.get("rate_limits_by_limit_id"));
    if let Some(codex) = by_id.and_then(|map| map.get("codex")) {
        return Some(codex);
    }
    let limits = value
        .get("rateLimits")
        .or_else(|| value.get("rate_limits"))
        .or_else(|| value.get("rate_limit"))
        .unwrap_or(value);
    if let Some(nested) = limits.get("codex").filter(|item| item.is_object()) {
        return match limit_id(nested) {
            None | Some("codex") => Some(nested),
            Some(_) => None,
        };
    }
    match limit_id(limits) {
        None | Some("codex") => Some(limits),
        Some(_) => None,
    }
}

fn codex_slot<'a>(limits: &'a Value, slot: &str) -> Option<&'a Value> {
    let names: &[&str] = match slot {
        "primary" => &["primary", "primary_window", "primaryWindow"],
        "secondary" => &["secondary", "secondary_window", "secondaryWindow"],
        _ => return None,
    };
    names
        .iter()
        .find_map(|name| limits.get(*name))
        .filter(|value| value.is_object())
}

fn first_number(value: &Value, keys: &[&str]) -> Option<f64> {
    keys.iter()
        .find_map(|key| value.get(*key).and_then(json_number))
}

fn unix_seconds(value: &Value) -> Option<i64> {
    let raw = value
        .as_i64()
        .or_else(|| json_number(value).map(|n| n as i64))
        .or_else(|| timestamp(value))?;
    // Live WHAM uses seconds; some camelCase payloads send milliseconds.
    Some(if raw > 10_000_000_000 { raw / 1000 } else { raw })
}

fn reset_at(window: &Value) -> Option<i64> {
    ["resets_at", "resetsAt", "reset_at", "resetAt"]
        .into_iter()
        .find_map(|key| window.get(key).and_then(unix_seconds))
}

fn window_minutes(window: &Value) -> Option<u64> {
    if let Some(mins) = first_number(
        window,
        &[
            "window_minutes",
            "windowDurationMins",
            "window_duration_mins",
        ],
    )
    .filter(|n| *n > 0.0 && *n <= 527_040.0)
    {
        return Some(mins as u64);
    }
    let secs = first_number(
        window,
        &[
            "limit_window_seconds",
            "limitWindowSeconds",
            "window_duration_seconds",
            "windowDurationSeconds",
        ],
    )
    .filter(|n| *n > 0.0 && *n <= 527_040.0 * 60.0)?;
    let mins = (secs / 60.0).round();
    (mins > 0.0 && mins <= 527_040.0).then_some(mins as u64)
}

fn named_plan(value: &Value) -> Option<String> {
    match value
        .get("plan_type")
        .or_else(|| value.get("planType"))
        .and_then(Value::as_str)
        .unwrap_or("")
    {
        "plus" => Some("Plus"),
        "pro" | "prolite" => Some("Pro"),
        "team" => Some("Team"),
        "business" => Some("Business"),
        "enterprise" => Some("Enterprise"),
        "edu" => Some("Edu"),
        "free" => Some("Free"),
        "go" => Some("Go"),
        _ => None,
    }
    .map(str::to_owned)
}

fn credits_unlimited(value: &Value) -> bool {
    value
        .get("credits")
        .and_then(|credits| credits.get("unlimited"))
        .and_then(Value::as_bool)
        == Some(true)
}

fn parse_codex(value: &Value) -> Result<AgentQuota, &'static str> {
    let limits = codex_limits(value).ok_or("no_quota")?;
    if (credits_unlimited(limits) || credits_unlimited(value))
        && codex_slot(limits, "primary").is_none()
        && codex_slot(limits, "secondary").is_none()
    {
        return Err("unlimited");
    }
    let mut quota = empty(Source::Codex, "ready");
    quota.plan = named_plan(limits).or_else(|| named_plan(value));
    for key in ["primary", "secondary"] {
        let Some(window) = codex_slot(limits, key) else {
            continue;
        };
        let Some(used) = first_number(window, &["used_percent", "usedPercent"]) else {
            continue;
        };
        let Some(minutes) = window_minutes(window) else {
            continue;
        };
        if let Some(parsed) = super::decorate_codex_window(used, minutes, reset_at(window)) {
            quota.windows.push(parsed);
        }
    }
    if quota.windows.is_empty() {
        return Err("no_quota");
    }
    quota.windows.sort_by_key(|window| window.window_minutes);
    Ok(quota)
}

fn chatgpt_account_id(tokens: &Value) -> Option<String> {
    if let Some(id) = tokens["account_id"].as_str().filter(|id| !id.is_empty()) {
        return Some(id.to_string());
    }
    jwt_payload(tokens["id_token"].as_str()?).and_then(|payload| {
        payload["https://api.openai.com/auth"]["chatgpt_account_id"]
            .as_str()
            .filter(|id| !id.is_empty())
            .map(str::to_owned)
    })
}

fn account_header(value: &str) -> Result<HeaderValue, &'static str> {
    if value.len() > 256
        || !value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
    {
        return Err("auth_required");
    }
    secret_header(value)
}

fn token_expired(token: &str) -> bool {
    jwt_payload(token)
        .and_then(|payload| payload["exp"].as_i64())
        .is_some_and(|exp| exp <= chrono::Utc::now().timestamp())
}

fn codex_credential(value: &Value) -> Result<Credential, &'static str> {
    let tokens = value.get("tokens").ok_or("not_connected")?;
    let access = tokens["access_token"]
        .as_str()
        .filter(|token| !token.is_empty() && token.len() <= 32768)
        .ok_or("auth_required")?;
    if token_expired(access) {
        return Err("auth_required");
    }
    let mut headers = HeaderMap::new();
    headers.insert(AUTHORIZATION, secret_header(&format!("Bearer {access}"))?);
    if let Some(account) = chatgpt_account_id(tokens) {
        headers.insert("ChatGPT-Account-Id", account_header(&account)?);
    }
    Ok(Credential {
        headers,
        identity: Sha256::digest(access.as_bytes()).to_vec(),
    })
}

fn read_json(path: &Path) -> Result<Option<Value>, &'static str> {
    let file = match std::fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err("unavailable"),
    };
    let mut bytes = vec![];
    file.take((LIMIT + 1) as u64)
        .read_to_end(&mut bytes)
        .map_err(|_| "unavailable")?;
    if bytes.len() > LIMIT {
        return Err("unavailable");
    }
    serde_json::from_slice(&bytes)
        .map(Some)
        .map_err(|_| "auth_required")
}
fn secret_header(value: &str) -> Result<HeaderValue, &'static str> {
    if value.len() > 32768 {
        return Err("auth_required");
    }
    let mut header = HeaderValue::from_str(value).map_err(|_| "auth_required")?;
    header.set_sensitive(true);
    Ok(header)
}
fn grok_credential(value: &Value) -> Result<Credential, &'static str> {
    let entries = value.as_object().ok_or("auth_required")?;
    let row = entries
        .iter()
        .filter(|(scope, row)| {
            (scope.starts_with("https://auth.x.ai::")
                || scope.as_str() == "https://accounts.x.ai/sign-in")
                && row["key"].as_str().is_some_and(|s| !s.is_empty())
        })
        .max_by_key(|(scope, row)| {
            (
                scope.starts_with("https://auth.x.ai::"),
                timestamp(&row["create_time"]),
            )
        })
        .map(|(_, row)| row)
        .ok_or("auth_required")?;
    if row["oidc_issuer"]
        .as_str()
        .is_some_and(|s| s != "https://auth.x.ai")
    {
        return Err("auth_required");
    }
    if row
        .get("expires_at")
        .is_some_and(|date| timestamp(date).is_none_or(|t| t <= chrono::Utc::now().timestamp()))
    {
        return Err("auth_required");
    }
    let token = row["key"].as_str().ok_or("auth_required")?;
    let mut headers = HeaderMap::new();
    headers.insert(AUTHORIZATION, secret_header(&format!("Bearer {token}"))?);
    headers.insert("x-xai-token-auth", HeaderValue::from_static("xai-grok-cli"));
    Ok(Credential {
        headers,
        identity: Sha256::digest(token.as_bytes()).to_vec(),
    })
}
fn cursor_credential(token: &str) -> Result<Credential, &'static str> {
    if token.len() > 16384
        || !token
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
    {
        return Err("auth_required");
    }
    let parts = token.split('.').collect::<Vec<_>>();
    if parts.len() != 3 {
        return Err("auth_required");
    }
    let bytes = URL_SAFE_NO_PAD
        .decode(parts[1])
        .map_err(|_| "auth_required")?;
    let payload: Value = serde_json::from_slice(&bytes).map_err(|_| "auth_required")?;
    if payload["exp"]
        .as_i64()
        .is_none_or(|t| t <= chrono::Utc::now().timestamp())
    {
        return Err("auth_required");
    }
    let user = payload["sub"]
        .as_str()
        .and_then(|s| s.rsplit('|').next())
        .ok_or("auth_required")?;
    if user.is_empty()
        || !user
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
    {
        return Err("auth_required");
    }
    let mut headers = HeaderMap::new();
    headers.insert(AUTHORIZATION, secret_header(&format!("Bearer {token}"))?);
    Ok(Credential {
        headers,
        identity: Sha256::digest(token.as_bytes()).to_vec(),
    })
}
fn cursor_from_paths(cli: &Path, database: &Path) -> Result<Option<Credential>, &'static str> {
    // A present CLI login is authoritative; do not silently switch accounts
    // when that session expires. No refresh tokens are read or rewritten.
    if let Some(auth) = read_json(cli)? {
        return cursor_credential(auth["accessToken"].as_str().ok_or("auth_required")?).map(Some);
    }
    if !database.exists() {
        return Ok(None);
    }
    let db = rusqlite::Connection::open_with_flags(
        database,
        rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY | rusqlite::OpenFlags::SQLITE_OPEN_NO_MUTEX,
    )
    .map_err(|_| "unavailable")?;
    db.busy_timeout(Duration::from_millis(500))
        .map_err(|_| "unavailable")?;
    let token: String = db.query_row("SELECT CAST(value AS TEXT) FROM ItemTable WHERE key = 'cursorAuth/accessToken' AND length(value) <= 16384", [], |row| row.get(0)).map_err(|_| "auth_required")?;
    cursor_credential(&token).map(Some)
}
fn load(source: Source) -> Result<Option<Credential>, &'static str> {
    let home = std::env::var_os("USERPROFILE")
        .or_else(|| std::env::var_os("HOME"))
        .map(PathBuf::from)
        .ok_or("not_connected")?;
    match source {
        Source::Grok => {
            let base = std::env::var_os("GROK_HOME")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join(".grok"));
            read_json(&base.join("auth.json"))?
                .as_ref()
                .map(grok_credential)
                .transpose()
        }
        Source::Cursor => {
            #[cfg(target_os = "windows")]
            let base = std::env::var_os("APPDATA")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join("AppData/Roaming"));
            #[cfg(target_os = "macos")]
            let base = home.join("Library/Application Support");
            #[cfg(not(any(target_os = "windows", target_os = "macos")))]
            let base = std::env::var_os("XDG_CONFIG_HOME")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join(".config"));
            cursor_from_paths(
                &base.join("Cursor/auth.json"),
                &base.join("Cursor/User/globalStorage/state.vscdb"),
            )
        }
        Source::Codex => {
            let base = std::env::var_os("CODEX_HOME")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join(".codex"));
            match read_json(&base.join("auth.json"))? {
                None => Ok(None),
                Some(value) if value.get("tokens").is_none() => Ok(None),
                Some(value) => codex_credential(&value).map(Some),
            }
        }
    }
}

fn client() -> Result<Client, &'static str> {
    Client::builder()
        .http1_only()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(12))
        .connect_timeout(Duration::from_secs(5))
        .user_agent("TokenDance")
        .build()
        .map_err(|_| "unavailable")
}
async fn get(client: &Client, url: &str, headers: HeaderMap) -> Result<Value, &'static str> {
    receive(
        client
            .get(url)
            .headers(headers)
            .header("Accept", "application/json"),
    )
    .await
}
async fn rpc(client: &Client, url: &str, headers: HeaderMap) -> Result<Value, &'static str> {
    // The two fixed Get* RPCs query the caller's account; no team selection,
    // quota reset, spending changes, or other mutation is sent.
    receive(
        client
            .post(url)
            .headers(headers)
            .header("Content-Type", "application/json")
            .header("Connect-Protocol-Version", "1")
            .body("{}"),
    )
    .await
}
async fn receive(request: reqwest::RequestBuilder) -> Result<Value, &'static str> {
    let mut response = request.send().await.map_err(|_| "network_error")?;
    if response
        .headers()
        .get("content-type")
        .is_some_and(|v| v.as_bytes().starts_with(b"text/html"))
    {
        return Err("unavailable"); // A gateway/security page is not a login verdict.
    }
    if matches!(response.status().as_u16(), 401 | 403) {
        return Err("auth_required");
    }
    if !response.status().is_success()
        || response.content_length().is_some_and(|n| n > LIMIT as u64)
    {
        return Err("unavailable");
    }
    let mut bytes = vec![];
    while let Some(chunk) = response.chunk().await.map_err(|_| "network_error")? {
        if bytes.len() + chunk.len() > LIMIT {
            return Err("unavailable");
        }
        bytes.extend_from_slice(&chunk);
    }
    serde_json::from_slice(&bytes).map_err(|_| "unavailable")
}
async fn fetch(source: Source, credential: &Credential) -> Result<AgentQuota, &'static str> {
    let client = client()?;
    match source {
        Source::Grok => {
            let (billing, settings) = tokio::join!(
                get(&client, GROK_BILLING, credential.headers.clone()),
                get(&client, GROK_SETTINGS, credential.headers.clone())
            );
            let mut quota = parse_grok(&billing?)?;
            if let Ok(settings) = settings {
                // An explicit allowlist keeps unexpected account details out of IPC.
                quota.plan = settings["subscription_tier_display"]
                    .as_str()
                    .filter(|s| {
                        matches!(
                            *s,
                            "SuperGrok"
                                | "SuperGrok Heavy"
                                | "SuperGrok Lite"
                                | "SuperGrok Pro"
                                | "SuperGrok Max"
                                | "Heavy"
                                | "Pro"
                                | "Free"
                        )
                    })
                    .map(str::to_owned);
            }
            Ok(quota)
        }
        Source::Cursor => {
            let (usage, plan) = tokio::join!(
                rpc(&client, CURSOR_USAGE, credential.headers.clone()),
                rpc(&client, CURSOR_PLAN, credential.headers.clone())
            );
            parse_cursor_rpc(&usage?, plan.as_ref().ok())
        }
        Source::Codex => {
            let mut headers = credential.headers.clone();
            headers.insert(REFERER, HeaderValue::from_static("https://chatgpt.com/"));
            headers.insert(ORIGIN, HeaderValue::from_static("https://chatgpt.com"));
            parse_codex(&get(&client, CODEX_USAGE, headers).await?)
        }
    }
}
fn previous_for(cache: &mut Option<Cached>, identity: &[u8]) {
    if cache.as_ref().is_some_and(|old| old.identity != identity) {
        *cache = None;
    }
}
fn failed(source: Source, old: Option<&AgentQuota>, status: &str) -> AgentQuota {
    if status == "auth_required" || status == "no_quota" || status == "unlimited" {
        return empty(source, status);
    }
    let mut quota = old.cloned().unwrap_or_else(|| empty(source, status));
    quota.status = Some(status.into());
    quota
}
async fn read(source: Source, cache: &Mutex<Option<Cached>>) -> Option<AgentQuota> {
    let credential = tokio::task::spawn_blocking(move || load(source))
        .await
        .unwrap_or(Err("unavailable"));
    let mut cache = cache.lock().await;
    let credential = match credential {
        Ok(Some(value)) => value,
        Ok(None) => {
            *cache = None;
            return None;
        }
        Err(status) => {
            *cache = None;
            return Some(empty(source, status));
        }
    };
    previous_for(&mut cache, &credential.identity);
    if let Some(old) = cache.as_ref().filter(|old| old.checked.elapsed() < REFRESH) {
        return Some(old.quota.clone());
    }
    let quota = match fetch(source, &credential).await {
        Ok(quota) => quota,
        Err(status) => failed(source, cache.as_ref().map(|old| &old.quota), status),
    };
    *cache = Some(Cached {
        identity: credential.identity,
        checked: Instant::now(),
        quota: quota.clone(),
    });
    Some(quota)
}
pub(super) async fn grok() -> Option<AgentQuota> {
    static CACHE: Mutex<Option<Cached>> = Mutex::const_new(None);
    read(Source::Grok, &CACHE).await
}
pub(super) async fn cursor() -> Option<AgentQuota> {
    static CACHE: Mutex<Option<Cached>> = Mutex::const_new(None);
    read(Source::Cursor, &CACHE).await
}
pub(super) async fn codex() -> Option<AgentQuota> {
    static CACHE: Mutex<Option<Cached>> = Mutex::const_new(None);
    read(Source::Codex, &CACHE).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn cursor_fixture() -> Value {
        json!({"membershipType":"ultra", "billingCycleStart":"2026-08-16T00:00:00Z", "billingCycleEnd":"2026-09-16T00:00:00Z",
            "individualUsage":{"plan":{"enabled":true,"used":40000,"limit":40000,"totalPercentUsed":39.622,"autoPercentUsed":31.442,"apiPercentUsed":88.7}}})
    }
    fn token(sub: &str, expiration: i64) -> String {
        format!(
            "e30.{}.signature",
            URL_SAFE_NO_PAD
                .encode(serde_json::to_vec(&json!({"sub":sub,"exp":expiration})).unwrap())
        )
    }
    #[test]
    fn grok_shared_quota_is_not_on_demand_spend() {
        let value = json!({"config":{"creditUsagePercent":65.0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-09-06T15:35:25Z"},"onDemandUsed":{"val":999},"onDemandCap":{"val":1000}}});
        let quota = parse_grok(&value).unwrap();
        assert_eq!(quota.windows[0].used_percent, 65.0);
        assert_eq!(quota.windows[0].window_minutes, 10080);
        assert_eq!(quota.windows[0].label.as_deref(), Some("shared_week"));
        assert!(quota.windows[0].resets_at.is_some());
        let mut missing = value.clone();
        missing["config"]
            .as_object_mut()
            .unwrap()
            .remove("creditUsagePercent");
        assert!(parse_grok(&missing).is_err());
        missing["config"]["creditUsagePercent"] = json!(-1);
        assert!(parse_grok(&missing).is_err());
    }
    #[test]
    fn cursor_separate_pools_keep_fractional_percent_units() {
        let quota = parse_cursor(&cursor_fixture()).unwrap();
        assert_eq!(quota.plan.as_deref(), Some("Ultra"));
        assert_eq!(quota.windows.len(), 2);
        assert_eq!(quota.windows[0].used_percent, 31.442);
        assert_eq!(quota.windows[1].used_percent, 88.7);
        assert_eq!(quota.windows[0].window_minutes, 31 * 1440);
        let mut value = cursor_fixture();
        value["individualUsage"]["plan"]["autoPercentUsed"] = json!(0.5);
        value["individualUsage"]["plan"]["apiPercentUsed"] = json!(105.0);
        assert_eq!(parse_cursor(&value).unwrap().windows[0].used_percent, 0.5);
        assert_eq!(parse_cursor(&value).unwrap().windows[1].used_percent, 105.0);
    }
    #[test]
    fn cursor_unknown_and_unlimited_are_not_zero_usage() {
        assert!(parse_cursor(&json!({})).is_err());
        assert_eq!(
            parse_cursor(&json!({"isUnlimited":true})).unwrap_err(),
            "unlimited"
        );
        let quota = parse_cursor(
            &json!({"individualUsage":{"overall":{"enabled":true,"used":50,"limit":100}}}),
        )
        .unwrap();
        assert_eq!(quota.windows[0].label.as_deref(), Some("personal_limit"));
        assert_eq!(quota.windows[0].used_percent, 50.0);
        assert_eq!(quota.windows[0].window_minutes, 0);
        assert!(parse_cursor(&json!({"individualUsage":{"plan":{"used":0,"limit":0}}})).is_err());
    }
    #[test]
    fn cursor_rpc_uses_millisecond_dates_and_documented_zero_defaults() {
        let value = serde_json::json!({"enabled":true,"billingCycleStart":"1786895801000","billingCycleEnd":"1789574201000",
            "planUsage":{"includedSpend":40000,"limit":40000,"autoPercentUsed":31.442,"apiPercentUsed":88.7,"totalPercentUsed":39.622}});
        let plan = serde_json::json!({"planInfo":{"planName":"Ultra"}});
        let quota = parse_cursor_rpc(&value, Some(&plan)).unwrap();
        assert_eq!(quota.plan.as_deref(), Some("Ultra"));
        assert_eq!(quota.windows[0].resets_at, Some(1789574201));
        assert_eq!(quota.windows[1].used_percent, 88.7);
        assert_eq!(
            parse_cursor_rpc(&serde_json::json!({"planUsage":{"limit":40000}}), None)
                .unwrap()
                .windows[0]
                .used_percent,
            0.0
        );
        assert!(parse_cursor_rpc(&serde_json::json!({}), None).is_err());
        assert!(parse_cursor_rpc(&serde_json::json!({"planUsage":{}}), None).is_err());
    }
    #[test]
    fn credentials_validate_scope_expiration_and_subject() {
        let mut grok = json!({"https://auth.x.ai::test":{"key":"test-token","expires_at":"2099-01-01T00:00:00Z"}});
        assert!(grok_credential(&grok).unwrap().headers[AUTHORIZATION].is_sensitive());
        grok["https://auth.x.ai::test"]["oidc_issuer"] = json!("https://evil.test");
        assert!(grok_credential(&grok).is_err());
        assert!(
            grok_credential(&json!({"https://evil.test/sign-in":{"key":"test-token"}})).is_err()
        );
        let valid = token("auth0|user_123", 4070908800);
        assert!(cursor_credential(&valid).unwrap().headers[AUTHORIZATION].is_sensitive());
        for invalid in [
            token("user;injected=1", 4070908800),
            token("user", 1),
            "invalid".into(),
        ] {
            assert!(cursor_credential(&invalid).is_err());
        }
    }
    #[test]
    fn account_change_and_logout_discard_previous_quota() {
        let old = parse_cursor(&cursor_fixture()).unwrap();
        let mut cache = Some(Cached {
            identity: vec![1],
            checked: Instant::now(),
            quota: old.clone(),
        });
        previous_for(&mut cache, &[2]);
        assert!(cache.is_none());
        let failed = failed(Source::Cursor, Some(&old), "unavailable");
        assert_eq!(failed.observed_at, old.observed_at);
        assert_eq!(failed.windows.len(), 2);
        assert!(super::failed(Source::Cursor, Some(&old), "auth_required")
            .windows
            .is_empty());
    }
    #[test]
    fn cursor_reads_only_its_auth_row_and_never_overwrites_client_state() {
        let dir = tempfile::tempdir().unwrap();
        let cli = dir.path().join("auth.json");
        let db_path = dir.path().join("state.vscdb");
        let db = rusqlite::Connection::open(&db_path).unwrap();
        db.execute(
            "CREATE TABLE ItemTable(key TEXT PRIMARY KEY, value TEXT)",
            [],
        )
        .unwrap();
        db.execute(
            "INSERT INTO ItemTable VALUES ('cursorAuth/accessToken', ?1)",
            [token("user", 4070908800)],
        )
        .unwrap();
        drop(db);
        let before = std::fs::read(&db_path).unwrap();
        assert!(cursor_from_paths(&cli, &db_path).unwrap().is_some());
        assert_eq!(std::fs::read(&db_path).unwrap(), before);
        std::fs::write(&cli, r#"{"accessToken":"invalid"}"#).unwrap();
        assert!(cursor_from_paths(&cli, &db_path).is_err()); // no cross-account fallback
    }
    #[tokio::test]
    async fn quota_timeout_is_a_network_error() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let client = Client::builder().no_proxy().timeout(Duration::from_millis(100)).build().unwrap();
        let url = format!("http://{}/quota", listener.local_addr().unwrap());
        assert_eq!(get(&client, &url, HeaderMap::new()).await.unwrap_err(), "network_error");
        drop(listener);
    }

    #[tokio::test]
    async fn quota_http_rejects_redirect_and_oversized_body() {
        use tokio::{
            io::{AsyncReadExt, AsyncWriteExt},
            net::TcpListener,
        };
        for reply in [
            "HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/leak\r\nContent-Length: 0\r\n\r\n",
            "HTTP/1.1 200 OK\r\nContent-Length: 999999\r\n\r\n",
            "HTTP/1.1 403 Forbidden\r\nContent-Type: text/html\r\nContent-Length: 0\r\n\r\n",
        ] {
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let url = format!("http://{}/quota", listener.local_addr().unwrap());
            let server = tokio::spawn(async move {
                let (mut stream, _) = listener.accept().await.unwrap();
                let mut buf = [0; 2048];
                let _ = stream.read(&mut buf).await.unwrap();
                stream.write_all(reply.as_bytes()).await.unwrap();
            });
            assert_eq!(
                get(&client().unwrap(), &url, HeaderMap::new())
                    .await
                    .unwrap_err(),
                "unavailable"
            );
            server.await.unwrap();
        }
    }
    #[test]
    fn parse_codex_usage_accepts_named_windows_and_camel_case() {
        let quota = parse_codex(&json!({
            "rate_limit": {
                "limit_id": "codex",
                "primary_window": {
                    "used_percent": 12.5,
                    "window_minutes": 300,
                    "resets_at": 1_800_000_000
                },
                "secondary_window": {
                    "used_percent": 49.0,
                    "window_minutes": 10080,
                    "resets_at": 1_789_805_411
                }
            }
        }))
        .unwrap();
        assert_eq!(quota.windows.len(), 2);
        assert_eq!(quota.windows[0].label.as_deref(), Some("five_hour"));
        assert_eq!(quota.windows[0].used_percent, 12.5);
        assert_eq!(quota.windows[0].window_minutes, 300);
        assert_eq!(quota.windows[1].label.as_deref(), Some("weekly"));
        assert_eq!(quota.windows[1].used_percent, 49.0);
        let reversed = parse_codex(&json!({
            "rate_limit": {
                "limit_id": "codex",
                "primary_window": {
                    "used_percent": 49.0,
                    "window_minutes": 10080
                },
                "secondary_window": {
                    "used_percent": 12.0,
                    "window_minutes": 299
                }
            }
        }))
        .unwrap();
        assert_eq!(reversed.windows[0].label.as_deref(), Some("five_hour"));
        assert_eq!(reversed.windows[1].label.as_deref(), Some("weekly"));
        let camel = parse_codex(&json!({
            "rateLimits": {
                "limitId": "codex",
                "primaryWindow": {
                    "usedPercent": 31.4,
                    "windowDurationMins": 299,
                    "resetsAt": 1_800_000_000
                }
            }
        }))
        .unwrap();
        assert_eq!(camel.windows.len(), 1);
        assert_eq!(camel.windows[0].label.as_deref(), Some("five_hour"));
        assert_eq!(camel.windows[0].window_minutes, 300);
        let by_id = parse_codex(&json!({
            "rateLimitsByLimitId": {
                "codex": {
                    "limitId": "codex",
                    "primaryWindow": {
                        "usedPercent": 8.0,
                        "windowDurationMins": 10080
                    }
                },
                "spark": {
                    "limitId": "spark",
                    "primaryWindow": {
                        "usedPercent": 99.0,
                        "windowDurationMins": 300
                    }
                }
            }
        }))
        .unwrap();
        assert_eq!(by_id.windows.len(), 1);
        assert_eq!(by_id.windows[0].label.as_deref(), Some("weekly"));
        assert_eq!(
            parse_codex(&json!({"rate_limit":{"limit_id":"spark","primary_window":{"used_percent":1.0,"window_minutes":300}}})).unwrap_err(),
            "no_quota"
        );
        assert_eq!(parse_codex(&json!({})).unwrap_err(), "no_quota");
    }
    #[test]
    fn parse_codex_wham_usage_uses_seconds_and_reset_at() {
        let quota = parse_codex(&json!({
            "plan_type": "pro",
            "rate_limit": {
                "allowed": true,
                "limit_reached": false,
                "primary_window": {
                    "used_percent": 55,
                    "limit_window_seconds": 18000,
                    "reset_after_seconds": 2547,
                    "reset_at": 1_800_000_000
                },
                "secondary_window": {
                    "used_percent": 51,
                    "limit_window_seconds": 604800,
                    "reset_after_seconds": 489405,
                    "reset_at": 1_789_805_411
                }
            },
            "credits": { "unlimited": false, "balance": "0" },
            "additional_rate_limits": [{
                "limit_name": "GPT-5.3-Codex-Spark",
                "rate_limit": {
                    "primary_window": {
                        "used_percent": 99,
                        "limit_window_seconds": 18000,
                        "reset_at": 1_800_000_000
                    }
                }
            }]
        }))
        .unwrap();
        assert_eq!(quota.plan.as_deref(), Some("Pro"));
        assert_eq!(quota.status.as_deref(), Some("ready"));
        assert_eq!(quota.windows.len(), 2);
        assert_eq!(quota.windows[0].label.as_deref(), Some("five_hour"));
        assert_eq!(quota.windows[0].used_percent, 55.0);
        assert_eq!(quota.windows[0].window_minutes, 300);
        assert_eq!(quota.windows[0].resets_at, Some(1_800_000_000));
        assert_eq!(quota.windows[1].label.as_deref(), Some("weekly"));
        assert_eq!(quota.windows[1].used_percent, 51.0);
        assert_eq!(quota.windows[1].window_minutes, 10080);
        assert_eq!(quota.windows[1].resets_at, Some(1_789_805_411));
    }

    #[test]
    fn parse_codex_skips_unknown_window_lengths() {
        let quota = parse_codex(&json!({
            "rate_limit": {
                "limit_id": "codex",
                "primary_window": {
                    "used_percent": 10.0,
                    "window_minutes": 30
                },
                "secondary_window": {
                    "used_percent": 20.0,
                    "window_minutes": 10080
                }
            }
        }))
        .unwrap();
        assert_eq!(quota.windows.len(), 1);
        assert_eq!(quota.windows[0].label.as_deref(), Some("weekly"));
    }
    #[test]
    fn parse_codex_auth_json_requires_chatgpt_access_token() {
        let cred = codex_credential(&json!({
            "tokens": {
                "access_token": "tok",
                "account_id": "acct-1",
                "id_token": "id",
                "refresh_token": "refresh"
            }
        }))
        .unwrap();
        assert_eq!(
            cred.headers.get(AUTHORIZATION).unwrap().to_str().unwrap(),
            "Bearer tok"
        );
        assert_eq!(
            cred.headers
                .get("chatgpt-account-id")
                .unwrap()
                .to_str()
                .unwrap(),
            "acct-1"
        );
        assert_eq!(cred.identity, Sha256::digest(b"tok").to_vec());
        assert_eq!(
            codex_credential(&json!({"OPENAI_API_KEY":"sk-..."})).err(),
            Some("not_connected")
        );
        assert_eq!(
            codex_credential(&json!({"tokens":{"access_token":""}})).err(),
            Some("auth_required")
        );
        assert_eq!(
            codex_credential(&json!({"tokens":{"refresh_token":"only"}})).err(),
            Some("auth_required")
        );
        let expired = format!(
            "e30.{}.sig",
            URL_SAFE_NO_PAD.encode(serde_json::to_vec(&json!({"exp": 1})).unwrap())
        );
        assert_eq!(
            codex_credential(&json!({"tokens":{"access_token": expired}})).err(),
            Some("auth_required")
        );
    }
    #[tokio::test]
    #[ignore = "explicit read-only verification using this machine's logged-in accounts"]
    async fn live_connected_quotas() {
        assert_eq!(
            std::env::var("TOKENDANCE_VERIFY_CONNECTED_QUOTAS").as_deref(),
            Ok("1")
        );
        assert!(load(Source::Cursor)
            .expect("Cursor credential parsing")
            .is_some());
        let (grok, cursor, codex) = tokio::join!(grok(), cursor(), codex());
        for quota in [grok, cursor] {
            let quota = quota.expect("client session available");
            assert_eq!(quota.status.as_deref(), Some("ready"));
            println!("{}", serde_json::to_string(&quota).unwrap());
        }
        if load(Source::Codex)
            .expect("Codex credential parsing")
            .is_some()
        {
            let quota = codex.expect("client session available");
            assert_eq!(quota.status.as_deref(), Some("ready"));
            println!("{}", serde_json::to_string(&quota).unwrap());
        }
    }
}
