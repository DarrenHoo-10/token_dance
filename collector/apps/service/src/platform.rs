//! Shared paths, instance lock, device identity, and agent source resolution.
//!
//! Local durable state lives under [`AppPaths::collector`]. The event-pipeline
//! plan makes SQLite events + `processing_tasks` the reliable queue; the
//! encrypted WAL spool remains only until that writer is enabled. Account
//! cookies stay in the OS keystore and never enter local event tables.

use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use protocol::{Architecture, OsType};

const BUNDLE_ID: &str = "io.tokendance.desktop";
const COLLECTOR_DIR: &str = "collector";
const MIGRATION_MARKER: &str = ".migrated-from-home-tokendance";
const MAX_LOG_BYTES: u64 = 2 * 1024 * 1024;
const MAX_LOG_FILES: usize = 5;

/// Opt-in local desktop testing is only available in debug builds.
pub fn local_test_enabled() -> bool {
    cfg!(debug_assertions)
        && (option_env!("TOKENDANCE_LOCAL_TEST_BUILD") == Some("1")
            || std::env::args_os().any(|arg| arg == "--local-test"))
}

fn local_test_paths(home: &Path) -> AppPaths {
    let root = home
        .join("Library/Application Support")
        .join("io.tokendance.desktop.local-test");
    AppPaths {
        collector: root.join("collector"),
        logs: root.join("logs"),
        cache: root.join("cache"),
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AppPaths {
    pub collector: PathBuf,
    pub logs: PathBuf,
    pub cache: PathBuf,
}

#[derive(Debug)]
pub struct InstanceLock {
    file: File,
    path: PathBuf,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ProtocolIdentity {
    pub os: OsType,
    pub arch: Architecture,
}

impl AppPaths {
    pub fn production() -> Result<Self, String> {
        if local_test_enabled() {
            return Ok(local_test_paths(&require_home()?));
        }
        #[cfg(target_os = "macos")]
        {
            let home = require_home()?;
            Ok(Self {
                collector: home
                    .join("Library/Application Support")
                    .join(BUNDLE_ID)
                    .join(COLLECTOR_DIR),
                logs: home.join("Library/Logs").join(BUNDLE_ID),
                cache: home.join("Library/Caches").join(BUNDLE_ID),
            })
        }
        #[cfg(target_os = "windows")]
        {
            let local = require_env("LOCALAPPDATA")?;
            let collector = local.join("TokenDance").join(COLLECTOR_DIR);
            Ok(Self {
                logs: collector.clone(),
                cache: collector.join("cache"),
                collector,
            })
        }
        #[cfg(not(any(target_os = "macos", target_os = "windows")))]
        {
            let base = std::env::var_os("XDG_DATA_HOME")
                .map(PathBuf::from)
                .or_else(|| require_home().ok().map(|home| home.join(".local/share")))
                .ok_or_else(|| "HOME is unavailable".to_string())?;
            let collector = base.join("TokenDance").join(COLLECTOR_DIR);
            Ok(Self {
                logs: collector.clone(),
                cache: collector.join("cache"),
                collector,
            })
        }
    }

    pub fn for_root(collector: PathBuf) -> Self {
        Self {
            logs: collector.clone(),
            cache: collector.join("cache"),
            collector,
        }
    }

    pub fn ensure(&self) -> Result<(), String> {
        create_private_dir(&self.collector)?;
        create_private_dir(&self.logs)?;
        create_private_dir(&self.cache)?;
        Ok(())
    }

    pub fn spool_dir(&self) -> PathBuf {
        self.collector.join("spool")
    }

    /// Legacy desktop SQLite; the v2 pipeline uses its own database.
    pub fn local_store_path(&self) -> PathBuf {
        self.collector.join("tokendance.sqlite3")
    }

    pub fn lock_path(&self) -> PathBuf {
        self.collector.join("collector.lock")
    }

    pub fn installation_id_path(&self) -> PathBuf {
        self.collector.join("installation-id")
    }

    pub fn device_registered_path(&self) -> PathBuf {
        self.collector.join("device-registered")
    }

    pub fn session_index_path(&self) -> PathBuf {
        self.collector.join("account-session.index.json")
    }

    pub fn legacy_session_path(&self) -> PathBuf {
        self.collector.join("account-session.json")
    }

    pub fn log_file(&self) -> PathBuf {
        self.logs.join("collector.log")
    }

    pub fn crash_log_file(&self) -> PathBuf {
        self.logs.join("crash.log")
    }

    pub fn onboarding_complete_path(&self) -> PathBuf {
        self.collector.join(".onboarding-complete")
    }
}

pub fn require_home() -> Result<PathBuf, String> {
    std::env::var_os("HOME")
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
        .ok_or_else(|| "HOME is unavailable".into())
}

#[cfg(target_os = "windows")]
fn require_env(name: &str) -> Result<PathBuf, String> {
    std::env::var_os(name)
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
        .ok_or_else(|| format!("{name} is unavailable"))
}

pub fn protocol_identity() -> Result<ProtocolIdentity, String> {
    let os = match std::env::consts::OS {
        "macos" => OsType::Macos,
        "windows" => OsType::Windows,
        other => {
            return Err(format!(
                "unsupported collector OS '{other}' for TokenDance protocol identity"
            ))
        }
    };
    let arch = match std::env::consts::ARCH {
        "x86_64" => Architecture::X8664,
        "aarch64" => Architecture::Aarch64,
        other => {
            return Err(format!(
                "unsupported collector architecture '{other}' for TokenDance protocol identity"
            ))
        }
    };
    Ok(ProtocolIdentity { os, arch })
}

pub fn create_private_dir(path: &Path) -> Result<(), String> {
    fs::create_dir_all(path).map_err(|error| error.to_string())?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))
            .map_err(|error| error.to_string())?;
    }
    Ok(())
}

pub fn write_private_file(path: &Path, contents: impl AsRef<[u8]>) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        create_private_dir(parent)?;
    }
    let temporary = path.with_extension("tmp");
    {
        let mut options = OpenOptions::new();
        options.create(true).write(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options
            .open(&temporary)
            .map_err(|error| error.to_string())?;
        file.write_all(contents.as_ref())
            .map_err(|error| error.to_string())?;
        file.sync_all().map_err(|error| error.to_string())?;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&temporary, fs::Permissions::from_mode(0o600))
            .map_err(|error| error.to_string())?;
    }
    fs::rename(&temporary, path).map_err(|error| error.to_string())
}

impl InstanceLock {
    pub fn acquire(paths: &AppPaths) -> Result<Self, String> {
        paths.ensure()?;
        let path = paths.lock_path();
        let mut options = OpenOptions::new();
        options.create(true).read(true).write(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&path).map_err(|error| error.to_string())?;
        try_lock_exclusive(&file)?;
        file.set_len(0).map_err(|error| error.to_string())?;
        write!(file, "{}", std::process::id()).map_err(|error| error.to_string())?;
        let _ = file.sync_all();
        Ok(Self { file, path })
    }
}

impl Drop for InstanceLock {
    fn drop(&mut self) {
        let _ = unlock_file(&self.file);
        let _ = self.path;
    }
}

fn try_lock_exclusive(file: &File) -> Result<(), String> {
    match file.try_lock() {
        Ok(()) => Ok(()),
        Err(std::fs::TryLockError::WouldBlock) => Err("collector already running".into()),
        Err(std::fs::TryLockError::Error(error)) => Err(error.to_string()),
    }
}

fn unlock_file(file: &File) -> io::Result<()> {
    file.unlock()
}

pub fn append_rotated_log(log_file: &Path, message: &str) {
    if let Some(parent) = log_file.parent() {
        let _ = create_private_dir(parent);
    }
    if let Ok(meta) = fs::metadata(log_file) {
        if meta.len() >= MAX_LOG_BYTES {
            rotate_logs(log_file);
        }
    }
    if let Ok(mut file) = OpenOptions::new().create(true).append(true).open(log_file) {
        let _ = writeln!(file, "{message}");
    }
}

fn rotate_logs(log_file: &Path) {
    for index in (1..MAX_LOG_FILES).rev() {
        let from = log_file.with_extension(format!("log.{index}"));
        let to = log_file.with_extension(format!("log.{}", index + 1));
        if from.exists() {
            let _ = fs::rename(from, to);
        }
    }
    let first = log_file.with_extension("log.1");
    if log_file.exists() {
        let _ = fs::rename(log_file, first);
    }
    let overflow = log_file.with_extension(format!("log.{}", MAX_LOG_FILES + 1));
    if overflow.exists() {
        let _ = fs::remove_file(overflow);
    }
}

pub fn legacy_macos_collector_dir() -> Option<PathBuf> {
    require_home()
        .ok()
        .map(|home| home.join("TokenDance").join(COLLECTOR_DIR))
}

pub fn maybe_migrate_macos_home_dir(paths: &AppPaths) -> Result<(), String> {
    if local_test_enabled() {
        return Ok(());
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = paths;
        return Ok(());
    }
    #[cfg(target_os = "macos")]
    {
        migrate_macos_home_dir(paths)
    }
}

#[cfg(target_os = "macos")]
fn migrate_macos_home_dir(paths: &AppPaths) -> Result<(), String> {
    let Some(old) = legacy_macos_collector_dir() else {
        return Ok(());
    };
    migrate_legacy_collector_dir(&old, paths)
}

fn migrate_legacy_collector_dir(old: &Path, paths: &AppPaths) -> Result<(), String> {
    let marker = paths.collector.join(MIGRATION_MARKER);
    if marker.exists() {
        return Ok(());
    }
    if !old.is_dir() {
        write_private_file(&marker, b"1")?;
        return Ok(());
    }
    let dest_id = paths.installation_id_path();
    let old_id = old.join("installation-id");
    if dest_id.exists() && old_id.exists() {
        let dest = fs::read(&dest_id).map_err(|error| error.to_string())?;
        let source = fs::read(&old_id).map_err(|error| error.to_string())?;
        if dest != source {
            write_private_file(&marker, b"1")?;
            return Ok(());
        }
    }
    let staging = paths.collector.join(".migrate-staging");
    if staging.exists() {
        fs::remove_dir_all(&staging).map_err(|error| error.to_string())?;
    }
    copy_dir_private(&old, &staging)?;
    if old.join("installation-id").exists() && !staging.join("installation-id").exists() {
        return Err("migration copy lost installation-id".into());
    }
    for name in [
        "installation-id",
        "usage-ledger.json",
        "control.json",
        "tokendance.sqlite3",
    ] {
        let source = old.join(name);
        if source.exists() && !staging.join(name).exists() {
            return Err(format!("migration copy lost {name}"));
        }
    }
    let spool = staging.join("spool");
    if wal_spool::spool_has_data(&spool) {
        // WAL reopen is verified later with the real key; presence is enough here.
    }
    for entry in fs::read_dir(&staging).map_err(|error| error.to_string())? {
        let entry = entry.map_err(|error| error.to_string())?;
        let name = entry.file_name();
        let dest = paths.collector.join(&name);
        if dest.exists() {
            continue;
        }
        fs::rename(entry.path(), dest).map_err(|error| error.to_string())?;
    }
    fs::remove_dir_all(&staging).map_err(|error| error.to_string())?;
    write_private_file(&marker, b"1")?;
    Ok(())
}

fn copy_dir_private(from: &Path, to: &Path) -> Result<(), String> {
    create_private_dir(to)?;
    for entry in fs::read_dir(from).map_err(|error| error.to_string())? {
        let entry = entry.map_err(|error| error.to_string())?;
        let source = entry.path();
        let dest = to.join(entry.file_name());
        let meta = entry.metadata().map_err(|error| error.to_string())?;
        if meta.is_dir() {
            copy_dir_private(&source, &dest)?;
        } else {
            let bytes = fs::read(&source).map_err(|error| error.to_string())?;
            write_private_file(&dest, bytes)?;
        }
    }
    Ok(())
}

#[derive(Debug, Clone, Default)]
pub struct PathResolver {
    home: PathBuf,
    env_codex_home: Option<PathBuf>,
    explicit_codex: Option<PathBuf>,
    explicit_claude: Option<PathBuf>,
    use_environment: bool,
}

impl PathResolver {
    pub fn production() -> Self {
        Self {
            home: user_home(),
            env_codex_home: std::env::var_os("CODEX_HOME").map(PathBuf::from),
            explicit_codex: None,
            explicit_claude: None,
            use_environment: true,
        }
    }

    pub fn isolated(home: PathBuf) -> Self {
        Self {
            home,
            env_codex_home: None,
            explicit_codex: None,
            explicit_claude: None,
            use_environment: false,
        }
    }

    pub fn with_explicit_codex(mut self, path: PathBuf) -> Self {
        self.explicit_codex = Some(path);
        self
    }

    pub fn home(&self) -> &Path {
        &self.home
    }

    pub fn codex_root(&self) -> PathBuf {
        self.explicit_codex
            .clone()
            .or_else(|| self.env_codex_home.clone())
            .unwrap_or_else(|| self.home.join(".codex"))
    }

    pub fn claude_root(&self) -> PathBuf {
        self.explicit_claude
            .clone()
            .unwrap_or_else(|| self.home.join(".claude"))
    }

    pub fn cursor_candidates(&self) -> Vec<PathBuf> {
        let mut paths = Vec::new();
        if let Some(appdata) = self.env_dir("APPDATA") {
            paths.push(appdata.join("Cursor"));
        }
        #[cfg(target_os = "macos")]
        {
            paths.push(self.home.join("Library/Application Support").join("Cursor"));
        }
        paths.push(self.home.join(".cursor"));
        paths
    }

    pub fn env_dir(&self, key: &str) -> Option<PathBuf> {
        self.use_environment
            .then(|| std::env::var_os(key))
            .flatten()
            .filter(|value| !value.is_empty())
            .map(PathBuf::from)
    }
}

pub fn user_home() -> PathBuf {
    std::env::var_os("USERPROFILE")
        .or_else(|| std::env::var_os("HOME"))
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("."))
}

pub const JSONL_WALK_MAX_DEPTH: usize = 6;
pub const JSONL_WALK_MAX_ENTRIES: usize = 4096;
pub const JSONL_WALK_MAX_DURATION: Duration = Duration::from_millis(250);

pub fn walk_budget_exceeded(started: Instant, visited: usize) -> bool {
    visited >= JSONL_WALK_MAX_ENTRIES || started.elapsed() >= JSONL_WALK_MAX_DURATION
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_test_paths_are_separate_from_the_installed_application() {
        let home = PathBuf::from("/tmp/tokendance-local-test-home");
        let paths = local_test_paths(&home);
        let root = home.join("Library/Application Support/io.tokendance.desktop.local-test");
        assert_eq!(paths.collector, root.join("collector"));
        assert_eq!(paths.logs, root.join("logs"));
        assert_eq!(paths.cache, root.join("cache"));
        assert!(!paths.collector.starts_with(home.join("Library/Application Support/io.tokendance.desktop")));
    }

    #[test]
    fn protocol_identity_matches_rustc_target() {
        let identity = protocol_identity();
        match std::env::consts::OS {
            "macos" | "windows" => {
                let identity = identity.unwrap();
                match std::env::consts::ARCH {
                    "aarch64" => assert_eq!(identity.arch, Architecture::Aarch64),
                    "x86_64" => assert_eq!(identity.arch, Architecture::X8664),
                    _ => {}
                }
            }
            _ => assert!(identity.is_err()),
        }
    }

    #[test]
    fn isolated_codex_root_ignores_process_env() {
        let home = PathBuf::from("/tmp/td-home");
        let resolver = PathResolver::isolated(home.clone());
        assert_eq!(resolver.codex_root(), home.join(".codex"));
        let resolver = PathResolver::isolated(home.clone())
            .with_explicit_codex(PathBuf::from("/custom/codex"));
        assert_eq!(resolver.codex_root(), PathBuf::from("/custom/codex"));
    }

    #[test]
    fn cursor_candidates_include_macos_application_support() {
        let home = PathBuf::from("/tmp/td-home");
        let resolver = PathResolver::isolated(home.clone());
        let candidates = resolver.cursor_candidates();
        assert!(candidates.contains(&home.join(".cursor")));
        #[cfg(target_os = "macos")]
        {
            assert!(candidates.contains(&home.join("Library/Application Support").join("Cursor")));
        }
    }

    #[test]
    fn instance_lock_is_exclusive() {
        let root = tempfile::tempdir().unwrap();
        let paths = AppPaths::for_root(root.path().to_path_buf());
        let first = InstanceLock::acquire(&paths).unwrap();
        let second = InstanceLock::acquire(&paths);
        assert!(second.is_err(), "{second:?}");
        drop(first);
        InstanceLock::acquire(&paths).unwrap();
    }

    #[test]
    fn interrupted_home_dir_migration_copies_remaining_files() {
        let old_root = tempfile::tempdir().unwrap();
        let new_root = tempfile::tempdir().unwrap();
        let old = old_root.path().join("TokenDance").join("collector");
        fs::create_dir_all(old.join("spool")).unwrap();
        write_private_file(&old.join("installation-id"), b"ins_same").unwrap();
        write_private_file(&old.join("usage-ledger.json"), b"{\"days\":[]}").unwrap();
        write_private_file(&old.join("tokendance.sqlite3"), b"sqlite").unwrap();
        write_private_file(&old.join("spool").join("0000000000000001.wal"), b"wal").unwrap();
        let paths = AppPaths::for_root(new_root.path().to_path_buf());
        paths.ensure().unwrap();
        // Simulate a crash after installation-id was moved, before ledger/WAL.
        write_private_file(&paths.installation_id_path(), b"ins_same").unwrap();
        migrate_legacy_collector_dir(&old, &paths).unwrap();
        assert_eq!(
            fs::read(paths.collector.join("usage-ledger.json")).unwrap(),
            b"{\"days\":[]}"
        );
        assert_eq!(
            fs::read(paths.collector.join("tokendance.sqlite3")).unwrap(),
            b"sqlite"
        );
        assert_eq!(
            fs::read(paths.collector.join("spool").join("0000000000000001.wal")).unwrap(),
            b"wal"
        );
        assert!(paths
            .collector
            .join(".migrated-from-home-tokendance")
            .exists());
    }

    #[test]
    fn private_file_is_owner_readable() {
        let root = tempfile::tempdir().unwrap();
        let path = root.path().join("secret.txt");
        write_private_file(&path, b"hello").unwrap();
        assert_eq!(fs::read(&path).unwrap(), b"hello");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
    }
}
