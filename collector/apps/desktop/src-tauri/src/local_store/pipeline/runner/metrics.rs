//! Acquisition metrics counters (bytes, ignore/conflict, checkpoint delay).

use std::sync::atomic::{AtomicU64, Ordering};

#[derive(Debug, Default)]
pub struct AcquisitionMetrics {
    pub bytes_read: AtomicU64,
    pub records_read: AtomicU64,
    pub events_emitted: AtomicU64,
    pub ignored: AtomicU64,
    pub conflicts: AtomicU64,
    pub checkpoint_commit_delay_ms_total: AtomicU64,
    pub checkpoint_commits: AtomicU64,
    pub source_changed: AtomicU64,
}

impl AcquisitionMetrics {
    pub fn record_read(&self, bytes: usize, records: usize) {
        self.bytes_read
            .fetch_add(bytes as u64, Ordering::Relaxed);
        self.records_read
            .fetch_add(records as u64, Ordering::Relaxed);
    }

    pub fn record_emit(&self, n: usize) {
        self.events_emitted.fetch_add(n as u64, Ordering::Relaxed);
    }

    pub fn record_ignore(&self, n: usize) {
        self.ignored.fetch_add(n as u64, Ordering::Relaxed);
    }

    pub fn record_conflict(&self) {
        self.conflicts.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_source_changed(&self) {
        self.source_changed.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_checkpoint_delay_ms(&self, delay_ms: u64) {
        self.checkpoint_commit_delay_ms_total
            .fetch_add(delay_ms, Ordering::Relaxed);
        self.checkpoint_commits.fetch_add(1, Ordering::Relaxed);
    }

    pub fn snapshot(&self) -> MetricsSnapshot {
        MetricsSnapshot {
            bytes_read: self.bytes_read.load(Ordering::Relaxed),
            records_read: self.records_read.load(Ordering::Relaxed),
            events_emitted: self.events_emitted.load(Ordering::Relaxed),
            ignored: self.ignored.load(Ordering::Relaxed),
            conflicts: self.conflicts.load(Ordering::Relaxed),
            checkpoint_commit_delay_ms_total: self
                .checkpoint_commit_delay_ms_total
                .load(Ordering::Relaxed),
            checkpoint_commits: self.checkpoint_commits.load(Ordering::Relaxed),
            source_changed: self.source_changed.load(Ordering::Relaxed),
        }
    }
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct MetricsSnapshot {
    pub bytes_read: u64,
    pub records_read: u64,
    pub events_emitted: u64,
    pub ignored: u64,
    pub conflicts: u64,
    pub checkpoint_commit_delay_ms_total: u64,
    pub checkpoint_commits: u64,
    pub source_changed: u64,
}
