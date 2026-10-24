use std::collections::{HashMap, HashSet};
use std::fs;
use std::io::{self, Write};
use std::path::{Component, Path, PathBuf};

use adapter_sdk::{ConfigMutation, SetupPlan, SetupPlanStatus, VerifyStep};
use serde_json::Value as JsonValue;
use sha2::{Digest, Sha256};
use tempfile::NamedTempFile;
use thiserror::Error;

pub trait EncryptedBackupStore {
    type Handle: Clone;

    fn save_encrypted(
        &mut self,
        plan_id: &str,
        path: &Path,
        before_hash: &str,
        plaintext: &[u8],
    ) -> Result<Self::Handle, String>;

    fn restore_decrypted(&mut self, handle: &Self::Handle) -> Result<Vec<u8>, String>;
}

pub trait SetupVerifier {
    fn configure(&mut self, _targets: &[SemanticTarget]) -> Result<(), String> {
        Ok(())
    }

    fn verify_semantics(&mut self) -> Result<(), String> {
        Ok(())
    }

    fn verify(&mut self, step: &VerifyStep) -> Result<(), String>;
}

/// Read/write access to the user's *persistent* environment (the store new
/// processes inherit). This is deliberately not `std::env`: the executor only
/// ever mutates the user-scoped store — on Windows `HKCU\Environment` plus a
/// `WM_SETTINGCHANGE` broadcast. Non-Windows builds keep a rejecting default
/// because macOS GUI apps do not inherit shell environment changes.
///
/// `Send` because executor state crosses async boundaries.
pub trait UserEnvironmentStore: Send {
    fn read(&self, key: &str) -> Result<Option<String>, String>;
    fn write(&self, key: &str, value: &str) -> Result<(), String>;
    fn delete(&self, key: &str) -> Result<(), String>;
}

/// Default store for platforms without a persistent user environment that GUI
/// and CLI children both inherit. Every operation reports the platform limit
/// so `EnvironmentSet` plans fail (and roll back) loudly instead of silently
/// no-op'ing.
#[derive(Debug, Default)]
pub struct UnsupportedUserEnvironmentStore;

impl UserEnvironmentStore for UnsupportedUserEnvironmentStore {
    fn read(&self, _key: &str) -> Result<Option<String>, String> {
        Err("user environment writes are only supported on Windows".into())
    }

    fn write(&self, _key: &str, _value: &str) -> Result<(), String> {
        Err("user environment writes are only supported on Windows".into())
    }

    fn delete(&self, _key: &str) -> Result<(), String> {
        Err("user environment writes are only supported on Windows".into())
    }
}

#[cfg(windows)]
#[derive(Debug, Default)]
pub struct WindowsUserEnvironmentStore;

#[cfg(windows)]
impl UserEnvironmentStore for WindowsUserEnvironmentStore {
    fn read(&self, key: &str) -> Result<Option<String>, String> {
        windows_registry::read_user_string(key)
    }

    fn write(&self, key: &str, value: &str) -> Result<(), String> {
        windows_registry::set_user_string(key, value)?;
        windows_registry::broadcast_environment_change();
        Ok(())
    }

    fn delete(&self, key: &str) -> Result<(), String> {
        windows_registry::delete_user_value(key)?;
        windows_registry::broadcast_environment_change();
        Ok(())
    }
}

#[cfg(windows)]
mod windows_registry {
    use std::io;

    const HKEY_CURRENT_USER: isize = 0x8000_0001;
    const RRF_RT_REG_SZ: u32 = 0x0000_0002;
    const REG_SZ: u32 = 1;
    const WM_SETTINGCHANGE: u32 = 0x001A;
    const HWND_BROADCAST: isize = 0xFFFF;
    const SMTO_ABORTIFHUNG: u32 = 0x0002;

    #[link(name = "Advapi32")]
    extern "system" {
        fn RegGetValueW(
            hkey: isize,
            lpsubkey: *const u16,
            lpvalue: *const u16,
            dwflags: u32,
            pdwtype: *mut u32,
            pvdata: *mut u8,
            pcbdata: *mut u32,
        ) -> i32;
        fn RegSetKeyValueW(
            hkey: isize,
            lpsubkey: *const u16,
            lpvalue: *const u16,
            dwtype: u32,
            pvdata: *const u8,
            cbdata: u32,
        ) -> i32;
        fn RegDeleteKeyValueW(hkey: isize, lpsubkey: *const u16, lpvalue: *const u16) -> i32;
    }

    #[link(name = "User32")]
    extern "system" {
        fn SendMessageTimeoutW(
            hwnd: isize,
            msg: u32,
            wparam: usize,
            lparam: isize,
            fuflags: u32,
            utimeout: u32,
            lresult: *mut usize,
        ) -> isize;
    }

    fn wide(value: &str) -> Vec<u16> {
        value.encode_utf16().chain(Some(0)).collect()
    }

    fn check(status: i32, operation: &str) -> Result<(), String> {
        if status == 0 {
            Ok(())
        } else {
            Err(format!(
                "{operation} failed: {}",
                io::Error::from_raw_os_error(status)
            ))
        }
    }

    pub fn read_user_string(key: &str) -> Result<Option<String>, String> {
        let subkey = wide("Environment");
        let value = wide(key);
        let mut capacity = 1024usize;
        loop {
            let mut buffer = Vec::<u16>::with_capacity(capacity);
            let mut size = (capacity * 2) as u32;
            let mut value_type = 0u32;
            let status = unsafe {
                RegGetValueW(
                    HKEY_CURRENT_USER,
                    subkey.as_ptr(),
                    value.as_ptr(),
                    RRF_RT_REG_SZ,
                    &mut value_type,
                    buffer.as_mut_ptr().cast(),
                    &mut size,
                )
            };
            if status == 2 {
                // ERROR_FILE_NOT_FOUND: the variable is not set.
                return Ok(None);
            }
            if status == 234 {
                // ERROR_MORE_DATA: retry with the reported size.
                capacity = (size as usize / 2).max(capacity + 1);
                continue;
            }
            check(status, "RegGetValueW")?;
            let units = (size as usize / 2).min(capacity);
            unsafe { buffer.set_len(units) };
            let end = buffer.iter().position(|&unit| unit == 0).unwrap_or(units);
            return Ok(Some(String::from_utf16_lossy(&buffer[..end])));
        }
    }

    pub fn set_user_string(key: &str, value: &str) -> Result<(), String> {
        let subkey = wide("Environment");
        let value_name = wide(key);
        let mut data = value.encode_utf16().collect::<Vec<u16>>();
        data.push(0);
        let status = unsafe {
            RegSetKeyValueW(
                HKEY_CURRENT_USER,
                subkey.as_ptr(),
                value_name.as_ptr(),
                REG_SZ,
                data.as_ptr().cast(),
                (data.len() * 2) as u32,
            )
        };
        check(status, "RegSetKeyValueW")
    }

    pub fn delete_user_value(key: &str) -> Result<(), String> {
        let subkey = wide("Environment");
        let value_name = wide(key);
        let status =
            unsafe { RegDeleteKeyValueW(HKEY_CURRENT_USER, subkey.as_ptr(), value_name.as_ptr()) };
        if status == 2 {
            // Already absent — deleting is idempotent.
            return Ok(());
        }
        check(status, "RegDeleteKeyValueW")
    }

    pub fn broadcast_environment_change() {
        let environment = wide("Environment");
        unsafe {
            SendMessageTimeoutW(
                HWND_BROADCAST,
                WM_SETTINGCHANGE,
                0,
                environment.as_ptr() as isize,
                SMTO_ABORTIFHUNG,
                5_000,
                std::ptr::null_mut(),
            );
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct SemanticTarget {
    path: PathBuf,
    kind: ConfigKind,
    endpoints: HashMap<String, JsonValue>,
    disabled_content_flags: HashMap<String, JsonValue>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ConfigKind {
    Json,
    Toml,
}

#[derive(Debug, Default)]
pub struct SemanticVerifier {
    targets: Vec<SemanticTarget>,
}

impl SetupVerifier for SemanticVerifier {
    fn configure(&mut self, targets: &[SemanticTarget]) -> Result<(), String> {
        self.targets = targets.to_vec();
        Ok(())
    }

    fn verify_semantics(&mut self) -> Result<(), String> {
        for target in &self.targets {
            verify_semantic_target(target)?;
        }
        Ok(())
    }

    fn verify(&mut self, _step: &VerifyStep) -> Result<(), String> {
        self.verify_semantics()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AppliedMutation {
    pub path: PathBuf,
    pub before_hash: Option<String>,
    pub after_hash: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ApplyReport {
    pub plan_id: String,
    pub mutations: Vec<AppliedMutation>,
}

#[derive(Debug, Error)]
pub enum ExecutorError {
    #[error("invalid setup state: expected {expected:?}, found {actual:?}")]
    InvalidState {
        expected: SetupPlanStatus,
        actual: SetupPlanStatus,
    },
    #[error("the applied plan differs from the approved plan")]
    PlanChanged,
    #[error("unsafe configuration path `{0}`")]
    UnsafePath(String),
    #[error("unsupported environment scope `{0}` (only `user` is supported)")]
    UnsupportedEnvironmentScope(String),
    #[error("unsupported environment value reference `{0}` (only literal `value://` references are supported)")]
    UnsupportedValueRef(String),
    #[error("invalid environment variable name `{0}`")]
    InvalidEnvironmentKey(String),
    #[error("user environment operation failed for `{key}`: {message}")]
    EnvironmentIo { key: String, message: String },
    #[error("duplicate mutation target `{0}`")]
    DuplicateTarget(PathBuf),
    #[error(
        "OTLP endpoint conflict in `{path}` at `{key}`: existing {existing}, requested {requested}"
    )]
    EndpointConflict {
        path: PathBuf,
        key: String,
        existing: String,
        requested: String,
    },
    #[error("invalid {kind} configuration for `{path}`: {message}")]
    InvalidSyntax {
        kind: &'static str,
        path: PathBuf,
        message: String,
    },
    #[error("backup failed for `{path}`: {message}")]
    Backup { path: PathBuf, message: String },
    #[error("configuration I/O failed for `{path}`: {source}")]
    Io {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("setup failed and was rolled back: {0}")]
    AppliedThenRolledBack(String),
    #[error("setup failed: {apply_error}; rollback also failed: {rollback_error}")]
    RollbackFailed {
        apply_error: String,
        rollback_error: String,
    },
}

pub struct SetupPlanExecutor<B, V> {
    roots: HashMap<String, PathBuf>,
    backup_store: B,
    verifier: V,
    environment: Box<dyn UserEnvironmentStore>,
    status: SetupPlanStatus,
    history: Vec<SetupPlanStatus>,
    approved_plan_hash: Option<String>,
}

impl<B, V> SetupPlanExecutor<B, V>
where
    B: EncryptedBackupStore,
    V: SetupVerifier,
{
    pub fn new(roots: HashMap<String, PathBuf>, backup_store: B, verifier: V) -> Self {
        Self {
            roots,
            backup_store,
            verifier,
            #[cfg(windows)]
            environment: Box::new(WindowsUserEnvironmentStore),
            #[cfg(not(windows))]
            environment: Box::new(UnsupportedUserEnvironmentStore),
            status: SetupPlanStatus::Proposed,
            history: vec![SetupPlanStatus::Proposed],
            approved_plan_hash: None,
        }
    }

    /// Swap the user-environment store (tests inject an in-memory store so the
    /// real `HKCU\Environment` key is never touched).
    pub fn with_environment(
        mut self,
        environment: impl UserEnvironmentStore + 'static,
    ) -> Self {
        self.environment = Box::new(environment);
        self
    }

    /// Boxed variant for callers that already hold a trait object.
    pub fn with_boxed_environment(
        mut self,
        environment: Box<dyn UserEnvironmentStore>,
    ) -> Self {
        self.environment = environment;
        self
    }

    pub fn status(&self) -> SetupPlanStatus {
        self.status
    }

    pub fn history(&self) -> &[SetupPlanStatus] {
        &self.history
    }

    pub fn backup_store(&self) -> &B {
        &self.backup_store
    }

    pub fn approve(&mut self, plan: &SetupPlan) -> Result<(), ExecutorError> {
        self.require_status(SetupPlanStatus::Proposed)?;
        self.approved_plan_hash = Some(plan_hash(plan));
        self.transition(SetupPlanStatus::Approved);
        Ok(())
    }

    pub fn apply(&mut self, plan: &SetupPlan) -> Result<ApplyReport, ExecutorError> {
        self.require_status(SetupPlanStatus::Approved)?;
        if self.approved_plan_hash.as_deref() != Some(plan_hash(plan).as_str()) {
            return Err(ExecutorError::PlanChanged);
        }

        self.transition(SetupPlanStatus::Applying);
        let mut prepared = match self.prepare(plan) {
            Ok(prepared) => prepared,
            Err(error @ ExecutorError::EndpointConflict { .. }) => {
                self.transition(SetupPlanStatus::Failed);
                return Err(error);
            }
            Err(error) => return self.fail_and_rollback(error.to_string(), &mut [], 0),
        };

        if let Err(error) = self.back_up(plan, &mut prepared) {
            return self.fail_and_rollback(error.to_string(), &mut prepared, 0);
        }

        let mut applied_count = 0;
        for mutation in &prepared {
            if let Err(error) = apply_prepared(self.environment.as_ref(), mutation) {
                return self.fail_and_rollback(error.to_string(), &mut prepared, applied_count);
            }
            applied_count += 1;
        }

        self.transition(SetupPlanStatus::Verifying);
        let semantic_targets = prepared
            .iter()
            .filter_map(PreparedMutation::semantic_target)
            .collect::<Vec<_>>();
        if let Err(message) = self.verifier.configure(&semantic_targets) {
            return self.fail_and_rollback(
                format!("semantic verifier configuration failed: {message}"),
                &mut prepared,
                applied_count,
            );
        }
        if let Err(message) = self.verifier.verify_semantics() {
            return self.fail_and_rollback(
                format!("semantic verification failed: {message}"),
                &mut prepared,
                applied_count,
            );
        }
        for step in &plan.verify {
            if let Err(message) = self.verifier.verify(step) {
                return self.fail_and_rollback(
                    format!("verification `{}` failed: {message}", step.id),
                    &mut prepared,
                    applied_count,
                );
            }
        }

        self.transition(SetupPlanStatus::Applied);
        Ok(ApplyReport {
            plan_id: plan.plan_id.clone(),
            mutations: prepared.iter().map(PreparedMutation::report).collect(),
        })
    }

    fn prepare(&self, plan: &SetupPlan) -> Result<Vec<PreparedMutation<B::Handle>>, ExecutorError> {
        let mut targets = HashSet::new();
        let mut prepared = Vec::with_capacity(plan.mutations.len());
        for mutation in &plan.mutations {
            let item = match mutation {
                ConfigMutation::EnvironmentSet {
                    scope,
                    key,
                    value_ref,
                } => prepare_environment(self.environment.as_ref(), scope, key, value_ref)?,
                ConfigMutation::DirectoryCreate { path_template } => {
                    let path = self.resolve_path(path_template)?;
                    PreparedMutation::Directory {
                        existed: path.is_dir(),
                        path,
                    }
                }
                ConfigMutation::JsonMergePatch {
                    path_template,
                    patch,
                } => {
                    let path = self.resolve_path(path_template)?;
                    prepare_json(path, patch)?
                }
                ConfigMutation::TomlMergePatch {
                    path_template,
                    patch,
                } => {
                    let path = self.resolve_path(path_template)?;
                    prepare_toml(path, patch)?
                }
            };
            if !targets.insert(item.target_key()) {
                return Err(ExecutorError::DuplicateTarget(item.dedup_path()));
            }
            prepared.push(item);
        }
        Ok(prepared)
    }

    fn back_up(
        &mut self,
        plan: &SetupPlan,
        prepared: &mut [PreparedMutation<B::Handle>],
    ) -> Result<(), ExecutorError> {
        for mutation in prepared {
            match mutation {
                PreparedMutation::File {
                    path,
                    before,
                    before_hash: Some(before_hash),
                    backup,
                    ..
                } => {
                    let handle = self
                        .backup_store
                        .save_encrypted(&plan.plan_id, path, before_hash, before)
                        .map_err(|message| ExecutorError::Backup {
                            path: path.clone(),
                            message,
                        })?;
                    *backup = Some(handle);
                }
                PreparedMutation::Environment {
                    key,
                    previous: Some(previous),
                    backup,
                    ..
                } => {
                    let path = environment_report_path(key);
                    let before_hash = sha256(previous.as_bytes());
                    let handle = self
                        .backup_store
                        .save_encrypted(
                            &plan.plan_id,
                            &path,
                            &before_hash,
                            previous.as_bytes(),
                        )
                        .map_err(|message| ExecutorError::Backup {
                            path: path.clone(),
                            message,
                        })?;
                    *backup = Some(handle);
                }
                _ => {}
            }
        }
        Ok(())
    }

    fn resolve_path(&self, template: &str) -> Result<PathBuf, ExecutorError> {
        let Some(rest) = template.strip_prefix("${") else {
            return Err(ExecutorError::UnsafePath(template.into()));
        };
        let Some(close) = rest.find('}') else {
            return Err(ExecutorError::UnsafePath(template.into()));
        };
        let root_name = &rest[..close];
        let raw_suffix = &rest[close + 1..];
        if !raw_suffix.starts_with(['/', '\\']) {
            return Err(ExecutorError::UnsafePath(template.into()));
        }
        let suffix = raw_suffix.trim_start_matches(['/', '\\']);
        if root_name.is_empty() || suffix.is_empty() {
            return Err(ExecutorError::UnsafePath(template.into()));
        }
        let root = self
            .roots
            .get(root_name)
            .ok_or_else(|| ExecutorError::UnsafePath(template.into()))?;
        let canonical_root = root.canonicalize().map_err(|source| ExecutorError::Io {
            path: root.clone(),
            source,
        })?;
        let relative = Path::new(suffix);
        if relative.components().any(|component| {
            matches!(
                component,
                Component::ParentDir | Component::RootDir | Component::Prefix(_)
            )
        }) {
            return Err(ExecutorError::UnsafePath(template.into()));
        }
        let path = canonical_root.join(relative);
        let existing_anchor = if path.exists() {
            path.as_path()
        } else {
            path.parent()
                .ok_or_else(|| ExecutorError::UnsafePath(template.into()))?
        };
        let canonical_anchor =
            existing_anchor
                .canonicalize()
                .map_err(|source| ExecutorError::Io {
                    path: existing_anchor.to_path_buf(),
                    source,
                })?;
        if !canonical_anchor.starts_with(&canonical_root) {
            return Err(ExecutorError::UnsafePath(template.into()));
        }
        Ok(path)
    }

    fn fail_and_rollback<T>(
        &mut self,
        apply_error: String,
        prepared: &mut [PreparedMutation<B::Handle>],
        applied_count: usize,
    ) -> Result<T, ExecutorError> {
        self.transition(SetupPlanStatus::RollingBack);
        match self.rollback(&mut prepared[..applied_count]) {
            Ok(()) => {
                self.transition(SetupPlanStatus::RolledBack);
                Err(ExecutorError::AppliedThenRolledBack(apply_error))
            }
            Err(rollback_error) => {
                self.transition(SetupPlanStatus::Failed);
                Err(ExecutorError::RollbackFailed {
                    apply_error,
                    rollback_error,
                })
            }
        }
    }

    fn rollback(&mut self, prepared: &mut [PreparedMutation<B::Handle>]) -> Result<(), String> {
        let mut errors = Vec::new();
        for mutation in prepared.iter_mut().rev() {
            let result = match mutation {
                PreparedMutation::File {
                    path,
                    before_hash,
                    after_hash,
                    backup,
                    ..
                } => (|| {
                    let current = fs::read(&*path).map_err(|error| {
                        format!(
                            "could not read `{}` before rollback: {error}",
                            path.display()
                        )
                    })?;
                    let current_hash = sha256(&current);
                    if current_hash != *after_hash {
                        return Err(format!(
                            "rollback conflict for `{}`: current hash {current_hash}, planned after hash {after_hash}",
                            path.display()
                        ));
                    }
                    if let Some(expected_hash) = before_hash {
                        let handle = backup
                            .as_ref()
                            .ok_or_else(|| format!("missing backup for `{}`", path.display()))?;
                        let original =
                            self.backup_store
                                .restore_decrypted(handle)
                                .map_err(|error| {
                                    format!(
                                        "could not restore backup for `{}`: {error}",
                                        path.display()
                                    )
                                })?;
                        if sha256(&original) != *expected_hash {
                            return Err(format!("backup hash mismatch for `{}`", path.display()));
                        }
                        write_atomic(path, &original).map_err(|error| error.to_string())
                    } else {
                        fs::remove_file(&*path).map_err(|error| {
                            format!("could not remove `{}`: {error}", path.display())
                        })
                    }
                })(),
                PreparedMutation::Environment {
                    key,
                    value,
                    previous,
                    backup,
                } => (|| {
                    let environment = self.environment.as_ref();
                    let current = environment
                        .read(key)
                        .map_err(|error| format!("could not read `{key}` before rollback: {error}"))?;
                    if current.as_deref() != Some(value.as_str()) {
                        return Err(format!(
                            "rollback conflict for `{key}`: current {current:?}, planned {value:?}"
                        ));
                    }
                    match previous {
                        Some(original) => {
                            let handle = backup
                                .as_ref()
                                .ok_or_else(|| format!("missing backup for `{key}`"))?;
                            let bytes = self
                                .backup_store
                                .restore_decrypted(handle)
                                .map_err(|error| {
                                    format!("could not restore backup for `{key}`: {error}")
                                })?;
                            let restored = String::from_utf8(bytes)
                                .map_err(|error| format!("backup for `{key}` is not UTF-8: {error}"))?;
                            if &restored != original {
                                return Err(format!("backup mismatch for `{key}`"));
                            }
                            environment
                                .write(key, &restored)
                                .map_err(|error| format!("could not restore `{key}`: {error}"))
                        }
                        None => environment
                            .delete(key)
                            .map_err(|error| format!("could not remove `{key}`: {error}")),
                    }
                })(),
                PreparedMutation::Directory { path, existed } if !*existed => {
                    fs::remove_dir(&*path)
                        .map_err(|error| format!("could not remove `{}`: {error}", path.display()))
                }
                PreparedMutation::Directory { .. } => Ok(()),
            };
            if let Err(error) = result {
                errors.push(error);
            }
        }
        if errors.is_empty() {
            Ok(())
        } else {
            Err(errors.join("; "))
        }
    }

    fn require_status(&self, expected: SetupPlanStatus) -> Result<(), ExecutorError> {
        if self.status == expected {
            Ok(())
        } else {
            Err(ExecutorError::InvalidState {
                expected,
                actual: self.status,
            })
        }
    }

    fn transition(&mut self, status: SetupPlanStatus) {
        self.status = status;
        self.history.push(status);
    }
}

enum PreparedMutation<H> {
    File {
        path: PathBuf,
        kind: ConfigKind,
        before: Vec<u8>,
        before_hash: Option<String>,
        after: Vec<u8>,
        after_hash: String,
        endpoints: HashMap<String, JsonValue>,
        content_flags: HashMap<String, JsonValue>,
        backup: Option<H>,
    },
    Directory {
        path: PathBuf,
        existed: bool,
    },
    Environment {
        key: String,
        value: String,
        previous: Option<String>,
        backup: Option<H>,
    },
}

/// Deduplication identity: file-system mutations collide by path, environment
/// mutations by key.
#[derive(PartialEq, Eq, Hash)]
enum TargetKey {
    Path(PathBuf),
    Env(String),
}

/// Pseudo path used in `ApplyReport` and backup records for an environment
/// mutation (there is no file behind it).
fn environment_report_path(key: &str) -> PathBuf {
    PathBuf::from(format!("env:{key}"))
}

impl<H> PreparedMutation<H> {
    fn target_key(&self) -> TargetKey {
        match self {
            Self::File { path, .. } | Self::Directory { path, .. } => TargetKey::Path(path.clone()),
            Self::Environment { key, .. } => TargetKey::Env(key.clone()),
        }
    }

    /// The path reported in `DuplicateTarget` errors (environment mutations
    /// report their pseudo path).
    fn dedup_path(&self) -> PathBuf {
        match self {
            Self::File { path, .. } | Self::Directory { path, .. } => path.clone(),
            Self::Environment { key, .. } => environment_report_path(key),
        }
    }

    fn report(&self) -> AppliedMutation {
        match self {
            Self::File {
                path,
                before_hash,
                after_hash,
                ..
            } => AppliedMutation {
                path: path.clone(),
                before_hash: before_hash.clone(),
                after_hash: Some(after_hash.clone()),
            },
            Self::Directory { path, .. } => AppliedMutation {
                path: path.clone(),
                before_hash: None,
                after_hash: None,
            },
            Self::Environment {
                key, value, previous, ..
            } => AppliedMutation {
                path: environment_report_path(key),
                before_hash: previous
                    .as_ref()
                    .map(|value| sha256(value.as_bytes())),
                after_hash: Some(sha256(value.as_bytes())),
            },
        }
    }

    fn semantic_target(&self) -> Option<SemanticTarget> {
        let Self::File {
            path,
            kind,
            endpoints,
            content_flags,
            ..
        } = self
        else {
            return None;
        };
        Some(SemanticTarget {
            path: path.clone(),
            kind: *kind,
            endpoints: endpoints.clone(),
            disabled_content_flags: content_flags.clone(),
        })
    }
}

fn prepare_json<H>(path: PathBuf, patch: &JsonValue) -> Result<PreparedMutation<H>, ExecutorError> {
    if !patch.is_object() {
        return Err(invalid_syntax(
            "JSON",
            &path,
            "merge patch must be an object",
        ));
    }
    let existed = path.exists();
    let before = read_existing(&path)?;
    let mut document = if existed {
        serde_json::from_slice(&before)
            .map_err(|error| invalid_syntax("JSON", &path, error.to_string()))?
    } else {
        JsonValue::Object(Default::default())
    };
    detect_endpoint_conflicts(&path, &document, patch)?;
    merge_json(&mut document, patch);
    let mut after = serde_json::to_vec_pretty(&document)
        .map_err(|error| invalid_syntax("JSON", &path, error.to_string()))?;
    after.push(b'\n');
    serde_json::from_slice::<JsonValue>(&after)
        .map_err(|error| invalid_syntax("JSON", &path, error.to_string()))?;
    Ok(PreparedMutation::File {
        path,
        kind: ConfigKind::Json,
        before_hash: existed.then(|| sha256(&before)),
        before,
        after_hash: sha256(&after),
        after,
        endpoints: semantic_fields(patch, is_otlp_endpoint),
        content_flags: semantic_fields(patch, is_content_flag),
        backup: None,
    })
}

fn prepare_toml<H>(path: PathBuf, patch: &JsonValue) -> Result<PreparedMutation<H>, ExecutorError> {
    let Some(patch) = patch.as_object() else {
        return Err(invalid_syntax(
            "TOML",
            &path,
            "merge patch must be an object",
        ));
    };
    let existed = path.exists();
    let before = read_existing(&path)?;
    let mut document = if existed {
        std::str::from_utf8(&before)
            .map_err(|error| invalid_syntax("TOML", &path, error.to_string()))?
            .parse::<toml::Value>()
            .map_err(|error| invalid_syntax("TOML", &path, error.to_string()))?
    } else {
        toml::Value::Table(Default::default())
    };
    let existing = serde_json::to_value(&document)
        .map_err(|error| invalid_syntax("TOML", &path, error.to_string()))?;
    detect_endpoint_conflicts(&path, &existing, &JsonValue::Object(patch.clone()))?;
    merge_toml(&mut document, patch, &path)?;
    let after = toml::to_string_pretty(&document)
        .map_err(|error| invalid_syntax("TOML", &path, error.to_string()))?
        .into_bytes();
    std::str::from_utf8(&after)
        .map_err(|error| invalid_syntax("TOML", &path, error.to_string()))?
        .parse::<toml::Value>()
        .map_err(|error| invalid_syntax("TOML", &path, error.to_string()))?;
    Ok(PreparedMutation::File {
        path,
        kind: ConfigKind::Toml,
        before_hash: existed.then(|| sha256(&before)),
        before,
        after_hash: sha256(&after),
        after,
        endpoints: semantic_fields(&JsonValue::Object(patch.clone()), is_otlp_endpoint),
        content_flags: semantic_fields(&JsonValue::Object(patch.clone()), is_content_flag),
        backup: None,
    })
}

fn read_existing(path: &Path) -> Result<Vec<u8>, ExecutorError> {
    if !path.exists() {
        return Ok(Vec::new());
    }
    fs::read(path).map_err(|source| ExecutorError::Io {
        path: path.to_path_buf(),
        source,
    })
}

fn merge_json(document: &mut JsonValue, patch: &JsonValue) {
    let JsonValue::Object(patch) = patch else {
        *document = patch.clone();
        return;
    };
    if !document.is_object() {
        *document = JsonValue::Object(Default::default());
    }
    let target = document.as_object_mut().expect("object initialized above");
    for (key, value) in patch {
        if value.is_null() {
            target.remove(key);
        } else {
            merge_json(target.entry(key.clone()).or_insert(JsonValue::Null), value);
        }
    }
}

fn detect_endpoint_conflicts(
    path: &Path,
    existing: &JsonValue,
    patch: &JsonValue,
) -> Result<(), ExecutorError> {
    let mut existing_fields = HashMap::new();
    let mut patch_fields = HashMap::new();
    collect_fields(existing, "", &mut existing_fields);
    collect_fields(patch, "", &mut patch_fields);
    for (key, requested) in patch_fields.iter().filter(|(key, _)| is_otlp_endpoint(key)) {
        let Some(current) = existing_fields.get(key) else {
            continue;
        };
        if current != requested {
            return Err(ExecutorError::EndpointConflict {
                path: path.to_path_buf(),
                key: key.clone(),
                existing: current.to_string(),
                requested: requested.to_string(),
            });
        }
    }
    Ok(())
}

fn collect_fields(value: &JsonValue, prefix: &str, fields: &mut HashMap<String, JsonValue>) {
    match value {
        JsonValue::Object(object) => {
            for (key, value) in object {
                let key = if prefix.is_empty() {
                    key.clone()
                } else {
                    format!("{prefix}.{key}")
                };
                collect_fields(value, &key, fields);
            }
        }
        _ => {
            fields.insert(prefix.to_string(), value.clone());
        }
    }
}

fn semantic_fields(
    value: &JsonValue,
    predicate: impl Fn(&str) -> bool,
) -> HashMap<String, JsonValue> {
    let mut fields = HashMap::new();
    collect_fields(value, "", &mut fields);
    fields
        .into_iter()
        .filter(|(key, _)| predicate(key))
        .collect()
}

fn normalized_key(key: &str) -> String {
    key.chars()
        .map(|character| {
            if character.is_ascii_alphanumeric() {
                character.to_ascii_lowercase()
            } else {
                '_'
            }
        })
        .collect()
}

fn is_otlp_endpoint(key: &str) -> bool {
    let key = normalized_key(key);
    key.contains("otlp") && key.ends_with("endpoint")
}

fn is_content_flag(key: &str) -> bool {
    let key = normalized_key(key);
    key.contains("prompt")
        || (key.contains("tool")
            && (key.contains("content") || key.contains("detail") || key.contains("argument")))
        || (key.contains("otel") && key.contains("log") && key.contains("content"))
}

fn is_loopback_endpoint(endpoint: &str) -> bool {
    let endpoint = endpoint.to_ascii_lowercase();
    [
        "http://127.0.0.1",
        "https://127.0.0.1",
        "http://localhost",
        "https://localhost",
        "http://[::1]",
        "https://[::1]",
    ]
    .iter()
    .any(|prefix| {
        endpoint == *prefix
            || endpoint
                .strip_prefix(prefix)
                .is_some_and(|suffix| suffix.starts_with([':', '/']))
    })
}

fn is_disabled(value: &JsonValue) -> bool {
    matches!(value, JsonValue::Bool(false))
        || matches!(value, JsonValue::Number(number) if number.as_i64() == Some(0))
        || matches!(value, JsonValue::String(value) if value == "0" || value.eq_ignore_ascii_case("false"))
}

fn parse_document(kind: ConfigKind, path: &Path, bytes: &[u8]) -> Result<JsonValue, String> {
    match kind {
        ConfigKind::Json => serde_json::from_slice(bytes)
            .map_err(|error| format!("could not parse JSON `{}`: {error}", path.display())),
        ConfigKind::Toml => {
            let text = std::str::from_utf8(bytes)
                .map_err(|error| format!("could not decode TOML `{}`: {error}", path.display()))?;
            let value = text
                .parse::<toml::Value>()
                .map_err(|error| format!("could not parse TOML `{}`: {error}", path.display()))?;
            serde_json::to_value(value)
                .map_err(|error| format!("could not inspect TOML `{}`: {error}", path.display()))
        }
    }
}

fn verify_semantic_target(target: &SemanticTarget) -> Result<(), String> {
    let bytes = fs::read(&target.path)
        .map_err(|error| format!("could not re-read `{}`: {error}", target.path.display()))?;
    let document = parse_document(target.kind, &target.path, &bytes)?;
    let mut fields = HashMap::new();
    collect_fields(&document, "", &mut fields);
    for (key, expected) in &target.endpoints {
        let expected_endpoint = expected.as_str().ok_or_else(|| {
            format!(
                "planned OTLP endpoint `{key}` in `{}` is not a string",
                target.path.display()
            )
        })?;
        if !is_loopback_endpoint(expected_endpoint) {
            return Err(format!(
                "planned OTLP endpoint `{key}` in `{}` is not loopback",
                target.path.display()
            ));
        }
        let actual = fields.get(key).ok_or_else(|| {
            format!(
                "OTLP endpoint `{key}` is missing from `{}`",
                target.path.display()
            )
        })?;
        if actual != expected {
            return Err(format!(
                "OTLP endpoint `{key}` in `{}` changed from {} to {}",
                target.path.display(),
                expected,
                actual
            ));
        }
    }
    for (key, expected) in &target.disabled_content_flags {
        let actual = fields.get(key).ok_or_else(|| {
            format!(
                "content flag `{key}` is missing from `{}`",
                target.path.display()
            )
        })?;
        if actual != expected || !is_disabled(actual) {
            return Err(format!(
                "content flag `{key}` in `{}` is not disabled as planned",
                target.path.display()
            ));
        }
    }
    Ok(())
}

fn merge_toml(
    document: &mut toml::Value,
    patch: &serde_json::Map<String, JsonValue>,
    path: &Path,
) -> Result<(), ExecutorError> {
    if !document.is_table() {
        *document = toml::Value::Table(Default::default());
    }
    let table = document.as_table_mut().expect("table initialized above");
    for (key, value) in patch {
        if value.is_null() {
            table.remove(key);
            continue;
        }
        if let Some(nested_patch) = value.as_object() {
            let entry = table
                .entry(key.clone())
                .or_insert_with(|| toml::Value::Table(Default::default()));
            merge_toml(entry, nested_patch, path)?;
            continue;
        }
        table.insert(
            key.clone(),
            toml::Value::try_from(value.clone())
                .map_err(|error| invalid_syntax("TOML", path, error.to_string()))?,
        );
    }
    Ok(())
}

fn prepare_environment<H>(
    environment: &dyn UserEnvironmentStore,
    scope: &str,
    key: &str,
    value_ref: &str,
) -> Result<PreparedMutation<H>, ExecutorError> {
    if !scope.eq_ignore_ascii_case("user") {
        return Err(ExecutorError::UnsupportedEnvironmentScope(scope.into()));
    }
    let Some(value) = value_ref.strip_prefix("value://") else {
        // Plans must never resolve secrets or host values at execute time;
        // only literal `value://` references are honored.
        return Err(ExecutorError::UnsupportedValueRef(value_ref.into()));
    };
    if key.is_empty()
        || key.len() > 256
        || !key
            .chars()
            .all(|character| character.is_ascii_alphanumeric() || character == '_')
    {
        return Err(ExecutorError::InvalidEnvironmentKey(key.into()));
    }
    let previous = environment
        .read(key)
        .map_err(|message| ExecutorError::EnvironmentIo {
            key: key.into(),
            message,
        })?;
    Ok(PreparedMutation::Environment {
        key: key.into(),
        value: value.into(),
        previous,
        backup: None,
    })
}

fn apply_prepared<H>(
    environment: &dyn UserEnvironmentStore,
    mutation: &PreparedMutation<H>,
) -> Result<(), ExecutorError> {
    match mutation {
        PreparedMutation::File { path, after, .. } => write_atomic(path, after),
        PreparedMutation::Environment { key, value, .. } => {
            environment
                .write(key, value)
                .map_err(|message| ExecutorError::EnvironmentIo {
                    key: key.clone(),
                    message,
                })?;
            // Read-back verification: the persistent store, not the current
            // process, must carry the value.
            let read_back = environment
                .read(key)
                .map_err(|message| ExecutorError::EnvironmentIo {
                    key: key.clone(),
                    message,
                })?;
            if read_back.as_deref() != Some(value.as_str()) {
                return Err(ExecutorError::EnvironmentIo {
                    key: key.clone(),
                    message: "read-back verification failed".into(),
                });
            }
            Ok(())
        }
        PreparedMutation::Directory { existed: true, .. } => Ok(()),
        PreparedMutation::Directory {
            path,
            existed: false,
        } => fs::create_dir(path).map_err(|source| ExecutorError::Io {
            path: path.clone(),
            source,
        }),
    }
}

fn write_atomic(path: &Path, bytes: &[u8]) -> Result<(), ExecutorError> {
    let parent = path
        .parent()
        .ok_or_else(|| ExecutorError::UnsafePath(path.display().to_string()))?;
    let mut temporary = NamedTempFile::new_in(parent).map_err(|source| ExecutorError::Io {
        path: parent.to_path_buf(),
        source,
    })?;
    if let Ok(metadata) = fs::metadata(path) {
        fs::set_permissions(temporary.path(), metadata.permissions()).map_err(|source| {
            ExecutorError::Io {
                path: temporary.path().to_path_buf(),
                source,
            }
        })?;
    }
    temporary
        .write_all(bytes)
        .and_then(|_| temporary.flush())
        .and_then(|_| temporary.as_file().sync_all())
        .map_err(|source| ExecutorError::Io {
            path: temporary.path().to_path_buf(),
            source,
        })?;
    atomic_replace(temporary.path(), path).map_err(|source| ExecutorError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    sync_parent(parent).map_err(|source| ExecutorError::Io {
        path: parent.to_path_buf(),
        source,
    })?;
    Ok(())
}

#[cfg(not(windows))]
fn atomic_replace(source: &Path, destination: &Path) -> io::Result<()> {
    fs::rename(source, destination)
}

#[cfg(windows)]
fn atomic_replace(source: &Path, destination: &Path) -> io::Result<()> {
    use std::os::windows::ffi::OsStrExt;

    const MOVEFILE_REPLACE_EXISTING: u32 = 0x1;
    const MOVEFILE_WRITE_THROUGH: u32 = 0x8;

    #[link(name = "Kernel32")]
    extern "system" {
        fn MoveFileExW(existing: *const u16, replacement: *const u16, flags: u32) -> i32;
    }

    let source: Vec<u16> = source.as_os_str().encode_wide().chain(Some(0)).collect();
    let destination: Vec<u16> = destination
        .as_os_str()
        .encode_wide()
        .chain(Some(0))
        .collect();
    let result = unsafe {
        MoveFileExW(
            source.as_ptr(),
            destination.as_ptr(),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )
    };
    if result == 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(())
    }
}

#[cfg(unix)]
fn sync_parent(parent: &Path) -> io::Result<()> {
    fs::File::open(parent)?.sync_all()
}

#[cfg(not(unix))]
fn sync_parent(_parent: &Path) -> io::Result<()> {
    Ok(())
}

fn invalid_syntax(kind: &'static str, path: &Path, message: impl Into<String>) -> ExecutorError {
    ExecutorError::InvalidSyntax {
        kind,
        path: path.to_path_buf(),
        message: message.into(),
    }
}

fn sha256(bytes: &[u8]) -> String {
    format!("sha256:{:x}", Sha256::digest(bytes))
}

fn plan_hash(plan: &SetupPlan) -> String {
    let encoded = serde_json::to_vec(plan).expect("SetupPlan serialization is infallible");
    sha256(&encoded)
}

#[cfg(test)]
mod tests;
