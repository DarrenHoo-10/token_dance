//! Fail-closed Adapter Pack verification and transactional installation.

#![forbid(unsafe_code)]

use std::collections::BTreeSet;
use std::fs;
use std::io::{self, Read, Write};
use std::path::{Component, Path, PathBuf};

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use semver::Version;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use thiserror::Error;

pub const MANIFEST_FILE: &str = "adapter-pack.json";
pub const SIGNATURE_FILE: &str = "adapter-pack.ed25519";
pub const FORMAT_VERSION: u32 = 1;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AdapterPackManifest {
    pub format_version: u32,
    pub pack_version: Version,
    pub schema_version: Version,
    pub files: Vec<PackFile>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct PackFile {
    pub path: String,
    pub sha256: String,
    pub size: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Compatibility {
    pub schema_major: u64,
    pub maximum_schema_minor: u64,
    pub installed_pack_version: Option<Version>,
}

#[derive(Debug, Error)]
pub enum PackError {
    #[error("I/O failed for {path}: {source}")]
    Io {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("manifest JSON is invalid: {0}")]
    Manifest(#[from] serde_json::Error),
    #[error("unsupported pack format version {0}")]
    Format(u32),
    #[error("pack manifest contains no files")]
    EmptyFiles,
    #[error("pack changed while it was being staged")]
    StagedPackChanged,
    #[error("manifest signature encoding is invalid")]
    SignatureEncoding,
    #[error("manifest Ed25519 signature is invalid")]
    SignatureInvalid,
    #[error("pack schema {pack} is incompatible with supported {major}.0 through {major}.{minor}")]
    SchemaIncompatible {
        pack: Version,
        major: u64,
        minor: u64,
    },
    #[error("pack version {candidate} is not newer than installed version {installed}")]
    NonMonotonic {
        candidate: Version,
        installed: Version,
    },
    #[error("unsafe or duplicate pack path: {0}")]
    UnsafePath(String),
    #[error("pack file is a symbolic link: {0}")]
    Symlink(String),
    #[error("pack file is missing: {0}")]
    Missing(String),
    #[error("pack file size mismatch for {path}: expected {expected}, got {actual}")]
    SizeMismatch {
        path: String,
        expected: u64,
        actual: u64,
    },
    #[error("pack file digest mismatch: {0}")]
    DigestMismatch(String),
    #[error("pack contains unlisted file: {0}")]
    UnlistedFile(String),
    #[error("activation check failed and previous pack was restored: {0}")]
    Activation(String),
    #[error("rollback failed after activation error `{activation}`: {rollback}")]
    Rollback {
        activation: String,
        rollback: String,
    },
}

pub fn verify_pack(
    source: &Path,
    public_key: &[u8; 32],
    compatibility: &Compatibility,
) -> Result<AdapterPackManifest, PackError> {
    let manifest_path = source.join(MANIFEST_FILE);
    let manifest_bytes = read(&manifest_path)?;
    let signature_text =
        fs::read_to_string(source.join(SIGNATURE_FILE)).map_err(|source_error| PackError::Io {
            path: source.join(SIGNATURE_FILE),
            source: source_error,
        })?;
    verify_signature(&manifest_bytes, signature_text.trim(), public_key)?;

    let manifest: AdapterPackManifest = serde_json::from_slice(&manifest_bytes)?;
    if manifest.format_version != FORMAT_VERSION {
        return Err(PackError::Format(manifest.format_version));
    }
    if manifest.files.is_empty() {
        return Err(PackError::EmptyFiles);
    }
    if manifest.schema_version.major != compatibility.schema_major
        || manifest.schema_version.minor > compatibility.maximum_schema_minor
    {
        return Err(PackError::SchemaIncompatible {
            pack: manifest.schema_version.clone(),
            major: compatibility.schema_major,
            minor: compatibility.maximum_schema_minor,
        });
    }
    if let Some(installed) = &compatibility.installed_pack_version {
        if manifest.pack_version <= *installed {
            return Err(PackError::NonMonotonic {
                candidate: manifest.pack_version.clone(),
                installed: installed.clone(),
            });
        }
    }

    verify_files(source, &manifest.files)?;
    Ok(manifest)
}

pub fn install_verified_pack<F>(
    source: &Path,
    install_root: &Path,
    public_key: &[u8; 32],
    compatibility: &Compatibility,
    activation_check: F,
) -> Result<AdapterPackManifest, PackError>
where
    F: FnOnce(&Path, &AdapterPackManifest) -> Result<(), String>,
{
    let manifest = verify_pack(source, public_key, compatibility)?;
    fs::create_dir_all(install_root).map_err(|source| PackError::Io {
        path: install_root.to_path_buf(),
        source,
    })?;
    let current = install_root.join("current");
    let staged = install_root.join(format!(".staged-{}", manifest.pack_version));
    let rollback = install_root.join(".rollback");
    remove_if_exists(&staged)?;
    remove_if_exists(&rollback)?;
    copy_pack(source, &staged, &manifest)?;
    if let Err(error) = verify_staged_pack(&staged, &manifest, public_key, compatibility) {
        let _ = remove_if_exists(&staged);
        return Err(error);
    }

    if current.exists() {
        rename(&current, &rollback)?;
    }
    if let Err(error) = rename(&staged, &current) {
        if rollback.exists() {
            let _ = rename(&rollback, &current);
        }
        return Err(error);
    }

    if let Err(activation) = activation_check(&current, &manifest) {
        let rollback_result = (|| {
            remove_if_exists(&current)?;
            if rollback.exists() {
                rename(&rollback, &current)?;
            }
            Ok::<(), PackError>(())
        })();
        return match rollback_result {
            Ok(()) => Err(PackError::Activation(activation)),
            Err(error) => Err(PackError::Rollback {
                activation,
                rollback: error.to_string(),
            }),
        };
    }

    remove_if_exists(&rollback)?;
    Ok(manifest)
}

fn verify_staged_pack(
    staged: &Path,
    expected: &AdapterPackManifest,
    public_key: &[u8; 32],
    compatibility: &Compatibility,
) -> Result<(), PackError> {
    let actual = verify_pack(staged, public_key, compatibility)?;
    if actual != *expected {
        return Err(PackError::StagedPackChanged);
    }
    Ok(())
}

fn verify_signature(
    manifest: &[u8],
    encoded: &str,
    public_key: &[u8; 32],
) -> Result<(), PackError> {
    let bytes = STANDARD
        .decode(encoded)
        .map_err(|_| PackError::SignatureEncoding)?;
    let signature = Signature::from_slice(&bytes).map_err(|_| PackError::SignatureEncoding)?;
    let key = VerifyingKey::from_bytes(public_key).map_err(|_| PackError::SignatureEncoding)?;
    key.verify(manifest, &signature)
        .map_err(|_| PackError::SignatureInvalid)
}

fn verify_files(root: &Path, files: &[PackFile]) -> Result<(), PackError> {
    let mut listed = BTreeSet::new();
    let mut duplicate_keys = BTreeSet::new();
    for file in files {
        validate_relative_path(&file.path)?;
        let normalized = normalize(&file.path);
        if !duplicate_keys.insert(normalized.to_ascii_lowercase()) {
            return Err(PackError::UnsafePath(file.path.clone()));
        }
        listed.insert(normalized);
        let path = root.join(&file.path);
        let metadata = fs::symlink_metadata(&path).map_err(|error| {
            if error.kind() == io::ErrorKind::NotFound {
                PackError::Missing(file.path.clone())
            } else {
                PackError::Io {
                    path: path.clone(),
                    source: error,
                }
            }
        })?;
        if metadata.file_type().is_symlink() {
            return Err(PackError::Symlink(file.path.clone()));
        }
        if !metadata.is_file() {
            return Err(PackError::Missing(file.path.clone()));
        }
        if metadata.len() != file.size {
            return Err(PackError::SizeMismatch {
                path: file.path.clone(),
                expected: file.size,
                actual: metadata.len(),
            });
        }
        let actual = hex_digest(&path)?;
        if !constant_time_eq(
            actual.as_bytes(),
            file.sha256.to_ascii_lowercase().as_bytes(),
        ) {
            return Err(PackError::DigestMismatch(file.path.clone()));
        }
    }

    let mut actual = BTreeSet::new();
    collect_files(root, root, &mut actual)?;
    actual.remove(MANIFEST_FILE);
    actual.remove(SIGNATURE_FILE);
    if let Some(path) = actual.difference(&listed).next() {
        return Err(PackError::UnlistedFile(path.clone()));
    }
    Ok(())
}

fn copy_pack(
    source: &Path,
    destination: &Path,
    manifest: &AdapterPackManifest,
) -> Result<(), PackError> {
    fs::create_dir_all(destination).map_err(|source_error| PackError::Io {
        path: destination.to_path_buf(),
        source: source_error,
    })?;
    for name in [MANIFEST_FILE, SIGNATURE_FILE] {
        copy_file(&source.join(name), &destination.join(name))?;
    }
    for file in &manifest.files {
        let target = destination.join(&file.path);
        if let Some(parent) = target.parent() {
            fs::create_dir_all(parent).map_err(|source_error| PackError::Io {
                path: parent.to_path_buf(),
                source: source_error,
            })?;
        }
        copy_file(&source.join(&file.path), &target)?;
    }
    Ok(())
}

fn collect_files(
    root: &Path,
    directory: &Path,
    output: &mut BTreeSet<String>,
) -> Result<(), PackError> {
    let entries = fs::read_dir(directory).map_err(|source| PackError::Io {
        path: directory.to_path_buf(),
        source,
    })?;
    for entry in entries {
        let entry = entry.map_err(|source| PackError::Io {
            path: directory.to_path_buf(),
            source,
        })?;
        let metadata = entry.file_type().map_err(|source| PackError::Io {
            path: entry.path(),
            source,
        })?;
        if metadata.is_symlink() {
            let relative = entry
                .path()
                .strip_prefix(root)
                .unwrap()
                .to_string_lossy()
                .into_owned();
            return Err(PackError::Symlink(relative));
        }
        if metadata.is_dir() {
            collect_files(root, &entry.path(), output)?;
        } else if metadata.is_file() {
            output.insert(normalize(
                &entry.path().strip_prefix(root).unwrap().to_string_lossy(),
            ));
        }
    }
    Ok(())
}

fn validate_relative_path(value: &str) -> Result<(), PackError> {
    let path = Path::new(value);
    if value.is_empty()
        || value.contains('\\')
        || value == MANIFEST_FILE
        || value == SIGNATURE_FILE
        || path.is_absolute()
        || path
            .components()
            .any(|component| !matches!(component, Component::Normal(_)))
    {
        Err(PackError::UnsafePath(value.into()))
    } else {
        Ok(())
    }
}

fn normalize(value: &str) -> String {
    value.replace('\\', "/")
}

fn hex_digest(path: &Path) -> Result<String, PackError> {
    let mut file = fs::File::open(path).map_err(|source| PackError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    let mut digest = Sha256::new();
    let mut buffer = [0u8; 64 * 1024];
    loop {
        let read = file.read(&mut buffer).map_err(|source| PackError::Io {
            path: path.to_path_buf(),
            source,
        })?;
        if read == 0 {
            break;
        }
        digest.update(&buffer[..read]);
    }
    Ok(format!("{:x}", digest.finalize()))
}

fn constant_time_eq(left: &[u8], right: &[u8]) -> bool {
    if left.len() != right.len() {
        return false;
    }
    left.iter()
        .zip(right)
        .fold(0u8, |diff, (a, b)| diff | (a ^ b))
        == 0
}

fn copy_file(source: &Path, destination: &Path) -> Result<(), PackError> {
    let mut input = fs::File::open(source).map_err(|error| PackError::Io {
        path: source.to_path_buf(),
        source: error,
    })?;
    let mut output = fs::File::create(destination).map_err(|error| PackError::Io {
        path: destination.to_path_buf(),
        source: error,
    })?;
    io::copy(&mut input, &mut output).map_err(|error| PackError::Io {
        path: destination.to_path_buf(),
        source: error,
    })?;
    output.flush().map_err(|error| PackError::Io {
        path: destination.to_path_buf(),
        source: error,
    })?;
    output.sync_all().map_err(|error| PackError::Io {
        path: destination.to_path_buf(),
        source: error,
    })?;
    Ok(())
}

fn read(path: &Path) -> Result<Vec<u8>, PackError> {
    fs::read(path).map_err(|source| PackError::Io {
        path: path.to_path_buf(),
        source,
    })
}

fn rename(source: &Path, destination: &Path) -> Result<(), PackError> {
    fs::rename(source, destination).map_err(|error| PackError::Io {
        path: destination.to_path_buf(),
        source: error,
    })
}

fn remove_if_exists(path: &Path) -> Result<(), PackError> {
    match fs::remove_dir_all(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(source) => Err(PackError::Io {
            path: path.to_path_buf(),
            source,
        }),
    }
}

#[cfg(test)]
mod tests;
