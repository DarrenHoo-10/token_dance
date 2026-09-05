use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use rand::RngCore;

use crate::error::WalError;
use crate::keys::DATA_KEY_LEN;

pub const NONCE_LEN: usize = 12;
pub const TAG_LEN: usize = 16;

pub fn encrypt(
    key: &[u8; DATA_KEY_LEN],
    sequence: u64,
    aad: &[u8],
    plaintext: &[u8],
) -> Result<Vec<u8>, WalError> {
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| WalError::Crypto)?;
    let nonce = unique_nonce(sequence);
    let ciphertext = cipher
        .encrypt(
            Nonce::from_slice(&nonce),
            Payload {
                msg: plaintext,
                aad,
            },
        )
        .map_err(|_| WalError::Crypto)?;
    let mut out = Vec::with_capacity(NONCE_LEN + ciphertext.len());
    out.extend_from_slice(&nonce);
    out.extend_from_slice(&ciphertext);
    Ok(out)
}

pub fn decrypt(key: &[u8; DATA_KEY_LEN], aad: &[u8], payload: &[u8]) -> Result<Vec<u8>, WalError> {
    if payload.len() < NONCE_LEN + TAG_LEN {
        return Err(WalError::Crypto);
    }
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| WalError::Crypto)?;
    let nonce = Nonce::from_slice(&payload[..NONCE_LEN]);
    cipher
        .decrypt(
            nonce,
            Payload {
                msg: &payload[NONCE_LEN..],
                aad,
            },
        )
        .map_err(|_| WalError::Crypto)
}

fn unique_nonce(sequence: u64) -> [u8; NONCE_LEN] {
    let mut nonce = [0u8; NONCE_LEN];
    nonce[..8].copy_from_slice(&sequence.to_be_bytes());
    rand::rngs::OsRng.fill_bytes(&mut nonce[8..]);
    nonce
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn nonce_includes_sequence_and_random_tail() {
        let left = unique_nonce(7);
        let right = unique_nonce(7);
        assert_eq!(&left[..8], &right[..8]);
        assert_eq!(&left[..8], &7u64.to_be_bytes());
        let other = unique_nonce(8);
        assert_ne!(&left[..8], &other[..8]);
    }
}
