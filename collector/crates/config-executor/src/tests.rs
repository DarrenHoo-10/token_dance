use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};

use adapter_sdk::{ConfigMutation, RollbackStep, SetupPlan, SetupPlanStatus, VerifyStep};
use serde_json::json;
use tempfile::tempdir;

use super::{
    sha256, EncryptedBackupStore, ExecutorError, SemanticVerifier, SetupPlanExecutor, SetupVerifier,
};

#[derive(Default)]
struct XorBackupStore {
    encrypted: Vec<Vec<u8>>,
}

impl EncryptedBackupStore for XorBackupStore {
    type Handle = usize;

    fn save_encrypted(
        &mut self,
        _plan_id: &str,
        _path: &Path,
        _before_hash: &str,
        plaintext: &[u8],
    ) -> Result<Self::Handle, String> {
        let ciphertext = plaintext.iter().map(|byte| byte ^ 0xa5).collect();
        self.encrypted.push(ciphertext);
        Ok(self.encrypted.len() - 1)
    }

    fn restore_decrypted(&mut self, handle: &Self::Handle) -> Result<Vec<u8>, String> {
        self.encrypted
            .get(*handle)
            .ok_or_else(|| "unknown backup".to_string())
            .map(|ciphertext| ciphertext.iter().map(|byte| byte ^ 0xa5).collect())
    }
}

#[derive(Default)]
struct PassVerifier;

impl SetupVerifier for PassVerifier {
    fn verify(&mut self, _step: &VerifyStep) -> Result<(), String> {
        Ok(())
    }
}

struct FileVerifier {
    path: PathBuf,
}

impl SetupVerifier for FileVerifier {
    fn verify(&mut self, _step: &VerifyStep) -> Result<(), String> {
        let bytes = fs::read(&self.path).map_err(|error| error.to_string())?;
        serde_json::from_slice::<serde_json::Value>(&bytes).map_err(|error| error.to_string())?;
        let entries = fs::read_dir(self.path.parent().unwrap())
            .map_err(|error| error.to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|error| error.to_string())?;
        if entries.len() != 1 {
            return Err("same-directory temporary file was not cleaned up".into());
        }
        Ok(())
    }
}

struct FailVerifier;

impl SetupVerifier for FailVerifier {
    fn verify(&mut self, _step: &VerifyStep) -> Result<(), String> {
        Err("agent rejected configuration".into())
    }
}

struct ConcurrentEditVerifier {
    path: PathBuf,
    replacement: Vec<u8>,
}

impl SetupVerifier for ConcurrentEditVerifier {
    fn verify(&mut self, _step: &VerifyStep) -> Result<(), String> {
        fs::write(&self.path, &self.replacement).map_err(|error| error.to_string())?;
        Err("configuration changed concurrently".into())
    }
}

struct BrokenRestoreStore;

impl EncryptedBackupStore for BrokenRestoreStore {
    type Handle = ();

    fn save_encrypted(
        &mut self,
        _plan_id: &str,
        _path: &Path,
        _before_hash: &str,
        _plaintext: &[u8],
    ) -> Result<Self::Handle, String> {
        Ok(())
    }

    fn restore_decrypted(&mut self, _handle: &Self::Handle) -> Result<Vec<u8>, String> {
        Err("encrypted backup unavailable".into())
    }
}

#[derive(Default)]
struct AllBrokenRestoreStore {
    saved: usize,
}

impl EncryptedBackupStore for AllBrokenRestoreStore {
    type Handle = usize;

    fn save_encrypted(
        &mut self,
        _plan_id: &str,
        _path: &Path,
        _before_hash: &str,
        _plaintext: &[u8],
    ) -> Result<Self::Handle, String> {
        let handle = self.saved;
        self.saved += 1;
        Ok(handle)
    }

    fn restore_decrypted(&mut self, handle: &Self::Handle) -> Result<Vec<u8>, String> {
        Err(format!("restore failure {handle}"))
    }
}

fn roots(root: &Path) -> HashMap<String, PathBuf> {
    HashMap::from([("AGENT_CONFIG_HOME".into(), root.to_path_buf())])
}

fn json_plan(patch: serde_json::Value) -> SetupPlan {
    SetupPlan {
        plan_id: "plan-real-fs".into(),
        adapter_id: "adapter-test".into(),
        summary: "configure adapter".into(),
        mutations: vec![ConfigMutation::JsonMergePatch {
            path_template: "${AGENT_CONFIG_HOME}/config.json".into(),
            patch,
        }],
        required_permissions: vec![],
        verify: vec![VerifyStep {
            id: "syntax".into(),
            summary: "configuration is accepted".into(),
        }],
        rollback: vec![RollbackStep {
            id: "restore".into(),
            summary: "restore encrypted backup".into(),
        }],
    }
}

#[test]
fn tempfile_merge_preserves_existing_configuration_and_records_before_hash() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("config.json");
    let original = br#"{
  "unrelated": "keep-me",
  "collector": { "enabled": false, "endpoint": "local" }
}
"#;
    fs::write(&path, original).unwrap();
    let plan = json_plan(json!({"collector": {"enabled": true}}));
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        PassVerifier,
    );

    executor.approve(&plan).unwrap();
    let report = executor.apply(&plan).unwrap();

    let configured: serde_json::Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
    assert_eq!(configured["unrelated"], "keep-me");
    assert_eq!(configured["collector"]["endpoint"], "local");
    assert_eq!(configured["collector"]["enabled"], true);
    let expected_before_hash = sha256(original);
    assert_eq!(
        report.mutations[0].before_hash.as_deref(),
        Some(expected_before_hash.as_str())
    );
    assert_ne!(executor.backup_store().encrypted[0], original);
    assert_eq!(
        executor.history(),
        &[
            SetupPlanStatus::Proposed,
            SetupPlanStatus::Approved,
            SetupPlanStatus::Applying,
            SetupPlanStatus::Verifying,
            SetupPlanStatus::Applied,
        ]
    );
}

#[test]
fn tempfile_replacement_is_same_directory_atomic_and_syntax_valid() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("config.json");
    fs::write(&path, b"{\"enabled\":false}\n").unwrap();
    let plan = json_plan(json!({"enabled": true}));
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        FileVerifier { path: path.clone() },
    );

    executor.approve(&plan).unwrap();
    executor.apply(&plan).unwrap();

    assert_eq!(executor.status(), SetupPlanStatus::Applied);
    let configured: serde_json::Value = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
    assert_eq!(configured["enabled"], true);
}

#[test]
fn tempfile_verification_failure_restores_exact_original() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("config.json");
    let original = b"{\"enabled\":false,\"preserve\":\"exact bytes\"}\n";
    fs::write(&path, original).unwrap();
    let plan = json_plan(json!({"enabled": true}));
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        FailVerifier,
    );

    executor.approve(&plan).unwrap();
    let error = executor.apply(&plan).unwrap_err();

    assert!(matches!(error, ExecutorError::AppliedThenRolledBack(_)));
    assert_eq!(fs::read(path).unwrap(), original);
    assert_eq!(executor.status(), SetupPlanStatus::RolledBack);
    assert_eq!(
        executor.history(),
        &[
            SetupPlanStatus::Proposed,
            SetupPlanStatus::Approved,
            SetupPlanStatus::Applying,
            SetupPlanStatus::Verifying,
            SetupPlanStatus::RollingBack,
            SetupPlanStatus::RolledBack,
        ]
    );
}

#[test]
fn rollback_failure_ends_in_failed_state() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("config.json");
    fs::write(&path, b"{\"enabled\":false}\n").unwrap();
    let plan = json_plan(json!({"enabled": true}));
    let mut executor =
        SetupPlanExecutor::new(roots(directory.path()), BrokenRestoreStore, FailVerifier);

    executor.approve(&plan).unwrap();
    let error = executor.apply(&plan).unwrap_err();

    assert!(matches!(error, ExecutorError::RollbackFailed { .. }));
    assert_eq!(executor.status(), SetupPlanStatus::Failed);
    assert_eq!(
        &executor.history()[executor.history().len() - 2..],
        &[SetupPlanStatus::RollingBack, SetupPlanStatus::Failed]
    );
}

#[test]
fn rejects_environment_and_path_escape_before_writing() {
    let directory = tempdir().unwrap();
    let mut environment_plan = json_plan(json!({"enabled": true}));
    environment_plan.mutations = vec![ConfigMutation::EnvironmentSet {
        scope: "user".into(),
        key: "TOKEN".into(),
        value_ref: "secret".into(),
    }];
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        PassVerifier,
    );
    executor.approve(&environment_plan).unwrap();
    assert!(matches!(
        executor.apply(&environment_plan),
        Err(ExecutorError::AppliedThenRolledBack(message))
            if message.contains("EnvironmentSet")
    ));

    let mut escape_plan = json_plan(json!({"enabled": true}));
    escape_plan.mutations = vec![ConfigMutation::JsonMergePatch {
        path_template: "${AGENT_CONFIG_HOME}/../outside.json".into(),
        patch: json!({"enabled": true}),
    }];
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        PassVerifier,
    );
    executor.approve(&escape_plan).unwrap();
    assert!(matches!(
        executor.apply(&escape_plan),
        Err(ExecutorError::AppliedThenRolledBack(message)) if message.contains("unsafe")
    ));
    assert!(!directory
        .path()
        .parent()
        .unwrap()
        .join("outside.json")
        .exists());
}

#[test]
fn multi_file_half_failure_rolls_back_every_applied_mutation() {
    let directory = tempdir().unwrap();
    let first_path = directory.path().join("config.json");
    let blocked_directory = directory.path().join("not-a-directory");
    let original = b"{\"enabled\":false}\n";
    fs::write(&first_path, original).unwrap();
    fs::write(&blocked_directory, b"occupied").unwrap();
    let mut plan = json_plan(json!({"enabled": true}));
    plan.mutations.push(ConfigMutation::DirectoryCreate {
        path_template: "${AGENT_CONFIG_HOME}/not-a-directory".into(),
    });
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        PassVerifier,
    );

    executor.approve(&plan).unwrap();
    let error = executor.apply(&plan).unwrap_err();

    assert!(matches!(error, ExecutorError::AppliedThenRolledBack(_)));
    assert_eq!(fs::read(first_path).unwrap(), original);
    assert_eq!(fs::read(blocked_directory).unwrap(), b"occupied");
    assert_eq!(executor.status(), SetupPlanStatus::RolledBack);
}

#[test]
fn rollback_attempts_all_applied_mutations_and_aggregates_errors() {
    let directory = tempdir().unwrap();
    let first_path = directory.path().join("config.json");
    let second_path = directory.path().join("second.json");
    fs::write(&first_path, b"{\"enabled\":false}\n").unwrap();
    fs::write(&second_path, b"{\"enabled\":false}\n").unwrap();
    let mut plan = json_plan(json!({"enabled": true}));
    plan.mutations.push(ConfigMutation::JsonMergePatch {
        path_template: "${AGENT_CONFIG_HOME}/second.json".into(),
        patch: json!({"enabled": true}),
    });
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        AllBrokenRestoreStore::default(),
        FailVerifier,
    );

    executor.approve(&plan).unwrap();
    let error = executor.apply(&plan).unwrap_err();

    assert!(matches!(
        error,
        ExecutorError::RollbackFailed { rollback_error, .. }
            if rollback_error.contains("restore failure 0")
                && rollback_error.contains("restore failure 1")
    ));
    assert_eq!(executor.status(), SetupPlanStatus::Failed);
}

#[test]
fn concurrent_user_edit_is_preserved_and_marks_rollback_failed() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("config.json");
    let user_edit = b"{\"enabled\":true,\"user\":\"new change\"}\n".to_vec();
    fs::write(&path, b"{\"enabled\":false}\n").unwrap();
    let plan = json_plan(json!({"enabled": true}));
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        ConcurrentEditVerifier {
            path: path.clone(),
            replacement: user_edit.clone(),
        },
    );

    executor.approve(&plan).unwrap();
    let error = executor.apply(&plan).unwrap_err();

    assert!(matches!(
        error,
        ExecutorError::RollbackFailed { rollback_error, .. }
            if rollback_error.contains("rollback conflict")
                && rollback_error.contains("planned after hash")
    ));
    assert_eq!(fs::read(path).unwrap(), user_edit);
    assert_eq!(executor.status(), SetupPlanStatus::Failed);
}

#[test]
fn existing_otlp_endpoint_conflict_is_explicit_and_not_overwritten() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("config.json");
    let original = br#"{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"http://127.0.0.1:9999"}}
"#;
    fs::write(&path, original).unwrap();
    let plan = json_plan(json!({
        "env": {
            "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
            "OTEL_LOG_USER_PROMPTS": "0",
            "OTEL_LOG_TOOL_CONTENT": "0"
        }
    }));
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        SemanticVerifier::default(),
    );

    executor.approve(&plan).unwrap();
    let error = executor.apply(&plan).unwrap_err();

    assert!(matches!(
        error,
        ExecutorError::EndpointConflict {
            key,
            existing,
            requested,
            ..
        } if key == "env.OTEL_EXPORTER_OTLP_ENDPOINT"
            && existing.contains("9999")
            && requested.contains("4318")
    ));
    assert_eq!(fs::read(path).unwrap(), original);
    assert_eq!(executor.status(), SetupPlanStatus::Failed);
}

#[test]
fn semantic_verifier_rereads_endpoint_and_rejects_enabled_content_flags() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("config.json");
    let plan = json_plan(json!({
        "env": {
            "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
            "OTEL_LOG_USER_PROMPTS": "1",
            "OTEL_LOG_TOOL_CONTENT": "0"
        }
    }));
    let mut executor = SetupPlanExecutor::new(
        roots(directory.path()),
        XorBackupStore::default(),
        SemanticVerifier::default(),
    );

    executor.approve(&plan).unwrap();
    let error = executor.apply(&plan).unwrap_err();

    assert!(matches!(
        error,
        ExecutorError::AppliedThenRolledBack(message)
            if message.contains("semantic verification failed")
                && message.contains("OTEL_LOG_USER_PROMPTS")
    ));
    assert!(!path.exists());
    assert_eq!(executor.status(), SetupPlanStatus::RolledBack);
}
