//! Client event-pipeline v3 store: 10-table SQLite schema, single writer,
//! task leases, and 14-day hard TTL (P1).

mod schema;
mod store;
mod types;
mod writer;

#[cfg(test)]
mod tests;

pub use store::PipelineStore;
pub use types::*;
pub use writer::{CompensationStats, PipelineWriter};
