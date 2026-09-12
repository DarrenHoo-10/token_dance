//! Typed native identity encoding for pipeline fact / event keys.
//!
//! Numeric rowid `42` and string `"42"` must never collide: encode carries an
//! explicit type tag and length boundary before the keyed digest.

use sha2::{Digest, Sha256};

const FIELD_SEP: u8 = 0x1f;

/// Typed native record key — type tag is part of the digest input.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TypedNativeKey {
    /// SQLite / native integer identity (rowid, numeric step id).
    I64(i64),
    /// Stable string identity (session id, call id, …).
    Str(String),
    /// JSONL append position when the source has no native id.
    ByteOffset(u64),
}

impl TypedNativeKey {
    pub fn encode_bytes(&self) -> Vec<u8> {
        let mut out = Vec::with_capacity(24);
        match self {
            Self::I64(v) => {
                out.extend_from_slice(b"i64");
                out.push(FIELD_SEP);
                out.extend_from_slice(&v.to_le_bytes());
            }
            Self::Str(s) => {
                out.extend_from_slice(b"str");
                out.push(FIELD_SEP);
                let len = s.len() as u32;
                out.extend_from_slice(&len.to_le_bytes());
                out.extend_from_slice(s.as_bytes());
            }
            Self::ByteOffset(off) => {
                out.extend_from_slice(b"off");
                out.push(FIELD_SEP);
                out.extend_from_slice(&off.to_le_bytes());
            }
        }
        out
    }
}

/// HMAC-SHA256 without pulling an extra crate (sha2 is already a desktop dep).
fn hmac_sha256(secret: &[u8], message: &[u8]) -> [u8; 32] {
    const BLOCK: usize = 64;
    let mut key = [0u8; BLOCK];
    if secret.len() > BLOCK {
        let dig = Sha256::digest(secret);
        key[..32].copy_from_slice(&dig);
    } else {
        key[..secret.len()].copy_from_slice(secret);
    }
    let mut ipad = [0x36u8; BLOCK];
    let mut opad = [0x5cu8; BLOCK];
    for i in 0..BLOCK {
        ipad[i] ^= key[i];
        opad[i] ^= key[i];
    }
    let mut inner = Sha256::new();
    inner.update(ipad);
    inner.update(message);
    let inner_dig = inner.finalize();
    let mut outer = Sha256::new();
    outer.update(opad);
    outer.update(inner_dig);
    let out = outer.finalize();
    let mut bytes = [0u8; 32];
    bytes.copy_from_slice(&out);
    bytes
}

fn hmac_parts(secret: &[u8], parts: &[&[u8]]) -> [u8; 32] {
    let mut msg = Vec::new();
    for (i, part) in parts.iter().enumerate() {
        if i > 0 {
            msg.push(FIELD_SEP);
        }
        let len = part.len() as u32;
        msg.extend_from_slice(&len.to_le_bytes());
        msg.extend_from_slice(part);
    }
    hmac_sha256(secret, &msg)
}

pub fn source_key(secret: &[u8], harness: &str, logical_scope: &str) -> [u8; 32] {
    hmac_parts(
        secret,
        &[b"source/v1", harness.as_bytes(), logical_scope.as_bytes()],
    )
}

pub fn fact_key(
    secret: &[u8],
    harness: &str,
    logical_scope: &str,
    native: &TypedNativeKey,
    fact_kind: &str,
) -> [u8; 32] {
    let native_bytes = native.encode_bytes();
    hmac_parts(
        secret,
        &[
            b"fact/v1",
            harness.as_bytes(),
            logical_scope.as_bytes(),
            &native_bytes,
            fact_kind.as_bytes(),
        ],
    )
}

pub fn event_id(secret: &[u8], fact: &[u8; 32], revision: i64) -> [u8; 32] {
    let rev = revision.to_le_bytes();
    hmac_parts(secret, &[b"event/v1", fact.as_slice(), &rev])
}

pub fn content_hash(secret: &[u8], parts: &[&[u8]]) -> [u8; 32] {
    let mut tagged = Vec::with_capacity(parts.len() + 1);
    tagged.push(b"content/v1".as_slice());
    tagged.extend_from_slice(parts);
    hmac_parts(secret, &tagged)
}

pub fn skill_key(secret: &[u8], stable_name: &str) -> [u8; 32] {
    hmac_parts(secret, &[b"skill/v1", stable_name.as_bytes()])
}

pub fn session_key(secret: &[u8], harness: &str, session_id: &str) -> [u8; 32] {
    hmac_parts(
        secret,
        &[b"session/v1", harness.as_bytes(), session_id.as_bytes()],
    )
}

pub fn turn_key(secret: &[u8], harness: &str, session_id: &str, turn_id: &str) -> [u8; 32] {
    hmac_parts(
        secret,
        &[
            b"turn/v1",
            harness.as_bytes(),
            session_id.as_bytes(),
            turn_id.as_bytes(),
        ],
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn numeric_rowid_does_not_collide_with_string_forty_two() {
        let secret = b"test-identity-secret";
        let a = fact_key(
            secret,
            "zcode",
            "sqlite/model_usage",
            &TypedNativeKey::I64(42),
            "model_usage_recorded",
        );
        let b = fact_key(
            secret,
            "zcode",
            "sqlite/model_usage",
            &TypedNativeKey::Str("42".into()),
            "model_usage_recorded",
        );
        assert_ne!(a, b);
    }

    #[test]
    fn same_inputs_are_stable() {
        let secret = b"k";
        let native = TypedNativeKey::I64(7);
        let left = fact_key(secret, "opencode", "part", &native, "model_usage_recorded");
        let right = fact_key(secret, "opencode", "part", &native, "model_usage_recorded");
        assert_eq!(left, right);
        let eid = event_id(secret, &left, 1);
        assert_eq!(eid, event_id(secret, &left, 1));
        assert_ne!(eid, event_id(secret, &left, 2));
    }
}
