use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Instant;

use protocol::{EventDeliveryStatus, EventEnvelope};
use sha2::{Digest, Sha256};

use crate::checkpoint::{
    Backpressure, CheckpointKey, IsolatedSegment, RescanHint, Snapshot, SourceCheckpoint,
};
use crate::codec::{encode_frame, read_frame, EncodedFrame, FrameRead, FrameType, MAGIC};
use crate::crypto::{decrypt, encrypt};
use crate::error::WalError;
use crate::fault::{FaultHook, FaultPoint};
use crate::frame::{
    decode_cbor, encode_cbor, AckPayload, DeadLetterPayload, PersistedTransaction, SettingsPayload,
    Transaction,
};
use crate::ids::{new_prefixed_id, utc_now_rfc3339};
use crate::keys::{KeyProvider, DATA_KEY_LEN};
use crate::limits::{AppendClass, SpoolLimits};

const SNAPSHOT_MAGIC: &[u8; 4] = b"TSS1";

pub struct WalStore {
    root: PathBuf,
    keys: Arc<dyn KeyProvider>,
    limits: SpoolLimits,
    writer: Option<SegmentWriter>,
    next_segment_id: u64,
    next_sequence: u64,
    checkpoints: BTreeMap<CheckpointKey, SourceCheckpoint>,
    unacked_order: Vec<String>,
    unacked: HashMap<String, EventEnvelope>,
    acked: BTreeSet<String>,
    dead_letters: BTreeSet<String>,
    isolated: Vec<IsolatedSegment>,
    frames_since_snapshot: u64,
    last_snapshot: Instant,
    snapshot_seq: u64,
    reopen_segment: Option<u64>,
    fault: FaultHook,
}

struct SegmentWriter {
    id: u64,
    path: PathBuf,
    file: File,
    bytes: u64,
    frames: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CompactReport {
    pub deleted_segments: Vec<u64>,
    pub rewritten_segments: Vec<u64>,
}

impl WalStore {
    pub fn open(root: impl AsRef<Path>, keys: Arc<dyn KeyProvider>) -> Result<Self, WalError> {
        Self::open_with_limits(root, keys, SpoolLimits::default())
    }

    pub fn open_with_limits(
        root: impl AsRef<Path>,
        keys: Arc<dyn KeyProvider>,
        limits: SpoolLimits,
    ) -> Result<Self, WalError> {
        let key = load_key(&keys)?;
        let root = root.as_ref().to_path_buf();
        fs::create_dir_all(root.join("wal")).map_err(|err| WalError::io(&root, err))?;
        fs::create_dir_all(root.join("snapshots")).map_err(|err| WalError::io(&root, err))?;

        let mut store = Self {
            root: root.clone(),
            keys,
            limits,
            writer: None,
            next_segment_id: 1,
            next_sequence: 1,
            checkpoints: BTreeMap::new(),
            unacked_order: Vec::new(),
            unacked: HashMap::new(),
            acked: BTreeSet::new(),
            dead_letters: BTreeSet::new(),
            isolated: Vec::new(),
            frames_since_snapshot: 0,
            last_snapshot: Instant::now(),
            snapshot_seq: 0,
            reopen_segment: None,
            fault: FaultHook::default(),
        };
        let snapshot_seq = store.load_snapshot(&key)?;
        store.snapshot_seq = snapshot_seq;
        store.replay_all(&key, snapshot_seq)?;
        store.open_writer()?;
        Ok(store)
    }

    pub fn inject_fault(&mut self, point: FaultPoint) {
        self.fault.push(point);
    }

    pub fn append_txn(
        &mut self,
        mut txn: Transaction,
        class: AppendClass,
    ) -> Result<u64, WalError> {
        validate_transaction_checkpoint(&txn)?;
        self.guard_key()?;
        let bp = self.backpressure();
        if class == AppendClass::Historical && !bp.allow_historical_scan() {
            return Err(WalError::HardBackpressure);
        }
        txn.normalized_events
            .retain(|event| !self.acked.contains(&event.event_id));
        if txn.transaction_id.is_empty() {
            txn.transaction_id = new_prefixed_id("txn");
        }
        if txn.created_at.is_empty() {
            txn.created_at = utc_now_rfc3339();
        }
        let persisted = PersistedTransaction::from(&txn);
        let seq = self.write_payload(
            FrameType::Txn,
            &encode_cbor(&persisted).map_err(WalError::Payload)?,
        )?;
        self.apply_txn(txn, true);
        self.maybe_snapshot()?;
        Ok(seq)
    }

    pub fn append_ack(&mut self, mut ack: AckPayload) -> Result<u64, WalError> {
        self.guard_key()?;
        if ack.batch_id.is_empty() {
            ack.batch_id = new_prefixed_id("bat");
        }
        if ack.server_acked_at.is_empty() {
            ack.server_acked_at = utc_now_rfc3339();
        }
        let seq = self.write_payload(
            FrameType::Ack,
            &encode_cbor(&ack).map_err(WalError::Payload)?,
        )?;
        self.apply_ack(&ack);
        self.maybe_snapshot()?;
        Ok(seq)
    }

    pub fn append_dead_letter(&mut self, mut payload: DeadLetterPayload) -> Result<u64, WalError> {
        self.guard_key()?;
        if !is_safe_diagnostic_code(&payload.safe_reason) {
            payload.safe_reason = "redacted".into();
        }
        if payload.created_at.is_empty() {
            payload.created_at = utc_now_rfc3339();
        }
        let seq = self.write_payload(
            FrameType::DeadLetter,
            &encode_cbor(&payload).map_err(WalError::Payload)?,
        )?;
        self.apply_dead_letter(&payload);
        Ok(seq)
    }

    pub fn checkpoint(&self, source_id: &str, file_identity: &str) -> Option<&SourceCheckpoint> {
        self.checkpoints.get(&CheckpointKey {
            source_id: source_id.to_string(),
            file_identity: file_identity.to_string(),
        })
    }

    pub fn latest_checkpoint(&self, source_id: &str) -> Option<&SourceCheckpoint> {
        self.checkpoints
            .values()
            .filter(|item| item.source_id == source_id)
            .max_by_key(|item| (item.generation, item.offset))
    }

    pub fn checkpoints(&self) -> impl Iterator<Item = &SourceCheckpoint> {
        self.checkpoints.values()
    }

    pub fn unacked_events(&self) -> Vec<EventEnvelope> {
        self.unacked_order
            .iter()
            .filter_map(|id| self.unacked.get(id).cloned())
            .collect()
    }

    pub fn event_status(&self, event_id: &str) -> Option<EventDeliveryStatus> {
        if self.acked.contains(event_id) {
            Some(EventDeliveryStatus::Acked)
        } else if self.dead_letters.contains(event_id) {
            Some(EventDeliveryStatus::DeadLetter)
        } else if self.unacked.contains_key(event_id) {
            Some(EventDeliveryStatus::Queued)
        } else {
            None
        }
    }

    pub fn backpressure(&self) -> Backpressure {
        let bytes = self.spool_bytes();
        let events = self.unacked.len() as u64 + self.dead_letters.len() as u64;
        if bytes >= self.limits.hard_bytes || events >= self.limits.hard_events {
            Backpressure::Hard
        } else if bytes >= self.limits.soft_bytes || events >= self.limits.soft_events {
            Backpressure::Soft
        } else {
            Backpressure::Normal
        }
    }

    pub fn spool_bytes(&self) -> u64 {
        let wal_dir = self.root.join("wal");
        let Ok(entries) = fs::read_dir(&wal_dir) else {
            return 0;
        };
        entries
            .filter_map(|entry| entry.ok())
            .filter_map(|entry| entry.metadata().ok())
            .map(|meta| meta.len())
            .sum()
    }

    pub fn unacked_count(&self) -> usize {
        self.unacked.len()
    }

    pub fn isolated_segments(&self) -> &[IsolatedSegment] {
        &self.isolated
    }

    pub fn needs_rescan(&self) -> Vec<RescanHint> {
        self.isolated
            .iter()
            .filter_map(|_| {
                let checkpoint = self.checkpoints.values().next().cloned();
                checkpoint.map(|safe_checkpoint| RescanHint {
                    source_id: safe_checkpoint.source_id.clone(),
                    safe_checkpoint: Some(safe_checkpoint),
                    reason: "wal segment isolated after mid-file corruption".into(),
                })
            })
            .collect()
    }

    pub fn snapshot(&mut self) -> Result<(), WalError> {
        let key = load_key(&self.keys)?;
        let snap = Snapshot {
            last_sequence: self.next_sequence.saturating_sub(1),
            checkpoints: self.checkpoints.clone(),
            acked_event_ids: self.acked.clone(),
            dead_letter_ids: self.dead_letters.clone(),
            unacked_event_ids: self.unacked.keys().cloned().collect(),
            isolated_segments: self.isolated.clone(),
            created_at: utc_now_rfc3339(),
        };
        let plaintext = encode_cbor(&snap).map_err(WalError::Payload)?;
        let aad = SNAPSHOT_MAGIC;
        let encrypted = encrypt(&key, snap.last_sequence, aad, &plaintext)?;
        let mut body = Vec::with_capacity(4 + 32 + 4 + encrypted.len());
        body.extend_from_slice(SNAPSHOT_MAGIC);
        let hash = Sha256::digest(&encrypted);
        body.extend_from_slice(&hash);
        body.extend_from_slice(&(encrypted.len() as u32).to_le_bytes());
        body.extend_from_slice(&encrypted);
        let dir = self.root.join("snapshots");
        let name = format!("checkpoint-{:016}.cbor", snap.last_sequence);
        let tmp = dir.join(format!("{name}.tmp"));
        let final_path = dir.join(name);
        {
            let mut file = File::create(&tmp).map_err(|err| WalError::io(&tmp, err))?;
            file.write_all(&body)
                .map_err(|err| WalError::io(&tmp, err))?;
            file.sync_all().map_err(|err| WalError::io(&tmp, err))?;
        }
        fs::rename(&tmp, &final_path).map_err(|err| WalError::io(&final_path, err))?;
        self.frames_since_snapshot = 0;
        self.last_snapshot = Instant::now();
        Ok(())
    }

    pub fn compact(&mut self) -> Result<CompactReport, WalError> {
        self.snapshot()?;
        let active_id = self.writer.as_ref().map(|writer| writer.id);
        let mut deleted = Vec::new();
        let mut rewritten = Vec::new();
        let ids = list_segment_ids(&self.root.join("wal"))?;
        let key = load_key(&self.keys)?;
        for id in ids {
            if Some(id) == active_id {
                continue;
            }
            if self.isolated.iter().any(|item| item.segment_id == id) {
                continue;
            }
            let path = segment_path(&self.root, id);
            let data = fs::read(&path).map_err(|err| WalError::io(&path, err))?;
            let frames = decode_segment(&data, &key, self.limits.max_frame_payload)?;
            let keep = frames_to_keep(&frames, &self.acked);
            if keep.is_empty() {
                drop_writer_if_matches(&mut self.writer, id);
                fs::remove_file(&path).map_err(|err| WalError::io(&path, err))?;
                deleted.push(id);
                continue;
            }
            if keep.len() == frames.len() {
                continue;
            }
            let tmp = path.with_extension("wal.compact");
            let mut file = File::create(&tmp).map_err(|err| WalError::io(&tmp, err))?;
            for frame in keep {
                let encoded =
                    encode_frame(&key, frame.sequence, frame.frame_type, &frame.plaintext)?;
                file.write_all(&encoded.bytes)
                    .map_err(|err| WalError::io(&tmp, err))?;
            }
            file.sync_all().map_err(|err| WalError::io(&tmp, err))?;
            drop(file);
            fs::rename(&tmp, &path).map_err(|err| WalError::io(&path, err))?;
            rewritten.push(id);
        }
        Ok(CompactReport {
            deleted_segments: deleted,
            rewritten_segments: rewritten,
        })
    }

    pub fn decoded_payload_bytes(&self) -> Result<Vec<u8>, WalError> {
        let key = load_key(&self.keys)?;
        let mut out = Vec::new();
        for id in list_segment_ids(&self.root.join("wal"))? {
            let path = segment_path(&self.root, id);
            let data = fs::read(&path).map_err(|err| WalError::io(&path, err))?;
            let frames = decode_segment(&data, &key, self.limits.max_frame_payload)?;
            for frame in frames {
                out.extend_from_slice(&frame.plaintext);
            }
        }
        Ok(out)
    }

    pub fn raw_wal_bytes(&self) -> Result<Vec<u8>, WalError> {
        let mut out = Vec::new();
        for id in list_segment_ids(&self.root.join("wal"))? {
            let path = segment_path(&self.root, id);
            let mut data = fs::read(&path).map_err(|err| WalError::io(&path, err))?;
            out.append(&mut data);
        }
        Ok(out)
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    pub fn next_sequence(&self) -> u64 {
        self.next_sequence
    }

    pub fn segment_ids(&self) -> Result<Vec<u64>, WalError> {
        list_segment_ids(&self.root.join("wal"))
    }

    fn write_payload(&mut self, frame_type: FrameType, plaintext: &[u8]) -> Result<u64, WalError> {
        if plaintext.len() > self.limits.max_frame_payload as usize {
            return Err(WalError::FrameTooLarge);
        }
        self.maybe_rotate()?;
        let key = load_key(&self.keys)?;
        let sequence = self.next_sequence;
        let encoded = encode_frame(&key, sequence, frame_type, plaintext)?;
        match self.write_encoded(encoded) {
            Ok(()) => {
                self.next_sequence += 1;
                self.frames_since_snapshot += 1;
                Ok(sequence)
            }
            Err(WalError::InjectedCrash) => Err(WalError::InjectedCrash),
            Err(err) => Err(err),
        }
    }

    fn write_encoded(&mut self, encoded: EncodedFrame) -> Result<(), WalError> {
        let fault = self.fault.take();
        let writer = self
            .writer
            .as_mut()
            .ok_or_else(|| WalError::Codec("WAL writer is not open".into()))?;
        match fault {
            Some(FaultPoint::BeforeWrite) => Err(WalError::InjectedCrash),
            Some(FaultPoint::PartialWrite { bytes }) => {
                let n = bytes.min(encoded.bytes.len());
                writer
                    .file
                    .write_all(&encoded.bytes[..n])
                    .map_err(|err| WalError::io(&writer.path, err))?;
                let _ = writer.file.sync_all();
                Err(WalError::InjectedCrash)
            }
            Some(FaultPoint::WriteSkipFsyncAbort) => {
                writer
                    .file
                    .write_all(&encoded.bytes)
                    .map_err(|err| WalError::io(&writer.path, err))?;
                Err(WalError::InjectedCrash)
            }
            Some(FaultPoint::DurableWriteAbort) => {
                writer
                    .file
                    .write_all(&encoded.bytes)
                    .map_err(|err| WalError::io(&writer.path, err))?;
                writer
                    .file
                    .sync_all()
                    .map_err(|err| WalError::io(&writer.path, err))?;
                Err(WalError::InjectedCrash)
            }
            None => {
                writer
                    .file
                    .write_all(&encoded.bytes)
                    .map_err(|err| WalError::io(&writer.path, err))?;
                writer
                    .file
                    .sync_all()
                    .map_err(|err| WalError::io(&writer.path, err))?;
                writer.bytes += encoded.bytes.len() as u64;
                writer.frames += 1;
                Ok(())
            }
        }
    }

    fn maybe_rotate(&mut self) -> Result<(), WalError> {
        let needs = self.writer.as_ref().is_some_and(|writer| {
            writer.frames > 0
                && (writer.bytes >= self.limits.rotate_bytes
                    || writer.frames >= self.limits.rotate_frames)
        });
        if needs {
            if let Some(writer) = self.writer.take() {
                writer
                    .file
                    .sync_all()
                    .map_err(|err| WalError::io(&writer.path, err))?;
            }
            self.open_writer()?;
        } else if self.writer.is_none() {
            self.open_writer()?;
        }
        Ok(())
    }

    fn open_writer(&mut self) -> Result<(), WalError> {
        let id = if let Some(id) = self.reopen_segment.take() {
            id
        } else {
            let mut id = self.next_segment_id;
            while self.isolated.iter().any(|item| item.segment_id == id) {
                id += 1;
            }
            self.next_segment_id = id + 1;
            id
        };
        let path = segment_path(&self.root, id);
        let file = OpenOptions::new()
            .create(true)
            .append(true)
            .read(true)
            .open(&path)
            .map_err(|err| WalError::io(&path, err))?;
        let bytes = file.metadata().map(|meta| meta.len()).unwrap_or(0);
        self.writer = Some(SegmentWriter {
            id,
            path,
            file,
            bytes,
            frames: 0,
        });
        if id >= self.next_segment_id {
            self.next_segment_id = id + 1;
        }
        Ok(())
    }

    fn apply_txn(&mut self, txn: Transaction, apply_checkpoint: bool) {
        for event in txn.normalized_events {
            if self.acked.contains(&event.event_id) || self.dead_letters.contains(&event.event_id) {
                continue;
            }
            if !self.unacked.contains_key(&event.event_id) {
                self.unacked_order.push(event.event_id.clone());
            }
            self.unacked.insert(event.event_id.clone(), event);
        }
        if apply_checkpoint {
            self.checkpoints
                .insert(txn.next_checkpoint.key(), txn.next_checkpoint);
        }
    }

    fn apply_ack(&mut self, ack: &AckPayload) {
        for event_id in &ack.acked_event_ids {
            self.unacked.remove(event_id);
            self.acked.insert(event_id.clone());
        }
        self.unacked_order
            .retain(|event_id| self.unacked.contains_key(event_id));
    }

    fn apply_dead_letter(&mut self, payload: &DeadLetterPayload) {
        if let Some(event_id) = &payload.event_id {
            self.unacked.remove(event_id);
            self.dead_letters.insert(event_id.clone());
            self.unacked_order
                .retain(|item| self.unacked.contains_key(item));
        }
    }

    fn replay_all(&mut self, key: &[u8; DATA_KEY_LEN], snapshot_seq: u64) -> Result<(), WalError> {
        let ids = list_segment_ids(&self.root.join("wal"))?;
        if let Some(max_id) = ids.last().copied() {
            self.next_segment_id = max_id + 1;
        }
        let mut max_seq = snapshot_seq;
        let mut last_healthy = None;
        for id in ids {
            let path = segment_path(&self.root, id);
            let data = fs::read(&path).map_err(|err| WalError::io(&path, err))?;
            let mut offset = 0u64;
            let mut last_good_seq = None;
            let mut isolated = false;
            loop {
                match read_frame(&data, offset, key, self.limits.max_frame_payload)? {
                    FrameRead::End => break,
                    FrameRead::IncompleteTail { good_len } => {
                        if good_len < data.len() as u64 {
                            let file = OpenOptions::new()
                                .write(true)
                                .open(&path)
                                .map_err(|err| WalError::io(&path, err))?;
                            file.set_len(good_len)
                                .map_err(|err| WalError::io(&path, err))?;
                            file.sync_all().map_err(|err| WalError::io(&path, err))?;
                        }
                        break;
                    }
                    FrameRead::Isolated {
                        good_len,
                        at_offset,
                    } => {
                        if !self.isolated.iter().any(|item| item.segment_id == id) {
                            self.isolated.push(IsolatedSegment {
                                segment_id: id,
                                at_offset,
                                last_good_sequence: last_good_seq,
                            });
                        }
                        isolated = true;
                        let _ = good_len;
                        break;
                    }
                    FrameRead::Frame(frame) => {
                        self.apply_decoded(
                            &frame.plaintext,
                            frame.frame_type,
                            frame.sequence,
                            snapshot_seq,
                        )?;
                        last_good_seq = Some(frame.sequence);
                        max_seq = max_seq.max(frame.sequence);
                        offset += frame.frame_len as u64;
                    }
                }
            }
            if isolated {
                continue;
            }
            last_healthy = Some(id);
        }
        self.next_sequence = max_seq + 1;
        self.reopen_segment = last_healthy;
        Ok(())
    }

    fn apply_decoded(
        &mut self,
        plaintext: &[u8],
        frame_type: FrameType,
        sequence: u64,
        snapshot_seq: u64,
    ) -> Result<(), WalError> {
        match frame_type {
            FrameType::Txn => {
                let txn = Transaction::from(
                    decode_cbor::<PersistedTransaction>(plaintext).map_err(WalError::Payload)?,
                );
                let apply_checkpoint = sequence > snapshot_seq
                    || !self.checkpoints.contains_key(&txn.next_checkpoint.key());
                self.apply_txn(txn, apply_checkpoint);
            }
            FrameType::Ack => {
                let ack: AckPayload = decode_cbor(plaintext).map_err(WalError::Payload)?;
                self.apply_ack(&ack);
            }
            FrameType::DeadLetter => {
                let payload: DeadLetterPayload =
                    decode_cbor(plaintext).map_err(WalError::Payload)?;
                self.apply_dead_letter(&payload);
            }
            FrameType::Settings => {
                let _settings: SettingsPayload =
                    decode_cbor(plaintext).map_err(WalError::Payload)?;
            }
        }
        Ok(())
    }

    fn load_snapshot(&mut self, key: &[u8; DATA_KEY_LEN]) -> Result<u64, WalError> {
        let dir = self.root.join("snapshots");
        let mut files: Vec<PathBuf> = fs::read_dir(&dir)
            .map_err(|err| WalError::io(&dir, err))?
            .filter_map(|entry| entry.ok().map(|item| item.path()))
            .filter(|path| {
                path.extension()
                    .and_then(|ext| ext.to_str())
                    .is_some_and(|ext| ext == "cbor")
            })
            .collect();
        files.sort();
        let Some(path) = files.last() else {
            return Ok(0);
        };
        match read_snapshot(path, key) {
            Ok(snap) => {
                self.checkpoints = snap.checkpoints;
                self.acked = snap.acked_event_ids;
                self.dead_letters = snap.dead_letter_ids;
                self.isolated = snap.isolated_segments;
                Ok(snap.last_sequence)
            }
            Err(WalError::SnapshotCorrupt) => Ok(0),
            Err(err) => Err(err),
        }
    }

    fn maybe_snapshot(&mut self) -> Result<(), WalError> {
        if self.frames_since_snapshot >= self.limits.snapshot_frames
            || self.last_snapshot.elapsed() >= self.limits.snapshot_interval
        {
            self.snapshot()?;
        }
        Ok(())
    }

    fn guard_key(&self) -> Result<[u8; DATA_KEY_LEN], WalError> {
        load_key(&self.keys)
    }
}

fn validate_transaction_checkpoint(txn: &Transaction) -> Result<(), WalError> {
    if txn.source_id != txn.next_checkpoint.source_id {
        return Err(WalError::Payload(
            "transaction and next checkpoint source_id mismatch".into(),
        ));
    }
    if let Some(previous) = &txn.previous_checkpoint {
        if previous.source_id != txn.source_id
            || previous.file_identity != txn.next_checkpoint.file_identity
            || previous.generation > txn.next_checkpoint.generation
            || (previous.generation == txn.next_checkpoint.generation
                && previous.offset > txn.next_checkpoint.offset)
        {
            return Err(WalError::Payload(
                "previous and next checkpoint are inconsistent".into(),
            ));
        }
    }
    Ok(())
}

fn is_safe_diagnostic_code(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b':' | b'-'))
}

#[derive(Clone)]
struct KeptFrame {
    sequence: u64,
    frame_type: FrameType,
    plaintext: Vec<u8>,
}

fn frames_to_keep(frames: &[KeptFrame], acked: &BTreeSet<String>) -> Vec<KeptFrame> {
    frames
        .iter()
        .filter(|frame| match frame.frame_type {
            FrameType::Txn => {
                let Ok(txn) = decode_cbor::<PersistedTransaction>(&frame.plaintext) else {
                    return false;
                };
                txn.normalized_events
                    .iter()
                    .any(|event| !acked.contains(&event.event_id))
                    || txn.normalized_events.is_empty()
            }
            FrameType::Ack => {
                let Ok(ack) = decode_cbor::<AckPayload>(&frame.plaintext) else {
                    return false;
                };
                ack.acked_event_ids
                    .iter()
                    .any(|event_id| !acked.contains(event_id))
            }
            FrameType::DeadLetter | FrameType::Settings => true,
        })
        .cloned()
        .collect()
}

fn decode_segment(
    data: &[u8],
    key: &[u8; DATA_KEY_LEN],
    max_payload: u32,
) -> Result<Vec<KeptFrame>, WalError> {
    let mut offset = 0u64;
    let mut frames = Vec::new();
    while let FrameRead::Frame(frame) = read_frame(data, offset, key, max_payload)? {
        offset += frame.frame_len as u64;
        frames.push(KeptFrame {
            sequence: frame.sequence,
            frame_type: frame.frame_type,
            plaintext: frame.plaintext,
        });
    }
    Ok(frames)
}

fn read_snapshot(path: &Path, key: &[u8; DATA_KEY_LEN]) -> Result<Snapshot, WalError> {
    let data = fs::read(path).map_err(|err| WalError::io(path, err))?;
    if data.len() < 4 + 32 + 4 || &data[..4] != SNAPSHOT_MAGIC {
        return Err(WalError::SnapshotCorrupt);
    }
    let hash = &data[4..36];
    let len = u32::from_le_bytes(data[36..40].try_into().expect("len")) as usize;
    if data.len() < 40 + len {
        return Err(WalError::SnapshotCorrupt);
    }
    let encrypted = &data[40..40 + len];
    if Sha256::digest(encrypted).as_slice() != hash {
        return Err(WalError::SnapshotCorrupt);
    }
    let plaintext =
        decrypt(key, SNAPSHOT_MAGIC, encrypted).map_err(|_| WalError::SnapshotCorrupt)?;
    decode_cbor(&plaintext).map_err(|_| WalError::SnapshotCorrupt)
}

fn load_key(keys: &Arc<dyn KeyProvider>) -> Result<[u8; DATA_KEY_LEN], WalError> {
    keys.data_key().map_err(|err| WalError::KeyUnavailable {
        reason: err.to_string(),
    })
}

fn list_segment_ids(dir: &Path) -> Result<Vec<u64>, WalError> {
    let mut ids = Vec::new();
    let entries = fs::read_dir(dir).map_err(|err| WalError::io(dir, err))?;
    for entry in entries {
        let entry = entry.map_err(|err| WalError::io(dir, err))?;
        let name = entry.file_name();
        let Some(name) = name.to_str() else {
            continue;
        };
        if let Some(stem) = name.strip_suffix(".wal") {
            if let Ok(id) = stem.parse::<u64>() {
                ids.push(id);
            }
        }
    }
    ids.sort_unstable();
    Ok(ids)
}

fn segment_path(root: &Path, id: u64) -> PathBuf {
    root.join("wal").join(format!("{id:016}.wal"))
}

fn drop_writer_if_matches(writer: &mut Option<SegmentWriter>, id: u64) {
    if writer.as_ref().is_some_and(|item| item.id == id) {
        *writer = None;
    }
}

/// Scan concatenated bytes for a UTF-8 needle. Used by canary tests.
pub fn contains_bytes(haystack: &[u8], needle: &str) -> bool {
    memchr_window(haystack, needle.as_bytes())
}

fn memchr_window(haystack: &[u8], needle: &[u8]) -> bool {
    if needle.is_empty() || haystack.len() < needle.len() {
        return false;
    }
    haystack
        .windows(needle.len())
        .any(|window| window == needle)
}

pub fn wal_files_contain_magic(root: &Path) -> bool {
    let Ok(ids) = list_segment_ids(&root.join("wal")) else {
        return false;
    };
    ids.iter().any(|id| {
        fs::read(segment_path(root, *id))
            .ok()
            .is_some_and(|data| data.windows(4).any(|window| window == MAGIC))
    })
}
