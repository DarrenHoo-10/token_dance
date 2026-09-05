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
