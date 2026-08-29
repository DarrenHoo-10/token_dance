use crate::state::{AppState, CollectorDevice};
use tauri::State;

#[tauri::command]
pub async fn list_devices(state: State<'_, AppState>) -> Result<Vec<CollectorDevice>, String> {
    Ok(state.list_devices().await)
}

#[tauri::command]
pub async fn revoke_device(state: State<'_, AppState>, device_id: String) -> Result<bool, String> {
    state.revoke_device(&device_id).await
}
