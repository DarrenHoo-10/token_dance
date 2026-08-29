use std::fs::Metadata;
use std::io::Read;
use std::path::Path;
use std::time::UNIX_EPOCH;

use sha2::{Digest, Sha256};

pub fn file_identity(path: &Path, meta: &Metadata) -> String {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        return format!("unix:{}:{}", meta.dev(), meta.ino());
    }
    fallback_identity(path, meta)
}

fn fallback_identity(path: &Path, meta: &Metadata) -> String {
    let created = meta
        .created()
        .ok()
        .and_then(|time| time.duration_since(UNIX_EPOCH).ok())
        .map(|dur| dur.as_secs())
        .unwrap_or(0);
    let prefix = std::fs::File::open(path)
        .ok()
        .and_then(|mut file| {
            let mut buf = [0u8; 64];
            let n = file.read(&mut buf).ok()?;
            let digest = Sha256::digest(&buf[..n]);
            Some(format!("{digest:x}"))
        })
        .unwrap_or_else(|| "empty".into());
    format!("ctime:{created}:prefix:{prefix}")
}

pub fn record_hash(line: &[u8]) -> String {
    format!("sha256:{:x}", Sha256::digest(line))
}

pub fn created_stamp(identity: &str) -> &str {
    identity.split(":prefix:").next().unwrap_or(identity)
}

pub fn prefix_stamp(identity: &str) -> &str {
    identity.split(":prefix:").nth(1).unwrap_or(identity)
}
