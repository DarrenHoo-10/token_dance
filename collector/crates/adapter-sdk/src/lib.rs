//! Unified Adapter SDK: contracts, manifest rules, frames, and events.

#![forbid(unsafe_code)]

pub mod adapter;
pub mod capability;
pub mod error;
pub mod event;
pub mod frame;
pub mod health;
pub mod manifest;
pub mod setup;
pub mod source;

pub use adapter::{AgentAdapter, ProbeContext, ProbeReport, SetupContext, SourceContext};
pub use capability::{CapabilityLevel, CapabilityReport};
pub use error::{AdapterError, ErrorCode};
pub use event::{event_id, keyed_hmac, raw_fingerprint, NormalizedEvent};
pub use frame::RawFrame;
pub use health::AdapterHealth;
pub use manifest::{
    format_path_permission, path_permission_covers, path_template_covers, path_template_id_name,
    protocol_compatible, sample_manifest, validate_manifest, AdapterManifestExt,
    SUPPORTED_PATH_ROOTS,
};
pub use protocol::{
    Accuracy, AdapterManifest, AdapterPermissions, AdapterRuntimeStatus, AdapterStatusReport,
    AgentDescriptor, Capability, CapabilityAvailability, CapabilityStatus, ClientUploadBatchStatus,
    CommandPermission, EventDeliveryStatus, EventEnvelope, EventPayload, EventSource, EventType,
    PathAccess, PathPermission, PathTemplateId, Platform, SetupPlanStatus, SourceCheckpointStatus,
    SourceKind, TokenUsage, PROTOCOL_VERSION,
};
pub use setup::{ConfigMutation, PermissionRequest, RollbackStep, SetupPlan, VerifyStep};
pub use source::{assert_source_allowed, assert_write_path_allowed, SourceSpec};
