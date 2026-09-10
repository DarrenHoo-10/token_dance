use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

use adapter_grok_build::{
    code_record_from_signals, find_signals_json, parse_session_end, SessionEndNotice, HOOK_PATH,
    HOOK_PORT, HOOK_SOURCE_ID,
};
use adapter_sdk::{RawFrame, SourceKind};
use protocol::EventEnvelope;

use crate::ProductionService;

const MAX_BODY: usize = 64 * 1024;

#[derive(Clone, Default)]
pub struct GrokHookInbox {
    pending: Arc<Mutex<Vec<SessionEndNotice>>>,
}

impl GrokHookInbox {
    pub fn push(&self, notice: SessionEndNotice) {
        if let Ok(mut pending) = self.pending.lock() {
            pending.push(notice);
        }
    }

    pub fn drain(&self) -> Vec<SessionEndNotice> {
        self.pending.lock().map(|mut pending| pending.split_off(0)).unwrap_or_default()
    }
}

pub fn start_listener(token: String, inbox: GrokHookInbox) -> std::io::Result<()> {
    let listener = TcpListener::bind(("127.0.0.1", HOOK_PORT))?;
    listener.set_nonblocking(false)?;
    thread::Builder::new()
        .name("grok-session-end-hook".into())
        .spawn(move || {
            for stream in listener.incoming().flatten() {
                let _ = handle_stream(&token, &inbox, stream);
            }
        })?;
    Ok(())
}

fn handle_stream(token: &str, inbox: &GrokHookInbox, mut stream: TcpStream) -> std::io::Result<()> {
    stream.set_read_timeout(Some(Duration::from_millis(800)))?;
    stream.set_write_timeout(Some(Duration::from_millis(800)))?;
    let peer = stream.peer_addr()?;
    if !peer.ip().is_loopback() {
        let _ = stream.write_all(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n");
        return Ok(());
    }
    let mut buf = vec![0_u8; 8192];
    let mut received = Vec::new();
    loop {
        let n = stream.read(&mut buf)?;
        if n == 0 {
            break;
        }
        received.extend_from_slice(&buf[..n]);
        if received.len() > MAX_BODY + 2048 {
            let _ = stream.write_all(b"HTTP/1.1 413 Payload Too Large\r\nContent-Length: 0\r\n\r\n");
            return Ok(());
        }
        if received.windows(4).any(|w| w == b"\r\n\r\n") {
            break;
        }
    }
    let Some(split) = received.windows(4).position(|w| w == b"\r\n\r\n") else {
        let _ = stream.write_all(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n");
        return Ok(());
    };
    let header = String::from_utf8_lossy(&received[..split]);
    let first = header.lines().next().unwrap_or("");
    if !first.starts_with("POST ") {
        let _ = stream.write_all(b"HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n");
        return Ok(());
    }
    let path = first.split_whitespace().nth(1).unwrap_or("");
    let (path_only, query) = path.split_once('?').unwrap_or((path, ""));
    if path_only != HOOK_PATH {
        let _ = stream.write_all(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n");
        return Ok(());
    }
    let provided = query.split('&').find_map(|part| {
        part.strip_prefix("token=").map(str::to_owned)
    });
    if provided.as_deref() != Some(token) {
        let _ = stream.write_all(b"HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n");
        return Ok(());
    }
    let content_length = header
        .lines()
        .find_map(|line| {
            line.split_once(':').and_then(|(name, value)| {
                name.eq_ignore_ascii_case("content-length")
                    .then(|| value.trim().parse::<usize>().ok())
                    .flatten()
            })
        })
        .unwrap_or(0);
    if content_length > MAX_BODY {
        let _ = stream.write_all(b"HTTP/1.1 413 Payload Too Large\r\nContent-Length: 0\r\n\r\n");
        return Ok(());
    }
    let mut body = received[split + 4..].to_vec();
    while body.len() < content_length {
        let n = stream.read(&mut buf)?;
        if n == 0 {
            break;
        }
        body.extend_from_slice(&buf[..n]);
        if body.len() > MAX_BODY {
            let _ = stream.write_all(b"HTTP/1.1 413 Payload Too Large\r\nContent-Length: 0\r\n\r\n");
            return Ok(());
        }
    }
    body.truncate(content_length);
    if let Ok(value) = serde_json::from_slice::<serde_json::Value>(&body) {
        if let Some(notice) = parse_session_end(&value) {
            inbox.push(notice);
        }
    }
    stream.write_all(b"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")?;
    Ok(())
}

pub fn take_hook_frames(
    installation_id: &str,
    inbox: &GrokHookInbox,
    sessions_root: &Path,
) -> Vec<RawFrame> {
    let mut frames = Vec::new();
    for notice in inbox.drain() {
        let Some(path) = find_signals_json(sessions_root, &notice.session_id) else {
            continue;
        };
        let Ok(bytes) = std::fs::read(&path) else {
            continue;
        };
        let Ok(signals) = serde_json::from_slice(&bytes) else {
            continue;
        };
        let Some(record) = code_record_from_signals(
            &signals,
            &notice.session_id,
            notice.occurred_at.as_deref(),
        ) else {
            continue;
        };
        let Ok(payload) = serde_json::to_vec(&record) else {
            continue;
        };
        frames.push(RawFrame {
            installation_id: installation_id.to_string(),
            source_kind: SourceKind::RuntimeStream,
            source_id: HOOK_SOURCE_ID.into(),
            cursor: format!("hook:{}", notice.session_id),
            payload,
        });
    }
    frames
}

pub async fn decode_pending_session_ends(
    service: &ProductionService,
    inbox: &GrokHookInbox,
    sessions_root: &Path,
) -> Vec<EventEnvelope> {
    let mut events = Vec::new();
    for frame in take_hook_frames(service.collector.installation_id(), inbox, sessions_root) {
        if let Ok(decoded) = service
            .collector
            .decode(adapter_grok_build::ADAPTER_ID, frame)
            .await
        {
            events.extend(decoded.into_iter().map(|item| item.into_envelope()));
        }
    }
    events
}

pub fn grok_sessions_root() -> Option<std::path::PathBuf> {
    std::env::var_os("USERPROFILE")
        .or_else(|| std::env::var_os("HOME"))
        .map(std::path::PathBuf::from)
        .map(|home| home.join(".grok").join("sessions"))
}

pub fn grok_user_home() -> Option<std::path::PathBuf> {
    std::env::var_os("USERPROFILE")
        .or_else(|| std::env::var_os("HOME"))
        .map(std::path::PathBuf::from)
}
