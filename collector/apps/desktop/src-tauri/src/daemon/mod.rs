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
        tauri::async_runtime::spawn(async move {
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
                runtime::append_log(
                    &state.control_dir_path(),
                    &format!(
                        "tick files={} events={} pending={} errors={}",
                        report.files_scanned,
                        report.accepted_events,
                        service.wal.unacked_count(),
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
