use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use wal_spool::{KeyError, KeyProvider, OsKeyProvider, WalStore};

use acquisition::DriverBatch;
use adapter_grok_build::{hook_auth_token, write_session_end_hook, HOOK_SOURCE_ID};

use crate::detect::{
    default_jsonl_limit, detect_local, detected_adapter_ids, grok_history_limit,
    list_grok_history_files, list_jsonl_files,
};
use crate::grok_hook::{grok_sessions_root, grok_user_home, start_listener, take_hook_frames};
use crate::platform::{self, AppPaths, InstanceLock};
use crate::upload::UploadPipeline;
use crate::{adapter_id, DecodedSourceBatch, DetectionSnapshot, ProductionService};

#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct CollectReport {
    pub files_scanned: usize,
    pub accepted_events: usize,
    pub errors: Vec<String>,
}

struct FileSecrets;
impl acquisition::SecretResolver for FileSecrets {
    fn resolve(&self, _secret_ref: &str) -> Result<Vec<u8>, String> {
        Err("secret_not_configured".into())
    }
}

pub fn collector_data_root() -> PathBuf {
    AppPaths::production()
        .map(|paths| paths.collector)
        .unwrap_or_else(|_| PathBuf::from("."))
}

pub fn append_log(root: &Path, message: &str) {
    let path = match AppPaths::production() {
        Ok(paths) if root == paths.collector || root == paths.logs => paths.log_file(),
        _ => root.join("daemon.log"),
    };
    platform::append_rotated_log(&path, message);
}

pub fn acquire_instance_lock(root: &Path) -> Result<InstanceLock, String> {
    let paths = AppPaths::for_root(root.to_path_buf());
    InstanceLock::acquire(&paths)
}

pub async fn collect_tick(
    service: &mut ProductionService,
    snapshot: &DetectionSnapshot,
    historical: bool,
) -> CollectReport {
    let mut report = CollectReport::default();
    for (agent, source_id, config) in snapshot.iter_sources() {
        let Some(path) = config.path.as_ref() else {
            continue;
        };
        let adapter = adapter_id(agent);
        if path
            .extension()
            .and_then(|ext| ext.to_str())
            .is_some_and(|ext| {
                ext.eq_ignore_ascii_case("sqlite")
                    || ext.eq_ignore_ascii_case("vscdb")
                    || ext.eq_ignore_ascii_case("db")
            })
        {
            match service.poll_sqlite(adapter, source_id).await {
                Ok(count) => report.accepted_events += count,
                Err(error) => report
                    .errors
                    .push(format!("{adapter}/{source_id}: {error}")),
            }
            continue;
        }
        let files = if source_id == adapter_grok_build::HISTORY_SOURCE_ID {
            list_grok_history_files(path, grok_history_limit())
        } else if historical && adapter == adapter_codex::ADAPTER_ID {
            list_jsonl_files(path, 128)
        } else {
            list_jsonl_files(path, default_jsonl_limit())
        };
        for file in files {
            report.files_scanned += 1;
            match service
                .ingest_jsonl_path(adapter, source_id, &file, historical)
                .await
            {
                Ok(count) => report.accepted_events += count,
                Err(error) => report
                    .errors
                    .push(format!("{adapter}/{source_id} {}: {error}", file.display())),
            }
        }
    }
    if let Some(sessions_root) = grok_sessions_root() {
        let frames = take_hook_frames(
            service.collector.installation_id(),
            &service.grok_hooks,
            &sessions_root,
        );
        for frame in frames {
            let batch = DriverBatch {
                frames: vec![frame],
                cursor: String::new(),
                driver_checkpoint: None,
            };
            match service
                .ingest_driver_batch(adapter_grok_build::ADAPTER_ID, HOOK_SOURCE_ID, batch, false)
                .await
            {
                Ok(count) => report.accepted_events += count,
                Err(error) => report.errors.push(format!("grok-hook: {error}")),
            }
        }
    }
    report
}

#[derive(Debug, Default)]
pub struct LocalCollectOutcome {
    pub report: CollectReport,
    pub batches: Vec<DecodedSourceBatch>,
}

/// Decode a bounded collect tick without writing the upload spool.
/// Desktop persists SQLite first, then enqueues upload separately.
pub async fn collect_decoded(
    service: &mut ProductionService,
    snapshot: &DetectionSnapshot,
    historical: bool,
) -> LocalCollectOutcome {
    let mut outcome = LocalCollectOutcome::default();
    for (agent, source_id, config) in snapshot.iter_sources() {
        let Some(path) = config.path.as_ref() else {
            continue;
        };
        let adapter = adapter_id(agent);
        if path
            .extension()
            .and_then(|ext| ext.to_str())
            .is_some_and(|ext| {
                ext.eq_ignore_ascii_case("sqlite")
                    || ext.eq_ignore_ascii_case("vscdb")
                    || ext.eq_ignore_ascii_case("db")
            })
        {
            match service.decode_sqlite(adapter, source_id).await {
                Ok(batch) => {
                    outcome.report.accepted_events += batch.accepted_events;
                    outcome.batches.push(batch);
                }
                Err(error) => outcome
                    .report
                    .errors
                    .push(format!("{adapter}/{source_id}: {error}")),
            }
            continue;
        }
        let files = if source_id == adapter_grok_build::HISTORY_SOURCE_ID {
            list_grok_history_files(path, grok_history_limit())
        } else if historical && adapter == adapter_codex::ADAPTER_ID {
            list_jsonl_files(path, 128)
        } else {
            list_jsonl_files(path, default_jsonl_limit())
        };
        for file in files {
            outcome.report.files_scanned += 1;
            match service
                .decode_jsonl_path(adapter, source_id, &file, historical)
                .await
            {
                Ok(batch) => {
                    outcome.report.accepted_events += batch.accepted_events;
                    outcome.batches.push(batch);
                }
                Err(error) => outcome
                    .report
                    .errors
                    .push(format!("{adapter}/{source_id} {}: {error}", file.display())),
            }
        }
    }
    outcome
}

pub fn wal_key_provider(root: &Path) -> Result<Arc<dyn KeyProvider>, String> {
    // WAL remains the transitional upload queue. Do not mint a replacement key
    // when encrypted spool frames already exist; the event-pipeline writer will
    // retire this key together with the spool.
    let spool = root.join("spool");
    let create = !wal_spool::spool_has_data(&spool);
    let provider = OsKeyProvider::wal_key(create);
    match provider.data_key() {
        Ok(_) => Ok(Arc::new(provider)),
        Err(KeyError::NotFound) => Err(
            "encrypted collector data exists but the OS keystore entry is missing; refusing to mint a replacement key".into(),
        ),
        Err(error) => Err(error.to_string()),
    }
}

pub async fn assemble_local_service(
    root: &Path,
) -> Result<(DetectionSnapshot, ProductionService), String> {
    platform::create_private_dir(root)?;
    let snapshot = detect_local();
    let key_provider = wal_key_provider(root)?;
    let key = key_provider.data_key().map_err(|error| error.to_string())?;
    let wal =
        WalStore::open(root.join("spool"), key_provider).map_err(|error| error.to_string())?;
    let installation_id = load_or_create_installation_id(root)?;
    let service = ProductionService::assemble(
        installation_id,
        &key,
        &snapshot,
        std::sync::Arc::new(FileSecrets),
        wal,
    )
    .await
    .map_err(|error| error.to_string())?;
    if let Some(home) = grok_user_home() {
        let _ = write_session_end_hook(&home, &key);
        let _ = start_listener(hook_auth_token(&key), service.grok_hooks.clone());
    }
    Ok((snapshot, service))
}

pub async fn run_headless() -> Result<(), String> {
    let root = collector_data_root();
    let _lock = acquire_instance_lock(&root)?;
    append_log(
        &root,
        &format!("collector start pid={}", std::process::id()),
    );
    let (snapshot, mut service) = assemble_local_service(&root).await?;
    append_log(
        &root,
        &format!(
            "detected adapters: {}",
            detected_adapter_ids(&snapshot).join(", ")
        ),
    );
    let mut upload = UploadPipeline::new(service.collector.installation_id().to_string())?;
    let mut historical = true;
    let mut interval = tokio::time::interval(Duration::from_secs(5));
    loop {
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {
                append_log(&root, "collector stopping on ctrl-c");
                break;
            }
            _ = interval.tick() => {
                let report = collect_tick(&mut service, &snapshot, historical).await;
                historical = false;
                append_log(
                    &root,
                    &format!(
                        "tick files={} events={} pending={} errors={}",
                        report.files_scanned,
                        report.accepted_events,
                        service.wal.unacked_count(),
                        report.errors.len()
                    ),
                );
                for error in report.errors.iter().take(8) {
                    append_log(&root, &format!("collect error: {error}"));
                }
                match upload.flush(&mut service.wal, &root).await {
                    Ok(flush) => append_log(
                        &root,
                        &format!(
                            "upload batches={} acked={} dead={} retries={} pending={}",
                            flush.batches,
                            flush.acked_events,
                            flush.dead_letters,
                            flush.retries,
                            service.wal.unacked_count()
                        ),
                    ),
                    Err(error) => append_log(&root, &format!("upload failed: {error}")),
                }
            }
        }
    }
    Ok(())
}

fn load_or_create_installation_id(root: &Path) -> Result<String, String> {
    let path = root.join("installation-id");
    if path.exists() {
        return fs::read_to_string(path)
            .map(|value| value.trim().to_string())
            .map_err(|error| error.to_string());
    }
    let id = wal_spool::new_prefixed_id("ins");
    platform::write_private_file(&path, &id)?;
    Ok(id)
}
