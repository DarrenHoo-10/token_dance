//! DSH stores independently decodable Zstandard frames. Checkpoint the physical
//! frame offset plus a line index, so polling never decompresses the entire history.
use crate::local_store::pipeline::runner::{
    CheckpointView, RawBatch, RawRecord, ReadBudget, RunnerError,
};
use serde_json::json;
use std::{
    fs::File,
    io::{Read, Seek, SeekFrom},
    path::Path,
    time::{Instant, UNIX_EPOCH},
};
const MAX_FRAME: usize = 32 * 1024 * 1024;
fn io(e: std::io::Error) -> RunnerError {
    RunnerError::Io(e.to_string())
}
fn take(file: &mut File, buf: &mut Vec<u8>, n: usize) -> Result<bool, RunnerError> {
    if n > MAX_FRAME.saturating_sub(buf.len()) {
        return Err(RunnerError::DecodeBlocked(
            "DSH frame exceeds 32 MiB compressed limit".into(),
        ));
    }
    let start = buf.len();
    buf.resize(start + n, 0);
    match file.read_exact(&mut buf[start..]) {
        Ok(()) => Ok(true),
        Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => Ok(false),
        Err(e) => Err(io(e)),
    }
}
/// None is an incomplete append; it never advances the committed frame offset.
fn frame(file: &mut File) -> Result<Option<Vec<u8>>, RunnerError> {
    let mut b = Vec::new();
    if !take(file, &mut b, 5)? {
        return Ok(None);
    }
    if b[..4] != [0x28, 0xb5, 0x2f, 0xfd] {
        return Err(RunnerError::DecodeBlocked(
            "invalid DSH Zstandard frame magic".into(),
        ));
    }
    let d = b[4];
    if d & 0x18 != 0 {
        return Err(RunnerError::DecodeBlocked(
            "invalid DSH frame header".into(),
        ));
    }
    let size_flag = d >> 6;
    let single = d & 0x20 != 0;
    let checksum = d & 4 != 0;
    let dictionary = match d & 3 {
        3 => 4,
        n => n as usize,
    };
    let size = if size_flag == 0 {
        usize::from(single)
    } else {
        1usize << size_flag
    };
    if !take(file, &mut b, usize::from(!single) + dictionary + size)? {
        return Ok(None);
    }
    loop {
        let start = b.len();
        if !take(file, &mut b, 3)? {
            return Ok(None);
        }
        let h = (b[start] as u32) | ((b[start + 1] as u32) << 8) | ((b[start + 2] as u32) << 16);
        let kind = (h >> 1) & 3;
        if kind == 3 {
            return Err(RunnerError::DecodeBlocked("invalid DSH block type".into()));
        }
        let n = if kind == 1 { 1 } else { (h >> 3) as usize };
        if !take(file, &mut b, n)? {
            return Ok(None);
        }
        if h & 1 != 0 {
            break;
        }
    }
    if checksum && !take(file, &mut b, 4)? {
        return Ok(None);
    }
    Ok(Some(b))
}
pub fn read_source(
    path: &Path,
    committed: &CheckpointView,
    budget: ReadBudget,
) -> Result<RawBatch, RunnerError> {
    let start = Instant::now();
    let mut file = File::open(path).map_err(io)?;
    let meta = file.metadata().map_err(io)?;
    let len = meta.len();
    let mtime = meta
        .modified()
        .ok()
        .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
        .and_then(|t| i64::try_from(t.as_millis()).ok());
    let mut offset = committed.cursor_json["offset"].as_u64().unwrap_or(0);
    let mut line = committed.cursor_json["frame_line"].as_u64().unwrap_or(0) as usize;
    if offset > len
        || committed.observed_boundary_json["len"]
            .as_u64()
            .is_some_and(|old| old > len)
    {
        return Err(RunnerError::SourceChanged(
            "DSH compressed source truncated".into(),
        ));
    }
    let mut records = Vec::new();
    let mut bytes = 0;
    let mut incomplete = false;
    while offset < len && !budget.exhausted(records.len(), bytes, start.elapsed()) {
        file.seek(SeekFrom::Start(offset)).map_err(io)?;
        let Some(compressed) = frame(&mut file)? else {
            incomplete = true;
            break;
        };
        let end = offset + compressed.len() as u64;
        let decoder = zstd::stream::read::Decoder::new(compressed.as_slice()).map_err(io)?;
        let mut decoded = Vec::new();
        decoder
            .take(MAX_FRAME as u64 + 1)
            .read_to_end(&mut decoded)
            .map_err(io)?;
        if decoded.len() > MAX_FRAME {
            return Err(RunnerError::DecodeBlocked(
                "DSH frame exceeds 32 MiB decoded limit".into(),
            ));
        }
        if !decoded.ends_with(b"\n") {
            return Err(RunnerError::DecodeBlocked(
                "complete DSH frame has incomplete JSONL record".into(),
            ));
        }
        let lines: Vec<_> = decoded[..decoded.len() - 1]
            .split(|b| *b == b'\n')
            .collect();
        if line > lines.len() {
            return Err(RunnerError::SourceChanged(
                "DSH frame checkpoint exceeds line count".into(),
            ));
        }
        while line < lines.len() {
            if !records.is_empty() && budget.exhausted(records.len(), bytes, start.elapsed()) {
                break;
            }
            let payload = lines[line].to_vec();
            bytes += payload.len();
            records.push(RawRecord {
                ordinal: line as u64,
                byte_start: Some(offset),
                byte_end: Some(end),
                native_rowid: None,
                payload,
                file_mtime_ms: mtime,
            });
            line += 1;
        }
        if line < lines.len() {
            break;
        }
        offset = end;
        line = 0;
    }
    Ok(RawBatch {
        records,
        next_cursor_json: json!({"offset":offset,"frame_line":line}),
        next_observed_boundary_json: json!({"len":len}),
        has_more: offset < len && !incomplete,
        bytes_read: bytes,
        ignored_incomplete_tail: incomplete,
    })
}
#[cfg(test)]
mod tests {
    use super::*;
    fn checkpoint() -> CheckpointView {
        CheckpointView {
            cursor_json: json!({"offset":0}),
            decoder_state_version: 1,
            decoder_state_json: json!({}),
            observed_boundary_json: json!({}),
            commit_seq: 0,
        }
    }
    #[test]
    fn frames_page_and_resume_without_skipping_partial_append() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("session.jsonl.zstd");
        let first = zstd::stream::encode_all(&b"{\"type\":\"session\"}\n"[..], 1).unwrap();
        let second = zstd::stream::encode_all(&b"{\"seq\":1}\n{\"seq\":2}\n"[..], 1).unwrap();
        let mut disk = first.clone();
        disk.extend_from_slice(&second[..second.len() - 2]);
        std::fs::write(&path, &disk).unwrap();
        let mut cp = checkpoint();
        let b = read_source(&path, &cp, ReadBudget::new(10, 1024, 1000)).unwrap();
        assert_eq!(b.records.len(), 1);
        assert!(b.ignored_incomplete_tail);
        assert_eq!(b.next_cursor_json["offset"], first.len());
        cp.cursor_json = b.next_cursor_json;
        cp.observed_boundary_json = b.next_observed_boundary_json;
        disk = first;
        disk.extend_from_slice(&second);
        std::fs::write(&path, disk).unwrap();
        let b = read_source(&path, &cp, ReadBudget::new(1, 1024, 1000)).unwrap();
        assert_eq!(b.records.len(), 1);
        assert!(b.has_more);
        assert_eq!(b.next_cursor_json["frame_line"], 1);
        cp.cursor_json = b.next_cursor_json;
        let b = read_source(&path, &cp, ReadBudget::new(1, 1024, 1000)).unwrap();
        assert_eq!(b.records[0].payload, b"{\"seq\":2}");
        assert!(!b.has_more);
    }
}
