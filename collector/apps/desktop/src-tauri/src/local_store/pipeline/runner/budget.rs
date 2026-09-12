//! Read / discovery budgets for bounded source I/O.

use std::time::Duration;

/// Suggested global acquisition pool size.
pub const DEFAULT_GLOBAL_ACQUISITION_CONCURRENCY: usize = 4;
/// Max concurrent leases per harness.
pub const DEFAULT_PER_HARNESS_CONCURRENCY: usize = 2;
/// Default pending-row revisit cap for running→completed streams.
pub const DEFAULT_PENDING_SET_LIMIT: usize = 4096;

#[derive(Debug, Clone, Copy)]
pub struct ReadBudget {
    pub max_records: usize,
    pub max_bytes: usize,
    pub max_duration: Duration,
}

impl ReadBudget {
    pub const fn new(max_records: usize, max_bytes: usize, max_duration_ms: u64) -> Self {
        Self {
            max_records,
            max_bytes,
            max_duration: Duration::from_millis(max_duration_ms),
        }
    }

    pub fn exhausted(&self, records: usize, bytes: usize, elapsed: Duration) -> bool {
        records >= self.max_records || bytes >= self.max_bytes || elapsed >= self.max_duration
    }
}

/// Default single-source read budget: 256 records / 1 MiB / 50 ms.
pub const DEFAULT_READ_BUDGET: ReadBudget = ReadBudget::new(256, 1024 * 1024, 50);

#[derive(Debug, Clone)]
pub struct DiscoveryBudget {
    pub max_sources: usize,
    pub max_duration: Duration,
    /// Lexicographic resume cursor (last path from prior discover tick).
    pub resume_after: Option<String>,
}

impl DiscoveryBudget {
    pub const fn new(max_sources: usize, max_duration_ms: u64) -> Self {
        Self {
            max_sources,
            max_duration: Duration::from_millis(max_duration_ms),
            resume_after: None,
        }
    }

    pub fn with_resume(mut self, resume_after: Option<String>) -> Self {
        self.resume_after = resume_after;
        self
    }
}

impl Default for DiscoveryBudget {
    fn default() -> Self {
        Self::new(64, 200)
    }
}
