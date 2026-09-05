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
    #[error("unsupported configuration mutation: EnvironmentSet")]
    EnvironmentSetRejected,
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
            status: SetupPlanStatus::Proposed,
            history: vec![SetupPlanStatus::Proposed],
            approved_plan_hash: None,
        }
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
            if let Err(error) = apply_prepared(mutation) {
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
                ConfigMutation::EnvironmentSet { .. } => {
                    return Err(ExecutorError::EnvironmentSetRejected)
                }
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
            if !targets.insert(item.path().to_path_buf()) {
                return Err(ExecutorError::DuplicateTarget(item.path().to_path_buf()));
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
            let PreparedMutation::File {
                path,
                before,
                before_hash: Some(before_hash),
                backup,
                ..
            } = mutation
            else {
                continue;
            };
            let handle = self
                .backup_store
                .save_encrypted(&plan.plan_id, path, before_hash, before)
                .map_err(|message| ExecutorError::Backup {
                    path: path.clone(),
                    message,
                })?;
            *backup = Some(handle);
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
}

impl<H> PreparedMutation<H> {
    fn path(&self) -> &Path {
        match self {
            Self::File { path, .. } | Self::Directory { path, .. } => path,
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

fn apply_prepared<H>(mutation: &PreparedMutation<H>) -> Result<(), ExecutorError> {
    match mutation {
        PreparedMutation::File { path, after, .. } => write_atomic(path, after),
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
