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

/// Discover JSONL files under `root`.
///
/// - If `root` is a **file**, treat it as a single-file locator (e.g. `history.jsonl`).
/// - If `root` is a directory, shallow-walk and return a bounded slice.
/// - `resume_after` enables fair rotation: return the next `limit` paths after the
///   given lexicographic cursor, wrapping to the start when needed.
///
/// Returns `(files, next_resume_cursor)` where the cursor is the last returned path
/// (for the next discover tick).
pub fn discover_jsonl_files(
    root: &Path,
    glob_suffix: &str,
    limit: usize,
    resume_after: Option<&str>,
) -> (Vec<PathBuf>, Option<String>) {
    if limit == 0 || !root.exists() {
        return (Vec::new(), resume_after.map(|s| s.to_string()));
    }

    if root.is_file() {
        let name = root.file_name().and_then(|s| s.to_str()).unwrap_or("");
        let matches = name.ends_with(glob_suffix)
            || root.extension().and_then(|e| e.to_str()) == Some("jsonl");
        if matches {
            let path = root.to_path_buf();
            let cursor = Some(path.to_string_lossy().into_owned());
            return (vec![path], cursor);
        }
        return (Vec::new(), resume_after.map(|s| s.to_string()));
    }

    let mut all = list_matching_jsonl(root, glob_suffix);
    if all.is_empty() {
        return (Vec::new(), resume_after.map(|s| s.to_string()));
    }
    all.sort();

    let start = resume_after
        .and_then(|after| {
            all.iter()
                .position(|p| p.to_string_lossy().as_ref() > after)
                .or_else(|| {
                    // Exact match: continue after it.
                    all.iter()
                        .position(|p| p.to_string_lossy().as_ref() == after)
                        .map(|i| i + 1)
                })
        })
        .unwrap_or(0);

    let n = all.len();
    let take = limit.min(n);
    let mut out = Vec::with_capacity(take);
    for i in 0..take {
        out.push(all[(start + i) % n].clone());
    }
    let next_cursor = out
        .last()
        .map(|p| p.to_string_lossy().into_owned())
        .or_else(|| resume_after.map(|s| s.to_string()));
    (out, next_cursor)
}

fn list_matching_jsonl(root: &Path, glob_suffix: &str) -> Vec<PathBuf> {
    let mut out = Vec::new();
    for path in walkdir_shallow(root, 6) {
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
