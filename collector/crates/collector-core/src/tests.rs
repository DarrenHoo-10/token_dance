use std::fs;
use std::path::PathBuf;
use std::sync::Arc;

use adapter_mock::MockAdapter;
use adapter_sdk::{
    sample_manifest, AdapterError, AdapterHealth, AdapterManifest, AdapterRuntimeStatus,
    AgentAdapter, Capability, CapabilityReport, ConfigMutation, ProbeContext, ProbeReport,
    RawFrame, SetupContext, SetupPlan, SetupPlanStatus, SourceContext, SourceKind, SourceSpec,
};
use async_trait::async_trait;

use crate::Collector;

struct StubAdapter {
    manifest: AdapterManifest,
    detected: bool,
    panic_on_probe: bool,
    available: Vec<Capability>,
}

impl StubAdapter {
    fn new(id: &str) -> Self {
        let mut manifest = sample_manifest(id);
        manifest.capabilities = vec![Capability::Sessions, Capability::Tokens];
        let available = manifest.capabilities.clone();
        Self {
            manifest,
            detected: true,
            panic_on_probe: false,
            available,
        }
    }
}

#[async_trait]
impl AgentAdapter for StubAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        if self.panic_on_probe {
            panic!("core stub probe panic");
        }
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: Some("1.0.0".into()),
            needs_permission: false,
            needs_setup: false,
            capability: CapabilityReport::from_declared(
                &self.manifest.id,
                &self.manifest.version,
                &self.manifest.capabilities,
                &self.available,
            ),
            detail: None,
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "plan-core".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "enable".into(),
            mutations: vec![ConfigMutation::JsonMergePatch {
                path_template: "${AGENT_CONFIG_HOME}/config.json".into(),
                patch: serde_json::json!({"enabled": true}),
            }],
            required_permissions: vec![],
            verify: vec![],
            rollback: vec![],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        Ok(vec![SourceSpec::JsonlTail {
            id: "sessions".into(),
            path_template: "${AGENT_CONFIG_HOME}/sessions/**".into(),
        }])
    }

    async fn decode(
        &self,
        _frame: RawFrame,
    ) -> Result<Vec<adapter_sdk::NormalizedEvent>, AdapterError> {
        Ok(Vec::new())
    }

    async fn health(&self) -> AdapterHealth {
        if self.available.len() < self.manifest.capabilities.len() {
            AdapterHealth::Degraded {
                reason: "partial capabilities".into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

#[tokio::test]
async fn undetected_probe_does_not_write_files() {
    let tmp = std::env::temp_dir().join(format!("tokenshow-core-{}", std::process::id()));
    let _ = fs::remove_dir_all(&tmp);
    fs::create_dir_all(&tmp).unwrap();
    let before = fs::read_dir(&tmp).unwrap().count();

    let mut collector = Collector::new("ins_undetected");
    let mut stub = StubAdapter::new("dev.tokenshow.adapter.example");
    stub.detected = false;
    collector.register_adapter(Arc::new(stub)).unwrap();
    collector.probe_all().await;

    assert_eq!(
        collector.status_of("dev.tokenshow.adapter.example"),
        Some(AdapterRuntimeStatus::Undetected)
    );
    assert_eq!(fs::read_dir(&tmp).unwrap().count(), before);
    let _ = fs::remove_dir_all(&tmp);
}

#[tokio::test]
async fn panicking_adapter_does_not_block_peers_or_core() {
    let mut collector = Collector::new("ins_iso");
    let mut panicking = StubAdapter::new("dev.tokenshow.adapter.panic");
    panicking.panic_on_probe = true;
    collector.register_adapter(Arc::new(panicking)).unwrap();
    collector
        .register_adapter(Arc::new(StubAdapter::new("dev.tokenshow.adapter.healthy")))
        .unwrap();
    collector.probe_all().await;

    assert_eq!(
        collector.status_of("dev.tokenshow.adapter.panic"),
        Some(AdapterRuntimeStatus::Error)
    );
    assert_eq!(
        collector.status_of("dev.tokenshow.adapter.healthy"),
        Some(AdapterRuntimeStatus::Active)
    );
}

#[tokio::test]
async fn partial_capabilities_are_degraded() {
    let mut collector = Collector::new("ins_cap");
    let mut stub = StubAdapter::new("dev.tokenshow.adapter.example");
    stub.available = vec![Capability::Sessions];
    collector.register_adapter(Arc::new(stub)).unwrap();
    collector.probe_all().await;
    assert_eq!(
        collector.status_of("dev.tokenshow.adapter.example"),
        Some(AdapterRuntimeStatus::Degraded)
    );
    let report = &collector.capability_reports()[0];
    assert!(report.missing().contains(&Capability::Tokens));
    let status = &collector.status_reports("2026-08-30T00:00:00.000Z")[0];
    assert_eq!(status.runtime_status, AdapterRuntimeStatus::Degraded);
    assert_eq!(status.source_kinds, Vec::<SourceKind>::new());
}

#[tokio::test]
async fn setup_request_remains_proposed_until_an_executor_applies_it() {
    let mut collector = Collector::new("ins_setup");
    collector
        .register_adapter(Arc::new(StubAdapter::new("dev.tokenshow.adapter.setup")))
        .unwrap();
    collector
        .propose_setup("dev.tokenshow.adapter.setup")
        .await
        .unwrap();
    let runtime = collector.runtime("dev.tokenshow.adapter.setup").unwrap();
    assert_eq!(runtime.setup_plan_status, Some(SetupPlanStatus::Proposed));
    assert_ne!(runtime.setup_plan_status, Some(SetupPlanStatus::Applied));
}

#[tokio::test]
async fn disable_one_adapter_leaves_others_active() {
    let mut collector = Collector::new("ins_disable");
    collector
        .register_adapter(Arc::new(StubAdapter::new("dev.tokenshow.adapter.one")))
        .unwrap();
    collector
        .register_adapter(Arc::new(StubAdapter::new("dev.tokenshow.adapter.two")))
        .unwrap();
    collector.probe_all().await;
    collector.disable("dev.tokenshow.adapter.one").unwrap();
    assert_eq!(
        collector.status_of("dev.tokenshow.adapter.one"),
        Some(AdapterRuntimeStatus::Disabled)
    );
    assert_eq!(
        collector.status_of("dev.tokenshow.adapter.two"),
        Some(AdapterRuntimeStatus::Active)
    );
    let error = collector
        .discover_sources("dev.tokenshow.adapter.one")
        .await
        .expect_err("disabled Adapter must not be called");
    assert_eq!(error.code, adapter_sdk::ErrorCode::AdapterDisabled);
}

#[tokio::test]
async fn decode_applies_final_privacy_filter() {
    let installation_id = format!("ins_{}", "0".repeat(26));
    let mut collector = Collector::new(&installation_id);
    collector
        .register_adapter(Arc::new(MockAdapter::new()))
        .unwrap();
    collector.probe_all().await;
    let frame = RawFrame::jsonl(
        &installation_id,
        "sessions",
        "0",
        br#"{"type":"model_usage_recorded","occurredAt":"2026-08-30T00:00:00Z","sessionId":"session-1","providerId":"mock","modelId":"C:\\Users\\private\\model","tokens":{"totalTokens":1}}"#.to_vec(),
    );

    let error = collector
        .decode("dev.tokenshow.adapter.mock", frame)
        .await
        .expect_err("absolute path must be rejected after Adapter decode");
    assert_eq!(error.code, adapter_sdk::ErrorCode::PrivacyRejected);
}

#[test]
fn collector_core_has_no_agent_product_branches() {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("src");
    let forbidden = [
        "\"codex\"",
        "\"claude\"",
        "\"claude-code\"",
        "\"cursor\"",
        "\"zcode\"",
        "\"deepseek\"",
        "\"grok\"",
        "\"grok-build\"",
    ];
    let mut hits = Vec::new();
    scan_dir(&root, &forbidden, &mut hits);
    assert!(
        hits.is_empty(),
        "collector-core contains agent product branches: {hits:?}"
    );
}

fn scan_dir(dir: &std::path::Path, forbidden: &[&str], hits: &mut Vec<String>) {
    for entry in fs::read_dir(dir).unwrap() {
        let entry = entry.unwrap();
        let path = entry.path();
        if path.is_dir() {
            scan_dir(&path, forbidden, hits);
            continue;
        }
        if path.extension().and_then(|ext| ext.to_str()) != Some("rs") {
            continue;
        }
        let content = fs::read_to_string(&path).unwrap().to_lowercase();
        for needle in forbidden {
            if content.contains(needle) {
                hits.push(format!("{}: {needle}", path.display()));
            }
        }
    }
}
