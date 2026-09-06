use super::model::{OrbPulse, PulseKind, QuotaState};
use super::preferences::atomic_write;
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

pub const ALERT_STATE_FILE: &str = "orb-alert-state.json";
const THRESHOLDS: [u8; 2] = [20, 10];
const DAY_MS: i64 = 24 * 60 * 60 * 1000;
const LOW_QUOTA_PULSE_MS: i64 = 800;

#[derive(Clone, Debug)]
pub struct AlertInput<'a> {
    pub agent_id: &'a str,
    pub window_id: &'a str,
    pub account_scope: Option<&'a str>,
    pub reset_cycle: Option<&'a str>,
    pub remaining_percent: Option<f64>,
    pub quota_state: QuotaState,
    pub orb_visible: bool,
    pub collector_paused: bool,
    pub now_ms: i64,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
struct AlertEntry {
    agent_id: String,
    window_id: String,
    account_scope: Option<String>,
    reset_cycle: Option<String>,
    threshold: u8,
    fired_at_ms: i64,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
struct AlertFile {
    #[serde(default = "alert_schema")]
    schema_version: u32,
    #[serde(default)]
    fired: Vec<AlertEntry>,
}

fn alert_schema() -> u32 {
    1
}

struct Inner {
    path: PathBuf,
    fired: Vec<AlertEntry>,
}

pub struct AlertStore {
    inner: Mutex<Inner>,
}

impl AlertStore {
    pub fn load(dir: impl AsRef<Path>) -> Self {
        let path = dir.as_ref().join(ALERT_STATE_FILE);
        let fired = load_entries(&path);
        Self {
            inner: Mutex::new(Inner { path, fired }),
        }
    }

    pub fn consider(&self, input: &AlertInput<'_>) -> Option<OrbPulse> {
        if input.collector_paused || !input.orb_visible || input.quota_state != QuotaState::Fresh {
            return None;
        }
        let remaining = input.remaining_percent.filter(|value| value.is_finite())?;
        let crossed: Vec<u8> = THRESHOLDS
            .into_iter()
            .filter(|threshold| remaining <= f64::from(*threshold))
            .collect();
        if crossed.is_empty() {
            return None;
        }
        let mut inner = self.inner.lock().expect("orb alerts poisoned");
        let mut newly = Vec::new();
        for threshold in &crossed {
            if !already_fired(&inner.fired, input, *threshold) {
                newly.push(*threshold);
            }
        }
        if newly.is_empty() {
            return None;
        }
        let fire = *newly.iter().min().expect("newly crossed");
        for threshold in crossed {
            if already_fired(&inner.fired, input, threshold) {
                continue;
            }
            if let Some(entry) = latest_match_mut(&mut inner.fired, input, threshold) {
                entry.fired_at_ms = input.now_ms;
            } else {
                inner.fired.push(AlertEntry {
                    agent_id: input.agent_id.into(),
                    window_id: input.window_id.into(),
                    account_scope: input.account_scope.map(str::to_string),
                    reset_cycle: input.reset_cycle.map(str::to_string),
                    threshold,
                    fired_at_ms: input.now_ms,
                });
            }
        }
        let _ = persist_entries(&inner.path, &inner.fired);
        Some(OrbPulse {
            id: format!("low-quota-{fire}-{}", input.now_ms),
            kind: PulseKind::LowQuota,
            expires_at_ms: input.now_ms.saturating_add(LOW_QUOTA_PULSE_MS),
        })
    }
}

fn already_fired(entries: &[AlertEntry], input: &AlertInput<'_>, threshold: u8) -> bool {
    let Some(entry) = latest_match(entries, input, threshold) else {
        return false;
    };
    if input.reset_cycle.is_some() {
        true
    } else {
        input.now_ms.saturating_sub(entry.fired_at_ms) < DAY_MS
    }
}

fn latest_match<'a>(
    entries: &'a [AlertEntry],
    input: &AlertInput<'_>,
    threshold: u8,
) -> Option<&'a AlertEntry> {
    entries
        .iter()
        .filter(|entry| matches_key(entry, input, threshold))
        .max_by_key(|entry| entry.fired_at_ms)
}

fn latest_match_mut<'a>(
    entries: &'a mut [AlertEntry],
    input: &AlertInput<'_>,
    threshold: u8,
) -> Option<&'a mut AlertEntry> {
    let index = entries
        .iter()
        .enumerate()
        .filter(|(_, entry)| matches_key(entry, input, threshold))
        .max_by_key(|(_, entry)| entry.fired_at_ms)
        .map(|(index, _)| index)?;
    entries.get_mut(index)
}

fn matches_key(entry: &AlertEntry, input: &AlertInput<'_>, threshold: u8) -> bool {
    entry.agent_id == input.agent_id
        && entry.window_id == input.window_id
        && entry.account_scope.as_deref() == input.account_scope
        && entry.reset_cycle.as_deref() == input.reset_cycle
        && entry.threshold == threshold
}

fn load_entries(path: &Path) -> Vec<AlertEntry> {
    let Ok(bytes) = fs::read(path) else {
        return Vec::new();
    };
    match serde_json::from_slice::<AlertFile>(&bytes) {
        Ok(file) if file.schema_version == 1 => file.fired,
        _ => {
            let backup = path.with_extension("json.corrupt");
            let _ = fs::write(&backup, &bytes);
            Vec::new()
        }
    }
}

fn persist_entries(path: &Path, fired: &[AlertEntry]) -> Result<(), String> {
    let file = AlertFile {
        schema_version: 1,
        fired: fired.to_vec(),
    };
    let bytes = serde_json::to_vec_pretty(&file).map_err(|error| error.to_string())?;
    atomic_write(path, &bytes).map_err(|error| error.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fresh<'a>(remaining: f64, now_ms: i64) -> AlertInput<'a> {
        AlertInput {
            agent_id: "codex",
            window_id: "codex:primary:300m",
            account_scope: None,
            reset_cycle: Some("cycle-a"),
            remaining_percent: Some(remaining),
            quota_state: QuotaState::Fresh,
            orb_visible: true,
            collector_paused: false,
            now_ms,
        }
    }

    #[test]
    fn thresholds_and_skip_level() {
        let dir = tempfile::tempdir().unwrap();
        let store = AlertStore::load(dir.path());
        assert!(store.consider(&fresh(100.0, 1)).is_none());
        let pulse_20 = store.consider(&fresh(20.0, 2)).unwrap();
        assert!(pulse_20.id.contains("low-quota-20"));
        let pulse_10 = store.consider(&fresh(10.0, 3)).unwrap();
        assert!(pulse_10.id.contains("low-quota-10"));
        assert!(store.consider(&fresh(0.0, 4)).is_none());

        let skip_dir = tempfile::tempdir().unwrap();
        let skip = AlertStore::load(skip_dir.path());
        let jumped = skip.consider(&fresh(5.0, 5)).unwrap();
        assert!(jumped.id.contains("low-quota-10"));
        assert!(skip.consider(&fresh(15.0, 6)).is_none());
        assert!(skip.consider(&fresh(5.0, 7)).is_none());
    }

    #[test]
    fn oscillation_does_not_clear_dedup() {
        let dir = tempfile::tempdir().unwrap();
        let store = AlertStore::load(dir.path());
        assert!(store.consider(&fresh(18.0, 1)).is_some());
        assert!(store.consider(&fresh(25.0, 2)).is_none());
        assert!(store.consider(&fresh(18.0, 3)).is_none());
    }

    #[test]
    fn twenty_four_hour_dedup_without_cycle() {
        let dir = tempfile::tempdir().unwrap();
        let store = AlertStore::load(dir.path());
        let mut input = fresh(10.0, 1_000);
        input.reset_cycle = None;
        assert!(store.consider(&input).is_some());
        input.now_ms = 1_000 + DAY_MS - 1;
        assert!(store.consider(&input).is_none());
        input.now_ms = 1_000 + DAY_MS;
        assert!(store.consider(&input).is_some());
        input.now_ms = 1_000 + DAY_MS + 1;
        assert!(store.consider(&input).is_none());
        input.now_ms = 1_000 + DAY_MS * 2;
        assert!(store.consider(&input).is_some());
        input.now_ms = 1_000 + DAY_MS * 2 + 1;
        assert!(store.consider(&input).is_none());
    }

    #[test]
    fn restart_source_change_and_missing_account_scope() {
        let dir = tempfile::tempdir().unwrap();
        let store = AlertStore::load(dir.path());
        assert!(store.consider(&fresh(8.0, 10)).is_some());
        drop(store);
        let reloaded = AlertStore::load(dir.path());
        assert!(reloaded.consider(&fresh(8.0, 11)).is_none());

        let mut other = fresh(8.0, 12);
        other.agent_id = "cursor";
        other.window_id = "cursor:auto";
        assert!(reloaded.consider(&other).is_some());

        let scope_dir = tempfile::tempdir().unwrap();
        let conservative = AlertStore::load(scope_dir.path());
        let mut first = fresh(9.0, 20);
        first.account_scope = None;
        first.reset_cycle = Some("week");
        assert!(conservative.consider(&first).is_some());
        assert!(conservative.consider(&first).is_none());
    }

    #[test]
    fn error_and_stale_states_do_not_fire() {
        let dir = tempfile::tempdir().unwrap();
        let store = AlertStore::load(dir.path());
        for state in [
            QuotaState::Stale,
            QuotaState::Unavailable,
            QuotaState::Loading,
            QuotaState::NotConnected,
            QuotaState::AuthRequired,
            QuotaState::NoQuota,
            QuotaState::Unlimited,
        ] {
            let mut input = fresh(5.0, 30);
            input.quota_state = state;
            assert!(store.consider(&input).is_none(), "{state:?}");
        }
        let mut hidden = fresh(5.0, 31);
        hidden.orb_visible = false;
        assert!(store.consider(&hidden).is_none());
        let mut paused = fresh(5.0, 32);
        paused.collector_paused = true;
        assert!(store.consider(&paused).is_none());
    }
}
