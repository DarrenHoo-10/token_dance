use std::future::pending;
use std::sync::Arc;
use std::time::{Duration, Instant};

use adapter_sdk::{
    sample_manifest, Accuracy, AdapterError, AdapterHealth, AdapterManifest, AgentAdapter,
    CapabilityReport, ConfigMutation, ErrorCode, EventEnvelope, EventPayload, EventSource,
    ProbeContext, ProbeReport, RawFrame, RollbackStep, SetupContext, SetupPlan, SourceContext,
    SourceKind, SourceSpec, TokenUsage, VerifyStep,
};
use async_trait::async_trait;
use protocol::ModelUsageRecordedPayload;

use crate::AdapterHost;

struct StubAdapter {
    manifest: AdapterManifest,
    detected: bool,
    agent_version: Option<String>,
    panic_on_probe: bool,
    panic_on_decode: bool,
    hang_probe: bool,
    fail_probe: bool,
    spoof_capability: bool,
    unsafe_reason: bool,
    overreach_source: bool,
    decoded_events: Vec<adapter_sdk::NormalizedEvent>,
}

impl StubAdapter {
    fn new(id: &str) -> Self {
        let mut manifest = sample_manifest(id);
        manifest.name = id.into();
        Self {
            manifest,
            detected: true,
            agent_version: Some("1.0.0".into()),
            panic_on_probe: false,
            panic_on_decode: false,
            hang_probe: false,
            fail_probe: false,
            spoof_capability: false,
            unsafe_reason: false,
            overreach_source: false,
            decoded_events: Vec::new(),
        }
    }
}

fn normalized_event(
    manifest: &AdapterManifest,
    installation_id: &str,
    agent_version: &str,
) -> EventEnvelope {
    let hmac = format!("hmac-sha256:{}", "A".repeat(43));
    EventEnvelope {
        schema_version: "1.0".into(),
        event_id: "B".repeat(43),
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: Some(agent_version.into()),
        installation_id: installation_id.into(),
        occurred_at: "2026-08-30T00:00:00Z".into(),
        session_hash: Some(hmac.clone()),
        turn_hash: None,
        tool_call_hash: None,
        source: EventSource {
            kind: SourceKind::JsonlTail,
            cursor_hmac: hmac.clone(),
            raw_fingerprint_hmac: hmac,
        },
        accuracy: Accuracy::Exact,
        payload: EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
            provider_id: "mock".into(),
            model_id: "mock-model".into(),
            tokens: TokenUsage {
                input_tokens: None,
                output_tokens: None,
                cache_read_tokens: None,
                cache_write_tokens: None,
                reasoning_tokens: None,
                tool_tokens: None,
                total_tokens: Some("1".into()),
            },
        }),
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
        if self.hang_probe {
            pending::<()>().await;
        }
        if self.fail_probe {
            return Err(AdapterError::probe_failed("forced probe failure"));
        }
        let available = if self.detected {
            self.manifest.capabilities.clone()
        } else {
            Vec::new()
        };
        let mut capability = CapabilityReport::from_declared(
            &self.manifest.id,
            &self.manifest.version,
            &self.manifest.capabilities,
            &available,
        );
        if self.spoof_capability {
            capability.adapter_id = "dev.tokenshow.adapter.spoofed".into();
        }
        if self.unsafe_reason {
            capability.capabilities[0].safe_reason_code =
                Some("C:\\Users\\private\\prompt content".into());
        }
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: self.agent_version.clone(),
            needs_permission: false,
            needs_setup: false,
            capability,
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
        Ok(self.decoded_events.clone())
    }

    async fn health(&self) -> AdapterHealth {
        AdapterHealth::Healthy
    }
}

struct PanicManifestAdapter;

#[async_trait]
impl AgentAdapter for PanicManifestAdapter {
    fn manifest(&self) -> &AdapterManifest {
        panic!("manifest panic")
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        unreachable!()
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        unreachable!()
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        unreachable!()
    }

    async fn decode(
        &self,
        _frame: RawFrame,
    ) -> Result<Vec<adapter_sdk::NormalizedEvent>, AdapterError> {
        unreachable!()
    }

    async fn health(&self) -> AdapterHealth {
        unreachable!()
    }
}

#[test]
fn manifest_panic_is_rejected_without_escaping() {
    let mut host = AdapterHost::new();
    let error = host
        .register(Arc::new(PanicManifestAdapter))
        .expect_err("manifest panic must be isolated");
    assert_eq!(error.code, ErrorCode::AdapterPanic);
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
async fn rejects_spoofed_capability_identity() {
    let mut host = AdapterHost::new();
    let mut spoofed = StubAdapter::new("dev.tokenshow.adapter.one");
    spoofed.spoof_capability = true;
    host.register(Arc::new(spoofed)).unwrap();
    let error = host
        .probe(
            "dev.tokenshow.adapter.one",
            ProbeContext {
                installation_id: "ins_1".into(),
            },
        )
        .await
        .unwrap_err();
    assert_eq!(error.code, ErrorCode::ProbeFailed);
}

#[tokio::test]
async fn rejects_unsafe_capability_reason_code() {
    let mut host = AdapterHost::new();
    let mut unsafe_adapter = StubAdapter::new("dev.tokenshow.adapter.one");
    unsafe_adapter.unsafe_reason = true;
    host.register(Arc::new(unsafe_adapter)).unwrap();
    let error = host
        .probe(
            "dev.tokenshow.adapter.one",
            ProbeContext {
                installation_id: "ins_1".into(),
            },
        )
        .await
        .unwrap_err();
    assert_eq!(error.code, ErrorCode::ProbeFailed);
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
    let panic = outcomes
        .iter()
        .find(|(id, _)| id.ends_with("panic"))
        .unwrap();
    let healthy = outcomes
        .iter()
        .find(|(id, _)| id.ends_with("healthy"))
        .unwrap();
    assert_eq!(panic.1.as_ref().unwrap_err().code, ErrorCode::AdapterPanic);
    assert!(healthy.1.as_ref().unwrap().detected);
}

#[tokio::test]
async fn hung_adapter_times_out_without_blocking_peer() {
    let mut host = AdapterHost::new().with_call_timeout(Duration::from_millis(25));
    let mut hung = StubAdapter::new("dev.tokenshow.adapter.hung");
    hung.hang_probe = true;
    host.register(Arc::new(hung)).unwrap();
    host.register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.healthy")))
        .unwrap();

    let started = Instant::now();
    let outcomes = host
        .probe_all(ProbeContext {
            installation_id: "ins_1".into(),
        })
        .await;
    assert!(started.elapsed() < Duration::from_secs(1));
    let hung = outcomes
        .iter()
        .find(|(id, _)| id.ends_with("hung"))
        .unwrap();
    let healthy = outcomes
        .iter()
        .find(|(id, _)| id.ends_with("healthy"))
        .unwrap();
    assert_eq!(hung.1.as_ref().unwrap_err().code, ErrorCode::AdapterTimeout);
    assert!(healthy.1.as_ref().unwrap().detected);
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
async fn decode_rejects_spoofed_installation_and_agent_version() {
    for (installation_id, agent_version) in [("ins_other", "1.0.0"), ("ins_1", "9.9.9")] {
        let mut host = AdapterHost::new();
        let mut adapter = StubAdapter::new("dev.tokenshow.adapter.one");
        adapter.decoded_events = vec![normalized_event(
            &adapter.manifest,
            installation_id,
            agent_version,
        )];
        host.register(Arc::new(adapter)).unwrap();
        host.probe(
            "dev.tokenshow.adapter.one",
            ProbeContext {
                installation_id: "ins_1".into(),
            },
        )
        .await
        .unwrap();
        let error = host
            .decode(
                "dev.tokenshow.adapter.one",
                RawFrame::jsonl("ins_1", "sessions", "0", b"{}".as_slice()),
            )
            .await
            .unwrap_err();
        assert_eq!(error.code, ErrorCode::DecodeFailed);
    }
}

#[tokio::test]
async fn decode_requires_successful_probe() {
    let mut host = AdapterHost::new();
    host.register(Arc::new(StubAdapter::new("dev.tokenshow.adapter.one")))
        .unwrap();
    let error = host
        .decode(
            "dev.tokenshow.adapter.one",
            RawFrame::jsonl("ins_1", "sessions", "0", b"{}".as_slice()),
        )
        .await
        .unwrap_err();
    assert_eq!(error.code, ErrorCode::DecodeFailed);
}

#[tokio::test]
async fn successful_probe_with_unknown_version_allows_versionless_event() {
    let mut host = AdapterHost::new();
    let mut adapter = StubAdapter::new("dev.tokenshow.adapter.one");
    adapter.agent_version = None;
    let mut event = normalized_event(&adapter.manifest, "ins_1", "1.0.0");
    event.agent_version = None;
    adapter.decoded_events = vec![event];
    host.register(Arc::new(adapter)).unwrap();
    host.probe(
        "dev.tokenshow.adapter.one",
        ProbeContext {
            installation_id: "ins_1".into(),
        },
    )
    .await
    .unwrap();
    let events = host
        .decode(
            "dev.tokenshow.adapter.one",
            RawFrame::jsonl("ins_1", "sessions", "0", b"{}".as_slice()),
        )
        .await
        .unwrap();
    assert_eq!(events.len(), 1);
}

#[tokio::test]
async fn decode_panic_does_not_escape_host() {
    let mut host = AdapterHost::new();
    let mut panicking = StubAdapter::new("dev.tokenshow.adapter.panic");
    panicking.panic_on_decode = true;
    host.register(Arc::new(panicking)).unwrap();
    host.probe(
        "dev.tokenshow.adapter.panic",
        ProbeContext {
            installation_id: "ins_1".into(),
        },
    )
    .await
    .unwrap();
    let err = host
        .decode(
            "dev.tokenshow.adapter.panic",
            RawFrame::jsonl("ins_1", "sessions", "0", b"{}".as_slice()),
        )
        .await
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::AdapterPanic);
}
