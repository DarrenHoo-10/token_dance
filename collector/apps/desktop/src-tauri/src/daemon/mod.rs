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
        let is_running = self.is_running.clone();
        let state = self.state.clone();

        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            while is_running.load(Ordering::SeqCst) {
                interval.tick().await;

                let is_paused = {
                    let st = state.inner.read().await;
                    st.global_paused
                };

                if !is_paused {
                    let mut st = state.inner.write().await;
                    // Increment collected counter for active agents
                    let active_count = st.agents.iter().filter(|a| a.enabled).count() as u64;
                    if active_count > 0 {
                        st.events_collected_counter += active_count;
                    }
                }
            }
        });
    }

    pub fn stop(&self) {
        self.is_running.store(false, Ordering::SeqCst);
    }
}
