//! Loopback-only, read-only HTTP server for the local dashboard.
//!
//! Security model (plan §3.4):
//! * binds `127.0.0.1` only;
//! * a one-time launch token travels in the URL fragment, never in a request line, and is
//!   redeemed exactly once for an HttpOnly session cookie;
//! * `Host` must be a loopback name with our port (DNS-rebinding), and `Origin` /
//!   `Sec-Fetch-Site`, when present, must be same-origin;
//! * data endpoints require the session cookie; nothing here writes.

use std::io::Read;
use std::net::{Ipv4Addr, SocketAddr};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;

use collector_service::AppPaths;
use rand::RngCore;
use serde_json::{json, Value};
use tiny_http::{Header, Method, Request, Response, Server};

use crate::error::{CliError, CliResult};
use crate::snapshot::{self, Range};

const INDEX: &str = include_str!("../ui/index.html");
const APP_JS: &str = include_str!("../ui/app.js");
const STYLES: &str = include_str!("../ui/styles.css");
const LOGO: &[u8] = include_bytes!("../ui/logo.png");
const COOKIE: &str = "td_session";
const MAX_BODY: u64 = 1024;
const WORKERS: usize = 4;

const CSP: &str = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; \
connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

struct Shared {
    paths: AppPaths,
    port: u16,
    launch_token: String,
    redeemed: AtomicBool,
    session: Mutex<Option<String>>,
}

pub struct Dashboard {
    server: Arc<Server>,
    threads: Vec<JoinHandle<()>>,
    pub port: u16,
    pub launch_token: String,
}

fn random_hex() -> String {
    let mut bytes = [0u8; 32];
    rand::rngs::OsRng.fill_bytes(&mut bytes);
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// Compares without an early exit so response time does not leak a matching prefix.
fn same_secret(a: &str, b: &str) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.bytes()
        .zip(b.bytes())
        .fold(0u8, |acc, (x, y)| acc | (x ^ y))
        == 0
}

impl Dashboard {
    pub fn start(paths: AppPaths, port: u16) -> CliResult<Self> {
        let addr = SocketAddr::from((Ipv4Addr::LOCALHOST, port));
        let server = Server::http(addr).map_err(|e| {
            CliError::new(
                crate::error::Kind::Incomplete,
                format!("cannot listen on {addr}: {e}"),
            )
            .hint("pick another port with --port, or omit it to let the system choose")
        })?;
        let bound = server
            .server_addr()
            .to_ip()
            .map(|a| a.port())
            .ok_or_else(|| CliError::internal("the server did not bind a TCP address"))?;
        let server = Arc::new(server);
        let shared = Arc::new(Shared {
            paths,
            port: bound,
            launch_token: random_hex(),
            redeemed: AtomicBool::new(false),
            session: Mutex::new(None),
        });
        let launch_token = shared.launch_token.clone();
        let mut threads = Vec::new();
        for n in 0..WORKERS {
            let server = Arc::clone(&server);
            let shared = Arc::clone(&shared);
            threads.push(
                std::thread::Builder::new()
                    .name(format!("dashboard-{n}"))
                    .spawn(move || {
                        while let Ok(request) = server.recv() {
                            handle(&shared, request);
                        }
                    })
                    .map_err(|e| CliError::internal(e.to_string()))?,
            );
        }
        Ok(Self {
            server,
            threads,
            port: bound,
            launch_token,
        })
    }

    pub fn url(&self) -> String {
        format!(
            "http://127.0.0.1:{}/#launch={}",
            self.port, self.launch_token
        )
    }

    pub fn stop(self) {
        self.server.unblock();
        for _ in 0..self.threads.len() {
            self.server.unblock();
        }
        for thread in self.threads {
            let _ = thread.join();
        }
    }
}

fn header(name: &str, value: &str) -> Header {
    Header::from_bytes(name.as_bytes(), value.as_bytes()).expect("static header")
}

fn security_headers<R: Read>(mut response: Response<R>) -> Response<R> {
    for (name, value) in [
        ("Cache-Control", "no-store"),
        ("Content-Security-Policy", CSP),
        ("X-Content-Type-Options", "nosniff"),
        ("Referrer-Policy", "no-referrer"),
        ("Cross-Origin-Resource-Policy", "same-origin"),
        ("Cross-Origin-Opener-Policy", "same-origin"),
        ("X-Frame-Options", "DENY"),
    ] {
        response.add_header(header(name, value));
    }
    response
}

fn respond(
    request: Request,
    status: u16,
    content_type: &str,
    body: Vec<u8>,
    cookie: Option<String>,
) {
    let mut response = security_headers(Response::from_data(body).with_status_code(status));
    response.add_header(header("Content-Type", content_type));
    if let Some(cookie) = cookie {
        response.add_header(header("Set-Cookie", &cookie));
    }
    let _ = request.respond(response);
}

fn json_response(request: Request, status: u16, body: Value, cookie: Option<String>) {
    respond(
        request,
        status,
        "application/json; charset=utf-8",
        serde_json::to_vec(&body).unwrap_or_default(),
        cookie,
    );
}

fn error_body(code: &str, message: &str) -> Value {
    json!({
        "schema_version": crate::SCHEMA_VERSION,
        "ok": false,
        "error": { "code": code, "message": message },
    })
}

fn request_header(request: &Request, name: &str) -> Option<String> {
    request
        .headers()
        .iter()
        .find(|h| h.field.as_str().as_str().eq_ignore_ascii_case(name))
        .map(|h| h.value.as_str().to_string())
}

fn host_allowed(host: &str, port: u16) -> bool {
    host == format!("127.0.0.1:{port}") || host == format!("localhost:{port}")
}

fn origin_allowed(origin: &str, port: u16) -> bool {
    origin == format!("http://127.0.0.1:{port}") || origin == format!("http://localhost:{port}")
}

fn cookie_value(request: &Request) -> Option<String> {
    let raw = request_header(request, "Cookie")?;
    raw.split(';')
        .filter_map(|part| part.trim().split_once('='))
        .find(|(name, _)| *name == COOKIE)
        .map(|(_, value)| value.to_string())
}

fn handle(shared: &Shared, request: Request) {
    // 1. DNS-rebinding and cross-origin gates run before any routing.
    match request_header(&request, "Host") {
        Some(host) if host_allowed(&host, shared.port) => {}
        _ => {
            return json_response(
                request,
                421,
                error_body("BAD_HOST", "unexpected Host header"),
                None,
            )
        }
    }
    if let Some(origin) = request_header(&request, "Origin") {
        if !origin_allowed(&origin, shared.port) {
            return json_response(
                request,
                403,
                error_body("BAD_ORIGIN", "cross-origin request refused"),
                None,
            );
        }
    }
    if let Some(site) = request_header(&request, "Sec-Fetch-Site") {
        if site != "same-origin" && site != "none" {
            return json_response(
                request,
                403,
                error_body("BAD_ORIGIN", "cross-site request refused"),
                None,
            );
        }
    }
    let method = request.method().clone();
    let url = request.url().to_string();
    let (path, query) = url.split_once('?').unwrap_or((url.as_str(), ""));

    match (&method, path) {
        (Method::Get | Method::Head, "/") => {
            respond(request, 200, "text/html; charset=utf-8", INDEX.into(), None)
        }
        (Method::Get | Method::Head, "/app.js") => respond(
            request,
            200,
            "text/javascript; charset=utf-8",
            APP_JS.into(),
            None,
        ),
        (Method::Get | Method::Head, "/styles.css") => {
            respond(request, 200, "text/css; charset=utf-8", STYLES.into(), None)
        }
        (Method::Get | Method::Head, "/logo.png") => {
            respond(request, 200, "image/png", LOGO.to_vec(), None)
        }
        (Method::Post, "/api/v1/session") => redeem(shared, request),
        (Method::Get, "/api/v1/snapshot") => {
            if !authorized(shared, &request) {
                return json_response(
                    request,
                    401,
                    error_body(
                        "UNAUTHENTICATED",
                        "open the dashboard with a fresh launch link",
                    ),
                    None,
                );
            }
            let range = query
                .split('&')
                .find_map(|kv| kv.strip_prefix("range="))
                .unwrap_or("24h");
            match Range::parse(range)
                .and_then(|r| snapshot::build(&shared.paths, r, snapshot::now_ms()))
            {
                Ok(data) => json_response(
                    request,
                    200,
                    json!({ "schema_version": crate::SCHEMA_VERSION, "ok": true, "data": data }),
                    None,
                ),
                Err(e) => json_response(
                    request,
                    if e.kind == crate::error::Kind::InvalidInput {
                        400
                    } else {
                        500
                    },
                    e.to_json(),
                    None,
                ),
            }
        }
        (Method::Get | Method::Head | Method::Post, _) => json_response(
            request,
            404,
            error_body("NOT_FOUND", "no such resource"),
            None,
        ),
        _ => json_response(
            request,
            405,
            error_body("METHOD_NOT_ALLOWED", "this dashboard is read-only"),
            None,
        ),
    }
}

fn authorized(shared: &Shared, request: &Request) -> bool {
    let Some(cookie) = cookie_value(request) else {
        return false;
    };
    shared
        .session
        .lock()
        .ok()
        .and_then(|s| s.clone())
        .is_some_and(|expected| same_secret(&expected, &cookie))
}

/// Exchanges the launch token for a session cookie, once.
fn redeem(shared: &Shared, mut request: Request) {
    let mut body = String::new();
    let _ = request.as_reader().take(MAX_BODY).read_to_string(&mut body);
    let presented = serde_json::from_str::<Value>(&body)
        .ok()
        .and_then(|v| v.get("token").and_then(Value::as_str).map(str::to_string))
        .unwrap_or_default();
    if !same_secret(&shared.launch_token, &presented) {
        return json_response(
            request,
            401,
            error_body("BAD_TOKEN", "the launch link is not valid"),
            None,
        );
    }
    if shared.redeemed.swap(true, Ordering::AcqRel) {
        return json_response(
            request,
            401,
            error_body(
                "TOKEN_ALREADY_USED",
                "this launch link was already used; run `tokendance dashboard` again",
            ),
            None,
        );
    }
    let session = random_hex();
    if let Ok(mut slot) = shared.session.lock() {
        *slot = Some(session.clone());
    }
    let cookie = format!("{COOKIE}={session}; HttpOnly; SameSite=Strict; Path=/");
    json_response(
        request,
        200,
        json!({ "schema_version": crate::SCHEMA_VERSION, "ok": true }),
        Some(cookie),
    );
}
