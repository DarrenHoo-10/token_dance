//! Source acquisition: JSONL tail, rotation, truncation, and rescan.

#![forbid(unsafe_code)]

mod drivers;
mod error;
mod identity;
mod jsonl;
mod log;
mod otlp;
mod pipeline;

pub use drivers::{
    detect_zcode_sqlite, registry_snapshot, DriverBatch, DriverEntry, DriverInstance, DriverKind,
    DriverRegistry, DriverTaskStatus, RemoteApiDriver, RemotePollError, RuntimeStreamDriver,
    SecretResolver, SqliteAdapterPlan, SqliteSnapshotDriver, ZcodeSqliteDetection,
    REMOTE_API_OVERLAP,
};
pub use error::AcquisitionError;
pub use identity::file_identity;
pub use jsonl::{JsonlTailer, PollResult, MAX_LINE_BYTES};
pub use log::SafeLog;
pub use otlp::{
    OtlpReceiverDriver, OtlpSignal, DEFAULT_OTLP_PAYLOAD_LIMIT,
    DEFAULT_OTLP_RESOURCE_ATTRIBUTE_LIMIT,
};
pub use pipeline::IngestPipeline;

#[cfg(test)]
mod tests;
