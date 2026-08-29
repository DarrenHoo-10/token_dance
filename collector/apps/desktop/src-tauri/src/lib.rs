pub mod autostart;
pub mod commands;
pub mod daemon;
pub mod state;

use autostart::SystemAutostartManager;
use commands::autostart::AutostartState;
use daemon::CollectorDaemon;
use state::AppState;
use std::sync::Arc;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Manager, WindowEvent};

pub fn run() {
    let app_state = AppState::new();
    let autostart_mgr = Arc::new(SystemAutostartManager::new("TokenDanceCollector"));
    let autostart_state = AutostartState {
        manager: autostart_mgr,
    };

    // Start background collection daemon (runs continuously in background)
    let daemon = CollectorDaemon::new(app_state.clone());
    daemon.start();

    let builder = tauri::Builder::default()
        .manage(app_state.clone())
        .manage(autostart_state)
        .invoke_handler(tauri::generate_handler![
            // Daemon & Metrics
            commands::daemon::get_daemon_status,
            commands::daemon::toggle_global_pause,
            commands::daemon::set_global_pause,
            commands::daemon::get_collector_metrics,
            // Agents Matrix
            commands::agents::get_agent_configs,
            commands::agents::toggle_agent,
            commands::agents::set_agent_status,
            // Upload & Preview
            commands::upload::preview_upload_batch,
            commands::upload::trigger_sync_now,
            commands::upload::get_pending_envelopes,
            // Configuration Snapshots & Rollback
            commands::config::create_config_backup,
            commands::config::restore_config_backup,
            commands::config::list_config_backups,
            // Devices & Keys
            commands::device::list_devices,
            commands::device::revoke_device,
            // Data Sovereignty & Deletion
            commands::deletion::request_data_deletion,
            commands::deletion::purge_local_cache,
            // Autostart
            commands::autostart::get_autostart_status,
            commands::autostart::set_autostart,
            // Window Lifecycle
            commands::window::hide_window,
            commands::window::show_window,
            commands::window::quit_app,
        ])
        .on_window_event(|window, event| {
            // Intercept window close: prevent exit, hide window to keep collector running in background
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .setup(|app| {
            // Build System Tray
            let open_item = MenuItem::with_id(app, "open_settings", "打开 TokenDance 设置 / Open Settings", true, None::<&str>)?;
            let toggle_pause_item = MenuItem::with_id(app, "toggle_pause", "暂停/恢复数据采集 / Pause Collection", true, None::<&str>)?;
            let sync_item = MenuItem::with_id(app, "sync_now", "立即同步数据 / Sync Now", true, None::<&str>)?;
            let quit_item = MenuItem::with_id(app, "quit", "退出程序 / Quit TokenDance", true, None::<&str>)?;
            let tray_menu = Menu::with_items(app, &[&open_item, &toggle_pause_item, &sync_item, &quit_item])?;

            let _tray = TrayIconBuilder::with_id("main-tray")
                .tooltip("TokenDance Collector 运行中 (后台静默采集)")
                .menu(&tray_menu)
                .on_menu_event(|app, event| {
                    match event.id.as_ref() {
                        "open_settings" => {
                            if let Some(window) = app.get_webview_window("main") {
                                let _ = window.show();
                                let _ = window.set_focus();
                            }
                        }
                        "toggle_pause" => {
                            let state = app.state::<AppState>().inner.clone();
                            tauri::async_runtime::spawn(async move {
                                let mut st = state.write().await;
                                st.global_paused = !st.global_paused;
                            });
                        }
                        "sync_now" => {
                            let state_wrapper = app.state::<AppState>().inner.clone();
                            tauri::async_runtime::spawn(async move {
                                let state = AppState { inner: state_wrapper };
                                let _ = state.trigger_sync_now().await;
                            });
                        }
                        "quit" => {
                            app.exit(0);
                        }
                        _ => {}
                    }
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let app = tray.app_handle();
                        if let Some(window) = app.get_webview_window("main") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                })
                .build(app)?;

            Ok(())
        });

    builder
        .run(tauri::generate_context!())
        .expect("error while running TokenDance desktop application");
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::autostart::AutostartProvider;

    #[tokio::test]
    async fn test_state_initialization() {
        let state = AppState::new();
        let status = state.get_daemon_status().await;
        assert_eq!(status.status, "RUNNING");
        assert!(!status.global_paused);
        assert_eq!(status.total_adapters_count, 6);
        assert_eq!(status.active_adapters_count, 4);
    }

    #[tokio::test]
    async fn test_global_pause_toggle() {
        let state = AppState::new();
        assert!(!state.get_daemon_status().await.global_paused);

        let paused = state.toggle_global_pause().await;
        assert!(paused);
        assert_eq!(state.get_daemon_status().await.status, "PAUSED");

        let resumed = state.toggle_global_pause().await;
        assert!(!resumed);
        assert_eq!(state.get_daemon_status().await.status, "RUNNING");
    }

    #[tokio::test]
    async fn test_agent_toggle_and_status() {
        let state = AppState::new();
        let codex = state.toggle_agent("codex").await.unwrap();
        assert!(!codex.enabled);
        assert_eq!(codex.status, "DISABLED");

        let codex_re_enabled = state.set_agent_status("codex", true).await.unwrap();
        assert!(codex_re_enabled.enabled);
        assert_eq!(codex_re_enabled.status, "ACTIVE");
    }

    #[tokio::test]
    async fn test_config_backup_and_restore() {
        let state = AppState::new();
        let backup = state.create_config_backup(Some("测试备份".into())).await;
        assert!(backup.snapshot.agent_toggles.get("codex").copied().unwrap());

        // Toggle codex off
        state.set_agent_status("codex", false).await.unwrap();
        let codex = state.get_agents().await.into_iter().find(|a| a.id == "codex").unwrap();
        assert!(!codex.enabled);

        // Restore backup
        let restored = state.restore_config_backup(&backup.id).await.unwrap();
        assert!(restored);
        let codex_restored = state.get_agents().await.into_iter().find(|a| a.id == "codex").unwrap();
        assert!(codex_restored.enabled);
    }

    #[tokio::test]
    async fn test_device_revocation() {
        let state = AppState::new();
        let devices = state.list_devices().await;
        assert_eq!(devices.len(), 2);
        assert_eq!(devices[0].status, "ACTIVE");

        let revoked = state.revoke_device(&devices[0].id).await.unwrap();
        assert!(revoked);

        let updated_devices = state.list_devices().await;
        assert_eq!(updated_devices[0].status, "REVOKED");
    }

    #[tokio::test]
    async fn test_data_deletion_request() {
        let state = AppState::new();
        let res = state.request_data_deletion().await;
        assert!(res.success);
        assert_eq!(res.status, "DELETION_PENDING");

        let status = state.get_daemon_status().await;
        assert_eq!(status.events_pending, 0);
        assert_eq!(status.events_collected, 0);
    }

    #[tokio::test]
    async fn test_upload_preview_and_sync() {
        let state = AppState::new();
        let preview = state.preview_upload_batch().await;
        assert!(preview.event_count >= 3);
        assert!(preview.redaction_applied);

        let sync_res = state.trigger_sync_now().await.unwrap();
        assert!(sync_res.get("accepted").unwrap().as_u64().unwrap() >= 3);
    }

    #[test]
    fn test_autostart_platform_abstraction() {
        let mgr = SystemAutostartManager::new("TokenDanceCollector");
        let info = mgr.get_info();
        assert!(info.enabled);
        assert!(!info.platform.is_empty());
        assert!(!info.target_path.is_empty());

        let disabled = mgr.disable().unwrap();
        assert!(!disabled.enabled);

        let enabled = mgr.enable().unwrap();
        assert!(enabled.enabled);
    }
}
