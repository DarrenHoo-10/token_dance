#[cfg(not(any(target_os = "windows", target_os = "macos")))]
use std::fs;
#[cfg(not(any(target_os = "windows", target_os = "macos")))]
use std::path::Path;
use std::path::PathBuf;
#[cfg(target_os = "windows")]
use std::process::Command;
use std::sync::Arc;

use crate::state::AutostartInfo;

pub trait AutostartProvider: Send + Sync {
    fn is_enabled(&self) -> Result<bool, String>;
    fn enable(&self) -> Result<AutostartInfo, String>;
    fn disable(&self) -> Result<AutostartInfo, String>;
    fn get_info(&self) -> Result<AutostartInfo, String>;
}

pub trait AutostartPlatform: Send + Sync {
    fn platform(&self) -> &'static str;
    fn method(&self) -> &'static str;
    fn target_path(&self) -> String;
    fn details(&self) -> String;
    fn is_enabled(&self) -> Result<bool, String>;
    fn set_enabled(&self, enabled: bool) -> Result<(), String>;
    fn login_status(&self) -> Result<crate::state::AutostartStatus, String> {
        self.is_enabled()
            .map(crate::state::AutostartStatus::from_enabled)
    }
}

pub struct SystemAutostartManager {
    platform: Arc<dyn AutostartPlatform>,
}

impl SystemAutostartManager {
    pub fn new(app_name: &str) -> Self {
        if crate::local_test::enabled() {
            return Self {
                platform: Arc::new(LocalTestAutostart),
            };
        }
        let exe_path =
            std::env::current_exe().unwrap_or_else(|_| PathBuf::from("tokendance-desktop"));
        Self {
            platform: native_platform(app_name, exe_path),
        }
    }

    #[cfg(test)]
    pub fn with_platform(platform: Arc<dyn AutostartPlatform>) -> Self {
        Self { platform }
    }

    fn info(&self, status: crate::state::AutostartStatus) -> AutostartInfo {
        AutostartInfo {
            enabled: status.is_enabled(),
            status,
            platform: self.platform.platform().into(),
            method: self.platform.method().into(),
            target_path: self.platform.target_path(),
            details: self.platform.details(),
        }
    }
}

struct LocalTestAutostart;
impl AutostartPlatform for LocalTestAutostart {
    fn platform(&self) -> &'static str {
        "local-test"
    }
    fn method(&self) -> &'static str {
        "disabled"
    }
    fn target_path(&self) -> String {
        String::new()
    }
    fn details(&self) -> String {
        "Local test does not register a login item".into()
    }
    fn is_enabled(&self) -> Result<bool, String> {
        Ok(false)
    }
    fn set_enabled(&self, enabled: bool) -> Result<(), String> {
        if enabled {
            Err("Local test does not register a login item".into())
        } else {
            Ok(())
        }
    }
}

impl AutostartProvider for SystemAutostartManager {
    fn is_enabled(&self) -> Result<bool, String> {
        self.platform.is_enabled()
    }

    fn enable(&self) -> Result<AutostartInfo, String> {
        self.platform.set_enabled(true)?;
        self.get_info()
    }

    fn disable(&self) -> Result<AutostartInfo, String> {
        self.platform.set_enabled(false)?;
        self.get_info()
    }

    fn get_info(&self) -> Result<AutostartInfo, String> {
        self.platform.login_status().map(|status| self.info(status))
    }
}

#[cfg(target_os = "windows")]
fn native_platform(app_name: &str, exe_path: PathBuf) -> Arc<dyn AutostartPlatform> {
    Arc::new(WindowsAutostart {
        app_name: app_name.into(),
        exe_path,
    })
}

#[cfg(target_os = "windows")]
struct WindowsAutostart {
    app_name: String,
    exe_path: PathBuf,
}

#[cfg(target_os = "windows")]
const LEGACY_RUN_VALUE: &str = "TokenDanceCollector";

#[cfg(target_os = "windows")]
impl WindowsAutostart {
    fn key(&self) -> &'static str {
        r"HKCU\Software\Microsoft\Windows\CurrentVersion\Run"
    }

    fn command(&self) -> String {
        format!("\"{}\" --minimized", self.exe_path.display())
    }

    fn query_value(&self, name: &str) -> Result<Option<String>, String> {
        let output = self.run_reg(&["query", self.key(), "/v", name])?;
        if output.status.success() {
            Ok(Some(String::from_utf8_lossy(&output.stdout).into_owned()))
        } else if output.status.code() == Some(1) {
            Ok(None)
        } else {
            Err(String::from_utf8_lossy(&output.stderr).trim().to_string())
        }
    }

    fn value_matches(&self, name: &str) -> Result<bool, String> {
        Ok(self
            .query_value(name)?
            .is_some_and(|stdout| stdout.contains(&self.command())))
    }

    fn delete_value(&self, name: &str) -> Result<(), String> {
        let output = self.run_reg(&["delete", self.key(), "/v", name, "/f"])?;
        if output.status.success() || output.status.code() == Some(1) {
            Ok(())
        } else {
            Err(String::from_utf8_lossy(&output.stderr).trim().to_string())
        }
    }

    fn write_value(&self) -> Result<(), String> {
        let output = self.run_reg(&[
            "add",
            self.key(),
            "/v",
            &self.app_name,
            "/t",
            "REG_SZ",
            "/d",
            &self.command(),
            "/f",
        ])?;
        if output.status.success() {
            Ok(())
        } else {
            Err(String::from_utf8_lossy(&output.stderr).trim().to_string())
        }
    }

    fn run_reg(&self, args: &[&str]) -> Result<std::process::Output, String> {
        use std::os::windows::process::CommandExt;

        Command::new("reg.exe")
            // A GUI parent otherwise creates a console on every status poll.
            .creation_flags(0x08000000) // CREATE_NO_WINDOW
            .args(args)
            .output()
            .map_err(|error| format!("failed to execute reg.exe: {error}"))
    }
}

#[cfg(target_os = "windows")]
impl AutostartPlatform for WindowsAutostart {
    fn platform(&self) -> &'static str {
        "windows"
    }
    fn method(&self) -> &'static str {
        "HKCU_Registry_Run"
    }
    fn target_path(&self) -> String {
        format!(r"{}\{}", self.key(), self.app_name)
    }
    fn details(&self) -> String {
        format!("Command: {}", self.command())
    }

    fn is_enabled(&self) -> Result<bool, String> {
        if self.value_matches(&self.app_name)? {
            return Ok(true);
        }
        if self.value_matches(LEGACY_RUN_VALUE)? {
            self.write_value()?;
            self.delete_value(LEGACY_RUN_VALUE)?;
            return Ok(true);
        }
        Ok(false)
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), String> {
        if enabled {
            self.write_value()?;
            self.delete_value(LEGACY_RUN_VALUE)
        } else {
            self.delete_value(&self.app_name)?;
            self.delete_value(LEGACY_RUN_VALUE)
        }
    }
}

#[cfg(target_os = "macos")]
fn native_platform(_app_name: &str, exe_path: PathBuf) -> Arc<dyn AutostartPlatform> {
    Arc::new(MacosAutostart { exe_path })
}

#[cfg(target_os = "macos")]
struct MacosAutostart {
    exe_path: PathBuf,
}

#[cfg(target_os = "macos")]
impl AutostartPlatform for MacosAutostart {
    fn platform(&self) -> &'static str {
        "macos"
    }
    fn method(&self) -> &'static str {
        "SMAppService_mainApp"
    }
    fn target_path(&self) -> String {
        "SMAppService.mainApp".into()
    }
    fn details(&self) -> String {
        "macOS 13+ login item via SMAppService.mainApp".into()
    }
    fn is_enabled(&self) -> Result<bool, String> {
        Ok(self.login_status()?.is_enabled())
    }
    fn login_status(&self) -> Result<crate::state::AutostartStatus, String> {
        use platform_macos::login_items::LoginItemStatus;
        match platform_macos::login_items::status().map_err(|error| error.to_string())? {
            LoginItemStatus::Enabled => Ok(crate::state::AutostartStatus::Enabled),
            LoginItemStatus::RequiresApproval => {
                Ok(crate::state::AutostartStatus::RequiresApproval)
            }
            LoginItemStatus::Unavailable | LoginItemStatus::NotFound => {
                Ok(crate::state::AutostartStatus::Unavailable)
            }
            LoginItemStatus::Disabled => Ok(crate::state::AutostartStatus::Disabled),
        }
    }
    fn set_enabled(&self, enabled: bool) -> Result<(), String> {
        if enabled && !platform_macos::login_items::running_from_install_location(&self.exe_path) {
            return Err(
                "Move TokenDance to /Applications or ~/Applications before enabling login start"
                    .into(),
            );
        }
        let status =
            platform_macos::login_items::set_enabled(enabled).map_err(|error| error.to_string())?;
        // Keep the existing launch item until its replacement is enabled.
        // Registration may fail or require approval in System Settings.
        if enabled && status == platform_macos::login_items::LoginItemStatus::Enabled {
            platform_macos::login_items::migrate_legacy_plist()
                .map_err(|error| error.to_string())?;
        }
        Ok(())
    }
}

#[cfg(not(any(target_os = "windows", target_os = "macos")))]
fn native_platform(_app_name: &str, exe_path: PathBuf) -> Arc<dyn AutostartPlatform> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .unwrap_or_default();
    Arc::new(FileAutostart {
        platform: "linux",
        method: "XDG_Autostart_Desktop",
        path: home.join(".config/autostart/tokendance-collector.desktop"),
        content: format!(
            "[Desktop Entry]\nType=Application\nName=TokenDance\nExec=\"{}\" --minimized\nX-GNOME-Autostart-enabled=true\n",
            exe_path.display()
        ),
    })
}

#[cfg(not(any(target_os = "windows", target_os = "macos")))]
struct FileAutostart {
    platform: &'static str,
    method: &'static str,
    path: PathBuf,
    content: String,
}

#[cfg(not(any(target_os = "windows", target_os = "macos")))]
impl AutostartPlatform for FileAutostart {
    fn platform(&self) -> &'static str {
        self.platform
    }
    fn method(&self) -> &'static str {
        self.method
    }
    fn target_path(&self) -> String {
        self.path.display().to_string()
    }
    fn details(&self) -> String {
        format!("Launch target: {}", self.path.display())
    }
    fn is_enabled(&self) -> Result<bool, String> {
        Ok(self.path.exists())
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), String> {
        if enabled {
            if let Some(parent) = self.path.parent() {
                fs::create_dir_all(parent).map_err(|error| error.to_string())?;
            }
            write_atomic(&self.path, self.content.as_bytes())
        } else if self.path.exists() {
            fs::remove_file(&self.path).map_err(|error| error.to_string())
        } else {
            Ok(())
        }
    }
}

#[cfg(not(any(target_os = "windows", target_os = "macos")))]
fn write_atomic(path: &Path, content: &[u8]) -> Result<(), String> {
    let temporary = path.with_extension("tmp");
    fs::write(&temporary, content).map_err(|error| error.to_string())?;
    fs::rename(temporary, path).map_err(|error| error.to_string())
}
