use crate::pricing::{Catalog, CostCoverage, CostLedger};
use chrono::{Local, NaiveDate, Utc};
use protocol::{Accuracy, EventEnvelope, EventPayload};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};

// Keep recorded history and deduplication IDs across acknowledgements/restarts.
// Only the IPC daily series is limited to a year; All time sums the full ledger.
pub(crate) const DISPLAY_DAYS: i64 = 366;
const LEDGER_FILE: &str = "usage-ledger.json";

pub(crate) fn accuracy_rank(accuracy: &Accuracy) -> u8 {
    match accuracy {
        Accuracy::Exact => 4,
        Accuracy::Derived => 3,
        Accuracy::Correlated => 2,
        Accuracy::Estimated => 1,
    }
}

pub(crate) fn accuracy_name(rank: u8) -> &'static str {
    match rank {
        4 => "exact",
        3 => "derived",
        2 => "correlated",
        1 => "estimated",
        _ => "unknown",
    }
}

pub(crate) fn cost_units(value: &str) -> Option<u64> {
    let (whole, fraction) = value.split_once('.').unwrap_or((value, ""));
    if whole.is_empty()
        || !whole.bytes().all(|c| c.is_ascii_digit())
        || !fraction.bytes().all(|c| c.is_ascii_digit())
        || fraction.len() > 8
    {
        return None;
    }
    whole
        .parse::<u64>()
        .ok()?
        .checked_mul(100_000_000)?
        .checked_add(format!("{fraction:0<8}").parse::<u64>().ok()?)
}

pub(crate) fn local_date(occurred_at: &str) -> Option<NaiveDate> {
    chrono::DateTime::parse_from_rfc3339(occurred_at)
        .ok()
        .map(|time| time.with_timezone(&Local).date_naive())
}

/// Total tokens carried by an envelope; `None` for non-usage events.
pub(crate) fn event_tokens(event: &EventEnvelope) -> Option<u64> {
    let EventPayload::ModelUsageRecorded(payload) = &event.payload else {
        return None;
    };
    let tokens = &payload.tokens;
    if let Some(total) = tokens
        .total_tokens
        .as_deref()
        .and_then(|value| value.parse::<u64>().ok())
    {
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
    #[serde(default)]
    pub costs: BTreeMap<String, u64>,
    #[serde(default)]
    pub pricing: CostCoverage,
}

pub struct AgentUsageSnapshot {
    pub today_tokens: u64,
    pub total_tokens: u64,
    pub accuracy: String,
    pub daily_usage: Vec<DayUsage>,
    pub total_costs: BTreeMap<String, u64>,
    pub pricing: CostCoverage,
    pub history_start: String,
}

/// Lightweight today totals. `today_tokens` is `None` when no listed agent has a
/// known (accuracy != unknown) usage record; known agents with no events today
/// contribute 0.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LedgerTodaySummary {
    pub today_tokens: Option<u128>,
    pub known_source_count: usize,
    pub known_agent_ids: Vec<String>,
    pub last_recorded_change_at_ms: Option<i64>,
}

#[derive(Debug, Default, Serialize, Deserialize)]
struct AgentDay {
    tokens: u64,
    // Weakest accuracy merged into this day, so the panel never overstates
    // precision. 0 only means "no usage event recorded on this day".
    accuracy: u8,
    #[serde(default)]
    costs: BTreeMap<String, u64>,
}

#[derive(Debug, Default, Serialize, Deserialize)]
struct LedgerFile {
    #[serde(default)]
    pricing: CostLedger,
    days: BTreeMap<String, BTreeMap<String, AgentDay>>,
    seen: BTreeMap<String, HashSet<String>>,
    #[serde(default)]
    last_recorded_change_at_ms: Option<i64>,
}

/// Device-local daily token ledger ("本机数据"). The WAL only keeps unacked
/// events, so acknowledged history would be lost without this record of what
/// the collector actually saw.
pub struct UsageLedger {
    path: PathBuf,
    file: LedgerFile,
    pub catalog: Catalog,
}

impl UsageLedger {
    pub fn load(dir: &Path) -> Self {
        let path = dir.join(LEDGER_FILE);
        let mut file: LedgerFile = fs::read(&path)
            .ok()
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
            .unwrap_or_default();
        let catalog = Catalog::load(dir);
        file.pricing.reprice(&catalog);
        Self {
            path,
            file,
            catalog,
        }
    }

    /// Record token usage from collected envelopes. Idempotent per event id,
    /// bucketed by the device-local date of `occurred_at`. Returns whether
    /// anything changed and is worth persisting.
    pub fn record(&mut self, events: &[EventEnvelope]) -> bool {
        let mut changed = false;
        for event in events {
            let tokens = event_tokens(event);
            let cost = match &event.payload {
                EventPayload::CostRecorded(payload)
                    if payload.currency.len() == 3
                        && payload.currency.bytes().all(|c| c.is_ascii_uppercase()) =>
                {
                    cost_units(&payload.amount).map(|amount| (payload.currency.clone(), amount))
                }
                _ => None,
            };
            if tokens.is_none() && cost.is_none() {
                continue;
            }
            let Some(date) = local_date(&event.occurred_at) else {
                continue;
            };
            if date > Local::now().date_naive() {
                continue;
            }
            let date_key = date.format("%Y-%m-%d").to_string();
            changed |=
                self.file
                    .pricing
                    .record(event, &date_key, tokens.unwrap_or(0), &self.catalog);
            let seen = self.file.seen.entry(date_key.clone()).or_default();
            if seen.contains(&event.event_id) {
                continue;
            }
            let day = self.file.days.entry(date_key.clone()).or_default();
            let agent_day = day.entry(event.agent_id.clone()).or_default();
            if let Some(tokens) = tokens {
                agent_day.tokens = agent_day.tokens.saturating_add(tokens);
                // Weakest accuracy wins so the panel never overstates precision.
                agent_day.accuracy = match agent_day.accuracy {
                    0 => accuracy_rank(&event.accuracy),
                    current => current.min(accuracy_rank(&event.accuracy)),
                };
            }
            if let Some((currency, amount)) = cost {
                let total = agent_day.costs.entry(currency).or_default();
                *total = total.saturating_add(amount);
            }
            seen.insert(event.event_id.clone());
            changed = true;
        }
        if changed {
            self.file.last_recorded_change_at_ms = Some(Utc::now().timestamp_millis());
        }
        changed
    }

    /// Per-agent lifetime totals plus a year of daily buckets; `None` while the agent has
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
        let recorded_days = self
            .file
            .days
            .iter()
            .filter(|(_, agents)| agents.get(agent_id).is_some_and(|d| !d.costs.is_empty()))
            .map(|(d, _)| d.clone())
            .collect();
        let priced_days = self.file.pricing.days(agent_id, &recorded_days);
        let mut pricing = CostCoverage::default();
        for (date, day) in &priced_days {
            if date.as_str() <= today.format("%Y-%m-%d").to_string().as_str() {
                pricing.add(day);
            }
        }
        let mut week = Vec::with_capacity(DISPLAY_DAYS as usize);
        let mut total = 0u64;
        let mut accuracy = u8::MAX;
        let mut total_costs = BTreeMap::<String, u64>::new();
        let mut history_start = String::new();
        for (date, agents) in &self.file.days {
            if date.as_str() > today.format("%Y-%m-%d").to_string().as_str() {
                continue;
            }
            if let Some(day) = agents.get(agent_id) {
                if history_start.is_empty() {
                    history_start = date.clone();
                }
                total = total.saturating_add(day.tokens);
                if day.accuracy > 0 {
                    accuracy = accuracy.min(day.accuracy);
                }
                for (currency, amount) in &day.costs {
                    let sum = total_costs.entry(currency.clone()).or_default();
                    *sum = sum.saturating_add(*amount);
                }
            }
        }
        for offset in (0..DISPLAY_DAYS).rev() {
            let date = (today - chrono::Duration::days(offset))
                .format("%Y-%m-%d")
                .to_string();
            let day = self
                .file
                .days
                .get(&date)
                .and_then(|agents| agents.get(agent_id));
            let tokens = day.map_or(0, |day| day.tokens);
            let costs = day.map_or_else(BTreeMap::new, |day| day.costs.clone());
            week.push(DayUsage {
                pricing: priced_days.get(&date).cloned().unwrap_or_default(),
                date,
                tokens,
                costs,
            });
        }
        Some(AgentUsageSnapshot {
            today_tokens: week.last().map_or(0, |day| day.tokens),
            total_tokens: total,
            accuracy: accuracy_name(if accuracy == u8::MAX { 0 } else { accuracy }).into(),
            daily_usage: week,
            total_costs,
            pricing,
            history_start,
        })
    }

    pub fn last_recorded_change_at_ms(&self) -> Option<i64> {
        self.file.last_recorded_change_at_ms
    }

    fn has_known_token_records(&self, agent_id: &str) -> bool {
        self.file
            .days
            .values()
            .any(|day| day.get(agent_id).is_some_and(|item| item.accuracy > 0))
    }

    /// Today tokens for an agent with known coverage. `None` if the agent has
    /// never produced a usage event; `Some(0)` if it has, but not on `today`.
    /// Cost-only rows keep accuracy unknown and are not a real zero.
    pub fn known_today_tokens(&self, agent_id: &str, today: NaiveDate) -> Option<u64> {
        if !self.has_known_token_records(agent_id) {
            return None;
        }
        let date = today.format("%Y-%m-%d").to_string();
        Some(
            self.file
                .days
                .get(&date)
                .and_then(|day| day.get(agent_id))
                .map(|day| day.tokens)
                .unwrap_or(0),
        )
    }

    pub fn today_summary(&self, today: NaiveDate, agent_ids: &[&str]) -> LedgerTodaySummary {
        let mut total = 0u128;
        let mut known_agent_ids = Vec::new();
        for id in agent_ids {
            if let Some(tokens) = self.known_today_tokens(id, today) {
                total = total.saturating_add(u128::from(tokens));
                known_agent_ids.push((*id).to_string());
            }
        }
        LedgerTodaySummary {
            today_tokens: if known_agent_ids.is_empty() {
                None
            } else {
                Some(total)
            },
            known_source_count: known_agent_ids.len(),
            known_agent_ids,
            last_recorded_change_at_ms: self.file.last_recorded_change_at_ms,
        }
    }

    /// Enrich only events already present in local totals. Historical replay
    /// must not silently expand the token accounting period or coverage.
    pub fn record_pricing(&mut self, events: &[EventEnvelope]) -> bool {
        let mut changed = false;
        for event in events {
            let Some(date) =
                local_date(&event.occurred_at).map(|d| d.format("%Y-%m-%d").to_string())
            else {
                continue;
            };
            if !self
                .file
                .seen
                .get(&date)
                .is_some_and(|seen| seen.contains(&event.event_id))
            {
                continue;
            }
            changed |= self.file.pricing.record(
                event,
                &date,
                event_tokens(event).unwrap_or(0),
                &self.catalog,
            );
        }
        changed
    }
    pub fn backfilled(&self) -> bool {
        self.file.pricing.backfilled
    }
    pub fn finish_backfill(&mut self) {
        self.file.pricing.backfilled = true;
    }
    pub fn apply_prices(&mut self, catalog: Catalog) -> Result<(), String> {
        self.file.pricing.reprice(&catalog);
        self.catalog = catalog;
        self.save()?;
        std::fs::write(
            self.path.with_file_name("openrouter-prices.json"),
            serde_json::to_vec(&self.catalog).map_err(|e| e.to_string())?,
        )
        .map_err(|e| e.to_string())
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

    fn envelope(
        agent_id: &str,
        occurred_at: &str,
        total: u64,
        accuracy: Accuracy,
    ) -> EventEnvelope {
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
        assert!(
            !ledger.record(&[event]),
            "same event id must not double count"
        );
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
        let snapshot = ledger
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
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
        let snapshot = ledger
            .agent_usage("cursor", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.today_tokens, 22, "10 + 5 + 7");
    }

    #[test]
    fn retains_history_beyond_the_display_window_and_dedupes_it() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let old = (Local::now().date_naive() - chrono::Duration::days(400))
            .and_hms_opt(12, 0, 0)
            .unwrap()
            .and_local_timezone(Local)
            .unwrap()
            .to_rfc3339();
        let event = envelope("codex", &old, 500, Accuracy::Exact);
        assert!(ledger.record(&[event.clone()]));
        assert!(!ledger.record(&[event]));
        let snapshot = ledger
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.total_tokens, 500);
        assert_eq!(
            snapshot
                .daily_usage
                .iter()
                .map(|day| day.tokens)
                .sum::<u64>(),
            0
        );
        assert_eq!(snapshot.accuracy, "exact");
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

    #[test]
    fn costs_use_fixed_precision_and_preserve_currency_after_reload() {
        let dir = tempfile::tempdir().unwrap();
        let mut ledger = UsageLedger::load(dir.path());
        let mut event = envelope("codex", &today_utc_noon(0), 0, Accuracy::Exact);
        event.payload = EventPayload::CostRecorded(protocol::CostRecordedPayload {
            amount: "1.23456789".into(),
            currency: "USD".into(),
            source: protocol::CostSource::ProviderReported,
            discount_amount: None,
        });
        assert!(ledger.record(&[event.clone()]));
        ledger.save().unwrap();
        let mut ledger = UsageLedger::load(dir.path());
        assert!(!ledger.record(&[event]));
        let snapshot = ledger
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.total_costs["USD"], 123456789);
        assert_eq!(snapshot.daily_usage.last().unwrap().costs["USD"], 123456789);
        assert_eq!(
            snapshot.accuracy, "unknown",
            "cost alone does not establish token coverage"
        );
        assert_eq!(cost_units("0"), Some(0));
        assert_eq!(cost_units("-1"), None);
        assert_eq!(cost_units("NaN"), None);
    }

    #[test]
    fn legacy_eight_day_ledger_migrates_without_losing_totals() {
        let dir = tempfile::tempdir().unwrap();
        let date = Local::now().format("%Y-%m-%d").to_string();
        let old = serde_json::json!({"days": {date.clone(): {"codex": {"tokens": 90, "accuracy": 4}}}, "seen": {date: ["old-id"]}});
        fs::write(
            dir.path().join(LEDGER_FILE),
            serde_json::to_vec(&old).unwrap(),
        )
        .unwrap();
        let ledger = UsageLedger::load(dir.path());
        let snapshot = ledger
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.total_tokens, 90);
        assert!(snapshot.total_costs.is_empty());
        assert_eq!(snapshot.daily_usage.len(), 366);
    }
}
