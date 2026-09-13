use crate::state::{AppState, CollectorMetrics, DaemonStatus, OperationAck};
use tauri::State;

#[tauri::command]
pub async fn get_daemon_status(state: State<'_, AppState>) -> Result<DaemonStatus, String> {
    Ok(state.get_daemon_status().await)
}

#[tauri::command]
pub async fn toggle_global_pause(
    state: State<'_, AppState>,
) -> Result<OperationAck<DaemonStatus>, String> {
    state.toggle_global_pause().await
}

#[tauri::command]
pub async fn set_global_pause(
    state: State<'_, AppState>,
    paused: bool,
) -> Result<OperationAck<DaemonStatus>, String> {
    state.set_global_pause(paused).await
}

#[tauri::command]
pub async fn get_collector_metrics(state: State<'_, AppState>) -> Result<CollectorMetrics, String> {
    Ok(state.get_collector_metrics().await)
}

#[tauri::command]
pub async fn retry_runtime_init(state: State<'_, AppState>) -> Result<DaemonStatus, String> {
    super::account::authorize_saved_credentials(None).await?;
    state.retry_runtime_init().await?;
    Ok(state.get_daemon_status().await)
}

#[tauri::command]
pub async fn rebuild_local_data(
    state: State<'_, AppState>,
    account: State<'_, super::account::AccountState>,
) -> Result<crate::local_store::pipeline::reconstruction::RebuildStatus, String> {
    account.rebuild_local_data(&state).await
}
#[tauri::command]
pub async fn get_rebuild_status(
    state: State<'_, AppState>,
) -> Result<crate::local_store::pipeline::reconstruction::RebuildStatus, String> {
    let writer = state.pipeline_writer().ok_or("PIPELINE_UNAVAILABLE")?;
    tauri::async_runtime::spawn_blocking(move || {
        writer
            .rebuild(crate::local_store::pipeline::reconstruction::RebuildAction::Status)
            .map_err(|e| e.to_string())
    })
    .await
    .map_err(|e| e.to_string())?
}
