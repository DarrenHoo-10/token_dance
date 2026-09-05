use std::collections::{HashMap, HashSet};
use std::future::Future;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use adapter_sdk::{
    assert_source_allowed, assert_write_path_allowed, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    ErrorCode, EventPayload, NormalizedEvent, ProbeContext, ProbeReport, RawFrame, SetupContext,
    SetupPlan, SourceContext, SourceSpec, PROTOCOL_VERSION,
};
use futures::stream::{FuturesUnordered, StreamExt};
use tokio::time::timeout;

use crate::isolate::isolate;

/// Outcome of a per-Adapter host call. Failures never abort the remaining adapters.
pub type AdapterOutcome<T> = (String, Result<T, AdapterError>);

#[derive(Default)]
struct CircuitState {
    consecutive_failures: u32,
    open: bool,
}

struct RegisteredAdapter {
    adapter: Arc<dyn AgentAdapter>,
    manifest: AdapterManifest,
    probe_succeeded: AtomicBool,
    agent_version: Mutex<Option<String>>,
    circuit: Mutex<CircuitState>,
}

/// In-process Adapter registry. Core talks to Adapters only through this host.
pub struct AdapterHost {
    entries: Vec<RegisteredAdapter>,
    index: HashMap<String, usize>,
    failure_threshold: u32,
    call_timeout: Duration,
}

impl Default for AdapterHost {
    fn default() -> Self {
        Self::new()
    }
}

impl AdapterHost {
    pub fn new() -> Self {
        Self::with_failure_threshold(3)
    }

    pub fn with_failure_threshold(failure_threshold: u32) -> Self {
        Self {
            entries: Vec::new(),
            index: HashMap::new(),
            failure_threshold: failure_threshold.max(1),
            call_timeout: Duration::from_secs(5),
        }
    }

    pub fn with_call_timeout(mut self, call_timeout: Duration) -> Self {
        self.call_timeout = call_timeout.max(Duration::from_millis(1));
        self
    }

    pub fn register(
        &mut self,
        adapter: Arc<dyn AgentAdapter>,
    ) -> Result<AdapterManifest, AdapterError> {
        let manifest =
            catch_unwind(AssertUnwindSafe(|| adapter.manifest().clone())).map_err(|_| {
                AdapterError::adapter_panic("adapter panicked while returning manifest")
            })?;
        adapter_sdk::validate_manifest(&manifest)?;
        if self.index.contains_key(&manifest.id) {
            return Err(AdapterError::duplicate_adapter(format!(
                "adapter {} is already registered",
                manifest.id
            )));
        }
        let id = manifest.id.clone();
        self.entries.push(RegisteredAdapter {
            adapter,
            manifest: manifest.clone(),
            probe_succeeded: AtomicBool::new(false),
            agent_version: Mutex::new(None),
            circuit: Mutex::new(CircuitState::default()),
        });
        self.index.insert(id, self.entries.len() - 1);
        Ok(manifest)
    }

    pub fn len(&self) -> usize {
        self.entries.len()
    }

    pub fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }

    pub fn adapter_ids(&self) -> Vec<String> {
        self.entries
            .iter()
            .map(|entry| entry.manifest.id.clone())
            .collect()
    }

    pub fn manifest(&self, id: &str) -> Result<&AdapterManifest, AdapterError> {
        Ok(&self.entry(id)?.manifest)
    }

    pub fn is_circuit_open(&self, id: &str) -> Result<bool, AdapterError> {
        let entry = self.entry(id)?;
        let circuit = entry.circuit.lock().unwrap_or_else(|err| err.into_inner());
        Ok(circuit.open)
    }

    pub async fn probe(&self, id: &str, ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        let entry = self.entry(id)?;
        self.guard_circuit(entry)?;
        let adapter = Arc::clone(&entry.adapter);
        let report =
            canonicalize_probe(&entry.manifest, self.call(entry, adapter.probe(ctx)).await?)?;
        record_agent_version(entry, report.agent_version.clone());
        Ok(report)
    }

    pub async fn probe_all(&self, ctx: ProbeContext) -> Vec<AdapterOutcome<ProbeReport>> {
        let mut pending = FuturesUnordered::new();
        for entry in &self.entries {
            let id = entry.manifest.id.clone();
            let call_ctx = ctx.clone();
            pending.push(async move {
                let result = if let Err(err) = self.guard_circuit(entry) {
                    Err(err)
                } else {
                    let adapter = Arc::clone(&entry.adapter);
                    match self.call(entry, adapter.probe(call_ctx)).await {
                        Ok(report) => {
                            canonicalize_probe(&entry.manifest, report).inspect(|report| {
                                record_agent_version(entry, report.agent_version.clone());
                            })
                        }
                        Err(err) => Err(err),
                    }
                };
                (id, result)
            });
        }
        let mut outcomes = Vec::with_capacity(self.entries.len());
        while let Some(outcome) = pending.next().await {
            outcomes.push(outcome);
        }
        outcomes.sort_by(|left, right| left.0.cmp(&right.0));
        outcomes
    }

    pub async fn setup_plan(&self, id: &str, ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        let entry = self.entry(id)?;
        self.guard_circuit(entry)?;
        let adapter = Arc::clone(&entry.adapter);
        let plan = self.call(entry, adapter.setup_plan(ctx)).await?;
        if plan.adapter_id != entry.manifest.id {
            return Err(AdapterError::setup_failed(
                "setup plan adapter_id does not match manifest",
            ));
        }
        for mutation in &plan.mutations {
            if let Some(path) = mutation.path_template() {
                assert_write_path_allowed(&entry.manifest, path)?;
            } else {
                return Err(AdapterError::manifest_permission_denied(
                    "environment mutation is forbidden until manifest environment permissions exist",
                ));
            }
        }
        Ok(plan)
    }

    pub async fn discover_sources(
        &self,
        id: &str,
        ctx: SourceContext,
    ) -> Result<Vec<SourceSpec>, AdapterError> {
        let entry = self.entry(id)?;
        self.guard_circuit(entry)?;
        let adapter = Arc::clone(&entry.adapter);
        let specs = self.call(entry, adapter.discover_sources(ctx)).await?;
        for spec in &specs {
            assert_source_allowed(&entry.manifest, spec)?;
        }
        Ok(specs)
    }

    pub async fn decode(
        &self,
        id: &str,
        frame: RawFrame,
    ) -> Result<Vec<NormalizedEvent>, AdapterError> {
        let entry = self.entry(id)?;
        self.guard_circuit(entry)?;
        if !entry.probe_succeeded.load(Ordering::Acquire) {
            return Err(AdapterError::decode_failed(
                "adapter must probe before decode",
            ));
        }
        let trusted_agent_version = entry
            .agent_version
            .lock()
            .unwrap_or_else(|err| err.into_inner())
            .clone();
        let adapter = Arc::clone(&entry.adapter);
        let frame_installation_id = frame.installation_id.clone();
        let frame_source_kind = frame.source_kind;
        let events = self.call(entry, adapter.decode(frame)).await?;
        for event in &events {
            validate_event_metadata(
                &entry.manifest,
                trusted_agent_version.as_deref(),
                &frame_installation_id,
                frame_source_kind,
                event,
            )?;
        }
        Ok(events)
    }

    pub async fn health(&self, id: &str) -> Result<AdapterHealth, AdapterError> {
        let entry = self.entry(id)?;
        let adapter = Arc::clone(&entry.adapter);
        self.call(entry, async move { Ok(adapter.health().await) })
            .await
    }

    fn entry(&self, id: &str) -> Result<&RegisteredAdapter, AdapterError> {
        self.index
            .get(id)
            .and_then(|index| self.entries.get(*index))
            .ok_or_else(|| {
                AdapterError::adapter_not_found(format!("adapter `{id}` is not registered"))
            })
    }

    fn guard_circuit(&self, entry: &RegisteredAdapter) -> Result<(), AdapterError> {
        let circuit = entry.circuit.lock().unwrap_or_else(|err| err.into_inner());
        if circuit.open {
            Err(AdapterError::circuit_open(format!(
                "adapter {} circuit is open after {} failures (collector protocol {PROTOCOL_VERSION})",
                entry.manifest.id,
                circuit.consecutive_failures
            )))
        } else {
            Ok(())
        }
    }

    async fn call<T, F>(&self, entry: &RegisteredAdapter, future: F) -> Result<T, AdapterError>
    where
        F: Future<Output = Result<T, AdapterError>>,
    {
        let result = match timeout(self.call_timeout, isolate(future)).await {
            Ok(result) => result,
            Err(_) => Err(AdapterError::new(
                ErrorCode::AdapterTimeout,
                format!("adapter {} call timed out", entry.manifest.id),
            )),
        };
        self.finish(entry, result)
    }

    fn finish<T>(
        &self,
        entry: &RegisteredAdapter,
        result: Result<T, AdapterError>,
    ) -> Result<T, AdapterError> {
        match &result {
            Ok(_) => record_success(entry),
            Err(err) if err.code == ErrorCode::AdapterCircuitOpen => {}
            Err(_) => record_failure(entry, self.failure_threshold),
        }
        result
    }
}

fn canonicalize_probe(
    manifest: &AdapterManifest,
    mut report: ProbeReport,
) -> Result<ProbeReport, AdapterError> {
    if report.capability.adapter_id != manifest.id
        || report.capability.adapter_version != manifest.version
    {
        return Err(AdapterError::probe_failed(
            "capability report identity does not match manifest",
        ));
    }
    let declared: HashSet<Capability> = manifest.capabilities.iter().copied().collect();
    let mut seen = HashSet::new();
    for status in &report.capability.capabilities {
        if !declared.contains(&status.capability) || !seen.insert(status.capability) {
            return Err(AdapterError::probe_failed(
                "capability report contains undeclared or duplicate capability",
            ));
        }
        if status
            .safe_reason_code
            .as_deref()
            .is_some_and(|code| !is_safe_reason_code(code))
        {
            return Err(AdapterError::probe_failed(
                "capability safe_reason_code is not a stable ASCII code",
            ));
        }
        match (status.availability, status.accuracy) {
            (CapabilityAvailability::Available, Some(_))
            | (CapabilityAvailability::Unavailable, None) => {}
            _ => {
                return Err(AdapterError::probe_failed(
                    "capability availability and accuracy are inconsistent",
                ));
            }
        }
    }
    let canonical = manifest
        .capabilities
        .iter()
        .copied()
        .map(|capability| {
            report
                .capability
                .capabilities
                .iter()
                .find(|status| status.capability == capability)
                .cloned()
                .unwrap_or(CapabilityStatus {
                    capability,
                    availability: CapabilityAvailability::Unavailable,
                    accuracy: None,
                    safe_reason_code: Some("NOT_REPORTED".into()),
                })
        })
        .collect();
    report.capability = CapabilityReport {
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        capabilities: canonical,
    };
    Ok(report)
}

fn is_safe_reason_code(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_')
}

fn validate_event_metadata(
    manifest: &AdapterManifest,
    trusted_agent_version: Option<&str>,
    installation_id: &str,
    source_kind: adapter_sdk::SourceKind,
    event: &NormalizedEvent,
) -> Result<(), AdapterError> {
    if event.adapter_id != manifest.id
        || event.adapter_version != manifest.version
        || event.agent_id != manifest.agent.id
        || event.agent_version.as_deref() != trusted_agent_version
        || event.installation_id != installation_id
        || event.source.kind != source_kind
    {
        return Err(AdapterError::decode_failed(
            "event metadata does not match manifest or RawFrame",
        ));
    }
    let required = match &event.payload {
        EventPayload::SessionStarted(_) | EventPayload::SessionEnded(_) => Capability::Sessions,
        EventPayload::TurnStarted(_) | EventPayload::TurnCompleted(_) => Capability::Turns,
        EventPayload::ModelUsageRecorded(_) => Capability::Tokens,
        EventPayload::ToolInvoked(_) => Capability::Tools,
        EventPayload::SkillInvoked(_) => Capability::Skills,
        EventPayload::CodeChanged(_) => Capability::Code,
        EventPayload::CostRecorded(_) => Capability::Cost,
        EventPayload::AgentSpawned(_) => Capability::Subagents,
    };
    if !manifest.capabilities.contains(&required) {
        return Err(AdapterError::decode_failed(format!(
            "event requires undeclared capability {required:?}"
        )));
    }
    Ok(())
}

fn record_agent_version(entry: &RegisteredAdapter, version: Option<String>) {
    *entry
        .agent_version
        .lock()
        .unwrap_or_else(|err| err.into_inner()) = version;
    entry.probe_succeeded.store(true, Ordering::Release);
}

fn record_success(entry: &RegisteredAdapter) {
    let mut circuit = entry.circuit.lock().unwrap_or_else(|err| err.into_inner());
    circuit.consecutive_failures = 0;
    circuit.open = false;
}

fn record_failure(entry: &RegisteredAdapter, threshold: u32) {
    let mut circuit = entry.circuit.lock().unwrap_or_else(|err| err.into_inner());
    circuit.consecutive_failures = circuit.consecutive_failures.saturating_add(1);
    if circuit.consecutive_failures >= threshold {
        circuit.open = true;
    }
}
