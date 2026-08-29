pub mod autostart;
pub mod commands;
pub mod daemon;
pub mod state;

use daemon::CollectorDaemon;
use state::AppState;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Manager, WindowEvent};

pub fn run() {
    let app_state = tauri::async_runtime::block_on(AppState::production())
        .expect("initialize service-backed desktop state");
    CollectorDaemon::new(app_state.clone()).start();

    let builder = tauri::Builder::default()
        .manage(app_state)
        .invoke_handler(tauri::generate_handler![
            commands::daemon::get_daemon_status,
            commands::daemon::toggle_global_pause,
            commands::daemon::set_global_pause,
            commands::daemon::get_collector_metrics,
            commands::agents::get_agent_configs,
            commands::agents::toggle_agent,
            commands::agents::set_agent_status,
            commands::upload::preview_upload_batch,
            commands::upload::trigger_sync_now,
            commands::upload::get_pending_envelopes,
            commands::config::create_config_backup,
            commands::config::restore_config_backup,
            commands::config::list_config_backups,
            commands::device::list_devices,
            commands::device::revoke_device,
            commands::deletion::request_data_deletion,
            commands::deletion::purge_local_cache,
            commands::autostart::get_autostart_status,
            commands::autostart::set_autostart,
            commands::window::hide_window,
            commands::window::show_window,
            commands::window::quit_app,
        ])
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .setup(|app| {
            let open_item = MenuItem::with_id(
                app,
                "open_settings",
                "打开 TokenDance 设置 / Open Settings",
                true,
                None::<&str>,
            )?;
            let toggle_pause_item = MenuItem::with_id(
                app,
                "toggle_pause",
                "暂停/恢复数据采集 / Pause Collection",
                true,
                None::<&str>,
            )?;
            let sync_item = MenuItem::with_id(
                app,
                "sync_now",
                "立即同步数据 / Sync Now",
                true,
                None::<&str>,
            )?;
            let quit_item = MenuItem::with_id(
                app,
                "quit",
                "退出程序 / Quit TokenDance",
                true,
                None::<&str>,
            )?;
            let tray_menu = Menu::with_items(
                app,
                &[&open_item, &toggle_pause_item, &sync_item, &quit_item],
            )?;

            TrayIconBuilder::with_id("main-tray")
                .tooltip("TokenDance Collector 运行中")
                .menu(&tray_menu)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "open_settings" => {
                        if let Some(window) = app.get_webview_window("main") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                    "toggle_pause" => {
                        let state = app.state::<AppState>().inner().clone();
                        tauri::async_runtime::spawn(async move {
                            let _ = state.toggle_global_pause().await;
                        });
                    }
                    "sync_now" => {
                        let state = app.state::<AppState>().inner().clone();
                        tauri::async_runtime::spawn(async move {
                            let _ = state.trigger_sync_now().await;
                        });
                    }
                    "quit" => {
                        let state = app.state::<AppState>().inner().clone();
                        let handle = app.clone();
                        tauri::async_runtime::spawn(async move {
                            let _ = state.shutdown().await;
                            handle.exit(0);
                        });
                    }
                    _ => {}
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
        .expect("run TokenDance desktop application");
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::Arc;

    use crate::autostart::AutostartProvider;
    use crate::state::{AppState, AutostartInfo};

    struct MockAutostart(AtomicBool);

    impl MockAutostart {
        fn new() -> Self {
            Self(AtomicBool::new(false))
        }

        fn info(&self) -> AutostartInfo {
            AutostartInfo {
                enabled: self.0.load(Ordering::Acquire),
                platform: "test".into(),
                method: "memory".into(),
                target_path: "test://autostart".into(),
                details: "test provider".into(),
            }
        }
    }

    impl AutostartProvider for MockAutostart {
        fn is_enabled(&self) -> Result<bool, String> {
            Ok(self.0.load(Ordering::Acquire))
        }
        fn enable(&self) -> Result<AutostartInfo, String> {
            self.0.store(true, Ordering::Release);
            Ok(self.info())
        }
        fn disable(&self) -> Result<AutostartInfo, String> {
            self.0.store(false, Ordering::Release);
            Ok(self.info())
        }
        fn get_info(&self) -> Result<AutostartInfo, String> {
            Ok(self.info())
        }
    }

    async fn state() -> (tempfile::TempDir, AppState) {
        let root = tempfile::tempdir().unwrap();
        let state = AppState::test(root.path().to_path_buf(), Arc::new(MockAutostart::new()))
            .await
            .unwrap();
        (root, state)
    }

    #[tokio::test]
    async fn service_state_and_pause_ack_are_authoritative() {
        let (_root, state) = state().await;
        let status = state.get_daemon_status().await;
        assert_eq!(status.status, "RUNNING");
        assert_eq!(status.total_adapters_count, 6);
        let paused = state.toggle_global_pause().await.unwrap();
        assert_eq!(paused.status, "ACKNOWLEDGED");
        assert!(paused.state.global_paused);
        assert!(state
            .get_agents()
            .await
            .iter()
            .all(|agent| agent.status == "PAUSED"));
    }

    #[tokio::test]
    async fn agent_backup_device_and_delete_commands_return_readback() {
        let (_root, state) = state().await;
        let codex = state.toggle_agent("codex").await.unwrap();
        assert!(!codex.state.enabled);
        let backup = state
            .create_config_backup(Some("baseline".into()))
            .await
            .unwrap();
        state.set_agent_status("codex", true).await.unwrap();
        let restored = state.restore_config_backup(&backup.id).await.unwrap();
        assert!(!restored.state.agent_toggles["codex"]);

        let device = state.list_devices().await.remove(0);
        let revoke = state.revoke_device(&device.id).await.unwrap();
        assert_eq!(revoke.status, "PENDING");
        assert_eq!(revoke.state.status, "REVOCATION_PENDING");

        let deletion = state.request_data_deletion().await.unwrap();
        assert_eq!(deletion.status, "PENDING");
        assert_eq!(deletion.state.status, "DELETION_PENDING");
    }

    #[tokio::test]
    async fn preview_autostart_and_shutdown_use_real_state() {
        let (_root, state) = state().await;
        let preview = state.preview_upload_batch().await;
        assert!(preview.state.redaction_applied);
        assert_eq!(preview.state.event_count, state.get_outbox().await.len());
        assert!(state.set_autostart(true).unwrap().enabled);
        assert!(state.get_autostart_status().unwrap().enabled);
        state.shutdown().await.unwrap();
    }
}
