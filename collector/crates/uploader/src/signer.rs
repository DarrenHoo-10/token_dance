use std::fmt;
use std::sync::Arc;

use ed25519_dalek::{Signer as _, SigningKey};

#[derive(Debug, thiserror::Error)]
pub enum SignerError {
    #[error("device key unavailable: {0}")]
    Unavailable(String),
    #[error("device signing failed: {0}")]
    Signing(String),
}

pub trait DeviceSigner: Send + Sync {
    fn public_key(&self) -> Result<[u8; 32], SignerError>;
    fn sign(&self, message: &[u8]) -> Result<[u8; 64], SignerError>;
}

pub trait OsEd25519KeyHandle: Send + Sync {
    fn public_key(&self) -> Result<[u8; 32], SignerError>;
    fn sign_ed25519(&self, message: &[u8]) -> Result<[u8; 64], SignerError>;
}

pub struct OsKeyDeviceSigner {
    handle: Arc<dyn OsEd25519KeyHandle>,
}

impl OsKeyDeviceSigner {
    pub fn new(handle: Arc<dyn OsEd25519KeyHandle>) -> Self {
        Self { handle }
    }
}

impl fmt::Debug for OsKeyDeviceSigner {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("OsKeyDeviceSigner")
            .finish_non_exhaustive()
    }
}

impl DeviceSigner for OsKeyDeviceSigner {
    fn public_key(&self) -> Result<[u8; 32], SignerError> {
        self.handle.public_key()
    }

    fn sign(&self, message: &[u8]) -> Result<[u8; 64], SignerError> {
        self.handle.sign_ed25519(message)
    }
}

pub struct InMemoryDeviceSigner {
    key: SigningKey,
}

impl InMemoryDeviceSigner {
    pub fn from_seed(seed: [u8; 32]) -> Self {
        Self {
            key: SigningKey::from_bytes(&seed),
        }
    }
}

impl fmt::Debug for InMemoryDeviceSigner {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("InMemoryDeviceSigner")
            .finish_non_exhaustive()
    }
}

impl DeviceSigner for InMemoryDeviceSigner {
    fn public_key(&self) -> Result<[u8; 32], SignerError> {
        Ok(self.key.verifying_key().to_bytes())
    }

    fn sign(&self, message: &[u8]) -> Result<[u8; 64], SignerError> {
        Ok(self.key.sign(message).to_bytes())
    }
}
