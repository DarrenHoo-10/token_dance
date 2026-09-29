//! Identity-secret storage. The secret keys the HMAC identity of every event, so
//! it must be the same one the desktop app uses on the same machine.
//!
//! * `os` — the system keystore entry the desktop app already owns (macOS/Windows).
//! * `private-file` — an explicit 0600 file for hosts without a system keystore.
//!
//! Failure never falls back from one backend to the other, and a missing secret
//! next to an existing database is an error rather than a reason to mint a new one.

use std::fs;
use std::path::{Path, PathBuf};

use collector_service::AppPaths;
use wal_spool::{KeyError, KeyProvider};

use crate::error::{CliError, CliResult, Kind};

pub const KEY_FILE: &str = "identity.key";
const KEY_LEN: usize = 32;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Backend {
    Os,
    PrivateFile,
}

impl Backend {
    pub fn parse(value: &str) -> CliResult<Self> {
        match value {
            "os" => Ok(Self::Os),
            "private-file" => Ok(Self::PrivateFile),
            other => Err(CliError::invalid(format!(
                "unknown credential backend `{other}` (use `os` or `private-file`)"
            ))),
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Os => "os",
            Self::PrivateFile => "private-file",
        }
    }

    /// The backend that needs no explicit choice, if any.
    pub fn platform_default() -> Option<Self> {
        if platform_credentials::backend_is_persistent() {
            Some(Self::Os)
        } else {
            None
        }
    }
}

/// Whether the secret exists, without creating it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum KeyState {
    Present,
    Missing,
    Problem(String),
}

pub fn key_path(paths: &AppPaths) -> PathBuf {
    paths.collector.join(KEY_FILE)
}

/// Data that was written under an existing identity: the event database or the
/// desktop's encrypted upload spool.
fn database_exists(paths: &AppPaths) -> bool {
    paths
        .collector
        .join(collector_service::local_store::pipeline::DB_FILE)
        .exists()
        || wal_spool::spool_has_data(paths.spool_dir())
}

fn os_available() -> CliResult<()> {
    if platform_credentials::backend_is_persistent() {
        Ok(())
    } else {
        Err(CliError::new(
            Kind::Credentials,
            "no system keystore backend is available on this platform yet",
        )
        .hint("re-run `tokendance init --credential-backend private-file` to keep the key in a 0600 file"))
    }
}

/// Inspect without creating or printing anything secret.
pub fn inspect(backend: Backend, paths: &AppPaths) -> KeyState {
    match backend {
        Backend::Os => {
            if let Err(e) = os_available() {
                return KeyState::Problem(e.message);
            }
            match wal_spool::OsKeyProvider::wal_key(false).data_key() {
                Ok(_) => KeyState::Present,
                Err(KeyError::NotFound) => KeyState::Missing,
                Err(e) => KeyState::Problem(e.to_string()),
            }
        }
        Backend::PrivateFile => match read_private_file(&key_path(paths)) {
            Ok(_) => KeyState::Present,
            Err(FileKeyError::Missing) => KeyState::Missing,
            Err(FileKeyError::Invalid(m)) => KeyState::Problem(m),
        },
    }
}

/// Load the identity secret. With `create`, a missing secret is generated only
/// when no database exists yet.
pub fn load(backend: Backend, paths: &AppPaths, create: bool) -> CliResult<Vec<u8>> {
    let refuse = || {
        CliError::new(
            Kind::Credentials,
            "the identity secret is missing but collected data already exists; refusing to create a replacement",
        )
        .hint("restore the original secret; a new one would duplicate this device's identity")
    };
    match backend {
        Backend::Os => {
            os_available()?;
            let has_data = database_exists(paths);
            let provider = wal_spool::OsKeyProvider::wal_key(create && !has_data);
            match provider.data_key() {
                Ok(key) => Ok(key.to_vec()),
                Err(KeyError::NotFound) if has_data => Err(refuse()),
                Err(KeyError::NotFound) => Err(CliError::new(
                    Kind::Credentials,
                    "no identity secret found; run `tokendance init` first",
                )),
                Err(e) => Err(CliError::new(
                    Kind::Credentials,
                    format!("system keystore unavailable: {e}"),
                )),
            }
        }
        Backend::PrivateFile => {
            let path = key_path(paths);
            match read_private_file(&path) {
                Ok(key) => Ok(key.to_vec()),
                Err(FileKeyError::Invalid(m)) => Err(CliError::new(Kind::Credentials, m)),
                Err(FileKeyError::Missing) if database_exists(paths) => Err(refuse()),
                Err(FileKeyError::Missing) if !create => Err(CliError::new(
                    Kind::Credentials,
                    "no identity secret found; run `tokendance init` first",
                )),
                Err(FileKeyError::Missing) => create_private_file(&path).map(|k| k.to_vec()),
            }
        }
    }
}

enum FileKeyError {
    Missing,
    Invalid(String),
}

fn read_private_file(path: &Path) -> Result<[u8; KEY_LEN], FileKeyError> {
    let meta = match fs::symlink_metadata(path) {
        Ok(m) => m,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Err(FileKeyError::Missing),
        Err(e) => return Err(FileKeyError::Invalid(format!("cannot stat key file: {e}"))),
    };
    if meta.file_type().is_symlink() || !meta.is_file() {
        return Err(FileKeyError::Invalid(
            "key file must be a regular file, not a symlink".into(),
        ));
    }
    if meta.len() != KEY_LEN as u64 {
        return Err(FileKeyError::Invalid(
            "key file has an unexpected size; existing data was preserved".into(),
        ));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        if meta.permissions().mode() & 0o077 != 0 {
            return Err(FileKeyError::Invalid(
                "key file must not be accessible by group or others (chmod 600)".into(),
            ));
        }
        // SAFETY: geteuid has no preconditions.
        if meta.uid() != unsafe { libc::geteuid() } {
            return Err(FileKeyError::Invalid(
                "key file is owned by a different user".into(),
            ));
        }
    }
    let bytes =
        fs::read(path).map_err(|e| FileKeyError::Invalid(format!("cannot read key file: {e}")))?;
    <[u8; KEY_LEN]>::try_from(bytes.as_slice())
        .map_err(|_| FileKeyError::Invalid("key file changed while reading".into()))
}

#[cfg(unix)]
fn create_private_file(path: &Path) -> CliResult<[u8; KEY_LEN]> {
    use rand::RngCore;
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;

    if let Some(parent) = path.parent() {
        collector_service::platform::create_private_dir(parent).map_err(CliError::internal)?;
    }
    let mut key = [0u8; KEY_LEN];
    rand::rngs::OsRng.fill_bytes(&mut key);
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true).mode(0o600);
    match options.open(path) {
        Ok(mut file) => {
            file.write_all(&key)
                .and_then(|_| file.sync_all())
                .map_err(|e| CliError::internal(format!("cannot write key file: {e}")))?;
            Ok(key)
        }
        // Lost a race with another creator: use theirs.
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => match read_private_file(path) {
            Ok(key) => Ok(key),
            Err(FileKeyError::Invalid(m)) => Err(CliError::new(Kind::Credentials, m)),
            Err(FileKeyError::Missing) => Err(CliError::internal("key file vanished")),
        },
        Err(e) => Err(CliError::internal(format!("cannot create key file: {e}"))),
    }
}

#[cfg(not(unix))]
fn create_private_file(_path: &Path) -> CliResult<[u8; KEY_LEN]> {
    Err(CliError::new(
        Kind::Credentials,
        "the private-file credential backend is only supported on Unix hosts",
    ))
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    fn paths(dir: &Path) -> AppPaths {
        AppPaths::for_root(dir.join("data"))
    }

    #[test]
    fn creates_once_and_reuses_the_same_secret() {
        let dir = tempfile::tempdir().unwrap();
        let paths = paths(dir.path());
        assert_eq!(inspect(Backend::PrivateFile, &paths), KeyState::Missing);
        let first = load(Backend::PrivateFile, &paths, true).unwrap();
        let second = load(Backend::PrivateFile, &paths, false).unwrap();
        assert_eq!(first, second);
        assert_eq!(first.len(), 32);
        let mode = fs::metadata(key_path(&paths)).unwrap().permissions().mode();
        assert_eq!(mode & 0o777, 0o600);
        assert_eq!(inspect(Backend::PrivateFile, &paths), KeyState::Present);
    }

    #[test]
    fn missing_key_next_to_existing_data_is_never_replaced() {
        let dir = tempfile::tempdir().unwrap();
        let paths = paths(dir.path());
        fs::create_dir_all(&paths.collector).unwrap();
        fs::write(
            paths
                .collector
                .join(collector_service::local_store::pipeline::DB_FILE),
            b"x",
        )
        .unwrap();
        let error = load(Backend::PrivateFile, &paths, true).unwrap_err();
        assert_eq!(error.kind, Kind::Credentials);
        assert!(!key_path(&paths).exists());
    }

    #[test]
    fn loose_permissions_symlinks_and_wrong_size_are_rejected() {
        let dir = tempfile::tempdir().unwrap();
        let paths = paths(dir.path());
        load(Backend::PrivateFile, &paths, true).unwrap();
        let key = key_path(&paths);
        fs::set_permissions(&key, fs::Permissions::from_mode(0o644)).unwrap();
        assert!(matches!(
            inspect(Backend::PrivateFile, &paths),
            KeyState::Problem(_)
        ));
        assert!(load(Backend::PrivateFile, &paths, false).is_err());
        fs::set_permissions(&key, fs::Permissions::from_mode(0o600)).unwrap();
        fs::write(&key, b"short").unwrap();
        fs::set_permissions(&key, fs::Permissions::from_mode(0o600)).unwrap();
        assert!(load(Backend::PrivateFile, &paths, false).is_err());
        fs::remove_file(&key).unwrap();
        let target = dir.path().join("elsewhere");
        fs::write(&target, [7u8; 32]).unwrap();
        fs::set_permissions(&target, fs::Permissions::from_mode(0o600)).unwrap();
        std::os::unix::fs::symlink(&target, &key).unwrap();
        assert!(load(Backend::PrivateFile, &paths, false).is_err());
    }

    #[test]
    fn parse_rejects_unknown_backends() {
        assert_eq!(Backend::parse("os").unwrap(), Backend::Os);
        assert!(Backend::parse("mock").is_err());
    }
}
