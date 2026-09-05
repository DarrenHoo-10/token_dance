use chrono::{Local, NaiveDate};
use protocol::{Accuracy, EventEnvelope, EventPayload};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};

// One extra day beyond the panel's 7-day window so late-arriving events still
// land in a live bucket instead of being dropped at the retention edge.
const RETAINED_DAYS: i64 = 8;
const LEDGER_FILE: &str = "usage-ledger.json";

fn accuracy_rank(accuracy: &Accuracy) -> u8 {
    match accuracy {
        Accuracy::Exact => 4,
        Accuracy::Derived => 3,
        Accuracy::Correlated => 2,
        Accuracy::Estimated => 1,
    }
}

fn accuracy_name(rank: u8) -> &'static str {
    match rank {
        4 => "exact",
        3 => "derived",
        2 => "correlated",
        1 => "estimated",
        _ => "unknown",
    }
}

fn oldest_retained() -> NaiveDate {
    Local::now().date_naive() - chrono::Duration::days(RETAINED_DAYS - 1)
}

fn local_date(occurred_at: &str) -> Option<NaiveDate> {
    chrono::DateTime::parse_from_rfc3339(occurred_at)
        .ok()
        .map(|time| time.with_timezone(&Local).date_naive())
}

/// Total tokens carried by an envelope; `None` for non-usage events.
fn event_tokens(event: &EventEnvelope) -> Option<u64> {
    let EventPayload::ModelUsageRecorded(payload) = &event.payload else {
        return None;
    };
    let tokens = &payload.tokens;
    if let Some(total) = tokens.total_tokens.as_deref().and_then(|value| value.parse::<u64>().ok()) {
        return Some(total);
    }
    Some(
        [
            tokens.input_tokens.as_deref(),
            tokens.output_tokens.as_deref(),
            tokens.cache_read_tokens.as_deref(),
            tokens.cache_write_tokens.as_deref(),
            tokens.reasoning_tokens.as_deref(),
            tokens.tool_tokens.as_deref(),
        ]
        .into_iter()
        .flatten()
        .filter_map(|value| value.parse::<u64>().ok())
        .sum(),
    )
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct DayUsage {
    pub date: String,
    pub tokens: u64,
}

pub struct AgentUsageSnapshot {
    pub today_tokens: u64,
    pub total_tokens: u64,
    pub accuracy: String,
    pub daily_usage: Vec<DayUsage>,
}

#[derive(Debug, Default, Serialize, Deserialize)]
struct AgentDay {
    tokens: u64,
    // Weakest accuracy merged into this day, so the panel never overstates
    // precision. 0 only means "no usage event recorded on this day".
    accuracy: u8,
}

#[derive(Debug, Default, Serialize, Deserialize)]
struct LedgerFile {
    days: BTreeMap<String, BTreeMap<String, AgentDay>>,
    seen: BTreeMap<String, HashSet<String>>,
}

/// Device-local daily token ledger ("本机数据"). The WAL only keeps unacked
/// events, so acknowledged history would be lost without this record of what
/// the collector actually saw.
pub struct UsageLedger {
    path: PathBuf,
    file: LedgerFile,
}

impl UsageLedger {
    pub fn load(dir: &Path) -> Self {
        let path = dir.join(LEDGER_FILE);
        let file = fs::read(&path)
            .ok()
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
            .unwrap_or_default();
        Self { path, file }
    }

    /// Record token usage from collected envelopes. Idempotent per event id,
    /// bucketed by the device-local date of `occurred_at`. Returns whether
    /// anything changed and is worth persisting.
    pub fn record(&mut self, events: &[EventEnvelope]) -> bool {
        let mut changed = false;
        for event in events {
            let Some(tokens) = event_tokens(event) else {
                continue;
            };
            let Some(date) = local_date(&event.occurred_at) else {
                continue;
            };
            if date < oldest_retained() {
                continue;
            }
            let date_key = date.format("%Y-%m-%d").to_string();
            let seen = self.file.seen.entry(date_key.clone()).or_default();
            if seen.contains(&event.event_id) {
                continue;
            }
            let day = self.file.days.entry(date_key.clone()).or_default();
            let agent_day = day.entry(event.agent_id.clone()).or_default();
            agent_day.tokens = agent_day.tokens.saturating_add(tokens);
            // Weakest accuracy wins so the panel never overstates precision.
            agent_day.accuracy = match agent_day.accuracy {
                0 => accuracy_rank(&event.accuracy),
                current => current.min(accuracy_rank(&event.accuracy)),
            };
            seen.insert(event.event_id.clone());
            changed = true;
        }
        if self.retain() {
            changed = true;
        }
        changed
    }

    fn retain(&mut self) -> bool {
        let cutoff = oldest_retained().format("%Y-%m-%d").to_string();
        let days_before = self.file.days.len();
        let seen_before = self.file.seen.len();
        self.file.days.retain(|date, _| date.as_str() >= cutoff.as_str());
        self.file.seen.retain(|date, _| date.as_str() >= cutoff.as_str());
        self.file.days.len() != days_before || self.file.seen.len() != seen_before
    }

    /// Per-agent snapshot over the retained window; `None` while the agent has
    /// never produced a usage event (the panel then shows its "待接入" state).
    pub fn agent_usage(&self, agent_id: &str, today: NaiveDate) -> Option<AgentUsageSnapshot> {
        let known = self
            .file
            .days
            .values()
            .any(|day| day.contains_key(agent_id));
        if !known {
            return None;
        }
        let mut week = Vec::with_capacity(RETAINED_DAYS as usize);
        let mut total = 0u64;
        let mut accuracy = u8::MAX;
        for offset in (0..RETAINED_DAYS).rev() {
            let date = (today - chrono::Duration::days(offset))
                .format("%Y-%m-%d")
                .to_string();
            let day = self.file.days.get(&date).and_then(|agents| agents.get(agent_id));
            if let Some(day) = day {
                accuracy = accuracy.min(day.accuracy);
            }
            let tokens = day.map_or(0, |day| day.tokens);
            total = total.saturating_add(tokens);
            week.push(DayUsage { date, tokens });
        }
        Some(AgentUsageSnapshot {
            today_tokens: week.last().map_or(0, |day| day.tokens),
            total_tokens: total,
            accuracy: accuracy_name(accuracy.min(4)).into(),
            daily_usage: week,
        })
    }

    pub fn save(&self) -> Result<(), String> {
        let bytes = serde_json::to_vec(&self.file).map_err(|error| error.to_string())?;
        let tmp = self.path.with_extension("json.tmp");
        fs::write(&tmp, bytes).map_err(|error| error.to_string())?;
        if self.path.exists() {
            fs::remove_file(&self.path).map_err(|error| error.to_string())?;
        }
        fs::rename(&tmp, &self.path).map_err(|error| error.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use protocol::{EventSource, SourceKind, TokenUsage};

    fn envelope(agent_id: &str, occurred_at: &str, total: u64, accuracy: Accuracy) -> EventEnvelope {
        EventEnvelope {
            schema_version: "1.0".into(),
            event_id: format!("evt-{agent_id}-{occurred_at}-{total}"),
            adapter_id: "adapter-test".into(),
            adapter_version: "1.0.0".into(),
            agent_id: agent_id.into(),
            agent_version: None,
            installation_id: "ins_test".into(),
            occurred_at: occurred_at.into(),
            session_hash: None,
            turn_hash: None,
            tool_call_hash: None,
            source: EventSource {
                kind: SourceKind::Otlp,
                cursor_hmac: String::new(),
                raw_fingerprint_hmac: String::new(),
            },
            accuracy,
            payload: EventPayload::ModelUsageRecorded(protocol::ModelUsageRecordedPayload {
                provider_id: "test".into(),
                model_id: "test-model".into(),
                tokens: TokenUsage {
                    input_tokens: Some("10".into()),
                    output_tokens: Some("5".into()),
                    cache_read_tokens: None,
                    cache_write_tokens: None,
                    reasoning_tokens: None,
                    tool_tokens: None,
                    total_tokens: Some(total.to_string()),
                },
            }),
        }
    }

    fn today_utc_noon(days_ago: i64) -> String {
        (Local::now().date_naive() - chrono::Duration::days(days_ago))
            .and_hms_opt(12, 0, 0)
            .unwrap()
            .and_local_timezone(Local)
            .unwrap()
            .to_rfc3339()
    }

    #[test]
    fn records_usage_and_dedupes_by_event_id() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let event = envelope("codex", &today_utc_noon(0), 100, Accuracy::Exact);
        assert!(ledger.record(&[event.clone()]));
        assert!(!ledger.record(&[event]), "same event id must not double count");
        let today = Local::now().date_naive();
        let snapshot = ledger.agent_usage("codex", today).unwrap();
        assert_eq!(snapshot.today_tokens, 100);
        assert_eq!(snapshot.accuracy, "exact");
        assert_eq!(snapshot.daily_usage.last().unwrap().tokens, 100);
        assert!(ledger.agent_usage("claude-code", today).is_none());
    }

    #[test]
    fn merges_weakest_accuracy_per_day() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        ledger.record(&[
            envelope("codex", &today_utc_noon(0), 10, Accuracy::Exact),
            envelope("codex", &today_utc_noon(0), 20, Accuracy::Estimated),
        ]);
        let snapshot = ledger.agent_usage("codex", Local::now().date_naive()).unwrap();
        assert_eq!(snapshot.today_tokens, 30);
        assert_eq!(snapshot.accuracy, "estimated", "weakest accuracy must win");
    }

    #[test]
    fn sums_token_parts_when_total_is_missing() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let mut event = envelope("cursor", &today_utc_noon(0), 0, Accuracy::Derived);
        if let EventPayload::ModelUsageRecorded(payload) = &mut event.payload {
            payload.tokens.total_tokens = None;
            payload.tokens.cache_read_tokens = Some("7".into());
        }
        assert!(ledger.record(&[event]));
        let snapshot = ledger.agent_usage("cursor", Local::now().date_naive()).unwrap();
        assert_eq!(snapshot.today_tokens, 22, "10 + 5 + 7");
    }

    #[test]
    fn retains_only_the_recent_window() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let old = (Local::now().date_naive() - chrono::Duration::days(RETAINED_DAYS))
            .and_hms_opt(12, 0, 0)
            .unwrap()
            .and_local_timezone(Local)
            .unwrap()
            .to_rfc3339();
        assert!(
            !ledger.record(&[envelope("codex", &old, 500, Accuracy::Exact)]),
            "events older than the window must be skipped entirely"
        );
        assert!(
            ledger.agent_usage("codex", Local::now().date_naive()).is_none(),
            "stale events must not resurrect a known agent"
        );
    }

    #[test]
    fn ignores_non_usage_events_and_unparseable_dates() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let mut event = envelope("codex", "not-a-date", 100, Accuracy::Exact);
        event.payload = EventPayload::CodeChanged(protocol::CodeChangedPayload {
            added_lines: "12".into(),
            removed_lines: "0".into(),
            generated_lines: None,
            accepted_lines: None,
            file_count: 2,
            language: None,
        });
        assert!(!ledger.record(&[event]));
    }

    #[test]
    fn save_and_load_roundtrip() {
        let dir = tempfile::tempdir().unwrap();
        let mut ledger = UsageLedger::load(dir.path());
        ledger.record(&[envelope("codex", &today_utc_noon(1), 42, Accuracy::Derived)]);
        ledger.save().unwrap();
        let reloaded = UsageLedger::load(dir.path());
        let snapshot = reloaded
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.total_tokens, 42);
        assert_eq!(snapshot.accuracy, "derived");
    }
}
