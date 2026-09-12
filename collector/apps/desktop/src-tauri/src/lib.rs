pub mod auto_sync;
pub mod autostart;
pub mod commands;
pub mod daemon;
pub mod local_store;
pub mod local_test;
#[cfg(target_os = "macos")]
pub mod macos_tray;
pub mod orb;
pub mod pricing;
pub mod rebuild;
mod single_instance;
pub mod state;
pub mod tray_state;
pub mod updates;
pub mod upload_pipeline;
pub mod usage_ledger;

use std::fs;
use std::panic;

use daemon::CollectorDaemon;
use state::AppState;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Manager, WindowEvent};

fn crash_log_path() -> std::path::PathBuf {
    state::crash_log_path()
}

fn write_crash_log(message: &str) {
    let path = crash_log_path();
    append_crash_record(&path, message);
    use std::io::Write;
    let _ = writeln!(
        std::io::stderr(),
        "{message}\ncrash log: {}",
        path.display()
    );
}

fn append_crash_record(path: &std::path::Path, message: &str) {
    use std::io::Write;
    if let Some(parent) = path.parent() {
        let _ = fs::create_dir_all(parent);
    }
    // A panic crossing an Objective-C callback triggers a second panic. Keep
    // the original error, rather than replacing it with "cannot unwind".
    if fs::metadata(path).is_ok_and(|meta| meta.len() > 256 * 1024) {
        let _ = fs::rename(path, path.with_extension("previous.log"));
    }
    if let Ok(mut file) = fs::OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(file, "\n[{}]\n{message}", chrono::Utc::now().to_rfc3339());
    }
}

fn install_panic_hook() {
    panic::set_hook(Box::new(|info| {
        let backtrace = std::backtrace::Backtrace::force_capture();
        write_crash_log(&format!("panic: {info}\n{backtrace}"));
    }));
}

fn app_context() -> tauri::Context<tauri::Wry> {
    tauri::generate_context!()
}

fn recovery_context() -> tauri::Context<tauri::Wry> {
    let mut context = app_context();
    // Recovery has no AppState: do not construct normal windows or the tray.
    context.config_mut().app.windows.clear();
    context.config_mut().app.tray_icon = None;
    context
}

fn startup_error_smoke() -> bool {
    cfg!(debug_assertions)
        && local_test::enabled()
        && std::env::args().any(|arg| arg == "--smoke-startup-error")
}

fn show_startup_error(error: String) {
    // No AppState or credential IPC exists in this recovery shell. In particular,
    // opening it must not retry a locked Keychain or replace an existing key.
    use base64::Engine;
    let escaped = error
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;");
    let authorization = if platform_credentials::uses_login_keychain() && !local_test::enabled() {
        "<p>未公证版本使用登录钥匙串。仅在你点击下方按钮时请求授权；如希望重启后继续使用，可在系统窗口选择“始终允许”。升级后可能需要再次授权。</p><p><a href=\"tokendance-keychain://authorize\">授权钥匙串并重新启动</a></p>"
    } else {
        ""
    };
    let html = format!(
        r#"<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><title>TokenDance</title><style>body{{font:15px system-ui;padding:32px;color:#25322d;background:#f7f9f7}}h1{{font-size:22px}}pre{{white-space:pre-wrap;overflow-wrap:anywhere;padding:16px;background:#fff;border:1px solid #ddd}}</style><h1>TokenDance 暂时无法启动</h1><p>本机存储或钥匙串暂不可用。原有数据已保留，程序不会反复请求密码。</p><pre>{escaped}</pre><p>处理上述问题后，关闭此窗口并重新打开 TokenDance。若是钥匙串访问问题，请先在“钥匙串访问”中检查登录钥匙串及应用权限。</p>"#
    );
    let html = format!("{html}{authorization}");
    let url = format!(
        "data:text/html;base64,{}",
        base64::engine::general_purpose::STANDARD.encode(html)
    );
    let result = tauri::Builder::default()
        .on_page_load(|webview, payload| {
            if startup_error_smoke()
                && webview.label() == "startup-error"
                && matches!(payload.event(), tauri::webview::PageLoadEvent::Finished)
            {
                println!("TOKENDANCE_STARTUP_ERROR_READY");
            }
        })
        .setup(move |app| {
            #[cfg(target_os = "macos")]
            app.set_activation_policy(tauri::ActivationPolicy::Regular);
            let handle = app.handle().clone();
            let authorizing = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
            tauri::WebviewWindowBuilder::new(
                app,
                "startup-error",
                tauri::WebviewUrl::External(tauri::Url::parse(&url)?),
            )
            .on_navigation(move |url| {
                if url.scheme() != "tokendance-keychain" {
                    return url.scheme() == "data";
                }
                if platform_credentials::uses_login_keychain()
                    && url.host_str() == Some("authorize")
                    && !authorizing.swap(true, std::sync::atomic::Ordering::AcqRel)
                {
                    let handle = handle.clone();
                    let authorizing = authorizing.clone();
                    tauri::async_runtime::spawn(async move {
                        match commands::account::authorize_saved_credentials(None).await {
                            Ok(()) => handle.restart(),
                            Err(error) => {
                                write_crash_log(&format!(
                                    "explicit Keychain authorization failed: {error}"
                                ));
                                authorizing.store(false, std::sync::atomic::Ordering::Release);
                            }
                        }
                    });
                }
                false
            })
            .title("TokenDance · 启动问题")
            .inner_size(600.0, 500.0)
            .center()
            .build()?;
            for label in ["main", "settings"] {
                if let Some(window) = app.get_webview_window(label) {
                    let _ = window.close();
                }
            }
            if let Some(tray) = app.remove_tray_by_id("main-tray") {
                drop(tray);
            }
            Ok(())
        })
        .run(recovery_context());
    if let Err(error) = result {
        write_crash_log(&format!("startup error window failed: {error}"));
    }
}

fn install_tray(app: &tauri::App) -> tauri::Result<()> {
    let english = tray_state::saved_english();
    let labels = tray_state::menu_labels(
        english,
        app.state::<orb::controller::OrbHandle>()
            .preferences()
            .enabled,
        false,
    );
    let open_item = MenuItem::with_id(app, "open_settings", labels[0], true, None::<&str>)?;
    let toggle_orb_item = MenuItem::with_id(app, "toggle_orb", labels[1], true, None::<&str>)?;
    let orb_details_item = MenuItem::with_id(app, "orb_details", labels[2], true, None::<&str>)?;
    let toggle_pause_item = MenuItem::with_id(app, "toggle_pause", labels[3], true, None::<&str>)?;
    let quit_item = MenuItem::with_id(app, "quit", labels[4], true, None::<&str>)?;
    let tray_menu = Menu::with_items(
        app,
        &[
            &open_item,
            &toggle_orb_item,
            &orb_details_item,
            &toggle_pause_item,
            &quit_item,
        ],
    )?;

    // tauri.conf.json already creates `main-tray`. Rebuilding the same id panics
    // the process during setup, which looks like an instant flash-exit.
    let tray = if let Some(existing) = app.tray_by_id("main-tray") {
        existing
    } else {
        let mut builder = TrayIconBuilder::with_id("main-tray")
            .tooltip("TokenDance")
            .menu(&tray_menu);
        if let Some(icon) = app.default_window_icon() {
            builder = builder.icon(icon.clone());
        }
        builder.build(app)?
    };
    tray.set_tooltip(Some("TokenDance"))?;
    tray.set_menu(Some(tray_menu))?;
    tray.set_show_menu_on_left_click(false)?;
    app.manage(tray_state::TrayMenuState::new(
        english,
        [
            open_item,
            toggle_orb_item,
            orb_details_item,
            toggle_pause_item,
            quit_item,
        ],
    ));
    tray_state::start(app.handle().clone());
    tray.on_menu_event(|app, event| match event.id.as_ref() {
        "open_settings" => {
            let _ = commands::window::open_settings(app.clone());
        }
        "toggle_orb" => {
            let orb = app
                .state::<crate::orb::controller::OrbHandle>()
                .inner()
                .clone();
            tauri::async_runtime::spawn(async move {
                let _ = tokio::task::spawn_blocking(move || orb.toggle_enabled()).await;
            });
        }
        "orb_details" => {
            let orb = app
                .state::<crate::orb::controller::OrbHandle>()
                .inner()
                .clone();
            tauri::async_runtime::spawn(async move {
                let _ =
                    tokio::task::spawn_blocking(move || orb.action("orb", "open_details", None))
                        .await;
            });
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
    if std::env::args().any(|arg| arg == "--smoke-startup-error") {
        if !startup_error_smoke() {
            eprintln!("Recovery smoke testing requires a debug build and --local-test");
            std::process::exit(2);
        }
        show_startup_error("恢复页面测试：模拟钥匙串暂不可用，未读取生产凭据。".into());
        return;
    }
    let instance = match single_instance::InstanceGuard::acquire(
        !std::env::args().any(|arg| arg == "--minimized"),
    ) {
        Ok(Some(instance)) => instance,
        Ok(None) => return,
        Err(error) => {
            write_crash_log(&format!("failed to claim desktop instance: {error}"));
            return;
        }
    };
    if !local_test::enabled() && updates::apply_pending_before_start(|| instance.release()) {
        return;
    }

    let app_state = {
        match tauri::async_runtime::block_on(AppState::production()) {
            Ok(state) => state,
            Err(error) if error.contains("already running") => {
                eprintln!("TokenDance is already running");
                std::process::exit(0);
            }
            Err(error) => {
                write_crash_log(&format!(
                    "failed to initialize service-backed desktop state: {error}"
                ));
                show_startup_error(error);
                return;
            }
        }
    };

    let builder = tauri::Builder::default()
        .manage(instance)
        .manage(app_state)
        .manage(commands::account::AccountState::default())
        .manage(commands::window::WindowPresentation::default())
        .manage(std::sync::Arc::new(updates::UpdateState::default()))
        .invoke_handler(tauri::generate_handler![
            tray_state::set_tray_language,
            updates::get_update_status,
            updates::check_for_updates,
            updates::set_auto_update,
            updates::install_update,
            commands::daemon::get_daemon_status,
            commands::daemon::rebuild_local_data,
            commands::daemon::get_rebuild_status,
            commands::daemon::toggle_global_pause,
            commands::daemon::set_global_pause,
            commands::daemon::get_collector_metrics,
            commands::daemon::retry_runtime_init,
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
            commands::autostart::open_login_items_settings,
            commands::window::hide_window,
            commands::window::window_ready,
            commands::window::show_window,
            commands::window::quit_app,
            commands::window::open_settings,
            commands::window::open_website,
            commands::orb::get_orb_snapshot,
            commands::orb::get_orb_render_snapshot,
            commands::orb::get_orb_details,
            commands::orb::get_orb_preferences,
            commands::orb::patch_orb_preferences,
            commands::orb::orb_ready,
            commands::orb::orb_action,
            commands::orb::orb_begin_drag,
            commands::orb::orb_end_drag,
            commands::orb::orb_move,
            commands::orb::orb_fling,
            commands::account::get_account_session,
            commands::account::login_account,
            commands::account::logout_account,
        ])
        .on_page_load(|webview, payload| {
            if matches!(payload.event(), tauri::webview::PageLoadEvent::Finished) {
                commands::window::page_loaded(webview.app_handle(), webview.label());
            }
        })
        .on_menu_event(|app, event| match event.id().as_ref() {
            "orb_ctx_details" | "orb_ctx_pause" | "orb_ctx_settings" | "orb_ctx_hide"
            | "orb_ctx_fx_orbit" | "orb_ctx_fx_soft" | "orb_ctx_fx_off" => {
                let _ = app
                    .state::<crate::orb::controller::OrbHandle>()
                    .handle_context_menu(event.id().as_ref());
            }
            _ => {}
        })
        .on_window_event(|window, event| {
            if commands::orb::is_orb_window(window.label()) {
                if let WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    window
                        .state::<crate::orb::controller::OrbHandle>()
                        .on_close(window.label());
                }
                return;
            }
            if window.label() == "main" {
                if let WindowEvent::Focused(focused) = event {
                    let presentation = window.state::<commands::window::WindowPresentation>();
                    if *focused {
                        presentation.on_focus_change(window.label(), true);
                    } else if presentation.on_focus_change(window.label(), false) {
                        let window = window.clone();
                        tauri::async_runtime::spawn(async move {
                            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                            if window.is_visible().unwrap_or(false)
                                && !window.is_focused().unwrap_or(true)
                            {
                                window
                                    .state::<commands::window::WindowPresentation>()
                                    .mark_hidden(window.label());
                                let _ = window.hide();
                            }
                        });
                    }
                }
            }
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                window
                    .state::<commands::window::WindowPresentation>()
                    .mark_hidden(window.label());
                let _ = window.hide();
            }
        })
        .setup(|app| {
            #[cfg(target_os = "macos")]
            {
                let _ = app.set_activation_policy(tauri::ActivationPolicy::Accessory);
                if let Err(error) = install_macos_menu(app) {
                    eprintln!("macos menu setup failed: {error}");
                }
                if let Some(settings) = app.get_webview_window("settings") {
                    commands::window::apply_macos_settings_chrome(&settings);
                }
                if let Some(panel) = app.get_webview_window("main") {
                    commands::window::apply_macos_overlay_chrome(&panel);
                }
                if let Some(tray) = app.remove_tray_by_id("main-tray") {
                    drop(tray);
                }
                if let Err(error) = macos_tray::install(app.handle()) {
                    write_crash_log(&format!("native status item failed: {error}"));
                    if let Err(error) = install_tray(app) {
                        write_crash_log(&format!("tray setup failed: {error}"));
                    }
                }
            }
            // Keep the native WebView hidden until React has committed its
            // first layout, including loading state. Autostart never requests presentation.
            if !std::env::args().any(|arg| arg == "--minimized") {
                let _ = commands::window::request_initial_panel(app.handle());
            }
            let state = app.state::<AppState>().inner().clone();
            let orb = crate::orb::controller::OrbHandle::install(app.handle(), state.clone());
            app.manage(orb);
            CollectorDaemon::new(state.clone()).start();
            if !local_test::enabled() {
                commands::account::start_auto_sync(app.handle().clone(), state.clone());
                updates::start(app.handle());
            }
            #[cfg(not(target_os = "macos"))]
            if let Err(error) = install_tray(app) {
                write_crash_log(&format!("tray setup failed: {error}"));
            }
            single_instance::listen(app.handle());
            Ok(())
        });

    if let Err(error) = builder.run(app_context()) {
        write_crash_log(&format!("run TokenDance desktop application: {error}"));
        panic!("run TokenDance desktop application: {error}");
    }
}

#[cfg(target_os = "macos")]
fn install_macos_menu(app: &tauri::App) -> tauri::Result<()> {
    use tauri::menu::{Menu, MenuItem, PredefinedMenuItem, Submenu};

    let about = PredefinedMenuItem::about(app, None, None)?;
    let settings = MenuItem::with_id(app, "open_settings", "Settings…", true, Some("CmdOrCtrl+,"))?;
    let hide = PredefinedMenuItem::hide(app, None)?;
    let hide_others = PredefinedMenuItem::hide_others(app, None)?;
    let quit = PredefinedMenuItem::quit(app, Some("Quit TokenDance"))?;
    let app_menu = Submenu::with_items(
        app,
        "TokenDance",
        true,
        &[&about, &settings, &hide, &hide_others, &quit],
    )?;
    let edit = Submenu::with_items(
        app,
        "Edit",
        true,
        &[
            &PredefinedMenuItem::undo(app, None)?,
            &PredefinedMenuItem::redo(app, None)?,
            &PredefinedMenuItem::cut(app, None)?,
            &PredefinedMenuItem::copy(app, None)?,
            &PredefinedMenuItem::paste(app, None)?,
            &PredefinedMenuItem::select_all(app, None)?,
        ],
    )?;
    let menu = Menu::with_items(app, &[&app_menu, &edit])?;
    app.set_menu(menu)?;
    app.on_menu_event(|app, event| {
        if event.id().as_ref() == "open_settings" {
            let _ = commands::window::open_settings(app.clone());
        }
    });
    Ok(())
}

#[cfg(test)]
mod tests {
    #[test]
    fn recovery_context_does_not_create_normal_windows_or_tray() {
        let context = super::recovery_context();
        assert!(context.config().app.windows.is_empty());
        assert!(context.config().app.tray_icon.is_none());
    }

    #[test]
    fn crash_log_retains_the_first_error_before_a_secondary_panic() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("crash.log");
        super::append_crash_record(&path, "initial setup failure");
        super::append_crash_record(&path, "panic cannot unwind");
        let log = std::fs::read_to_string(path).unwrap();
        assert!(
            log.find("initial setup failure").unwrap() < log.find("panic cannot unwind").unwrap()
        );
    }

    #[test]
    fn crash_log_rotates_large_history_without_losing_it() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("crash.log");
        let history = "x".repeat(256 * 1024 + 1);
        std::fs::write(&path, &history).unwrap();
        super::append_crash_record(&path, "new failure");
        assert_eq!(
            std::fs::read_to_string(path.with_extension("previous.log")).unwrap(),
            history
        );
        assert!(std::fs::read_to_string(path)
            .unwrap()
            .contains("new failure"));
    }

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
                status: crate::state::AutostartStatus::from_enabled(self.0.load(Ordering::Acquire)),
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
    async fn first_launch_enables_autostart_by_default() {
        let (_root, state) = state().await;
        assert!(
            state.get_autostart_status().unwrap().enabled,
            "install / first launch must turn autostart on"
        );
    }

    #[tokio::test]
    async fn later_launch_does_not_override_disabled_autostart() {
        let root = tempfile::tempdir().unwrap();
        {
            let state = AppState::test(root.path().to_path_buf(), Arc::new(MockAutostart::new()))
                .await
                .unwrap();
            assert!(state.get_autostart_status().unwrap().enabled);
            assert!(!state.set_autostart(false).unwrap().enabled);
        }
        let state = AppState::test(root.path().to_path_buf(), Arc::new(MockAutostart::new()))
            .await
            .unwrap();
        assert!(
            !state.get_autostart_status().unwrap().enabled,
            "existing installs must keep a user-disabled autostart off"
        );
    }

    #[tokio::test]
    async fn first_launch_autostart_failure_does_not_block_startup() {
        struct FailEnable;
        impl AutostartProvider for FailEnable {
            fn is_enabled(&self) -> Result<bool, String> {
                Ok(false)
            }
            fn enable(&self) -> Result<AutostartInfo, String> {
                Err("denied".into())
            }
            fn disable(&self) -> Result<AutostartInfo, String> {
                Err("denied".into())
            }
            fn get_info(&self) -> Result<AutostartInfo, String> {
                Ok(AutostartInfo {
                    enabled: false,
                    status: crate::state::AutostartStatus::Disabled,
                    platform: "test".into(),
                    method: "memory".into(),
                    target_path: "test://autostart".into(),
                    details: "test provider".into(),
                })
            }
        }
        let root = tempfile::tempdir().unwrap();
        let state = AppState::test(root.path().to_path_buf(), Arc::new(FailEnable))
            .await
            .unwrap();
        assert!(!state.get_autostart_status().unwrap().enabled);
        assert_eq!(state.get_daemon_status().await.status, "RUNNING");
    }

    #[tokio::test]
    async fn legacy_sqlite_usage_survives_restart_without_leaking_into_pipeline_ui() {
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
            assert_eq!(codex.today_tokens, 0);
            assert_eq!(codex.total_tokens, 0);
            assert_eq!(
                state
                    .lock_store()
                    .agent_usage("codex", chrono::Local::now().date_naive())
                    .unwrap()
                    .total_tokens,
                15
            );
            assert!(!root.path().join("usage-ledger.json").exists());
            assert!(root.path().join("tokendance.sqlite3").exists());
            assert!(state.get_outbox().await.is_empty());
        }
        let state = AppState::test(root.path().to_path_buf(), Arc::new(MockAutostart::new()))
            .await
            .unwrap();
        let agents = state.get_agents().await;
        let codex = agents.iter().find(|agent| agent.id == "codex").unwrap();
        assert_eq!(codex.total_tokens, 0);
        assert_eq!(
            state
                .lock_store()
                .agent_usage("codex", chrono::Local::now().date_naive())
                .unwrap()
                .total_tokens,
            15
        );
        assert_eq!(codex.today_tokens, 0);
        let orb = state.get_usage_summary(chrono::Local::now().date_naive());
        assert_eq!(orb.today_tokens, None);
        assert_eq!(orb.known_source_count, 0);
        let sources = state.orb_today_sources();
        assert_eq!(
            sources
                .iter()
                .find(|source| source.agent_id == "codex")
                .unwrap()
                .today_tokens
                .as_deref(),
            None
        );
        assert!(state.get_outbox().await.is_empty());
        assert!(!state.record_usage(&[event]));
    }
}
