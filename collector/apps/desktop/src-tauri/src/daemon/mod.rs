use crate::state::AppState;
use collector_service::{collect_decoded, runtime, DecodedSourceBatch};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use wal_spool::{AppendClass, Backpressure, Transaction};

pub struct CollectorDaemon {
    state: AppState,
    is_running: Arc<AtomicBool>,
}

impl CollectorDaemon {
    pub fn new(state: AppState) -> Self {
        Self {
            state,
            is_running: Arc::new(AtomicBool::new(true)),
        }
    }

    pub fn start(&self) {
        let is_running = Arc::clone(&self.is_running);
        let state = self.state.clone();
        let price_state = state.clone();
        let price_running = Arc::clone(&is_running);
        tauri::async_runtime::spawn(async move {
            while price_running.load(Ordering::Acquire) {
                price_state.refresh_local_prices().await;
                tokio::time::sleep(Duration::from_secs(300)).await;
            }
        });
        tauri::async_runtime::spawn(async move {
            state.backfill_local_prices().await;
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            let mut historical = true;
            let mut last_compaction = None;
            while is_running.load(Ordering::Acquire) {
                interval.tick().await;
                if state.get_daemon_status().await.global_paused {
                    continue;
                }
                let snapshot = Arc::clone(&state.detection);
                let mut service = state.service.lock().await;
                if service.wal.backpressure() != Backpressure::Normal
                    && last_compaction.is_none_or(|at: std::time::Instant| {
                        at.elapsed() >= Duration::from_secs(60)
                    })
                {
                    if service.wal.compact().is_err() {
                        runtime::append_log(
                            &state.control_dir_path(),
                            "storage maintenance failed; retrying later",
                        );
                    }
                    last_compaction = Some(std::time::Instant::now());
                }
                let scan_historical =
                    historical && service.wal.backpressure().allow_historical_scan();
                let outcome = collect_decoded(&mut service, &snapshot, scan_historical).await;
                let events: Vec<_> = outcome
                    .batches
                    .iter()
                    .flat_map(|batch| batch.events.iter().cloned())
                    .collect();
                let checkpoints: Vec<_> = outcome
                    .batches
                    .iter()
                    .map(|batch| batch.next.clone())
                    .collect();
                let local = state.commit_local(&events, &checkpoints);
                match local {
                    Ok(_) => {
                        let class = if scan_historical {
                            AppendClass::Historical
                        } else {
                            AppendClass::Realtime
                        };
                        for batch in outcome.batches {
                            if let Err(error) = enqueue_upload(&mut service, batch, class) {
                                runtime::append_log(
                                    &state.control_dir_path(),
                                    &format!("upload enqueue failed; retrying later: {error}"),
                                );
                            }
                        }
                    }
                    Err(error) => runtime::append_log(
                        &state.control_dir_path(),
                        &format!("存储异常: {error}"),
                    ),
                }
                let pending = service.wal.unacked_count();
                drop(service);
                historical = false;
                runtime::append_log(
                    &state.control_dir_path(),
                    &format!(
                        "tick files={} events={} pending={} errors={}",
                        outcome.report.files_scanned,
                        outcome.report.accepted_events,
                        pending,
                        outcome.report.errors.len()
                    ),
                );
            }
        });
    }

    pub fn stop(&self) {
        self.is_running.store(false, Ordering::Release);
    }
}

fn enqueue_upload(
    service: &mut collector_service::ProductionService,
    batch: DecodedSourceBatch,
    class: AppendClass,
) -> Result<usize, String> {
    if class == AppendClass::Historical && !service.wal.backpressure().allow_historical_scan() {
        return Ok(0);
    }
    if !batch.checkpoint_moved && batch.events.is_empty() {
        return Ok(0);
    }
    let accepted = batch.accepted_events;
    let checked = batch
        .events
        .into_iter()
        .filter_map(|event| privacy::PrivacyFilter.filter(event).ok())
        .collect();
    let txn = Transaction::new(
        String::new(),
        batch.source_id,
        batch.previous,
        batch.next,
        checked,
        String::new(),
    );
    let control = service
        .collector
        .control(&batch.adapter_id)
        .ok_or_else(|| "adapter_disabled".to_string())?;
    control
        .with_commit_lease(|| service.wal.append_txn(txn, class))
        .ok_or_else(|| "adapter_disabled".to_string())?
        .map_err(|error| error.to_string())?;
    Ok(accepted)
}
