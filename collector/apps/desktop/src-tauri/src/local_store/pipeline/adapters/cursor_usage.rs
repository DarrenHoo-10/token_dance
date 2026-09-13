//! Cursor's authenticated, per-call usage feed. Account totals are never imported.
//! Only conversations evidenced by this machine's CLI/IDE storage are admitted.
use std::collections::{HashMap, HashSet};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::Duration;

use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use reqwest::header::{HeaderValue, AUTHORIZATION};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

use super::common::{emit_usage_fact, UsageFactArgs};
use super::identity::TypedNativeKey;
use crate::local_store::pipeline::runner::{
    CheckpointView, FactDraft, RawBatch, RawRecord, ReadBudget, RunnerError, TimeSource,
    TokenAccuracy,
};

pub const STREAM_USAGE: &str = "official-usage-events";
const ENDPOINT: &str = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetFilteredUsageEvents";
const MAX_RESPONSE: u64 = 2 * 1024 * 1024;
const PAGE_SIZE: usize = 100;

#[derive(Clone)]
pub struct CursorUsagePaths {
    pub chats: PathBuf,
    pub database: PathBuf,
    pub auth: PathBuf,
}

impl CursorUsagePaths {
    pub fn for_home(home: &Path) -> Self {
        #[cfg(target_os = "windows")]
        let config = std::env::var_os("APPDATA")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join("AppData/Roaming"));
        #[cfg(target_os = "macos")]
        let config = home.join("Library/Application Support");
        #[cfg(not(any(target_os = "windows", target_os = "macos")))]
        let config = std::env::var_os("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join(".config"));
        Self {
            chats: home.join(".cursor/chats"),
            database: config.join("Cursor/User/globalStorage/state.vscdb"),
            auth: config.join("Cursor/auth.json"),
        }
    }
}

pub struct CursorUsageSource {
    pub paths: CursorUsagePaths,
    // Credentials/account identity stay in memory. On restart/change replay today's window.
    credential_identity: Mutex<Option<[u8; 32]>>,
    status: Mutex<&'static str>,
    local_cache: Mutex<Option<(std::time::Instant, HashSet<String>)>>,
    #[cfg(test)]
    pub endpoint: Option<String>,
}

fn failure(code: &str) -> RunnerError {
    RunnerError::Io(code.into())
}

impl CursorUsageSource {
    pub fn new(paths: CursorUsagePaths) -> Self {
        Self {
            paths,
            credential_identity: Mutex::new(None),
            status: Mutex::new("CONNECTING"),
            local_cache: Mutex::new(None),
            #[cfg(test)]
            endpoint: None,
        }
    }
    pub fn configured(&self) -> bool {
        self.paths.auth.exists() || self.paths.database.exists() || self.paths.chats.exists()
    }
    pub fn status(&self) -> &'static str {
        *self.status.lock().expect("cursor status")
    }
    pub fn locator(&self) -> String {
        self.paths
            .auth
            .with_file_name("usage-events")
            .to_string_lossy()
            .into_owned()
    }

    pub fn read(
        &self,
        transcripts: &Path,
        committed: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError> {
        let result = self.read_inner(transcripts, committed, budget);
        *self.status.lock().expect("cursor status") = match &result {
            Ok(_) => "ACTIVE",
            Err(RunnerError::Io(code)) if code == "CURSOR_AUTH_REQUIRED" => "AUTH_REQUIRED",
            Err(_) => "ERROR",
        };
        result
    }

    fn read_inner(
        &self,
        transcripts: &Path,
        committed: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError> {
        let token = load_token(&self.paths)?;
        let identity: [u8; 32] = Sha256::digest(token.as_bytes()).into();
        let mut previous = self.credential_identity.lock().expect("cursor credential");
        let rebuilding = committed.observed_boundary_json["_rebuild_pending"].as_bool() == Some(true);
        // Resume persisted history pagination after restart. A credential change
        // observed in this process restarts the window; local conversation matching still applies.
        let reset = previous.as_ref().is_some_and(|old| old != &identity)
            || (previous.is_none() && !rebuilding);
        // Do not install the new identity until a successful response has been checked.
        let now = chrono::Utc::now().timestamp_millis();
        let day_start = if rebuilding { 0 } else { (now + 28_800_000).div_euclid(86_400_000) * 86_400_000 - 28_800_000 };
        let mut cursor = if reset || committed.cursor_json["start"].as_i64() != Some(day_start) {
            json!({"start":day_start,"end":now,"page":1,"page_size":budget.max_records.min(PAGE_SIZE).max(1)})
        } else {
            committed.cursor_json.clone()
        };
        let local = {
            let mut cache = self.local_cache.lock().expect("cursor local conversations");
            if cache
                .as_ref()
                .is_none_or(|(at, _)| at.elapsed() >= Duration::from_secs(30))
            {
                *cache = Some((
                    std::time::Instant::now(),
                    local_conversations(&self.paths, transcripts)?,
                ));
            }
            cache.as_ref().expect("conversation cache").1.clone()
        };
        let mut auth = HeaderValue::from_str(&format!("Bearer {token}"))
            .map_err(|_| failure("CURSOR_AUTH_REQUIRED"))?;
        auth.set_sensitive(true);
        let endpoint = ENDPOINT;
        #[cfg(test)]
        let endpoint = self.endpoint.as_deref().unwrap_or(endpoint);
        let client = reqwest::blocking::Client::builder()
            .http1_only()
            .redirect(reqwest::redirect::Policy::none())
            .connect_timeout(Duration::from_secs(5))
            .timeout(Duration::from_secs(12))
            .user_agent("TokenDance")
            .build()
            .map_err(|_| failure("CURSOR_NETWORK_ERROR"))?;
        let response = client.post(endpoint).header(AUTHORIZATION, auth)
            .header("Content-Type", "application/json").header("Connect-Protocol-Version", "1")
            .json(&json!({"teamId":0,"startDate":cursor["start"].as_i64().unwrap().to_string(),
                "endDate":cursor["end"].as_i64().ok_or_else(|| failure("CURSOR_INVALID_CURSOR"))?.to_string(),
                "page":cursor["page"],"pageSize":cursor["page_size"]}))
            .send().map_err(|_| failure("CURSOR_NETWORK_ERROR"))?;
        if matches!(response.status().as_u16(), 401 | 403) {
            return Err(failure("CURSOR_AUTH_REQUIRED"));
        }
        if response.status().as_u16() == 429 {
            return Err(failure("CURSOR_RATE_LIMITED"));
        }
        if !response.status().is_success()
            || response.content_length().is_some_and(|n| n > MAX_RESPONSE)
        {
            return Err(failure("CURSOR_USAGE_UNAVAILABLE"));
        }
        let mut bytes = Vec::new();
        response
            .take(MAX_RESPONSE + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| failure("CURSOR_NETWORK_ERROR"))?;
        if bytes.len() as u64 > MAX_RESPONSE {
            return Err(failure("CURSOR_RESPONSE_TOO_LARGE"));
        }
        let value: Value =
            serde_json::from_slice(&bytes).map_err(|_| failure("CURSOR_INVALID_RESPONSE"))?;
        if load_token(&self.paths)?.as_bytes() != token.as_bytes() {
            return Err(failure("CURSOR_ACCOUNT_CHANGED"));
        }
        let mut batch = project_page(&value, &local, &mut cursor)?;
        batch.bytes_read = bytes.len();
        *previous = Some(identity);
        Ok(batch)
    }
}

fn load_token(paths: &CursorUsagePaths) -> Result<String, RunnerError> {
    // The CLI login takes precedence, matching the quota reader. Never fall back to another account.
    let token = if paths.auth.exists() {
        let file = std::fs::File::open(&paths.auth).map_err(|_| failure("CURSOR_AUTH_REQUIRED"))?;
        let mut bytes = Vec::new();
        file.take(65537)
            .read_to_end(&mut bytes)
            .map_err(|_| failure("CURSOR_AUTH_REQUIRED"))?;
        if bytes.len() > 65536 {
            return Err(failure("CURSOR_AUTH_REQUIRED"));
        }
        let value: Value =
            serde_json::from_slice(&bytes).map_err(|_| failure("CURSOR_AUTH_REQUIRED"))?;
        value["accessToken"]
            .as_str()
            .ok_or_else(|| failure("CURSOR_AUTH_REQUIRED"))?
            .to_string()
    } else {
        let db = open_db(&paths.database)?;
        db.query_row("SELECT CAST(value AS TEXT) FROM ItemTable WHERE key='cursorAuth/accessToken' AND length(value)<=16384", [], |r| r.get(0))
            .map_err(|_| failure("CURSOR_AUTH_REQUIRED"))?
    };
    if token.len() > 16384
        || !token
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
    {
        return Err(failure("CURSOR_AUTH_REQUIRED"));
    }
    let parts: Vec<_> = token.split('.').collect();
    if parts.len() != 3 {
        return Err(failure("CURSOR_AUTH_REQUIRED"));
    }
    let claims: Value = URL_SAFE_NO_PAD
        .decode(parts[1])
        .ok()
        .and_then(|b| serde_json::from_slice(&b).ok())
        .ok_or_else(|| failure("CURSOR_AUTH_REQUIRED"))?;
    if claims["exp"]
        .as_i64()
        .is_none_or(|n| n <= chrono::Utc::now().timestamp())
        || claims["sub"].as_str().is_none_or(str::is_empty)
    {
        return Err(failure("CURSOR_AUTH_REQUIRED"));
    }
    Ok(token)
}

fn open_db(path: &Path) -> Result<rusqlite::Connection, RunnerError> {
    let db = rusqlite::Connection::open_with_flags(
        path,
        rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY | rusqlite::OpenFlags::SQLITE_OPEN_NO_MUTEX,
    )
    .map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?;
    db.busy_timeout(Duration::from_millis(500))
        .map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?;
    Ok(db)
}

fn add_id(ids: &mut HashSet<String>, id: &str) {
    // Exact native conversation ID only. Never strip cloud/subagent prefixes to infer ownership.
    if uuid::Uuid::parse_str(id).is_ok() {
        ids.insert(id.to_ascii_lowercase());
    }
}

pub fn local_conversations(
    paths: &CursorUsagePaths,
    transcripts: &Path,
) -> Result<HashSet<String>, RunnerError> {
    let mut ids = HashSet::new();
    // CLI metadata is hex-encoded JSON; no prompt/blobs/encryption keys leave this function.
    let mut stack = vec![(paths.chats.clone(), 0), (transcripts.to_path_buf(), 0)];
    while let Some((dir, depth)) = stack.pop() {
        if !dir.exists() {
            continue;
        }
        for entry in
            std::fs::read_dir(&dir).map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?
        {
            let entry = entry.map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?;
            let path = entry.path();
            if entry
                .file_type()
                .map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?
                .is_dir()
            {
                if depth < 6 {
                    stack.push((path, depth + 1));
                }
            } else if path.extension().is_some_and(|e| e == "jsonl") {
                if let Some(id) = path.file_stem().and_then(|s| s.to_str()) {
                    add_id(&mut ids, id);
                }
            } else if path.extension().is_some_and(|e| e == "db") && path.starts_with(&paths.chats)
            {
                let db = open_db(&path)?;
                let value: String = db
                    .query_row(
                        "SELECT value FROM meta WHERE key='0' AND length(value)<=65536",
                        [],
                        |r| r.get(0),
                    )
                    .map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?;
                let decoded: Option<Vec<u8>> = (0..value.len())
                    .step_by(2)
                    .map(|i| {
                        value
                            .get(i..i + 2)
                            .and_then(|s| u8::from_str_radix(s, 16).ok())
                    })
                    .collect();
                if let Some(meta) = decoded.and_then(|b| serde_json::from_slice::<Value>(&b).ok()) {
                    if let Some(id) = meta["agentId"].as_str() {
                        add_id(&mut ids, id);
                    }
                }
            }
        }
    }
    if paths.database.exists() {
        let db = open_db(&paths.database)?;
        // The IDE can have authentication/settings before it creates a conversation
        // table. CLI metadata and transcript IDs still prove local ownership.
        let has_conversations: bool = db.query_row(
            "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='cursorDiskKV')",
            [], |row| row.get(0),
        ).map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?;
        if !has_conversations {
            return Ok(ids);
        }
        let mut stmt = db
            .prepare("SELECT key FROM cursorDiskKV WHERE key LIKE 'composerData:%'")
            .map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?;
        for key in stmt
            .query_map([], |r| r.get::<_, String>(0))
            .map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?
        {
            let key = key.map_err(|_| failure("CURSOR_LOCAL_SOURCE_UNAVAILABLE"))?;
            if let Some(id) = key.strip_prefix("composerData:") {
                add_id(&mut ids, id);
            }
        }
    }
    Ok(ids)
}

fn number(value: &Value) -> Option<u64> {
    value.as_u64().or_else(|| value.as_str()?.parse().ok())
}
fn timestamp(value: &Value) -> Option<i64> {
    value.as_i64().or_else(|| value.as_str()?.parse().ok())
}

/// Project only confirmed local request details. No account, credential or monetary float is persisted.
fn project_page(
    value: &Value,
    local: &HashSet<String>,
    cursor: &mut Value,
) -> Result<RawBatch, RunnerError> {
    let rows = value["usageEventsDisplay"].as_array();
    let total = number(&value["totalUsageEventsCount"])
        .ok_or_else(|| failure("CURSOR_INVALID_RESPONSE"))?;
    let empty = Vec::new();
    let rows = match rows {
        Some(rows) => rows,
        None if total == 0 => &empty,
        _ => return Err(failure("CURSOR_INVALID_RESPONSE")),
    };
    let page = number(&cursor["page"]).ok_or_else(|| failure("CURSOR_INVALID_CURSOR"))?;
    let size = number(&cursor["page_size"]).ok_or_else(|| failure("CURSOR_INVALID_CURSOR"))?;
    if page == 0
        || size == 0
        || size > PAGE_SIZE as u64
        || rows.len() > size as usize
        || (rows.is_empty() && (page - 1) * size < total)
    {
        return Err(failure("CURSOR_INVALID_RESPONSE"));
    }
    let mut last = cursor["last_timestamp"].as_i64();
    let mut occurrences: HashMap<String, u64> =
        serde_json::from_value(cursor["occurrences"].clone()).unwrap_or_default();
    let mut records = Vec::new();
    for row in rows {
        let ts = timestamp(&row["timestamp"]).ok_or_else(|| failure("CURSOR_INVALID_RESPONSE"))?;
        if ts < cursor["start"].as_i64().unwrap_or(i64::MAX)
            || ts > cursor["end"].as_i64().unwrap_or(0)
            || last.is_some_and(|t| ts > t)
        {
            return Err(failure("CURSOR_UNSTABLE_PAGE_ORDER"));
        }
        if last != Some(ts) {
            occurrences.clear();
            last = Some(ts);
        }
        let Some(session) = row["conversationId"]
            .as_str()
            .filter(|s| local.contains(&s.to_ascii_lowercase()))
        else {
            continue;
        };
        let Some(usage) = row["tokenUsage"].as_object() else {
            continue;
        };
        let mut counts = [0u64; 4];
        for (i, k) in [
            "inputTokens",
            "outputTokens",
            "cacheReadTokens",
            "cacheWriteTokens",
        ]
        .iter()
        .enumerate()
        {
            // Protobuf JSON omits zero-valued scalars. Present invalid values are never zeroed.
            counts[i] = match usage.get(*k) {
                Some(v) => number(v).ok_or_else(|| failure("CURSOR_INVALID_TOKENS"))?,
                None => 0,
            };
        }
        let model = row["model"].as_str().unwrap_or("");
        let identity = json!([ts, session.to_ascii_lowercase(), model, counts]).to_string();
        let signature = format!("{:x}", Sha256::digest(identity.as_bytes()));
        let count = occurrences.entry(signature.clone()).or_default();
        *count += 1;
        let key = format!("{signature}:{}", *count);
        records.push(RawRecord { ordinal: records.len() as u64, byte_start: None, byte_end: None, native_rowid: None,
            payload: json!({"cursor_usage":true,"timestamp":ts,"conversation":session.to_ascii_lowercase(),"native_key":key,"model":model,"counts":counts}).to_string().into_bytes(), file_mtime_ms:None });
    }
    cursor["last_timestamp"] = json!(last);
    cursor["occurrences"] = json!(occurrences);
    let has_more = page * size < total;
    cursor["page"] = json!(page + 1);
    Ok(RawBatch {
        records,
        next_cursor_json: if has_more { cursor.clone() } else { json!({}) },
        next_observed_boundary_json: json!({}),
        has_more,
        bytes_read: 0,
        ignored_incomplete_tail: false,
    })
}

pub fn decode_usage(secret: &[u8], value: &Value) -> Result<FactDraft, RunnerError> {
    let counts: [u64; 4] = serde_json::from_value(value["counts"].clone())
        .map_err(|_| failure("CURSOR_INVALID_TOKENS"))?;
    let input = counts[0]
        .checked_add(counts[2])
        .and_then(|v| v.checked_add(counts[3]))
        .ok_or_else(|| failure("CURSOR_INVALID_TOKENS"))?;
    let total = input
        .checked_add(counts[1])
        .filter(|v| *v <= i64::MAX as u64)
        .ok_or_else(|| failure("CURSOR_INVALID_TOKENS"))?;
    let mut fact = emit_usage_fact(UsageFactArgs {
        secret,
        harness: "cursor",
        scope: STREAM_USAGE,
        native: TypedNativeKey::Str(
            value["native_key"]
                .as_str()
                .ok_or_else(|| failure("CURSOR_INVALID_ID"))?
                .into(),
        ),
        fact_kind: "model_usage_recorded",
        occurred_at: value["timestamp"]
            .as_i64()
            .ok_or_else(|| failure("CURSOR_INVALID_TIME"))?,
        time_source: TimeSource::SourceRecord,
        token_total: total,
        input_tokens: input,
        output_tokens: counts[1],
        accuracy: TokenAccuracy::Exact,
        session_id: value["conversation"].as_str(),
        turn_id: None,
        skill_id: None,
        skill_key: None,
        model_key: 0,
        cache_read_tokens: Some(counts[2]),
        reasoning_tokens: None,
    });
    fact.payload_sections["usage"]["cache_write_tokens"] = json!(counts[3]);
    Ok(fact)
}

#[cfg(test)]
mod tests;
