use std::collections::HashMap;
use std::sync::Arc;

use adapter_host::AdapterHost;
use adapter_sdk::{
    AdapterError, AdapterHealth, AdapterRuntimeStatus, AdapterStatusReport, AgentAdapter,
    CapabilityReport, ErrorCode, ProbeContext, RawFrame, SetupContext, SourceContext, SourceSpec,
};

use futures::future::join_all;
use privacy::{PrivacyCheckedEvent, PrivacyFilter};

use crate::control::CollectionControl;
use crate::runtime::AdapterRuntime;

/// Core orchestrator. Adapters are injected through the host; Core has no product switch.
pub struct Collector {
    installation_id: String,
    host: AdapterHost,
    privacy: PrivacyFilter,
    controls: HashMap<String, CollectionControl>,
    runtimes: HashMap<String, AdapterRuntime>,
}

impl Collector {
    pub fn new(installation_id: impl Into<String>) -> Self {
        Self {
            installation_id: installation_id.into(),
            host: AdapterHost::new(),
            privacy: PrivacyFilter,
            controls: HashMap::new(),
            runtimes: HashMap::new(),
        }
    }

    pub fn with_host(installation_id: impl Into<String>, host: AdapterHost) -> Self {
        Self {
            installation_id: installation_id.into(),
            host,
            privacy: PrivacyFilter,
            controls: HashMap::new(),
            runtimes: HashMap::new(),
        }
    }

    pub fn installation_id(&self) -> &str {
        &self.installation_id
    }

    pub fn register_adapter(&mut self, adapter: Arc<dyn AgentAdapter>) -> Result<(), AdapterError> {
        let manifest = self.host.register(adapter)?;
        let id = manifest.id;
        let version = manifest.version;
        self.controls
            .insert(id.clone(), CollectionControl::default());
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

    pub fn is_enabled(&self, adapter_id: &str) -> bool {
        self.controls
            .get(adapter_id)
            .is_some_and(CollectionControl::is_enabled)
    }

    pub fn control(&self, adapter_id: &str) -> Option<CollectionControl> {
        self.controls.get(adapter_id).cloned()
    }

    fn ensure_enabled(&self, adapter_id: &str) -> Result<(), AdapterError> {
        let runtime = self.runtimes.get(adapter_id).ok_or_else(|| {
            AdapterError::adapter_not_found(format!("adapter `{adapter_id}` is not registered"))
        })?;
        if runtime.enabled && self.is_enabled(adapter_id) {
            Ok(())
        } else {
            Err(AdapterError::new(
                ErrorCode::AdapterDisabled,
                format!("adapter `{adapter_id}` is disabled"),
            ))
        }
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
        let enabled_ids: Vec<String> = self
            .runtimes()
            .into_iter()
            .filter(|runtime| runtime.enabled)
            .map(|runtime| runtime.adapter_id.clone())
            .collect();
        let host = &self.host;
        let outcomes = join_all(enabled_ids.into_iter().map(|id| {
            let call_ctx = ctx.clone();
            async move {
                let result = host.probe(&id, call_ctx).await;
                (id, result)
            }
        }))
        .await;
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
        self.ensure_enabled(adapter_id)?;
        match self.host.health(adapter_id).await {
            Ok(health) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime.apply_health(&health);
                }
                Ok(health)
            }
            Err(err) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime.apply_operation_error(&err);
                }
                Err(err)
            }
        }
    }

    pub async fn discover_sources(
        &mut self,
        adapter_id: &str,
    ) -> Result<Vec<SourceSpec>, AdapterError> {
        self.ensure_enabled(adapter_id)?;
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

    /// Request a declarative setup proposal. This does not mutate user configuration.
    pub async fn propose_setup(
        &mut self,
        adapter_id: &str,
    ) -> Result<adapter_sdk::SetupPlan, AdapterError> {
        self.ensure_enabled(adapter_id)?;
        let ctx = SetupContext {
            installation_id: self.installation_id.clone(),
        };
        match self.host.setup_plan(adapter_id, ctx).await {
            Ok(plan) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime.record_setup_proposed();
                }
                Ok(plan)
            }
            Err(err) => {
                if let Some(runtime) = self.runtimes.get_mut(adapter_id) {
                    runtime.apply_operation_error(&err);
                }
                Err(err)
            }
        }
    }

    pub async fn decode(
        &self,
        adapter_id: &str,
        frame: RawFrame,
    ) -> Result<Vec<PrivacyCheckedEvent>, AdapterError> {
        self.ensure_enabled(adapter_id)?;
        if frame.installation_id != self.installation_id {
            return Err(AdapterError::decode_failed(
                "RawFrame installation_id does not match Collector installation",
            ));
        }
        let runtime = self.runtimes.get(adapter_id).ok_or_else(|| {
            AdapterError::adapter_not_found(format!("adapter `{adapter_id}` is not registered"))
        })?;
        if !runtime
            .sources
            .iter()
            .any(|source| source.id() == frame.source_id && source.kind() == frame.source_kind)
        {
            return Err(AdapterError::decode_failed(
                "RawFrame source is not an authorized discovered source",
            ));
        }
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
        if let Some(control) = self.controls.get(adapter_id) {
            control.disable();
        }
        Ok(())
    }

    pub fn enable(&mut self, adapter_id: &str) -> Result<(), AdapterError> {
        let runtime = self.runtimes.get_mut(adapter_id).ok_or_else(|| {
            AdapterError::adapter_not_found(format!("adapter `{adapter_id}` is not registered"))
        })?;
        runtime.enable();
        if let Some(control) = self.controls.get(adapter_id) {
            control.enable();
        }
        Ok(())
    }
}
