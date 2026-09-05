//! Encrypted file-backed WAL/spool for checkpoint-atomic events.

#![forbid(unsafe_code)]

mod checkpoint;
mod codec;
mod crypto;
mod error;
mod fault;
mod frame;
mod ids;
mod keys;
mod limits;
mod store;

pub use checkpoint::{
    Backpressure, CheckpointKey, DriverCheckpoint, IsolatedSegment, RescanHint, Snapshot,
    SourceCheckpoint,
};
pub use error::{KeyError, WalError};
pub use fault::{FaultHook, FaultPoint};
pub use frame::{AckPayload, DeadLetterPayload, SettingsPayload, Transaction};
pub use ids::new_prefixed_id;
pub use keys::{
    InjectedKeyProvider, KeyProvider, OsKeyProvider, ToggleKeyProvider, UnavailableKeyProvider,
};
pub use limits::{AppendClass, SpoolLimits};
pub use store::{contains_bytes, wal_files_contain_magic, CompactReport, WalStore};

#[cfg(test)]
mod tests;
