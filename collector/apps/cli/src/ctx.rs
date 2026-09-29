//! Resolved environment shared by every command.

use std::path::PathBuf;

use collector_service::{AppPaths, InstanceLock};

use crate::config::Config;
use crate::error::{lock_error, CliError, CliResult};
use crate::keys::Backend;

pub struct Ctx {
    pub paths: AppPaths,
    pub config_path: PathBuf,
}

pub struct Overrides {
    pub data_dir: Option<PathBuf>,
    pub config: Option<PathBuf>,
}

impl Ctx {
    /// Precedence: command-line flag, then `TOKENDANCE_*` (wired through clap), then defaults.
    pub fn resolve(o: Overrides) -> CliResult<Self> {
        let (paths, default_config) = match &o.data_dir {
            Some(dir) => (AppPaths::for_root(dir.clone()), dir.join("config.toml")),
            None => {
                let paths = AppPaths::production().map_err(CliError::internal)?;
                let config = default_config_path(&paths)?;
                (paths, config)
            }
        };
        Ok(Self {
            config_path: o.config.unwrap_or(default_config),
            paths,
        })
    }

    pub fn config(&self) -> CliResult<Config> {
        Config::load(&self.config_path)
    }

    /// `TOKENDANCE_CREDENTIAL_BACKEND` beats the config file, which beats the platform default.
    pub fn backend(&self, config: &Config) -> CliResult<Backend> {
        if let Some(value) = std::env::var_os("TOKENDANCE_CREDENTIAL_BACKEND") {
            return Backend::parse(&value.to_string_lossy());
        }
        if let Some(value) = &config.credential_backend {
            return Backend::parse(value);
        }
        Backend::platform_default().ok_or_else(|| {
            CliError::new(
                crate::error::Kind::Credentials,
                "no credential backend is configured for this host",
            )
            .hint("run `tokendance init --credential-backend private-file`")
        })
    }

    /// Every command that may write local state takes the same lock as the desktop app.
    pub fn lock(&self) -> CliResult<InstanceLock> {
        InstanceLock::acquire(&self.paths).map_err(lock_error)
    }
}

#[cfg(target_os = "linux")]
fn default_config_path(_paths: &AppPaths) -> CliResult<PathBuf> {
    let base = std::env::var_os("XDG_CONFIG_HOME")
        .filter(|v| !v.is_empty())
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".config")))
        .ok_or_else(|| CliError::internal("HOME is unavailable"))?;
    Ok(base.join("TokenDance").join("config.toml"))
}

#[cfg(not(target_os = "linux"))]
fn default_config_path(paths: &AppPaths) -> CliResult<PathBuf> {
    Ok(paths.collector.join("cli-config.toml"))
}
