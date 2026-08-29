use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "SCREAMING_SNAKE_CASE")]
pub enum AdapterHealth {
    Healthy,
    Degraded { reason: String },
    Recoverable { reason: String },
    Permission { reason: String },
    Incompatible { reason: String },
    PermanentlyDisabled { reason: String },
}

impl AdapterHealth {
    pub fn is_healthy(&self) -> bool {
        matches!(self, Self::Healthy)
    }
}
