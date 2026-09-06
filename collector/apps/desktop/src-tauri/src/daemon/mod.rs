use crate::state::AppState;
use collector_service::{collect_tick, runtime};
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
        let price_state=state.clone();
        let price_running=Arc::clone(&is_running);
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
            while is_running.load(Ordering::Acquire) {
                interval.tick().await;
                if state.get_daemon_status().await.global_paused {
                    continue;
                }
                let snapshot = Arc::clone(&state.detection);
                let mut service = state.service.lock().await;
                let report = collect_tick(&mut service, &snapshot, historical).await;
                historical = false;
                let pending = service.wal.unacked_count();
                let observations = service.wal.take_observations();
                let envelopes = service.wal.unacked_events();
                drop(service);
                state.record_usage(&envelopes);
                state.record_pricing_observations(&observations);
                runtime::append_log(
                    &state.control_dir_path(),
                    &format!(
                        "tick files={} events={} pending={} errors={}",
                        report.files_scanned,
                        report.accepted_events,
                        pending,
                        report.errors.len()
                    ),
                );
            }
        });
    }

    pub fn stop(&self) {
        self.is_running.store(false, Ordering::Release);
    }
}
