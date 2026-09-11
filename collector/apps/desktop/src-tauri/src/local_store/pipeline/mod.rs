//! Client event-pipeline v3 store: 10-table SQLite schema, single writer,
//! task leases, 14-day hard TTL (P1), public acquisition runner (P2),
//! harness adapters (P3), and local metrics/query (P4).

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
