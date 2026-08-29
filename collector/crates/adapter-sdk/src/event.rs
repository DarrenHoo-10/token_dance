use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use hmac::{Hmac, Mac};
use sha2::Sha256;

use protocol::EventEnvelope;

/// Adapter output type. Privacy and WAL layers consume this envelope only.
pub type NormalizedEvent = EventEnvelope;

const FIELD_SEP: u8 = 0x1f;

/// Rebuildable event id: HMAC-SHA256 over identity fields, base64url without padding.
pub fn event_id(
    key: &[u8],
    installation_id: &str,
    adapter_id: &str,
    source_identity: &str,
    source_cursor: &str,
    semantic_event_type: &str,
    semantic_sequence: &str,
) -> String {
    keyed_hmac(
        key,
        &[
            installation_id,
            adapter_id,
            source_identity,
            source_cursor,
            semantic_event_type,
            semantic_sequence,
        ],
    )
}

pub fn keyed_hmac(key: &[u8], parts: &[&str]) -> String {
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("HMAC-SHA256 accepts any key length");
    for (index, part) in parts.iter().enumerate() {
        if index > 0 {
            mac.update(&[FIELD_SEP]);
        }
        mac.update(part.as_bytes());
    }
    URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes())
}

pub fn raw_fingerprint(payload: &[u8]) -> String {
    use sha2::Digest;
    let digest = Sha256::digest(payload);
    let mut out = String::from("sha256:");
    for byte in digest {
        out.push_str(&format!("{byte:02x}"));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::{event_id, keyed_hmac};

    #[test]
    fn event_id_is_stable_for_identical_inputs() {
        let key = b"tokshow-mock-event-id-key";
        let left = event_id(key, "ins", "adapter", "src", "0:1", "session_started", "1");
        let right = event_id(key, "ins", "adapter", "src", "0:1", "session_started", "1");
        assert_eq!(left, right);
        assert_ne!(
            left,
            event_id(key, "ins", "adapter", "src", "0:2", "session_started", "1")
        );
    }

    #[test]
    fn keyed_hmac_uses_unit_separator() {
        let key = b"k";
        assert_ne!(keyed_hmac(key, &["a", "bc"]), keyed_hmac(key, &["ab", "c"]));
    }
}
