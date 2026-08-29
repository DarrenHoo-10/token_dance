use std::sync::Mutex;

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
}

impl OsKeyProvider {
    pub fn new(service: impl Into<String>, user: impl Into<String>) -> Self {
        Self {
            service: service.into(),
            user: user.into(),
        }
    }
}

impl KeyProvider for OsKeyProvider {
    fn data_key(&self) -> Result<[u8; DATA_KEY_LEN], KeyError> {
        let entry = keyring::Entry::new(&self.service, &self.user)
            .map_err(|err| KeyError::Unavailable(format!("os keystore entry failed: {err}")))?;
        match entry.get_password() {
            Ok(secret) => decode_key(&secret),
            Err(keyring::Error::NoEntry) => {
                let mut key = [0u8; DATA_KEY_LEN];
                rand::rngs::OsRng.fill_bytes(&mut key);
                let encoded = hex_encode(&key);
                entry.set_password(&encoded).map_err(|err| {
                    key.zeroize();
                    KeyError::Unavailable(format!("os keystore write failed: {err}"))
                })?;
                Ok(key)
            }
            Err(err) => Err(KeyError::Unavailable(format!(
                "os keystore read failed: {err}"
            ))),
        }
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
