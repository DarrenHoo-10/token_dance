//! Windows per-user credential and startup integration.

#![deny(unsafe_op_in_unsafe_fn)]

use std::path::Path;

use serde::{Deserialize, Serialize};
use thiserror::Error;

pub const DEVICE_KEY_LEN: usize = 32;
pub const DEFAULT_CREDENTIAL_TARGET: &str = "TokenShow/Collector/device-key/v1";
pub const RUN_VALUE_NAME: &str = "TokenShow Collector";

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SecretRef {
    pub provider: String,
    pub target: String,
}

impl SecretRef {
    pub fn device_key() -> Self {
        Self {
            provider: "windows-credential-manager".into(),
            target: DEFAULT_CREDENTIAL_TARGET.into(),
        }
    }
}

#[derive(Debug, Error)]
pub enum PlatformError {
    #[error("Windows integration is unavailable on this platform")]
    Unsupported,
    #[error("invalid credential target")]
    InvalidTarget,
    #[error("invalid executable path")]
    InvalidExecutable,
    #[error("Windows API {operation} failed with error {code}")]
    WindowsApi { operation: &'static str, code: u32 },
    #[error("stored device key has length {actual}, expected {DEVICE_KEY_LEN}")]
    InvalidKeyLength { actual: usize },
    #[error("secure random generation failed")]
    Random,
}

pub fn device_key(secret: &SecretRef) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
    imp::device_key(secret)
}

pub fn delete_secret(secret: &SecretRef) -> Result<(), PlatformError> {
    imp::delete_secret(secret)
}

pub fn set_current_user_autostart(
    executable: &Path,
    arguments: &[String],
) -> Result<(), PlatformError> {
    imp::set_current_user_autostart(executable, arguments)
}

pub fn remove_current_user_autostart() -> Result<(), PlatformError> {
    imp::remove_current_user_autostart()
}

pub fn startup_command(executable: &Path, arguments: &[String]) -> Result<String, PlatformError> {
    let executable = executable
        .to_str()
        .ok_or(PlatformError::InvalidExecutable)?;
    if executable.is_empty() || executable.contains('\0') {
        return Err(PlatformError::InvalidExecutable);
    }
    let mut command = quote_windows_argument(executable);
    for argument in arguments {
        command.push(' ');
        command.push_str(&quote_windows_argument(argument));
    }
    Ok(command)
}

fn quote_windows_argument(value: &str) -> String {
    if !value.is_empty() && !value.chars().any(|ch| ch.is_whitespace() || ch == '"') {
        return value.to_owned();
    }

    let mut quoted = String::from("\"");
    let mut backslashes = 0usize;
    for ch in value.chars() {
        if ch == '\\' {
            backslashes += 1;
        } else if ch == '"' {
            quoted.push_str(&"\\".repeat(backslashes * 2 + 1));
            quoted.push('"');
            backslashes = 0;
        } else {
            quoted.push_str(&"\\".repeat(backslashes));
            backslashes = 0;
            quoted.push(ch);
        }
    }
    quoted.push_str(&"\\".repeat(backslashes * 2));
    quoted.push('"');
    quoted
}

#[cfg(windows)]
mod imp;

#[cfg(not(windows))]
mod imp {
    use super::*;

    pub fn device_key(_: &SecretRef) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
        Err(PlatformError::Unsupported)
    }

    pub fn delete_secret(_: &SecretRef) -> Result<(), PlatformError> {
        Err(PlatformError::Unsupported)
    }

    pub fn set_current_user_autostart(_: &Path, _: &[String]) -> Result<(), PlatformError> {
        Err(PlatformError::Unsupported)
    }

    pub fn remove_current_user_autostart() -> Result<(), PlatformError> {
        Err(PlatformError::Unsupported)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn secret_ref_contains_no_secret_material() {
        let encoded = serde_json::to_string(&SecretRef::device_key()).unwrap();
        assert_eq!(
            encoded,
            r#"{"provider":"windows-credential-manager","target":"TokenShow/Collector/device-key/v1"}"#
        );
    }

    #[cfg(windows)]
    #[test]
    fn credential_manager_round_trip_uses_secret_ref_only() {
        let secret = SecretRef {
            provider: "windows-credential-manager".into(),
            target: format!("TokenShow/Collector/test-device-key/{}", std::process::id()),
        };
        delete_secret(&secret).unwrap();
        let first = device_key(&secret).unwrap();
        let second = device_key(&secret).unwrap();
        assert_eq!(first, second);
        assert_ne!(first, [0; DEVICE_KEY_LEN]);
        delete_secret(&secret).unwrap();
    }

    #[test]
    fn startup_command_quotes_spaces_and_embedded_quotes() {
        let command = startup_command(
            Path::new(r"C:\Program Files\TokenShow\collector.exe"),
            &[
                "--background".into(),
                "label=hello world".into(),
                "a\"b".into(),
            ],
        )
        .unwrap();
        assert_eq!(
            command,
            r#""C:\Program Files\TokenShow\collector.exe" --background "label=hello world" "a\"b""#
        );
    }
}
