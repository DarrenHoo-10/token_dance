use crate::state::{AppState, DataDeletionResponse, OperationAck};
use tauri::State;

#[tauri::command]
pub async fn request_data_deletion(
    state: State<'_, AppState>,
) -> Result<OperationAck<DataDeletionResponse>, String> {
    state.request_data_deletion().await
}

#[tauri::command]
pub async fn purge_local_cache(state: State<'_, AppState>) -> Result<OperationAck<u64>, String> {
    state.purge_local_queue().await
}
