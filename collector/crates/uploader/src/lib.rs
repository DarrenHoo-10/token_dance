//! Reliable ingest uploader. Reads unacked WAL events only.

#![forbid(unsafe_code)]

mod batch;
mod client;
mod error;
mod retry;
mod signer;
mod transport;
pub mod v2;

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
pub use v2::{
    canonical_request_v2, freeze_events_request, freeze_events_request_with_reconstruction,
    sha256_hex, HttpTelemetryV2, ScriptedTelemetryV2, TelemetryV2Transport, V2ScriptStep,
    V2UploadAuth, CLIENT_HTTP_TIMEOUT, CLIENT_LEASE_MS, CLIENT_LEASE_RENEW_MS,
    CLIENT_MAX_BATCH_BYTES, CLIENT_MAX_BATCH_EVENTS, CLIENT_MAX_IN_FLIGHT,
    TELEMETRY_CAPABILITIES_PATH, TELEMETRY_EVENTS_PATH,
};

#[cfg(test)]
mod tests;
