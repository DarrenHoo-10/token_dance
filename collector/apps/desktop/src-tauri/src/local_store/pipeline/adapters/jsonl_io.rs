//! Shared budgeted JSONL read for file-backed harness streams.

use std::path::{Path, PathBuf};

use serde_json::json;

use crate::local_store::pipeline::runner::{
    read_jsonl_budgeted, CheckpointView, RawBatch, RawRecord, ReadBudget, RunnerError, SourceChange,
};

pub fn read_jsonl_source(
    path: &Path,
    committed: &CheckpointView,
    budget: ReadBudget,
) -> Result<RawBatch, RunnerError> {
    let offset = committed
        .cursor_json
        .get("offset")
        .and_then(|v| v.as_u64())
        .unwrap_or(0);
    let expected_len = committed
        .observed_boundary_json
        .get("len")
        .and_then(|v| v.as_u64());
    let result = read_jsonl_budgeted(path, offset, expected_len, budget)?;
    if let Some(change) = result.source_change {
        match change {
            SourceChange::Truncated | SourceChange::IdentityMismatch => {
                return Err(RunnerError::SourceChanged(format!("{change:?}")));
            }
        }
    }
    let records = result
        .records
        .into_iter()
        .enumerate()
        .map(|(i, rec)| RawRecord {
            ordinal: i as u64,
            byte_start: Some(rec.byte_start),
            byte_end: Some(rec.byte_end),
            native_rowid: None,
            payload: rec.payload,
            file_mtime_ms: result.file_mtime_ms,
        })
        .collect();
    Ok(RawBatch {
        records,
        next_cursor_json: json!({ "offset": result.next_offset }),
        next_observed_boundary_json: json!({ "len": result.file_len }),
        has_more: result.has_more,
        bytes_read: result.bytes_read,
        ignored_incomplete_tail: result.ignored_incomplete_tail,
    })
}

pub fn discover_jsonl_files(root: &Path, glob_suffix: &str, limit: usize) -> Vec<PathBuf> {
    let mut out = Vec::new();
    if !root.exists() {
        return out;
    }
    let walker = walkdir_shallow(root, 6);
    for path in walker {
        if out.len() >= limit {
            break;
        }
        let name = path.file_name().and_then(|s| s.to_str()).unwrap_or("");
        if name.ends_with(glob_suffix) || path.extension().and_then(|e| e.to_str()) == Some("jsonl")
        {
            out.push(path);
        }
    }
    out
}

fn walkdir_shallow(root: &Path, max_depth: usize) -> Vec<PathBuf> {
    let mut out = Vec::new();
    let mut stack = vec![(root.to_path_buf(), 0usize)];
    while let Some((dir, depth)) = stack.pop() {
        let entries = match std::fs::read_dir(&dir) {
            Ok(e) => e,
            Err(_) => continue,
        };
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_dir() {
                if depth < max_depth {
                    stack.push((path, depth + 1));
                }
            } else {
                out.push(path);
            }
        }
    }
    out.sort();
    out
}
