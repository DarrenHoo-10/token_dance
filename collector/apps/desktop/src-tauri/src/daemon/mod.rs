use crate::rebuild;
use crate::state::AppState;
use collector_service::runtime;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

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
            while is_running.load(Ordering::Acquire) {
                interval.tick().await;
                let maintenance = state
                    .lock_store()
                    .prune_details(chrono::Utc::now().date_naive());
                if let Err(error) = maintenance {
                    state.set_storage_error(&error);
                    continue;
                }
                if state.get_daemon_status().await.global_paused {
                    continue;
                }
                let snapshot = Arc::clone(&state.detection);
                let prepared = {
                    let mut store = state.lock_store();
                    rebuild::prepare_tick(&mut store, &snapshot)
                };
                let prepared = match prepared {
                    Ok(prepared) => prepared,
                    Err(error) => {
                        state.set_storage_error(&error);
                        runtime::append_log(
                            &state.control_dir_path(),
                            &format!("存储异常: {error}"),
                        );
                        continue;
                    }
                };
                let mut service = state.service.lock().await;
                let decoded = rebuild::decode_tick(&mut service, prepared).await;
                drop(service);
                let report = {
                    let mut store = state.lock_store();
                    rebuild::commit_tick(&mut store, &decoded)
                };
                match report {
                    Ok(report) => {
                        state.clear_storage_error();
                        state.set_rebuilding(report.rebuilding);
                        runtime::append_log(
                            &state.control_dir_path(),
                            &format!(
                                "tick files={} events={} pending={} errors={} rebuild={}",
                                report.files_scanned,
                                report.accepted_events,
                                state.pending_sync_count(),
                                report.errors.len(),
                                report.rebuilding
                            ),
                        );
                    }
                    Err(error) => {
                        state.set_storage_error(&error);
                        runtime::append_log(
                            &state.control_dir_path(),
                            &format!("存储异常: {error}"),
                        );
                    }
                }
            }
        });
    }

    pub fn stop(&self) {
        self.is_running.store(false, Ordering::Release);
    }
}
