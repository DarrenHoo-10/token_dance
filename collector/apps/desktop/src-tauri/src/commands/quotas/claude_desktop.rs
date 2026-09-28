//! Claude Desktop writes account quota utilization to a local, bounded history.
//! This is a percentage of the shared plan allowance, not a token count.
use super::{AgentQuota, QuotaWindow};
use chrono::{DateTime, Utc};
use serde_json::Value;
use std::{
    fs,
    io::Read,
    path::{Path, PathBuf},
};

const MAX_FILE_BYTES: u64 = 4 * 1024 * 1024;
const EARLIEST_SAMPLE_MS: i64 = 1_577_836_800_000; // 2020-01-01 UTC

#[cfg(target_os = "macos")]
fn history_path() -> Option<PathBuf> {
    std::env::var_os("HOME").map(|home| {
        PathBuf::from(home).join("Library/Application Support/Claude/plan-usage-history.json")
    })
}

#[cfg(target_os = "windows")]
fn history_path() -> Option<PathBuf> {
    std::env::var_os("APPDATA")
        .map(|appdata| PathBuf::from(appdata).join("Claude/plan-usage-history.json"))
}

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
fn history_path() -> Option<PathBuf> {
    None
}

pub(super) fn read_quota() -> Option<AgentQuota> {
    read_quota_from(&history_path()?, Utc::now().timestamp_millis())
}

fn read_quota_from(path: &Path, now_ms: i64) -> Option<AgentQuota> {
    let file = fs::File::open(path).ok()?;
    if file.metadata().ok()?.len() > MAX_FILE_BYTES {
        return None;
    }
    let mut bytes = Vec::new();
    file.take(MAX_FILE_BYTES + 1).read_to_end(&mut bytes).ok()?;
    if bytes.len() as u64 > MAX_FILE_BYTES {
        return None;
    }
    parse_history(&bytes, now_ms)
}

fn parse_history(bytes: &[u8], now_ms: i64) -> Option<AgentQuota> {
    let value: Value = serde_json::from_slice(bytes).ok()?;
    let version = value.get("version")?.as_u64()?;
    if version != 1 && version != 2 {
        return None;
    }
    let samples = value.get("samples")?.as_array()?;
    samples.iter().rev().find_map(|sample| {
        let time_ms = sample.get("t")?.as_i64()?;
        if !(EARLIEST_SAMPLE_MS..=now_ms.saturating_add(5 * 60_000)).contains(&time_ms) {
            return None;
        }
        let observed_at = DateTime::<Utc>::from_timestamp_millis(time_ms)?.to_rfc3339();
        let usage = if version == 2 {
            sample.get("u")?
        } else {
            sample
        };
        let mut windows = Vec::new();
        for (key, minutes, label) in [("fh", 300, "five_hour"), ("sd", 10_080, "weekly")] {
            if let Some(used_percent) = usage
                .get(key)
                .and_then(Value::as_f64)
                .filter(|number| number.is_finite() && (0.0..=100.0).contains(number))
            {
                windows.push(QuotaWindow {
                    used_percent,
                    window_minutes: minutes,
                    resets_at: None,
                    provider: Some("Claude".into()),
                    label: Some(label.into()),
                });
            }
        }
        if windows.is_empty() {
            return None;
        }
        Some(AgentQuota {
            agent_id: "claude-code".into(),
            observed_at,
            plan: None,
            windows,
            status: None,
        })
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const NOW: i64 = 1_780_000_000_000;

    #[test]
    fn reads_shared_claude_plan_percentages_without_exposing_account_identity() {
        let bytes = format!(
            r#"{{"version":2,"samples":[{{"t":{},"org":"private-org","u":{{"fh":12,"sd":7}}}},{{"t":{},"org":"current-org","u":{{"fh":41,"sd":11,"cw":3}}}}]}}"#,
            NOW - 600_000,
            NOW - 1_000
        );
        let quota = parse_history(bytes.as_bytes(), NOW).unwrap();
        assert_eq!(quota.agent_id, "claude-code");
        assert_eq!(
            quota
                .windows
                .iter()
                .map(|w| (w.label.as_deref(), w.used_percent))
                .collect::<Vec<_>>(),
            vec![(Some("five_hour"), 41.0), (Some("weekly"), 11.0)]
        );
        assert!(!serde_json::to_string(&quota)
            .unwrap()
            .contains("current-org"));
    }

    #[test]
    fn rejects_unknown_schema_and_out_of_range_utilization() {
        let unsupported = format!(
            r#"{{"version":3,"samples":[{{"t":{},"u":{{"fh":41}}}}]}}"#,
            NOW
        );
        assert!(parse_history(unsupported.as_bytes(), NOW).is_none());
        let invalid = format!(
            r#"{{"version":2,"samples":[{{"t":{},"u":{{"fh":101,"sd":-1}}}}]}}"#,
            NOW
        );
        assert!(parse_history(invalid.as_bytes(), NOW).is_none());
        let future = format!(
            r#"{{"version":2,"samples":[{{"t":{},"u":{{"fh":41}}}}]}}"#,
            NOW + 600_000
        );
        assert!(parse_history(future.as_bytes(), NOW).is_none());
    }

    #[test]
    fn reads_legacy_desktop_history() {
        let bytes = format!(
            r#"{{"version":1,"samples":[{{"t":{},"fh":25,"sd":null}}]}}"#,
            NOW
        );
        let quota = parse_history(bytes.as_bytes(), NOW).unwrap();
        assert_eq!(quota.windows.len(), 1);
        assert_eq!(quota.windows[0].used_percent, 25.0);
    }
}
