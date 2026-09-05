use std::collections::{BTreeMap, HashMap, VecDeque};
use std::net::IpAddr;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use adapter_sdk::{RawFrame, SourceKind};
use reqwest::{Client, StatusCode, Url};
use rusqlite::{types::ValueRef, Connection, OpenFlags};
use serde_json::{Map, Value};
use wal_spool::DriverCheckpoint;

use crate::AcquisitionError;

use crate::otlp::DEFAULT_OTLP_PAYLOAD_LIMIT;

pub const REMOTE_API_OVERLAP: Duration = Duration::from_secs(5 * 60);

pub trait SecretResolver: Send + Sync {
    fn resolve(&self, secret_ref: &str) -> Result<Vec<u8>, String>;
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DriverBatch {
    pub frames: Vec<RawFrame>,
    pub cursor: String,
    pub driver_checkpoint: Option<DriverCheckpoint>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DriverKind {
    OtlpReceiver,
    JsonlTail,
    SqliteSnapshot,
    RuntimeStream,
    RemoteApi,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DriverTaskStatus {
    Idle,
    Running,
    Backoff(Duration),
    Failed(String),
}

pub enum DriverInstance {
    OtlpReceiver(crate::otlp::OtlpReceiverDriver),
    JsonlTail(crate::jsonl::JsonlTailer),
    SqliteSnapshot(SqliteSnapshotDriver),
    RuntimeStream(RuntimeStreamDriver),
    RemoteApi(RemoteApiDriver),
}

impl DriverInstance {
    pub fn kind(&self) -> DriverKind {
        match self {
            Self::OtlpReceiver(_) => DriverKind::OtlpReceiver,
            Self::JsonlTail(_) => DriverKind::JsonlTail,
            Self::SqliteSnapshot(_) => DriverKind::SqliteSnapshot,
            Self::RuntimeStream(_) => DriverKind::RuntimeStream,
            Self::RemoteApi(_) => DriverKind::RemoteApi,
        }
    }
}

pub struct DriverEntry {
    pub driver: DriverInstance,
    pub status: DriverTaskStatus,
}

#[derive(Default)]
pub struct DriverRegistry {
    sources: HashMap<(String, String), DriverEntry>,
}

impl DriverRegistry {
    pub fn register(
        &mut self,
        adapter_id: impl Into<String>,
        source_id: impl Into<String>,
        driver: DriverInstance,
    ) -> Result<(), AcquisitionError> {
        let key = (adapter_id.into(), source_id.into());
        if self
            .sources
            .insert(
                key,
                DriverEntry {
                    driver,
                    status: DriverTaskStatus::Idle,
                },
            )
            .is_some()
        {
            return Err(AcquisitionError::Other("duplicate_source_driver".into()));
        }
        Ok(())
    }

    pub fn entry(&self, adapter_id: &str, source_id: &str) -> Option<&DriverEntry> {
        self.sources
            .get(&(adapter_id.to_owned(), source_id.to_owned()))
    }

    pub fn entry_mut(&mut self, adapter_id: &str, source_id: &str) -> Option<&mut DriverEntry> {
        self.sources
            .get_mut(&(adapter_id.to_owned(), source_id.to_owned()))
    }

    pub fn kind(&self, adapter_id: &str, source_id: &str) -> Option<DriverKind> {
        self.entry(adapter_id, source_id)
            .map(|entry| entry.driver.kind())
    }

    pub fn len(&self) -> usize {
        self.sources.len()
    }

    pub fn is_empty(&self) -> bool {
        self.sources.is_empty()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RemotePollError {
    RateLimited(Duration),
    Transport(String),
    Http(u16),
    InvalidResponse(String),
    Secret(String),
}

pub struct RemoteApiDriver {
    installation_id: String,
    source_id: String,
    endpoint: Url,
    secret_ref: String,
    resolver: Arc<dyn SecretResolver>,
    client: Client,
    cursor: Option<String>,
    last_window_end: Option<i64>,
    max_payload_bytes: usize,
}

impl RemoteApiDriver {
    pub fn new(
        installation_id: impl Into<String>,
        source_id: impl Into<String>,
        endpoint: impl AsRef<str>,
        declared_domain: &str,
        secret_ref: impl Into<String>,
        resolver: Arc<dyn SecretResolver>,
        max_payload_bytes: usize,
    ) -> Result<Self, AcquisitionError> {
        let endpoint = Url::parse(endpoint.as_ref())
            .map_err(|error| AcquisitionError::Other(format!("invalid_remote_endpoint:{error}")))?;
        let host = endpoint.host_str().unwrap_or_default();
        let loopback_http = endpoint.scheme() == "http"
            && host
                .parse::<IpAddr>()
                .is_ok_and(|address| address.is_loopback());
        if host != declared_domain || (endpoint.scheme() != "https" && !loopback_http) {
            return Err(AcquisitionError::Other(
                "remote_endpoint_not_allowed".into(),
            ));
        }
        if max_payload_bytes == 0 || max_payload_bytes > DEFAULT_OTLP_PAYLOAD_LIMIT {
            return Err(AcquisitionError::Other(
                "invalid_remote_payload_limit".into(),
            ));
        }
        let secret_ref = secret_ref.into();
        if !secret_ref.starts_with("secret://") {
            return Err(AcquisitionError::Other("remote_secret_ref_required".into()));
        }
        Ok(Self {
            installation_id: installation_id.into(),
            source_id: source_id.into(),
            endpoint,
            secret_ref,
            resolver,
            client: Client::new(),
            cursor: None,
            last_window_end: None,
            max_payload_bytes,
        })
    }

    pub fn restore_cursor(&mut self, cursor: impl Into<String>, window_end: i64) {
        self.cursor = Some(cursor.into());
        self.last_window_end = Some(window_end);
    }

    pub fn cursor_state(&self) -> (Option<&str>, Option<i64>) {
        (self.cursor.as_deref(), self.last_window_end)
    }

    pub async fn poll(&mut self, now_unix_seconds: i64) -> Result<DriverBatch, RemotePollError> {
        let overlap = REMOTE_API_OVERLAP.as_secs() as i64;
        let window_start = self
            .last_window_end
            .map(|end| end.saturating_sub(overlap))
            .unwrap_or_else(|| now_unix_seconds.saturating_sub(overlap));
        let mut url = self.endpoint.clone();
        {
            let mut query = url.query_pairs_mut();
            query.append_pair("from", &window_start.to_string());
            query.append_pair("until", &now_unix_seconds.to_string());
            if let Some(cursor) = &self.cursor {
                query.append_pair("cursor", cursor);
            }
        }
        let secret = self
            .resolver
            .resolve(&self.secret_ref)
            .map_err(RemotePollError::Secret)?;
        let response = self
            .client
            .get(url)
            .bearer_auth(String::from_utf8_lossy(&secret).as_ref())
            .send()
            .await
            .map_err(|error| RemotePollError::Transport(error.to_string()))?;
        if response.status() == StatusCode::TOO_MANY_REQUESTS {
            let delay = response
                .headers()
                .get(reqwest::header::RETRY_AFTER)
                .and_then(|value| value.to_str().ok())
                .and_then(parse_retry_after)
                .unwrap_or(Duration::from_secs(60));
            return Err(RemotePollError::RateLimited(delay));
        }
        if !response.status().is_success() {
            return Err(RemotePollError::Http(response.status().as_u16()));
        }
        if response
            .content_length()
            .is_some_and(|len| len > self.max_payload_bytes as u64)
        {
            return Err(RemotePollError::InvalidResponse(
                "remote_payload_too_large".into(),
            ));
        }
        let header_cursor = response
            .headers()
            .get("x-next-cursor")
            .and_then(|value| value.to_str().ok())
            .map(ToOwned::to_owned);
        let payload = response
            .bytes()
            .await
            .map_err(|error| RemotePollError::Transport(error.to_string()))?;
        if payload.len() > self.max_payload_bytes {
            return Err(RemotePollError::InvalidResponse(
                "remote_payload_too_large".into(),
            ));
        }
        let body_cursor = serde_json::from_slice::<Value>(&payload)
            .ok()
            .and_then(|value| {
                value
                    .get("next_cursor")
                    .and_then(Value::as_str)
                    .map(ToOwned::to_owned)
            });
        let next_cursor = header_cursor
            .or(body_cursor)
            .or_else(|| self.cursor.clone())
            .unwrap_or_else(|| now_unix_seconds.to_string());
        self.cursor = Some(next_cursor.clone());
        self.last_window_end = Some(now_unix_seconds);
        Ok(DriverBatch {
            frames: vec![RawFrame {
                installation_id: self.installation_id.clone(),
                source_kind: SourceKind::RemoteApi,
                source_id: self.source_id.clone(),
                cursor: next_cursor.clone(),
                payload: payload.to_vec(),
            }],
            cursor: next_cursor.clone(),
            driver_checkpoint: Some(DriverCheckpoint::RemoteApi {
                cursor: next_cursor,
                window_end_unix_seconds: now_unix_seconds,
            }),
        })
    }
}

fn parse_retry_after(value: &str) -> Option<Duration> {
    if let Ok(seconds) = value.trim().parse::<u64>() {
        return Some(Duration::from_secs(seconds));
    }
    let at = httpdate::parse_http_date(value).ok()?;
    Some(
        at.duration_since(std::time::SystemTime::now())
            .unwrap_or_default(),
    )
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SqliteAdapterPlan {
    CursorPersonalV1,
    ZcodeV1,
    ZcodeV2,
    ZcodeV3,
}

impl SqliteAdapterPlan {
    pub fn fingerprint(self) -> &'static str {
        match self {
            Self::CursorPersonalV1 => "cursor-local-v1",
            Self::ZcodeV1 => "zcode-sqlite-v1-uv7",
            Self::ZcodeV2 => "zcode-sqlite-v2-uv9",
            Self::ZcodeV3 => "zcode-sqlite-v3-uv0",
        }
    }

    fn user_version(self) -> i64 {
        match self {
            Self::CursorPersonalV1 => 0,
            Self::ZcodeV1 => 7,
            Self::ZcodeV2 => 9,
            Self::ZcodeV3 => 0,
        }
    }

    fn tables(self) -> &'static [(&'static str, &'static [&'static str])] {
        match self {
            Self::CursorPersonalV1 => &[("cursor_conversations", &["id", "timestamp", "model"])],
            Self::ZcodeV1 => &[
                ("sessions", &["id", "created_at", "model"]),
                (
                    "steps",
                    &[
                        "id",
                        "session_id",
                        "finished_at",
                        "input_tokens",
                        "output_tokens",
                        "tool_count",
                    ],
                ),
            ],
            Self::ZcodeV2 => &[
                ("sessions", &["id", "created_at", "model"]),
                (
                    "step_metrics",
                    &[
                        "id",
                        "session_id",
                        "finished_at",
                        "input_tokens",
                        "output_tokens",
                        "total_tokens",
                        "tool_count",
                        "skill_name",
                    ],
                ),
            ],
            Self::ZcodeV3 => &[
                (
                    "session",
                    &[
                        "id",
                        "project_id",
                        "workspace_id",
                        "parent_id",
                        "slug",
                        "directory",
                        "path",
                        "title",
                        "version",
                        "share_url",
                        "summary_additions",
                        "summary_deletions",
                        "summary_files",
                        "summary_diffs",
                        "revert",
                        "permission",
                        "time_created",
                        "time_updated",
                        "time_compacting",
                        "time_archived",
                        "task_type",
                        "title_source",
                        "title_message_id",
                        "time_title_updated",
                        "trace_id",
                    ],
                ),
                (
                    "model_usage",
                    &[
                        "id",
                        "logical_request_id",
                        "attempt_index",
                        "session_id",
                        "turn_id",
                        "trace_id",
                        "span_id",
                        "assistant_message_id",
                        "parent_user_message_id",
                        "query_source",
                        "provider_id",
                        "model_id",
                        "variant",
                        "agent",
                        "mode",
                        "task_type",
                        "status",
                        "started_at",
                        "first_token_at",
                        "completed_at",
                        "duration_ms",
                        "time_to_first_token_ms",
                        "finish_reason",
                        "tool_call_count",
                        "input_tokens",
                        "output_tokens",
                        "reasoning_tokens",
                        "cache_creation_input_tokens",
                        "cache_read_input_tokens",
                        "provider_total_tokens",
                        "computed_total_tokens",
                        "retry_count",
                        "retryable",
                        "cancelled_by_user",
                        "context_exceeded",
                        "error_type",
                        "error_code",
                        "error_message",
                        "raw_usage_json",
                        "provider_metadata_json",
                    ],
                ),
            ],
        }
    }

    fn queries(self) -> &'static [&'static str] {
        match self {
            Self::CursorPersonalV1 => &["SELECT id, timestamp, model FROM cursor_conversations WHERE id > ?1 ORDER BY id"],
            Self::ZcodeV1 => &[
                "SELECT id, created_at, model FROM sessions WHERE id > ?1 ORDER BY id",
                "SELECT id, session_id, finished_at, input_tokens, output_tokens, tool_count FROM steps WHERE id > ?1 ORDER BY id",
            ],
            Self::ZcodeV2 => &[
                "SELECT id, created_at, model FROM sessions WHERE id > ?1 ORDER BY id",
                "SELECT id, session_id, finished_at, input_tokens, output_tokens, total_tokens, tool_count, skill_name FROM step_metrics WHERE id > ?1 ORDER BY id",
            ],
            Self::ZcodeV3 => &[
                "SELECT rowid AS id, id AS session_ref, time_created FROM session WHERE rowid > ?1 ORDER BY rowid",
                "SELECT rowid AS id, session_id, provider_id, model_id, input_tokens, output_tokens, computed_total_tokens, tool_call_count, completed_at FROM model_usage WHERE rowid > ?1 AND status = 'completed' ORDER BY rowid",
            ],
        }
    }
}

pub struct SqliteSnapshotDriver {
    installation_id: String,
    source_id: String,
    path: PathBuf,
    plan: SqliteAdapterPlan,
    trusted_fingerprint: String,
    query_cursors: Vec<i64>,
}

impl SqliteSnapshotDriver {
    pub fn new(
        installation_id: impl Into<String>,
        source_id: impl Into<String>,
        path: impl Into<PathBuf>,
        plan: SqliteAdapterPlan,
        trusted_fingerprint: impl Into<String>,
    ) -> Result<Self, AcquisitionError> {
        let trusted_fingerprint = trusted_fingerprint.into();
        if trusted_fingerprint != plan.fingerprint() {
            return Err(AcquisitionError::Other(
                "untrusted_sqlite_schema_fingerprint".into(),
            ));
        }
        Ok(Self {
            installation_id: installation_id.into(),
            source_id: source_id.into(),
            path: path.into(),
            plan,
            trusted_fingerprint,
            query_cursors: vec![0; plan.queries().len()],
        })
    }

    pub fn restore_cursor(&mut self, row_cursor: i64) {
        self.query_cursors.fill(row_cursor.max(0));
    }

    pub fn restore_query_cursors(&mut self, cursors: &[i64]) {
        for (current, restored) in self.query_cursors.iter_mut().zip(cursors) {
            *current = (*restored).max(0);
        }
    }

    pub fn query_cursors(&self) -> &[i64] {
        &self.query_cursors
    }

    pub fn poll(&mut self) -> Result<DriverBatch, AcquisitionError> {
        let connection = open_snapshot(&self.path)?;
        verify_sqlite_plan(&connection, self.plan)?;
        let mut records = Vec::new();
        for (query_index, query) in self.plan.queries().iter().enumerate() {
            let query_cursor = self.query_cursors[query_index];
            let mut max_cursor = query_cursor;
            let mut statement = connection
                .prepare(query)
                .map_err(|error| AcquisitionError::Other(format!("sqlite_prepare:{error}")))?;
            let names: Vec<String> = statement
                .column_names()
                .iter()
                .map(|name| (*name).to_owned())
                .collect();

            let rows = statement
                .query_map([query_cursor], |row| {
                    let mut object = Map::new();
                    for (index, name) in names.iter().enumerate() {
                        object.insert(name.clone(), sqlite_value(row.get_ref(index)?));
                    }
                    Ok(object)
                })
                .map_err(|error| AcquisitionError::Other(format!("sqlite_query:{error}")))?;
            for row in rows {
                let mut row =
                    row.map_err(|error| AcquisitionError::Other(format!("sqlite_row:{error}")))?;
                if let Some(id) = row.get("id").and_then(Value::as_i64) {
                    max_cursor = max_cursor.max(id);
                }
                normalize_sqlite_record(self.plan, &mut row);
                records.push(Value::Object(row));
            }
            self.query_cursors[query_index] = max_cursor;
        }
        let cursor = self
            .query_cursors
            .iter()
            .map(i64::to_string)
            .collect::<Vec<_>>()
            .join(":");
        let mut root = Map::new();
        root.insert(
            "fingerprint".into(),
            Value::String(self.trusted_fingerprint.clone()),
        );
        let collection = if self.plan == SqliteAdapterPlan::CursorPersonalV1 {
            "events"
        } else {
            "records"
        };
        root.insert(collection.into(), Value::Array(records));
        let payload = serde_json::to_vec(&Value::Object(root))
            .map_err(|error| AcquisitionError::Other(error.to_string()))?;
        Ok(DriverBatch {
            frames: vec![RawFrame {
                installation_id: self.installation_id.clone(),
                source_kind: SourceKind::SqliteSnapshot,
                source_id: self.source_id.clone(),
                cursor: cursor.clone(),
                payload,
            }],
            cursor,
            driver_checkpoint: Some(DriverCheckpoint::SqliteSnapshot {
                query_cursors: self.query_cursors.clone(),
            }),
        })
    }
}

fn verify_sqlite_plan(
    connection: &Connection,
    plan: SqliteAdapterPlan,
) -> Result<(), AcquisitionError> {
    let user_version: i64 = connection
        .pragma_query_value(None, "user_version", |row| row.get(0))
        .map_err(|error| AcquisitionError::Other(format!("sqlite_user_version:{error}")))?;
    let mut signature = format!("user_version={user_version}");
    let mut matches = user_version == plan.user_version();
    for (table, expected_columns) in plan.tables() {
        let mut statement = connection
            .prepare(&format!("PRAGMA table_info(\"{table}\")"))
            .map_err(|error| AcquisitionError::Other(format!("sqlite_schema:{error}")))?;
        let columns = statement
            .query_map([], |row| row.get::<_, String>(1))
            .map_err(|error| AcquisitionError::Other(format!("sqlite_schema:{error}")))?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|error| AcquisitionError::Other(format!("sqlite_schema:{error}")))?;
        signature.push('|');
        signature.push_str(table);
        signature.push('(');
        signature.push_str(&columns.join(","));
        signature.push(')');
        matches &= columns == *expected_columns;
    }
    if matches {
        Ok(())
    } else {
        Err(AcquisitionError::Other(format!(
            "sqlite_schema_plan_mismatch:{signature}"
        )))
    }
}

fn open_snapshot(path: &Path) -> Result<Connection, AcquisitionError> {
    let source = Connection::open_with_flags(
        path,
        OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX,
    )
    .map_err(|error| AcquisitionError::Other(format!("sqlite_open:{error}")))?;
    let mut snapshot = Connection::open_in_memory()
        .map_err(|error| AcquisitionError::Other(format!("sqlite_snapshot_open:{error}")))?;
    {
        let backup = rusqlite::backup::Backup::new(&source, &mut snapshot)
            .map_err(|error| AcquisitionError::Other(format!("sqlite_snapshot:{error}")))?;
        backup
            .run_to_completion(100, Duration::from_millis(1), None)
            .map_err(|error| AcquisitionError::Other(format!("sqlite_snapshot:{error}")))?;
    }
    Ok(snapshot)
}

fn sqlite_value(value: ValueRef<'_>) -> Value {
    match value {
        ValueRef::Null => Value::Null,
        ValueRef::Integer(value) => Value::from(value),
        ValueRef::Real(value) => Value::from(value),
        ValueRef::Text(value) => Value::String(String::from_utf8_lossy(value).into_owned()),
        ValueRef::Blob(_) => Value::Null,
    }
}

fn normalize_sqlite_record(plan: SqliteAdapterPlan, row: &mut Map<String, Value>) {
    match plan {
        SqliteAdapterPlan::ZcodeV3 => {
            // Session probe rows carry `session_ref`; usage rows carry `session_id`.
            let session_ref = row.remove("session_ref");
            let timestamp = row
                .remove("completed_at")
                .or_else(|| row.remove("time_created"));
            match session_ref {
                Some(session_ref) => {
                    row.insert("type".into(), Value::String("session".into()));
                    row.insert("sessionId".into(), session_ref);
                }
                None => {
                    row.insert("type".into(), Value::String("step_finish".into()));
                    rename(row, "session_id", "sessionId");
                    rename(row, "id", "stepId");
                    rename(row, "provider_id", "provider");
                    rename(row, "model_id", "model");
                    rename(row, "input_tokens", "inputTokens");
                    rename(row, "output_tokens", "outputTokens");
                    rename(row, "computed_total_tokens", "totalTokens");
                    rename(row, "tool_call_count", "toolCount");
                }
            }
            if let Some(tokens) = timestamp.as_ref().and_then(Value::as_i64) {
                if let Some(timestamp) = unix_ms_to_rfc3339(tokens) {
                    row.insert("timestamp".into(), Value::String(timestamp));
                }
            }
        }
        SqliteAdapterPlan::CursorPersonalV1 => {
            row.insert("type".into(), Value::String("conversation".into()));
            rename(row, "id", "conversationId");
        }
        SqliteAdapterPlan::ZcodeV1 | SqliteAdapterPlan::ZcodeV2 => {
            let session_id = row.remove("session_id");
            let timestamp = row
                .remove("created_at")
                .or_else(|| row.remove("finished_at"));
            match session_id {
                Some(session_id) => {
                    row.insert("type".into(), Value::String("step_finish".into()));
                    row.insert("sessionId".into(), session_id);
                    rename(row, "id", "stepId");
                    rename(row, "input_tokens", "inputTokens");
                    rename(row, "output_tokens", "outputTokens");
                    rename(row, "total_tokens", "totalTokens");
                    rename(row, "tool_count", "toolCount");
                    rename(row, "skill_name", "skillName");
                }
                None => {
                    row.insert("type".into(), Value::String("session".into()));
                    rename(row, "id", "sessionId");
                }
            }
            if let Some(timestamp) = timestamp {
                row.insert("timestamp".into(), timestamp);
            }
        }
    }
}

fn rename(row: &mut Map<String, Value>, from: &str, to: &str) {
    if let Some(value) = row.remove(from) {
        row.insert(to.into(), value);
    }
}

fn unix_ms_to_rfc3339(milliseconds: i64) -> Option<String> {
    time::OffsetDateTime::from_unix_timestamp_nanos((milliseconds * 1_000_000).into())
        .ok()?
        .format(&time::format_description::well_known::Rfc3339)
        .ok()
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ZcodeSqliteDetection {
    pub fingerprint: &'static str,
    pub app_version: Option<String>,
}

/// Probe a ZCode CLI database and return the verified schema fingerprint, so
/// detection can gate collection on the actual on-disk schema instead of a
/// version string. `None` means the schema matches no verified plan.
pub fn detect_zcode_sqlite(path: &Path) -> Option<ZcodeSqliteDetection> {
    let connection = open_snapshot(path).ok()?;
    for plan in [
        SqliteAdapterPlan::ZcodeV3,
        SqliteAdapterPlan::ZcodeV2,
        SqliteAdapterPlan::ZcodeV1,
    ] {
        if verify_sqlite_plan(&connection, plan).is_ok() {
            let app_version = connection
                .query_row(
                    "SELECT app_version FROM schema_migration ORDER BY id DESC LIMIT 1",
                    [],
                    |row| row.get::<_, String>(0),
                )
                .ok();
            return Some(ZcodeSqliteDetection {
                fingerprint: plan.fingerprint(),
                app_version,
            });
        }
    }
    None
}

#[derive(Debug)]
pub struct RuntimeStreamDriver {
    installation_id: String,
    source_id: String,
    stream_id: String,
    next_sequence: u64,
    queue: VecDeque<(u64, Vec<u8>)>,
    max_payload_bytes: usize,
}

impl RuntimeStreamDriver {
    pub fn new(
        installation_id: impl Into<String>,
        source_id: impl Into<String>,
        stream_id: impl Into<String>,
        max_payload_bytes: usize,
    ) -> Result<Self, AcquisitionError> {
        if max_payload_bytes == 0 || max_payload_bytes > DEFAULT_OTLP_PAYLOAD_LIMIT {
            return Err(AcquisitionError::Other(
                "invalid_runtime_payload_limit".into(),
            ));
        }
        Ok(Self {
            installation_id: installation_id.into(),
            source_id: source_id.into(),
            stream_id: stream_id.into(),
            next_sequence: 1,
            queue: VecDeque::new(),
            max_payload_bytes,
        })
    }

    pub fn restore_next_sequence(&mut self, next_sequence: u64) {
        self.next_sequence = next_sequence.max(1);
        self.queue.clear();
    }

    pub fn next_sequence(&self) -> u64 {
        self.next_sequence
    }

    pub fn push(
        &mut self,
        stream_id: &str,
        sequence: u64,
        payload: Vec<u8>,
    ) -> Result<(), AcquisitionError> {
        if stream_id != self.stream_id {
            return Err(AcquisitionError::Other("runtime_stream_id_mismatch".into()));
        }
        if sequence < self.next_sequence
            || self.queue.back().is_some_and(|(last, _)| sequence <= *last)
        {
            return Err(AcquisitionError::Other(
                "runtime_sequence_not_monotonic".into(),
            ));
        }
        if payload.len() > self.max_payload_bytes {
            return Err(AcquisitionError::Other("runtime_payload_too_large".into()));
        }
        self.queue.push_back((sequence, payload));
        Ok(())
    }

    pub fn poll(&mut self, limit: usize) -> DriverBatch {
        let mut frames = Vec::new();
        while frames.len() < limit {
            let Some((sequence, payload)) = self.queue.pop_front() else {
                break;
            };
            self.next_sequence = sequence.saturating_add(1);
            frames.push(RawFrame {
                installation_id: self.installation_id.clone(),
                source_kind: SourceKind::RuntimeStream,
                source_id: self.source_id.clone(),
                cursor: sequence.to_string(),
                payload,
            });
        }
        DriverBatch {
            cursor: self.next_sequence.saturating_sub(1).to_string(),
            frames,
            driver_checkpoint: Some(DriverCheckpoint::RuntimeStream {
                next_sequence: self.next_sequence,
            }),
        }
    }
}

pub fn registry_snapshot(registry: &DriverRegistry) -> BTreeMap<String, DriverKind> {
    registry
        .sources
        .iter()
        .map(|((adapter, source), entry)| (format!("{adapter}:{source}"), entry.driver.kind()))
        .collect()
}
