//! macOS Keychain and per-user LaunchAgent integration.

#![forbid(unsafe_code)]

use std::fs;
use std::io::Write;
#[cfg(unix)]
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Command;

use serde::{Deserialize, Serialize};
use thiserror::Error;

pub const DEVICE_KEY_LEN: usize = 32;
pub const DEFAULT_KEYCHAIN_SERVICE: &str = "dev.tokenshow.collector.device-key";
pub const LAUNCH_AGENT_LABEL: &str = "dev.tokenshow.collector";

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SecretRef {
    pub provider: String,
    pub service: String,
    pub account: String,
}

impl SecretRef {
    pub fn device_key(account: impl Into<String>) -> Self {
        Self {
            provider: "macos-keychain".into(),
            service: DEFAULT_KEYCHAIN_SERVICE.into(),
            account: account.into(),
        }
    }
}

#[derive(Debug, Error)]
pub enum PlatformError {
    #[error("macOS integration is unavailable on this platform")]
    Unsupported,
    #[error("invalid Keychain reference")]
    InvalidSecretRef,
    #[error("Keychain operation failed: {0}")]
    Keychain(String),
    #[error("stored device key has length {actual}, expected {DEVICE_KEY_LEN}")]
    InvalidKeyLength { actual: usize },
    #[error("secure random generation failed")]
    Random,
    #[error("invalid LaunchAgent value")]
    InvalidLaunchAgentValue,
    #[error("LaunchAgent I/O failed: {0}")]
    Io(#[from] std::io::Error),
    #[error("launchctl failed: {0}")]
    Launchctl(String),
}

pub fn device_key(secret: &SecretRef) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
    keychain::device_key(secret)
}

pub fn delete_secret(secret: &SecretRef) -> Result<(), PlatformError> {
    keychain::delete_secret(secret)
}

pub fn launch_agent_path(home: &Path) -> PathBuf {
    home.join("Library/LaunchAgents")
        .join(format!("{LAUNCH_AGENT_LABEL}.plist"))
}

pub fn render_launch_agent(
    executable: &Path,
    arguments: &[String],
    stdout_path: &Path,
    stderr_path: &Path,
) -> Result<String, PlatformError> {
    let executable = executable
        .to_str()
        .ok_or(PlatformError::InvalidLaunchAgentValue)?;
    if executable.is_empty() {
        return Err(PlatformError::InvalidLaunchAgentValue);
    }
    let mut args = format!("    <string>{}</string>\n", xml_escape(executable)?);
    for argument in arguments {
        args.push_str(&format!("    <string>{}</string>\n", xml_escape(argument)?));
    }
    Ok(format!(
        "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\">\n<dict>\n  <key>Label</key><string>{LAUNCH_AGENT_LABEL}</string>\n  <key>ProgramArguments</key>\n  <array>\n{args}  </array>\n  <key>RunAtLoad</key><true/>\n  <key>KeepAlive</key><true/>\n  <key>ProcessType</key><string>Background</string>\n  <key>StandardOutPath</key><string>{}</string>\n  <key>StandardErrorPath</key><string>{}</string>\n</dict>\n</plist>\n",
        xml_escape_path(stdout_path)?,
        xml_escape_path(stderr_path)?,
    ))
}

pub fn install_launch_agent(
    home: &Path,
    executable: &Path,
    arguments: &[String],
    log_dir: &Path,
) -> Result<PathBuf, PlatformError> {
    let path = launch_agent_path(home);
    let parent = path
        .parent()
        .ok_or(PlatformError::InvalidLaunchAgentValue)?;
    fs::create_dir_all(parent)?;
    fs::create_dir_all(log_dir)?;
    let plist = render_launch_agent(
        executable,
        arguments,
        &log_dir.join("collector.stdout.log"),
        &log_dir.join("collector.stderr.log"),
    )?;
    let temporary = path.with_extension("plist.tmp");
    let mut file = fs::File::create(&temporary)?;
    file.write_all(plist.as_bytes())?;
    file.sync_all()?;
    set_owner_only(&temporary)?;
    fs::rename(&temporary, &path)?;
    Ok(path)
}

pub fn bootstrap_launch_agent(uid: u32, plist: &Path) -> Result<(), PlatformError> {
    let domain = format!("gui/{uid}");
    let _ = Command::new("launchctl")
        .args(["bootout", &domain])
        .arg(plist)
        .status();
    let output = Command::new("launchctl")
        .args(["bootstrap", &domain])
        .arg(plist)
        .output()?;
    if output.status.success() {
        Ok(())
    } else {
        Err(PlatformError::Launchctl(
            String::from_utf8_lossy(&output.stderr).trim().into(),
        ))
    }
}

pub fn uninstall_launch_agent(uid: u32, home: &Path) -> Result<(), PlatformError> {
    let path = launch_agent_path(home);
    if path.exists() {
        let domain = format!("gui/{uid}");
        let output = Command::new("launchctl")
            .args(["bootout", &domain])
            .arg(&path)
            .output()?;
        if !output.status.success()
            && !String::from_utf8_lossy(&output.stderr).contains("No such process")
        {
            return Err(PlatformError::Launchctl(
                String::from_utf8_lossy(&output.stderr).trim().into(),
            ));
        }
        fs::remove_file(path)?;
    }
    Ok(())
}

#[cfg(unix)]
fn set_owner_only(path: &Path) -> Result<(), PlatformError> {
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    Ok(())
}

#[cfg(not(unix))]
fn set_owner_only(_: &Path) -> Result<(), PlatformError> {
    Ok(())
}

fn xml_escape_path(path: &Path) -> Result<String, PlatformError> {
    xml_escape(
        path.to_str()
            .ok_or(PlatformError::InvalidLaunchAgentValue)?,
    )
}

fn xml_escape(value: &str) -> Result<String, PlatformError> {
    if value.contains('\0') {
        return Err(PlatformError::InvalidLaunchAgentValue);
    }
    Ok(value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;"))
}

#[cfg(target_os = "macos")]
mod keychain;

#[cfg(not(target_os = "macos"))]
mod keychain {
    use super::*;

    pub fn device_key(_: &SecretRef) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
        Err(PlatformError::Unsupported)
    }

    pub fn delete_secret(_: &SecretRef) -> Result<(), PlatformError> {
        Err(PlatformError::Unsupported)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn launch_agent_is_per_user_and_escaped() {
        let plist = render_launch_agent(
            Path::new("/Applications/TokenShow & Tools/collector"),
            &["--background".into(), "a<b".into()],
            Path::new("/tmp/out.log"),
            Path::new("/tmp/err.log"),
        )
        .unwrap();
        assert!(plist.contains("<key>Label</key><string>dev.tokenshow.collector</string>"));
        assert!(plist.contains("/Applications/TokenShow &amp; Tools/collector"));
        assert!(plist.contains("a&lt;b"));
        assert!(!plist.contains("UserName"));
    }

    #[cfg(unix)]
    #[test]
    fn install_writes_owner_only_plist_atomically() {
        let home = tempfile::tempdir().unwrap();
        let logs = home.path().join("logs");
        let path = install_launch_agent(
            home.path(),
            Path::new("/Applications/TokenShow.app/Contents/MacOS/collector"),
            &["--background".into()],
            &logs,
        )
        .unwrap();
        assert_eq!(
            fs::metadata(path).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }
}
