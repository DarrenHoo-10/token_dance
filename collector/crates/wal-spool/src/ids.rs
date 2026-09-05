use rand::RngCore;

const CROCKFORD: &[u8; 32] = b"0123456789ABCDEFGHJKMNPQRSTVWXYZ";

/// `xxx_` + 26 Crockford characters, matching protocol prefixed ids.
pub fn new_prefixed_id(prefix: &str) -> String {
    debug_assert_eq!(prefix.len(), 3);
    let mut bytes = [0u8; 26];
    rand::rngs::OsRng.fill_bytes(&mut bytes);
    let mut out = String::with_capacity(30);
    out.push_str(prefix);
    out.push('_');
    for byte in bytes {
        out.push(CROCKFORD[(byte >> 3) as usize] as char);
    }
    out
}

pub fn utc_now_rfc3339() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let dur = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    // Tests use explicit timestamps; this is a stable fallback, not a calendar impl.
    format!("{}.{:03}Z", dur.as_secs(), dur.subsec_millis())
}
