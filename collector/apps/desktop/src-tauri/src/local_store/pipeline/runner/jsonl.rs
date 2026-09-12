//! Budgeted JSONL offset reader: complete newlines only; triple budget; raw EOF.

use std::fs::File;
use std::io::{Read, Seek, SeekFrom};
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
}

/// Read JSONL from a committed byte offset with record/byte/time budgets.
/// Incomplete trailing lines do not advance `next_offset`.
pub fn read_jsonl_budgeted(
    path: &Path,
    committed_offset: u64,
    expected_len_hint: Option<u64>,
    budget: ReadBudget,
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
            });
        }
    }

    let mut file = File::open(path).map_err(|e| RunnerError::Io(e.to_string()))?;
    file.seek(SeekFrom::Start(committed_offset))
        .map_err(|e| RunnerError::Io(e.to_string()))?;

    let started = Instant::now();
    let mut records = Vec::new();
    let mut bytes_read = 0usize;
    let mut oversized_lines = 0usize;
    let mut offset = committed_offset;
    let mut carry = Vec::new();
    let mut chunk = vec![0u8; CHUNK];
    let mut ignored_incomplete_tail = false;

    loop {
        if budget.exhausted(records.len(), bytes_read, started.elapsed()) {
            break;
        }
        let n = file
            .read(&mut chunk)
            .map_err(|e| RunnerError::Io(e.to_string()))?;
        if n == 0 {
            if !carry.is_empty() {
                ignored_incomplete_tail = true;
            }
            break;
        }
        bytes_read = bytes_read.saturating_add(n);
        carry.extend_from_slice(&chunk[..n]);

        let mut start = 0usize;
        while start < carry.len() {
            if budget.exhausted(records.len(), bytes_read, started.elapsed()) {
                break;
            }
            match carry[start..].iter().position(|b| *b == b'\n') {
                Some(rel) => {
                    let end = start + rel;
                    let mut line = &carry[start..end];
                    if line.last() == Some(&b'\r') {
                        line = &line[..line.len().saturating_sub(1)];
                    }
                    let line_start = offset + start as u64;
                    let line_end = offset + end as u64 + 1; // include newline
                    if line.len() > MAX_LINE_BYTES {
                        oversized_lines += 1;
                    } else if !line.is_empty() {
                        records.push(JsonlRecord {
                            byte_start: line_start,
                            byte_end: line_end,
                            payload: line.to_vec(),
                        });
                    }
                    start = end + 1;
                }
                None => break,
            }
        }
        if start > 0 {
            offset += start as u64;
            carry.drain(..start);
        }
        if carry.len() > MAX_LINE_BYTES {
            // Poisonously large unterminated span: skip one byte to make progress
            // and count as oversized; callers may Ignore.
            oversized_lines += 1;
            offset += 1;
            carry.remove(0);
        }
        if n < CHUNK && carry.iter().all(|b| *b != b'\n') {
            // Likely EOF mid-line; keep incomplete tail uncommitted.
            if !carry.is_empty() {
                ignored_incomplete_tail = true;
            }
            break;
        }
    }

    // Raw has_more is decided by source bytes, never by decoded emit count.
    let has_more = offset < file_len || !carry.is_empty();

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
    })
}

pub fn cursor_offset(cursor: &Value) -> u64 {
    cursor
        .get("offset")
        .and_then(|v| v.as_u64())
        .or_else(|| cursor.get("offset").and_then(|v| v.as_i64()).map(|v| v as u64))
        .unwrap_or(0)
}

pub fn cursor_with_offset(offset: u64) -> Value {
    json!({ "offset": offset })
}

pub fn boundary_with_len(len: u64) -> Value {
    json!({ "len": len })
}
