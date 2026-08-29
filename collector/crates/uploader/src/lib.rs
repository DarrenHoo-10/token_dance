//! Reliable ingest uploader. Reads unacked WAL events only.

#![forbid(unsafe_code)]

mod batch;
mod client;
mod error;
mod retry;
mod transport;

pub use batch::{BatchLimits, DEFAULT_MAX_BATCH_BYTES, DEFAULT_MAX_BATCH_EVENTS};
pub use client::{FlushReport, Uploader};
pub use error::{TransportError, UploadError};
pub use retry::RetryPolicy;
pub use transport::{
    body_contains_canary, HttpTransport, IngestTransport, MemoryIngest, ScriptStep,
    ScriptedTransport,
};

#[cfg(test)]
mod tests;
