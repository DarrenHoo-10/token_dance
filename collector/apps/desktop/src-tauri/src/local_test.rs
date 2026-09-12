//! Explicit debug-only local testing without production storage or Keychain.

use std::fs::{self, OpenOptions};
use std::io::{Read, Write};
use std::path::Path;

const KEY_FILE: &str = "local-test.key";

pub fn enabled() -> bool {
    collector_service::platform::local_test_enabled()
}

/// This separate local-test key is never a fallback for production credentials.
pub fn data_key(dir: &Path) -> Result<[u8; 32], String> {
    if !enabled() {
        return Err("local-test credentials require a debug build and --local-test".into());
    }
    load_or_create_data_key(dir)
}

fn read_key(path: &Path) -> Result<[u8; 32], String> {
    let metadata = fs::symlink_metadata(path).map_err(|error| error.to_string())?;
    if !metadata.is_file() || metadata.len() != 32 {
        return Err("local-test key is invalid; existing test data was preserved".into());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if metadata.permissions().mode() & 0o077 != 0 {
            return Err("local-test key must have private file permissions".into());
        }
    }
    let mut key = [0u8; 32];
    fs::File::open(path)
        .and_then(|mut file| file.read_exact(&mut key))
        .map_err(|error| error.to_string())?;
    Ok(key)
}

fn load_or_create_data_key(dir: &Path) -> Result<[u8; 32], String> {
    let path = dir.join(KEY_FILE);
    match fs::symlink_metadata(&path) {
        Ok(_) => return read_key(&path),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) => return Err(error.to_string()),
    }
    if dir
        .join("tokendance.sqlite3")
        .try_exists()
        .map_err(|error| error.to_string())?
    {
        return Err("local-test key is missing for existing test data".into());
    }
    collector_service::platform::create_private_dir(dir)?;
    let mut key = [0u8; 32];
    #[cfg(unix)]
    fs::File::open("/dev/urandom")
        .and_then(|mut random| random.read_exact(&mut key))
        .map_err(|error| error.to_string())?;
    #[cfg(not(unix))]
    {
        key[..16].copy_from_slice(uuid::Uuid::new_v4().as_bytes());
        key[16..].copy_from_slice(uuid::Uuid::new_v4().as_bytes());
    }
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    match options.open(&path) {
        Ok(mut file) => {
            file.write_all(&key)
                .and_then(|_| file.sync_all())
                .map_err(|error| error.to_string())?;
            Ok(key)
        }
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => read_key(&path),
        Err(error) => Err(error.to_string()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn key_is_stable_private_and_unique_per_test_directory() {
        let first = tempfile::tempdir().unwrap();
        let second = tempfile::tempdir().unwrap();
        let key = load_or_create_data_key(first.path()).unwrap();
        assert_eq!(key, load_or_create_data_key(first.path()).unwrap());
        assert_ne!(key, load_or_create_data_key(second.path()).unwrap());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(first.path().join(KEY_FILE))
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o600
            );
        }
    }

    #[test]
    fn missing_or_malformed_key_never_replaces_existing_test_identity() {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("tokendance.sqlite3"), b"existing-data").unwrap();
        assert!(load_or_create_data_key(dir.path()).is_err());
        assert!(!dir.path().join(KEY_FILE).exists());
        fs::write(dir.path().join(KEY_FILE), b"malformed").unwrap();
        assert!(load_or_create_data_key(dir.path()).is_err());
        assert_eq!(fs::read(dir.path().join(KEY_FILE)).unwrap(), b"malformed");
        assert_eq!(
            fs::read(dir.path().join("tokendance.sqlite3")).unwrap(),
            b"existing-data"
        );
    }

    #[test]
    fn credentials_are_unavailable_without_the_explicit_local_test_flag() {
        if !enabled() {
            let dir = tempfile::tempdir().unwrap();
            assert!(data_key(dir.path()).is_err());
            assert!(!dir.path().join(KEY_FILE).exists());
        }
    }
}
