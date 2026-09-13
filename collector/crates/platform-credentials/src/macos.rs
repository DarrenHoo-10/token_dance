//! macOS Keychain is the durable secret store. In-memory cache is process-local.
//!
//! Notarized builds use the data-protection Keychain. Free distribution opts
//! into the traditional login Keychain at build time, without paid profiles.
//! Neither mode replaces inaccessible keys or falls back to plaintext storage.
//!
//! Leftover plaintext files from an older build are migrated and deleted.
//! Legacy login-keychain items are read silently and rewritten into the data
//! protection keychain when available in that mode. Background operations are
//! noninteractive. Only an explicit free-build authorization action may show
//! the system dialog; locked/denied errors preserve existing secrets.

use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;
use std::sync::{Mutex, OnceLock};

use core_foundation::base::{CFType, TCFType};
use core_foundation::data::CFData;
use core_foundation::dictionary::CFDictionary;
use core_foundation::string::{CFString, CFStringRef};
use security_framework::access_control::{ProtectionMode, SecAccessControl};
use security_framework::os::macos::keychain::SecKeychain;
use security_framework::os::macos::passwords::find_generic_password;
use security_framework::passwords::{
    delete_generic_password_options, generic_password, PasswordOptions,
};
use security_framework_sys::base::{errSecAuthFailed, errSecDuplicateItem, errSecItemNotFound};
use security_framework_sys::item::{kSecUseAuthenticationUI, kSecValueData};
use security_framework_sys::keychain_item::{SecItemAdd, SecItemUpdate};

use super::CredentialError;

const USER_CANCELED: i32 = -128;
const INTERACTION_NOT_ALLOWED: i32 = -25308;
const BUNDLE_ID: &str = "io.tokendance.desktop";

static CACHE: OnceLock<Mutex<HashMap<(String, String), String>>> = OnceLock::new();
static KEYCHAIN: OnceLock<Mutex<()>> = OnceLock::new();

// security-framework-sys exposes the query key but not the "fail" value.
// This symbol is declared by the macOS Security.framework SDK (10.11+).
extern "C" {
    static kSecUseAuthenticationUIFail: CFStringRef;
}

fn cache() -> &'static Mutex<HashMap<(String, String), String>> {
    CACHE.get_or_init(|| Mutex::new(HashMap::new()))
}

fn keychain_lock() -> &'static Mutex<()> {
    KEYCHAIN.get_or_init(|| Mutex::new(()))
}

fn cache_key(service: &str, account: &str) -> (String, String) {
    (service.to_string(), account.to_string())
}

fn cache_get(service: &str, account: &str) -> Option<String> {
    cache()
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
        .get(&cache_key(service, account))
        .cloned()
}

fn cache_put(service: &str, account: &str, secret: &str) {
    cache()
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
        .insert(cache_key(service, account), secret.to_string());
}

fn cache_delete(service: &str, account: &str) {
    cache()
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
        .remove(&cache_key(service, account));
}

fn leftover_path(service: &str, account: &str) -> Result<PathBuf, CredentialError> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| CredentialError::Unavailable("HOME is unavailable".into()))?;
    let name: String = format!("{service}.{account}")
        .chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() || ch == '.' || ch == '-' || ch == '_' {
                ch
            } else {
                '_'
            }
        })
        .collect();
    Ok(home
        .join("Library/Application Support")
        .join(BUNDLE_ID)
        .join("collector")
        .join("keystore")
        .join(name))
}

fn leftover_get(service: &str, account: &str) -> Result<String, CredentialError> {
    let bytes = fs::read(leftover_path(service, account)?).map_err(map_file_error)?;
    let secret = decode_secret(&bytes)?;
    if secret.is_empty() {
        return Err(CredentialError::Invalid);
    }
    Ok(secret)
}

fn leftover_delete(service: &str, account: &str) -> Result<(), CredentialError> {
    ignore_not_found(fs::remove_file(leftover_path(service, account)?).map_err(map_file_error))
}

fn map_file_error(error: std::io::Error) -> CredentialError {
    if error.kind() == std::io::ErrorKind::NotFound {
        CredentialError::NotFound
    } else {
        CredentialError::Unavailable(format!("legacy credential file: {error}"))
    }
}

fn map_error(err: security_framework::base::Error) -> CredentialError {
    match err.code() {
        code if code == errSecItemNotFound => CredentialError::NotFound,
        code if code == USER_CANCELED || code == errSecAuthFailed => {
            CredentialError::Denied(format!("os keystore access denied ({code})"))
        }
        code if code == INTERACTION_NOT_ALLOWED => {
            CredentialError::Locked(format!("os keystore is locked ({code})"))
        }
        code => {
            let text = err.to_string();
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
                CredentialError::Unavailable(format!("os keystore error {code}: {text}"))
            }
        }
    }
}

fn decode_secret(bytes: &[u8]) -> Result<String, CredentialError> {
    String::from_utf8(bytes.to_vec()).map_err(|_| CredentialError::Invalid)
}

fn dp_query(service: &str, account: &str) -> PasswordOptions {
    let mut options = silent_query(service, account);
    options.use_protected_keychain();
    options
}

fn silent_query(service: &str, account: &str) -> PasswordOptions {
    let mut options = PasswordOptions::new_generic_password(service, account);
    // Fail rather than skip protected items: skipping would look like NotFound
    // and could cause a caller to generate a new WAL key or device identity.
    #[allow(deprecated)]
    options.query.push(unsafe {
        (
            CFString::wrap_under_get_rule(kSecUseAuthenticationUI),
            CFString::wrap_under_get_rule(kSecUseAuthenticationUIFail).into_CFType(),
        )
    });
    options
}

fn dp_write_options(service: &str, account: &str) -> Result<PasswordOptions, CredentialError> {
    let mut options = dp_query(service, account);
    options.set_label(BUNDLE_ID);
    let access = SecAccessControl::create_with_protection(
        Some(ProtectionMode::AccessibleAfterFirstUnlockThisDeviceOnly),
        0,
    )
    .map_err(map_error)?;
    options.set_access_control(access);
    Ok(options)
}

fn keychain_get_dp(service: &str, account: &str) -> Result<String, CredentialError> {
    match generic_password(dp_query(service, account)) {
        Ok(bytes) => decode_secret(&bytes),
        Err(err) => Err(map_error(err)),
    }
}

fn keychain_put_dp(service: &str, account: &str, secret: &str) -> Result<(), CredentialError> {
    let query = options_dictionary(&dp_query(service, account));
    let value = unsafe {
        (
            CFString::wrap_under_get_rule(kSecValueData),
            CFData::from_buffer(secret.as_bytes()).into_CFType(),
        )
    };
    let updates = CFDictionary::from_CFType_pairs(&[value.clone()]);
    let mut create = dp_write_options(service, account)?;
    #[allow(deprecated)]
    create.query.push(value);
    let create = options_dictionary(&create);
    // Match an existing item by identity only. Label and access-control are
    // creation attributes; including them in an update query can fail to find
    // an existing credential created by an older version of the application.
    let status = upsert_status(
        || unsafe { SecItemUpdate(query.as_concrete_TypeRef(), updates.as_concrete_TypeRef()) },
        || unsafe { SecItemAdd(create.as_concrete_TypeRef(), std::ptr::null_mut()) },
    );
    if status == 0 {
        Ok(())
    } else {
        Err(map_error(security_framework::base::Error::from_code(
            status,
        )))
    }
}

#[allow(deprecated)]
fn options_dictionary(options: &PasswordOptions) -> CFDictionary<CFString, CFType> {
    CFDictionary::from_CFType_pairs(&options.query)
}

fn upsert_status(mut update: impl FnMut() -> i32, insert: impl FnOnce() -> i32) -> i32 {
    let status = update();
    if status != errSecItemNotFound {
        return status;
    }
    let status = insert();
    if status == errSecDuplicateItem {
        update()
    } else {
        status
    }
}

fn keychain_delete_dp(service: &str, account: &str) -> Result<(), CredentialError> {
    ignore_not_found(delete_generic_password_options(dp_query(service, account)).map_err(map_error))
}

fn keychain_get_legacy(service: &str, account: &str) -> Result<String, CredentialError> {
    let options = silent_query(service, account);
    match generic_password(options) {
        Ok(bytes) => return decode_secret(&bytes),
        Err(err) if err.code() == errSecItemNotFound => {}
        Err(err) => return Err(map_error(err)),
    }
    match find_generic_password(None, service, account) {
        Ok((password, _)) => decode_secret(&password),
        Err(err) => Err(map_error(err)),
    }
}

fn keychain_delete_legacy(service: &str, account: &str) -> Result<(), CredentialError> {
    ignore_not_found(
        delete_generic_password_options(silent_query(service, account)).map_err(map_error),
    )
}

fn ignore_not_found(result: Result<(), CredentialError>) -> Result<(), CredentialError> {
    match result {
        Ok(()) | Err(CredentialError::NotFound) => Ok(()),
        Err(error) => Err(error),
    }
}

fn keychain_get(service: &str, account: &str) -> Result<String, CredentialError> {
    if super::uses_login_keychain() {
        // Chosen at build time. Do not probe inaccessible data-protection
        // storage and mistake it for a missing free-distribution credential.
        return keychain_get_legacy(service, account);
    }
    lookup_secret(
        keychain_get_dp(service, account),
        || keychain_get_legacy(service, account),
        |secret| keychain_put_dp(service, account, secret),
    )
}

fn lookup_secret(
    protected: Result<String, CredentialError>,
    legacy: impl FnOnce() -> Result<String, CredentialError>,
    migrate: impl FnOnce(&str) -> Result<(), CredentialError>,
) -> Result<String, CredentialError> {
    let protected_error = match protected {
        Ok(secret) => return Ok(secret),
        Err(error @ (CredentialError::NotFound | CredentialError::Unavailable(_))) => error,
        Err(error) => return Err(error),
    };
    match legacy() {
        Ok(secret) => {
            // Keep the source intact even if a local unsigned build cannot
            // migrate it. Readable legacy secrets remain usable without UI.
            let _ = migrate(&secret);
            Ok(secret)
        }
        // Unavailable data protection storage does not prove the credential
        // is absent. Never tell callers to mint a replacement key in this case.
        Err(CredentialError::NotFound) => Err(protected_error),
        Err(error) => Err(error),
    }
}

fn keychain_put(service: &str, account: &str, secret: &str) -> Result<(), CredentialError> {
    if super::uses_login_keychain() {
        let keychain = SecKeychain::default().map_err(map_error)?;
        return put_legacy_in(&keychain, service, account, secret);
    }
    keychain_put_dp(service, account, secret)
}

fn put_legacy_in(
    keychain: &SecKeychain,
    service: &str,
    account: &str,
    secret: &str,
) -> Result<(), CredentialError> {
    match keychain.find_generic_password(service, account) {
        Ok((_, mut item)) => item.set_password(secret.as_bytes()).map_err(map_error),
        Err(error) if error.code() == errSecItemNotFound => keychain
            .add_generic_password(service, account, secret.as_bytes())
            .map_err(map_error),
        // In particular, denial/lock is not permission to replace a key.
        Err(error) => Err(map_error(error)),
    }
}

fn keychain_delete(service: &str, account: &str) -> Result<(), CredentialError> {
    if super::uses_login_keychain() {
        // Preserve the authoritative system copy if leftover cleanup fails.
        leftover_delete(service, account)?;
        return keychain_delete_legacy(service, account);
    }
    // Clear the fallback first. Otherwise a failed legacy deletion could
    // resurrect an old login session once the protected copy is gone.
    remove_copies(
        || keychain_delete_legacy(service, account),
        || keychain_delete_dp(service, account),
        || leftover_delete(service, account),
    )
}

#[cfg(feature = "login-keychain")]
pub fn authorize_login_keychain(accounts: &[String]) -> Result<(), CredentialError> {
    let _guard = keychain_lock()
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    // No UI suppression here: the caller is an explicit authorization button.
    // The same lock serializes background reads so they cannot display prompts.
    authorize_accounts(accounts, |account| {
        match find_generic_password(None, super::DESKTOP_SERVICE, account) {
            Ok((password, _)) => decode_secret(&password).map(Some),
            Err(error) if error.code() == errSecItemNotFound => Ok(None),
            Err(error) => Err(map_error(error)),
        }
    })
}

#[cfg(feature = "login-keychain")]
fn authorize_accounts(
    accounts: &[String],
    mut read: impl FnMut(&str) -> Result<Option<String>, CredentialError>,
) -> Result<(), CredentialError> {
    for account in accounts {
        super::validate(super::DESKTOP_SERVICE, account)?;
        // Preserve earlier grants if a later system prompt was canceled.
        if cache_get(super::DESKTOP_SERVICE, account).is_some() { continue; }
        if let Some(secret) = read(account)? {
            cache_put(super::DESKTOP_SERVICE, account, &secret);
        }
    }
    Ok(())
}

fn remove_copies(
    legacy: impl FnOnce() -> Result<(), CredentialError>,
    protected: impl FnOnce() -> Result<(), CredentialError>,
    leftover: impl FnOnce() -> Result<(), CredentialError>,
) -> Result<(), CredentialError> {
    legacy()?;
    // Remove the file before the final durable copy. If file cleanup fails,
    // a retry must still read the current protected credential, not old bytes.
    leftover()?;
    protected()
}

pub fn get(service: &str, account: &str) -> Result<String, CredentialError> {
    if let Some(secret) = cache_get(service, account) {
        return Ok(secret);
    }
    let _guard = keychain_lock()
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    if let Some(secret) = cache_get(service, account) {
        return Ok(secret);
    }
    let _no_ui = SecKeychain::disable_user_interaction().map_err(map_error)?;
    match keychain_get(service, account) {
        Ok(secret) => {
            cache_put(service, account, &secret);
            Ok(secret)
        }
        Err(CredentialError::NotFound) => {
            let secret = leftover_get(service, account)?;
            keychain_put(service, account, &secret)?;
            leftover_delete(service, account)?;
            cache_put(service, account, &secret);
            Ok(secret)
        }
        Err(error) => Err(error),
    }
}

pub fn put(service: &str, account: &str, secret: &str) -> Result<(), CredentialError> {
    let _guard = keychain_lock()
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    let _no_ui = SecKeychain::disable_user_interaction().map_err(map_error)?;
    keychain_put(service, account, secret)?;
    leftover_delete(service, account)?;
    cache_put(service, account, secret);
    Ok(())
}

pub fn delete(service: &str, account: &str) -> Result<(), CredentialError> {
    let _guard = keychain_lock()
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    let _no_ui = SecKeychain::disable_user_interaction().map_err(map_error)?;
    keychain_delete(service, account)?;
    cache_delete(service, account);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;

    #[test]
    #[cfg(feature = "login-keychain")]
    fn canceled_authorization_reuses_earlier_grants_on_retry() {
        let first = "test-recovery-cache-first".to_string();
        let second = "test-recovery-cache-second".to_string();
        let accounts = vec![first.clone(), second.clone()];
        let mut calls = Vec::new();
        assert!(authorize_accounts(&accounts, |account| {
            calls.push(account.to_string());
            if account == first { Ok(Some("fixture-one".into())) }
            else { Err(CredentialError::Denied("canceled (-128)".into())) }
        }).is_err());
        assert_eq!(calls, accounts);
        calls.clear();
        authorize_accounts(&accounts, |account| {
            calls.push(account.to_string());
            Ok(Some("fixture-two".into()))
        }).unwrap();
        assert_eq!(calls, vec![second.clone()]);
        assert_eq!(cache_get(super::super::DESKTOP_SERVICE, &first).as_deref(), Some("fixture-one"));
        cache_delete(super::super::DESKTOP_SERVICE, &first);
        cache_delete(super::super::DESKTOP_SERVICE, &second);
    }

    #[test]
    #[cfg(feature = "login-keychain")]
    #[ignore = "explicit integration test using only a newly-created disposable keychain"]
    fn free_backend_persists_in_an_isolated_os_keychain() {
        let _guard = keychain_lock().lock().unwrap();
        let _no_ui = SecKeychain::disable_user_interaction().unwrap();
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("isolated.keychain");
        let keychain = security_framework::os::macos::keychain::CreateOptions::new()
            .password("disposable-test-keychain-password")
            .create(&path)
            .unwrap();
        struct Cleanup(std::path::PathBuf);
        impl Drop for Cleanup {
            fn drop(&mut self) {
                let _ = std::process::Command::new("security")
                    .arg("delete-keychain")
                    .arg(&self.0)
                    .output();
            }
        }
        let _cleanup = Cleanup(path.clone());
        put_legacy_in(
            &keychain,
            "io.tokendance.disposable-test",
            "account",
            "first",
        )
        .unwrap();
        let reopened = SecKeychain::open(&path).unwrap();
        let (password, _) = reopened
            .find_generic_password("io.tokendance.disposable-test", "account")
            .unwrap();
        assert_eq!(password.as_ref(), b"first");
        put_legacy_in(
            &reopened,
            "io.tokendance.disposable-test",
            "account",
            "second",
        )
        .unwrap();
        let (password, _) = keychain
            .find_generic_password("io.tokendance.disposable-test", "account")
            .unwrap();
        assert_eq!(password.as_ref(), b"second");
    }

    #[test]
    fn updates_match_identity_without_creation_attributes() {
        let options = dp_query("test", "account");
        for key in unsafe {
            [
                security_framework_sys::item::kSecAttrLabel,
                security_framework_sys::item::kSecAttrAccessControl,
            ]
        } {
            let key = unsafe { CFString::wrap_under_get_rule(key) };
            #[allow(deprecated)]
            let has_creation_attribute = options.query.iter().any(|(name, _)| name == &key);
            assert!(!has_creation_attribute);
        }
    }

    #[test]
    fn denied_update_never_attempts_to_create_a_replacement() {
        assert_eq!(
            upsert_status(
                || errSecAuthFailed,
                || panic!("must not replace inaccessible item")
            ),
            errSecAuthFailed
        );
    }

    #[test]
    fn concurrent_creation_retries_the_identity_only_update() {
        let mut calls = 0;
        let status = upsert_status(
            || {
                calls += 1;
                if calls == 1 {
                    errSecItemNotFound
                } else {
                    0
                }
            },
            || errSecDuplicateItem,
        );
        assert_eq!(status, 0);
        assert_eq!(calls, 2);
    }

    #[test]
    fn every_secitem_query_refuses_authentication_ui() {
        for options in [
            silent_query("test", "account"),
            dp_query("test", "account"),
            dp_write_options("test", "account").unwrap(),
        ] {
            let key = unsafe { CFString::wrap_under_get_rule(kSecUseAuthenticationUI) };
            let fail =
                unsafe { CFString::wrap_under_get_rule(kSecUseAuthenticationUIFail).into_CFType() };
            #[allow(deprecated)]
            let value = options
                .query
                .iter()
                .find(|(name, _)| name == &key)
                .map(|(_, value)| value);
            assert_eq!(value, Some(&fail));
        }
    }

    #[test]
    fn locked_protected_key_does_not_attempt_legacy_or_migration() {
        let result = lookup_secret(
            Err(CredentialError::Locked("test".into())),
            || panic!("must not read fallback"),
            |_| panic!("must not replace a locked key"),
        );
        assert!(matches!(result, Err(CredentialError::Locked(_))));
    }

    #[test]
    fn legacy_denial_is_returned_without_an_interactive_retry() {
        let reads = RefCell::new(0);
        let result = lookup_secret(
            Err(CredentialError::NotFound),
            || {
                *reads.borrow_mut() += 1;
                Err(CredentialError::Denied("test".into()))
            },
            |_| panic!("must not migrate a denied key"),
        );
        assert!(matches!(result, Err(CredentialError::Denied(_))));
        assert_eq!(*reads.borrow(), 1);
    }

    #[test]
    fn unavailable_protected_storage_is_not_reported_as_a_missing_key() {
        let result = lookup_secret(
            Err(CredentialError::Unavailable(
                "missing signing entitlement".into(),
            )),
            || Err(CredentialError::NotFound),
            |_| panic!("must not migrate missing legacy data"),
        );
        assert!(matches!(result, Err(CredentialError::Unavailable(_))));
    }

    #[test]
    fn existing_protected_secret_wins_over_an_old_legacy_secret() {
        let result = lookup_secret(
            Ok("current".into()),
            || panic!("must not read legacy data"),
            |_| panic!("must not migrate legacy data"),
        );
        assert_eq!(result.unwrap(), "current");
    }

    #[test]
    fn failed_migration_preserves_a_readable_legacy_secret() {
        let result = lookup_secret(
            Err(CredentialError::NotFound),
            || Ok("legacy-key".into()),
            |secret| {
                assert_eq!(secret, "legacy-key");
                Err(CredentialError::Unavailable("test".into()))
            },
        );
        assert_eq!(result.unwrap(), "legacy-key");
    }

    #[test]
    fn failed_fallback_deletion_keeps_the_current_secret() {
        let result = remove_copies(
            || Err(CredentialError::Denied("test".into())),
            || panic!("must retain protected copy"),
            || panic!("must retain leftover"),
        );
        assert!(matches!(result, Err(CredentialError::Denied(_))));
    }

    #[test]
    fn failed_file_cleanup_keeps_the_current_secret() {
        let result = remove_copies(
            || Ok(()),
            || panic!("must retain protected copy"),
            || Err(CredentialError::Unavailable("test".into())),
        );
        assert!(matches!(result, Err(CredentialError::Unavailable(_))));
    }

    #[test]
    fn protected_deletion_failure_is_reported() {
        let result = remove_copies(
            || Ok(()),
            || Err(CredentialError::Locked("test".into())),
            || Ok(()),
        );
        assert!(matches!(result, Err(CredentialError::Locked(_))));
    }

    #[test]
    fn deletion_is_idempotent_only_for_missing_items() {
        assert!(ignore_not_found(Err(CredentialError::NotFound)).is_ok());
        assert!(ignore_not_found(Err(CredentialError::Denied("test".into()))).is_err());
    }

    #[test]
    fn unreadable_legacy_files_are_not_reported_as_missing() {
        assert!(matches!(
            map_file_error(std::io::ErrorKind::PermissionDenied.into()),
            CredentialError::Unavailable(_)
        ));
        assert!(matches!(
            map_file_error(std::io::ErrorKind::NotFound.into()),
            CredentialError::NotFound
        ));
        assert!(matches!(
            decode_secret(&[0xff]),
            Err(CredentialError::Invalid)
        ));
    }
}
