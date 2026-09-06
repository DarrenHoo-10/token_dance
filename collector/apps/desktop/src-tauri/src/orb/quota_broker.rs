use super::model::{
    details_window_from_record, pick_initial_selection, quota_snapshot_from_record, unique_window_ids,
    window_is_fresh, DetailsWindow, OrbQuotaSnapshot, QuotaRecord, QuotaSelection, QuotaState,
    QuotaWindowRecord,
};
use std::collections::{HashMap, HashSet};

pub trait QuotaSource {
    fn agent_id(&self) -> &str;
    /// Immediate cache read. Must not block on HTTP.
    fn read_cached(&self) -> Option<QuotaRecord>;
}

#[derive(Clone, Debug)]
pub struct MemoryQuotaSource {
    pub agent_id: String,
    pub cached: Option<QuotaRecord>,
}

impl QuotaSource for MemoryQuotaSource {
    fn agent_id(&self) -> &str {
        &self.agent_id
    }

    fn read_cached(&self) -> Option<QuotaRecord> {
        self.cached.clone()
    }
}

#[derive(Debug, Default)]
pub struct QuotaBroker {
    generations: HashMap<String, u64>,
    cache: HashMap<String, QuotaRecord>,
    seen: HashSet<String>,
    selection: Option<QuotaSelection>,
}

impl QuotaBroker {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn generation(&self, agent_id: &str) -> u64 {
        self.generations.get(agent_id).copied().unwrap_or(0)
    }

    pub fn invalidate(&mut self, agent_id: &str) -> u64 {
        let generation = self.generations.entry(agent_id.to_string()).or_insert(0);
        *generation = generation.saturating_add(1);
        self.cache.remove(agent_id);
        *generation
    }

    pub fn pull_cached<S: QuotaSource>(&mut self, sources: &[S]) {
        for source in sources {
            match source.read_cached() {
                Some(record) => {
                    self.seen.insert(source.agent_id().to_string());
                    self.cache.insert(source.agent_id().to_string(), record);
                }
                None => {
                    self.cache.remove(source.agent_id());
                }
            }
        }
    }

    pub fn ingest_async(&mut self, generation: u64, record: QuotaRecord) {
        if self.generation(&record.agent_id) != generation {
            return;
        }
        self.seen.insert(record.agent_id.clone());
        self.cache.insert(record.agent_id.clone(), record);
    }

    pub fn restore_selection(&mut self, selection: Option<QuotaSelection>) {
        if selection.is_some() {
            self.selection = selection;
        }
    }

    pub fn set_selection(&mut self, selection: Option<QuotaSelection>) {
        self.selection = selection;
    }

    pub fn selection(&self) -> Option<&QuotaSelection> {
        self.selection.as_ref()
    }

    pub fn maybe_auto_select(&mut self, now_ms: i64) {
        if self.selection.is_some() {
            return;
        }
        let sources = self.fresh_sources(now_ms);
        let refs: Vec<(&str, &[QuotaWindowRecord])> = sources
            .iter()
            .map(|(id, windows)| (id.as_str(), windows.as_slice()))
            .collect();
        self.selection = pick_initial_selection(refs);
    }

    pub fn view(&self, now_ms: i64) -> OrbQuotaSnapshot {
        let Some(selection) = &self.selection else {
            return OrbQuotaSnapshot::empty(QuotaState::NotConnected);
        };
        match self.cache.get(&selection.agent_id) {
            Some(record) => quota_snapshot_from_record(record, selection, now_ms),
            None => {
                let mut snapshot = OrbQuotaSnapshot::empty(if self.seen.contains(&selection.agent_id) {
                    QuotaState::Unavailable
                } else {
                    QuotaState::Loading
                });
                snapshot.selection = Some(selection.clone());
                snapshot.agent_name = Some(super::model::agent_display_name(&selection.agent_id).to_string());
                snapshot
            }
        }
    }

    pub fn details_windows(&self, now_ms: i64) -> Vec<DetailsWindow> {
        let Some(selection) = &self.selection else {
            return Vec::new();
        };
        let Some(record) = self.cache.get(&selection.agent_id) else {
            return Vec::new();
        };
        record
            .windows
            .iter()
            .filter_map(|window| details_window_from_record(record, window, now_ms))
            .collect()
    }

    pub fn cached(&self, agent_id: &str) -> Option<&QuotaRecord> {
        self.cache.get(agent_id)
    }

    pub fn cached_records(&self) -> Vec<QuotaRecord> {
        self.cache.values().cloned().collect()
    }

    pub fn drop_missing(&mut self, present: &HashSet<String>) {
        let gone: Vec<String> = self
            .cache
            .keys()
            .filter(|id| !present.contains(*id))
            .cloned()
            .collect();
        for id in gone {
            self.invalidate(&id);
        }
    }

    fn fresh_sources(&self, now_ms: i64) -> Vec<(String, Vec<QuotaWindowRecord>)> {
        let mut sources = Vec::new();
        for (agent_id, record) in &self.cache {
            if unique_window_ids(agent_id, &record.windows).is_err() {
                continue;
            }
            let windows: Vec<_> = record
                .windows
                .iter()
                .filter(|window| window_is_fresh(record, window, now_ms))
                .cloned()
                .collect();
            if !windows.is_empty() {
                sources.push((agent_id.clone(), windows));
            }
        }
        sources
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::orb::model::{quota_visual_remaining, QuotaState};
    use std::collections::HashSet;

    fn rfc3339(ms: i64) -> String {
        chrono::DateTime::<chrono::Utc>::from_timestamp_millis(ms)
            .unwrap()
            .to_rfc3339()
    }

    fn window(key: &str, minutes: u64, used: f64, resets_at: Option<i64>) -> QuotaWindowRecord {
        QuotaWindowRecord {
            used_percent: used,
            window_minutes: minutes,
            resets_at,
            provider: None,
            label: None,
            key: Some(key.into()),
        }
    }

    fn ready(agent: &str, now_ms: i64, windows: Vec<QuotaWindowRecord>) -> QuotaRecord {
        QuotaRecord {
            agent_id: agent.into(),
            observed_at: rfc3339(now_ms),
            plan: None,
            windows,
            status: if agent == "codex" {
                None
            } else {
                Some("ready".into())
            },
        }
    }

    #[test]
    fn drop_missing_clears_logged_out_sources() {
        let now = 1_000_000_000_000i64;
        let mut broker = QuotaBroker::new();
        broker.ingest_async(0, ready("grok-build", now, vec![window("shared_week", 10080, 40.0, None)]));
        broker.ingest_async(0, ready("codex", now, vec![window("primary", 300, 10.0, None)]));
        broker.restore_selection(Some(QuotaSelection {
            agent_id: "grok-build".into(),
            window_id: "grok:shared_week".into(),
        }));
        let mut present = HashSet::new();
        present.insert("codex".into());
        broker.drop_missing(&present);
        assert!(broker.cached("grok-build").is_none());
        assert!(broker.cached("codex").is_some());
        let view = broker.view(now);
        assert_eq!(view.selection.as_ref().unwrap().agent_id, "grok-build");
        assert_eq!(view.state, QuotaState::Unavailable);
        assert_eq!(view.remaining_percent, None);
    }

    #[test]
    fn expired_generation_is_ignored() {
        let now = 1_000_000_000_000i64;
        let mut broker = QuotaBroker::new();
        let stale = ready("codex", now, vec![window("primary", 300, 10.0, None)]);
        let gen = broker.invalidate("codex");
        assert_eq!(gen, 1);
        broker.ingest_async(0, stale);
        assert!(broker.cached("codex").is_none());
        let fresh = ready("codex", now, vec![window("primary", 300, 42.0, None)]);
        broker.ingest_async(1, fresh);
        assert_eq!(broker.cached("codex").unwrap().windows[0].used_percent, 42.0);
    }

    #[test]
    fn persisted_selection_is_kept_when_source_goes_stale() {
        let now = 1_000_000_000_000i64;
        let mut broker = QuotaBroker::new();
        broker.restore_selection(Some(QuotaSelection {
            agent_id: "codex".into(),
            window_id: "codex:primary:300m".into(),
        }));
        broker.ingest_async(
            0,
            ready("codex", now - 31 * 60_000, vec![window("primary", 300, 20.0, None)]),
        );
        broker.maybe_auto_select(now);
        let view = broker.view(now);
        assert_eq!(view.selection.as_ref().unwrap().agent_id, "codex");
        assert_eq!(view.state, QuotaState::Stale);
        assert_eq!(view.remaining_percent, None);
        assert_eq!(view.last_known_remaining_percent, Some(80.0));
        assert_eq!(quota_visual_remaining(view.state, view.remaining_percent), None);
    }

    #[test]
    fn first_selection_skips_colliding_source() {
        let now = 1_000_000_000_000i64;
        let mut broker = QuotaBroker::new();
        let colliding = ready(
            "codex",
            now,
            vec![
                window("primary", 300, 10.0, None),
                QuotaWindowRecord {
                    used_percent: 20.0,
                    window_minutes: 300,
                    resets_at: None,
                    provider: None,
                    label: None,
                    key: None,
                },
            ],
        );
        let cursor = QuotaRecord {
            agent_id: "cursor".into(),
            observed_at: rfc3339(now),
            plan: None,
            windows: vec![QuotaWindowRecord {
                used_percent: 31.442,
                window_minutes: 44640,
                resets_at: None,
                provider: None,
                label: Some("auto".into()),
                key: None,
            }],
            status: Some("ready".into()),
        };
        broker.pull_cached(&[
            MemoryQuotaSource {
                agent_id: "codex".into(),
                cached: Some(colliding),
            },
            MemoryQuotaSource {
                agent_id: "cursor".into(),
                cached: Some(cursor),
            },
        ]);
        broker.maybe_auto_select(now);
        assert_eq!(
            broker.selection(),
            Some(&QuotaSelection {
                agent_id: "cursor".into(),
                window_id: "cursor:auto".into(),
            })
        );
    }

    #[test]
    fn memory_source_serves_cache_without_waiting() {
        let now = 1_000_000_000_000i64;
        let source = MemoryQuotaSource {
            agent_id: "codex".into(),
            cached: Some(ready("codex", now, vec![window("primary", 300, 28.0, None)])),
        };
        let mut broker = QuotaBroker::new();
        broker.pull_cached(&[source]);
        broker.maybe_auto_select(now);
        let view = broker.view(now);
        assert_eq!(view.state, QuotaState::Fresh);
        assert_eq!(view.remaining_percent, Some(72.0));
        assert_eq!(view.identity_confidence, crate::orb::model::IdentityConfidence::Unavailable);
    }
}
