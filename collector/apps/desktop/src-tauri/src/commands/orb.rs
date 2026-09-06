use crate::orb::controller::{window_is_orb, OrbDetailsView, OrbHandle};
use crate::orb::preferences::PreferencesPatch;
use crate::orb::{OrbPreferences, OrbRenderSnapshot, OrbSnapshot};
use tauri::{State, WebviewWindow};

fn deny_label(window: &WebviewWindow, allowed: &[&str]) -> Result<(), String> {
    if allowed.iter().any(|label| window.label() == *label) {
        Ok(())
    } else {
        Err(format!("command is not allowed from {}", window.label()))
    }
}

#[tauri::command]
pub async fn get_orb_snapshot(window: WebviewWindow, orb: State<'_, OrbHandle>) -> Result<OrbSnapshot, String> {
    deny_label(&window, &["orb", "orb-details"])?;
    Ok(orb.snapshot())
}

#[tauri::command]
pub async fn get_orb_render_snapshot(
    window: WebviewWindow,
    orb: State<'_, OrbHandle>,
) -> Result<OrbRenderSnapshot, String> {
    deny_label(&window, &["orb-effects"])?;
    Ok(orb.render_snapshot())
}

#[tauri::command]
pub async fn get_orb_details(window: WebviewWindow, orb: State<'_, OrbHandle>) -> Result<OrbDetailsView, String> {
    deny_label(&window, &["orb-details", "settings"])?;
    Ok(orb.details())
}

#[tauri::command]
pub async fn get_orb_preferences(window: WebviewWindow, orb: State<'_, OrbHandle>) -> Result<OrbPreferences, String> {
    deny_label(&window, &["orb-details", "settings"])?;
    Ok(orb.preferences())
}

#[tauri::command]
pub async fn patch_orb_preferences(
    window: WebviewWindow,
    orb: State<'_, OrbHandle>,
    patch: PreferencesPatch,
) -> Result<OrbPreferences, String> {
    deny_label(&window, &["orb-details", "settings"])?;
    let orb = orb.inner().clone();
    tokio::task::spawn_blocking(move || orb.patch_preferences(patch))
        .await
        .map_err(|error| error.to_string())?
}

#[tauri::command]
pub async fn orb_ready(window: WebviewWindow, orb: State<'_, OrbHandle>, generation: u64) -> Result<(), String> {
    deny_label(&window, &["orb", "orb-effects", "orb-details"])?;
    orb.ready(window.label(), generation)
}

#[tauri::command]
pub async fn orb_action(
    window: WebviewWindow,
    orb: State<'_, OrbHandle>,
    action: String,
    paused: Option<bool>,
) -> Result<(), String> {
    deny_label(&window, &["orb", "orb-details"])?;
    if action == "set_paused" {
        orb.set_paused(paused.unwrap_or(true)).await?;
        return Ok(());
    }
    let label = window.label().to_string();
    let orb = orb.inner().clone();
    tokio::task::spawn_blocking(move || orb.action(&label, &action, paused))
        .await
        .map_err(|error| error.to_string())?
}

#[tauri::command]
pub async fn orb_begin_drag(window: WebviewWindow, orb: State<'_, OrbHandle>) -> Result<(), String> {
    deny_label(&window, &["orb"])?;
    orb.begin_drag(&window)
}

#[tauri::command]
pub async fn orb_end_drag(window: WebviewWindow, orb: State<'_, OrbHandle>) -> Result<(), String> {
    deny_label(&window, &["orb"])?;
    let orb = orb.inner().clone();
    tokio::task::spawn_blocking(move || orb.end_drag())
        .await
        .map_err(|error| error.to_string())?
}

#[tauri::command]
pub async fn orb_move(window: WebviewWindow, orb: State<'_, OrbHandle>, dx: f64, dy: f64) -> Result<(), String> {
    deny_label(&window, &["orb"])?;
    let orb = orb.inner().clone();
    tokio::task::spawn_blocking(move || orb.move_drag(dx, dy)).await.map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn orb_fling(window: WebviewWindow, orb: State<'_, OrbHandle>, dx: f64, dy: f64) -> Result<(), String> {
    deny_label(&window, &["orb"])?;
    let orb = orb.inner().clone();
    tokio::task::spawn_blocking(move || orb.fling(dx, dy)).await.map_err(|e| e.to_string())?
}

pub fn is_orb_window(label: &str) -> bool {
    window_is_orb(label)
}

#[cfg(test)]
mod tests {
    #[test]
    fn orb_restrictions_do_not_disable_primary_window_commands() {
        let manifests: serde_json::Value = serde_json::from_str(include_str!("../../gen/schemas/acl-manifests.json")).unwrap();
        let app = &manifests["__app-acl__"]["permissions"];
        let allowed = app["allow-primary-desktop"]["commands"]["allow"].as_array().unwrap();
        let manifests = serde_json::from_str(include_str!("../../gen/schemas/acl-manifests.json")).unwrap();
        let capabilities = serde_json::from_str(include_str!("../../gen/schemas/capabilities.json")).unwrap();
        let resolved = tauri::utils::acl::resolved::Resolved::resolve(
            &manifests, capabilities, tauri::utils::platform::Target::Windows,
        ).unwrap();
        // Tauri's explicit command denies apply before window matching. A deny
        // in an orb capability can therefore block the main window as well.
        let authority = tauri::ipc::RuntimeAuthority::new(
            #[cfg(debug_assertions)]
            manifests,
            resolved,
        );
        // Once an application ACL manifest exists, EVERY application command
        // is opt-in, even commands absent from the orb's explicit deny list.
        let source = include_str!("../lib.rs");
        let registered = source.split("tauri::generate_handler![").nth(1).unwrap().split("])").next().unwrap();
        for line in registered.lines().filter(|line| line.contains("::") && !line.contains("commands::orb::")) {
            let name = line.trim().trim_end_matches(',').rsplit("::").next().unwrap();
            assert!(allowed.iter().any(|value| value == name), "main/settings command missing ACL: {name}");
            for label in ["main", "settings"] {
                assert!(authority.resolve_access(name, label, label, &tauri::ipc::Origin::Local).is_some(), "{label} cannot call {name}");
            }
            for label in ["orb", "orb-effects", "orb-details"] {
                assert!(authority.resolve_access(name, label, label, &tauri::ipc::Origin::Local).is_none(), "{label} can call privileged command {name}");
            }
        }
        for command in ["orb_move", "orb_fling"] {
            assert!(authority.resolve_access(command, "orb", "orb", &tauri::ipc::Origin::Local).is_some());
            for label in ["main", "settings", "orb-effects", "orb-details"] {
                assert!(authority.resolve_access(command, label, label, &tauri::ipc::Origin::Local).is_none());
            }
        }
        let primary: serde_json::Value = serde_json::from_str(include_str!("../../capabilities/default.json")).unwrap();
        assert!(primary["permissions"].as_array().unwrap().iter().any(|p| p == "allow-primary-desktop"));
        for source in [include_str!("../../capabilities/orb.json"), include_str!("../../capabilities/orb-effects.json"), include_str!("../../capabilities/orb-details.json")] {
            let orb: serde_json::Value = serde_json::from_str(source).unwrap();
            assert!(!orb["permissions"].as_array().unwrap().iter().any(|p| p == "allow-primary-desktop"));
        }
    }
}
