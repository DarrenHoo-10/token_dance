//! Portable Windows updates. Release metadata and SHA-256 come only from the
//! project's HTTPS GitHub API; cached executables are revalidated before use.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    path::{Path, PathBuf},
    sync::Arc,
    time::Duration,
};
use tauri::{AppHandle, Manager, State};
use tokio::sync::{Mutex, RwLock};

const RELEASES: &str =
    "https://api.github.com/repos/DarrenHoo-10/token_dance/releases?per_page=100";
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
    name: String,
    browser_download_url: String,
    digest: Option<String>,
    size: u64,
}
#[derive(Default, Clone, Deserialize)]
struct Release {
    tag_name: String,
    #[serde(default)]
    draft: bool,
    body: Option<String>,
    published_at: Option<String>,
    assets: Vec<Asset>,
}
#[derive(Clone)]
struct Candidate {
    version: String,
    notes: String,
    published_at: Option<String>,
    asset: Asset,
}

fn parse_version(value: &str) -> Option<semver::Version> {
    semver::Version::parse(value.strip_prefix('v').unwrap_or(value)).ok()
}
fn valid_digest(value: &str) -> bool {
    value
        .strip_prefix("sha256:")
        .is_some_and(|hash| hash.len() == 64 && hash.bytes().all(|c| c.is_ascii_hexdigit()))
}
fn select_release(releases: Vec<Release>, current: &str) -> Result<Option<Candidate>, String> {
    let current = parse_version(current).ok_or("invalid_current_version")?;
    // Our public desktop channel is currently published as GitHub prereleases.
    // Only plain numeric tags qualify; alpha/beta semver tags and drafts do not.
    let best = releases
        .into_iter()
        .filter(|r| !r.draft)
        .filter_map(|r| {
            parse_version(&r.tag_name)
                .filter(|v| v.pre.is_empty() && v > &current)
                .map(|v| (v, r))
        })
        .max_by(|a, b| a.0.cmp(&b.0));
    let Some((version, release)) = best else {
        return Ok(None);
    };
    let asset = release
        .assets
        .into_iter()
        .find(|a| a.name == "TokenDance.exe")
        .ok_or("asset_missing")?;
    let expected = format!(
        "https://github.com/DarrenHoo-10/token_dance/releases/download/{}/TokenDance.exe",
        release.tag_name
    );
    if asset.browser_download_url != expected
        || asset.size == 0
        || asset.size > MAX_DOWNLOAD
        || !asset.digest.as_deref().is_some_and(valid_digest)
    {
        return Err("unverified_release".into());
    }
    Ok(Some(Candidate {
        version: version.to_string(),
        notes: release.body.unwrap_or_default(),
        published_at: release.published_at,
        asset,
    }))
}

fn client(timeout: u64) -> Result<reqwest::Client, String> {
    // reqwest's system-proxy feature follows this machine's network settings.
    reqwest::Client::builder()
        .user_agent(concat!("TokenDance/", env!("CARGO_PKG_VERSION")))
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(timeout))
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            let url = attempt.url();
            if attempt.previous().len() < 5
                && url.scheme() == "https"
                && matches!(
                    url.host_str(),
                    Some(
                        "github.com"
                            | "release-assets.githubusercontent.com"
                            | "objects.githubusercontent.com"
                    )
                )
            {
                attempt.follow()
            } else {
                attempt.stop()
            }
        }))
        .build()
        .map_err(|_| "network".into())
}
async fn latest() -> Result<Option<Candidate>, String> {
    let mut response = client(20)?
        .get(RELEASES)
        .header("Accept", "application/vnd.github+json")
        .header("X-GitHub-Api-Version", "2022-11-28")
        .send()
        .await
        .map_err(|_| "network")?;
    if !response.status().is_success() {
        return Err(
            if response.status().as_u16() == 403 || response.status().as_u16() == 429 {
                "rate_limited"
            } else {
                "network"
            }
            .into(),
        );
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(|_| "network")? {
        if bytes.len() + chunk.len() > 2 * 1024 * 1024 {
            return Err("invalid_release".into());
        }
        bytes.extend_from_slice(&chunk);
    }
    let releases = serde_json::from_slice(&bytes).map_err(|_| "invalid_release")?;
    select_release(releases, CURRENT)
}
fn verify(bytes: &[u8], candidate: &Candidate) -> bool {
    bytes.len() as u64 == candidate.asset.size
        && bytes.starts_with(b"MZ")
        && candidate.asset.digest.as_deref().is_some_and(|expected| {
            format!("sha256:{:x}", Sha256::digest(bytes)).eq_ignore_ascii_case(expected)
        })
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
            .get(&candidate.asset.browser_download_url)
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
        app.state::<crate::single_instance::InstanceGuard>().release();
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
    fn release(tag: &str) -> Release {
        Release { tag_name: tag.into(), assets: vec![Asset { name: "TokenDance.exe".into(), size: 2, digest: Some(format!("sha256:{:x}", Sha256::digest(b"MZ"))), browser_download_url: format!("https://github.com/DarrenHoo-10/token_dance/releases/download/{tag}/TokenDance.exe") }], ..Default::default() }
    }
    #[test]
    fn semantic_order_and_no_downgrade() {
        let selected = select_release(
            vec![release("v0.1.9"), release("v0.1.12"), release("v0.1.11")],
            "0.1.10",
        )
        .unwrap()
        .unwrap();
        assert_eq!(selected.version, "0.1.12");
        assert!(
            select_release(vec![release("v0.1.9"), release("v0.1.10")], "0.1.10")
                .unwrap()
                .is_none()
        );
    }
    #[test]
    fn drafts_and_experimental_tags_are_excluded() {
        let mut draft = release("v9.0.0");
        draft.draft = true;
        assert!(
            select_release(vec![draft, release("v1.0.0-beta.1")], "0.1.11")
                .unwrap()
                .is_none()
        );
    }
    #[test]
    fn missing_checksums_or_foreign_assets_fail_closed() {
        let mut r = release("v0.1.12");
        r.assets[0].digest = None;
        assert!(select_release(vec![r], "0.1.11").is_err());
        let mut r = release("v0.1.12");
        r.assets[0].browser_download_url = "https://evil.test/TokenDance.exe".into();
        assert!(select_release(vec![r], "0.1.11").is_err());
    }
    #[test]
    fn tampered_or_truncated_downloads_are_rejected() {
        let candidate = select_release(vec![release("v0.1.12")], "0.1.11")
            .unwrap()
            .unwrap();
        assert!(verify(b"MZ", &candidate));
        assert!(!verify(b"MZextra", &candidate));
        assert!(!verify(b"ZZ", &candidate));
        assert!(!verify(b"M", &candidate));
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
