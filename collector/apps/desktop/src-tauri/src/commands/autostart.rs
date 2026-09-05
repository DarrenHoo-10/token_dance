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
