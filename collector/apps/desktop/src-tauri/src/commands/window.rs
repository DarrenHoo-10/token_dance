use crate::state::AppState;
use tauri::{AppHandle, Manager, PhysicalPosition, PhysicalSize, State, WebviewWindow};
use std::{collections::HashMap, sync::Mutex};

#[derive(Clone, Copy)]
enum OpenRequest { Panel(PhysicalPosition<f64>), Settings }

#[derive(Default)]
struct Presentation {
    ready: bool,
    pending: Option<OpenRequest>,
    /// Hide-on-blur only after the window has actually held focus. Startup
    /// `show()` often fails `SetForegroundWindow`, and WebView2 then emits a
    /// stale `Focused(false)` that would otherwise hide the panel immediately.
    held_focus: bool,
}

#[derive(Default)]
pub struct WindowPresentation(Mutex<HashMap<String, Presentation>>);

impl WindowPresentation {
    fn request(&self, label: &str, request: OpenRequest) -> bool {
        let mut entries = self.0.lock().expect("window presentation lock");
        let entry = entries.entry(label.into()).or_default();
        if entry.ready { true } else { entry.pending = Some(request); false }
    }
    fn ready(&self, label: &str) -> Option<OpenRequest> {
        let mut entries = self.0.lock().expect("window presentation lock");
        let entry = entries.entry(label.into()).or_default();
        entry.ready = true;
        entry.pending.take()
    }
    pub(crate) fn has_pending(&self, label: &str) -> bool {
        self.0.lock().expect("window presentation lock")
            .get(label).is_some_and(|entry| entry.pending.is_some())
    }
    pub fn cancel(&self, label: &str) {
        if let Some(entry) = self.0.lock().expect("window presentation lock").get_mut(label) { entry.pending = None; }
    }
    pub fn mark_hidden(&self, label: &str) {
        if let Some(entry) = self.0.lock().expect("window presentation lock").get_mut(label) {
            entry.pending = None;
            entry.held_focus = false;
        }
    }
    /// Returns true when a delayed hide-on-blur should be scheduled.
    pub fn on_focus_change(&self, label: &str, focused: bool) -> bool {
        let mut entries = self.0.lock().expect("window presentation lock");
        let entry = entries.entry(label.into()).or_default();
        if focused {
            entry.held_focus = true;
            false
        } else {
            entry.held_focus
        }
    }
}

fn window_blocks_orb(pending: bool, visible: bool, minimized: bool) -> bool {
    pending || (visible && !minimized)
}

/// UI-thread only. Pending startup presentation also suppresses the orb,
/// avoiding a flash while the primary WebView is loading.
pub(crate) fn primary_ui_visible(app: &AppHandle) -> bool {
    ["main", "settings"].iter().any(|label| {
        let pending = app.state::<WindowPresentation>().has_pending(label);
        let Some(window) = app.get_webview_window(label) else { return pending };
        window_blocks_orb(pending, window.is_visible().unwrap_or(true), window.is_minimized().unwrap_or(false))
    })
}

fn suppress_orb(app: &AppHandle) {
    if let Some(orb) = app.try_state::<crate::orb::controller::OrbHandle>() {
        orb.main_opened();
    }
}

fn present(window: &WebviewWindow) -> tauri::Result<()> {
    suppress_orb(window.app_handle());
    if window.is_minimized()? { window.unminimize()?; }
    if !window.is_visible()? { window.show()?; }
    if !window.is_focused()? { window.set_focus()?; }
    Ok(())
}

pub fn request_initial_panel(app: &AppHandle) -> tauri::Result<()> {
    let point = app.get_webview_window("main")
        .and_then(|window| window.current_monitor().ok().flatten())
        .map(|monitor| PhysicalPosition::new((monitor.position().x + 1) as f64, (monitor.position().y + 1) as f64))
        .unwrap_or(PhysicalPosition::new(0.0, 0.0));
    show_usage_panel(app, point)
}

fn complete_pending_presentation(app: &AppHandle, label: &str) -> Result<(), String> {
    let pending = app.state::<WindowPresentation>().ready(label);
    match pending {
        Some(OpenRequest::Panel(point)) => show_usage_panel(app, point).map_err(|e| e.to_string()),
        Some(OpenRequest::Settings) => open_settings(app.clone()),
        None => Ok(()),
    }
}

#[tauri::command]
pub fn window_ready(app: AppHandle, window: WebviewWindow) -> Result<(), String> {
    complete_pending_presentation(&app, window.label())
}

/// Hidden WebViews can delay JavaScript/paint callbacks. Native navigation
/// completion must also release an explicit show request. A cancelled request
/// (minimize/close, or --minimized startup) is never recreated by this fallback.
pub(crate) fn page_loaded(app: &AppHandle, label: &str) {
    if !matches!(label, "main" | "settings") { return; }
    let label = label.to_owned();
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(std::time::Duration::from_millis(250)).await;
        let callback_app = app.clone();
        let _ = app.run_on_main_thread(move || {
            if let Err(error) = complete_pending_presentation(&callback_app, &label) {
                eprintln!("primary window presentation failed: {error}");
            }
        });
    });
}

fn panel_position(
    origin: PhysicalPosition<i32>,
    area: PhysicalSize<u32>,
    size: PhysicalSize<u32>,
    gap: i32,
) -> PhysicalPosition<i32> {
    PhysicalPosition::new(
        origin.x + (area.width as i32 - size.width as i32 - gap).max(0),
        origin.y + (area.height as i32 - size.height as i32 - gap).max(0),
    )
}

pub fn show_usage_panel(app: &AppHandle, point: PhysicalPosition<f64>) -> tauri::Result<()> {
    suppress_orb(app);
    if !app.state::<WindowPresentation>().request("main", OpenRequest::Panel(point)) { return Ok(()); }
    if let Some(window) = app.get_webview_window("main") {
        if let Some(monitor) = app.monitor_from_point(point.x, point.y)? {
            let area = monitor.work_area();
            let scale = monitor.scale_factor();
            let gap = (12.0 * scale).round() as i32;
            // Compute in target-monitor physical pixels, including mixed DPI and negative origins.
            let size = PhysicalSize::new(
                ((480.0 * scale).round() as u32).min(area.size.width),
                ((780.0 * scale).round() as u32).min(area.size.height),
            );
            let position = panel_position(area.position, area.size, size, gap);
            if window.outer_position()? != position { window.set_position(position)?; }
            if window.inner_size()? != size { window.set_size(size)?; }
        }
        present(&window)?;
    }
    Ok(())
}

#[tauri::command]
pub fn open_settings(app: AppHandle) -> Result<(), String> {
    suppress_orb(&app);
    let settings = app
        .get_webview_window("settings")
        .ok_or("Settings window unavailable")?;
    app.state::<WindowPresentation>().mark_hidden("main");
    if let Some(panel) = app.get_webview_window("main") {
        panel.hide().map_err(|error| error.to_string())?;
    }
    if app.state::<WindowPresentation>().request("settings", OpenRequest::Settings) {
        present(&settings).map_err(|error| error.to_string())?;
    }
    Ok(())
}

fn website_url(value: &str) -> Result<tauri::Url, String> {
    let url = tauri::Url::parse(value).map_err(|_| "Invalid website URL")?;
    if !matches!(url.scheme(), "https" | "http")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err("Only HTTP / HTTPS website addresses without credentials are supported".into());
    }
    Ok(url)
}

#[tauri::command]
pub fn open_website(url: String) -> Result<(), String> {
    let url = website_url(&url)?;
    #[cfg(target_os = "windows")]
    {
        use windows::core::{w, PCWSTR};
        use windows::Win32::UI::{Shell::ShellExecuteW, WindowsAndMessaging::SW_SHOWNORMAL};
        let target: Vec<u16> = url.as_str().encode_utf16().chain(Some(0)).collect();
        // Ask Windows to open the registered HTTPS handler directly. Spawning
        // Explorer only reports process creation, even when no browser opens.
        let result = unsafe { ShellExecuteW(None, w!("open"), PCWSTR(target.as_ptr()), None, None, SW_SHOWNORMAL) };
        if result.0 as isize <= 32 { return Err("BROWSER_OPEN_FAILED".into()); }
        Ok(())
    }
    #[cfg(not(target_os = "windows"))]
    {
        #[cfg(target_os = "macos")]
        let mut command = std::process::Command::new("open");
        #[cfg(not(target_os = "macos"))]
        let mut command = std::process::Command::new("xdg-open");
        let status = command.arg(url.as_str()).status().map_err(|_| "BROWSER_OPEN_FAILED")?;
        if !status.success() { return Err("BROWSER_OPEN_FAILED".into()); }
        Ok(())
    }
}

#[tauri::command]
pub async fn hide_window(window: WebviewWindow) -> Result<(), String> {
    window.state::<WindowPresentation>().mark_hidden(window.label());
    window.hide().map_err(|error| error.to_string())?;
    if let Some(orb) = window.app_handle().try_state::<crate::orb::controller::OrbHandle>() {
        orb.queue_visibility_sync();
    }
    Ok(())
}

/// Reuse the existing settings window when it is open; otherwise show usage.
/// Going through presentation preserves loading, focus and orb visibility rules.
pub(crate) fn activate_primary_window(app: &AppHandle) -> Result<(), String> {
    if let Some(settings) = app.get_webview_window("settings") {
        if settings.is_visible().unwrap_or(false)
            || settings.is_minimized().unwrap_or(false)
            || app.state::<WindowPresentation>().has_pending("settings")
        {
            return open_settings(app.clone());
        }
    }
    request_initial_panel(app).map_err(|error| error.to_string())
}
#[tauri::command]
pub async fn show_window(window: WebviewWindow) -> Result<(), String> {
    if window.label() == "settings" { open_settings(window.app_handle().clone()) }
    else { request_initial_panel(window.app_handle()).map_err(|e| e.to_string()) }
}

#[tauri::command]
pub async fn quit_app(app: AppHandle, state: State<'_, AppState>) -> Result<(), String> {
    state.shutdown().await?;
    app.exit(0);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn primary_window_startup_minimize_restore_blocks_orb() {
        assert!(window_blocks_orb(true, false, false), "pending startup must not flash the orb");
        assert!(window_blocks_orb(false, true, false), "open main suppresses orb");
        assert!(!window_blocks_orb(false, true, true), "OS minimize releases orb");
        assert!(!window_blocks_orb(false, false, false), "minimize to tray releases orb");
        assert!(window_blocks_orb(false, true, false), "restore suppresses orb again");
    }

    #[test]
    fn startup_and_early_clicks_wait_for_frontend_once() {
        let state = WindowPresentation::default();
        assert!(!state.request("main", OpenRequest::Panel(PhysicalPosition::new(0.0, 0.0))));
        assert!(!state.request("main", OpenRequest::Panel(PhysicalPosition::new(1800.0, 900.0))));
        assert!(matches!(state.ready("main"), Some(OpenRequest::Panel(point)) if point.x == 1800.0));
        assert!(state.ready("main").is_none());
        assert!(state.request("main", OpenRequest::Panel(PhysicalPosition::new(0.0, 0.0))));
    }

    #[test]
    fn minimized_start_and_cancelled_requests_do_not_pop_open_on_ready() {
        let state = WindowPresentation::default();
        assert!(state.ready("main").is_none());
        assert!(!state.request("settings", OpenRequest::Settings));
        state.cancel("settings");
        assert!(state.ready("settings").is_none());
    }

    #[test]
    fn blur_before_first_focus_does_not_hide() {
        let state = WindowPresentation::default();
        assert!(!state.on_focus_change("main", false));
        state.mark_hidden("main");
        assert!(!state.on_focus_change("main", false));
    }

    #[test]
    fn blur_after_real_focus_schedules_hide() {
        let state = WindowPresentation::default();
        assert!(!state.on_focus_change("main", true));
        assert!(state.on_focus_change("main", false));
        state.mark_hidden("main");
        assert!(!state.on_focus_change("main", false));
    }

    #[test]
    fn popup_stays_in_work_area_on_negative_origin_and_small_monitors() {
        assert_eq!(
            panel_position(
                PhysicalPosition::new(-1920, 0),
                PhysicalSize::new(1920, 1040),
                PhysicalSize::new(630, 840),
                18
            ),
            PhysicalPosition::new(-648, 182)
        );
        assert_eq!(
            panel_position(
                PhysicalPosition::new(0, 40),
                PhysicalSize::new(400, 500),
                PhysicalSize::new(400, 500),
                12
            ),
            PhysicalPosition::new(0, 40)
        );
    }

    #[test]
    fn website_opener_rejects_non_web_schemes_and_credentials() {
        for value in [
            "file:///C:/Windows",
            "javascript:alert(1)",
            "https://user:secret@example.com",
            "--help",
            "mailto:test@example.com",
        ] {
            assert!(website_url(value).is_err());
        }
        assert!(website_url("https://example.com/").is_ok());
        assert!(website_url("http://localhost:3000/").is_ok());
        assert!(website_url("http://127.0.0.1:3000/login?return_to=%2Fleaderboard").is_ok());
    }
}
