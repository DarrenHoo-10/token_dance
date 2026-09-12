//! Client event-pipeline v3 store: 10-table SQLite schema, single writer,
//! task leases, 14-day hard TTL (P1), public acquisition runner (P2),
//! harness adapters (P3), local metrics/query (P4), and upload support (P7).

pub mod adapters;
mod apply;
mod buckets;
mod content_hash;
mod flags;
mod query;
pub mod reconstruction;
mod rollout;
pub mod runner;
pub mod runtime;
mod schema;
mod store;
mod types;
mod writer;

#[cfg(test)]
mod metrics_tests;
#[cfg(test)]
mod tests;

pub use adapters::{HarnessRegistry, SkillBook};
pub use buckets::{beijing_day_start, bucket_start, Grain};
pub use flags::event_pipeline_v2_client_enabled;
pub use query::{
    query_consumer_backlog, query_harness_token_series, query_skill_ranks, query_usage_summary,
    ConsumerBacklog, CostByCurrency, CoverageLevel, CoveredValue, RatioValue, SkillRankRow,
    UsageSummary,
};
pub use rollout::{
    ensure_rollout, workers_allowed, RolloutPhase, RolloutStatus, CLOSED_BETA_GENERATION,
};
pub use runner::{
    AcquisitionRunner, DecodeOutcome, HarnessStrategy, ReadBudget, RunOutcome, DEFAULT_READ_BUDGET,
};
pub use runtime::{PipelineRuntime, PipelineTickStats};
pub use store::{DrainStats, PipelineStore};
pub use types::*;
pub use writer::{CompensationStats, PipelineWriter};
