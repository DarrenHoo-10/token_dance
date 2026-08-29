use crate::state::AutostartInfo;
use std::path::PathBuf;

pub trait AutostartProvider: Send + Sync {
    fn is_enabled(&self) -> Result<bool, String>;
    fn enable(&self) -> Result<AutostartInfo, String>;
    fn disable(&self) -> Result<AutostartInfo, String>;
    fn get_info(&self) -> AutostartInfo;
}

pub struct SystemAutostartManager {
    app_name: String,
    exe_path: PathBuf,
    simulated_enabled: std::sync::atomic::AtomicBool,
}

impl SystemAutostartManager {
    pub fn new(app_name: &str) -> Self {
        let exe_path = std::env::current_exe().unwrap_or_else(|_| PathBuf::from("tokendance-collector.exe"));
        Self {
            app_name: app_name.to_string(),
            exe_path,
            simulated_enabled: std::sync::atomic::AtomicBool::new(true),
        }
    }

    pub fn get_target_path(&self) -> String {
        self.exe_path.to_string_lossy().to_string()
    }

    #[cfg(target_os = "windows")]
    pub fn windows_registry_key(&self) -> String {
        format!(r"HKCU\Software\Microsoft\Windows\CurrentVersion\Run\{}", self.app_name)
    }

    #[cfg(target_os = "macos")]
    pub fn macos_plist_path(&self) -> PathBuf {
        let home = std::env::var("HOME").unwrap_or_else(|_| "/Users/user".into());
        PathBuf::from(home)
            .join("Library")
            .join("LaunchAgents")
            .join(format!("io.tokendance.{}.plist", self.app_name.to_lowercase()))
    }

    pub fn generate_macos_plist_content(&self) -> String {
        format!(
            r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.tokendance.collector</string>
    <key>ProgramArguments</key>
    <array>
        <string>{}</string>
        <string>--minimized</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardErrorPath</key>
    <string>/tmp/io.tokendance.collector.err</string>
    <key>StandardOutPath</key>
    <string>/tmp/io.tokendance.collector.out</string>
</dict>
</plist>"#,
            self.get_target_path()
        )
    }

    pub fn generate_windows_command(&self) -> String {
        format!("\"{}\" --minimized", self.get_target_path())
    }
}

impl AutostartProvider for SystemAutostartManager {
    fn is_enabled(&self) -> Result<bool, String> {
        Ok(self.simulated_enabled.load(std::sync::atomic::Ordering::SeqCst))
    }

    fn enable(&self) -> Result<AutostartInfo, String> {
        self.simulated_enabled.store(true, std::sync::atomic::Ordering::SeqCst);
        Ok(self.get_info())
    }

    fn disable(&self) -> Result<AutostartInfo, String> {
        self.simulated_enabled.store(false, std::sync::atomic::Ordering::SeqCst);
        Ok(self.get_info())
    }

    fn get_info(&self) -> AutostartInfo {
        let enabled = self.simulated_enabled.load(std::sync::atomic::Ordering::SeqCst);

        #[cfg(target_os = "windows")]
        {
            AutostartInfo {
                enabled,
                platform: "windows".into(),
                method: "HKCU_Registry_Run".into(),
                target_path: self.windows_registry_key(),
                details: format!("Command: {}", self.generate_windows_command()),
            }
        }

        #[cfg(target_os = "macos")]
        {
            AutostartInfo {
                enabled,
                platform: "macos".into(),
                method: "LaunchAgents_Plist".into(),
                target_path: self.macos_plist_path().to_string_lossy().to_string(),
                details: "User-level ~/Library/LaunchAgents/io.tokendance.collector.plist".into(),
            }
        }

        #[cfg(not(any(target_os = "windows", target_os = "macos")))]
        {
            AutostartInfo {
                enabled,
                platform: "linux".into(),
                method: "XDG_Autostart_Desktop".into(),
                target_path: "~/.config/autostart/tokendance-collector.desktop".into(),
                details: "Standard Freedesktop Autostart Entry".into(),
            }
        }
    }
}
