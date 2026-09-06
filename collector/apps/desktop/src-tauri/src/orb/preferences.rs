use super::model::{
    bump_revision, OrbPreferences, QuotaSelection, ALLOWED_DIAMETERS, SCHEMA_VERSION,
};
use serde::Deserialize;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

pub const PREFERENCES_FILE: &str = "orb-preferences.json";

#[derive(Debug)]
pub enum PreferencesError {
    Conflict { current: OrbPreferences },
    InvalidDiameter(u32),
    Io(String),
}

impl std::fmt::Display for PreferencesError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Conflict { .. } => write!(f, "preferences revision conflict"),
            Self::InvalidDiameter(value) => write!(f, "unsupported orb diameter {value}"),
            Self::Io(error) => write!(f, "{error}"),
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PreferencesPatch {
    pub expected_revision: String,
    pub enabled: Option<bool>,
    pub diameter_dip: Option<u32>,
    pub effects_mode: Option<super::model::EffectsMode>,
    pub hide_on_fullscreen: Option<bool>,
    pub selection: Option<Option<QuotaSelection>>,
    pub placement: Option<super::model::OrbPlacement>,
}

struct Inner {
    path: PathBuf,
    current: OrbPreferences,
}

pub struct PreferencesStore {
    inner: Mutex<Inner>,
}

impl PreferencesStore {
    pub fn load(dir: impl AsRef<Path>) -> Self {
        let path = dir.as_ref().join(PREFERENCES_FILE);
        let current = load_preferences(&path);
        Self {
            inner: Mutex::new(Inner { path, current }),
        }
    }

    pub fn snapshot(&self) -> OrbPreferences {
        self.inner.lock().expect("orb preferences poisoned").current.clone()
    }

    pub fn patch(&self, patch: PreferencesPatch) -> Result<OrbPreferences, PreferencesError> {
        let mut inner = self.inner.lock().expect("orb preferences poisoned");
        if patch.expected_revision != inner.current.revision {
            return Err(PreferencesError::Conflict {
                current: inner.current.clone(),
            });
        }
        if let Some(diameter) = patch.diameter_dip {
            if !ALLOWED_DIAMETERS.contains(&diameter) {
                return Err(PreferencesError::InvalidDiameter(diameter));
            }
        }
        let previous = inner.current.clone();
        apply_patch(&mut inner.current, &patch);
        inner.current.revision = bump_revision(&previous.revision);
        inner.current.schema_version = SCHEMA_VERSION;
        inner.current.sanitize();
        match persist_preferences(&inner.path, &inner.current) {
            Ok(()) => Ok(inner.current.clone()),
            Err(error) => {
                inner.current = previous;
                Err(PreferencesError::Io(error))
            }
        }
    }

    pub fn set_placement(&self, placement: super::model::OrbPlacement) -> Result<OrbPreferences, PreferencesError> {
        let mut inner = self.inner.lock().expect("orb preferences poisoned");
        let previous = inner.current.clone();
        inner.current.placement = placement;
        inner.current.revision = bump_revision(&previous.revision);
        inner.current.sanitize();
        match persist_preferences(&inner.path, &inner.current) {
            Ok(()) => Ok(inner.current.clone()),
            Err(error) => {
                inner.current = previous;
                Err(PreferencesError::Io(error))
            }
        }
    }
}

fn apply_patch(prefs: &mut OrbPreferences, patch: &PreferencesPatch) {
    if let Some(enabled) = patch.enabled {
        prefs.enabled = enabled;
    }
    if let Some(diameter) = patch.diameter_dip {
        prefs.diameter_dip = diameter;
    }
    if let Some(mode) = patch.effects_mode {
        prefs.effects_mode = mode;
    }
    if let Some(hide) = patch.hide_on_fullscreen {
        prefs.hide_on_fullscreen = hide;
    }
    if let Some(selection) = patch.selection.clone() {
        prefs.selection = selection;
    }
    if let Some(placement) = patch.placement.clone() {
        prefs.placement = placement;
    }
}

fn load_preferences(path: &Path) -> OrbPreferences {
    let Ok(bytes) = fs::read(path) else {
        return OrbPreferences::default();
    };
    match serde_json::from_slice::<serde_json::Value>(&bytes) {
        Ok(value) => match value.get("schemaVersion").and_then(|item| item.as_u64()) {
            Some(version) if version == u64::from(SCHEMA_VERSION) => {
                match serde_json::from_value::<OrbPreferences>(value) {
                    Ok(mut prefs) => {
                        prefs.sanitize();
                        prefs
                    }
                    Err(_) => recover_corrupt(path, &bytes),
                }
            }
            Some(_) => {
                let _ = backup_file(path, "bak");
                OrbPreferences::default()
            }
            None => recover_corrupt(path, &bytes),
        },
        Err(_) => recover_corrupt(path, &bytes),
    }
}

fn recover_corrupt(path: &Path, bytes: &[u8]) -> OrbPreferences {
    let backup = path.with_extension("json.corrupt");
    let _ = fs::write(&backup, bytes);
    let _ = fs::remove_file(path);
    OrbPreferences::default()
}

fn backup_file(path: &Path, suffix: &str) -> std::io::Result<()> {
    let backup = path.with_extension(format!("json.{suffix}"));
    fs::copy(path, backup).map(|_| ())
}

fn persist_preferences(path: &Path, prefs: &OrbPreferences) -> Result<(), String> {
    let bytes = serde_json::to_vec_pretty(prefs).map_err(|error| error.to_string())?;
    atomic_write(path, &bytes).map_err(|error| error.to_string())
}

pub(crate) fn atomic_write(path: &Path, bytes: &[u8]) -> std::io::Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, bytes)?;
    match replace_file(&tmp, path) {
        Ok(()) => Ok(()),
        Err(error) => {
            let _ = fs::remove_file(&tmp);
            Err(error)
        }
    }
}

fn replace_file(from: &Path, to: &Path) -> std::io::Result<()> {
    #[cfg(windows)]
    {
        use std::os::windows::ffi::OsStrExt;
        extern "system" {
            fn MoveFileExW(existing: *const u16, new: *const u16, flags: u32) -> i32;
        }
        const MOVEFILE_REPLACE_EXISTING: u32 = 0x1;
        const MOVEFILE_WRITE_THROUGH: u32 = 0x8;
        let from_w: Vec<u16> = from.as_os_str().encode_wide().chain([0]).collect();
        let to_w: Vec<u16> = to.as_os_str().encode_wide().chain([0]).collect();
        let ok = unsafe {
            MoveFileExW(
                from_w.as_ptr(),
                to_w.as_ptr(),
                MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
            )
        };
        if ok == 0 {
            Err(std::io::Error::last_os_error())
        } else {
            Ok(())
        }
    }
    #[cfg(not(windows))]
    {
        fs::rename(from, to)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use std::thread;

    fn patch_enabled(revision: &str, enabled: bool) -> PreferencesPatch {
        PreferencesPatch {
            expected_revision: revision.into(),
            enabled: Some(enabled),
            ..PreferencesPatch::default()
        }
    }

    #[test]
    fn defaults_enabled_but_preserves_user_disable_across_restart() {
        let dir = tempfile::tempdir().unwrap();
        let store = PreferencesStore::load(dir.path());
        assert!(store.snapshot().enabled);
        store.patch(patch_enabled("0", false)).unwrap();
        assert!(!PreferencesStore::load(dir.path()).snapshot().enabled);
    }

    #[test]
    fn concurrent_expected_revision_conflicts() {
        let dir = tempfile::tempdir().unwrap();
        let store = Arc::new(PreferencesStore::load(dir.path()));
        let revision = store.snapshot().revision;
        let left = Arc::clone(&store);
        let right = Arc::clone(&store);
        let expected = revision.clone();
        let first = thread::spawn(move || left.patch(patch_enabled(&expected, true)));
        let expected = revision;
        let second = thread::spawn(move || right.patch(patch_enabled(&expected, false)));
        let results = [first.join().unwrap(), second.join().unwrap()];
        assert_eq!(results.iter().filter(|item| item.is_ok()).count(), 1);
        assert_eq!(
            results
                .iter()
                .filter(|item| matches!(item, Err(PreferencesError::Conflict { .. })))
                .count(),
            1
        );
        assert_eq!(store.snapshot().revision, "1");
    }

    #[test]
    fn write_failure_keeps_previous_prefs() {
        let dir = tempfile::tempdir().unwrap();
        let store = PreferencesStore::load(dir.path());
        store.patch(patch_enabled("0", true)).unwrap();
        let path = dir.path().join(PREFERENCES_FILE);
        fs::remove_file(&path).unwrap();
        fs::create_dir(&path).unwrap();
        let before = store.snapshot();
        let error = store
            .patch(PreferencesPatch {
                expected_revision: before.revision.clone(),
                diameter_dip: Some(160),
                ..PreferencesPatch::default()
            })
            .unwrap_err();
        assert!(matches!(error, PreferencesError::Io(_)));
        let after = store.snapshot();
        assert_eq!(after.enabled, true);
        assert_eq!(after.diameter_dip, 112);
        assert_eq!(after.revision, before.revision);
    }

    #[test]
    fn corrupt_file_uses_defaults_and_keeps_backup() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(PREFERENCES_FILE);
        fs::write(&path, "{not-json").unwrap();
        let store = PreferencesStore::load(dir.path());
        assert_eq!(store.snapshot(), OrbPreferences::default());
        assert!(dir.path().join("orb-preferences.json.corrupt").exists());
        assert!(!path.exists());
    }

    #[test]
    fn unknown_version_keeps_backup_and_does_not_overwrite() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(PREFERENCES_FILE);
        let original = r#"{"schemaVersion":9,"revision":"4","enabled":true,"diameterDip":160}"#;
        fs::write(&path, original).unwrap();
        let store = PreferencesStore::load(dir.path());
        assert_eq!(store.snapshot(), OrbPreferences::default());
        assert!(store.snapshot().enabled);
        assert_eq!(fs::read_to_string(&path).unwrap(), original);
        assert!(dir.path().join("orb-preferences.json.bak").exists());
    }

    #[test]
    fn unknown_fields_are_ignored_and_diameters_are_validated() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(PREFERENCES_FILE);
        fs::write(
            &path,
            r#"{"schemaVersion":1,"revision":"3","enabled":true,"diameterDip":128,"extraField":true}"#,
        )
        .unwrap();
        let store = PreferencesStore::load(dir.path());
        let loaded = store.snapshot();
        assert!(loaded.enabled);
        assert_eq!(loaded.diameter_dip, 128);
        assert_eq!(loaded.revision, "3");
        let err = store
            .patch(PreferencesPatch {
                expected_revision: "3".into(),
                diameter_dip: Some(96),
                ..PreferencesPatch::default()
            })
            .unwrap_err();
        assert!(matches!(err, PreferencesError::InvalidDiameter(96)));
    }
}
