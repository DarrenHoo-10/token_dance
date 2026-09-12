use crate::state::AppState;
use platform_credentials::{CredentialError, DESKTOP_SERVICE};
use crate::upload_pipeline::{session_bearer_from_cookies, UploadConsumer, UploadCredentials};
use std::collections::BTreeMap;
use std::fs;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use uploader::{
    DeviceSigner, HttpTelemetryV2, HttpTransport, InMemoryDeviceSigner, TelemetryV2Transport,
};
use wal_spool::{KeyProvider, OsKeyProvider};

use reqwest::{Client, Method, StatusCode};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::sync::atomic::{AtomicU64, Ordering};
use tauri::{Manager, State, Url};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::sync::Mutex;

// Passwords are never saved. Native session cookies are scoped to one website
// origin and are never returned across the WebView IPC boundary.
#[derive(Default)]
pub struct AccountState(Mutex<Option<Connection>>, Mutex<()>, AtomicU64);

struct Connection {
    origin: Url,
    client: Client,
    cookies: BTreeMap<String, String>,
    csrf: String,
    transport: Option<HttpTransport>,
    telemetry_v2: Option<Arc<HttpTelemetryV2>>,
    upload_consumer: Option<UploadConsumer>,
    binding_status_version: u64,
    binding_generation: u64,
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
    pub avatar_url: Option<String>,
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
        let mut builder = Client::builder()
            .timeout(Duration::from_secs(15))
            .redirect(reqwest::redirect::Policy::none());
        if matches!(origin.host_str(), Some("localhost" | "127.0.0.1" | "[::1]")) {
            builder = builder.no_proxy();
        }
        let client = builder.build().map_err(|_| "NETWORK_ERROR")?;
        Ok(Self {
            origin,
            client,
            cookies: BTreeMap::new(),
            csrf: String::new(),
            transport: None,
            telemetry_v2: None,
            upload_consumer: None,
            binding_status_version: 1,
            binding_generation: 0,
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

    async fn finish_browser_login(
        &mut self,
        code: &str,
        verifier: &str,
        redirect: &str,
    ) -> Result<AccountSession, String> {
        self.request(
            Method::POST,
            "/api/v1/auth/desktop/exchange",
            Some(json!({
                "code": code, "codeVerifier": verifier, "redirectUri": redirect
            })),
        )
        .await?;
        let session = self.session().await?;
        if session.user.is_none() {
            return Err("INVALID_RESPONSE".to_string());
        }
        Ok(session)
    }

    #[cfg(test)]
    async fn login(&mut self, email: &str, password: &str) -> Result<AccountSession, String> {
        self.transport = None;
        self.telemetry_v2 = None;
        self.upload_consumer = None;
        self.binding_generation = self.binding_generation.saturating_add(1);
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
    async fn register_sync_device(
        &mut self,
        signer: Arc<dyn DeviceSigner>,
        user_id: &str,
    ) -> Result<(), String> {
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
        let proof_timestamp = chrono::Utc::now().timestamp().to_string();
        let proof_message = format!(
            "tokendance-device-binding\nregister:{user_id}\n{public_key}\n{proof_timestamp}"
        );
        let proof_signature = signer
            .sign(proof_message.as_bytes())
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
                "publicKey": public_key, "proofTimestamp": proof_timestamp, "proofSignature": proof_signature, "deviceName": "TokenDance Desktop",
                "osType": std::env::consts::OS, "architecture": std::env::consts::ARCH,
                "collectorVersion": env!("CARGO_PKG_VERSION")
            }))
            .send()
            .await
            .map_err(|_| "NETWORK_ERROR")?;
        if !response.status().is_success() {
            return Err(match response.status().as_u16() {
                409 => "DEVICE_BOUND_ELSEWHERE",
                401 | 403 => "DEVICE_UNAVAILABLE",
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
        self.binding_status_version = value
            .get("statusVersion")
            .and_then(|v| v.as_u64())
            .filter(|v| *v > 0)
            .unwrap_or(1);
        self.binding_generation = self.binding_generation.saturating_add(1);
        self.transport = Some(HttpTransport::new_claimed(
            self.origin.as_str(),
            self.client.clone(),
            installation,
            Arc::clone(&signer),
        ));
        self.telemetry_v2 = Some(Arc::new(HttpTelemetryV2::new(
            self.origin.as_str(),
            self.client.clone(),
            installation,
            signer,
        )));
        self.upload_consumer = None;
        Ok(())
    }

    async fn sync_once(&mut self, app: &AppState) -> Result<&'static str, String> {
        if crate::updates::upgrade_required() {
            return Ok("CLIENT_UPGRADE_REQUIRED");
        }
        if self.cookies.is_empty() {
            let _ = app.deactivate_sync_account();
            return Ok("LOGIN_REQUIRED");
        }
        let session = self.session().await?;
        let Some(user) = session.user else {
            self.transport = None;
            self.telemetry_v2 = None;
            self.upload_consumer = None;
            let _ = app.deactivate_sync_account();
            return Ok("LOGIN_REQUIRED");
        };
        if user.onboarding_required {
            return Ok("NEEDS_PROFILE");
        }
        if !app.sync_enabled() {
            return Ok("SYNC_OFF");
        }
        if !crate::local_store::pipeline::event_pipeline_v2_client_enabled() {
            // P8 rollback: pause upload without reopening legacy snapshot routes.
            return Ok("PIPELINE_PAUSED");
        }
        let status = app.get_daemon_status().await;
        if status.global_paused {
            return Ok("PAUSED");
        }
        let _target_id = app.activate_sync_account(&user.user_id).await?;
        if app.pending_sync_count() > 0 {
            *app.sync_status.write().await = "SYNCING".into();
        }
        if self.telemetry_v2.is_none() {
            let create = !app.control_dir_path().join("device-registered").exists();
            let seed = OsKeyProvider::device_seed(create)
                .data_key()
                .map_err(|_| "DEVICE_KEY_ERROR")?;
            self.register_sync_device(
                Arc::new(InMemoryDeviceSigner::from_seed(seed)),
                &user.user_id,
            )
            .await?;
            collector_service::platform::write_private_file(
                &app.control_dir_path().join("device-registered"),
                b"1",
            )?;
        }
        let Some(session_bearer) = session_bearer_from_cookies(&self.cookies) else {
            return Ok("LOGIN_REQUIRED");
        };
        let Some(writer) = app.pipeline_writer() else {
            // Pipeline not ready: do not fall back to legacy snapshot upload.
            return Ok("WAITING");
        };
        if self.upload_consumer.is_none() {
            let transport = self.telemetry_v2.clone().ok_or("DEVICE_UNAVAILABLE")?
                as Arc<dyn TelemetryV2Transport>;
            self.upload_consumer = Some(UploadConsumer::new(writer, transport));
        }
        let creds = UploadCredentials {
            session_bearer,
            binding_status_version: self.binding_status_version,
            binding_generation: self.binding_generation,
        };
        let report = self
            .upload_consumer
            .as_mut()
            .ok_or("DEVICE_UNAVAILABLE")?
            .tick(Some(&creds))
            .await?;
        if report.auth_blocked {
            return Err("DEVICE_UNAVAILABLE".into());
        }
        Ok(if report.pending == 0 {
            "SYNCED"
        } else {
            "WAITING"
        })
    }
}

impl AccountState {
    pub async fn rebuild_local_data(
        &self,
        app: &AppState,
    ) -> Result<crate::local_store::pipeline::reconstruction::RebuildStatus, String> {
        let mut guard = self.0.lock().await;
        if let Some(connection) = guard.as_mut() {
            if let Some(mut uploader) = connection.upload_consumer.take() {
                uploader.finish_in_flight().await;
            }
        }
        let runtime = app.pipeline_runtime().ok_or("PIPELINE_UNAVAILABLE")?;
        app.set_rebuilding(true);
        let result = tauri::async_runtime::spawn_blocking(move || runtime.rebuild())
            .await
            .map_err(|e| e.to_string())
            .and_then(|result| result);
        if result.is_err() {
            app.set_rebuilding(false);
        }
        result
    }

    async fn auto_sync_tick(&self, app: &AppState) {
        // Keep one batch serialized with login/logout; after sign-out completes
        // no request can use an old account or device transport.
        let mut guard = self.0.lock().await;
        let Some(current) = guard.as_mut() else {
            let _ = app.deactivate_sync_account();
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
                        | "DEVICE_BOUND_ELSEWHERE"
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
                    } else if error == "DEVICE_BOUND_ELSEWHERE" {
                        "DEVICE_BOUND_ELSEWHERE"
                    } else {
                        "NEEDS_ATTENTION"
                    }
                } else {
                    "RETRYING"
                }
            }
        };
        let mut status = app.sync_status.write().await;
        if status.as_str() != next {
            collector_service::runtime::append_log(
                &app.control_dir_path(),
                &format!("sync status={next}"),
            );
        }
        *status = next.into();
    }
}

pub fn start_auto_sync(handle: tauri::AppHandle, app: AppState) {
    if crate::local_test::enabled() {
        return;
    }
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
const SESSION_VERSION: u32 = 1;

#[derive(Serialize, Deserialize)]
struct PersistedAccount {
    #[serde(default = "session_version")]
    version: u32,
    origin: String,
    cookies: BTreeMap<String, String>,
    csrf: String,
    expires_at: u64,
}

fn session_version() -> u32 {
    SESSION_VERSION
}

#[derive(Serialize, Deserialize)]
struct SessionIndex {
    origin: String,
    expires_at: u64,
}

fn persist_path() -> std::path::PathBuf {
    crate::state::app_data_root().join("account-session.json")
}

fn index_path() -> std::path::PathBuf {
    crate::state::app_data_root().join("account-session.index.json")
}

fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

fn session_account(origin: &str) -> String {
    let digest = Sha256::digest(origin.as_bytes());
    format!("account-session-{}", hex_encode(&digest[..16]))
}

fn hex_encode(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

fn save_account(connection: &Connection) {
    let origin = connection.origin.as_str().to_string();
    let account = session_account(&origin);
    if connection.cookies.is_empty() {
        let _ = platform_credentials::delete(DESKTOP_SERVICE, &account);
        let _ = fs::remove_file(index_path());
        let _ = fs::remove_file(persist_path());
        return;
    }
    let payload = PersistedAccount {
        version: SESSION_VERSION,
        origin: origin.clone(),
        cookies: connection.cookies.clone(),
        csrf: connection.csrf.clone(),
        expires_at: unix_now().saturating_add(SESSION_TTL_SECS),
    };
    let Ok(secret) = serde_json::to_string(&payload) else {
        return;
    };
    if platform_credentials::put(DESKTOP_SERVICE, &account, &secret).is_err() {
        return;
    }
    match platform_credentials::get(DESKTOP_SERVICE, &account) {
        Ok(readback) if readback == secret => {}
        _ => return,
    }
    let index = SessionIndex {
        origin,
        expires_at: payload.expires_at,
    };
    if let Ok(body) = serde_json::to_vec(&index) {
        let _ = collector_service::platform::write_private_file(&index_path(), body);
    }
    if persist_path().exists() {
        let _ = fs::remove_file(persist_path());
    }
}

fn load_account(origin: &Url) -> Option<(BTreeMap<String, String>, String)> {
    let origin_s = origin.as_str();
    let account = session_account(origin_s);
    match platform_credentials::get(DESKTOP_SERVICE, &account) {
        Ok(secret) => parse_session_secret(&secret, origin_s),
        Err(CredentialError::NotFound) => migrate_legacy_session(origin_s),
        Err(_) => None,
    }
}

fn parse_session_secret(secret: &str, origin: &str) -> Option<(BTreeMap<String, String>, String)> {
    let stored: PersistedAccount = serde_json::from_str(secret).ok()?;
    if stored.origin != origin || stored.expires_at <= unix_now() || stored.cookies.is_empty() {
        return None;
    }
    Some((stored.cookies, stored.csrf))
}

fn migrate_legacy_session(origin: &str) -> Option<(BTreeMap<String, String>, String)> {
    let body = fs::read(persist_path()).ok()?;
    let stored: PersistedAccount = serde_json::from_slice(&body).ok()?;
    if stored.origin != origin || stored.expires_at <= unix_now() || stored.cookies.is_empty() {
        return None;
    }
    let cookies = stored.cookies.clone();
    let csrf = stored.csrf.clone();
    if let Ok(secret) = serde_json::to_string(&stored) {
        let account = session_account(origin);
        if platform_credentials::put(DESKTOP_SERVICE, &account, &secret).is_ok() {
            if platform_credentials::get(DESKTOP_SERVICE, &account)
                .ok()
                .as_deref()
                == Some(secret.as_str())
            {
                let _ = fs::remove_file(persist_path());
            }
        }
    }
    Some((cookies, csrf))
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
    if crate::local_test::enabled() {
        return Ok(AccountSession { user: None });
    }
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

const LOGIN_TIMEOUT: Duration = Duration::from_secs(10 * 60);

pub(crate) async fn authorize_saved_credentials(website: Option<String>) -> Result<(), String> {
    if crate::local_test::enabled() || !platform_credentials::uses_login_keychain() {
        return Ok(());
    }
    let mut accounts = vec![
        platform_credentials::WAL_KEY_ACCOUNT.to_owned(),
        platform_credentials::DEVICE_SEED_ACCOUNT.to_owned(),
    ];
    let saved_origin = website.or_else(|| {
        let bytes = fs::read(index_path()).ok()?;
        let index: SessionIndex = serde_json::from_slice(&bytes).ok()?;
        Some(index.origin)
    });
    if let Some(origin) = saved_origin.and_then(|value| account_origin(&value).ok()) {
        accounts.push(session_account(origin.as_str()));
    }
    tokio::task::spawn_blocking(move || platform_credentials::authorize_login_keychain(&accounts))
        .await
        .map_err(|error| error.to_string())?
        .map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn login_account(
    website: String,
    mode: Option<String>,
    state: State<'_, AccountState>,
    app: State<'_, AppState>,
) -> Result<AccountSession, String> {
    if crate::local_test::enabled() {
        return Err("LOCAL_TEST_MODE".into());
    }
    authorize_saved_credentials(Some(website.clone())).await?;
    wait_for_login(browser_login(website, mode, state, app)).await
}

fn browser_login_url(
    origin: &Url,
    mode: Option<&str>,
    redirect: &str,
    challenge: &str,
    nonce: &str,
) -> Result<Url, String> {
    let mut return_to = Url::parse("https://local.invalid/desktop-login").unwrap();
    return_to
        .query_pairs_mut()
        .append_pair("redirect_uri", redirect)
        .append_pair("code_challenge", challenge)
        .append_pair("state", nonce);
    let page = match mode {
        None | Some("login") => "login",
        Some("register") => "register",
        _ => return Err("INVALID_LOGIN_MODE".into()),
    };
    let mut login_url = origin.join(page).map_err(|_| "INVALID_WEBSITE")?;
    login_url.query_pairs_mut().append_pair(
        "return_to",
        &format!("{}?{}", return_to.path(), return_to.query().unwrap()),
    );
    Ok(login_url)
}

async fn wait_for_login<T>(
    future: impl std::future::Future<Output = Result<T, String>>,
) -> Result<T, String> {
    tokio::time::timeout(LOGIN_TIMEOUT, future)
        .await
        .map_err(|_| "LOGIN_TIMEOUT".to_string())?
}

async fn browser_login(
    website: String,
    mode: Option<String>,
    state: State<'_, AccountState>,
    app: State<'_, AppState>,
) -> Result<AccountSession, String> {
    let _login = state.1.try_lock().map_err(|_| "LOGIN_IN_PROGRESS")?;
    let generation = state.2.fetch_add(1, Ordering::SeqCst) + 1;
    let origin = account_origin(&website)?;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .map_err(|_| "CALLBACK_UNAVAILABLE")?;
    let redirect = format!(
        "http://127.0.0.1:{}/callback",
        listener
            .local_addr()
            .map_err(|_| "CALLBACK_UNAVAILABLE")?
            .port()
    );
    let verifier = format!(
        "{}{}",
        uuid::Uuid::new_v4().simple(),
        uuid::Uuid::new_v4().simple()
    );
    let nonce = format!(
        "{}{}",
        uuid::Uuid::new_v4().simple(),
        uuid::Uuid::new_v4().simple()
    );
    let challenge = format!("{:x}", Sha256::digest(verifier.as_bytes()));
    let login_url = browser_login_url(&origin, mode.as_deref(), &redirect, &challenge, &nonce)?;
    tauri::async_runtime::spawn_blocking(move || {
        super::window::open_website(login_url.to_string())
    })
    .await
    .map_err(|_| "BROWSER_OPEN_FAILED")??;
    let (mut stream, code) = wait_browser_callback(&listener, &redirect, &nonce).await?;
    let mut current = Connection::new(origin)?;
    let result: Result<AccountSession, String> = async {
        let session = current
            .finish_browser_login(&code, &verifier, &redirect)
            .await?;
        let mut guard = state.0.lock().await;
        if state.2.load(Ordering::SeqCst) != generation {
            return Err("LOGIN_CANCELLED".into());
        }
        save_account(&current);
        *guard = Some(current);
        Ok(session)
    }
    .await;
    let message = if result.is_ok() {
        "TokenDance 登录成功。可以关闭此页面并返回桌面应用。<br>Signed in. You can close this page and return to TokenDance."
    } else {
        "登录未完成，请返回 TokenDance 重试。<br>Sign-in failed. Return to TokenDance and retry."
    };
    let _ = send_browser_result(&mut stream, "200 OK", message).await;
    let session = result?;
    if let Some(user) = &session.user {
        let _ = app.activate_sync_account(&user.user_id).await;
    }
    *app.sync_status.write().await = "WAITING".into();
    Ok(session)
}

async fn send_browser_result(
    stream: &mut tokio::net::TcpStream,
    status: &str,
    message: &str,
) -> Result<(), String> {
    let body = format!("<!doctype html><html lang=zh-CN><meta charset=utf-8><meta name=viewport content=\"width=device-width,initial-scale=1\"><title>TokenDance</title><body><h1>TokenDance</h1><p>{message}</p></body></html>");
    let response = format!("HTTP/1.1 {status}\r\nContent-Type: text/html; charset=utf-8\r\nCache-Control: no-store\r\nReferrer-Policy: no-referrer\r\nContent-Security-Policy: default-src 'none'; frame-ancestors 'none'\r\nConnection: close\r\nContent-Length: {}\r\n\r\n{body}", body.len());
    tokio::time::timeout(
        Duration::from_secs(3),
        stream.write_all(response.as_bytes()),
    )
    .await
    .map_err(|_| "CALLBACK_ERROR")?
    .map_err(|_| "CALLBACK_ERROR")?;
    Ok(())
}

fn callback_code(request: &str, redirect: &str, state: &str) -> Option<String> {
    let mut lines = request.split("\r\n");
    let mut line = lines.next()?.split_whitespace();
    if line.next()? != "GET" {
        return None;
    }
    let target = line.next()?;
    if !target.starts_with("/callback?") {
        return None;
    }
    let expected = Url::parse(redirect).ok()?;
    let host = lines.find_map(|line| {
        let (key, value) = line.split_once(':')?;
        key.eq_ignore_ascii_case("host").then(|| value.trim())
    })?;
    if host != format!("127.0.0.1:{}", expected.port()?) {
        return None;
    }
    let url = expected.join(target).ok()?;
    let pairs: Vec<_> = url.query_pairs().collect();
    if pairs.len() != 2 || pairs.iter().filter(|(key, _)| key == "state").count() != 1 {
        return None;
    }
    if pairs.iter().find(|(key, _)| key == "state")?.1 != state {
        return None;
    }
    let code = &pairs.iter().find(|(key, _)| key == "code")?.1;
    if code.len() < 32
        || code.len() > 128
        || !code
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-')
    {
        return None;
    }
    Some(code.to_string())
}

async fn wait_browser_callback(
    listener: &tokio::net::TcpListener,
    redirect: &str,
    state: &str,
) -> Result<(tokio::net::TcpStream, String), String> {
    loop {
        let (mut stream, _) = listener.accept().await.map_err(|_| "CALLBACK_ERROR")?;
        let request = tokio::time::timeout(Duration::from_secs(3), async {
            let mut data = Vec::new();
            let mut chunk = [0u8; 1024];
            while data.len() < 8192 {
                let count = stream.read(&mut chunk).await.ok()?;
                if count == 0 {
                    return None;
                }
                data.extend_from_slice(&chunk[..count]);
                if data.windows(4).any(|bytes| bytes == b"\r\n\r\n") {
                    return String::from_utf8(data).ok();
                }
            }
            None
        })
        .await
        .ok()
        .flatten();
        if let Some(code) = request.and_then(|request| callback_code(&request, redirect, state)) {
            return Ok((stream, code));
        }
        let _ =
            send_browser_result(&mut stream, "400 Bad Request", "Invalid sign-in callback.").await;
    }
}

#[tauri::command]
pub async fn logout_account(
    website: String,
    state: State<'_, AccountState>,
    app: State<'_, AppState>,
) -> Result<(), String> {
    if crate::local_test::enabled() {
        return Ok(());
    }
    state.2.fetch_add(1, Ordering::SeqCst);
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
    let _ = app.deactivate_sync_account();
    *app.sync_status.write().await = "LOGIN_REQUIRED".into();
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    #[test]
    fn login_and_registration_keep_callback_and_deployment_path() {
        let origin = account_origin("https://example.test/token-dance").unwrap();
        for mode in ["login", "register"] {
            let url = browser_login_url(
                &origin,
                Some(mode),
                "http://127.0.0.1:1234/callback",
                "challenge",
                "nonce",
            )
            .unwrap();
            assert_eq!(url.path(), format!("/token-dance/{mode}"));
            let target = url
                .query_pairs()
                .find(|(k, _)| k == "return_to")
                .unwrap()
                .1
                .into_owned();
            let target = Url::parse(&format!("https://example.test{target}")).unwrap();
            assert_eq!(target.path(), "/desktop-login");
            assert!(target
                .query_pairs()
                .any(|(k, v)| k == "redirect_uri" && v == "http://127.0.0.1:1234/callback"));
            assert!(target
                .query_pairs()
                .any(|(k, v)| k == "state" && v == "nonce"));
            assert!(target
                .query_pairs()
                .any(|(k, v)| k == "code_challenge" && v == "challenge"));
        }
        assert!(browser_login_url(&origin, Some("https://evil.test"), "", "", "").is_err());
    }

    #[tokio::test(start_paused = true)]
    async fn browser_wait_expires_at_ten_minutes_and_releases_login_lock() {
        let lock = Arc::new(Mutex::new(()));
        let task_lock = lock.clone();
        let task = tokio::spawn(async move {
            wait_for_login(async move {
                let _guard = task_lock.lock().await;
                std::future::pending::<Result<(), String>>().await
            })
            .await
        });
        tokio::task::yield_now().await;
        tokio::time::advance(Duration::from_secs(599)).await;
        assert!(!task.is_finished());
        assert!(lock.try_lock().is_err());
        tokio::time::advance(Duration::from_secs(1)).await;
        assert_eq!(task.await.unwrap(), Err("LOGIN_TIMEOUT".into()));
        assert!(lock.try_lock().is_ok());
    }

    #[test]
    fn account_user_preserves_avatar_and_accepts_older_sessions() {
        let mut value = json!({"userId":"user", "displayName":"Jiayu", "handle":"jayzhang", "avatarUrl":"/api/v1/public/avatars/current"});
        let user: AccountUser = serde_json::from_value(value.clone()).unwrap();
        assert_eq!(
            serde_json::to_value(user).unwrap()["avatarUrl"],
            value["avatarUrl"]
        );
        value.as_object_mut().unwrap().remove("avatarUrl");
        assert!(serde_json::from_value::<AccountUser>(value)
            .unwrap()
            .avatar_url
            .is_none());
    }

    #[test]
    fn browser_callback_is_bound_to_loopback_host_path_and_state() {
        let redirect = "http://127.0.0.1:49152/callback";
        let code = "c".repeat(43);
        let valid = format!(
            "GET /callback?code={code}&state=expected HTTP/1.1\r\nHost: 127.0.0.1:49152\r\n\r\n"
        );
        assert_eq!(callback_code(&valid, redirect, "expected"), Some(code));
        for invalid in [
            valid.replace("state=expected", "state=wrong"),
            valid.replace("Host: 127.0.0.1", "Host: evil.example"),
            valid.replace("GET /callback", "GET /other"),
            valid.replace("GET ", "POST "),
            valid.replace("state=expected", "state=expected&state=expected"),
        ] {
            assert!(callback_code(&invalid, redirect, "expected").is_none());
        }
    }

    #[tokio::test]
    async fn browser_exchange_checks_real_session_without_exposing_secrets() {
        let (origin, server) = mock_server(vec![
            response(
                "200 OK",
                "Set-Cookie: tokendance_session=desktop-session; HttpOnly; Path=/\r\n",
                r#"{"csrfToken":"desktop-csrf"}"#,
            ),
            session_response(),
        ]);
        let mut client = Connection::new(origin).unwrap();
        let session = client
            .finish_browser_login(
                "one-time-code",
                "native-verifier",
                "http://127.0.0.1:49152/callback",
            )
            .await
            .unwrap();
        assert!(session.user.is_some());
        let json = serde_json::to_string(&session).unwrap();
        assert!(!json.contains("desktop-session") && !json.contains("desktop-csrf"));
        let requests = server.join().unwrap();
        assert!(requests[0].starts_with("POST /api/v1/auth/desktop/exchange "));
        assert!(requests[0].contains("native-verifier"));
        assert!(!requests[0].contains("password"));
        assert!(requests[1].contains("tokendance_session=desktop-session"));
    }

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
                    let response = if response.contains("$AGGREGATE") {
                        let (_,raw)=request.split_once("\r\n\r\n").unwrap();
                        let value:Value=serde_json::from_str(raw).unwrap();
                        let digest=Sha256::digest(raw.as_bytes()).iter().map(|b|format!("{b:02x}")).collect::<String>();
                        let body=serde_json::json!({"day":value["day"],"revision":value["revision"],"sha256":digest}).to_string();
                        format!("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",body.len(),body)
                    } else if response.contains("$BATCH") {
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
    async fn auto_sync_uses_pipeline_v2_and_skips_legacy_snapshot_routes() {
        let (_root, app) = crate::auto_sync::tests::seeded_app().await;
        assert!(app.pipeline_writer().is_some());
        let (origin, server) = mock_server(vec![
            response("201 Created", "", r#"{"grantToken":"dgt_fixture"}"#),
            response(
                "200 OK",
                "",
                r#"{"installationId":"ins_fixture","status":"active","statusVersion":1}"#,
            ),
            session_response(),
            response(
                "200 OK",
                "",
                r#"{"protocolVersion":2,"supportedSchemaVersions":[2],"supportedMetricSemanticsVersions":[1],"maxBatchEvents":500,"maxBatchBytes":1048576,"serverTimeMs":"1700000000000","eventReceiveLowerBoundMs":"1698000000000"}"#,
            ),
        ]);
        let origin = origin.join("token-dance/").unwrap();
        let mut client = Connection::new(origin).unwrap();
        client
            .cookies
            .insert("tokendance_session".into(), "fixture-session".into());
        client.csrf = "fixture-csrf".into();
        client
            .register_sync_device(
                Arc::new(InMemoryDeviceSigner::from_seed([7; 32])),
                "usr_fixture",
            )
            .await
            .unwrap();
        let account = AccountState(Mutex::new(Some(client)), Mutex::new(()), AtomicU64::new(0));
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "SYNCED");
        let requests = server.join().unwrap();
        assert!(requests[0].starts_with("POST /token-dance/api/v1/me/device-grants "));
        assert!(requests[1].starts_with("POST /token-dance/v1/installations/register "));
        assert!(requests[2].starts_with("GET /token-dance/api/v1/auth/session "));
        assert!(requests
            .iter()
            .any(|r| r.contains("GET /token-dance/v2/telemetry/capabilities")));
        assert!(!requests
            .iter()
            .any(|r| r.contains("/v1/telemetry/cursor") || r.contains("/v1/telemetry/aggregates")));
        *account.0.lock().await = None;
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "LOGIN_REQUIRED");
    }

    #[tokio::test]
    async fn expired_session_never_uploads_pending_records() {
        let (_root, app) = crate::auto_sync::tests::seeded_app().await;
        let (origin, server) = mock_server(vec![response("204 No Content", "", "")]);
        let mut client = Connection::new(origin).unwrap();
        client
            .cookies
            .insert("tokendance_session".into(), "expired".into());
        let account = AccountState(Mutex::new(Some(client)), Mutex::new(()), AtomicU64::new(0));
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "LOGIN_REQUIRED");
        assert_eq!(app.lock_store().event_count(), 2);
        assert_eq!(server.join().unwrap().len(), 1);
    }

    #[tokio::test]
    async fn account_switch_without_pipeline_queue_does_not_hit_legacy_ingest() {
        let (_root, app) = crate::auto_sync::tests::seeded_app().await;
        app.activate_sync_account("account-a").await.unwrap();
        let (origin, server) = mock_server(vec![
            session_response(),
            response(
                "200 OK",
                "",
                r#"{"protocolVersion":2,"supportedSchemaVersions":[2],"supportedMetricSemanticsVersions":[1],"maxBatchEvents":500,"maxBatchBytes":1048576,"serverTimeMs":"1700000000000","eventReceiveLowerBoundMs":"1698000000000"}"#,
            ),
        ]);
        let mut client = Connection::new(origin.clone()).unwrap();
        client
            .cookies
            .insert("tokendance_session".into(), "fixture-session".into());
        let signer = Arc::new(InMemoryDeviceSigner::from_seed([7; 32]));
        client.transport = Some(HttpTransport::new_claimed(
            origin.as_str(),
            client.client.clone(),
            "ins_fixture",
            Arc::clone(&signer) as Arc<dyn DeviceSigner>,
        ));
        client.telemetry_v2 = Some(Arc::new(HttpTelemetryV2::new(
            origin.as_str(),
            client.client.clone(),
            "ins_fixture",
            signer,
        )));
        client.binding_generation = 1;
        let account = AccountState(Mutex::new(Some(client)), Mutex::new(()), AtomicU64::new(0));
        account.auto_sync_tick(&app).await;
        assert_eq!(app.sync_status.read().await.as_str(), "SYNCED");
        let requests = server.join().unwrap();
        assert!(requests[0].starts_with("GET /api/v1/auth/session "));
        assert!(!requests.iter().any(|request| {
            request.contains("/v1/telemetry/batches")
                || request.contains("/v1/telemetry/aggregates")
                || request.contains("/v1/telemetry/cursor")
        }));
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
