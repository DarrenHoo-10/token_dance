use crate::local_store::pipeline::event_pipeline_v2_client_enabled;
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
            while price_running.load(Ordering::Acquire) && !price_state.is_shutting_down() {
                price_state.refresh_local_prices().await;
                tokio::time::sleep(Duration::from_secs(300)).await;
            }
        });
        // Persistent worker tasks refill independently after every commit. Blocking
        // filesystem/SQLite-channel work runs on Tokio's blocking pool, never its async threads.
        for _ in 0..crate::local_store::pipeline::runner::DEFAULT_GLOBAL_ACQUISITION_CONCURRENCY {
            let worker_state = state.clone();
            let worker_running = Arc::clone(&is_running);
            tauri::async_runtime::spawn(async move {
                while worker_running.load(Ordering::Acquire) && !worker_state.is_shutting_down() {
                    if event_pipeline_v2_client_enabled() && !worker_state.collection_paused().await
                    {
                        if let Some(runtime) = worker_state.pipeline_runtime() {
                            let work = Arc::clone(&runtime);
                            if tauri::async_runtime::spawn_blocking(move || work.acquire_one())
                                .await
                                .unwrap_or(false)
                            {
                                continue;
                            }
                            // Notification on commit/discovery, timeout for retries and pause/stop.
                            let _ = tokio::time::timeout(
                                Duration::from_millis(500),
                                runtime.work_available.notified(),
                            )
                            .await;
                            continue;
                        }
                    }
                    tokio::time::sleep(Duration::from_millis(500)).await;
                }
            });
        }
        tauri::async_runtime::spawn(async move {
            state.backfill_local_prices().await;
            let mut interval = tokio::time::interval(Duration::from_secs(1));
            interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            let mut next_legacy = std::time::Instant::now();
            while is_running.load(Ordering::Acquire) && !state.is_shutting_down() {
                interval.tick().await;
                if state.is_shutting_down() {
                    break;
                }
                if state.collection_paused().await {
                    continue;
                }

                // P0–P8 pipeline path: discover → acquire → metrics. Stop legacy rebuild writes.
                if event_pipeline_v2_client_enabled() {
                    if let Some(runtime) = state.pipeline_runtime() {
                        let stats = match tauri::async_runtime::spawn_blocking(move || {
                            runtime.tick()
                        })
                        .await
                        {
                            Ok(stats) => stats,
                            Err(error) => {
                                state.set_storage_error(&error.to_string());
                                continue;
                            }
                        };
                        if stats.failures == 0 {
                            state.clear_storage_error();
                        }
                        state.set_rebuilding(stats.rebuilding);
                        runtime::append_log(
                            &state.control_dir_path(),
                            &format!(
                                "pipeline discover={} acquire={} metrics={} pending={} sources={} backlog={} failures={}",
                                stats.discovered,
                                stats.acquired,
                                stats.metrics,
                                state.pending_sync_count(),
                                stats.sources, stats.backlog, stats.failures,
                            ),
                        );
                    } else {
                        runtime::append_log(
                            &state.control_dir_path(),
                            "pipeline workers paused or store unavailable",
                        );
                    }
                    continue;
                }

                if std::time::Instant::now() < next_legacy {
                    continue;
                }
                next_legacy = std::time::Instant::now() + Duration::from_secs(5);
                let maintenance = state
                    .lock_store()
                    .prune_details(chrono::Utc::now().date_naive());
                if let Err(error) = maintenance {
                    state.set_storage_error(&error);
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
