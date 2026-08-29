use std::collections::{BTreeMap, BTreeSet};

use protocol::SourceCheckpointStatus;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SourceCheckpoint {
    pub source_id: String,
    pub path_template_id: String,
    pub file_identity: String,
    pub generation: u64,
    pub file_len: u64,
    pub offset: u64,
    pub last_record_hash: Option<String>,
    pub status: SourceCheckpointStatus,
}

impl SourceCheckpoint {
    pub fn key(&self) -> CheckpointKey {
        CheckpointKey {
            source_id: self.source_id.clone(),
            file_identity: self.file_identity.clone(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub struct CheckpointKey {
    pub source_id: String,
    pub file_identity: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Backpressure {
    Normal,
    Soft,
    Hard,
}

impl Backpressure {
    pub fn allow_historical_scan(self) -> bool {
        !matches!(self, Self::Hard)
    }

    pub fn historical_batch_limit(self, requested: usize) -> usize {
        match self {
            Self::Normal => requested,
            Self::Soft => requested.max(1) / 4 + 1,
            Self::Hard => 0,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Snapshot {
    pub last_sequence: u64,
    pub checkpoints: BTreeMap<CheckpointKey, SourceCheckpoint>,
    pub acked_event_ids: BTreeSet<String>,
    pub dead_letter_ids: BTreeSet<String>,
    pub unacked_event_ids: BTreeSet<String>,
    pub isolated_segments: Vec<IsolatedSegment>,
    pub created_at: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct IsolatedSegment {
    pub segment_id: u64,
    pub at_offset: u64,
    pub last_good_sequence: Option<u64>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RescanHint {
    pub source_id: String,
    pub safe_checkpoint: Option<SourceCheckpoint>,
    pub reason: String,
}
