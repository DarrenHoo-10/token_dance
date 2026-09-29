//! `config.toml`: per-environment collection settings. Never holds credentials.

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::error::{CliError, CliResult};
use crate::sources;

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Config {
    #[serde(default = "one")]
    pub schema_version: u32,
    /// `os` or `private-file`; unset until `tokendance init` chooses.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub credential_backend: Option<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub sources: BTreeMap<String, SourceConfig>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SourceConfig {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub path: Option<PathBuf>,
}

fn one() -> u32 {
    1
}

impl Config {
    pub fn load(path: &Path) -> CliResult<Self> {
        let text = match fs::read_to_string(path) {
            Ok(text) => text,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                return Ok(Self {
                    schema_version: 1,
                    ..Self::default()
                })
            }
            Err(e) => {
                return Err(CliError::invalid(format!(
                    "cannot read config {}: {e}",
                    path.display()
                )))
            }
        };
        let config: Self = toml::from_str(&text)
            .map_err(|e| CliError::invalid(format!("invalid config {}: {e}", path.display())))?;
        config.validate(path)?;
        Ok(config)
    }

    fn validate(&self, path: &Path) -> CliResult<()> {
        if self.schema_version != 1 {
            return Err(CliError::new(
                crate::error::Kind::Incompatible,
                format!(
                    "config {} has schema_version {}, this build understands 1",
                    path.display(),
                    self.schema_version
                ),
            ));
        }
        for name in self.sources.keys() {
            if sources::find(name).is_none() {
                return Err(CliError::invalid(format!(
                    "invalid config {}: unknown source `{name}`",
                    path.display()
                )));
            }
        }
        if let Some(backend) = &self.credential_backend {
            crate::keys::Backend::parse(backend).map_err(|e| {
                CliError::invalid(format!("invalid config {}: {}", path.display(), e.message))
            })?;
        }
        Ok(())
    }

    pub fn save(&self, path: &Path) -> CliResult<()> {
        let text = toml::to_string_pretty(self)
            .map_err(|e| CliError::internal(format!("cannot serialize config: {e}")))?;
        if let Some(parent) = path.parent() {
            collector_service::platform::create_private_dir(parent).map_err(CliError::internal)?;
        }
        collector_service::platform::write_private_file(path, text).map_err(CliError::internal)
    }

    pub fn source_enabled(&self, name: &str) -> bool {
        self.sources
            .get(name)
            .and_then(|s| s.enabled)
            .unwrap_or(true)
    }

    pub fn source_path(&self, name: &str) -> Option<&Path> {
        self.sources.get(name).and_then(|s| s.path.as_deref())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip_and_defaults() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        assert_eq!(Config::load(&path).unwrap().schema_version, 1);
        let mut config = Config {
            schema_version: 1,
            credential_backend: Some("private-file".into()),
            ..Config::default()
        };
        config.sources.insert(
            "codex".into(),
            SourceConfig {
                enabled: Some(false),
                path: Some("/tmp/x".into()),
            },
        );
        config.save(&path).unwrap();
        let loaded = Config::load(&path).unwrap();
        assert_eq!(loaded, config);
        assert!(!loaded.source_enabled("codex"));
        assert!(loaded.source_enabled("cursor"));
    }

    #[test]
    fn rejects_unknown_source_key_and_secrets() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "[sources.nope]\nenabled = true\n").unwrap();
        assert!(Config::load(&path).is_err());
        fs::write(&path, "token = \"abc\"\n").unwrap();
        assert!(Config::load(&path).is_err());
    }

    #[test]
    fn newer_schema_is_incompatible() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "schema_version = 2\n").unwrap();
        let error = Config::load(&path).unwrap_err();
        assert_eq!(error.kind, crate::error::Kind::Incompatible);
    }
}
