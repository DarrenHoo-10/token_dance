use crate::state::{AppState, AutostartInfo};
use tauri::State;

#[tauri::command]
pub async fn get_autostart_status(app_state: State<'_, AppState>) -> Result<AutostartInfo, String> {
    app_state.get_autostart_status()
}

#[tauri::command]
pub async fn set_autostart(
    app_state: State<'_, AppState>,
    enabled: bool,
) -> Result<AutostartInfo, String> {
    app_state.set_autostart(enabled)
}

#[tauri::command]
pub async fn open_login_items_settings() -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        platform_macos::login_items::open_system_settings().map_err(|error| error.to_string())
    }
    #[cfg(not(target_os = "macos"))]
    {
        Ok(())
    }
}
