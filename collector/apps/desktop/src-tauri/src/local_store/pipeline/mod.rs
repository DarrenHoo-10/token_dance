//! Client event-pipeline v3 store: 10-table SQLite schema, single writer,
<<<<<<< HEAD
//! task leases, and 14-day hard TTL (P1), plus public acquisition runner (P2).
=======
//! task leases, 14-day hard TTL (P1), and local metrics/query (P4).
>>>>>>> origin/cursor/p4-local-metrics-query-e84a

mod apply;
mod buckets;
mod query;
mod schema;
mod store;
mod types;
mod writer;
pub mod runner;
pub mod adapters;

#[cfg(test)]
mod tests;
#[cfg(test)]
mod metrics_tests;

pub use buckets::{bucket_start, beijing_day_start, Grain};
pub use query::{
    query_consumer_backlog, query_skill_ranks, query_usage_summary, ConsumerBacklog, CostByCurrency,
    CoverageLevel, CoveredValue, RatioValue, SkillRankRow, UsageSummary,
};
pub use store::{DrainStats, PipelineStore};
pub use types::*;
pub use writer::{CompensationStats, PipelineWriter};
pub use runner::{
    AcquisitionRunner, DecodeOutcome, HarnessStrategy, ReadBudget, RunOutcome, DEFAULT_READ_BUDGET,
};
pub use adapters::{HarnessRegistry, SkillBook};
