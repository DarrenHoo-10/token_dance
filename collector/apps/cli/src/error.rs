//! Exit codes and machine-readable errors (plan §3.5).

use serde_json::{json, Value};

/// Failure classes. The numeric value is the process exit code.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Internal,
    InvalidInput,
    Credentials,
    Incomplete,
    InstanceInUse,
    Incompatible,
    Interrupted,
}

impl Kind {
    pub fn exit_code(self) -> u8 {
        match self {
            Self::Internal => 1,
            Self::InvalidInput => 2,
            Self::Credentials => 3,
            Self::Incomplete => 4,
            Self::InstanceInUse => 5,
            Self::Incompatible => 6,
            Self::Interrupted => 130,
        }
    }

    pub fn code(self) -> &'static str {
        match self {
            Self::Internal => "INTERNAL_ERROR",
            Self::InvalidInput => "INVALID_INPUT",
            Self::Credentials => "CREDENTIALS_UNAVAILABLE",
            Self::Incomplete => "INCOMPLETE",
            Self::InstanceInUse => "INSTANCE_IN_USE",
            Self::Incompatible => "INCOMPATIBLE",
            Self::Interrupted => "INTERRUPTED",
        }
    }
}

#[derive(Debug, Clone)]
pub struct CliError {
    pub kind: Kind,
    pub message: String,
    pub hint: Option<String>,
    /// Partial result still worth printing (for example a timed-out collection).
    pub data: Option<Value>,
}

pub type CliResult<T> = Result<T, CliError>;

impl CliError {
    pub fn new(kind: Kind, message: impl Into<String>) -> Self {
        Self {
            kind,
            message: message.into(),
            hint: None,
            data: None,
        }
    }

    pub fn invalid(message: impl Into<String>) -> Self {
        Self::new(Kind::InvalidInput, message)
    }

    pub fn internal(message: impl Into<String>) -> Self {
        Self::new(Kind::Internal, message)
    }

    pub fn hint(mut self, hint: impl Into<String>) -> Self {
        self.hint = Some(hint.into());
        self
    }

    pub fn with_data(mut self, data: Value) -> Self {
        self.data = Some(data);
        self
    }

    pub fn to_json(&self) -> Value {
        let mut error = json!({
            "code": self.kind.code(),
            "exit_code": self.kind.exit_code(),
            "message": self.message,
        });
        if let Some(hint) = &self.hint {
            error["hint"] = json!(hint);
        }
        let mut out = json!({
            "schema_version": crate::SCHEMA_VERSION,
            "ok": false,
            "error": error,
        });
        if let Some(data) = &self.data {
            out["data"] = data.clone();
        }
        out
    }
}

impl std::fmt::Display for CliError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.message)
    }
}

/// Maps a `collector_service` lock failure to a typed error. The service reports
/// contention only as a string, so this is the single place that interprets it.
pub fn lock_error(message: String) -> CliError {
    if message == "collector already running" {
        CliError::new(
            Kind::InstanceInUse,
            "another TokenDance collector (desktop app or `tokendance run`) is already running for this data directory",
        )
        .hint("stop it first; the CLI never stops another process")
    } else {
        CliError::internal(format!("cannot take the instance lock: {message}"))
    }
}
