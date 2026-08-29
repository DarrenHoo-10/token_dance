use base64::engine::general_purpose::STANDARD_NO_PAD;
use base64::Engine;

use super::{PlatformError, SecretRef, DEVICE_KEY_LEN};

pub fn device_key(secret: &SecretRef) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
    validate(secret)?;
    let entry = keyring::Entry::new(&secret.service, &secret.account)
        .map_err(|error| PlatformError::Keychain(error.to_string()))?;
    match entry.get_password() {
        Ok(encoded) => decode(&encoded),
        Err(keyring::Error::NoEntry) => {
            let mut key = [0u8; DEVICE_KEY_LEN];
            getrandom::getrandom(&mut key).map_err(|_| PlatformError::Random)?;
            entry
                .set_password(&STANDARD_NO_PAD.encode(key))
                .map_err(|error| PlatformError::Keychain(error.to_string()))?;
            Ok(key)
        }
        Err(error) => Err(PlatformError::Keychain(error.to_string())),
    }
}

pub fn delete_secret(secret: &SecretRef) -> Result<(), PlatformError> {
    validate(secret)?;
    let entry = keyring::Entry::new(&secret.service, &secret.account)
        .map_err(|error| PlatformError::Keychain(error.to_string()))?;
    match entry.delete_credential() {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(error) => Err(PlatformError::Keychain(error.to_string())),
    }
}

fn validate(secret: &SecretRef) -> Result<(), PlatformError> {
    if secret.provider != "macos-keychain"
        || secret.service.trim().is_empty()
        || secret.account.trim().is_empty()
        || secret.service.contains('\0')
        || secret.account.contains('\0')
    {
        Err(PlatformError::InvalidSecretRef)
    } else {
        Ok(())
    }
}

fn decode(encoded: &str) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
    let bytes = STANDARD_NO_PAD
        .decode(encoded)
        .map_err(|error| PlatformError::Keychain(format!("invalid Keychain value: {error}")))?;
    let actual = bytes.len();
    bytes
        .try_into()
        .map_err(|_| PlatformError::InvalidKeyLength { actual })
}
