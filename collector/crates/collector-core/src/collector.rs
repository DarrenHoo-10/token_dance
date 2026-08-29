use std::collections::HashMap;
use std::sync::Arc;

use adapter_host::AdapterHost;
use adapter_sdk::{
    AdapterError, AdapterHealth, AdapterRuntimeStatus, AdapterStatusReport, AgentAdapter,
    CapabilityReport, ErrorCode, NormalizedEvent, ProbeContext, RawFrame, SetupContext,
    SourceContext, SourceSpec,
};

use privacy::PrivacyFilter;

use crate::runtime::AdapterRuntime;

/// Core orchestrator. Adapters are injected through the host; Core has no product switch.
pub struct Collector {
    installation_id: String,
    host: AdapterHost,
    privacy: PrivacyFilter,
    runtimes: HashMap<String, AdapterRuntime>,
}

impl Collector {
    pub fn new(installation_id: impl Into<String>) -> Self {
        Self {
            installation_id: installation_id.into(),
            host: AdapterHost::new(),
            privacy: PrivacyFilter,
            runtimes: HashMap::new(),
        }
    }

    pub fn with_host(installation_id: impl Into<String>, host: AdapterHost) -> Self {
        Self {
            installation_id: installation_id.into(),
            host,
            privacy: PrivacyFilter,
            runtimes: HashMap::new(),
        }
    }

    pub fn installation_id(&self) -> &str {
        &self.installation_id
    }

    pub fn host(&self) -> &AdapterHost {
        &self.host
    }

    pub fn register_adapter(&mut self, adapter: Arc<dyn AgentAdapter>) -> Result<(), AdapterError> {
        let id = adapter.manifest().id.clone();
        let version = adapter.manifest().version.clone();
        self.host.register(adapter)?;
        self.runtimes
            .insert(id.clone(), AdapterRuntime::new(id, version));
        Ok(())
    }

    pub fn runtime(&self, adapter_id: &str) -> Option<&AdapterRuntime> {
        self.runtimes.get(adapter_id)
    }

    pub fn runtimes(&self) -> Vec<&AdapterRuntime> {
        self.host
            .adapter_ids()
            .into_iter()
            .filter_map(|id| self.runtimes.get(&id))
            .collect()
    }

    pub fn status_of(&self, adapter_id: &str) -> Option<AdapterRuntimeStatus> {
        self.runtimes.get(adapter_id).map(|runtime| runtime.status)
    }

    pub fn capability_reports(&self) -> Vec<CapabilityReport> {
        self.runtimes()
            .into_iter()
            .map(|runtime| runtime.capability.clone())
            .collect()
    }

    pub fn status_reports(&self, reported_at: impl Into<String>) -> Vec<AdapterStatusReport> {
        let reported_at = reported_at.into();
        self.runtimes()
            .into_iter()
            .map(|runtime| runtime.to_status_report(reported_at.clone()))
            .collect()
    }

    pub async fn probe_all(&mut self) {
        let ctx = ProbeContext {
            installation_id: self.installation_id.clone(),
        };
        let outcomes = self.host.probe_all(ctx).await;
        for (id, result) in outcomes {
            if let Some(runtime) = self.runtimes.get_mut(&id) {
                runtime.apply_probe_result(result);
            }
        }
    }

    pub async fn refresh_health(
        &mut self,
        adapter_id: &str,
    ) -> Result<AdapterHealth, AdapterError> {
        let health = self.host.health(adapter_id).await?;
        if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
            runtime.apply_health(&health);
        }
        Ok(health)
    }

    pub async fn discover_sources(
        &mut self,
        adapter_id: &str,
    ) -> Result<Vec<SourceSpec>, AdapterError> {
        let ctx = SourceContext {
            installation_id: self.installation_id.clone(),
        };
        match self.host.discover_sources(adapter_id, ctx).await {
            Ok(sources) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime.apply_sources(sources.clone());
                }
                Ok(sources)
            }
            Err(err) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime
                        .apply_probe_result(Err(AdapterError::new(err.code, err.message.clone())));
                }
                Err(err)
            }
        }
    }

    pub async fn approve_setup(
        &mut self,
        adapter_id: &str,
    ) -> Result<adapter_sdk::SetupPlan, AdapterError> {
        if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
            runtime.begin_configure();
        }
        let ctx = SetupContext {
            installation_id: self.installation_id.clone(),
        };
        match self.host.setup_plan(adapter_id, ctx).await {
            Ok(plan) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime.finish_configure_ok();
                }
                Ok(plan)
            }
            Err(err) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime.finish_configure_err(&err);
                }
                Err(err)
            }
        }
    }

    pub async fn decode(
        &self,
        adapter_id: &str,
        frame: RawFrame,
    ) -> Result<Vec<NormalizedEvent>, AdapterError> {
        let events = self.host.decode(adapter_id, frame).await?;
        self.privacy
            .filter_all(events)
            .map_err(|error| AdapterError::new(ErrorCode::PrivacyRejected, error.to_string()))
    }

    pub fn disable(&mut self, adapter_id: &str) -> Result<(), AdapterError> {
        let runtime = self.runtimes.get_mut(adapter_id).ok_or_else(|| {
            AdapterError::adapter_not_found(format!("adapter `{adapter_id}` is not registered"))
        })?;
        runtime.disable();
        Ok(())
    }

    pub fn enable(&mut self, adapter_id: &str) -> Result<(), AdapterError> {
        let runtime = self.runtimes.get_mut(adapter_id).ok_or_else(|| {
            AdapterError::adapter_not_found(format!("adapter `{adapter_id}` is not registered"))
        })?;
        runtime.enable();
        Ok(())
    }
}
