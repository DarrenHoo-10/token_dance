//! macOS in-app update: mount the verified DMG, check the new bundle, stage it
//! beside the installed app and let a detached helper swap it in after this
//! process exits. The installed bundle is never modified while it is running.
use std::{
    path::{Path, PathBuf},
    process::{Command, Stdio},
};

const BUNDLE_NAME: &str = "TokenDance.app";

/// Waits for the old process, swaps bundles with rollback, then reopens the app.
/// Arguments: pid, installed bundle, staged bundle.
const SWAP_SCRIPT: &str = r#"
pid="$1"; app="$2"; new="$3"; old="$2.old-update"
i=0
while kill -0 "$pid" 2>/dev/null; do
  i=$((i+1))
  if [ "$i" -gt 150 ]; then rm -rf "$new"; exit 1; fi
  sleep 0.2
done
rm -rf "$old"
if mv "$app" "$old"; then
  if mv "$new" "$app"; then rm -rf "$old"; else mv "$old" "$app"; fi
fi
rm -rf "$new"
[ -z "$TOKENDANCE_NO_OPEN" ] && open "$app"
exit 0
"#;

fn run(program: &str, args: &[&std::ffi::OsStr]) -> Result<String, String> {
    let output = Command::new(program)
        .args(args)
        .stdin(Stdio::null())
        .output()
        .map_err(|_| "install_failed".to_string())?;
    if !output.status.success() {
        return Err("install_failed".into());
    }
    // codesign writes its description to stderr.
    let mut text = String::from_utf8_lossy(&output.stdout).into_owned();
    text.push_str(&String::from_utf8_lossy(&output.stderr));
    Ok(text)
}

/// The `.app` that contains the running executable; None for dev/unbundled runs.
pub fn installed_bundle() -> Option<PathBuf> {
    let exe = std::env::current_exe().ok()?.canonicalize().ok()?;
    let bundle = exe.ancestors().nth(3)?.to_path_buf();
    (bundle.extension()? == "app").then_some(bundle)
}

/// The designated requirement text from `codesign -d -r-` output.
fn designated_requirement(output: &str) -> Option<String> {
    output
        .lines()
        .find_map(|line| line.strip_prefix("designated => "))
        .map(|requirement| requirement.trim().to_owned())
}

/// Only a requirement bound to a certificate can vouch for the next release; an
/// ad-hoc cdhash requirement matches nothing but that exact build.
fn certificate_requirement(requirement: &str) -> bool {
    requirement.contains("certificate leaf = H\"") && !requirement.contains("cdhash")
}

fn identifier(bundle: &Path) -> Result<String, String> {
    let text = run("/usr/bin/codesign", &["-d".as_ref(), "-vv".as_ref(), bundle.as_os_str()])?;
    text.lines()
        .find_map(|line| line.strip_prefix("Identifier="))
        .map(str::to_owned)
        .ok_or_else(|| "install_failed".into())
}

fn bundle_version(bundle: &Path) -> Result<String, String> {
    let info = bundle.join("Contents/Info");
    Ok(run("/usr/bin/defaults", &["read".as_ref(), info.as_os_str(), "CFBundleShortVersionString".as_ref()])?
        .trim()
        .to_owned())
}

fn verify_staged(installed: &Path, staged: &Path, version: &str) -> Result<(), String> {
    run(
        "/usr/bin/codesign",
        &["--verify".as_ref(), "--deep".as_ref(), "--strict".as_ref(), staged.as_os_str()],
    )
    .map_err(|_| "signature")?;
    if identifier(installed)? != identifier(staged)? || bundle_version(staged)? != version {
        return Err("signature".into());
    }
    let current = run("/usr/bin/codesign", &["-d".as_ref(), "-r-".as_ref(), installed.as_os_str()])?;
    if let Some(requirement) = designated_requirement(&current).filter(|r| certificate_requirement(r)) {
        // The installed app is signed by a stable certificate: the update must be too.
        // A leading `=` makes codesign read the requirement inline, not as a file path.
        let inline = format!("={requirement}");
        run(
            "/usr/bin/codesign",
            &["--verify".as_ref(), "-R".as_ref(), inline.as_ref(), staged.as_os_str()],
        )
        .map_err(|_| "signature")?;
    }
    Ok(())
}

struct Mounted(PathBuf);
impl Drop for Mounted {
    fn drop(&mut self) {
        let _ = Command::new("/usr/bin/hdiutil")
            .args(["detach", "-force", "-quiet"])
            .arg(&self.0)
            .stdin(Stdio::null())
            .status();
    }
}

fn spawn_swap(installed: &Path, staged: &Path) -> Result<(), String> {
    use std::os::unix::process::CommandExt;
    Command::new("/bin/sh")
        .args(["-c", SWAP_SCRIPT, "sh", &std::process::id().to_string()])
        .arg(installed)
        .arg(staged)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .process_group(0)
        .spawn()
        .map(|_| ())
        .map_err(|_| "install_failed".into())
}

/// Stage and verify the update from `dmg`, then schedule the swap. The caller
/// must exit promptly afterwards. The DMG has already passed size and SHA-256.
pub fn install(dmg: &Path, version: &str) -> Result<(), String> {
    let installed = installed_bundle().ok_or("unsupported")?;
    let parent = installed.parent().ok_or("install_failed")?;
    // Replacing the bundle needs write access to its folder; fail before any work.
    tempfile::Builder::new()
        .prefix(".tokendance-write-check")
        .tempfile_in(parent)
        .map_err(|_| "install_permission")?;
    let staged = parent.join(".TokenDance-update.app");
    let _ = std::fs::remove_dir_all(&staged);
    let mount = tempfile::tempdir().map_err(|_| "storage")?;
    let result = (|| {
        run(
            "/usr/bin/hdiutil",
            &[
                "attach".as_ref(), "-nobrowse".as_ref(), "-readonly".as_ref(), "-noverify".as_ref(),
                "-noautoopen".as_ref(), "-mountpoint".as_ref(), mount.path().as_os_str(), dmg.as_os_str(),
            ],
        )?;
        let _mounted = Mounted(mount.path().to_path_buf());
        let source = mount.path().join(BUNDLE_NAME);
        if !source.is_dir() {
            return Err("install_failed".to_string());
        }
        run("/usr/bin/ditto", &[source.as_os_str(), staged.as_os_str()])?;
        Ok(())
    })();
    let result = result
        .and_then(|_| verify_staged(&installed, &staged, version))
        .and_then(|_| spawn_swap(&installed, &staged));
    if result.is_err() {
        let _ = std::fs::remove_dir_all(&staged);
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_certificate_bound_requirements_are_stable() {
        let out = "Executable=/x\ndesignated => identifier \"io.tokendance.desktop\" and certificate leaf = H\"ab12\"\n";
        let req = designated_requirement(out).unwrap();
        assert!(certificate_requirement(&req));
        assert!(!certificate_requirement("cdhash H\"e000\""));
        assert!(designated_requirement("nothing").is_none());
    }

    #[test]
    fn swap_replaces_the_bundle_and_rolls_back_on_failure() {
        let dir = tempfile::tempdir().unwrap();
        let (app, new) = (dir.path().join("T.app"), dir.path().join(".new.app"));
        std::fs::create_dir_all(&app).unwrap();
        std::fs::create_dir_all(&new).unwrap();
        std::fs::write(app.join("v"), "old").unwrap();
        std::fs::write(new.join("v"), "new").unwrap();
        let swap = |app: &Path, new: &Path| {
            Command::new("/bin/sh")
                .env("TOKENDANCE_NO_OPEN", "1")
                .args(["-c", SWAP_SCRIPT, "sh", "99999999"])
                .arg(app)
                .arg(new)
                .status()
                .unwrap()
        };
        assert!(swap(&app, &new).success());
        assert_eq!(std::fs::read_to_string(app.join("v")).unwrap(), "new");
        assert!(!new.exists() && !dir.path().join("T.app.old-update").exists());
        // A missing staged bundle must leave the installed app untouched.
        assert!(swap(&app, &new).success());
        assert_eq!(std::fs::read_to_string(app.join("v")).unwrap(), "new");
    }
}
