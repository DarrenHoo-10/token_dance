//! Reliable ingest uploader. Reads unacked WAL events only.

#![forbid(unsafe_code)]

mod batch;
mod client;
mod error;
mod retry;
mod signer;
mod transport;

pub use batch::{BatchLimits, DEFAULT_MAX_BATCH_BYTES, DEFAULT_MAX_BATCH_EVENTS};
pub use client::{FlushReport, Uploader};
pub use error::{TransportError, UploadError};
pub use retry::RetryPolicy;
pub use signer::{
    DeviceSigner, InMemoryDeviceSigner, OsEd25519KeyHandle, OsKeyDeviceSigner, SignerError,
};
pub use transport::{
    body_contains_canary, canonical_request, HttpTransport, IngestTransport, MemoryIngest,
    RegisteredCollector, RegistrationClient, ScriptStep, ScriptedTransport,
};

#[cfg(test)]
mod tests;
