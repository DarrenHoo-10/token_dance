use async_trait::async_trait;

use crate::capability::CapabilityReport;
use crate::error::AdapterError;
use crate::frame::RawFrame;
use crate::health::AdapterHealth;
use crate::setup::SetupPlan;
use crate::source::SourceSpec;
use crate::NormalizedEvent;
use protocol::AdapterManifest;

/// Read-only probe input. Must not include secrets or writable handles.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ProbeContext {
    pub installation_id: String,
}

/// Setup-plan input. Adapters must not write files from this context.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SetupContext {
    pub installation_id: String,
}

/// Source discovery input. Returned specs are filtered by manifest permissions.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SourceContext {
    pub installation_id: String,
}

/// Result of a read-only, repeatable probe.
#[derive(Debug, Clone, PartialEq)]
pub struct ProbeReport {
    pub detected: bool,
    pub agent_version: Option<String>,
    pub needs_permission: bool,
    pub needs_setup: bool,
    pub capability: CapabilityReport,
    pub detail: Option<String>,
}

/// Unified in-process Adapter contract. Core never special-cases product names.
#[async_trait]
pub trait AgentAdapter: Send + Sync {
    fn manifest(&self) -> &AdapterManifest;

    async fn probe(&self, ctx: ProbeContext) -> Result<ProbeReport, AdapterError>;

    async fn setup_plan(&self, ctx: SetupContext) -> Result<SetupPlan, AdapterError>;

    async fn discover_sources(&self, ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError>;

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError>;

    async fn health(&self) -> AdapterHealth;
}
