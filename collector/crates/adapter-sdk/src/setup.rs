use serde::{Deserialize, Serialize};
use serde_json::Value;

/// Declarative configuration plan. Adapters never apply it themselves.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SetupPlan {
    pub plan_id: String,
    pub adapter_id: String,
    pub summary: String,
    pub mutations: Vec<ConfigMutation>,
    pub required_permissions: Vec<PermissionRequest>,
    pub verify: Vec<VerifyStep>,
    pub rollback: Vec<RollbackStep>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "camelCase")]
pub enum ConfigMutation {
    JsonMergePatch {
        path_template: String,
        patch: Value,
    },
    TomlMergePatch {
        path_template: String,
        patch: Value,
    },
    EnvironmentSet {
        scope: String,
        key: String,
        value_ref: String,
    },
    DirectoryCreate {
        path_template: String,
    },
}

impl ConfigMutation {
    pub fn path_template(&self) -> Option<&str> {
        match self {
            Self::JsonMergePatch { path_template, .. }
            | Self::TomlMergePatch { path_template, .. }
            | Self::DirectoryCreate { path_template } => Some(path_template),
            Self::EnvironmentSet { .. } => None,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PermissionRequest {
    pub code: String,
    pub summary: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct VerifyStep {
    pub id: String,
    pub summary: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RollbackStep {
    pub id: String,
    pub summary: String,
}
