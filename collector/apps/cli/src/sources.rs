//! The collection sources the CLI can switch on and off.

use std::path::PathBuf;

use collector_service::local_store::pipeline::adapters::AdapterRoots;
use collector_service::OfficialAgent;

pub struct Source {
    /// Stable command-line name; equals the pipeline harness id.
    pub name: &'static str,
    pub agent: OfficialAgent,
}

pub const ALL: [Source; 10] = [
    Source {
        name: "codex",
        agent: OfficialAgent::Codex,
    },
    Source {
        name: "claude-code",
        agent: OfficialAgent::ClaudeCode,
    },
    Source {
        name: "grok-build",
        agent: OfficialAgent::GrokBuild,
    },
    Source {
        name: "cursor",
        agent: OfficialAgent::Cursor,
    },
    Source {
        name: "zcode",
        agent: OfficialAgent::Zcode,
    },
    Source {
        name: "pi",
        agent: OfficialAgent::Pi,
    },
    Source {
        name: "deepseek-harness",
        agent: OfficialAgent::DeepseekHarness,
    },
    Source {
        name: "opencode",
        agent: OfficialAgent::OpenCode,
    },
    Source {
        name: "workbuddy",
        agent: OfficialAgent::WorkBuddy,
    },
    Source {
        name: "doubao-work",
        agent: OfficialAgent::DoubaoWork,
    },
];

pub fn find(name: &str) -> Option<&'static Source> {
    ALL.iter().find(|s| s.name == name)
}

pub fn names() -> String {
    ALL.iter().map(|s| s.name).collect::<Vec<_>>().join(", ")
}

/// Applies an explicit path on top of the detected roots.
pub fn override_root(roots: &mut AdapterRoots, name: &str, path: PathBuf) {
    match name {
        "codex" => roots.codex_roots = vec![("codex-sessions".into(), path)],
        "claude-code" => roots.claude_projects = path,
        "grok-build" => roots.grok_updates = path,
        "cursor" => roots.cursor_transcripts = path,
        "zcode" => roots.zcode_db = path,
        "pi" => roots.pi_sessions = path,
        "deepseek-harness" => roots.deepseek_sessions = path,
        "opencode" => roots.opencode_db = path,
        "workbuddy" => roots.workbuddy_history = path,
        "doubao-work" => roots.doubao_history = path,
        _ => {}
    }
}

/// Where the collector will look for `name` after detection and overrides.
pub fn effective_path(roots: &AdapterRoots, name: &str) -> Option<PathBuf> {
    Some(match name {
        "codex" => roots.codex_roots.first()?.1.clone(),
        "claude-code" => roots.claude_projects.clone(),
        "grok-build" => roots.grok_updates.clone(),
        "cursor" => roots.cursor_transcripts.clone(),
        "zcode" => roots.zcode_db.clone(),
        "pi" => roots.pi_sessions.clone(),
        "deepseek-harness" => roots.deepseek_sessions.clone(),
        "opencode" => roots.opencode_db.clone(),
        "workbuddy" => roots.workbuddy_history.clone(),
        "doubao-work" => roots.doubao_history.clone(),
        _ => return None,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn names_are_unique_and_match_adapter_ids() {
        for (i, a) in ALL.iter().enumerate() {
            assert!(ALL.iter().skip(i + 1).all(|b| b.name != a.name));
            let adapter = collector_service::adapter_id(a.agent);
            assert!(adapter.ends_with(&format!(".adapter.{}", a.name)) || a.name == "claude-code");
        }
    }
}

#[derive(Debug, Clone, serde::Serialize)]
pub struct SourceReport {
    pub name: &'static str,
    pub enabled: bool,
    pub path: Option<String>,
    /// `configured` (set-path) or `detected` (platform default).
    pub path_origin: &'static str,
    /// The collector's own detection found this tool installed on this host.
    pub detected: bool,
    pub exists: bool,
    pub readable: bool,
}

/// Snapshot of every source as the collector would see it. Read-only.
pub fn report(config: &crate::config::Config) -> Vec<SourceReport> {
    let detection = collector_service::detect_local();
    let mut roots = collector_service::local_store::pipeline::runtime::adapter_roots_from_detection(
        Vec::new(),
        &detection,
    );
    ALL.iter()
        .map(|source| {
            let configured = config.source_path(source.name);
            if let Some(path) = configured {
                override_root(&mut roots, source.name, path.to_path_buf());
            }
            let path = effective_path(&roots, source.name);
            let (exists, readable) = path.as_deref().map(probe).unwrap_or((false, false));
            SourceReport {
                name: source.name,
                enabled: config.source_enabled(source.name),
                path: path.map(|p| p.display().to_string()),
                path_origin: if configured.is_some() {
                    "configured"
                } else {
                    "default"
                },
                detected: detection.is_installed(source.agent),
                exists,
                readable,
            }
        })
        .collect()
}

/// `(exists, readable)` for a file or directory.
pub fn probe(path: &std::path::Path) -> (bool, bool) {
    match std::fs::metadata(path) {
        Ok(meta) if meta.is_dir() => (true, std::fs::read_dir(path).is_ok()),
        Ok(_) => (true, std::fs::File::open(path).is_ok()),
        Err(_) => (false, false),
    }
}
