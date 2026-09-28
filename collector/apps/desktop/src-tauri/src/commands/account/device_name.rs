//! Human-readable initial label; device identity remains the signing key.

const MAX_DEVICE_NAME_BYTES: usize = 120;

pub(super) fn initial_device_name(
    hostname: Option<&str>,
    os: &str,
    public_key_hex: &str,
) -> String {
    hostname.and_then(normalize_hostname).unwrap_or_else(|| {
        let platform = match os {
            "macos" => "Mac",
            "windows" => "Windows",
            "linux" => "Linux",
            _ => "Device",
        };
        let suffix = public_key_hex
            .get(..8)
            .unwrap_or("00000000")
            .to_ascii_uppercase();
        format!("{platform} · {suffix}")
    })
}

fn normalize_hostname(raw: &str) -> Option<String> {
    if raw.chars().any(char::is_control) {
        return None;
    }
    let trimmed = raw.trim();
    let without_local = if trimmed.to_ascii_lowercase().ends_with(".local") {
        &trimmed[..trimmed.len() - ".local".len()]
    } else {
        trimmed
    };
    let compact = without_local
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ");
    if compact.is_empty()
        || compact.eq_ignore_ascii_case("localhost")
        || compact.eq_ignore_ascii_case("tokendance desktop")
    {
        return None;
    }
    let end = compact
        .char_indices()
        .take_while(|(index, ch)| index + ch.len_utf8() <= MAX_DEVICE_NAME_BYTES)
        .last()
        .map(|(index, ch)| index + ch.len_utf8())?;
    Some(compact[..end].trim_end().to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn uses_the_machine_name_when_available() {
        assert_eq!(
            initial_device_name(
                Some("  Darren's MacBook Pro.local  "),
                "macos",
                "0123456789"
            ),
            "Darren's MacBook Pro"
        );
        assert_eq!(
            initial_device_name(Some("DESKTOP-4D2A"), "windows", "0123456789"),
            "DESKTOP-4D2A"
        );
    }

    #[test]
    fn generic_or_missing_hostname_uses_a_stable_device_specific_label() {
        assert_eq!(
            initial_device_name(Some("localhost"), "macos", "abcdef012345"),
            "Mac · ABCDEF01"
        );
        assert_eq!(
            initial_device_name(None, "windows", "1234567890ab"),
            "Windows · 12345678"
        );
        assert_ne!(
            initial_device_name(None, "windows", "1234567890ab"),
            initial_device_name(None, "windows", "876543210abc")
        );
    }

    #[test]
    fn trims_utf8_at_the_server_name_limit() {
        let name = initial_device_name(Some(&"中".repeat(50)), "macos", "01234567");
        assert_eq!(name.as_bytes().len(), 120);
        assert_eq!(name.chars().count(), 40);
    }
}
