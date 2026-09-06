pub mod auto_sync;
pub mod autostart;
pub mod commands;
pub mod daemon;
pub mod local_store;
pub mod pricing;
pub mod rebuild;
pub mod state;
pub mod updates;
pub mod usage_ledger;

use std::fs;
use std::panic;

use daemon::CollectorDaemon;
use state::AppState;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Manager, WindowEvent};

fn crash_log_path() -> std::path::PathBuf {
    state::app_data_root().join("crash.log")
}

fn write_crash_log(message: &str) {
    let path = crash_log_path();
    if let Some(parent) = path.parent() {
        let _ = fs::create_dir_all(parent);
    }
    let _ = fs::write(&path, message);
    eprintln!("{message}\ncrash log: {}", path.display());
}

fn install_panic_hook() {
    panic::set_hook(Box::new(|info| {
        let backtrace = std::backtrace::Backtrace::force_capture();
        write_crash_log(&format!("panic: {info}\n{backtrace}"));
    }));
}

fn install_tray(app: &tauri::App) -> tauri::Result<()> {
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
    let quit_item = MenuItem::with_id(
        app,
        "quit",
        "退出程序 / Quit TokenDance",
        true,
        None::<&str>,
    )?;
    let tray_menu = Menu::with_items(app, &[&open_item, &toggle_pause_item, &quit_item])?;

    // tauri.conf.json already creates `main-tray`. Rebuilding the same id panics
    // the process during setup, which looks like an instant flash-exit.
    let tray = if let Some(existing) = app.tray_by_id("main-tray") {
        existing
    } else {
        let mut builder = TrayIconBuilder::with_id("main-tray")
            .tooltip("TokenDance Collector 运行中")
            .menu(&tray_menu);
        if let Some(icon) = app.default_window_icon() {
            builder = builder.icon(icon.clone());
        }
        builder.build(app)?
    };
    tray.set_tooltip(Some("TokenDance Collector 运行中"))?;
    tray.set_menu(Some(tray_menu))?;
    tray.set_show_menu_on_left_click(false)?;
    tray.on_menu_event(|app, event| match event.id.as_ref() {
        "open_settings" => {
            let _ = commands::window::open_settings(app.clone());
        }
        "toggle_pause" => {
            let state = app.state::<AppState>().inner().clone();
            tauri::async_runtime::spawn(async move {
                let _ = state.toggle_global_pause().await;
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
    });
    tray.on_tray_icon_event(|tray, event| {
        if let TrayIconEvent::Click {
            button: MouseButton::Left,
            button_state: MouseButtonState::Up,
            position,
            ..
        } = event
        {
            if let Err(error) = commands::window::show_usage_panel(tray.app_handle(), position) {
                eprintln!("failed to show usage panel: {error}");
            }
        }
    });
    Ok(())
}

pub fn run() {
    install_panic_hook();
    if updates::apply_pending_before_start() {
        return;
    }

    let app_state = match tauri::async_runtime::block_on(AppState::production()) {
        Ok(state) => state,
        Err(error) => {
            write_crash_log(&format!(
                "failed to initialize service-backed desktop state: {error}"
            ));
            panic!("initialize service-backed desktop state: {error}");
        }
    };

    let builder = tauri::Builder::default()
        .manage(app_state)
        .manage(commands::account::AccountState::default())
        .manage(commands::window::WindowPresentation::default())
        .manage(std::sync::Arc::new(updates::UpdateState::default()))
        .invoke_handler(tauri::generate_handler![
            updates::get_update_status,
            updates::check_for_updates,
            updates::set_auto_update,
            updates::install_update,
            commands::daemon::get_daemon_status,
            commands::daemon::toggle_global_pause,
            commands::daemon::set_global_pause,
            commands::daemon::get_collector_metrics,
            commands::agents::get_agent_configs,
            commands::quotas::get_agent_quotas,
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
            commands::window::window_ready,
            commands::window::show_window,
            commands::window::quit_app,
            commands::window::open_settings,
            commands::window::open_website,
            commands::account::get_account_session,
            commands::account::login_account,
            commands::account::logout_account,
        ])
        .on_window_event(|window, event| {
            if matches!(event, WindowEvent::Focused(false)) && window.label() == "main" {
                // WebView2 can enqueue a blur while a hidden window is being
                // shown/focused. Recheck actual focus instead of hiding on that
                // obsolete notification (which produces a show-hide flash).
                let window = window.clone();
                tauri::async_runtime::spawn(async move {
                    tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                    if window.is_visible().unwrap_or(false) && !window.is_focused().unwrap_or(true)
                    {
                        window
                            .state::<commands::window::WindowPresentation>()
                            .cancel(window.label());
                        let _ = window.hide();
                    }
                });
            }
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                window
                    .state::<commands::window::WindowPresentation>()
                    .cancel(window.label());
                let _ = window.hide();
            }
        })
        .setup(|app| {
            // Keep the native WebView hidden until React has committed its
            // first data/error screen. Autostart never requests presentation.
            if !std::env::args().any(|arg| arg == "--minimized") {
                let _ = commands::window::request_initial_panel(app.handle());
            }
            let state = app.state::<AppState>().inner().clone();
            CollectorDaemon::new(state.clone()).start();
            commands::account::start_auto_sync(app.handle().clone(), state);
            updates::start(app.handle());
            if let Err(error) = install_tray(app) {
                write_crash_log(&format!("tray setup failed: {error}"));
            }
            Ok(())
        });

    if let Err(error) = builder.run(tauri::generate_context!()) {
        write_crash_log(&format!("run TokenDance desktop application: {error}"));
        panic!("run TokenDance desktop application: {error}");
    }
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

    pub(crate) async fn state() -> (tempfile::TempDir, AppState) {
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
        assert_eq!(
            status.total_adapters_count as usize,
            state.get_agents().await.len()
        );
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

    #[tokio::test]
    async fn sqlite_usage_survives_restart_without_json_or_upload_wal() {
        let root = tempfile::tempdir().unwrap();
        let mut event = crate::auto_sync::tests::event('B');
        event.agent_id = "codex".into();
        event.occurred_at = chrono::Local::now().to_rfc3339();
        {
            let state = AppState::test(root.path().to_path_buf(), Arc::new(MockAutostart::new()))
                .await
                .unwrap();
            assert!(state.record_usage(&[event.clone()]));
            let agents = state.get_agents().await;
            let codex = agents.iter().find(|agent| agent.id == "codex").unwrap();
            assert_eq!(codex.today_tokens, 15);
            assert_eq!(codex.total_tokens, 15);
            assert!(!root.path().join("usage-ledger.json").exists());
            assert!(root.path().join("tokendance.sqlite3").exists());
            assert!(state.get_outbox().await.is_empty());
        }
        let state = AppState::test(root.path().to_path_buf(), Arc::new(MockAutostart::new()))
            .await
            .unwrap();
        let agents = state.get_agents().await;
        let codex = agents.iter().find(|agent| agent.id == "codex").unwrap();
        assert_eq!(codex.total_tokens, 15);
        assert_eq!(codex.today_tokens, 15);
        assert!(state.get_outbox().await.is_empty());
        assert!(!state.record_usage(&[event]));
    }
}
