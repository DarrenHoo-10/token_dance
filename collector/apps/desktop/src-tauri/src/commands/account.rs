use crate::{auto_sync, state::AppState};
use std::collections::BTreeMap;
use std::fs;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use uploader::{DeviceSigner, HttpTransport, InMemoryDeviceSigner, IngestTransport};
use wal_spool::{KeyProvider, OsKeyProvider};

use reqwest::{Client, Method, StatusCode};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tauri::{Manager, State, Url};
use tokio::sync::Mutex;

// Passwords are never saved. Native session cookies are scoped to one website
// origin and are never returned across the WebView IPC boundary.
#[derive(Default)]
pub struct AccountState(Mutex<Option<Connection>>);

struct Connection {
    origin: Url,
    client: Client,
    cookies: BTreeMap<String, String>,
    csrf: String,
    transport: Option<HttpTransport>,
    retry_at: Option<Instant>,
    failures: u32,
    blocked: bool,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AccountUser {
    pub user_id: String,
    pub display_name: String,
    pub handle: Option<String>,
    #[serde(default)]
    pub onboarding_required: bool,
}

#[derive(Serialize)]
pub struct AccountSession {
    pub user: Option<AccountUser>,
}

fn account_origin(website: &str) -> Result<Url, String> {
    let mut url = Url::parse(website).map_err(|_| "INVALID_WEBSITE")?;
    let local = matches!(url.host_str(), Some("localhost" | "127.0.0.1" | "[::1]"));
    if (url.scheme() != "https" && !(url.scheme() == "http" && local))
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err("HTTPS_REQUIRED".into());
    }
    url.set_query(None);
    url.set_fragment(None);
    let path = format!("{}/", url.path().trim_end_matches('/'));
    url.set_path(&path);
    Ok(url)
}

impl Connection {
    fn new(origin: Url) -> Result<Self, String> {
        let client = Client::builder()
            .timeout(Duration::from_secs(15))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| "NETWORK_ERROR")?;
        Ok(Self {
            origin,
            client,
            cookies: BTreeMap::new(),
            csrf: String::new(),
            transport: None,
            retry_at: None,
            failures: 0,
            blocked: false,
        })
    }

    async fn request(
        &mut self,
        method: Method,
        path: &str,
        body: Option<Value>,
    ) -> Result<Option<Value>, String> {
        let url = self
            .origin
            .join(path.trim_start_matches('/'))
            .map_err(|_| "INVALID_WEBSITE")?;
        let mut request = self
            .client
            .request(method, url)
            .header("Accept", "application/json")
            .header("Origin", self.origin.origin().ascii_serialization());
        if !self.cookies.is_empty() {
            request = request.header(
                "Cookie",
                self.cookies
                    .iter()
                    .map(|(name, value)| format!("{name}={value}"))
                    .collect::<Vec<_>>()
                    .join("; "),
            );
        }
        if !self.csrf.is_empty() {
            request = request.header("X-CSRF-Token", &self.csrf);
        }
        if let Some(body) = body {
            request = request.json(&body);
        }
        let response = request.send().await.map_err(|_| "NETWORK_ERROR")?;
        let status = response.status();
        for header in response.headers().get_all("set-cookie") {
            if let Ok(raw) = header.to_str() {
                if let Ok(cookie) = cookie::Cookie::parse(raw) {
                    if matches!(
                        cookie.name(),
                        "__Host-tokendance_session" | "tokendance_session"
                    ) {
                        if cookie.value().is_empty() {
                            self.cookies.remove(cookie.name());
                        } else {
                            self.cookies
                                .insert(cookie.name().into(), cookie.value().into());
                        }
                    }
                }
            }
        }
        if status == StatusCode::NO_CONTENT {
            return Ok(None);
        }
        if !status.is_success() {
            return Err(match status.as_u16() {
                401 => "INVALID_CREDENTIALS",
                403 => "ACCOUNT_FORBIDDEN",
                429 => "TOO_MANY_ATTEMPTS",
                _ => "SERVER_ERROR",
            }
            .into());
        }
        let value: Value = response.json().await.map_err(|_| "INVALID_RESPONSE")?;
        if let Some(csrf) = value.get("csrfToken").and_then(Value::as_str) {
            self.csrf = csrf.to_owned();
        }
        Ok(Some(value))
    }

    async fn session(&mut self) -> Result<AccountSession, String> {
        match self
            .request(Method::GET, "/api/v1/auth/session", None)
            .await
        {
            Ok(Some(value))
                if value.get("authenticated").and_then(Value::as_bool) == Some(true) =>
            {
                let user = serde_json::from_value(value["user"].clone())
                    .map_err(|_| "INVALID_RESPONSE")?;
                Ok(AccountSession { user: Some(user) })
            }
            Ok(_) => {
                self.cookies.clear();
                self.csrf.clear();
                Ok(AccountSession { user: None })
            }
            Err(error) if error == "INVALID_CREDENTIALS" => {
                self.cookies.clear();
                self.csrf.clear();
                Ok(AccountSession { user: None })
            }
            Err(error) => Err(error),
        }
    }

    async fn login(&mut self, email: &str, password: &str) -> Result<AccountSession, String> {
        self.transport = None;
        self.retry_at = None;
        self.failures = 0;
        self.blocked = false;
        self.cookies.clear();
        self.csrf.clear();
        let result = self.request(Method::POST, "/api/v1/auth/login", Some(json!({
            "email": email.trim(), "password": password, "deviceLabel": "TokenDance Desktop", "keepSignedIn": true
        }))).await?;
        if result.is_none() || self.cookies.is_empty() {
            return Err("INVALID_RESPONSE".into());
        }
        let session = self.session().await?;
        if session.user.is_none() {
            return Err("INVALID_RESPONSE".into());
        }
        Ok(session)
    }
}

impl Connection {
    async fn register_sync_device(&mut self, signer: Arc<dyn DeviceSigner>) -> Result<(), String> {
        let grant = self
            .request(Method::POST, "/api/v1/me/device-grants", Some(json!({})))
            .await?
            .and_then(|value| value["grantToken"].as_str().map(str::to_owned))
            .filter(|token| token.starts_with("dgt_"))
            .ok_or("INVALID_RESPONSE")?;
        let public_key = signer
            .public_key()
            .map_err(|_| "DEVICE_KEY_ERROR")?
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        let response = self
            .client
            .post(
                self.origin
                    .join("v1/installations/register")
                    .map_err(|_| "INVALID_WEBSITE")?,
            )
            .bearer_auth(grant)
            .json(&json!({
                "publicKey": public_key, "deviceName": "TokenDance Desktop",
                "osType": std::env::consts::OS, "architecture": std::env::consts::ARCH,
                "collectorVersion": env!("CARGO_PKG_VERSION")
            }))
            .send()
            .await
            .map_err(|_| "NETWORK_ERROR")?;
        if !response.status().is_success() {
            return Err(match response.status().as_u16() {
                401 | 403 | 409 => "DEVICE_UNAVAILABLE",
                _ => "NETWORK_ERROR",
            }
            .into());
        }
        let value: Value = response.json().await.map_err(|_| "INVALID_RESPONSE")?;
        if value["status"] != "active" {
            return Err("DEVICE_UNAVAILABLE".into());
        }
        let installation = value["installationId"]
            .as_str()
            .filter(|id| id.starts_with("ins_"))
            .ok_or("INVALID_RESPONSE")?;
        self.transport = Some(HttpTransport::new_claimed(
            self.origin.as_str(),
            self.client.clone(),
            installation,
            signer,
        ));
        Ok(())
    }

    async fn sync_once(&mut self, app: &AppState) -> Result<&'static str, String> {
        if self.cookies.is_empty() {
            return Ok("LOGIN_REQUIRED");
        }
        let session = self.session().await?;
        let Some(user) = session.user else {
            self.transport = None;
            return Ok("LOGIN_REQUIRED");
        };
        if user.onboarding_required {
            return Ok("NEEDS_PROFILE");
        }
        let status = app.get_daemon_status().await;
        if status.global_paused {
            return Ok("PAUSED");
        }
        if status.events_pending > 0 {
            *app.sync_status.write().await = "SYNCING".into();
        }
        if self.transport.is_none() {
            let seed = OsKeyProvider::new("io.tokendance.desktop", "collector-device-ed25519")
                .data_key()
                .map_err(|_| "DEVICE_KEY_ERROR")?;
            self.register_sync_device(Arc::new(InMemoryDeviceSigner::from_seed(seed)))
                .await?;
        }
        let transport = self.transport.as_ref().ok_or("DEVICE_UNAVAILABLE")?;
        let events = { app.service.lock().await.wal.unacked_events() };
        if events.is_empty() {
            return Ok("SYNCED");
        }
        let batch = auto_sync::batch(transport.installation_id(), events)?;
        let ack = transport
            .upload(&batch)
            .await
            .map_err(|error| match error {
                uploader::TransportError::Auth => "DEVICE_UNAVAILABLE",
                _ => "UPLOAD_FAILED",
            })?;
        let checked = auto_sync::checked_ack(&batch, &ack)?;
        if !checked.acked_event_ids.is_empty() {
            app.acknowledge_auto_sync(checked).await?;
        }
        if ack.rejected.iter().any(|item| !item.retryable) {
            return Err("REJECTED_EVENTS".into());
        }
        if !ack.rejected.is_empty() {
            return Err("UPLOAD_FAILED".into());
        }
        let pending = app.service.lock().await.wal.unacked_count();
        Ok(if pending == 0 { "SYNCED" } else { "WAITING" })
    }
}

impl AccountState {
    async fn auto_sync_tick(&self, app: &AppState) {
        // Keep one batch serialized with login/logout; after sign-out completes
        // no request can use an old account or device transport.
        let mut guard = self.0.lock().await;
        let Some(current) = guard.as_mut() else {
            *app.sync_status.write().await = "LOGIN_REQUIRED".into();
            return;
        };
        if current
            .retry_at
            .is_some_and(|deadline| Instant::now() < deadline)
        {
            return;
        }
        let next = match current.sync_once(app).await {
            Ok(status) => {
                current.failures = 0;
                current.retry_at = None;
                current.blocked = false;
                status
            }
            Err(error) => {
                current.failures = current.failures.saturating_add(1);
                current.retry_at = Some(
                    Instant::now() + Duration::from_secs(10 * (1u64 << current.failures.min(5))),
                );
                current.blocked = matches!(
                    error.as_str(),
                    "DEVICE_UNAVAILABLE"
                        | "DEVICE_KEY_ERROR"
                        | "REJECTED_EVENTS"
                        | "EVENT_TOO_LARGE"
                        | "ACCOUNT_FORBIDDEN"
                );
                if current.blocked {
                    // Recheck slowly so a device resumed on the website can
                    // recover without requiring a manual desktop action.
                    current.retry_at = Some(Instant::now() + Duration::from_secs(300));
                    if matches!(error.as_str(), "REJECTED_EVENTS" | "EVENT_TOO_LARGE") {
                        "DATA_REJECTED"
                    } else {
                        "NEEDS_ATTENTION"
                    }
                } else {
                    "RETRYING"
                }
            }
        };
        *app.sync_status.write().await = next.into();
    }
}

pub fn start_auto_sync(handle: tauri::AppHandle, app: AppState) {
    tauri::async_runtime::spawn(async move {
        let account = handle.state::<AccountState>();
        {
            let mut guard = account.0.lock().await;
            if guard.is_none() {
                if let Ok(bytes) = fs::read(persist_path()) {
                    if let Ok(saved) = serde_json::from_slice::<PersistedAccount>(&bytes) {
                        if saved.expires_at > unix_now() {
                            if let Ok(origin) = account_origin(&saved.origin) {
                                if let Ok(mut current) = Connection::new(origin) {
                                    current.cookies = saved.cookies;
                                    current.csrf = saved.csrf;
                                    *guard = Some(current);
                                }
                            }
                        }
                    }
                }
            }
        }
        let mut interval = tokio::time::interval(Duration::from_secs(10));
        interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        loop {
            interval.tick().await;
            account.auto_sync_tick(&app).await;
        }
    });
}

const SESSION_TTL_SECS: u64 = 30 * 24 * 60 * 60;

#[derive(Serialize, Deserialize)]
struct PersistedAccount {
    origin: String,
    cookies: BTreeMap<String, String>,
    csrf: String,
    expires_at: u64,
}

fn persist_path() -> std::path::PathBuf {
    crate::state::app_data_root().join("account-session.json")
}

fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

fn save_account(connection: &Connection) {
    if connection.cookies.is_empty() {
        let _ = fs::remove_file(persist_path());
        return;
    }
    let path = persist_path();
    if let Some(parent) = path.parent() {
        let _ = fs::create_dir_all(parent);
    }
    let payload = PersistedAccount {
        origin: connection.origin.as_str().to_string(),
        cookies: connection.cookies.clone(),
        csrf: connection.csrf.clone(),
        expires_at: unix_now().saturating_add(SESSION_TTL_SECS),
    };
    if let Ok(body) = serde_json::to_vec(&payload) {
        let _ = fs::write(path, body);
    }
}

fn load_account(origin: &Url) -> Option<(BTreeMap<String, String>, String)> {
    let body = fs::read(persist_path()).ok()?;
    let stored: PersistedAccount = serde_json::from_slice(&body).ok()?;
    if stored.origin != origin.as_str()
        || stored.expires_at <= unix_now()
        || stored.cookies.is_empty()
    {
        let _ = fs::remove_file(persist_path());
        return None;
    }
    Some((stored.cookies, stored.csrf))
}

fn connection(state: &mut Option<Connection>, origin: Url) -> Result<&mut Connection, String> {
    if state
        .as_ref()
        .is_none_or(|current| current.origin != origin)
    {
        *state = Some(Connection::new(origin)?);
    }
    Ok(state.as_mut().expect("connection initialized"))
}

#[tauri::command]
pub async fn get_account_session(
    website: String,
    state: State<'_, AccountState>,
) -> Result<AccountSession, String> {
    let mut guard = state.0.lock().await;
    if website.is_empty() {
        *guard = None;
        return Ok(AccountSession { user: None });
    }
    let current = connection(&mut guard, account_origin(&website)?)?;
    if current.cookies.is_empty() {
        if let Some((cookies, csrf)) = load_account(&current.origin) {
            current.cookies = cookies;
            current.csrf = csrf;
        } else {
            return Ok(AccountSession { user: None });
        }
    }
    let session = current.session().await?;
    if session.user.is_some() {
        save_account(current);
    } else {
        let _ = fs::remove_file(persist_path());
    }
    Ok(session)
}

#[tauri::command]
pub async fn login_account(
    website: String,
    email: String,
    password: String,
    state: State<'_, AccountState>,
    app: State<'_, AppState>,
) -> Result<AccountSession, String> {
    if email.trim().is_empty() || password.is_empty() {
        return Err("MISSING_CREDENTIALS".into());
    }
    let mut guard = state.0.lock().await;
    let current = connection(&mut guard, account_origin(&website)?)?;
    // Read back the authenticated session, rather than trusting a successful POST alone.
    let _ = fs::remove_file(persist_path());
    *app.sync_status.write().await = "LOGIN_REQUIRED".into();
    let session = current.login(&email, &password).await?;
    save_account(current);
    *app.sync_status.write().await = "WAITING".into();
    Ok(session)
}

#[tauri::command]
pub async fn logout_account(
    website: String,
    state: State<'_, AccountState>,
    app: State<'_, AppState>,
) -> Result<(), String> {
    let mut guard = state.0.lock().await;
    let origin = account_origin(&website)?;
    if let Some(current) = guard.as_mut().filter(|current| current.origin == origin) {
        if !current.cookies.is_empty() {
            current
                .request(Method::POST, "/api/v1/auth/logout", Some(json!({})))
                .await?;
        }
    }
    *guard = None;
    let _ = fs::remove_file(persist_path());
    *app.sync_status.write().await = "LOGIN_REQUIRED".into();
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    fn mock_server(responses: Vec<String>) -> (Url, std::thread::JoinHandle<Vec<String>>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let origin = Url::parse(&format!("http://{}/", listener.local_addr().unwrap())).unwrap();
        let handle = std::thread::spawn(move || {
            responses
                .into_iter()
                .map(|response| {
                    let (mut socket, _) = listener.accept().unwrap();
                    socket
                        .set_read_timeout(Some(Duration::from_secs(5)))
                        .unwrap();
                    let mut bytes = Vec::new();
                    loop {
                        let mut buf = [0; 4096];
                        let count = socket.read(&mut buf).unwrap();
                        if count == 0 {
                            break;
                        }
                        bytes.extend_from_slice(&buf[..count]);
                        if let Some(end) = bytes.windows(4).position(|part| part == b"\r\n\r\n") {
                            let headers = String::from_utf8_lossy(&bytes[..end]).to_lowercase();
                            let length = headers
                                .lines()
                                .find_map(|line| {
                                    line.strip_prefix("content-length:")
                                        .map(|v| v.trim().parse::<usize>().unwrap())
                                })
                                .unwrap_or(0);
                            if bytes.len() >= end + 4 + length {
                                break;
                            }
                        }
                    }
                    let request = String::from_utf8_lossy(&bytes).into_owned();
                    let response = if response.contains("$BATCH") {
                        let batch_id = request
                            .lines()
                            .find_map(|line| line.strip_prefix("idempotency-key: "))
                            .unwrap();
                        let (headers, body) = response.split_once("\r\n\r\n").unwrap();
                        let body = body.replace("$BATCH", batch_id);
                        let headers = headers
                            .lines()
                            .map(|line| {
                                if line.starts_with("Content-Length:") {
                                    format!("Content-Length: {}", body.len())
                                } else {
                                    line.to_owned()
                                }
                            })
                            .collect::<Vec<_>>()
                            .join("\r\n");
                        format!("{headers}\r\n\r\n{body}")
                    } else {
                        response
                    };
                    socket.write_all(response.as_bytes()).unwrap();
                    request
                })
                .collect()
        });
        (origin, handle)
    }

    fn response(status: &str, headers: &str, body: &str) -> String {
        format!("HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n{headers}\r\n{body}", body.len())
    }

    fn session_response() -> String {
        response(
            "200 OK",
            "",
            r#"{"authenticated":true,"user":{"userId":"fixture-user","displayName":"Test User"},"csrfToken":"fixture-csrf"}"#,
        )
    }

    #[tokio::test]
    async fn auto_sync_registers_with_grant_and_only_removes_server_confirmed_events() {
        let (_root, app) = crate::auto_sync::tests::seeded_app().await;
        let (origin, server) = mock_server(vec![
            response("201 Created", "", r#"{"grantToken":"dgt_fixture"}"#),
            response(
                "200 OK",
                "",
                r#"{"installationId":"ins_fixture","status":"active"}"#,
            ),
            session_response(),
            response(
                "200 OK",
                "",
                r#"{"batchId":"$BATCH","installationId":"ins_fixture","accepted":2,"duplicates":0,"rejected":[],"serverTime":"2026-09-05T00:00:00Z"}"#,
            ),
        ]);
        let origin = origin.join("token-dance/").unwrap();
        let mut client = Connection::new(origin).unwrap();
        client
            .cookies
            .insert("tokendance_session".into(), "fixture-session".into());
        client.csrf = "fixture-csrf".into();
        client
            .register_sync_device(Arc::new(InMemoryDeviceSigner::from_seed([7; 32])))
            .await
            .unwrap();
        let account = AccountState(Mutex::new(Some(client)));
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "SYNCED");
        let status = app.get_daemon_status().await;
        assert_eq!(status.events_pending, 0);
        assert_eq!(status.events_uploaded, 2);
        assert!(status.last_sync_at.is_some());
        let requests = server.join().unwrap();
        assert!(requests[0].starts_with("POST /token-dance/api/v1/me/device-grants "));
        assert!(requests[1].starts_with("POST /token-dance/v1/installations/register "));
        assert!(requests[2].starts_with("GET /token-dance/api/v1/auth/session "));
        assert!(requests[3].starts_with("POST /token-dance/v1/telemetry/batches "));
        assert!(requests[0].contains("x-csrf-token: fixture-csrf"));
        assert!(requests[1].contains("authorization: Bearer dgt_fixture"));
        assert!(requests[3].contains("authorization: Device ins_fixture:"));
        assert!(!requests[3].contains("fixture-session"));
        *account.0.lock().await = None;
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "LOGIN_REQUIRED");
    }

    #[tokio::test]
    async fn failed_upload_keeps_queue_and_retries_without_manual_input() {
        let (_root, app) = crate::auto_sync::tests::seeded_app().await;
        let (origin, server) = mock_server(vec![
            session_response(),
            response("503 Service Unavailable", "", "{}"),
            session_response(),
            response(
                "200 OK",
                "",
                r#"{"batchId":"$BATCH","accepted":2,"duplicates":0,"rejected":[],"serverTime":"2026-09-05T00:00:00Z"}"#,
            ),
        ]);
        let mut client = Connection::new(origin.clone()).unwrap();
        client
            .cookies
            .insert("tokendance_session".into(), "fixture-session".into());
        client.transport = Some(HttpTransport::new_claimed(
            origin.as_str(),
            client.client.clone(),
            "ins_fixture",
            Arc::new(InMemoryDeviceSigner::from_seed([7; 32])),
        ));
        let account = AccountState(Mutex::new(Some(client)));
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "RETRYING");
        assert_eq!(app.get_daemon_status().await.events_pending, 2);
        assert_eq!(app.get_daemon_status().await.events_uploaded, 0);
        account.auto_sync_tick(&app).await; // Backoff performs no network request.
        account.0.lock().await.as_mut().unwrap().retry_at = None;
        account.auto_sync_tick(&app).await;
        assert_eq!(app.get_daemon_status().await.events_pending, 0);
        assert_eq!(server.join().unwrap().len(), 4);
    }

    #[tokio::test]
    async fn expired_session_never_uploads_pending_records() {
        let (_root, app) = crate::auto_sync::tests::seeded_app().await;
        let (origin, server) = mock_server(vec![response("204 No Content", "", "")]);
        let mut client = Connection::new(origin).unwrap();
        client
            .cookies
            .insert("tokendance_session".into(), "expired".into());
        let account = AccountState(Mutex::new(Some(client)));
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "LOGIN_REQUIRED");
        assert_eq!(app.get_daemon_status().await.events_pending, 2);
        assert_eq!(server.join().unwrap().len(), 1);
    }

    #[tokio::test]
    async fn login_reads_session_and_logout_sends_cookie_and_csrf() {
        let (origin, server) = mock_server(vec![
            response(
                "200 OK",
                "Set-Cookie: tokendance_session=fixture-session; HttpOnly; Path=/\r\n",
                r#"{"csrfToken":"login-csrf"}"#,
            ),
            response(
                "200 OK",
                "",
                r#"{"authenticated":true,"user":{"userId":"fixture-user","displayName":"Test User","handle":"test-user"},"csrfToken":"session-csrf"}"#,
            ),
            response(
                "204 No Content",
                "Set-Cookie: tokendance_session=; Max-Age=0; Path=/\r\n",
                "",
            ),
        ]);
        let mut client = Connection::new(origin.clone()).unwrap();
        let session = client
            .login(" fixture@example.com ", "fixture-password")
            .await
            .unwrap();
        assert_eq!(session.user.as_ref().unwrap().user_id, "fixture-user");
        let serialized = serde_json::to_string(&session).unwrap();
        assert!(!serialized.contains("csrf"));
        assert!(!serialized.contains("fixture-session"));
        client
            .request(Method::POST, "/api/v1/auth/logout", Some(json!({})))
            .await
            .unwrap();
        assert!(client.cookies.is_empty());
        let requests = server.join().unwrap();
        assert!(requests[0].starts_with("POST /api/v1/auth/login "));
        let body: Value =
            serde_json::from_str(requests[0].split("\r\n\r\n").nth(1).unwrap()).unwrap();
        assert_eq!(body["email"], "fixture@example.com");
        assert_eq!(body["keepSignedIn"], true);
        assert!(requests[1].starts_with("GET /api/v1/auth/session "));
        assert!(requests[1].contains("tokendance_session=fixture-session"));
        assert!(requests[2]
            .to_lowercase()
            .contains("x-csrf-token: session-csrf"));
        assert!(requests[2].contains(&format!(
            "origin: {}",
            origin.origin().ascii_serialization()
        )));
    }

    #[tokio::test]
    async fn rejected_login_and_expired_sessions_do_not_authenticate() {
        let (origin, server) = mock_server(vec![
            response("401 Unauthorized", "", "{}"),
            response("204 No Content", "", ""),
        ]);
        let mut client = Connection::new(origin).unwrap();
        assert!(
            matches!(client.login("fixture@example.com", "wrong").await, Err(error) if error == "INVALID_CREDENTIALS")
        );
        client
            .cookies
            .insert("tokendance_session".into(), "expired".into());
        assert!(client.session().await.unwrap().user.is_none());
        assert!(client.cookies.is_empty());
        server.join().unwrap();
    }

    #[tokio::test]
    async fn login_never_follows_a_redirect_with_credentials() {
        let (origin, server) = mock_server(vec![response(
            "307 Temporary Redirect",
            "Location: https://other.example/login\r\n",
            "",
        )]);
        let mut client = Connection::new(origin).unwrap();
        assert!(
            matches!(client.login("fixture@example.com", "fixture-password").await, Err(error) if error == "SERVER_ERROR")
        );
        assert_eq!(server.join().unwrap().len(), 1);
    }

    #[test]
    fn account_requests_require_https_or_loopback() {
        assert!(
            account_origin("https://example.com/base?q=x")
                .unwrap()
                .as_str()
                == "https://example.com/base/"
        );
        assert!(account_origin("http://localhost:3000").is_ok());
        for bad in [
            "http://example.com",
            "file:///C:/x",
            "https://user:pass@example.com",
        ] {
            assert!(account_origin(bad).is_err());
        }
    }

    #[test]
    fn changing_websites_discards_session_secrets() {
        let mut state = None;
        let first = connection(&mut state, account_origin("https://one.example").unwrap()).unwrap();
        first
            .cookies
            .insert("tokendance_session".into(), "fixture-session".into());
        first.csrf = "fixture-csrf".into();
        let next = connection(&mut state, account_origin("https://two.example").unwrap()).unwrap();
        assert!(next.cookies.is_empty());
        assert!(next.csrf.is_empty());
    }

    #[tokio::test]
    async fn account_requests_keep_the_application_path_and_origin_header() {
        let (origin, server) = mock_server(vec![response("204 No Content", "", "")]);
        let base =
            account_origin(origin.join("token-dance/?old=1#fragment").unwrap().as_str()).unwrap();
        let mut client = Connection::new(base).unwrap();
        client
            .request(Method::GET, "/api/v1/auth/session", None)
            .await
            .unwrap();
        let requests = server.join().unwrap();
        assert!(requests[0].starts_with("GET /token-dance/api/v1/auth/session "));
        assert!(requests[0].contains(&format!(
            "origin: {}\r\n",
            origin.origin().ascii_serialization()
        )));
    }
}
