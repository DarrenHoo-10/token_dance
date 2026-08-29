use crate::autostart::AutostartProvider;
use crate::state::{AppState, AutostartInfo};
use std::sync::Arc;
use tauri::State;

pub struct AutostartState {
    pub manager: Arc<dyn AutostartProvider>,
}

#[tauri::command]
pub async fn get_autostart_status(
    app_state: State<'_, AppState>,
    autostart_state: State<'_, AutostartState>,
) -> Result<AutostartInfo, String> {
    let info = autostart_state.manager.get_info();
    app_state.set_autostart_state(info.enabled).await;
    Ok(info)
}

#[tauri::command]
pub async fn set_autostart(
    app_state: State<'_, AppState>,
    autostart_state: State<'_, AutostartState>,
    enabled: bool,
) -> Result<AutostartInfo, String> {
    let res = if enabled {
        autostart_state.manager.enable()?
    } else {
        autostart_state.manager.disable()?
    };
    app_state.set_autostart_state(enabled).await;
    Ok(res)
}
