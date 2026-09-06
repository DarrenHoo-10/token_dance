use collector_service::{
    enumerate_all_source_files, DecodedSourceBatch, DetectionSnapshot, EnumeratedSourceKind,
    ProductionService,
};

use crate::local_store::{DiscoveredSource, LocalStore, RebuildFileProgress, ScanWorkItem};

pub const ACTIVE_FILE_LIMIT: usize = 8;
pub const REBUILD_FILES_PER_TICK: usize = 4;
pub const FRAMES_PER_FILE: usize = 32;

#[derive(Debug, Default)]
pub struct CollectionTick {
    pub files_scanned: usize,
    pub accepted_events: usize,
    pub rebuilding: bool,
    pub errors: Vec<String>,
}

pub struct PreparedTick {
    pub work: Vec<ScanWorkItem>,
    pub rebuilding: bool,
}

pub struct DecodedTick {
    pub events: Vec<protocol::EventEnvelope>,
    pub checkpoints: Vec<wal_spool::SourceCheckpoint>,
    pub progress: Vec<RebuildFileProgress>,
    pub report: CollectionTick,
}

pub fn prepare_tick(
    store: &mut LocalStore,
    snapshot: &DetectionSnapshot,
) -> Result<PreparedTick, String> {
    let enumerated = enumerate_all_source_files(snapshot);
    let discovered = enumerated
        .iter()
        .map(|file| DiscoveredSource {
            adapter_id: file.adapter_id.to_string(),
            source_id: file.source_id.clone(),
            scan_root: file.scan_root.to_string_lossy().into_owned(),
            path: file.path.to_string_lossy().into_owned(),
            mtime_unix: file.mtime_unix as i64,
            file_len: std::fs::metadata(&file.path)
                .map(|meta| meta.len() as i64)
                .unwrap_or(0),
            kind: match file.kind {
                EnumeratedSourceKind::Sqlite => "sqlite".into(),
                EnumeratedSourceKind::GrokUpdates => "grok".into(),
                EnumeratedSourceKind::Jsonl => "jsonl".into(),
            },
        })
        .collect::<Vec<_>>();
    store.discover_sources(&discovered)?;
    Ok(PreparedTick {
        work: store.select_scan_work(ACTIVE_FILE_LIMIT, REBUILD_FILES_PER_TICK)?,
        rebuilding: store.rebuild_in_progress(),
    })
}

pub async fn decode_tick(service: &mut ProductionService, prepared: PreparedTick) -> DecodedTick {
    let mut report = CollectionTick {
        rebuilding: prepared.rebuilding,
        ..CollectionTick::default()
    };
    let mut events = Vec::new();
    let mut checkpoints = Vec::new();
    let mut progress = Vec::new();
    for item in prepared.work {
        report.files_scanned += 1;
        match decode_work(service, &item).await {
            Ok(batch) => {
                report.accepted_events += batch.accepted_events;
                let caught_up = batch.events.len() < FRAMES_PER_FILE;
                progress.push(RebuildFileProgress {
                    job_id: item.job_id,
                    file_path: item.path.to_string_lossy().into_owned(),
                    file_identity: batch.next.file_identity.clone(),
                    mtime_unix: item.mtime_unix,
                    file_len: batch.next.file_len as i64,
                    status: if !item.path.exists() {
                        "skipped".into()
                    } else if caught_up {
                        "caught_up".into()
                    } else {
                        "in_progress".into()
                    },
                });
                if batch.checkpoint_moved || !batch.events.is_empty() {
                    checkpoints.push(batch.next);
                }
                events.extend(batch.events);
            }
            Err(error) => report.errors.push(format!(
                "{}/{} {}: {error}",
                item.adapter_id,
                item.source_id,
                item.path.display()
            )),
        }
    }
    DecodedTick {
        events,
        checkpoints,
        progress,
        report,
    }
}

pub fn commit_tick(
    store: &mut LocalStore,
    decoded: &DecodedTick,
) -> Result<CollectionTick, String> {
    store.commit_scan_batch(&decoded.events, &decoded.checkpoints, &decoded.progress)?;
    let mut report = CollectionTick {
        files_scanned: decoded.report.files_scanned,
        accepted_events: decoded.report.accepted_events,
        rebuilding: store.rebuild_in_progress(),
        errors: decoded.report.errors.clone(),
    };
    report.rebuilding = store.rebuild_in_progress();
    Ok(report)
}

async fn decode_work(
    service: &mut ProductionService,
    item: &ScanWorkItem,
) -> Result<DecodedSourceBatch, String> {
    if !item.path.exists() {
        return Ok(DecodedSourceBatch {
            adapter_id: item.adapter_id.clone(),
            source_id: item.source_id.clone(),
            events: Vec::new(),
            accepted_events: 0,
            previous: item.checkpoint.clone(),
            next: item
                .checkpoint
                .clone()
                .unwrap_or_else(|| wal_spool::SourceCheckpoint {
                    source_id: item.source_id.clone(),
                    path_template_id: item.source_id.clone(),
                    file_identity: String::new(),
                    generation: 1,
                    file_len: 0,
                    offset: 0,
                    last_record_hash: None,
                    driver_checkpoint: None,
                    status: protocol::SourceCheckpointStatus::Discovered,
                }),
            checkpoint_moved: false,
        });
    }
    if item.kind == "sqlite" {
        service
            .decode_sqlite_from(&item.adapter_id, &item.source_id, item.checkpoint.as_ref())
            .await
            .map_err(|error| error.to_string())
    } else {
        service
            .decode_jsonl_file(
                &item.adapter_id,
                &item.source_id,
                &item.path,
                item.checkpoint.as_ref(),
                FRAMES_PER_FILE,
            )
            .await
            .map_err(|error| error.to_string())
    }
}
