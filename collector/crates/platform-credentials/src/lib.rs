//! Shared OS credential store for WAL keys, device seeds, and login sessions.
//!
//! Production targets enable the native Windows / Apple keyring backend. A mock
//! store is never an acceptable fallback on those platforms.

use thiserror::Error;

pub const DESKTOP_SERVICE: &str = "io.tokendance.desktop";
pub const WAL_KEY_ACCOUNT: &str = "collector-wal-key";
pub const DEVICE_SEED_ACCOUNT: &str = "collector-device-ed25519";
pub const IDENTITY_SECRET_ACCOUNT: &str = "collector-identity-hmac";
pub const LEGACY_TOKENSHOW_SERVICE: &str = "dev.tokenshow.collector.device-key";

#[derive(Debug, Error)]
pub enum CredentialError {
    #[error("credential not found")]
    NotFound,
    #[error("os keystore is locked: {0}")]
    Locked(String),
    #[error("os keystore access denied: {0}")]
    Denied(String),
    #[error("stored credential is invalid")]
    Invalid,
    #[error("os keystore is unavailable: {0}")]
    Unavailable(String),
}

/// True when this build talks to a real, cross-process OS store.
pub fn backend_is_persistent() -> bool {
    cfg!(any(target_os = "macos", windows))
}

/// Explicit free-distribution backend, never an automatic fallback from a
/// failed protected-keychain read. Both stores remain OS-encrypted stores.
pub fn uses_login_keychain() -> bool {
    cfg!(all(target_os = "macos", feature = "login-keychain"))
}

/// Only call in response to an explicit user action. Never returns secrets.
pub fn authorize_login_keychain(accounts: &[String]) -> Result<(), CredentialError> {
    #[cfg(all(target_os = "macos", feature = "login-keychain"))]
    {
        return macos::authorize_login_keychain(accounts);
    }
    #[cfg(not(all(target_os = "macos", feature = "login-keychain")))]
    {
        let _ = accounts;
        Ok(())
    }
}

#[cfg(target_os = "macos")]
mod macos;

pub fn get(service: &str, account: &str) -> Result<String, CredentialError> {
    validate(service, account)?;
    ensure_native_backend()?;
    #[cfg(target_os = "macos")]
    {
        macos::get(service, account)
    }
    #[cfg(not(target_os = "macos"))]
    {
        let entry = entry(service, account)?;
        match entry.get_password() {
            Ok(secret) => Ok(secret),
            Err(error) => Err(map_keyring_error(error)),
        }
    }
}

pub fn put(service: &str, account: &str, secret: &str) -> Result<(), CredentialError> {
    validate(service, account)?;
    ensure_native_backend()?;
    #[cfg(target_os = "macos")]
    {
        macos::put(service, account, secret)
    }
    #[cfg(not(target_os = "macos"))]
    {
        let entry = entry(service, account)?;
        entry.set_password(secret).map_err(map_keyring_error)
    }
}

pub fn delete(service: &str, account: &str) -> Result<(), CredentialError> {
    validate(service, account)?;
    ensure_native_backend()?;
    #[cfg(target_os = "macos")]
    {
        macos::delete(service, account)
    }
    #[cfg(not(target_os = "macos"))]
    {
        let entry = entry(service, account)?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(error) => Err(map_keyring_error(error)),
        }
    }
}

/// Read a historical `dev.tokenshow.*` item. Never writes over TokenDance credentials.
pub fn get_legacy_tokenshow(account: &str) -> Result<String, CredentialError> {
    get(LEGACY_TOKENSHOW_SERVICE, account)
}

#[cfg(not(target_os = "macos"))]
fn entry(service: &str, account: &str) -> Result<keyring::Entry, CredentialError> {
    keyring::Entry::new(service, account)
        .map_err(|error| CredentialError::Unavailable(format!("os keystore entry failed: {error}")))
}

fn validate(service: &str, account: &str) -> Result<(), CredentialError> {
    if service.trim().is_empty()
        || account.trim().is_empty()
        || service.contains('\0')
        || account.contains('\0')
    {
        Err(CredentialError::Invalid)
    } else {
        Ok(())
    }
}

fn ensure_native_backend() -> Result<(), CredentialError> {
    if backend_is_persistent() {
        return Ok(());
    }
    if std::env::var_os("TOKENDANCE_ALLOW_MOCK_KEYSTORE").is_some() {
        return Ok(());
    }
    Err(CredentialError::Unavailable(
        "persistent OS keystore is required on this platform".into(),
    ))
}

#[cfg(not(target_os = "macos"))]
fn map_keyring_error(error: keyring::Error) -> CredentialError {
    match error {
        keyring::Error::NoEntry => CredentialError::NotFound,
        keyring::Error::Ambiguous(_) => CredentialError::Invalid,
        keyring::Error::NoStorageAccess(message) => CredentialError::Denied(message.to_string()),
        other => {
            let text = other.to_string();
            let lower = text.to_ascii_lowercase();
            if lower.contains("user canceled")
                || lower.contains("(-128)")
                || lower.contains("auth failed")
                || lower.contains("not authorized")
                || lower.contains("denied")
            {
                CredentialError::Denied(text)
            } else if lower.contains("lock") || lower.contains("interaction not allowed") {
                CredentialError::Locked(text)
            } else {
                CredentialError::Unavailable(text)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn unique_account(label: &str) -> String {
        format!(
            "td-cred-test-{label}-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_nanos()
        )
    }

    #[test]
    fn rejects_empty_service_or_account() {
        assert!(matches!(get("", "a"), Err(CredentialError::Invalid)));
        assert!(matches!(put("svc", "", "x"), Err(CredentialError::Invalid)));
    }

    #[test]
    #[cfg_attr(
        target_os = "macos",
        ignore = "login keychain sheets can block unsigned test binaries"
    )]
    fn roundtrip_and_delete() {
        if !backend_is_persistent() {
            std::env::set_var("TOKENDANCE_ALLOW_MOCK_KEYSTORE", "1");
        }
        let account = unique_account("roundtrip");
        assert!(matches!(
            get(DESKTOP_SERVICE, &account),
            Err(CredentialError::NotFound)
        ));
        put(DESKTOP_SERVICE, &account, "cafebabe").unwrap();
        assert_eq!(get(DESKTOP_SERVICE, &account).unwrap(), "cafebabe");
        delete(DESKTOP_SERVICE, &account).unwrap();
        assert!(matches!(
            get(DESKTOP_SERVICE, &account),
            Err(CredentialError::NotFound)
        ));
    }

    #[cfg(target_os = "macos")]
    #[test]
    #[ignore = "login keychain sheets can block unsigned test binaries"]
    fn keychain_item_roundtrip_does_not_block_on_a_second_read() {
        let account = unique_account("second-read");
        put(DESKTOP_SERVICE, &account, "deadbeef").unwrap();
        assert_eq!(get(DESKTOP_SERVICE, &account).unwrap(), "deadbeef");
        assert_eq!(get(DESKTOP_SERVICE, &account).unwrap(), "deadbeef");
        delete(DESKTOP_SERVICE, &account).unwrap();
        assert!(matches!(
            get(DESKTOP_SERVICE, &account),
            Err(CredentialError::NotFound)
        ));
    }
}
