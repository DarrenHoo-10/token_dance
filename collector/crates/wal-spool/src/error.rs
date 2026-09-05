use std::io;
use std::path::PathBuf;

/// Durable WAL/spool failures. Missing keys never degrade to plaintext.
#[derive(Debug, thiserror::Error)]
pub enum WalError {
    #[error("device data key is unavailable; collection paused")]
    KeyUnavailable { reason: String },
    #[error("WAL directory error at {path}: {source}")]
    Io {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("WAL codec error: {0}")]
    Codec(String),
    #[error("WAL payload decode error: {0}")]
    Payload(String),
    #[error("AEAD encrypt/decrypt failed")]
    Crypto,
    #[error("injected crash at durable WAL boundary")]
    InjectedCrash,
    #[error("hard spool backpressure; historical scan is paused")]
    HardBackpressure,
    #[error("frame exceeds max payload size")]
    FrameTooLarge,
    #[error("snapshot is corrupt")]
    SnapshotCorrupt,
    #[error("plaintext WAL frames are not permitted")]
    PlaintextForbidden,
}

impl WalError {
    pub fn io(path: impl Into<PathBuf>, source: io::Error) -> Self {
        Self::Io {
            path: path.into(),
            source,
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum KeyError {
    #[error("device data key is unavailable: {0}")]
    Unavailable(String),
    #[error("device data key is invalid")]
    Invalid,
}
