use super::model::{TodaySourceTotal, UsageState, UsageSummary};
use crate::usage_ledger::UsageLedger;
use chrono::NaiveDate;

pub trait TodayUsage {
    fn known_today_tokens(&self, agent_id: &str, today: NaiveDate) -> Option<u64>;
    fn last_recorded_change_at_ms(&self) -> Option<i64>;
}

impl TodayUsage for UsageLedger {
    fn known_today_tokens(&self, id: &str, today: NaiveDate) -> Option<u64> {
        self.known_today_tokens(id, today)
    }
    fn last_recorded_change_at_ms(&self) -> Option<i64> {
        self.last_recorded_change_at_ms()
    }
}

impl TodayUsage for crate::local_store::LocalStore {
    fn known_today_tokens(&self, id: &str, today: NaiveDate) -> Option<u64> {
        self.known_today_tokens(id, today)
    }
    fn last_recorded_change_at_ms(&self) -> Option<i64> {
        self.last_recorded_change_at_ms()
    }
}

pub const CATALOG_AGENTS: &[(&str, &str)] = &[
    ("codex", "Codex"),
    ("claude-code", "Claude Code"),
    ("grok-build", "Grok Build"),
    ("cursor", "Cursor"),
    ("zcode", "ZCode"),
    ("pi", "Pi"),
    ("deepseek-harness", "DeepSeek Harness"),
];

pub fn from_ledger(
    ledger: &impl TodayUsage,
    local_date: NaiveDate,
    catalog_ids: &[&str],
    captured_at_ms: i64,
) -> UsageSummary {
    let totals: Vec<u64> = catalog_ids
        .iter()
        .filter_map(|id| ledger.known_today_tokens(id, local_date))
        .collect();
    let known = totals.len() as u32;
    let has_unmeasured_sources = totals.len() < catalog_ids.len();
    let (state, today_tokens) = if totals.is_empty() {
        (UsageState::Unknown, None)
    } else {
        (
            UsageState::Known,
            Some(
                totals
                    .iter()
                    .map(|value| u128::from(*value))
                    .sum::<u128>()
                    .to_string(),
            ),
        )
    };
    UsageSummary {
        local_date: local_date.format("%Y-%m-%d").to_string(),
        state,
        today_tokens,
        known_source_count: known,
        has_unmeasured_sources,
        captured_at_ms,
        last_recorded_change_at_ms: ledger.last_recorded_change_at_ms(),
    }
}

pub fn today_source_totals(
    ledger: &impl TodayUsage,
    local_date: NaiveDate,
    agents: &[(&str, &str)],
) -> Vec<TodaySourceTotal> {
    agents
        .iter()
        .map(|(id, name)| TodaySourceTotal {
            agent_id: (*id).to_string(),
            agent_name: (*name).to_string(),
            today_tokens: ledger
                .known_today_tokens(id, local_date)
                .map(|tokens| u128::from(tokens).to_string()),
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Local;
    use protocol::{
        Accuracy, CostRecordedPayload, CostSource, EventEnvelope, EventPayload, EventSource,
        ModelUsageRecordedPayload, SourceKind, TokenUsage,
    };
    use std::path::Path;

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
            payload: EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
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

    fn noon(date: NaiveDate) -> String {
        date.and_hms_opt(12, 0, 0)
            .unwrap()
            .and_local_timezone(Local)
            .unwrap()
            .to_rfc3339()
    }

    #[test]
    fn known_zero_is_not_unknown() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let today = NaiveDate::from_ymd_opt(2026, 9, 6).unwrap();
        ledger.record(&[envelope("codex", &noon(today), 0, Accuracy::Exact)]);
        let summary = from_ledger(&ledger, today, &["codex", "cursor"], 1);
        assert_eq!(summary.state, UsageState::Known);
        assert_eq!(summary.today_tokens.as_deref(), Some("0"));
        assert_eq!(summary.known_source_count, 1);
        assert!(summary.has_unmeasured_sources);
        assert_eq!(summary.local_date, "2026-09-06");
    }

    #[test]
    fn sqlite_rollups_preserve_zero_unknown_large_totals_and_restart() {
        let root = tempfile::tempdir().unwrap();
        let today = Local::now().date_naive();
        let mut store = crate::local_store::LocalStore::open(root.path()).unwrap();
        let mut cost = envelope("zcode", &noon(today), 0, Accuracy::Exact);
        cost.payload = EventPayload::CostRecorded(CostRecordedPayload {
            amount: "1.00".into(),
            currency: "USD".into(),
            source: CostSource::ProviderReported,
            discount_amount: None,
        });
        let events = [
            envelope("codex", &noon(today), 0, Accuracy::Exact),
            envelope(
                "cursor",
                &noon(today),
                9_007_199_254_740_993,
                Accuracy::Exact,
            ),
            cost,
        ];
        assert!(store.commit_batch(&events, &[]).unwrap());
        let recorded_at = store.last_recorded_change_at_ms();
        assert!(recorded_at.is_some());
        assert!(!store.commit_batch(&events, &[]).unwrap());
        assert_eq!(store.last_recorded_change_at_ms(), recorded_at);
        drop(store);
        let store = crate::local_store::LocalStore::open(root.path()).unwrap();
        assert_eq!(store.known_today_tokens("codex", today), Some(0));
        assert_eq!(store.known_today_tokens("zcode", today), None);
        assert_eq!(store.known_today_tokens("grok-build", today), None);
        assert_eq!(
            store.known_today_tokens("cursor", today + chrono::Duration::days(1)),
            Some(0)
        );
        let summary = from_ledger(&store, today, &["codex", "cursor", "zcode"], 1);
        assert_eq!(summary.today_tokens.as_deref(), Some("9007199254740993"));
        assert_eq!(summary.known_source_count, 2);
        assert!(summary.has_unmeasured_sources);
        assert_eq!(summary.last_recorded_change_at_ms, None);
        assert!(!root.path().join("usage-ledger.json").exists());
    }

    #[test]
    fn unknown_is_not_a_real_zero() {
        let ledger = UsageLedger::load(Path::new("/nonexistent"));
        let today = NaiveDate::from_ymd_opt(2026, 9, 6).unwrap();
        let summary = from_ledger(&ledger, today, &["codex"], 1);
        assert_eq!(summary.state, UsageState::Unknown);
        assert_eq!(summary.today_tokens, None);
        assert_eq!(summary.known_source_count, 0);
        assert!(summary.has_unmeasured_sources);

        let mut cost_only = UsageLedger::load(Path::new("/nonexistent"));
        let mut event = envelope("codex", &noon(today), 0, Accuracy::Exact);
        event.payload = EventPayload::CostRecorded(CostRecordedPayload {
            amount: "1.00".into(),
            currency: "USD".into(),
            source: CostSource::ProviderReported,
            discount_amount: None,
        });
        assert!(cost_only.record(&[event]));
        let snapshot = cost_only.agent_usage("codex", today).unwrap();
        assert_eq!(snapshot.accuracy, "unknown");
        assert_eq!(snapshot.today_tokens, 0);
        let summary = from_ledger(&cost_only, today, &["codex"], 1);
        assert_eq!(summary.state, UsageState::Unknown);
        assert_eq!(summary.today_tokens, None);
    }

    #[test]
    fn cross_day_and_timezone_resample_use_naive_local_date() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let occurred = "2026-09-06T02:30:00Z";
        ledger.record(&[envelope("codex", occurred, 11, Accuracy::Exact)]);
        let local = chrono::DateTime::parse_from_rfc3339(occurred)
            .unwrap()
            .with_timezone(&Local)
            .date_naive();
        let summary = from_ledger(&ledger, local, &["codex"], 1);
        assert_eq!(summary.local_date, local.format("%Y-%m-%d").to_string());
        assert_eq!(summary.today_tokens.as_deref(), Some("11"));
        let neighbor = local + chrono::Duration::days(1);
        let other = from_ledger(&ledger, neighbor, &["codex"], 1);
        assert_eq!(other.local_date, neighbor.format("%Y-%m-%d").to_string());
        assert_eq!(other.today_tokens.as_deref(), Some("0"));
        assert_ne!(summary.local_date, other.local_date);
    }

    #[test]
    fn large_integer_sum_is_decimal_string() {
        let mut ledger = UsageLedger::load(Path::new("/nonexistent"));
        let today = NaiveDate::from_ymd_opt(2026, 9, 6).unwrap();
        let a = 9_007_199_254_740_993u64;
        let b = 9_007_199_254_740_993u64;
        ledger.record(&[
            envelope("codex", &noon(today), a, Accuracy::Exact),
            envelope("cursor", &noon(today), b, Accuracy::Derived),
        ]);
        let summary = from_ledger(&ledger, today, &["codex", "cursor"], 1);
        assert_eq!(summary.today_tokens.as_deref(), Some("18014398509481986"));
        assert_ne!(summary.today_tokens.as_deref(), Some("18014398509481984"));
        let totals =
            today_source_totals(&ledger, today, &[("codex", "Codex"), ("cursor", "Cursor")]);
        assert_eq!(totals[0].today_tokens.as_deref(), Some("9007199254740993"));
        assert_eq!(totals[1].today_tokens.as_deref(), Some("9007199254740993"));
    }
}
