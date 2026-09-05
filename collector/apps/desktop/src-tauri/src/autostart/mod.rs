#[cfg(not(target_os = "windows"))]
use std::fs;
#[cfg(target_os = "macos")]
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
}

pub struct SystemAutostartManager {
    platform: Arc<dyn AutostartPlatform>,
}

impl SystemAutostartManager {
    pub fn new(app_name: &str) -> Self {
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

    fn info(&self, enabled: bool) -> AutostartInfo {
        AutostartInfo {
            enabled,
            platform: self.platform.platform().into(),
            method: self.platform.method().into(),
            target_path: self.platform.target_path(),
            details: self.platform.details(),
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
        self.is_enabled().map(|enabled| self.info(enabled))
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
impl WindowsAutostart {
    fn key(&self) -> &'static str {
        r"HKCU\Software\Microsoft\Windows\CurrentVersion\Run"
    }

    fn command(&self) -> String {
        format!("\"{}\" --minimized", self.exe_path.display())
    }

    fn run_reg(&self, args: &[&str]) -> Result<std::process::Output, String> {
        Command::new("reg.exe")
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
        let output = self.run_reg(&["query", self.key(), "/v", &self.app_name])?;
        if output.status.success() {
            Ok(String::from_utf8_lossy(&output.stdout).contains(&self.command()))
        } else if output.status.code() == Some(1) {
            Ok(false)
        } else {
            Err(String::from_utf8_lossy(&output.stderr).trim().to_string())
        }
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), String> {
        let output = if enabled {
            self.run_reg(&[
                "add",
                self.key(),
                "/v",
                &self.app_name,
                "/t",
                "REG_SZ",
                "/d",
                &self.command(),
                "/f",
            ])?
        } else {
            self.run_reg(&["delete", self.key(), "/v", &self.app_name, "/f"])?
        };
        if output.status.success() || (!enabled && output.status.code() == Some(1)) {
            Ok(())
        } else {
            Err(String::from_utf8_lossy(&output.stderr).trim().to_string())
        }
    }
}

#[cfg(target_os = "macos")]
fn native_platform(_app_name: &str, exe_path: PathBuf) -> Arc<dyn AutostartPlatform> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .unwrap_or_default();
    Arc::new(FileAutostart {
        platform: "macos",
        method: "LaunchAgents_Plist",
        path: home.join("Library/LaunchAgents/io.tokendance.collector.plist"),
        content: macos_plist(&exe_path),
    })
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
            "[Desktop Entry]\nType=Application\nName=TokenDance Collector\nExec=\"{}\" --minimized\nX-GNOME-Autostart-enabled=true\n",
            exe_path.display()
        ),
    })
}

#[cfg(not(target_os = "windows"))]
struct FileAutostart {
    platform: &'static str,
    method: &'static str,
    path: PathBuf,
    content: String,
}

#[cfg(not(target_os = "windows"))]
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

#[cfg(target_os = "macos")]
fn macos_plist(exe_path: &Path) -> String {
    format!(
        "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict><key>Label</key><string>io.tokendance.collector</string><key>ProgramArguments</key><array><string>{}</string><string>--minimized</string></array><key>RunAtLoad</key><true/></dict></plist>\n",
        exe_path.display()
    )
}

#[cfg(not(target_os = "windows"))]
fn write_atomic(path: &Path, content: &[u8]) -> Result<(), String> {
    let temporary = path.with_extension("tmp");
    fs::write(&temporary, content).map_err(|error| error.to_string())?;
    fs::rename(temporary, path).map_err(|error| error.to_string())
}
