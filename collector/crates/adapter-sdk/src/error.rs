use std::fmt;

/// Stable error codes returned across Adapter SDK, host, and Core.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorCode {
    ProtocolIncompatible,
    ManifestInvalid,
    ManifestPermissionDenied,
    DuplicateAdapter,
    AdapterNotFound,
    AdapterDisabled,
    AdapterPanic,
    AdapterCircuitOpen,
    AdapterTimeout,
    ProbeFailed,
    SetupFailed,
    SourceDiscoveryFailed,
    SourcePermissionDenied,
    DecodeFailed,
    PrivacyRejected,
}

impl ErrorCode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ProtocolIncompatible => "PROTOCOL_INCOMPATIBLE",
            Self::ManifestInvalid => "MANIFEST_INVALID",
            Self::ManifestPermissionDenied => "MANIFEST_PERMISSION_DENIED",
            Self::DuplicateAdapter => "DUPLICATE_ADAPTER",
            Self::AdapterNotFound => "ADAPTER_NOT_FOUND",
            Self::AdapterDisabled => "ADAPTER_DISABLED",
            Self::AdapterPanic => "ADAPTER_PANIC",
            Self::AdapterCircuitOpen => "ADAPTER_CIRCUIT_OPEN",
            Self::AdapterTimeout => "ADAPTER_TIMEOUT",
            Self::ProbeFailed => "PROBE_FAILED",
            Self::SetupFailed => "SETUP_FAILED",
            Self::SourceDiscoveryFailed => "SOURCE_DISCOVERY_FAILED",
            Self::SourcePermissionDenied => "SOURCE_PERMISSION_DENIED",
            Self::DecodeFailed => "DECODE_FAILED",
            Self::PrivacyRejected => "PRIVACY_REJECTED",
        }
    }
}

impl fmt::Display for ErrorCode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Recoverable Adapter failure. Host maps panics onto `ErrorCode::AdapterPanic`.
#[derive(Debug, Clone, thiserror::Error, PartialEq, Eq)]
#[error("{code}: {message}")]
pub struct AdapterError {
    pub code: ErrorCode,
    pub message: String,
}

impl AdapterError {
    pub fn new(code: ErrorCode, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }

    pub fn protocol_incompatible(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::ProtocolIncompatible, message)
    }

    pub fn manifest_invalid(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::ManifestInvalid, message)
    }

    pub fn manifest_permission_denied(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::ManifestPermissionDenied, message)
    }

    pub fn decode_failed(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::DecodeFailed, message)
    }

    pub fn probe_failed(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::ProbeFailed, message)
    }

    pub fn duplicate_adapter(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::DuplicateAdapter, message)
    }

    pub fn adapter_not_found(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::AdapterNotFound, message)
    }

    pub fn adapter_panic(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::AdapterPanic, message)
    }

    pub fn circuit_open(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::AdapterCircuitOpen, message)
    }

    pub fn setup_failed(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::SetupFailed, message)
    }

    pub fn source_discovery_failed(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::SourceDiscoveryFailed, message)
    }
}
