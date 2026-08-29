use std::io;
use std::path::PathBuf;

use wal_spool::WalError;

#[derive(Debug, thiserror::Error)]
pub enum AcquisitionError {
    #[error("source io {path}: {source}")]
    Io {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("line exceeds 4 MiB and was rejected")]
    LineTooLarge,
    #[error(transparent)]
    Wal(#[from] WalError),
    #[error("{0}")]
    Other(String),
}

impl AcquisitionError {
    pub fn io(path: impl Into<PathBuf>, source: io::Error) -> Self {
        Self::Io {
            path: path.into(),
            source,
        }
    }
}
