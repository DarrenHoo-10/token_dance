//! Budgeted JSONL offset reader: complete newlines only; triple budget; raw EOF.

use std::fs::File;
use std::io::{BufRead, BufReader, Read, Seek, SeekFrom};
use std::path::Path;
use std::time::Instant;

use serde_json::{json, Value};

use super::budget::ReadBudget;
use super::strategy::RunnerError;

pub const MAX_LINE_BYTES: usize = 4 * 1024 * 1024;
const CHUNK: usize = 64 * 1024;

#[derive(Debug, Clone)]
pub struct JsonlRecord {
    pub byte_start: u64,
    pub byte_end: u64,
    pub payload: Vec<u8>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SourceChange {
    Truncated,
    IdentityMismatch,
}

#[derive(Debug, Clone)]
pub struct JsonlReadResult {
    pub records: Vec<JsonlRecord>,
    pub next_offset: u64,
    pub file_len: u64,
    pub file_mtime_ms: Option<i64>,
    pub bytes_read: usize,
    pub has_more: bool,
    pub ignored_incomplete_tail: bool,
    pub oversized_lines: usize,
    pub source_change: Option<SourceChange>,
    /// Continue discarding an oversized record until its newline (persist with cursor).
    pub skipping_oversized: bool,
}

/// Read JSONL from a committed byte offset with record/byte/time budgets.
/// Incomplete trailing lines do not advance `next_offset`.
pub fn read_jsonl_budgeted(
    path: &Path,
    committed_offset: u64,
    expected_len_hint: Option<u64>,
    budget: ReadBudget,
) -> Result<JsonlReadResult, RunnerError> {
    read_jsonl_budgeted_with_state(path, committed_offset, expected_len_hint, budget, false)
}

pub fn read_jsonl_budgeted_with_state(
    path: &Path,
    committed_offset: u64,
    expected_len_hint: Option<u64>,
    budget: ReadBudget,
    mut skipping_oversized: bool,
) -> Result<JsonlReadResult, RunnerError> {
    if !path.exists() {
        return Ok(JsonlReadResult {
            records: Vec::new(),
            next_offset: 0,
            file_len: 0,
            file_mtime_ms: None,
            bytes_read: 0,
            has_more: false,
            ignored_incomplete_tail: false,
            oversized_lines: 0,
            source_change: None,
            skipping_oversized,
        });
    }
    let meta = std::fs::metadata(path).map_err(|e| RunnerError::Io(e.to_string()))?;
    let file_len = meta.len();
    let file_mtime_ms = meta
        .modified()
        .ok()
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_millis() as i64);

    if file_len < committed_offset {
        return Ok(JsonlReadResult {
            records: Vec::new(),
            next_offset: committed_offset,
            file_len,
            file_mtime_ms,
            bytes_read: 0,
            has_more: false,
            ignored_incomplete_tail: false,
            oversized_lines: 0,
            source_change: Some(SourceChange::Truncated),
            skipping_oversized,
        });
    }
    if let Some(hint) = expected_len_hint {
        // Shrinking below a previously observed boundary without offset regression
        // is treated as identity/content change by callers that track size.
        if hint > file_len && committed_offset > file_len {
            return Ok(JsonlReadResult {
                records: Vec::new(),
                next_offset: committed_offset,
                file_len,
                file_mtime_ms,
                bytes_read: 0,
                has_more: false,
                ignored_incomplete_tail: false,
                oversized_lines: 0,
                source_change: Some(SourceChange::IdentityMismatch),
                skipping_oversized,
            });
        }
    }

    let mut file = File::open(path).map_err(|e| RunnerError::Io(e.to_string()))?;
    file.seek(SeekFrom::Start(committed_offset))
        .map_err(|e| RunnerError::Io(e.to_string()))?;

    // Read only the observed boundary. Concurrent appends are picked up next time.
    let mut reader = BufReader::with_capacity(CHUNK, file.take(file_len - committed_offset));
    let started = Instant::now();
    let mut records = Vec::new();
    let mut bytes_read = 0usize;
    let mut oversized_lines = 0usize;
    let mut offset = committed_offset;
    let mut line_start = offset;
    let mut line = Vec::new();
    let mut ignored_incomplete_tail = false;

    loop {
        // Byte/time limits are soft for ONE supported record, never mid-line.
        // Oversized records instead persist a discard cursor, bounding both memory and I/O.
        if (line.is_empty() || skipping_oversized)
            && budget.exhausted(records.len(), bytes_read, started.elapsed())
        {
            break;
        }
        let buf = reader
            .fill_buf()
            .map_err(|e| RunnerError::Io(e.to_string()))?;
        if buf.is_empty() {
            ignored_incomplete_tail = !line.is_empty() || skipping_oversized;
            break;
        }
        let newline = buf.iter().position(|b| *b == b'\n');
        let used = newline.map_or(buf.len(), |n| n + 1);
        let payload_len = used - usize::from(newline.is_some());
        if !skipping_oversized {
            if line.len() + payload_len > MAX_LINE_BYTES {
                skipping_oversized = true;
                oversized_lines += 1;
                line.clear();
            } else {
                line.extend_from_slice(&buf[..payload_len]);
            }
        }
        bytes_read += used;
        let position = committed_offset + bytes_read as u64;
        reader.consume(used);
        if newline.is_some() {
            if !skipping_oversized {
                if line.last() == Some(&b'\r') {
                    line.pop();
                }
                if !line.is_empty() {
                    records.push(JsonlRecord {
                        byte_start: line_start,
                        byte_end: position,
                        payload: std::mem::take(&mut line),
                    });
                }
            }
            line.clear();
            skipping_oversized = false;
            offset = position;
            line_start = position;
        } else if skipping_oversized {
            offset = position;
            line_start = position;
        }
    }
    let has_more = offset < file_len;

    Ok(JsonlReadResult {
        records,
        next_offset: offset,
        file_len,
        file_mtime_ms,
        bytes_read,
        has_more,
        ignored_incomplete_tail,
        oversized_lines,
        source_change: None,
        skipping_oversized,
    })
}

pub fn cursor_offset(cursor: &Value) -> u64 {
    cursor
        .get("offset")
        .and_then(|v| v.as_u64())
        .or_else(|| {
            cursor
                .get("offset")
                .and_then(|v| v.as_i64())
                .map(|v| v as u64)
        })
        .unwrap_or(0)
}

pub fn cursor_with_offset(offset: u64) -> Value {
    json!({ "offset": offset })
}

pub fn boundary_with_len(len: u64) -> Value {
    json!({ "len": len })
}
