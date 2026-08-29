use crate::state::{AgentConfig, AppState};
use tauri::State;

#[tauri::command]
pub async fn get_agent_configs(state: State<'_, AppState>) -> Result<Vec<AgentConfig>, String> {
    Ok(state.get_agents().await)
}

#[tauri::command]
pub async fn toggle_agent(state: State<'_, AppState>, agent_id: String) -> Result<AgentConfig, String> {
    state.toggle_agent(&agent_id).await
}

#[tauri::command]
pub async fn set_agent_status(
    state: State<'_, AppState>,
    agent_id: String,
    enabled: bool,
) -> Result<AgentConfig, String> {
    state.set_agent_status(&agent_id, enabled).await
}
