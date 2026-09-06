use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::time::Duration;

use wal_spool::{KeyProvider, OsKeyProvider, WalStore};

use crate::detect::{
    default_jsonl_limit, detect_local, detected_adapter_ids, grok_history_limit,
    list_grok_history_files, list_jsonl_files,
};
use crate::upload::UploadPipeline;
use crate::{adapter_id, DetectionSnapshot, ProductionService};

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
    let base = std::env::var_os("LOCALAPPDATA")
        .or_else(|| std::env::var_os("XDG_DATA_HOME"))
        .or_else(|| std::env::var_os("HOME"))
        .map(PathBuf::from)
        .unwrap_or_else(std::env::temp_dir);
    base.join("TokenDance").join("collector")
}

pub fn append_log(root: &Path, message: &str) {
    let path = root.join("daemon.log");
    let _ = fs::create_dir_all(root);
    if let Ok(mut file) = OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(file, "{message}");
    }
}

pub fn acquire_instance_lock(root: &Path) -> Result<File, String> {
    fs::create_dir_all(root).map_err(|error| error.to_string())?;
    let path = root.join("collector.lock");
    if path.exists() {
        if let Ok(existing) = fs::read_to_string(&path) {
            let pid = existing.trim();
            if !pid.is_empty() && process_is_running(pid) {
                return Err(format!("collector already running as pid {pid}"));
            }
        }
    }
    let mut file = File::create(&path).map_err(|error| error.to_string())?;
    write!(file, "{}", std::process::id()).map_err(|error| error.to_string())?;
    Ok(file)
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
                ext.eq_ignore_ascii_case("sqlite") || ext.eq_ignore_ascii_case("vscdb")
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
    report
}

pub async fn assemble_local_service(
    root: &Path,
) -> Result<(DetectionSnapshot, ProductionService), String> {
    fs::create_dir_all(root).map_err(|error| error.to_string())?;
    let snapshot = detect_local();
    let key_provider: std::sync::Arc<dyn KeyProvider> = std::sync::Arc::new(OsKeyProvider::new(
        "io.tokendance.desktop",
        "collector-wal-key",
    ));
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
    fs::write(&path, &id).map_err(|error| error.to_string())?;
    Ok(id)
}

fn process_is_running(pid: &str) -> bool {
    let Ok(pid) = pid.parse::<u32>() else {
        return false;
    };
    if pid == std::process::id() {
        return false;
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;

        let output = std::process::Command::new("tasklist")
            .creation_flags(0x08000000) // CREATE_NO_WINDOW for GUI callers
            .args(["/FI", &format!("PID eq {pid}"), "/NH"])
            .output();
        return output
            .map(|output| String::from_utf8_lossy(&output.stdout).contains(&pid.to_string()))
            .unwrap_or(false);
    }
    #[cfg(not(windows))]
    {
        Path::new("/proc").join(pid.to_string()).exists()
    }
}
