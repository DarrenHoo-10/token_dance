//! Client event-pipeline v3 store: 10-table SQLite schema, single writer,
//! task leases, and 14-day hard TTL (P1), plus public acquisition runner (P2).

mod schema;
mod store;
mod types;
mod writer;
pub mod runner;
pub mod adapters;

#[cfg(test)]
mod tests;

pub use store::PipelineStore;
pub use types::*;
pub use writer::{CompensationStats, PipelineWriter};
pub use runner::{
    AcquisitionRunner, DecodeOutcome, HarnessStrategy, ReadBudget, RunOutcome, DEFAULT_READ_BUDGET,
};
pub use adapters::{HarnessRegistry, SkillBook};
