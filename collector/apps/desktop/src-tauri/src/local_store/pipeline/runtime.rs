//! Desktop/daemon glue: discover sources, run AcquisitionRunner, drain metrics.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use collector_service::{DetectionSnapshot, OfficialAgent};

use super::adapters::{AdapterRoots, HarnessRegistry, SkillBook};
use super::runner::{
    AcquisitionRunner, AcquisitionScheduler, DiscoveryBudget, DEFAULT_READ_BUDGET,
};
use super::types::{Consumer, CursorKind, RegisterSource, SourceKind, DEFAULT_LEASE_MS};
use super::PipelineWriter;

const DISCOVER_MAX_SOURCES: usize = 64;
const DISCOVER_MAX_DURATION_MS: u64 = 200;

/// Orchestrates harness discovery, bounded concurrent acquisition, and local metrics drain.
pub struct PipelineRuntime {
    writer: Arc<PipelineWriter>,
    registry: HarnessRegistry,
    runner: AcquisitionRunner,
    scheduler: AcquisitionScheduler,
    /// Per-harness discover resume cursor (last locator_ref).
    discover_cursors: Mutex<HashMap<String, String>>,
    /// Per-harness enable switches (default true). Used for discover/claim gate.
    harness_enabled: Mutex<HashMap<String, bool>>,
}

impl PipelineRuntime {
    pub fn start(
        writer: Arc<PipelineWriter>,
        identity_secret: Vec<u8>,
        detection: &DetectionSnapshot,
    ) -> Self {
        let roots = adapter_roots_from_detection(identity_secret, detection);
        let writer_for_skills = Arc::clone(&writer);
        let book = SkillBook::new();
        let book_for_alloc = book.clone();
        let alloc: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync> = Arc::new(move |key, name| {
            if let Some(id) = book_for_alloc.get(&key) {
                return id;
            }
            match writer_for_skills.register_skill(key, Some(name)) {
                Ok(id) => {
                    book_for_alloc.upsert(key, id);
                    id
                }
                Err(_) => 0,
            }
        });
        let registry = HarnessRegistry::from_roots(roots, alloc);
        let _ = book;
        Self {
            writer,
            registry,
            runner: AcquisitionRunner::default(),
            scheduler: AcquisitionScheduler::with_defaults(),
            discover_cursors: Mutex::new(HashMap::new()),
            harness_enabled: Mutex::new(HashMap::new()),
        }
    }

    /// Update per-harness enable for discover / claim / resume.
    pub fn set_harness_enabled(&self, harness_id: &str, enabled: bool) {
        if let Ok(mut map) = self.harness_enabled.lock() {
            map.insert(harness_id.to_string(), enabled);
        }
        let _ = self
            .writer
            .set_harness_sources_enabled(harness_id, enabled);
    }

    pub fn is_harness_enabled(&self, harness_id: &str) -> bool {
        self.harness_enabled
            .lock()
            .ok()
            .and_then(|m| m.get(harness_id).copied())
            .unwrap_or(true)
    }

    /// One daemon tick: rediscover → concurrent acquire ∥ independent metrics drain.
    pub fn tick(&self) -> PipelineTickStats {
        let mut stats = PipelineTickStats::default();
        stats.discovered = self.discover_and_register();

        let (metrics_tx, metrics_rx) = std::sync::mpsc::sync_channel(1);
        let (acquire_tx, acquire_rx) = std::sync::mpsc::sync_channel(1);

        std::thread::scope(|scope| {
            scope.spawn(|| {
                let acquired = self.run_due_sources();
                let _ = acquire_tx.send(acquired);
            });
            scope.spawn(|| {
                // Metrics wake independently of this tick's acquisition finishing.
                let metrics = self.drain_metrics();
                let _ = metrics_tx.send(metrics);
            });
        });

        stats.acquired = acquire_rx.recv().unwrap_or(0);
        stats.metrics = metrics_rx.recv().unwrap_or(0);
        let _ = self.writer.run_compensation();
        stats
    }

    fn discover_and_register(&self) -> usize {
        let mut n = 0usize;
        let now = now_ms();
        for strategy in self.registry.strategies() {
            let harness = strategy.harness_id();
            if !self.is_harness_enabled(harness) {
                continue;
            }
            let resume = self
                .discover_cursors
                .lock()
                .ok()
                .and_then(|m| m.get(harness).cloned());
            let budget = DiscoveryBudget::new(DISCOVER_MAX_SOURCES, DISCOVER_MAX_DURATION_MS)
                .with_resume(resume);
            let Ok(specs) = strategy.discover(budget) else {
                continue;
            };
            if let Some(last) = specs.last() {
                if let Ok(mut cursors) = self.discover_cursors.lock() {
                    cursors.insert(harness.to_string(), last.locator_ref.clone());
                }
            }
            let known: std::collections::HashSet<String> = self
                .writer
                .list_source_locators(harness)
                .unwrap_or_default()
                .into_iter()
                .collect();
            // Prefer unknown locators within the page so new files register under the 64 cap.
            let mut ordered: Vec<_> = specs.into_iter().collect();
            ordered.sort_by_key(|s| known.contains(&s.locator_ref));
            for spec in ordered {
                let source = RegisterSource {
                    harness_id: spec.harness_id,
                    source_key: spec.source_key,
                    source_kind: spec.source_kind,
                    locator_ref: spec.locator_ref,
                    stream_key: spec.stream_key,
                    cursor_kind: spec.cursor_kind,
                    cursor_json: spec.initial_cursor_json.to_string(),
                    decoder_state_version: 1,
                    decoder_state_json: spec.initial_decoder_state_json.to_string(),
                    observed_boundary_json: spec.observed_boundary_json.to_string(),
                    next_poll_at: Some(now),
                };
                if self.writer.register_source(source).is_ok() {
                    n += 1;
                }
            }
        }
        n
    }

    fn run_due_sources(&self) -> usize {
        let now = now_ms();
        let Ok(due) = self.writer.list_due_sources(now, 32) else {
            return 0;
        };

        let mut work = Vec::new();
        for source_id in due {
            let Ok(snapshot) = self.writer.load_source_checkpoint(source_id) else {
                continue;
            };
            if !self.is_harness_enabled(&snapshot.harness_id) || !snapshot.enabled {
                continue;
            }
            let Some(strategy) = self
                .registry
                .get_for_source(&snapshot.harness_id, &snapshot.locator_ref)
            else {
                continue;
            };
            let Some(permit) = self.scheduler.try_acquire(&snapshot.harness_id, source_id) else {
                continue;
            };
            work.push((source_id, strategy, permit));
        }

        let ran = Mutex::new(0usize);
        std::thread::scope(|scope| {
            for (source_id, strategy, permit) in work {
                let writer = Arc::clone(&self.writer);
                let runner = &self.runner;
                let counter = &ran;
                scope.spawn(move || {
                    let _permit = permit;
                    let budget = DEFAULT_READ_BUDGET;
                    let _ = runner.run_once(writer.as_ref(), strategy, source_id, budget);
                    if let Ok(mut n) = counter.lock() {
                        *n += 1;
                    }
                });
            }
        });
        ran.into_inner().unwrap_or(0)
    }

    fn drain_metrics(&self) -> usize {
        let mut applied = 0usize;
        for consumer in [Consumer::Hour, Consumer::Day, Consumer::Month] {
            if let Ok(stats) = self
                .writer
                .drain_metrics_consumer(consumer, 64, DEFAULT_LEASE_MS)
            {
                applied += stats.applied;
            }
        }
        applied
    }

    pub fn writer(&self) -> Arc<PipelineWriter> {
        Arc::clone(&self.writer)
    }
}

#[derive(Debug, Clone, Default)]
pub struct PipelineTickStats {
    pub discovered: usize,
    pub acquired: usize,
    pub metrics: usize,
}

fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

pub fn adapter_roots_from_detection(
    identity_secret: Vec<u8>,
    detection: &DetectionSnapshot,
) -> AdapterRoots {
    let home = user_home();
    AdapterRoots {
        identity_secret,
        codex_roots: source_paths(detection, OfficialAgent::Codex).unwrap_or_else(|| {
            vec![(
                "codex-sessions".into(),
                home.join(".codex").join("sessions"),
            )]
        }),
        claude_projects: source_path(detection, OfficialAgent::ClaudeCode)
            .unwrap_or_else(|| home.join(".claude").join("projects")),
        cursor_transcripts: source_path(detection, OfficialAgent::Cursor)
            .unwrap_or_else(|| home.join(".cursor").join("projects")),
        zcode_db: source_path(detection, OfficialAgent::Zcode)
            .unwrap_or_else(|| home.join(".zcode").join("cli/db/db.sqlite")),
        opencode_db: source_path(detection, OfficialAgent::OpenCode)
            .unwrap_or_else(|| home.join(".opencode").join("opencode.sqlite")),
        grok_updates: source_path(detection, OfficialAgent::GrokBuild)
            .unwrap_or_else(|| home.join(".grok").join("sessions")),
        deepseek_sessions: source_path(detection, OfficialAgent::DeepseekHarness)
            .unwrap_or_else(|| home.join(".deepseek-harness")),
        pi_sessions: source_path(detection, OfficialAgent::Pi)
            .unwrap_or_else(|| home.join(".pi")),
        workbuddy_history: source_path(detection, OfficialAgent::WorkBuddy)
            .unwrap_or_else(|| home.join(".workbuddy")),
        doubao_history: source_path(detection, OfficialAgent::DoubaoWork)
            .unwrap_or_else(|| home.join(".doubao-work")),
    }
}

/// Keep **all** supported detection sources for an agent (BTreeMap order must not drop any).
fn source_paths(
    detection: &DetectionSnapshot,
    agent: OfficialAgent,
) -> Option<Vec<(String, PathBuf)>> {
    let mut out = Vec::new();
    for (a, source_id, cfg) in detection.iter_sources() {
        if a != agent {
            continue;
        }
        if let Some(path) = cfg.path.clone() {
            out.push((source_id.to_string(), path));
        }
    }
    if out.is_empty() {
        None
    } else {
        Some(out)
    }
}

fn source_path(detection: &DetectionSnapshot, agent: OfficialAgent) -> Option<PathBuf> {
    // Prefer non-archived / primary source when multiple exist; never silently keep only archived.
    let paths = source_paths(detection, agent)?;
    if let Some((_, p)) = paths.iter().find(|(id, _)| !id.contains("archived")) {
        return Some(p.clone());
    }
    paths.into_iter().next().map(|(_, p)| p)
}

fn user_home() -> PathBuf {
    std::env::var_os("USERPROFILE")
        .or_else(|| std::env::var_os("HOME"))
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("."))
}

/// Test helper: build roots under a single fixture directory layout.
pub fn adapter_roots_for_fixture(identity_secret: Vec<u8>, root: &Path) -> AdapterRoots {
    AdapterRoots {
        identity_secret,
        codex_roots: vec![("codex-sessions".into(), root.join("codex"))],
        claude_projects: root.join("claude"),
        cursor_transcripts: root.join("cursor"),
        zcode_db: root.join("zcode.sqlite"),
        opencode_db: root.join("opencode.sqlite"),
        grok_updates: root.join("grok"),
        deepseek_sessions: root.join("deepseek"),
        pi_sessions: root.join("pi"),
        workbuddy_history: root.join("workbuddy"),
        doubao_history: root.join("doubao"),
    }
}

#[allow(dead_code)]
fn _touch_kinds(_: SourceKind, _: CursorKind) {}
