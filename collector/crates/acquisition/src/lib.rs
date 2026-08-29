//! Source acquisition: JSONL tail, rotation, truncation, and rescan.

#![forbid(unsafe_code)]

mod error;
mod identity;
mod jsonl;
mod log;
mod pipeline;

pub use error::AcquisitionError;
pub use jsonl::{JsonlTailer, PollResult, MAX_LINE_BYTES};
pub use log::SafeLog;
pub use pipeline::IngestPipeline;

#[cfg(test)]
mod tests;
