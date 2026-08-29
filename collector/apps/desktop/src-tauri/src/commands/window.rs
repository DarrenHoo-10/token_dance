use crate::state::AppState;
use tauri::{AppHandle, State, WebviewWindow};

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
