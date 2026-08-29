use std::fs::{self, File};
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};

use adapter_sdk::{RawFrame, SourceKind};
use protocol::SourceCheckpointStatus;
use wal_spool::{Backpressure, SourceCheckpoint};

use crate::error::AcquisitionError;
use crate::identity::{created_stamp, file_identity, prefix_stamp, record_hash};

pub const MAX_LINE_BYTES: u64 = 4 * 1024 * 1024;

#[derive(Debug, Clone)]
pub struct PollResult {
    pub frames: Vec<RawFrame>,
    pub next_checkpoint: SourceCheckpoint,
    pub status: SourceCheckpointStatus,
    pub historical: bool,
    pub skipped: bool,
    pub accepted_events: usize,
    pub diagnostics: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct JsonlTailer {
    pub logical_source_id: String,
    pub path_template_id: String,
    pub installation_id: String,
    path: PathBuf,
    identity: Option<String>,
    generation: u64,
    offset: u64,
    last_record_hash: Option<String>,
}

impl JsonlTailer {
    pub fn new(
        installation_id: impl Into<String>,
        logical_source_id: impl Into<String>,
        path_template_id: impl Into<String>,
        path: impl Into<PathBuf>,
    ) -> Self {
        Self {
            logical_source_id: logical_source_id.into(),
            path_template_id: path_template_id.into(),
            installation_id: installation_id.into(),
            path: path.into(),
            identity: None,
            generation: 1,
            offset: 0,
            last_record_hash: None,
        }
    }

    pub fn restore(&mut self, checkpoint: &SourceCheckpoint) {
        self.identity = Some(checkpoint.file_identity.clone());
        self.generation = checkpoint.generation;
        self.offset = checkpoint.offset;
        self.last_record_hash = checkpoint.last_record_hash.clone();
    }

    pub fn reset_for_rescan(&mut self) {
        self.offset = 0;
        self.last_record_hash = None;
    }

    pub fn poll(
        &mut self,
        backpressure: Backpressure,
        historical: bool,
    ) -> Result<PollResult, AcquisitionError> {
        if historical && !backpressure.allow_historical_scan() {
            return Ok(PollResult {
                frames: Vec::new(),
                next_checkpoint: self.current_checkpoint(0, SourceCheckpointStatus::Stale),
                status: SourceCheckpointStatus::Stale,
                historical,
                skipped: true,
                accepted_events: 0,
                diagnostics: vec!["historical_scan_paused_hard_backpressure".into()],
            });
        }
        if !self.path.exists() {
            return Ok(PollResult {
                frames: Vec::new(),
                next_checkpoint: self.current_checkpoint(0, SourceCheckpointStatus::Discovered),
                status: SourceCheckpointStatus::Discovered,
                historical,
                skipped: false,
                accepted_events: 0,
                diagnostics: Vec::new(),
            });
        }
        let meta = fs::metadata(&self.path).map_err(|err| AcquisitionError::io(&self.path, err))?;
        let observed = file_identity(&self.path, &meta);
        let file_len = meta.len();
        let mut status = SourceCheckpointStatus::Current;
        let identity = if let Some(previous) = &self.identity {
            let same_prefix = prefix_stamp(previous) == prefix_stamp(&observed);
            if file_len < self.offset && same_prefix {
                self.generation += 1;
                self.offset = 0;
                self.last_record_hash = None;
                status = SourceCheckpointStatus::Truncated;
                previous.clone()
            } else if created_stamp(previous) != created_stamp(&observed) || !same_prefix {
                self.generation += 1;
                self.offset = 0;
                self.last_record_hash = None;
                status = SourceCheckpointStatus::Rotated;
                observed
            } else {
                previous.clone()
            }
        } else {
            observed
        };
        self.identity = Some(identity);
        let mut file =
            File::open(&self.path).map_err(|err| AcquisitionError::io(&self.path, err))?;
        file.seek(SeekFrom::Start(self.offset))
            .map_err(|err| AcquisitionError::io(&self.path, err))?;
        let mut buf = Vec::new();
        file.read_to_end(&mut buf)
            .map_err(|err| AcquisitionError::io(&self.path, err))?;

        let mut frames = Vec::new();
        let mut diagnostics = Vec::new();
        let mut consumed = 0usize;
        let mut start = 0usize;
        let limit = backpressure.historical_batch_limit(if historical { 32 } else { usize::MAX });
        while start < buf.len() {
            if frames.len() >= limit {
                break;
            }
            match buf[start..].iter().position(|byte| *byte == b'\n') {
                Some(rel) => {
                    let end = start + rel;
                    let line = if end > start && buf[end - 1] == b'\r' {
                        &buf[start..end - 1]
                    } else {
                        &buf[start..end]
                    };
                    if line.len() as u64 > MAX_LINE_BYTES {
                        diagnostics.push("line_too_large_rejected".into());
                    } else if !line.is_empty() {
                        frames.push(self.frame(line, self.offset + start as u64));
                        self.last_record_hash = Some(record_hash(line));
                    }
                    start = end + 1;
                    consumed = start;
                }
                None => {
                    let pending = (buf.len() - start) as u64;
                    if pending > MAX_LINE_BYTES {
                        diagnostics.push("line_too_large_rejected".into());
                        start = buf.len();
                        consumed = start;
                    }
                    break;
                }
            }
        }
        self.offset += consumed as u64;
        Ok(PollResult {
            frames,
            next_checkpoint: self.current_checkpoint(file_len, status),
            status,
            historical,
            skipped: false,
            accepted_events: 0,
            diagnostics,
        })
    }

    fn frame(&self, line: &[u8], offset: u64) -> RawFrame {
        RawFrame {
            installation_id: self.installation_id.clone(),
            source_kind: SourceKind::JsonlTail,
            source_id: self.logical_source_id.clone(),
            cursor: format!(
                "{}:{}:{offset}",
                self.identity.as_deref().unwrap_or("unknown"),
                self.generation
            ),
            payload: line.to_vec(),
        }
    }

    fn current_checkpoint(
        &self,
        file_len: u64,
        status: SourceCheckpointStatus,
    ) -> SourceCheckpoint {
        SourceCheckpoint {
            source_id: self.logical_source_id.clone(),
            path_template_id: self.path_template_id.clone(),
            file_identity: self.identity.clone().unwrap_or_default(),
            generation: self.generation,
            file_len,
            offset: self.offset,
            last_record_hash: self.last_record_hash.clone(),
            driver_checkpoint: None,
            status,
        }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }
}
