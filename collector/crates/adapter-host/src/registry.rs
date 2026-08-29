use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use adapter_sdk::{
    assert_source_allowed, assert_write_path_allowed, AdapterError, AdapterHealth, AgentAdapter,
    ErrorCode, NormalizedEvent, ProbeContext, ProbeReport, RawFrame, SetupContext, SetupPlan,
    SourceContext, SourceSpec, PROTOCOL_VERSION,
};

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
    circuit: Mutex<CircuitState>,
}

/// In-process Adapter registry. Core talks to Adapters only through this host.
pub struct AdapterHost {
    entries: Vec<RegisteredAdapter>,
    index: HashMap<String, usize>,
    failure_threshold: u32,
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
        }
    }

    pub fn register(&mut self, adapter: Arc<dyn AgentAdapter>) -> Result<(), AdapterError> {
        {
            let manifest = adapter.manifest();
            adapter_sdk::validate_manifest(manifest)?;
            if self.index.contains_key(&manifest.id) {
                return Err(AdapterError::duplicate_adapter(format!(
                    "adapter {} is already registered",
                    manifest.id
                )));
            }
        }
        let id = adapter.manifest().id.clone();
        self.entries.push(RegisteredAdapter {
            adapter,
            circuit: Mutex::new(CircuitState::default()),
        });
        self.index.insert(id, self.entries.len() - 1);
        Ok(())
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
            .map(|entry| entry.adapter.manifest().id.clone())
            .collect()
    }

    pub fn adapter(&self, id: &str) -> Result<Arc<dyn AgentAdapter>, AdapterError> {
        Ok(Arc::clone(&self.entry(id)?.adapter))
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
        self.finish(entry, isolate(adapter.probe(ctx)).await)
    }

    pub async fn probe_all(&self, ctx: ProbeContext) -> Vec<AdapterOutcome<ProbeReport>> {
        let mut outcomes = Vec::with_capacity(self.entries.len());
        for entry in &self.entries {
            let id = entry.adapter.manifest().id.clone();
            let result = if let Err(err) = self.guard_circuit(entry) {
                Err(err)
            } else {
                let adapter = Arc::clone(&entry.adapter);
                self.finish(entry, isolate(adapter.probe(ctx.clone())).await)
            };
            outcomes.push((id, result));
        }
        outcomes
    }

    pub async fn setup_plan(&self, id: &str, ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        let entry = self.entry(id)?;
        self.guard_circuit(entry)?;
        let adapter = Arc::clone(&entry.adapter);
        let plan = self.finish(entry, isolate(adapter.setup_plan(ctx)).await)?;
        for mutation in &plan.mutations {
            if let Some(path) = mutation.path_template() {
                assert_write_path_allowed(adapter.manifest(), path)?;
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
        let specs = self.finish(entry, isolate(adapter.discover_sources(ctx)).await)?;
        for spec in &specs {
            assert_source_allowed(adapter.manifest(), spec)?;
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
        let adapter = Arc::clone(&entry.adapter);
        let expected_id = adapter.manifest().id.clone();
        let events = self.finish(entry, isolate(adapter.decode(frame)).await)?;
        for event in &events {
            if event.adapter_id != expected_id {
                return Err(AdapterError::decode_failed(format!(
                    "event adapter_id `{}` does not match registered `{expected_id}`",
                    event.adapter_id
                )));
            }
        }
        Ok(events)
    }

    pub async fn health(&self, id: &str) -> Result<AdapterHealth, AdapterError> {
        let entry = self.entry(id)?;
        let adapter = Arc::clone(&entry.adapter);
        match isolate(async move { Ok(adapter.health().await) }).await {
            Ok(health) => {
                if health.is_healthy() {
                    record_success(entry);
                }
                Ok(health)
            }
            Err(err) => {
                record_failure(entry, self.failure_threshold);
                Err(err)
            }
        }
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
                entry.adapter.manifest().id,
                circuit.consecutive_failures
            )))
        } else {
            Ok(())
        }
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
