//! Shared budgeted JSONL read for file-backed harness streams.

use std::path::{Path, PathBuf};

use serde_json::json;

use crate::local_store::pipeline::runner::{
    read_jsonl_budgeted_with_state, CheckpointView, RawBatch, RawRecord, ReadBudget, RunnerError,
    SourceChange,
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
    let skipping = committed
        .cursor_json
        .get("skipping_oversized")
        .and_then(|v| v.as_bool())
        .unwrap_or(false);
    let result = read_jsonl_budgeted_with_state(path, offset, expected_len, budget, skipping)?;
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
        next_cursor_json: json!({ "offset": result.next_offset, "skipping_oversized": result.skipping_oversized }),
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
    discover_jsonl_files_in_roots(std::iter::once(root), glob_suffix, limit, resume_after)
}

/// Page once over the union of roots. A cursor belongs to this global ordering,
/// so no individual root can repeatedly wrap before another root's cursor.
pub fn discover_jsonl_files_in_roots<'a>(
    roots: impl IntoIterator<Item = &'a Path>,
    glob_suffix: &str,
    limit: usize,
    resume_after: Option<&str>,
) -> (Vec<PathBuf>, Option<String>) {
    if limit == 0 {
        return (Vec::new(), resume_after.map(|s| s.to_string()));
    }
    let mut all = Vec::new();
    for root in roots {
        if root.is_file() {
            let name = root.file_name().and_then(|s| s.to_str()).unwrap_or("");
            if name.ends_with(glob_suffix)
                || root.extension().and_then(|e| e.to_str()) == Some("jsonl")
            {
                all.push(root.to_path_buf());
            }
        } else if root.is_dir() {
            all.extend(list_matching_jsonl(root, glob_suffix));
        }
    }
    if all.is_empty() {
        return (Vec::new(), resume_after.map(|s| s.to_string()));
    }
    // Path equality and string ordering differ on Windows (mixed separators).
    // Deduplicate before sorting; equal paths are not necessarily adjacent in
    // string order when multiple roots describe the same directory.
    let mut seen = std::collections::HashSet::new();
    all.retain(|path| seen.insert(path.clone()));
    all.sort_by(|a, b| a.to_string_lossy().cmp(&b.to_string_lossy()));

    let start = resume_after
        .and_then(|after| {
            all.iter()
                .position(|p| p.to_string_lossy().as_ref() > after)
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

#[cfg(test)]
mod union_tests {
    use super::*;
    #[test]
    fn overlapping_roots_do_not_duplicate_sources_or_page_entries() {
        let temp = tempfile::tempdir().unwrap();
        let child = temp.path().join("nested");
        std::fs::create_dir_all(&child).unwrap();
        for file in ["a.jsonl", "b.jsonl"] {
            std::fs::write(child.join(file), "{}\n").unwrap();
        }
        let alternate = PathBuf::from(child.to_string_lossy().replace('\\', "/"));
        let (files, _) = discover_jsonl_files_in_roots(
            [child.as_path(), alternate.as_path(), temp.path()],
            ".jsonl",
            10,
            None,
        );
        assert_eq!(files.len(), 2);
        let (first, cursor) = discover_jsonl_files_in_roots(
            [child.as_path(), alternate.as_path()],
            ".jsonl",
            1,
            None,
        );
        let (second, _) = discover_jsonl_files_in_roots(
            [child.as_path(), alternate.as_path()],
            ".jsonl",
            1,
            cursor.as_deref(),
        );
        assert_ne!(first, second);
    }
}
