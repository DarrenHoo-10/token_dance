use std::fs;

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use semver::Version;
use sha2::{Digest, Sha256};
use tempfile::TempDir;

use super::*;

fn make_pack(version: &str, schema: &str) -> (TempDir, SigningKey) {
    let directory = tempfile::tempdir().unwrap();
    let signing_key = SigningKey::from_bytes(&[0x42; 32]);
    fs::create_dir_all(directory.path().join("adapters")).unwrap();
    let payload = b"adapter-binary-v2";
    fs::write(directory.path().join("adapters/example.bin"), payload).unwrap();
    let manifest = AdapterPackManifest {
        format_version: FORMAT_VERSION,
        pack_version: Version::parse(version).unwrap(),
        schema_version: Version::parse(schema).unwrap(),
        files: vec![PackFile {
            path: "adapters/example.bin".into(),
            sha256: format!("{:x}", Sha256::digest(payload)),
            size: payload.len() as u64,
        }],
    };
    let bytes = serde_json::to_vec_pretty(&manifest).unwrap();
    fs::write(directory.path().join(MANIFEST_FILE), &bytes).unwrap();
    fs::write(
        directory.path().join(SIGNATURE_FILE),
        STANDARD.encode(signing_key.sign(&bytes).to_bytes()),
    )
    .unwrap();
    (directory, signing_key)
}

fn compatibility(installed: Option<&str>) -> Compatibility {
    Compatibility {
        schema_major: 1,
        maximum_schema_minor: 2,
        installed_pack_version: installed.map(|value| Version::parse(value).unwrap()),
    }
}

#[test]
fn valid_signed_pack_is_accepted() {
    let (pack, key) = make_pack("2.0.0", "1.1.0");
    let manifest = verify_pack(
        pack.path(),
        &key.verifying_key().to_bytes(),
        &compatibility(Some("1.9.0")),
    )
    .unwrap();
    assert_eq!(manifest.pack_version, Version::parse("2.0.0").unwrap());
}

#[test]
fn empty_pack_is_rejected() {
    let (pack, key) = make_pack("2.0.0", "1.1.0");
    let manifest_path = pack.path().join(MANIFEST_FILE);
    let mut manifest: AdapterPackManifest =
        serde_json::from_slice(&fs::read(&manifest_path).unwrap()).unwrap();
    manifest.files.clear();
    let bytes = serde_json::to_vec_pretty(&manifest).unwrap();
    fs::write(&manifest_path, &bytes).unwrap();
    fs::write(
        pack.path().join(SIGNATURE_FILE),
        STANDARD.encode(key.sign(&bytes).to_bytes()),
    )
    .unwrap();

    assert!(matches!(
        verify_pack(
            pack.path(),
            &key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::EmptyFiles)
    ));
}

#[test]
fn staged_pack_must_match_the_verified_manifest() {
    let (source, key) = make_pack("2.0.0", "1.1.0");
    let expected = verify_pack(
        source.path(),
        &key.verifying_key().to_bytes(),
        &compatibility(None),
    )
    .unwrap();
    let (replacement, _) = make_pack("2.1.0", "1.1.0");
    fs::copy(
        replacement.path().join(MANIFEST_FILE),
        source.path().join(MANIFEST_FILE),
    )
    .unwrap();
    fs::copy(
        replacement.path().join(SIGNATURE_FILE),
        source.path().join(SIGNATURE_FILE),
    )
    .unwrap();

    let staged = tempfile::tempdir().unwrap();
    copy_pack(source.path(), staged.path(), &expected).unwrap();
    assert!(matches!(
        verify_staged_pack(
            staged.path(),
            &expected,
            &key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::StagedPackChanged)
    ));
}

#[test]
fn tampering_does_not_pass_signature_or_digest_checks() {
    let (pack, key) = make_pack("2.0.0", "1.1.0");
    fs::write(
        pack.path().join("adapters/example.bin"),
        b"tampered-payload!",
    )
    .unwrap();
    assert!(matches!(
        verify_pack(
            pack.path(),
            &key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::DigestMismatch(_)) | Err(PackError::SizeMismatch { .. })
    ));

    let manifest_path = pack.path().join(MANIFEST_FILE);
    let mut manifest = fs::read(&manifest_path).unwrap();
    manifest.push(b' ');
    fs::write(manifest_path, manifest).unwrap();
    assert!(matches!(
        verify_pack(
            pack.path(),
            &key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::SignatureInvalid)
    ));
}

#[test]
fn downgrade_and_future_schema_are_rejected() {
    let (old_pack, key) = make_pack("1.5.0", "1.1.0");
    assert!(matches!(
        verify_pack(
            old_pack.path(),
            &key.verifying_key().to_bytes(),
            &compatibility(Some("1.5.0"))
        ),
        Err(PackError::NonMonotonic { .. })
    ));

    let (future_pack, future_key) = make_pack("2.0.0", "1.3.0");
    assert!(matches!(
        verify_pack(
            future_pack.path(),
            &future_key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::SchemaIncompatible { .. })
    ));
}

#[test]
fn failed_activation_restores_previous_pack() {
    let (pack, key) = make_pack("2.0.0", "1.0.0");
    let install = tempfile::tempdir().unwrap();
    fs::create_dir_all(install.path().join("current")).unwrap();
    fs::write(install.path().join("current/previous.txt"), b"previous").unwrap();

    let error = install_verified_pack(
        pack.path(),
        install.path(),
        &key.verifying_key().to_bytes(),
        &compatibility(Some("1.0.0")),
        |_, _| Err("adapter load failed".into()),
    )
    .unwrap_err();

    assert!(matches!(error, PackError::Activation(_)));
    assert_eq!(
        fs::read(install.path().join("current/previous.txt")).unwrap(),
        b"previous"
    );
    assert!(!install.path().join("current/adapters/example.bin").exists());
}

#[test]
fn traversal_paths_are_rejected() {
    let (pack, key) = make_pack("2.0.0", "1.0.0");
    let manifest_path = pack.path().join(MANIFEST_FILE);
    let mut manifest: AdapterPackManifest =
        serde_json::from_slice(&fs::read(&manifest_path).unwrap()).unwrap();
    manifest.files[0].path = "../escape.bin".into();
    let bytes = serde_json::to_vec_pretty(&manifest).unwrap();
    fs::write(&manifest_path, &bytes).unwrap();
    fs::write(
        pack.path().join(SIGNATURE_FILE),
        STANDARD.encode(key.sign(&bytes).to_bytes()),
    )
    .unwrap();

    assert!(matches!(
        verify_pack(
            pack.path(),
            &key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::UnsafePath(_))
    ));
}

#[test]
fn case_insensitive_duplicates_are_rejected() {
    let (pack, key) = make_pack("2.0.0", "1.0.0");
    let manifest_path = pack.path().join(MANIFEST_FILE);
    let mut manifest: AdapterPackManifest =
        serde_json::from_slice(&fs::read(&manifest_path).unwrap()).unwrap();
    let mut duplicate = manifest.files[0].clone();
    duplicate.path = "ADAPTERS/EXAMPLE.BIN".into();
    manifest.files.push(duplicate);
    let bytes = serde_json::to_vec_pretty(&manifest).unwrap();
    fs::write(&manifest_path, &bytes).unwrap();
    fs::write(
        pack.path().join(SIGNATURE_FILE),
        STANDARD.encode(key.sign(&bytes).to_bytes()),
    )
    .unwrap();

    assert!(matches!(
        verify_pack(
            pack.path(),
            &key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::UnsafePath(_))
    ));
}

#[test]
fn unlisted_files_are_rejected() {
    let (pack, key) = make_pack("2.0.0", "1.0.0");
    fs::write(pack.path().join("extra.txt"), b"not listed").unwrap();
    assert!(matches!(
        verify_pack(
            pack.path(),
            &key.verifying_key().to_bytes(),
            &compatibility(None)
        ),
        Err(PackError::UnlistedFile(_))
    ));
}
