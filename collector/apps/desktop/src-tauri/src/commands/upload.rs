use crate::state::{AppState, OperationAck, OutboxEnvelope, SyncRequestState, UploadBatchPreview};
use tauri::State;

#[tauri::command]
pub async fn preview_upload_batch(
    state: State<'_, AppState>,
) -> Result<OperationAck<UploadBatchPreview>, String> {
    Ok(state.preview_upload_batch().await)
}

#[tauri::command]
pub async fn trigger_sync_now(
    state: State<'_, AppState>,
) -> Result<OperationAck<SyncRequestState>, String> {
    state.trigger_sync_now().await
}

#[tauri::command]
pub async fn get_pending_envelopes(
    state: State<'_, AppState>,
) -> Result<Vec<OutboxEnvelope>, String> {
    Ok(state.get_outbox().await)
}
