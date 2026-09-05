use std::collections::BTreeMap;
use std::fs;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use reqwest::{Client, Method, StatusCode};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tauri::{State, Url};
use tokio::sync::Mutex;

// Passwords are never saved. Session cookies stay in native memory, scoped to
// one website origin, and are never returned across the WebView IPC boundary.
#[derive(Default)]
pub struct AccountState(Mutex<Option<Connection>>);

struct Connection {
    origin: Url,
    client: Client,
    cookies: BTreeMap<String, String>,
    csrf: String,
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
    let url = Url::parse(website).map_err(|_| "INVALID_WEBSITE")?;
    let local = matches!(url.host_str(), Some("localhost" | "127.0.0.1" | "[::1]"));
    if (url.scheme() != "https" && !(url.scheme() == "http" && local))
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err("HTTPS_REQUIRED".into());
    }
    Url::parse(&format!("{}/", url.origin().ascii_serialization()))
        .map_err(|_| "INVALID_WEBSITE".into())
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
        })
    }

    async fn request(
        &mut self,
        method: Method,
        path: &str,
        body: Option<Value>,
    ) -> Result<Option<Value>, String> {
        let url = self.origin.join(path).map_err(|_| "INVALID_WEBSITE")?;
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
    if stored.origin != origin.as_str() || stored.expires_at <= unix_now() || stored.cookies.is_empty()
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
) -> Result<AccountSession, String> {
    if email.trim().is_empty() || password.is_empty() {
        return Err("MISSING_CREDENTIALS".into());
    }
    let mut guard = state.0.lock().await;
    let current = connection(&mut guard, account_origin(&website)?)?;
    // Read back the authenticated session, rather than trusting a successful POST alone.
    let session = current.login(&email, &password).await?;
    save_account(current);
    Ok(session)
}

#[tauri::command]
pub async fn logout_account(website: String, state: State<'_, AccountState>) -> Result<(), String> {
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
                    socket.write_all(response.as_bytes()).unwrap();
                    String::from_utf8(bytes).unwrap()
                })
                .collect()
        });
        (origin, handle)
    }

    fn response(status: &str, headers: &str, body: &str) -> String {
        format!("HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n{headers}\r\n{body}", body.len())
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
                == "https://example.com/"
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
}
