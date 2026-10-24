use crate::state::AppState;
use tauri::State;

/// Epoch ms when Qoder token exposure was auto-enabled, or null once the
/// one-time notice has been dismissed.
#[tauri::command]
pub async fn get_qoder_env_notice(state: State<'_, AppState>) -> Result<Option<i64>, String> {
    Ok(state.qoder_env_notice().await)
}

/// Dismiss the Qoder env notice; the environment variable stays enabled.
#[tauri::command]
pub async fn dismiss_qoder_env_notice(state: State<'_, AppState>) -> Result<(), String> {
    state.dismiss_qoder_env_notice().await
}
