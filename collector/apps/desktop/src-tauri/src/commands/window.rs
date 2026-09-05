use crate::state::AppState;
use tauri::{AppHandle, Manager, PhysicalPosition, PhysicalSize, State, WebviewWindow};

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
    if let Some(window) = app.get_webview_window("main") {
        if let Some(monitor) = app.monitor_from_point(point.x, point.y)? {
            let area = monitor.work_area();
            let scale = monitor.scale_factor();
            let gap = (12.0 * scale).round() as i32;
            // Compute in target-monitor physical pixels, including mixed DPI and negative origins.
            let size = PhysicalSize::new(
                ((420.0 * scale).round() as u32).min(area.size.width),
                ((560.0 * scale).round() as u32).min(area.size.height),
            );
            window.set_position(panel_position(area.position, area.size, size, gap))?;
            window.set_size(size)?;
        }
        window.show()?;
        window.set_focus()?;
    }
    Ok(())
}

#[tauri::command]
pub fn open_settings(app: AppHandle) -> Result<(), String> {
    let settings = app
        .get_webview_window("settings")
        .ok_or("Settings window unavailable")?;
    settings.unminimize().map_err(|error| error.to_string())?;
    settings.show().map_err(|error| error.to_string())?;
    settings.set_focus().map_err(|error| error.to_string())?;
    if let Some(panel) = app.get_webview_window("main") {
        panel.hide().map_err(|error| error.to_string())?;
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
    window.hide().map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn show_window(window: WebviewWindow) -> Result<(), String> {
    window.show().map_err(|error| error.to_string())?;
    window.set_focus().map_err(|error| error.to_string())
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
    }
}
