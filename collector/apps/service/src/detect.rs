use std::cmp::Reverse;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use crate::{adapter_id, AgentDetection, DetectedSourceConfig, DetectionSnapshot, OfficialAgent};

const MAX_JSONL_FILES: usize = 8;
const GROK_HISTORY_FILE_LIMIT: usize = 512;
const GROK_UPDATES_FILE_NAME: &str = "updates.jsonl";

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
    detect_pi(home, &mut snapshot);
    snapshot
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EnumeratedSourceKind {
    Jsonl,
    GrokUpdates,
    Sqlite,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiscoveredFile {
    pub path: PathBuf,
    pub mtime: SystemTime,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EnumeratedSourceFile {
    pub adapter_id: &'static str,
    pub source_id: String,
    pub scan_root: PathBuf,
    pub path: PathBuf,
    pub mtime_unix: u64,
    pub kind: EnumeratedSourceKind,
}

/// Complete JSONL listing. Callers that still want a hot window pass a limit;
/// rebuild uses [`discover_jsonl_files`] with no cap.
pub fn list_jsonl_files(root: &Path, limit: usize) -> Vec<PathBuf> {
    discover_jsonl_files(root)
        .into_iter()
        .take(limit.max(1))
        .map(|file| file.path)
        .collect()
}

pub fn discover_jsonl_files(root: &Path) -> Vec<DiscoveredFile> {
    if root.is_file() {
        return if is_jsonl(root) {
            vec![DiscoveredFile {
                path: root.to_path_buf(),
                mtime: mtime_of(root),
            }]
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
        .map(|(mtime, path)| DiscoveredFile { path, mtime })
        .collect()
}

/// Enumerate every authorized JSONL, archive, and agent-DB source without a
/// "last N files" cap. Recent files are sorted first so rebuild can prefer them.
pub fn enumerate_all_source_files(snapshot: &DetectionSnapshot) -> Vec<EnumeratedSourceFile> {
    let mut files = Vec::new();
    for (agent, source_id, config) in snapshot.iter_sources() {
        let Some(path) = config.path.as_ref() else {
            continue;
        };
        let adapter = adapter_id(agent);
        if is_sqlite_path(path) {
            files.push(EnumeratedSourceFile {
                adapter_id: adapter,
                source_id: source_id.to_string(),
                scan_root: path.clone(),
                path: path.clone(),
                mtime_unix: unix_secs(mtime_of(path)),
                kind: EnumeratedSourceKind::Sqlite,
            });
            continue;
        }
        let grok_history = source_id == adapter_grok_build::HISTORY_SOURCE_ID;
        let discovered = if grok_history {
            discover_grok_history_files(path)
        } else {
            discover_jsonl_files(path)
        };
        let kind = if grok_history {
            EnumeratedSourceKind::GrokUpdates
        } else {
            EnumeratedSourceKind::Jsonl
        };
        for file in discovered {
            files.push(EnumeratedSourceFile {
                adapter_id: adapter,
                source_id: source_id.to_string(),
                scan_root: path.clone(),
                path: file.path,
                mtime_unix: unix_secs(file.mtime),
                kind,
            });
        }
    }
    files.sort_by(|left, right| {
        right
            .mtime_unix
            .cmp(&left.mtime_unix)
            .then_with(|| left.path.cmp(&right.path))
    });
    files
}

fn is_sqlite_path(path: &Path) -> bool {
    path.extension()
        .and_then(|ext| ext.to_str())
        .is_some_and(|ext| ext.eq_ignore_ascii_case("sqlite") || ext.eq_ignore_ascii_case("vscdb"))
}

fn mtime_of(path: &Path) -> SystemTime {
    fs::metadata(path)
        .and_then(|meta| meta.modified())
        .unwrap_or(SystemTime::UNIX_EPOCH)
}

fn unix_secs(time: SystemTime) -> u64 {
    time.duration_since(SystemTime::UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or(0)
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
    let mut detection = AgentDetection::installed(
        read_json_version(&root.join("settings.json")).unwrap_or_else(|| "1.0.0".into()),
    );
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
    let sessions = root.join("sessions");
    if !sessions.is_dir() {
        return;
    }
    snapshot.configure_source(
        OfficialAgent::GrokBuild,
        adapter_grok_build::HISTORY_SOURCE_ID,
        DetectedSourceConfig {
            path: Some(sessions),
            ..DetectedSourceConfig::default()
        },
    );
}

fn detect_zcode(home: &Path, snapshot: &mut DetectionSnapshot) {
    let root = home.join(".zcode");
    if !root.is_dir() {
        return;
    }
    let db = root.join("cli/db/db.sqlite");
    // Collection is gated on the verified on-disk schema fingerprint, never on
    // the version string alone; unknown schemas stay unverified ("unverified").
    let verified = db
        .is_file()
        .then(|| acquisition::detect_zcode_sqlite(&db))
        .flatten();
    match verified {
        Some(schema) => {
            let mut item = AgentDetection::installed(schema.app_version.as_deref().unwrap_or("0"));
            item.sqlite_fingerprint = Some(schema.fingerprint.to_string());
            snapshot.insert(OfficialAgent::Zcode, item);
            snapshot.configure_source(
                OfficialAgent::Zcode,
                "zcode-sqlite",
                DetectedSourceConfig {
                    path: Some(db),
                    ..DetectedSourceConfig::default()
                },
            );
        }
        None => {
            snapshot.insert(OfficialAgent::Zcode, AgentDetection::installed("0"));
        }
    }
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

fn detect_pi(home: &Path, snapshot: &mut DetectionSnapshot) {
    let root = home.join(".pi");
    if !root.is_dir() {
        return;
    }
    snapshot.insert(
        OfficialAgent::Pi,
        AgentDetection::installed(
            read_json_version(&root.join("agent").join("version.json"))
                .unwrap_or_else(|| "0.3.0".into()),
        ),
    );
    let sessions = root.join("agent").join("sessions");
    if sessions.is_dir() {
        snapshot.configure_source(
            OfficialAgent::Pi,
            adapter_pi::HISTORY_SOURCE_ID,
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
        if name.starts_with('.')
            || matches!(
                name.as_ref(),
                "node_modules" | "target" | "bin" | "vendor" | "cache"
            )
        {
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

pub fn grok_history_limit() -> usize {
    GROK_HISTORY_FILE_LIMIT
}

pub fn list_grok_history_files(root: &Path, limit: usize) -> Vec<PathBuf> {
    discover_grok_history_files(root)
        .into_iter()
        .take(limit.max(1))
        .map(|file| file.path)
        .collect()
}

pub fn discover_grok_history_files(root: &Path) -> Vec<DiscoveredFile> {
    if root.is_file() {
        return if is_grok_updates_file(root) {
            vec![DiscoveredFile {
                path: root.to_path_buf(),
                mtime: mtime_of(root),
            }]
        } else {
            Vec::new()
        };
    }
    if !root.is_dir() {
        return Vec::new();
    }
    let mut files = Vec::new();
    walk_grok_updates(root, &mut files);
    files.sort_by_key(|(mtime, _)| Reverse(*mtime));
    files
        .into_iter()
        .map(|(mtime, path)| DiscoveredFile { path, mtime })
        .collect()
}

fn walk_grok_updates(dir: &Path, out: &mut Vec<(SystemTime, PathBuf)>) {
    let Ok(entries) = fs::read_dir(dir) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if name.starts_with('.')
            || matches!(
                name.as_ref(),
                "node_modules" | "target" | "bin" | "vendor" | "cache" | "subagents"
            )
        {
            continue;
        }
        if path.is_dir() {
            walk_grok_updates(&path, out);
        } else if is_grok_updates_file(&path) {
            let mtime = entry
                .metadata()
                .and_then(|meta| meta.modified())
                .unwrap_or(SystemTime::UNIX_EPOCH);
            out.push((mtime, path));
        }
    }
}

fn is_grok_updates_file(path: &Path) -> bool {
    path.file_name().and_then(|name| name.to_str()) == Some(GROK_UPDATES_FILE_NAME)
        && !grok_session_is_subagent(path.parent().unwrap_or(path))
}

fn grok_session_is_subagent(session_dir: &Path) -> bool {
    let summary = session_dir.join("summary.json");
    let Ok(text) = fs::read_to_string(summary) else {
        return false;
    };
    let Ok(value) = serde_json::from_str::<serde_json::Value>(&text) else {
        return false;
    };
    value
        .get("session_kind")
        .and_then(serde_json::Value::as_str)
        .is_some_and(|kind| kind.starts_with("subagent"))
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

    #[test]
    fn discover_jsonl_files_returns_more_than_the_hot_window() {
        let root = tempfile::tempdir().unwrap();
        for index in 0..12 {
            fs::write(root.path().join(format!("{index:02}.jsonl")), "{}\n").unwrap();
        }
        assert_eq!(list_jsonl_files(root.path(), 8).len(), 8);
        assert_eq!(discover_jsonl_files(root.path()).len(), 12);
    }

    #[test]
    fn enumerate_all_source_files_includes_archives_beyond_last_n() {
        let home = tempfile::tempdir().unwrap();
        let sessions = home.path().join(".codex").join("sessions");
        let archived = home.path().join(".codex").join("archived_sessions");
        fs::create_dir_all(&sessions).unwrap();
        fs::create_dir_all(&archived).unwrap();
        fs::write(
            home.path().join(".codex").join("version.json"),
            "{\"version\":\"0.1.0\"}\n",
        )
        .unwrap();
        for index in 0..20 {
            fs::write(sessions.join(format!("s{index:02}.jsonl")), "{}\n").unwrap();
            fs::write(archived.join(format!("a{index:02}.jsonl")), "{}\n").unwrap();
        }
        let snapshot = detect_from_home(home.path());
        let files = enumerate_all_source_files(&snapshot);
        assert!(files.len() >= 40, "got {}", files.len());
        assert!(files
            .iter()
            .any(|file| file.source_id == adapter_codex::SOURCE_JSONL));
        assert!(files
            .iter()
            .any(|file| file.source_id == "codex-archived-sessions"));
    }

    #[test]
    fn grok_detection_reads_primary_session_updates_not_unified_logs() {
        let home = tempfile::tempdir().unwrap();
        let grok = home.path().join(".grok");
        let primary = grok.join("sessions").join("proj").join("primary");
        let subagent = grok.join("sessions").join("proj").join("child");
        fs::create_dir_all(grok.join("logs")).unwrap();
        fs::create_dir_all(&primary).unwrap();
        fs::create_dir_all(&subagent).unwrap();
        fs::write(grok.join("version.json"), "{\"version\":\"1.0.13\"}\n").unwrap();
        fs::write(
            grok.join("logs").join("unified.jsonl"),
            "{\"msg\":\"shell.turn.inference_done\"}\n",
        )
        .unwrap();
        fs::write(primary.join("updates.jsonl"), "{}\n").unwrap();
        fs::write(primary.join("events.jsonl"), "{}\n").unwrap();
        fs::write(subagent.join("updates.jsonl"), "{}\n").unwrap();
        fs::write(
            subagent.join("summary.json"),
            "{\"session_kind\":\"subagent\"}\n",
        )
        .unwrap();
        let snapshot = detect_from_home(home.path());
        assert!(snapshot.is_installed(OfficialAgent::GrokBuild));
        let source = snapshot
            .source(
                OfficialAgent::GrokBuild,
                adapter_grok_build::HISTORY_SOURCE_ID,
            )
            .unwrap();
        assert_eq!(source.path.as_ref().unwrap(), &grok.join("sessions"));
        let files = list_grok_history_files(source.path.as_ref().unwrap(), 32);
        assert_eq!(files.len(), 1);
        assert!(files[0].ends_with("updates.jsonl"));
        assert!(files[0].to_string_lossy().contains("primary"));
    }
}
