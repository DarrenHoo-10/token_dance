use crate::state::{AppState, DataDeletionResponse};
use tauri::State;

#[tauri::command]
pub async fn request_data_deletion(state: State<'_, AppState>) -> Result<DataDeletionResponse, String> {
    Ok(state.request_data_deletion().await)
}

#[tauri::command]
pub async fn purge_local_cache(state: State<'_, AppState>) -> Result<u64, String> {
    Ok(state.purge_local_cache().await)
}
