use crate::state::AppState;
use tauri::{AppHandle, Manager, PhysicalPosition, PhysicalSize, State, WebviewWindow};
use std::{collections::HashMap, sync::Mutex};

#[derive(Clone, Copy)]
enum OpenRequest { Panel(PhysicalPosition<f64>), Settings }

#[derive(Default)]
struct Presentation { ready: bool, pending: Option<OpenRequest> }

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
    pub fn cancel(&self, label: &str) {
        if let Some(entry) = self.0.lock().expect("window presentation lock").get_mut(label) { entry.pending = None; }
    }
}

fn present(window: &WebviewWindow) -> tauri::Result<()> {
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

#[tauri::command]
pub fn window_ready(app: AppHandle, window: WebviewWindow) -> Result<(), String> {
    let pending = app.state::<WindowPresentation>().ready(window.label());
    match pending {
        Some(OpenRequest::Panel(point)) => show_usage_panel(&app, point).map_err(|e| e.to_string()),
        Some(OpenRequest::Settings) => open_settings(app),
        None => Ok(()),
    }
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
    let settings = app
        .get_webview_window("settings")
        .ok_or("Settings window unavailable")?;
    app.state::<WindowPresentation>().cancel("main");
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
    let mut command = {
        use std::os::windows::process::CommandExt;
        let mut command = std::process::Command::new("explorer.exe");
        command.creation_flags(0x08000000);
        command
    };
    #[cfg(target_os = "macos")]
    let mut command = std::process::Command::new("open");
    #[cfg(not(any(target_os = "windows", target_os = "macos")))]
    let mut command = std::process::Command::new("xdg-open");
    command
        .arg(url.as_str())
        .spawn()
        .map_err(|error| error.to_string())?;
    Ok(())
}

#[tauri::command]
pub async fn hide_window(window: WebviewWindow) -> Result<(), String> {
    window.state::<WindowPresentation>().cancel(window.label());
    window.hide().map_err(|error| error.to_string())
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
