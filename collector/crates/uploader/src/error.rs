use std::time::Duration;

use wal_spool::WalError;

#[derive(Debug, thiserror::Error)]
pub enum UploadError {
    #[error("upload paused after authentication failure")]
    AuthPaused,
    #[error("transport: {0}")]
    Transport(#[from] TransportError),
    #[error("wal: {0}")]
    Wal(#[from] WalError),
    #[error("batch encode failed: {0}")]
    Encode(String),
}

#[derive(Debug, thiserror::Error)]
pub enum TransportError {
    #[error("network failure: {0}")]
    Network(String),
    #[error("request timed out")]
    Timeout,
    #[error("http {status}")]
    Http {
        status: u16,
        retry_after: Option<Duration>,
        body: String,
    },
    #[error("authentication failed")]
    Auth,
    #[error("response decode failed: {0}")]
    Decode(String),
}

impl TransportError {
    pub fn is_retryable(&self) -> bool {
        match self {
            Self::Network(_) | Self::Timeout => true,
            Self::Http { status, .. } => matches!(status, 429 | 500..=599),
            Self::Auth | Self::Decode(_) => false,
        }
    }

    pub fn retry_after(&self) -> Option<Duration> {
        match self {
            Self::Http { retry_after, .. } => *retry_after,
            _ => None,
        }
    }
}
