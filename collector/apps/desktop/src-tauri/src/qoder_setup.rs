//! Auto-enable Qoder token exposure after detection (design doc §4.3).
//!
//! When Qoder is detected, the desktop executes the adapter's declarative
//! setup plan through the audited config-executor (backup → apply → read-back
//! verify → rollback on failure). No approval gate: the product decision
//! treats a user-scope environment variable as a non-dangerous permission;
//! the user is notified after the write, never asked before it.
//!
//! Platform boundary: Windows writes `HKCU\Environment` and broadcasts the
//! change. macOS GUI apps cannot persistently inject user environment
//! variables that Qoder's shell children would inherit, so setup stays
//! suggest-only there and nothing is written.

use std::collections::HashMap;

use collector_service::{DetectionSnapshot, OfficialAgent, ProductionService};
use config_executor::{SemanticVerifier, SetupPlanExecutor, UserEnvironmentStore};

#[cfg(windows)]
use config_executor::WindowsUserEnvironmentStore;

use adapter_sdk::{AdapterError, ConfigMutation};

/// Matches `("qoder", …)` in `state.rs::agent_metadata`.
pub const QODER_ADAPTER_ID: &str = "dev.tokenshow.adapter.qoder";
pub const EXPOSE_ENV_KEY: &str = "QODER_EXPOSE_TOKEN_USAGE";

/// Qoder's runtime accepts "1" or "true" as enabled.
fn is_enabled_value(value: &str) -> bool {
    value == "true" || value == "1"
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum QoderEnvOutcome {
    /// Qoder is not installed; nothing to do.
    NotDetected,
    /// The variable is already enabled in the user environment.
    AlreadyEnabled,
    /// The plan was applied and read-back verified this launch.
    Applied,
    /// This platform cannot persistently set the variable; suggest-only mode.
    SuggestOnly,
    /// The apply (or its verification/rollback) failed; retried next launch.
    Failed(String),
}

/// Production entry point: Windows writes the variable, other platforms stay
/// suggest-only per the design's platform boundary.
pub async fn ensure_exposed(
    service: &mut ProductionService,
    detection: &DetectionSnapshot,
) -> QoderEnvOutcome {
    #[cfg(windows)]
    {
        ensure_exposed_with(
            Box::new(WindowsUserEnvironmentStore),
            service,
            detection,
        )
        .await
    }
    #[cfg(not(windows))]
    {
        let _ = (service, detection);
        QoderEnvOutcome::SuggestOnly
    }
}

/// Testable core: the environment store is injected so tests never touch the
/// real `HKCU\Environment` key.
pub async fn ensure_exposed_with(
    environment: Box<dyn UserEnvironmentStore>,
    service: &mut ProductionService,
    detection: &DetectionSnapshot,
) -> QoderEnvOutcome {
    if !detection.is_installed(OfficialAgent::Qoder) {
        return QoderEnvOutcome::NotDetected;
    }
    match environment.read(EXPOSE_ENV_KEY) {
        Ok(Some(value)) if is_enabled_value(&value) => return QoderEnvOutcome::AlreadyEnabled,
        Ok(_) => {}
        Err(message) => return QoderEnvOutcome::Failed(format!("read {EXPOSE_ENV_KEY}: {message}")),
    }

    let plan = match service.collector.propose_setup(QODER_ADAPTER_ID).await {
        Ok(plan) => plan,
        Err(error) => return QoderEnvOutcome::Failed(format!("propose setup: {error}")),
    };
    // Defense in depth: only execute the shape this desktop build knows how to
    // notify about — exactly one user-scope write of the exposure flag.
    let expected = plan.mutations.len() == 1
        && matches!(
            plan.mutations.first(),
            Some(ConfigMutation::EnvironmentSet { scope, key, .. })
                if scope.eq_ignore_ascii_case("user") && key == EXPOSE_ENV_KEY
        );
    if !expected {
        let error = AdapterError::setup_failed("unexpected qoder setup plan shape");
        let _ = service
            .collector
            .finish_configure_err(QODER_ADAPTER_ID, &error);
        return QoderEnvOutcome::Failed("unexpected qoder setup plan shape".into());
    }

    let _ = service.collector.begin_configure(QODER_ADAPTER_ID);
    let mut executor = SetupPlanExecutor::new(
        HashMap::new(),
        crate::state::MemoryBackupStore::default(),
        SemanticVerifier::default(),
    )
    .with_boxed_environment(environment);
    let result = executor
        .approve(&plan)
        .and_then(|()| executor.apply(&plan));
    match result {
        Ok(_) => {
            let _ = service.collector.finish_configure_ok(QODER_ADAPTER_ID);
            QoderEnvOutcome::Applied
        }
        Err(error) => {
            let adapter_error = AdapterError::setup_failed(error.to_string());
            let _ = service
                .collector
                .finish_configure_err(QODER_ADAPTER_ID, &adapter_error);
            QoderEnvOutcome::Failed(error.to_string())
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use std::sync::{Arc, Mutex};

    use adapter_sdk::SetupPlanStatus;
    use collector_service::AgentDetection;
    use wal_spool::{InjectedKeyProvider, SpoolLimits, WalStore};

    #[derive(Clone, Default)]
    struct FakeEnvironment {
        values: Arc<Mutex<HashMap<String, String>>>,
        fail_writes: bool,
    }

    impl UserEnvironmentStore for FakeEnvironment {
        fn read(&self, key: &str) -> Result<Option<String>, String> {
            Ok(self.values.lock().unwrap().get(key).cloned())
        }

        fn write(&self, key: &str, value: &str) -> Result<(), String> {
            if self.fail_writes {
                return Err("registry unavailable".into());
            }
            self.values
                .lock()
                .unwrap()
                .insert(key.into(), value.into());
            Ok(())
        }

        fn delete(&self, key: &str) -> Result<(), String> {
            self.values.lock().unwrap().remove(key);
            Ok(())
        }
    }

    fn qoder_detection() -> DetectionSnapshot {
        DetectionSnapshot::default()
            .with(OfficialAgent::Qoder, AgentDetection::installed("1.1.75"))
    }

    async fn service() -> ProductionService {
        let dir = tempfile::tempdir().unwrap();
        let wal = WalStore::open_with_limits(
            dir.path().join("spool"),
            Arc::new(InjectedKeyProvider::new([0x51; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        ProductionService::assemble(
            "ins_qoder_setup_test",
            b"qoder-setup-hmac-key!!",
            &qoder_detection(),
            Arc::new(crate::state::EmptySecrets),
            wal,
        )
        .await
        .unwrap()
    }

    #[tokio::test]
    async fn undetected_qoder_is_a_no_op() {
        let mut service = service().await;
        let outcome = ensure_exposed_with(
            Box::new(FakeEnvironment::default()),
            &mut service,
            &DetectionSnapshot::default(),
        )
        .await;
        assert_eq!(outcome, QoderEnvOutcome::NotDetected);
        assert_eq!(
            service
                .collector
                .runtime(QODER_ADAPTER_ID)
                .and_then(|runtime| runtime.setup_plan_status),
            None
        );
    }

    #[tokio::test]
    async fn already_enabled_value_skips_the_executor() {
        let mut service = service().await;
        let environment = FakeEnvironment::default();
        environment
            .values
            .lock()
            .unwrap()
            .insert(EXPOSE_ENV_KEY.into(), "true".into());
        let outcome =
            ensure_exposed_with(Box::new(environment), &mut service, &qoder_detection()).await;
        assert_eq!(outcome, QoderEnvOutcome::AlreadyEnabled);
    }

    #[tokio::test]
    async fn applying_the_plan_writes_and_marks_the_runtime_applied() {
        let mut service = service().await;
        let environment = FakeEnvironment::default();
        let outcome = ensure_exposed_with(
            Box::new(environment.clone()),
            &mut service,
            &qoder_detection(),
        )
        .await;
        assert_eq!(outcome, QoderEnvOutcome::Applied);
        assert_eq!(
            environment
                .values
                .lock()
                .unwrap()
                .get(EXPOSE_ENV_KEY)
                .map(String::as_str),
            Some("true")
        );
        assert_eq!(
            service
                .collector
                .runtime(QODER_ADAPTER_ID)
                .and_then(|runtime| runtime.setup_plan_status),
            Some(SetupPlanStatus::Applied)
        );
    }

    #[tokio::test]
    async fn failing_write_rolls_back_and_marks_the_runtime_failed() {
        let mut service = service().await;
        let environment = FakeEnvironment {
            fail_writes: true,
            ..FakeEnvironment::default()
        };
        let outcome =
            ensure_exposed_with(Box::new(environment), &mut service, &qoder_detection()).await;
        assert!(matches!(outcome, QoderEnvOutcome::Failed(_)));
        assert_eq!(
            service
                .collector
                .runtime(QODER_ADAPTER_ID)
                .and_then(|runtime| runtime.setup_plan_status),
            Some(SetupPlanStatus::Failed)
        );
    }
}
