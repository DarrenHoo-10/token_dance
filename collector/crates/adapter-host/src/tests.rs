use std::sync::Arc;

use adapter_sdk::{
    sample_manifest, AdapterError, AdapterHealth, AdapterManifest, AgentAdapter, CapabilityReport,
    ConfigMutation, ErrorCode, ProbeContext, ProbeReport, RawFrame, RollbackStep, SetupContext,
    SetupPlan, SourceContext, SourceSpec, VerifyStep,
};
use async_trait::async_trait;

use crate::AdapterHost;

struct StubAdapter {
    manifest: AdapterManifest,
    detected: bool,
    panic_on_probe: bool,
    panic_on_decode: bool,
    fail_probe: bool,
    overreach_source: bool,
}

impl StubAdapter {
    fn new(id: &str) -> Self {
        let mut manifest = sample_manifest(id);
        manifest.name = id.into();
        Self {
            manifest,
            detected: true,
            panic_on_probe: false,
            panic_on_decode: false,
            fail_probe: false,
            overreach_source: false,
        }
    }
}

#[async_trait]
impl AgentAdapter for StubAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        if self.panic_on_probe {
            panic!("stub probe panic");
        }
        if self.fail_probe {
            return Err(AdapterError::probe_failed("forced probe failure"));
        }
        let available = if self.detected {
            self.manifest.capabilities.clone()
        } else {
            Vec::new()
        };
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: Some("1.0.0".into()),
            needs_permission: false,
            needs_setup: false,
            capability: CapabilityReport::from_declared(
                &self.manifest.id,
                &self.manifest.version,
                &self.manifest.capabilities,
                &available,
            ),
            detail: Some(ctx.installation_id),
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "plan-1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "enable collection".into(),
            mutations: vec![ConfigMutation::JsonMergePatch {
                path_template: "${AGENT_CONFIG_HOME}/config.json".into(),
                patch: serde_json::json!({"telemetry": true}),
            }],
            required_permissions: vec![],
            verify: vec![VerifyStep {
                id: "syntax".into(),
                summary: "config parses".into(),
            }],
            rollback: vec![RollbackStep {
                id: "restore".into(),
                summary: "restore backup".into(),
            }],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        if self.overreach_source {
            return Ok(vec![SourceSpec::JsonlTail {
                id: "secrets".into(),
                path_template: "${USER_HOME}/secret.jsonl".into(),
            }]);
        }
        Ok(vec![SourceSpec::JsonlTail {
            id: "sessions".into(),
            path_template: "${AGENT_CONFIG_HOME}/sessions/**".into(),
        }])
    }

    async fn decode(
        &self,
        _frame: RawFrame,
    ) -> Result<Vec<adapter_sdk::NormalizedEvent>, AdapterError> {
        if self.panic_on_decode {
            panic!("stub decode panic");
        }
        Ok(Vec::new())
    }

    async fn health(&self) -> AdapterHealth {
        AdapterHealth::Healthy
    }
}

#[tokio::test]
async fn registers_and_probes_without_product_branches() {
    let mut host = AdapterHost::new();
    host.register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.one")))
        .unwrap();
    host.register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.two")))
        .unwrap();
    let outcomes = host
        .probe_all(ProbeContext {
            installation_id: "ins_1".into(),
        })
        .await;
    assert_eq!(outcomes.len(), 2);
    assert!(outcomes
        .iter()
        .all(|(_, result)| result.as_ref().unwrap().detected));
}

#[tokio::test]
async fn rejects_duplicate_and_incompatible_protocol() {
    let mut host = AdapterHost::new();
    host.register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.one")))
        .unwrap();
    let err = host
        .register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.one")))
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::DuplicateAdapter);

    let mut bad = StubAdapter::new("dev.tokenshow.adapter.legacy");
    bad.manifest.protocol_version = "9.0".into();
    let err = host.register(Arc::new(bad)).unwrap_err();
    assert_eq!(err.code, ErrorCode::ProtocolIncompatible);
}

#[tokio::test]
async fn panicking_adapter_is_isolated_from_peers() {
    let mut host = AdapterHost::new();
    let mut panicking = StubAdapter::new("dev.tokenshow.adapter.panic");
    panicking.panic_on_probe = true;
    host.register(Arc::new(panicking)).unwrap();
    host.register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.healthy")))
        .unwrap();

    let outcomes = host
        .probe_all(ProbeContext {
            installation_id: "ins_1".into(),
        })
        .await;
    assert_eq!(
        outcomes[0].1.as_ref().unwrap_err().code,
        ErrorCode::AdapterPanic
    );
    assert!(outcomes[1].1.as_ref().unwrap().detected);
}

#[tokio::test]
async fn consecutive_failures_open_circuit() {
    let mut host = AdapterHost::with_failure_threshold(2);
    let mut failing = StubAdapter::new("dev.tokenshow.adapter.fail");
    failing.fail_probe = true;
    host.register(Arc::new(failing)).unwrap();
    let ctx = ProbeContext {
        installation_id: "ins_1".into(),
    };
    assert_eq!(
        host.probe("dev.tokenshow.adapter.fail", ctx.clone())
            .await
            .unwrap_err()
            .code,
        ErrorCode::ProbeFailed
    );
    assert_eq!(
        host.probe("dev.tokenshow.adapter.fail", ctx.clone())
            .await
            .unwrap_err()
            .code,
        ErrorCode::ProbeFailed
    );
    assert!(host.is_circuit_open("dev.tokenshow.adapter.fail").unwrap());
    assert_eq!(
        host.probe("dev.tokenshow.adapter.fail", ctx)
            .await
            .unwrap_err()
            .code,
        ErrorCode::AdapterCircuitOpen
    );
}

#[tokio::test]
async fn discover_sources_enforces_manifest_paths() {
    let mut host = AdapterHost::new();
    let mut overreach = StubAdapter::new("dev.tokenshow.adapter.overreach");
    overreach.overreach_source = true;
    host.register(Arc::new(overreach)).unwrap();
    let err = host
        .discover_sources(
            "dev.tokenshow.adapter.overreach",
            SourceContext {
                installation_id: "ins_1".into(),
            },
        )
        .await
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::SourcePermissionDenied);
}

#[tokio::test]
async fn setup_plan_allows_declared_write_path() {
    let mut host = AdapterHost::new();
    host.register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.one")))
        .unwrap();
    let plan = host
        .setup_plan(
            "dev.tokenshow.adapter.one",
            SetupContext {
                installation_id: "ins_1".into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(plan.adapter_id, "dev.tokenshow.adapter.one");
    assert_eq!(plan.mutations.len(), 1);
}

#[tokio::test]
async fn decode_panic_does_not_escape_host() {
    let mut host = AdapterHost::new();
    let mut panicking = StubAdapter::new("dev.tokenshow.adapter.panic");
    panicking.panic_on_decode = true;
    host.register(Arc::new(panicking)).unwrap();
    let err = host
        .decode(
            "dev.tokenshow.adapter.panic",
            RawFrame::jsonl("ins_1", "sessions", "0", b"{}".as_slice()),
        )
        .await
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::AdapterPanic);
}
