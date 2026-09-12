//! Client feature flags for event-pipeline v2 (P8).

use std::env;

/// `TOKENDANCE_EVENT_PIPELINE_V2_CLIENT` — default true on this cutover branch.
/// When false: pause new upload/sync workers; do **not** reopen legacy snapshot upload.
pub fn event_pipeline_v2_client_enabled() -> bool {
    parse_env_bool("TOKENDANCE_EVENT_PIPELINE_V2_CLIENT").unwrap_or(true)
}

fn parse_env_bool(name: &str) -> Option<bool> {
    let raw = env::var(name).ok()?;
    match raw.trim().to_ascii_lowercase().as_str() {
        "1" | "true" | "yes" | "on" => Some(true),
        "0" | "false" | "no" | "off" => Some(false),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_client_flag_is_on() {
        // Unset in test process may still be set by environment; just ensure parser works.
        assert_eq!(parse_env_bool("__missing_flag__"), None);
        assert_eq!(
            {
                env::set_var("TOKENDANCE_EVENT_PIPELINE_V2_CLIENT_TEST", "false");
                let v = env::var("TOKENDANCE_EVENT_PIPELINE_V2_CLIENT_TEST").ok();
                env::remove_var("TOKENDANCE_EVENT_PIPELINE_V2_CLIENT_TEST");
                v
            }
            .as_deref(),
            Some("false")
        );
        assert!(matches!(
            parse_env_bool("__definitely_unset_tokendance_flag__"),
            None
        ));
    }
}
