use std::sync::Mutex;

use platform_credentials::{CredentialError, DESKTOP_SERVICE};
use rand::RngCore;
use zeroize::{Zeroize, ZeroizeOnDrop};

use crate::error::KeyError;

pub const DATA_KEY_LEN: usize = 32;

/// Supplies the per-device AEAD data key. Implementations must fail closed.
pub trait KeyProvider: Send + Sync {
    fn data_key(&self) -> Result<[u8; DATA_KEY_LEN], KeyError>;
}

/// Test/injected key. Production open paths must pass an OS provider instead.
#[derive(Clone, Zeroize, ZeroizeOnDrop)]
pub struct InjectedKeyProvider {
    key: [u8; DATA_KEY_LEN],
}

impl InjectedKeyProvider {
    pub fn new(key: [u8; DATA_KEY_LEN]) -> Self {
        Self { key }
    }
}

impl KeyProvider for InjectedKeyProvider {
    fn data_key(&self) -> Result<[u8; DATA_KEY_LEN], KeyError> {
        Ok(self.key)
    }
}

/// Always fails. Used to prove missing keys do not write plaintext.
#[derive(Debug, Default, Clone)]
pub struct UnavailableKeyProvider;

impl KeyProvider for UnavailableKeyProvider {
    fn data_key(&self) -> Result<[u8; DATA_KEY_LEN], KeyError> {
        Err(KeyError::Unavailable(
            "keystore unavailable in this context".into(),
        ))
    }
}

/// Windows Credential Manager / macOS Keychain backed device key.
pub struct OsKeyProvider {
    service: String,
    user: String,
    create_if_missing: bool,
    cached: Mutex<Option<[u8; DATA_KEY_LEN]>>,
}

impl OsKeyProvider {
    /// Create the secret on first use. Callers must use [`existing`] when an
    /// encrypted WAL or registered device identity is already on disk.
    pub fn new(service: impl Into<String>, user: impl Into<String>) -> Self {
        Self {
            service: service.into(),
            user: user.into(),
            create_if_missing: true,
            cached: Mutex::new(None),
        }
    }

    /// Read only. Missing entries do not mint a replacement key.
    pub fn existing(service: impl Into<String>, user: impl Into<String>) -> Self {
        Self {
            service: service.into(),
            user: user.into(),
            create_if_missing: false,
            cached: Mutex::new(None),
        }
    }

    pub fn wal_key(create_if_missing: bool) -> Self {
        Self {
            service: DESKTOP_SERVICE.into(),
            user: platform_credentials::WAL_KEY_ACCOUNT.into(),
            create_if_missing,
            cached: Mutex::new(None),
        }
    }

    pub fn device_seed(create_if_missing: bool) -> Self {
        Self {
            service: DESKTOP_SERVICE.into(),
            user: platform_credentials::DEVICE_SEED_ACCOUNT.into(),
            create_if_missing,
            cached: Mutex::new(None),
        }
    }

    pub fn identity_secret(create_if_missing: bool) -> Self {
        Self {
            service: DESKTOP_SERVICE.into(),
            user: platform_credentials::IDENTITY_SECRET_ACCOUNT.into(),
            create_if_missing,
            cached: Mutex::new(None),
        }
    }

    fn remember(&self, key: [u8; DATA_KEY_LEN]) -> [u8; DATA_KEY_LEN] {
        *self.cached.lock().expect("os key cache") = Some(key);
        key
    }
}

impl KeyProvider for OsKeyProvider {
    fn data_key(&self) -> Result<[u8; DATA_KEY_LEN], KeyError> {
        if let Some(key) = *self.cached.lock().expect("os key cache") {
            return Ok(key);
        }
        match platform_credentials::get(&self.service, &self.user) {
            Ok(secret) => Ok(self.remember(decode_key(&secret)?)),
            Err(CredentialError::NotFound) if self.create_if_missing => {
                let mut key = [0u8; DATA_KEY_LEN];
                rand::rngs::OsRng.fill_bytes(&mut key);
                let encoded = hex_encode(&key);
                if let Err(error) = platform_credentials::put(&self.service, &self.user, &encoded) {
                    key.zeroize();
                    return Err(map_credential_error(error));
                }
                Ok(self.remember(key))
            }
            Err(error) => Err(map_credential_error(error)),
        }
    }
}

fn map_credential_error(error: CredentialError) -> KeyError {
    match error {
        CredentialError::NotFound => KeyError::NotFound,
        CredentialError::Invalid => KeyError::Invalid,
        other => KeyError::Unavailable(other.to_string()),
    }
}

/// Fails on demand after construction. No plaintext fallback exists.
pub struct ToggleKeyProvider {
    inner: InjectedKeyProvider,
    available: Mutex<bool>,
}

impl ToggleKeyProvider {
    pub fn new(key: [u8; DATA_KEY_LEN]) -> Self {
        Self {
            inner: InjectedKeyProvider::new(key),
            available: Mutex::new(true),
        }
    }

    pub fn set_available(&self, available: bool) {
        *self.available.lock().expect("key toggle lock") = available;
    }
}

impl KeyProvider for ToggleKeyProvider {
    fn data_key(&self) -> Result<[u8; DATA_KEY_LEN], KeyError> {
        if *self.available.lock().expect("key toggle lock") {
            self.inner.data_key()
        } else {
            Err(KeyError::Unavailable("toggled unavailable".into()))
        }
    }
}

fn decode_key(secret: &str) -> Result<[u8; DATA_KEY_LEN], KeyError> {
    let raw = hex_decode(secret).ok_or(KeyError::Invalid)?;
    if raw.len() != DATA_KEY_LEN {
        return Err(KeyError::Invalid);
    }
    let mut key = [0u8; DATA_KEY_LEN];
    key.copy_from_slice(&raw);
    Ok(key)
}

fn hex_encode(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

fn hex_decode(text: &str) -> Option<Vec<u8>> {
    if !text.len().is_multiple_of(2) {
        return None;
    }
    let mut out = Vec::with_capacity(text.len() / 2);
    let bytes = text.as_bytes();
    for chunk in bytes.chunks(2) {
        let hi = from_hex(chunk[0])?;
        let lo = from_hex(chunk[1])?;
        out.push((hi << 4) | lo);
    }
    Some(out)
}

fn from_hex(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hex_roundtrip() {
        let key = [0xab; DATA_KEY_LEN];
        assert_eq!(decode_key(&hex_encode(&key)).unwrap(), key);
        assert!(decode_key("zz").is_err());
        assert!(decode_key("ab").is_err());
    }
}
