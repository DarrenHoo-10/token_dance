use crate::state::{AppState, ConfigBackup};
use tauri::State;

#[tauri::command]
pub async fn create_config_backup(
    state: State<'_, AppState>,
    description: Option<String>,
) -> Result<ConfigBackup, String> {
    Ok(state.create_config_backup(description).await)
}

#[tauri::command]
pub async fn restore_config_backup(
    state: State<'_, AppState>,
    backup_id: String,
) -> Result<bool, String> {
    state.restore_config_backup(&backup_id).await
}

#[tauri::command]
pub async fn list_config_backups(state: State<'_, AppState>) -> Result<Vec<ConfigBackup>, String> {
    Ok(state.list_config_backups().await)
}
