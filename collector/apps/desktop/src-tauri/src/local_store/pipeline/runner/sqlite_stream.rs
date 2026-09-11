//! SQLite short read transactions with independent stream cursors and pending sets.

use std::collections::BTreeSet;
use std::path::Path;
use std::time::Instant;

use rusqlite::{params, Connection, OpenFlags};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};

use super::budget::{ReadBudget, DEFAULT_PENDING_SET_LIMIT};
use super::strategy::RunnerError;

pub const DEFAULT_SQLITE_PENDING_LIMIT: usize = DEFAULT_PENDING_SET_LIMIT;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SqliteChangeMode {
    /// Append-only completed rows keyed by native rowid.
    AppendRowid,
    /// Discover all rows; revisit unfinished set until completed.
    RunningToCompleted,
    /// Lexicographic (updated_at, rowid) pagination.
    UpdatedAtRowid,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct CompositeCursor {
    pub updated_at: i64,
    pub rowid: i64,
}

impl CompositeCursor {
    pub fn start() -> Self {
        Self {
            updated_at: 0,
            rowid: 0,
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PendingSet {
    pub row_ids: BTreeSet<i64>,
    pub limit: usize,
}

impl PendingSet {
    pub fn new(limit: usize) -> Self {
        Self {
            row_ids: BTreeSet::new(),
            limit: limit.max(1),
        }
    }

    pub fn is_full(&self) -> bool {
        self.row_ids.len() >= self.limit
    }

    pub fn insert(&mut self, rowid: i64) -> bool {
        if self.row_ids.contains(&rowid) {
            return true;
        }
        if self.is_full() {
            return false;
        }
        self.row_ids.insert(rowid);
        true
    }

    pub fn remove(&mut self, rowid: i64) {
        self.row_ids.remove(&rowid);
    }
}

#[derive(Debug, Clone)]
pub struct SqliteRow {
    pub rowid: i64,
    pub updated_at: Option<i64>,
    pub status: Option<String>,
    pub payload_json: Value,
}

#[derive(Debug, Clone)]
pub struct SqliteReadResult {
    pub rows: Vec<SqliteRow>,
    pub next_cursor_json: Value,
    pub pending: PendingSet,
    /// When pending is full, discovery cursor must not advance.
    pub discovery_paused: bool,
    pub bytes_read: usize,
    pub has_more: bool,
}

struct LiveCursor {
    mode: SqliteChangeMode,
    last_rowid: i64,
    last_updated_at: i64,
    pending: PendingSet,
}

fn open_readonly(path: &Path) -> Result<Connection, RunnerError> {
    Connection::open_with_flags(
        path,
        OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX,
    )
    .map_err(|e| RunnerError::Io(e.to_string()))
}

/// Short read-only transaction over one query stream.
///
/// `select_sql` projection depends on mode:
/// - AppendRowid / RunningToCompleted: bind `last_rowid`, columns
///   `rowid, updated_at, status, payload_json` with `WHERE rowid > ?1 ORDER BY rowid`
/// - UpdatedAtRowid: bind `(last_updated_at, last_rowid)`, same columns, with
///   `WHERE (updated_at > ?1) OR (updated_at = ?1 AND rowid > ?2)
///    ORDER BY updated_at, rowid`
pub fn read_sqlite_change_stream(
    path: &Path,
    select_sql: &str,
    cursor_json: &Value,
    mode: SqliteChangeMode,
    budget: ReadBudget,
) -> Result<SqliteReadResult, RunnerError> {
    let mut state = parse_cursor(cursor_json, mode);
    let conn = open_readonly(path)?;
    let started = Instant::now();
    let tx = conn
        .unchecked_transaction()
        .map_err(|e| RunnerError::Io(e.to_string()))?;

    let mut rows = Vec::new();
    let mut bytes_read = 0usize;
    let mut discovery_paused = false;
    let mut has_more = false;

    match mode {
        SqliteChangeMode::AppendRowid => {
            let mut stmt = tx
                .prepare(select_sql)
                .map_err(|e| RunnerError::Io(e.to_string()))?;
            let mapped = stmt
                .query_map(params![state.last_rowid], map_row)
                .map_err(|e| RunnerError::Io(e.to_string()))?;
            for item in mapped {
                if budget.exhausted(rows.len(), bytes_read, started.elapsed()) {
                    has_more = true;
                    break;
                }
                let row = item.map_err(|e| RunnerError::Io(e.to_string()))?;
                bytes_read = bytes_read.saturating_add(estimate_row_bytes(&row));
                state.last_rowid = row.rowid;
                rows.push(row);
            }
        }
        SqliteChangeMode::RunningToCompleted => {
            let pending_ids: Vec<i64> = state.pending.row_ids.iter().copied().collect();
            for id in pending_ids {
                if budget.exhausted(rows.len(), bytes_read, started.elapsed()) {
                    has_more = true;
                    break;
                }
                if let Some(row) = load_one(&tx, select_sql, id)? {
                    bytes_read = bytes_read.saturating_add(estimate_row_bytes(&row));
                    let done = is_completed(&row);
                    if done {
                        state.pending.remove(id);
                        rows.push(row);
                    }
                } else {
                    state.pending.remove(id);
                }
            }

            if state.pending.is_full() {
                discovery_paused = true;
                has_more = !state.pending.row_ids.is_empty();
            } else if !budget.exhausted(rows.len(), bytes_read, started.elapsed()) {
                let discover_from = state.last_rowid;
                let mut stmt = tx
                    .prepare(select_sql)
                    .map_err(|e| RunnerError::Io(e.to_string()))?;
                let mapped = stmt
                    .query_map(params![discover_from], map_row)
                    .map_err(|e| RunnerError::Io(e.to_string()))?;
                for item in mapped {
                    if budget.exhausted(rows.len(), bytes_read, started.elapsed()) {
                        has_more = true;
                        break;
                    }
                    if state.pending.is_full() {
                        discovery_paused = true;
                        has_more = true;
                        break;
                    }
                    let row = item.map_err(|e| RunnerError::Io(e.to_string()))?;
                    let new_id = row.rowid;
                    bytes_read = bytes_read.saturating_add(estimate_row_bytes(&row));
                    let done = is_completed(&row);
                    if done {
                        state.last_rowid = new_id;
                        rows.push(row);
                    } else if state.pending.insert(new_id) {
                        state.last_rowid = new_id;
                    } else {
                        discovery_paused = true;
                        has_more = true;
                        break;
                    }
                }
            }
        }
        SqliteChangeMode::UpdatedAtRowid => {
            let mut stmt = tx
                .prepare(select_sql)
                .map_err(|e| RunnerError::Io(e.to_string()))?;
            let mapped = stmt
                .query_map(params![state.last_updated_at, state.last_rowid], map_row)
                .map_err(|e| RunnerError::Io(e.to_string()))?;
            for item in mapped {
                if budget.exhausted(rows.len(), bytes_read, started.elapsed()) {
                    has_more = true;
                    break;
                }
                let row = item.map_err(|e| RunnerError::Io(e.to_string()))?;
                let updated = row.updated_at.unwrap_or(0);
                if updated < state.last_updated_at
                    || (updated == state.last_updated_at && row.rowid <= state.last_rowid)
                {
                    continue;
                }
                state.last_updated_at = updated;
                state.last_rowid = row.rowid;
                bytes_read = bytes_read.saturating_add(estimate_row_bytes(&row));
                rows.push(row);
            }
        }
    }

    drop(tx);

    Ok(SqliteReadResult {
        next_cursor_json: serialize_cursor(&state),
        pending: state.pending,
        discovery_paused,
        bytes_read,
        has_more,
        rows,
    })
}

fn is_completed(row: &SqliteRow) -> bool {
    row.status
        .as_deref()
        .map(|s| s.eq_ignore_ascii_case("completed"))
        .unwrap_or(false)
}

fn map_row(row: &rusqlite::Row<'_>) -> rusqlite::Result<SqliteRow> {
    let payload: String = row.get(3)?;
    let payload_json = serde_json::from_str(&payload).unwrap_or(Value::String(payload));
    Ok(SqliteRow {
        rowid: row.get(0)?,
        updated_at: row.get(1)?,
        status: row.get(2)?,
        payload_json,
    })
}

fn load_one(
    tx: &rusqlite::Transaction<'_>,
    select_sql: &str,
    rowid: i64,
) -> Result<Option<SqliteRow>, RunnerError> {
    let mut stmt = tx
        .prepare(select_sql)
        .map_err(|e| RunnerError::Io(e.to_string()))?;
    let mapped = stmt
        .query_map(params![rowid.saturating_sub(1)], map_row)
        .map_err(|e| RunnerError::Io(e.to_string()))?;
    for item in mapped {
        let row = item.map_err(|e| RunnerError::Io(e.to_string()))?;
        if row.rowid == rowid {
            return Ok(Some(row));
        }
        if row.rowid > rowid {
            break;
        }
    }
    Ok(None)
}

fn estimate_row_bytes(row: &SqliteRow) -> usize {
    row.payload_json.to_string().len() + 32
}

fn parse_cursor(cursor_json: &Value, mode: SqliteChangeMode) -> LiveCursor {
    let last_rowid = cursor_json
        .get("last_rowid")
        .and_then(|v| v.as_i64())
        .unwrap_or(0);
    let last_updated_at = cursor_json
        .get("last_updated_at")
        .and_then(|v| v.as_i64())
        .unwrap_or(0);
    let pending_limit = cursor_json
        .get("pending_limit")
        .and_then(|v| v.as_u64())
        .map(|v| v as usize)
        .unwrap_or(DEFAULT_SQLITE_PENDING_LIMIT);
    let mut pending = PendingSet::new(pending_limit);
    if let Some(arr) = cursor_json.get("pending").and_then(|v| v.as_array()) {
        for v in arr {
            if let Some(id) = v.as_i64() {
                let _ = pending.insert(id);
            }
        }
    }
    let parsed_mode = cursor_json
        .get("mode")
        .and_then(|v| serde_json::from_value::<SqliteChangeMode>(v.clone()).ok())
        .unwrap_or(mode);
    LiveCursor {
        mode: parsed_mode,
        last_rowid,
        last_updated_at,
        pending,
    }
}

fn serialize_cursor(state: &LiveCursor) -> Value {
    json!({
        "mode": state.mode,
        "last_rowid": state.last_rowid,
        "last_updated_at": state.last_updated_at,
        "pending": state.pending.row_ids.iter().copied().collect::<Vec<_>>(),
        "pending_limit": state.pending.limit,
    })
}

/// Rebuild cursor JSON after mutating a live PendingSet + watermarks.
pub fn cursor_from_parts(
    mode: SqliteChangeMode,
    last_rowid: i64,
    last_updated_at: i64,
    pending: &PendingSet,
) -> Value {
    json!({
        "mode": mode,
        "last_rowid": last_rowid,
        "last_updated_at": last_updated_at,
        "pending": pending.row_ids.iter().copied().collect::<Vec<_>>(),
        "pending_limit": pending.limit,
    })
}
