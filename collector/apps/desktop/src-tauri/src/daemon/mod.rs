use crate::state::AppState;
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
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            while is_running.load(Ordering::Acquire) {
                interval.tick().await;
                // Reading the real service-backed status keeps the daemon task attached to
                // Collector lifetime without manufacturing counters or runtime health.
                let _ = state.get_daemon_status().await;
            }
        });
    }

    pub fn stop(&self) {
        self.is_running.store(false, Ordering::Release);
    }
}
