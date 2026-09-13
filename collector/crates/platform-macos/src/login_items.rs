//! SMAppService.mainApp login item bridge.
//!
//! Requires macOS 13. Callers should treat status as the source of truth after
//! every enable/disable. Unsafe is limited to the ServiceManagement bindings;
//! objects are confined to this module and not retained across threads.

use std::path::Path;
use std::process::Command;

use objc2::rc::Retained;
use objc2_foundation::NSError;
use objc2_service_management::{SMAppService, SMAppServiceStatus};

use super::PlatformError;

const KNOWN_LABEL: &str = "io.tokendance.collector";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LoginItemStatus {
    Enabled,
    Disabled,
    RequiresApproval,
    Unavailable,
    NotFound,
}

pub fn status() -> Result<LoginItemStatus, PlatformError> {
    let service = unsafe { SMAppService::mainAppService() };
    Ok(map_status(unsafe { service.status() }))
}

pub fn set_enabled(enabled: bool) -> Result<LoginItemStatus, PlatformError> {
    let service = unsafe { SMAppService::mainAppService() };
    if enabled {
        register(&service)?;
    } else {
        unregister(&service)?;
    }
    Ok(map_status(unsafe { service.status() }))
}

pub fn open_system_settings() -> Result<(), PlatformError> {
    unsafe { SMAppService::openSystemSettingsLoginItems() };
    Ok(())
}

pub fn running_from_install_location(exe: &Path) -> bool {
    let text = exe.to_string_lossy();
    if text.contains("/Volumes/") || text.contains("AppTranslocation") {
        return false;
    }
    text.contains("/Applications/TokenDance.app/")
        || text.contains("/Applications/tokendance-desktop")
        || text.contains("/Applications/") && text.contains("TokenDance.app")
}

pub fn migrate_legacy_plist() -> Result<(), PlatformError> {
    let home = std::env::var_os("HOME").map(std::path::PathBuf::from);
    let Some(home) = home else {
        return Ok(());
    };
    let path = home
        .join("Library/LaunchAgents")
        .join(format!("{KNOWN_LABEL}.plist"));
    if !path.exists() {
        return Ok(());
    }
    let content = std::fs::read_to_string(&path)?;
    let has_label = content.contains(&format!("<string>{KNOWN_LABEL}</string>"));
    let known_path = content.contains("/Applications/TokenDance.app")
        || content.contains("TokenDance.app/Contents/MacOS");
    if !has_label || !known_path {
        return Ok(());
    }
    let uid = user_id();
    let domain = format!("gui/{uid}");
    let _ = Command::new("launchctl")
        .args(["bootout", &domain])
        .arg(&path)
        .status();
    std::fs::remove_file(path)?;
    Ok(())
}

fn register(service: &SMAppService) -> Result<(), PlatformError> {
    unsafe { service.registerAndReturnError() }
        .map_err(|error| map_ns_error(Some(error), "register login item"))
}

fn unregister(service: &SMAppService) -> Result<(), PlatformError> {
    unsafe { service.unregisterAndReturnError() }
        .map_err(|error| map_ns_error(Some(error), "unregister login item"))
}

fn map_status(status: SMAppServiceStatus) -> LoginItemStatus {
    if status == SMAppServiceStatus::Enabled {
        LoginItemStatus::Enabled
    } else if status == SMAppServiceStatus::RequiresApproval {
        LoginItemStatus::RequiresApproval
    } else if status == SMAppServiceStatus::NotFound {
        LoginItemStatus::NotFound
    } else if status == SMAppServiceStatus::NotRegistered {
        LoginItemStatus::Disabled
    } else {
        LoginItemStatus::Unavailable
    }
}

fn map_ns_error(error: Option<Retained<NSError>>, action: &str) -> PlatformError {
    let detail = error
        .map(|error| error.localizedDescription().to_string())
        .unwrap_or_else(|| "unknown ServiceManagement error".into());
    PlatformError::Launchctl(format!("{action}: {detail}"))
}

fn user_id() -> String {
    Command::new("id")
        .arg("-u")
        .output()
        .ok()
        .and_then(|output| String::from_utf8(output.stdout).ok())
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| "501".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dmg_and_translocation_paths_are_not_install_locations() {
        assert!(!running_from_install_location(Path::new(
            "/Volumes/TokenDance/TokenDance.app/Contents/MacOS/TokenDance"
        )));
        assert!(!running_from_install_location(Path::new(
            "/private/var/folders/xx/AppTranslocation/TokenDance.app/Contents/MacOS/TokenDance"
        )));
        assert!(running_from_install_location(Path::new(
            "/Applications/TokenDance.app/Contents/MacOS/TokenDance"
        )));
    }
}
