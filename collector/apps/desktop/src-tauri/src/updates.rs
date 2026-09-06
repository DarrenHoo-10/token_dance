//! Portable Windows updates. Release metadata and SHA-256 come only from the
//! project's fixed HTTPS release manifest; cached executables are revalidated before use.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    path::{Path, PathBuf},
    sync::Arc,
    time::Duration,
};
use tauri::{AppHandle, Manager, State};
use tokio::sync::{Mutex, RwLock};

const RELEASES: &str = "https://www.nexorai.com.cn/token-dance/releases/stable.json";
const MAX_DOWNLOAD: u64 = 150 * 1024 * 1024;
const CURRENT: &str = env!("CARGO_PKG_VERSION");
const SUPPORTED: bool = cfg!(all(target_os = "windows", target_arch = "x86_64"));

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateStatus {
    current_version: String,
    version: Option<String>,
    notes: String,
    published_at: Option<String>,
    phase: String,
    auto_update: bool,
    progress: u8,
    checked_at: Option<String>,
    error: Option<String>,
    supported: bool,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Preferences {
    auto_update: bool,
}

fn directory() -> PathBuf {
    crate::state::app_data_root().join("updates")
}
fn cached_binary() -> PathBuf {
    directory().join("pending.exe")
}
fn auto_enabled() -> bool {
    let path = directory().join("preferences.json");
    match std::fs::read(path) {
        Ok(bytes) => serde_json::from_slice::<Preferences>(&bytes)
            .map(|p| p.auto_update)
            .unwrap_or(false),
        Err(e) => e.kind() == std::io::ErrorKind::NotFound,
    }
}

#[derive(Default, Clone, Deserialize)]
struct Asset {
    url: String,
    sha256: String,
    size: u64,
}
#[derive(Default, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Release {
    version: String,
    platform: String,
    notes: String,
    published_at: String,
    exe: Asset,
    zip: Option<Asset>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Manifest {
    schema_version: u32,
    releases: Vec<Release>,
}
#[derive(Clone)]
struct Candidate {
    version: String,
    notes: String,
    published_at: Option<String>,
    asset: Asset,
}

fn parse_version(value: &str) -> Option<semver::Version> {
    let version = semver::Version::parse(value).ok()?;
    (version.pre.is_empty() && version.build.is_empty()).then_some(version)
}
fn valid_digest(value: &str) -> bool {
    value.len() == 64 && value.bytes().all(|hash| hash.is_ascii_hexdigit())
}
// URLs are supplied by the fixed, trusted first-party HTTPS manifest. No tokens
// or expiring signed links belong in this public feed. Downloads cannot redirect.
fn valid_asset_url(value: &str) -> bool {
    let Ok(url) = reqwest::Url::parse(value) else {
        return false;
    };
    let Some(host) = url.host_str() else {
        return false;
    };
    url.scheme() == "https"
        && url.username().is_empty()
        && url.password().is_none()
        && url.query().is_none()
        && url.fragment().is_none()
        && url.port_or_known_default() == Some(443)
        && host.contains('.')
        && !host.ends_with('.')
        && !host.ends_with(".localhost")
        && !host.ends_with(".local")
        && !host.ends_with(".internal")
        && host.parse::<std::net::IpAddr>().is_err()
        && !host.contains(':')
        && !value.bytes().any(|b| b.is_ascii_whitespace() || b == b'\\')
}
fn valid_asset(asset: &Asset) -> bool {
    valid_asset_url(&asset.url)
        && asset.size > 0
        && asset.size <= MAX_DOWNLOAD
        && valid_digest(&asset.sha256)
}
fn select_release(manifest: Manifest, current: &str) -> Result<Option<Candidate>, String> {
    let current = parse_version(current).ok_or("invalid_current_version")?;
    if manifest.schema_version != 1 || manifest.releases.len() > 100 {
        return Err("invalid_release".into());
    }
    let mut best: Option<(semver::Version, Release)> = None;
    let mut seen = std::collections::HashSet::new();
    for release in manifest.releases {
        if release.platform != "windows-x64" {
            continue;
        }
        let version = parse_version(&release.version).ok_or("invalid_release")?;
        if !seen.insert(version.clone()) {
            return Err("invalid_release".into());
        }
        if best
            .as_ref()
            .is_none_or(|(previous, _)| version > *previous)
        {
            best = Some((version, release));
        }
    }
    let Some((version, release)) = best else {
        return Ok(None);
    };
    // Validate newest metadata even when installed version is current. Never
    // silently fall back to an older release when the newest package is broken.
    if !valid_asset(&release.exe)
        || release
            .zip
            .as_ref()
            .is_some_and(|asset| !valid_asset(asset))
        || chrono::DateTime::parse_from_rfc3339(&release.published_at).is_err()
    {
        return Err("unverified_release".into());
    }
    if version <= current {
        return Ok(None);
    }
    Ok(Some(Candidate {
        version: version.to_string(),
        notes: release.notes,
        published_at: Some(release.published_at),
        asset: release.exe,
    }))
}

fn client(timeout: u64) -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .user_agent(concat!("TokenDance/", env!("CARGO_PKG_VERSION")))
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(timeout))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "network".into())
}
async fn latest() -> Result<Option<Candidate>, String> {
    let mut response = client(20)?
        .get(RELEASES)
        .header("Accept", "application/json")
        .header("Cache-Control", "no-cache")
        .send()
        .await
        .map_err(|_| "network")?;
    if !response.status().is_success() {
        return Err(if response.status().as_u16() == 429 {
            "rate_limited"
        } else {
            "network"
        }
        .into());
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(|_| "network")? {
        if bytes.len() + chunk.len() > 2 * 1024 * 1024 {
            return Err("invalid_release".into());
        }
        bytes.extend_from_slice(&chunk);
    }
    let manifest = serde_json::from_slice(&bytes).map_err(|_| "invalid_release")?;
    select_release(manifest, CURRENT)
}
fn verify(bytes: &[u8], candidate: &Candidate) -> bool {
    bytes.len() as u64 == candidate.asset.size
        && bytes.starts_with(b"MZ")
        && format!("{:x}", Sha256::digest(bytes)).eq_ignore_ascii_case(&candidate.asset.sha256)
}
fn verified_cache(candidate: &Candidate) -> bool {
    let path = cached_binary();
    if std::fs::metadata(&path).map(|m| m.len()).ok() != Some(candidate.asset.size) {
        return false;
    }
    std::fs::read(path).is_ok_and(|bytes| verify(&bytes, candidate))
}
fn persist(path: &Path, bytes: &[u8]) -> Result<(), String> {
    use std::io::Write;
    std::fs::create_dir_all(directory()).map_err(|_| "storage")?;
    let mut temporary = tempfile::NamedTempFile::new_in(directory()).map_err(|_| "storage")?;
    temporary.write_all(bytes).map_err(|_| "storage")?;
    temporary.as_file().sync_all().map_err(|_| "storage")?;
    temporary.persist(path).map_err(|_| "storage")?;
    Ok(())
}

pub struct UpdateState {
    snapshot: RwLock<UpdateStatus>,
    candidate: RwLock<Option<Candidate>>,
    operation: Mutex<()>,
}
impl Default for UpdateState {
    fn default() -> Self {
        Self {
            snapshot: RwLock::new(UpdateStatus {
                current_version: CURRENT.into(),
                version: None,
                notes: String::new(),
                published_at: None,
                phase: "idle".into(),
                auto_update: auto_enabled(),
                progress: 0,
                checked_at: None,
                error: None,
                supported: SUPPORTED,
            }),
            candidate: RwLock::new(None),
            operation: Mutex::new(()),
        }
    }
}
impl UpdateState {
    async fn failed(&self, error: String) {
        let mut view = self.snapshot.write().await;
        view.phase = "error".into();
        view.error = Some(error);
    }
    async fn check(&self) -> Result<(), String> {
        {
            let mut view = self.snapshot.write().await;
            view.phase = "checking".into();
            view.error = None;
        }
        let result = latest().await?;
        let ready = result.as_ref().is_some_and(verified_cache);
        let mut view = self.snapshot.write().await;
        view.checked_at = Some(chrono::Utc::now().to_rfc3339());
        view.version = result.as_ref().map(|c| c.version.clone());
        view.notes = result.as_ref().map(|c| c.notes.clone()).unwrap_or_default();
        view.published_at = result.as_ref().and_then(|c| c.published_at.clone());
        view.phase = if ready {
            "ready"
        } else if result.is_some() {
            "available"
        } else {
            "latest"
        }
        .into();
        view.progress = if ready { 100 } else { 0 };
        *self.candidate.write().await = result;
        Ok(())
    }
    async fn download(&self) -> Result<(), String> {
        let candidate = self.candidate.read().await.clone().ok_or("no_update")?;
        if verified_cache(&candidate) {
            self.snapshot.write().await.phase = "ready".into();
            return Ok(());
        }
        {
            let mut view = self.snapshot.write().await;
            view.phase = "downloading".into();
            view.progress = 0;
            view.error = None;
        }
        let mut response = client(300)?
            .get(&candidate.asset.url)
            .send()
            .await
            .map_err(|_| "network")?;
        if !response.status().is_success() {
            return Err("network".into());
        }
        let mut bytes = Vec::new();
        while let Some(chunk) = response.chunk().await.map_err(|_| "network")? {
            if bytes.len() as u64 + chunk.len() as u64 > candidate.asset.size {
                return Err("integrity".into());
            }
            bytes.extend_from_slice(&chunk);
            self.snapshot.write().await.progress =
                ((bytes.len() as u64 * 100) / candidate.asset.size) as u8;
        }
        if !verify(&bytes, &candidate) {
            return Err("integrity".into());
        }
        persist(&cached_binary(), &bytes)?;
        self.snapshot.write().await.phase = "ready".into();
        Ok(())
    }
    async fn background_check(&self) {
        let Ok(_guard) = self.operation.try_lock() else {
            return;
        };
        if !SUPPORTED {
            return;
        }
        let result = async {
            self.check().await?;
            if self.snapshot.read().await.auto_update && self.candidate.read().await.is_some() {
                self.download().await?;
            }
            Ok::<_, String>(())
        }
        .await;
        if let Err(error) = result {
            self.failed(error).await
        }
    }
}

#[tauri::command]
pub async fn get_update_status(state: State<'_, Arc<UpdateState>>) -> Result<UpdateStatus, String> {
    Ok(state.snapshot.read().await.clone())
}
#[tauri::command]
pub async fn check_for_updates(state: State<'_, Arc<UpdateState>>) -> Result<UpdateStatus, String> {
    state.background_check().await;
    Ok(state.snapshot.read().await.clone())
}
#[tauri::command]
pub async fn set_auto_update(
    enabled: bool,
    state: State<'_, Arc<UpdateState>>,
) -> Result<UpdateStatus, String> {
    // Write before acknowledgment. This switch does not interrupt an active download.
    let mut view = state.snapshot.write().await;
    persist(
        &directory().join("preferences.json"),
        &serde_json::to_vec(&Preferences {
            auto_update: enabled,
        })
        .map_err(|_| "storage")?,
    )?;
    view.auto_update = enabled;
    let snapshot = view.clone();
    drop(view);
    if enabled {
        let state = state.inner().clone();
        tauri::async_runtime::spawn(async move {
            state.background_check().await;
        });
    }
    Ok(snapshot)
}
#[tauri::command]
pub async fn install_update(
    app: AppHandle,
    state: State<'_, Arc<UpdateState>>,
) -> Result<(), String> {
    if !SUPPORTED {
        return Err("unsupported".into());
    }
    let _guard = state.operation.try_lock().map_err(|_| "busy")?;
    let result = async {
        // Refresh trusted metadata immediately before replacing the binary.
        state.check().await?;
        state.download().await?;
        state.snapshot.write().await.phase = "installing".into();
        let candidate = state.candidate.read().await.clone().ok_or("no_update")?;
        replace_verified(&candidate)?;
        let _ = app.state::<crate::state::AppState>().shutdown().await;
        app.state::<crate::single_instance::InstanceGuard>()
            .release();
        app.restart();
        #[allow(unreachable_code)]
        Ok::<(), String>(())
    }
    .await;
    if let Err(ref error) = result {
        state.failed(error.clone()).await;
    }
    result
}

fn replace_verified(candidate: &Candidate) -> Result<(), String> {
    // Re-read into an exclusive temporary file so validation and replacement use
    // the same bytes, even if another instance replaces the download cache.
    let bytes = std::fs::read(cached_binary()).map_err(|_| "storage")?;
    if !verify(&bytes, candidate) {
        return Err("integrity".into());
    }
    use std::io::Write;
    let mut file = tempfile::NamedTempFile::new_in(directory()).map_err(|_| "storage")?;
    file.write_all(&bytes).map_err(|_| "storage")?;
    file.as_file().sync_all().map_err(|_| "storage")?;
    let current = std::env::current_exe().map_err(|_| "install_failed")?;
    replace_with_rollback(&current, || self_replace::self_replace(file.path()))?;
    let _ = std::fs::remove_file(cached_binary());
    Ok(())
}

fn replace_with_rollback(
    current: &Path,
    replace: impl FnOnce() -> std::io::Result<()>,
) -> Result<(), String> {
    // self-replace moves the original aside before copying the new file. Keep
    // an independent backup so a copy/cleanup failure cannot remove the app.
    let parent = current.parent().ok_or("install_failed")?;
    let backup = tempfile::NamedTempFile::new_in(parent).map_err(|_| "install_failed")?;
    std::fs::copy(current, backup.path()).map_err(|_| "install_failed")?;
    backup.as_file().sync_all().map_err(|_| "install_failed")?;
    if replace().is_err() {
        if !current.exists() {
            // Same-volume rename does not need space for another executable.
            backup.persist(current).map_err(|_| "restore_failed")?;
        }
        return Err("install_failed".into());
    }
    Ok(())
}

/// Called before creating any window or collector. Offline failures leave the
/// current app usable. No visible window is restarted by background checks.
pub fn apply_pending_before_start(release_instance: impl FnOnce()) -> bool {
    if !SUPPORTED || !auto_enabled() || !cached_binary().is_file() {
        return false;
    }
    let candidate = tauri::async_runtime::block_on(latest());
    let Ok(Some(candidate)) = candidate else {
        return false;
    };
    if !verified_cache(&candidate) {
        return false;
    }
    let Ok(executable) = std::env::current_exe() else {
        return false;
    };
    if replace_verified(&candidate).is_err() {
        return false;
    }
    let mut command = std::process::Command::new(executable);
    command.args(std::env::args_os().skip(1));
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x08000000);
    }
    // The replacement must be able to claim the identity before this process exits.
    release_instance();
    match command.spawn() {
        Ok(_) => true,
        Err(error) => {
            eprintln!("failed to launch updated TokenDance: {error}");
            // Never initialize a collector after giving up the instance claim.
            true
        }
    }
}
pub fn start(app: &AppHandle) {
    let state = app.state::<Arc<UpdateState>>().inner().clone();
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(Duration::from_secs(10)).await;
        loop {
            state.background_check().await;
            tokio::time::sleep(Duration::from_secs(4 * 60 * 60)).await;
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    fn release(version: &str) -> Release {
        Release {
            version: version.into(),
            platform: "windows-x64".into(),
            published_at: "2026-09-07T00:00:00Z".into(),
            exe: Asset {
                url: format!("https://downloads.example.com/{version}/TokenDance.exe"),
                size: 2,
                sha256: format!("{:x}", Sha256::digest(b"MZ")),
            },
            ..Default::default()
        }
    }
    fn manifest(releases: Vec<Release>) -> Manifest {
        Manifest {
            schema_version: 1,
            releases,
        }
    }
    #[test]
    fn semantic_order_and_no_downgrade() {
        let selected = select_release(
            manifest(vec![release("0.1.9"), release("0.1.12"), release("0.1.11")]),
            "0.1.10",
        )
        .unwrap()
        .unwrap();
        assert_eq!(selected.version, "0.1.12");
        assert!(select_release(
            manifest(vec![release("0.1.9"), release("0.1.10")]),
            "0.1.10"
        )
        .unwrap()
        .is_none());
    }
    #[test]
    fn schema_platform_and_versions_are_checked() {
        let mut other = release("9.0.0");
        other.platform = "macos-arm64".into();
        assert!(select_release(manifest(vec![other]), "0.1.11")
            .unwrap()
            .is_none());
        for version in ["1.0.0-beta.1", "01.0.0", "v1.0.0", "1.0.0+test"] {
            assert!(select_release(manifest(vec![release(version)]), "0.1.11").is_err());
        }
        assert!(select_release(
            Manifest {
                schema_version: 2,
                releases: vec![]
            },
            "0.1.11"
        )
        .is_err());
        assert!(
            select_release(manifest(vec![release("1.0.0"), release("1.0.0")]), "0.1.11").is_err()
        );
    }
    #[test]
    fn missing_checksums_and_insecure_urls_fail_closed() {
        let mut r = release("0.1.12");
        r.exe.sha256.clear();
        assert!(select_release(manifest(vec![r]), "0.1.11").is_err());
        for url in [
            "http://downloads.example.com/a.exe",
            "file:///tmp/a.exe",
            "https://u:p@downloads.example.com/a.exe",
            "https://127.0.0.1/a.exe",
            "https://[::1]/a.exe",
            "https://localhost/a.exe",
            "https://host.local/a.exe",
            "https://cdn.example.com/a.exe?token=secret",
            "https://cdn.example.com:8443/a.exe",
            "https://cdn.example.com/a.exe#hash",
        ] {
            assert!(!valid_asset_url(url), "{url}");
        }
        assert!(valid_asset_url(
            "https://bucket.oss-region.aliyuncs.com/token-dance/1.0.0/TokenDance.exe"
        ));
    }
    #[test]
    fn broken_newest_does_not_fall_back() {
        let mut newest = release("0.1.12");
        newest.exe.size = MAX_DOWNLOAD + 1;
        assert!(select_release(manifest(vec![release("0.1.11"), newest]), "0.1.10").is_err());
        let mut r = release("0.1.12");
        r.published_at = "invalid".into();
        assert!(select_release(manifest(vec![r]), "0.1.10").is_err());
    }
    #[test]
    fn shared_manifest_contract_and_tampered_downloads() {
        let input = include_str!("../../../../../schemas/fixtures/desktop-release-manifest.json");
        let manifest = serde_json::from_str(input).unwrap();
        let candidate = select_release(manifest, "0.1.0").unwrap().unwrap();
        assert_eq!(candidate.version, "0.2.0");
        assert!(verify(b"MZ", &candidate));
        for bytes in [b"MZextra".as_slice(), b"ZZ", b"M"] {
            assert!(!verify(bytes, &candidate));
        }
    }
    #[tokio::test]
    async fn concurrent_operations_do_not_queue_a_second_install() {
        let state = UpdateState::default();
        let _first = state.operation.lock().await;
        assert!(state.operation.try_lock().is_err());
    }
    #[test]
    fn failed_replacement_restores_original_without_touching_settings() {
        let directory = tempfile::tempdir().unwrap();
        let exe = directory.path().join("TokenDance.exe");
        let settings = directory.path().join("settings.json");
        std::fs::write(&exe, b"original").unwrap();
        std::fs::write(&settings, b"keep").unwrap();
        let result = replace_with_rollback(&exe, || {
            std::fs::rename(&exe, directory.path().join("moved.exe"))?;
            Err(std::io::Error::other("simulated copy failure"))
        });
        assert!(result.is_err());
        assert_eq!(std::fs::read(exe).unwrap(), b"original");
        assert_eq!(std::fs::read(settings).unwrap(), b"keep");
    }
    #[tokio::test]
    #[ignore = "live read-only check of the public release endpoint"]
    async fn live_release_feed() {
        assert!(latest().await.is_ok());
    }

    #[test]
    #[cfg(windows)]
    fn portable_replacement_runs_from_a_directory_with_spaces() {
        let temp = tempfile::tempdir().unwrap();
        let dir = temp.path().join("portable update fixture");
        std::fs::create_dir(&dir).unwrap();
        let exe = dir.join("TokenDance-update-fixture.exe");
        std::fs::copy(std::env::current_exe().unwrap(), &exe).unwrap();
        std::fs::write(dir.join("settings.json"), b"preserve settings").unwrap();
        let result = std::process::Command::new(&exe)
            .args(["--exact", "updates::tests::replacement_child", "--ignored"])
            .env("TOKEN_DANCE_UPDATE_TEST", "1")
            .output()
            .unwrap();
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stdout)
        );
        assert!(std::fs::read(&exe)
            .unwrap()
            .ends_with(b"update-test-overlay"));
        assert_eq!(
            std::fs::read(dir.join("settings.json")).unwrap(),
            b"preserve settings"
        );
        assert!(std::process::Command::new(exe)
            .arg("--list")
            .output()
            .unwrap()
            .status
            .success());
    }

    #[test]
    #[cfg(windows)]
    #[ignore = "only launched by the isolated replacement test"]
    fn replacement_child() {
        if std::env::var("TOKEN_DANCE_UPDATE_TEST").as_deref() != Ok("1") {
            return;
        }
        let exe = std::env::current_exe().unwrap();
        assert_eq!(exe.file_name().unwrap(), "TokenDance-update-fixture.exe");
        assert_eq!(
            exe.parent().unwrap().file_name().unwrap(),
            "portable update fixture"
        );
        let mut bytes = std::fs::read(&exe).unwrap();
        bytes.extend_from_slice(b"update-test-overlay");
        let staged = exe.parent().unwrap().join("staged.exe");
        std::fs::write(&staged, bytes).unwrap();
        replace_with_rollback(&exe, || self_replace::self_replace(&staged)).unwrap();
    }
}
