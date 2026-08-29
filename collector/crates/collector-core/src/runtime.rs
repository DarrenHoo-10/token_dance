use adapter_sdk::{
    AdapterError, AdapterHealth, AdapterRuntimeStatus, CapabilityLevel, CapabilityReport,
    ErrorCode, ProbeReport, SetupPlanStatus, SourceSpec,
};

/// Unified per-Adapter runtime snapshot owned by Core.
#[derive(Debug, Clone)]
pub struct AdapterRuntime {
    pub adapter_id: String,
    pub adapter_version: String,
    pub status: AdapterRuntimeStatus,
    pub enabled: bool,
    pub detected: bool,
    pub needs_permission: bool,
    pub needs_setup: bool,
    pub capability: CapabilityReport,
    pub sources: Vec<SourceSpec>,
    pub last_error: Option<String>,
    pub last_error_code: Option<ErrorCode>,
    pub setup_plan_status: Option<SetupPlanStatus>,
}

impl AdapterRuntime {
    pub fn new(adapter_id: impl Into<String>, adapter_version: impl Into<String>) -> Self {
        let adapter_id = adapter_id.into();
        let adapter_version = adapter_version.into();
        Self {
            capability: CapabilityReport {
                adapter_id: adapter_id.clone(),
                adapter_version: adapter_version.clone(),
                capabilities: Vec::new(),
            },
            adapter_id,
            adapter_version,
            status: AdapterRuntimeStatus::Undetected,
            enabled: true,
            detected: false,
            needs_permission: false,
            needs_setup: false,
            sources: Vec::new(),
            last_error: None,
            last_error_code: None,
            setup_plan_status: None,
        }
    }

    pub fn apply_probe_result(&mut self, result: Result<ProbeReport, AdapterError>) {
        match result {
            Ok(report) => {
                self.last_error = None;
                self.last_error_code = None;
                self.detected = report.detected;
                self.needs_permission = report.needs_permission;
                self.needs_setup = report.needs_setup;
                self.capability = report.capability;
                self.recompute(None);
            }
            Err(err) => {
                self.last_error = Some(err.message.clone());
                self.last_error_code = Some(err.code);
                self.recompute(None);
            }
        }
    }

    pub fn apply_health(&mut self, health: &AdapterHealth) {
        if !matches!(health, AdapterHealth::Healthy) {
            self.last_error = health_reason(health).map(ToOwned::to_owned);
        }
        self.recompute(Some(health));
    }

    pub fn apply_sources(&mut self, sources: Vec<SourceSpec>) {
        self.sources = sources;
        self.recompute(None);
    }

    pub fn disable(&mut self) {
        self.enabled = false;
        self.status = AdapterRuntimeStatus::Disabled;
    }

    pub fn enable(&mut self) {
        self.enabled = true;
        if self.status == AdapterRuntimeStatus::Disabled {
            self.status = AdapterRuntimeStatus::Detected;
        }
        self.recompute(None);
    }

    pub fn begin_configure(&mut self) {
        if self.enabled {
            self.status = AdapterRuntimeStatus::Configuring;
            self.setup_plan_status = Some(SetupPlanStatus::Applying);
        }
    }

    pub fn finish_configure_ok(&mut self) {
        self.needs_setup = false;
        self.needs_permission = false;
        self.setup_plan_status = Some(SetupPlanStatus::Applied);
        self.recompute(None);
    }

    pub fn finish_configure_err(&mut self, err: &AdapterError) {
        self.last_error = Some(err.message.clone());
        self.last_error_code = Some(err.code);
        self.setup_plan_status = Some(SetupPlanStatus::Failed);
        self.recompute(None);
    }

    fn recompute(&mut self, health: Option<&AdapterHealth>) {
        self.status = derive_status(self, health);
    }

    pub fn to_status_report(
        &self,
        reported_at: impl Into<String>,
    ) -> adapter_sdk::AdapterStatusReport {
        adapter_sdk::AdapterStatusReport {
            adapter_id: self.adapter_id.clone(),
            adapter_version: self.adapter_version.clone(),
            agent_version: None,
            runtime_status: self.status,
            capabilities: self.capability.capabilities.clone(),
            source_kinds: self
                .sources
                .iter()
                .map(adapter_sdk::SourceSpec::kind)
                .collect(),
            safe_error_code: self.last_error_code.map(|code| code.as_str().to_string()),
            reported_at: reported_at.into(),
        }
    }
}

pub fn derive_status(
    runtime: &AdapterRuntime,
    health: Option<&AdapterHealth>,
) -> AdapterRuntimeStatus {
    if !runtime.enabled {
        return AdapterRuntimeStatus::Disabled;
    }
    if matches!(
        runtime.setup_plan_status,
        Some(SetupPlanStatus::Applying | SetupPlanStatus::Verifying)
    ) {
        return AdapterRuntimeStatus::Configuring;
    }
    if let Some(code) = runtime.last_error_code {
        if matches!(
            code,
            ErrorCode::AdapterPanic
                | ErrorCode::AdapterCircuitOpen
                | ErrorCode::ProtocolIncompatible
                | ErrorCode::ManifestInvalid
                | ErrorCode::ManifestPermissionDenied
        ) {
            return AdapterRuntimeStatus::Error;
        }
        if runtime.detected {
            return AdapterRuntimeStatus::Error;
        }
    }
    if !runtime.detected {
        return AdapterRuntimeStatus::Undetected;
    }
    if matches!(health, Some(AdapterHealth::PermanentlyDisabled { .. })) {
        return AdapterRuntimeStatus::Disabled;
    }
    if matches!(health, Some(AdapterHealth::Permission { .. })) || runtime.needs_permission {
        return AdapterRuntimeStatus::NeedsPermission;
    }
    if matches!(
        health,
        Some(AdapterHealth::Incompatible { .. } | AdapterHealth::Recoverable { .. })
    ) {
        return AdapterRuntimeStatus::Error;
    }
    if runtime.needs_setup {
        return AdapterRuntimeStatus::Detected;
    }
    let degraded_health = matches!(health, Some(AdapterHealth::Degraded { .. }));
    match runtime.capability.level() {
        CapabilityLevel::None => AdapterRuntimeStatus::Degraded,
        CapabilityLevel::Partial => AdapterRuntimeStatus::Degraded,
        CapabilityLevel::Full if degraded_health => AdapterRuntimeStatus::Degraded,
        CapabilityLevel::Full => AdapterRuntimeStatus::Active,
    }
}

fn health_reason(health: &AdapterHealth) -> Option<&str> {
    match health {
        AdapterHealth::Healthy => None,
        AdapterHealth::Degraded { reason }
        | AdapterHealth::Recoverable { reason }
        | AdapterHealth::Permission { reason }
        | AdapterHealth::Incompatible { reason }
        | AdapterHealth::PermanentlyDisabled { reason } => Some(reason),
    }
}

#[cfg(test)]
mod tests {
    use adapter_sdk::{Capability, CapabilityReport};

    use super::*;

    fn runtime_with(available: &[Capability], declared: &[Capability]) -> AdapterRuntime {
        let mut runtime = AdapterRuntime::new("dev.tokenshow.adapter.example", "1.0.0");
        runtime.detected = true;
        runtime.capability = CapabilityReport::from_declared(
            runtime.adapter_id.clone(),
            runtime.adapter_version.clone(),
            declared,
            available,
        );
        runtime
    }

    #[test]
    fn undetected_when_probe_misses() {
        let runtime = AdapterRuntime::new("dev.tokenshow.adapter.example", "1.0.0");
        assert_eq!(
            derive_status(&runtime, None),
            AdapterRuntimeStatus::Undetected
        );
    }

    #[test]
    fn active_when_detected_and_full_capability() {
        let runtime = runtime_with(&[Capability::Sessions], &[Capability::Sessions]);
        assert_eq!(derive_status(&runtime, None), AdapterRuntimeStatus::Active);
    }

    #[test]
    fn degraded_when_capability_partial() {
        let runtime = runtime_with(
            &[Capability::Sessions],
            &[Capability::Sessions, Capability::Tokens],
        );
        assert_eq!(
            derive_status(&runtime, None),
            AdapterRuntimeStatus::Degraded
        );
    }

    #[test]
    fn panic_maps_to_error_and_disable_is_sticky() {
        let mut runtime = runtime_with(&[Capability::Sessions], &[Capability::Sessions]);
        runtime.last_error_code = Some(ErrorCode::AdapterPanic);
        assert_eq!(derive_status(&runtime, None), AdapterRuntimeStatus::Error);
        runtime.disable();
        assert_eq!(runtime.status, AdapterRuntimeStatus::Disabled);
        runtime.enable();
        runtime.last_error_code = None;
        runtime.recompute(None);
        assert_eq!(runtime.status, AdapterRuntimeStatus::Active);
    }
}
