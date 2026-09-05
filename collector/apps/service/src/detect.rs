use std::cmp::Reverse;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use crate::{
    adapter_id, AgentDetection, DetectedSourceConfig, DetectionSnapshot, OfficialAgent,
};

const MAX_JSONL_FILES: usize = 8;

pub fn detect_local() -> DetectionSnapshot {
    detect_from_home(&user_home())
}

pub fn detect_from_home(home: &Path) -> DetectionSnapshot {
    let mut snapshot = DetectionSnapshot::default();
    detect_claude(home, &mut snapshot);
    detect_codex(home, &mut snapshot);
    detect_grok(home, &mut snapshot);
    detect_zcode(home, &mut snapshot);
    detect_cursor(home, &mut snapshot);
    detect_deepseek(home, &mut snapshot);
    snapshot
}

pub fn list_jsonl_files(root: &Path, limit: usize) -> Vec<PathBuf> {
    if root.is_file() {
        return if is_jsonl(root) {
            vec![root.to_path_buf()]
        } else {
            Vec::new()
        };
    }
    if !root.is_dir() {
        return Vec::new();
    }
    let mut files = Vec::new();
    walk_jsonl(root, &mut files);
    files.sort_by_key(|(mtime, _)| Reverse(*mtime));
    files
        .into_iter()
        .take(limit.max(1))
        .map(|(_, path)| path)
        .collect()
}

pub fn detected_adapter_ids(snapshot: &DetectionSnapshot) -> Vec<&'static str> {
    OfficialAgent::ALL
        .into_iter()
        .filter(|agent| snapshot.is_installed(*agent))
        .map(adapter_id)
        .collect()
}

fn user_home() -> PathBuf {
    std::env::var_os("USERPROFILE")
        .or_else(|| std::env::var_os("HOME"))
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("."))
}

fn detect_claude(home: &Path, snapshot: &mut DetectionSnapshot) {
    let root = home.join(".claude");
    let projects = root.join("projects");
    if !root.is_dir() {
        return;
    }
    let mut detection = AgentDetection::installed(read_json_version(&root.join("settings.json")).unwrap_or_else(|| "1.0.0".into()));
    detection.otlp_available = false;
    snapshot.insert(OfficialAgent::ClaudeCode, detection);
    let jsonl_root = if projects.is_dir() {
        projects
    } else if root.join("history.jsonl").is_file() {
        root.join("history.jsonl")
    } else {
        return;
    };
    snapshot.configure_source(
        OfficialAgent::ClaudeCode,
        adapter_claude::HISTORY_SOURCE_ID,
        DetectedSourceConfig {
            path: Some(jsonl_root),
            ..DetectedSourceConfig::default()
        },
    );
}

fn detect_codex(home: &Path, snapshot: &mut DetectionSnapshot) {
    let root = home.join(".codex");
    let sessions = root.join("sessions");
    if !root.is_dir() {
        return;
    }
    let mut detection = AgentDetection::installed(
        read_json_version(&root.join("version.json")).unwrap_or_else(|| "0.150.0".into()),
    );
    detection.mode = Some("interactive".into());
    detection.otlp_available = false;
    snapshot.insert(OfficialAgent::Codex, detection);
    if sessions.is_dir() {
        snapshot.configure_source(
            OfficialAgent::Codex,
            adapter_codex::SOURCE_JSONL,
            DetectedSourceConfig {
                path: Some(sessions),
                ..DetectedSourceConfig::default()
            },
        );
    }
    let archived = root.join("archived_sessions");
    if archived.is_dir() {
        snapshot.configure_source(
            OfficialAgent::Codex,
            "codex-archived-sessions",
            DetectedSourceConfig {
                path: Some(archived),
                ..DetectedSourceConfig::default()
            },
        );
    }
}

fn detect_grok(home: &Path, snapshot: &mut DetectionSnapshot) {
    let root = home.join(".grok");
    if !root.is_dir() {
        return;
    }
    snapshot.insert(
        OfficialAgent::GrokBuild,
        AgentDetection::installed(
            read_json_version(&root.join("version.json")).unwrap_or_else(|| "1.0.0".into()),
        ),
    );
    let unified = root.join("logs").join("unified.jsonl");
    let sessions = root.join("sessions");
    let path = if unified.is_file() {
        unified
    } else if sessions.is_dir() {
        sessions
    } else {
        return;
    };
    snapshot.configure_source(
        OfficialAgent::GrokBuild,
        adapter_grok_build::HISTORY_SOURCE_ID,
        DetectedSourceConfig {
            path: Some(path),
            ..DetectedSourceConfig::default()
        },
    );
}

fn detect_zcode(home: &Path, snapshot: &mut DetectionSnapshot) {
    let root = home.join(".zcode");
    if !root.is_dir() {
        return;
    }
    snapshot.insert(
        OfficialAgent::Zcode,
        AgentDetection::installed("1.0.0"),
    );
}

fn detect_cursor(home: &Path, snapshot: &mut DetectionSnapshot) {
    let roaming = std::env::var_os("APPDATA")
        .map(PathBuf::from)
        .map(|path| path.join("Cursor"));
    let local = home.join(".cursor");
    if roaming.as_ref().is_some_and(|path| path.is_dir()) || local.is_dir() {
        let mut detection = AgentDetection::installed("0");
        detection.cursor_mode = Some(crate::DetectedCursorMode::PersonalLocal);
        snapshot.insert(OfficialAgent::Cursor, detection);
    }
}

fn detect_deepseek(home: &Path, snapshot: &mut DetectionSnapshot) {
    let root = home.join(".deepseek-harness");
    if !root.is_dir() {
        return;
    }
    snapshot.insert(
        OfficialAgent::DeepseekHarness,
        AgentDetection::installed("1.0.0"),
    );
    let sessions = root.join("sessions");
    if sessions.is_dir() {
        snapshot.configure_source(
            OfficialAgent::DeepseekHarness,
            adapter_deepseek_harness::HISTORY_SOURCE_ID,
            DetectedSourceConfig {
                path: Some(sessions),
                ..DetectedSourceConfig::default()
            },
        );
    }
}

fn read_json_version(path: &Path) -> Option<String> {
    let text = fs::read_to_string(path).ok()?;
    let value: serde_json::Value = serde_json::from_str(&text).ok()?;
    value
        .get("version")
        .or_else(|| value.get("cli_version"))
        .and_then(serde_json::Value::as_str)
        .map(|version| version.trim().trim_start_matches('v').to_string())
}

fn walk_jsonl(dir: &Path, out: &mut Vec<(SystemTime, PathBuf)>) {
    let Ok(entries) = fs::read_dir(dir) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if name.starts_with('.') || matches!(name.as_ref(), "node_modules" | "target" | "bin" | "vendor" | "cache") {
            continue;
        }
        if path.is_dir() {
            walk_jsonl(&path, out);
        } else if is_jsonl(&path) {
            let mtime = entry
                .metadata()
                .and_then(|meta| meta.modified())
                .unwrap_or(SystemTime::UNIX_EPOCH);
            out.push((mtime, path));
        }
    }
}

fn is_jsonl(path: &Path) -> bool {
    path.extension().and_then(|ext| ext.to_str()) == Some("jsonl")
}

pub fn default_jsonl_limit() -> usize {
    MAX_JSONL_FILES
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lists_newest_jsonl_files_from_a_tree() {
        let root = tempfile::tempdir().unwrap();
        let nested = root.path().join("a").join("b");
        fs::create_dir_all(&nested).unwrap();
        fs::write(nested.join("old.jsonl"), "{}\n").unwrap();
        std::thread::sleep(std::time::Duration::from_millis(20));
        fs::write(nested.join("new.jsonl"), "{\"type\":\"x\"}\n").unwrap();
        fs::write(nested.join("skip.json"), "{}\n").unwrap();
        let files = list_jsonl_files(root.path(), 8);
        assert_eq!(files.len(), 2);
        assert!(files[0].ends_with("new.jsonl"));
    }
}
