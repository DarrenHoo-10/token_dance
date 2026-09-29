//! Bounded acquisition concurrency: global / per-harness / per-stream.

use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};

use super::budget::{
    DEFAULT_GLOBAL_ACQUISITION_CONCURRENCY, DEFAULT_PER_HARNESS_CONCURRENCY,
};

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct StreamKey {
    harness_id: String,
    source_id: i64,
}

/// Fair-ish lease gate. Per-stream concurrency is always 1.
#[derive(Debug, Clone)]
pub struct AcquisitionScheduler {
    inner: Arc<Mutex<SchedulerInner>>,
}

#[derive(Debug)]
struct SchedulerInner {
    global_limit: usize,
    per_harness_limit: usize,
    active_global: usize,
    active_harness: HashMap<String, usize>,
    active_streams: HashSet<StreamKey>,
}

pub struct SchedulerPermit {
    scheduler: AcquisitionScheduler,
    harness_id: String,
    source_id: i64,
}

impl AcquisitionScheduler {
    pub fn new(global_limit: usize, per_harness_limit: usize) -> Self {
        Self {
            inner: Arc::new(Mutex::new(SchedulerInner {
                global_limit: global_limit.max(1),
                per_harness_limit: per_harness_limit.max(1),
                active_global: 0,
                active_harness: HashMap::new(),
                active_streams: HashSet::new(),
            })),
        }
    }

    pub fn with_defaults() -> Self {
        Self::new(
            DEFAULT_GLOBAL_ACQUISITION_CONCURRENCY,
            DEFAULT_PER_HARNESS_CONCURRENCY,
        )
    }

    /// Try to acquire a permit for one source stream. Returns None when saturated.
    pub fn try_acquire(&self, harness_id: &str, source_id: i64) -> Option<SchedulerPermit> {
        let mut g = self.inner.lock().ok()?;
        let stream = StreamKey {
            harness_id: harness_id.to_string(),
            source_id,
        };
        if g.active_streams.contains(&stream) {
            return None;
        }
        if g.active_global >= g.global_limit {
            return None;
        }
        let harness_count = g.active_harness.get(harness_id).copied().unwrap_or(0);
        if harness_count >= g.per_harness_limit {
            return None;
        }
        g.active_global += 1;
        *g.active_harness
            .entry(harness_id.to_string())
            .or_insert(0) += 1;
        g.active_streams.insert(stream);
        Some(SchedulerPermit {
            scheduler: self.clone(),
            harness_id: harness_id.to_string(),
            source_id,
        })
    }

    pub fn active_counts(&self) -> (usize, HashMap<String, usize>) {
        let g = self.inner.lock().expect("scheduler");
        (g.active_global, g.active_harness.clone())
    }
}

impl Drop for SchedulerPermit {
    fn drop(&mut self) {
        if let Ok(mut g) = self.scheduler.inner.lock() {
            let stream = StreamKey {
                harness_id: self.harness_id.clone(),
                source_id: self.source_id,
            };
            g.active_streams.remove(&stream);
            g.active_global = g.active_global.saturating_sub(1);
            if let Some(count) = g.active_harness.get_mut(&self.harness_id) {
                *count = count.saturating_sub(1);
                if *count == 0 {
                    g.active_harness.remove(&self.harness_id);
                }
            }
        }
    }
}
