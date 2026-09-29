//! Whether a collector currently owns the data directory, without taking the lock.

use std::fs::OpenOptions;
use std::io::Read;

use collector_service::AppPaths;
use serde::Serialize;

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct CollectorProcess {
    /// `running`, `stopped`, `never_started` or `unknown`.
    pub state: &'static str,
    pub pid: Option<u32>,
}

/// Probe with a shared try-lock on a read-only handle: no write, no lock file created.
pub fn probe(paths: &AppPaths) -> CollectorProcess {
    let path = paths.lock_path();
    let mut file = match OpenOptions::new().read(true).open(&path) {
        Ok(f) => f,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            return CollectorProcess {
                state: "never_started",
                pid: None,
            }
        }
        Err(_) => {
            return CollectorProcess {
                state: "unknown",
                pid: None,
            }
        }
    };
    match file.try_lock_shared() {
        Ok(()) => {
            let _ = file.unlock();
            CollectorProcess {
                state: "stopped",
                pid: None,
            }
        }
        Err(std::fs::TryLockError::WouldBlock) => {
            let mut text = String::new();
            let _ = file.read_to_string(&mut text);
            CollectorProcess {
                state: "running",
                pid: text.trim().parse().ok(),
            }
        }
        Err(_) => CollectorProcess {
            state: "unknown",
            pid: None,
        },
    }
}
