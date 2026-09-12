//! Continuous bounded acquisition; discovery/metadata reconciliation are independent.

use super::adapters::{AdapterRoots, HarnessRegistry, SkillBook};
use super::runner::{
    AcquisitionRunner, AcquisitionScheduler, DiscoveryBudget, RunOutcome, DEFAULT_READ_BUDGET,
};
use super::types::{Consumer, CursorKind, RegisterSource, SourceKind, DEFAULT_LEASE_MS};
use super::PipelineWriter;
use collector_service::{DetectionSnapshot, OfficialAgent};
use notify::{EventKind, RecursiveMode, Watcher};
use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

const RECONCILE_MS: i64 = 5_000;
const DISCOVER_MS: i64 = 30_000;
const DISCOVER_DEBOUNCE_MS: i64 = 2_000;

type Identity = (String, [u8; 32], String);

#[derive(Clone, Debug, PartialEq, Eq)]
struct Fingerprint(Vec<Option<(u64, SystemTime)>>);

fn fingerprint(path: &str, kind: SourceKind) -> Fingerprint {
    let mut paths = vec![PathBuf::from(path)];
    if kind == SourceKind::Sqlite {
        // SQLite WAL writes may leave the main database's metadata unchanged.
        paths.push(PathBuf::from(format!("{path}-wal")));
    }
    Fingerprint(
        paths
            .iter()
            .map(|p| {
                std::fs::metadata(p)
                    .ok()
                    .and_then(|m| Some((m.len(), m.modified().ok()?)))
            })
            .collect(),
    )
}

impl Fingerprint {
    fn activity(&self) -> SystemTime {
        self.0
            .iter()
            .flatten()
            .map(|(_, t)| *t)
            .max()
            .unwrap_or(UNIX_EPOCH)
    }
}

struct QueuedSource {
    id: i64,
    harness: String,
    locator: String,
    kind: SourceKind,
    observed: Fingerprint,
    ready_at: i64,
    idle: bool,
    in_flight: bool,
    last_served: u64,
}

#[derive(Default)]
struct WorkQueue {
    sources: Vec<QueuedSource>,
    known: HashSet<Identity>,
    next_harness: usize,
    harness_turns: HashMap<String, u64>,
    turn: u64,
}

#[derive(Default)]
struct DiscoveryState {
    last_scan: i64,
    last_metadata: i64,
}

pub struct PipelineRuntime {
    lifecycle: std::sync::RwLock<()>,
    discovery_complete: AtomicBool,
    writer: Arc<PipelineWriter>,
    registry: HarnessRegistry,
    runner: AcquisitionRunner,
    scheduler: AcquisitionScheduler,
    queue: Mutex<WorkQueue>,
    discovery: Mutex<DiscoveryState>,
    harness_enabled: Mutex<HashMap<String, bool>>,
    scan_dirty: Arc<AtomicBool>,
    metadata_dirty: Arc<AtomicBool>,
    _watcher: Option<notify::RecommendedWatcher>,
    acquired: AtomicUsize,
    failures: AtomicUsize,
    pub work_available: tokio::sync::Notify,
}

impl PipelineRuntime {
    pub fn start(
        writer: Arc<PipelineWriter>,
        identity_secret: Vec<u8>,
        detection: &DetectionSnapshot,
    ) -> Self {
        Self::from_roots(
            writer,
            adapter_roots_from_detection(identity_secret, detection),
        )
    }

    pub(crate) fn from_roots(writer: Arc<PipelineWriter>, roots: AdapterRoots) -> Self {
        let scan_dirty = Arc::new(AtomicBool::new(true));
        let metadata_dirty = Arc::new(AtomicBool::new(true));
        let scan_signal = Arc::clone(&scan_dirty);
        let metadata_signal = Arc::clone(&metadata_dirty);
        let mut watcher = notify::recommended_watcher(
            move |result: notify::Result<notify::Event>| match result {
                Ok(event) if !matches!(event.kind, EventKind::Access(_)) => {
                    metadata_signal.store(true, Ordering::Release);
                    if matches!(
                        event.kind,
                        EventKind::Create(_)
                            | EventKind::Remove(_)
                            | EventKind::Modify(notify::event::ModifyKind::Name(_))
                    ) {
                        scan_signal.store(true, Ordering::Release);
                    }
                }
                Err(_) => {
                    scan_signal.store(true, Ordering::Release);
                }
                _ => {}
            },
        )
        .ok();
        if let Some(watcher) = watcher.as_mut() {
            let mut paths: Vec<_> = roots.codex_roots.iter().map(|(_, p)| p.clone()).collect();
            paths.extend([
                roots.claude_projects.clone(),
                roots.cursor_transcripts.clone(),
                roots.zcode_db.clone(),
                roots.opencode_db.clone(),
                roots.grok_updates.clone(),
                roots.deepseek_sessions.clone(),
                roots.pi_sessions.clone(),
                roots.workbuddy_history.clone(),
                roots.doubao_history.clone(),
            ]);
            for path in paths {
                // Missing roots are recovered by the periodic discovery pass.
                let _ = watcher.watch(
                    &path,
                    if path.is_dir() {
                        RecursiveMode::Recursive
                    } else {
                        RecursiveMode::NonRecursive
                    },
                );
            }
        }
        let writer_for_skills = Arc::clone(&writer);
        let book = SkillBook::new();
        let alloc = Arc::new(move |key, name: &str| {
            if let Some(id) = book.get(&key) {
                return id;
            }
            match writer_for_skills.register_skill(key, Some(name)) {
                Ok(id) => {
                    book.upsert(key, id);
                    id
                }
                Err(_) => 0,
            }
        });
        let mut registry = HarnessRegistry::from_roots(roots, alloc);
        let model_writer = Arc::clone(&writer);
        let models = Mutex::new(HashMap::new());
        registry.set_model_allocator(Arc::new(move |provider, model| {
            let key = (provider.to_string(), model.to_string());
            let mut models = models.lock().expect("model dimensions");
            if let Some(id) = models.get(&key) {
                return Ok(*id);
            }
            let id = model_writer.upsert_model(provider, model).map_err(|_| {
                super::runner::RunnerError::Pipeline("model dimension unavailable".into())
            })?;
            models.insert(key, id);
            Ok(id)
        }));
        Self {
            lifecycle: std::sync::RwLock::new(()),
            discovery_complete: AtomicBool::new(false),
            writer,
            registry,
            runner: AcquisitionRunner::default(),
            scheduler: AcquisitionScheduler::with_defaults(),
            queue: Mutex::new(WorkQueue::default()),
            discovery: Mutex::new(DiscoveryState::default()),
            harness_enabled: Mutex::new(HashMap::new()),
            scan_dirty,
            metadata_dirty,
            _watcher: watcher,
            acquired: AtomicUsize::new(0),
            failures: AtomicUsize::new(0),
            work_available: tokio::sync::Notify::new(),
        }
    }

    pub fn set_harness_enabled(&self, harness: &str, enabled: bool) {
        self.harness_enabled
            .lock()
            .expect("harness switches")
            .insert(harness.into(), enabled);
        let _ = self.writer.set_harness_sources_enabled(harness, enabled);
        self.scan_dirty.store(true, Ordering::Release);
        self.work_available.notify_waiters();
    }

    pub fn is_harness_enabled(&self, harness: &str) -> bool {
        self.harness_enabled
            .lock()
            .expect("harness switches")
            .get(harness)
            .copied()
            .unwrap_or(true)
    }

    /// Independent maintenance tick. Acquisition workers never wait for this tick.
    /// The writer already runs lease/retention compensation every five seconds.
    pub fn tick(&self) -> PipelineTickStats {
        let _lifecycle = self.lifecycle.read().expect("pipeline lifecycle");
        let discovered = self.discover_and_register();
        self.refresh_sources();
        let metrics = self.drain_metrics();
        let rebuild = self
            .writer
            .rebuild(super::reconstruction::RebuildAction::Reconcile {
                discovery_ok: self.discovery_complete.load(Ordering::Acquire),
            })
            .unwrap_or_default();
        let q = self.queue.lock().expect("source queue");
        PipelineTickStats {
            rebuilding: rebuild.active,
            discovered,
            acquired: self.acquired.swap(0, Ordering::Relaxed),
            metrics,
            failures: self.failures.swap(0, Ordering::Relaxed),
            sources: q.sources.len(),
            backlog: q.sources.iter().filter(|s| !s.idle).count(),
        }
    }

    fn discover_and_register(&self) -> usize {
        let now = now_ms();
        let mut discovery = self.discovery.lock().expect("discovery");
        if now - discovery.last_scan < DISCOVER_MS
            && (!self.scan_dirty.load(Ordering::Acquire)
                || now - discovery.last_scan < DISCOVER_DEBOUNCE_MS)
        {
            return 0;
        }
        self.scan_dirty.store(false, Ordering::Release);
        discovery.last_scan = now;
        let mut registered = 0;
        let mut discovery_ok = true;
        for strategy in self.registry.strategies() {
            let harness = strategy.harness_id();
            if !self.is_harness_enabled(harness) {
                continue;
            }
            // Enumerate the union once per reconciliation, not once per 64-file page.
            let specs = match strategy.discover(DiscoveryBudget::new(usize::MAX, 200)) {
                Ok(specs) => specs,
                Err(_) => {
                    discovery_ok = false;
                    self.failures.fetch_add(1, Ordering::Relaxed);
                    continue;
                }
            };
            let known = self.queue.lock().expect("source queue").known.clone();
            let mut unknown: Vec<_> = specs
                .into_iter()
                .filter(|s| {
                    !known.contains(&(s.harness_id.clone(), s.source_key, s.stream_key.clone()))
                })
                .map(|s| {
                    let observed = fingerprint(&s.locator_ref, s.source_kind);
                    (s, observed)
                })
                .collect();
            unknown.sort_by(|a, b| {
                b.1.activity()
                    .cmp(&a.1.activity())
                    .then_with(|| a.0.locator_ref.cmp(&b.0.locator_ref))
            });
            for page in unknown.chunks(64) {
                let sources = page
                    .iter()
                    .map(|(s, _)| RegisterSource {
                        harness_id: s.harness_id.clone(),
                        source_key: s.source_key,
                        source_kind: s.source_kind,
                        locator_ref: s.locator_ref.clone(),
                        stream_key: s.stream_key.clone(),
                        cursor_kind: s.cursor_kind,
                        cursor_json: s.initial_cursor_json.to_string(),
                        decoder_state_version: 1,
                        decoder_state_json: s.initial_decoder_state_json.to_string(),
                        observed_boundary_json: s.observed_boundary_json.to_string(),
                        next_poll_at: Some(now),
                    })
                    .collect();
                let ids = match self.writer.register_sources(sources) {
                    Ok(ids) => ids,
                    Err(_) => {
                        discovery_ok = false;
                        self.scan_dirty.store(true, Ordering::Release);
                        self.failures.fetch_add(1, Ordering::Relaxed);
                        continue;
                    }
                };
                for ((spec, observed), id) in page.iter().zip(ids) {
                    let checkpoint = match self.writer.load_source_checkpoint(id) {
                        Ok(c) => c,
                        Err(_) => {
                            self.scan_dirty.store(true, Ordering::Release);
                            continue;
                        }
                    };
                    let mut q = self.queue.lock().expect("source queue");
                    q.known.insert((
                        spec.harness_id.clone(),
                        spec.source_key,
                        spec.stream_key.clone(),
                    ));
                    q.sources.push(QueuedSource {
                        id,
                        harness: spec.harness_id.clone(),
                        locator: spec.locator_ref.clone(),
                        kind: spec.source_kind,
                        observed: observed.clone(),
                        ready_at: checkpoint
                            .lease_until
                            .or(checkpoint.next_poll_at)
                            .unwrap_or(now),
                        idle: false,
                        in_flight: false,
                        last_served: 0,
                    });
                    registered += 1;
                }
                self.work_available.notify_waiters();
            }
        }
        if discovery_ok {
            self.discovery_complete.store(true, Ordering::Release);
        }
        registered
    }

    fn refresh_sources(&self) {
        let now = now_ms();
        let mut discovery = self.discovery.lock().expect("discovery");
        // Coalesce filesystem notifications; compensate for lost notifications every 5s.
        if now - discovery.last_metadata < RECONCILE_MS
            && (!self.metadata_dirty.load(Ordering::Acquire)
                || now - discovery.last_metadata < 1_000)
        {
            return;
        }
        self.metadata_dirty.store(false, Ordering::Release);
        discovery.last_metadata = now;
        // Filesystem I/O is outside both the queue mutex and the SQLite writer.
        let checks: Vec<_> = self
            .queue
            .lock()
            .expect("source queue")
            .sources
            .iter()
            .filter(|s| !s.in_flight)
            .map(|s| (s.id, s.locator.clone(), s.kind, s.observed.clone()))
            .collect();
        for (id, locator, kind, old) in checks {
            let current = fingerprint(&locator, kind);
            if current != old {
                let mut q = self.queue.lock().expect("source queue");
                if let Some(s) = q
                    .sources
                    .iter_mut()
                    .find(|s| s.id == id && !s.in_flight && s.observed == old)
                {
                    s.observed = current;
                    s.idle = false;
                    s.ready_at = now;
                }
            }
        }
        self.work_available.notify_waiters();
    }

    /// Claim ONE batch. Four persistent daemon workers independently refill free slots.
    pub fn acquire_one(&self) -> bool {
        let _lifecycle = self.lifecycle.read().expect("pipeline lifecycle");
        let harnesses = self.registry.harness_ids();
        if harnesses.is_empty() {
            return false;
        }
        let now = now_ms();
        let work = {
            let mut q = self.queue.lock().expect("source queue");
            let mut selected = None;
            for i in 0..harnesses.len() {
                let index = (q.next_harness + i) % harnesses.len();
                let harness = harnesses[index];
                if !self.is_harness_enabled(harness) {
                    continue;
                }
                // Recent sources first, but reserve every eighth turn for the oldest served source.
                // Within active/history classes use least-recently-served order, so large files yield.
                let fair_turn = q.harness_turns.get(harness).copied().unwrap_or(0) % 8 == 7;
                let today = (now + 8 * 3_600_000).div_euclid(86_400_000);
                let candidate = q
                    .sources
                    .iter()
                    .enumerate()
                    .filter(|(_, s)| {
                        s.harness == harness
                            && !s.in_flight
                            && s.ready_at <= now
                            && (!s.idle || s.kind != SourceKind::Jsonl)
                    })
                    .min_by_key(|(_, s)| {
                        let activity_ms = s
                            .observed
                            .activity()
                            .duration_since(UNIX_EPOCH)
                            .unwrap_or_default()
                            .as_millis() as i64;
                        let active = (activity_ms + 8 * 3_600_000).div_euclid(86_400_000) >= today;
                        ((!fair_turn && !active) as u8, s.last_served, s.id)
                    })
                    .map(|(i, _)| i);
                if let Some(source_index) = candidate {
                    let s = &q.sources[source_index];
                    if let Some(permit) = self.scheduler.try_acquire(harness, s.id) {
                        let result = (s.id, s.harness.clone(), s.locator.clone(), s.kind, permit);
                        *q.harness_turns.entry(harness.into()).or_default() += 1;
                        q.turn += 1;
                        let turn = q.turn;
                        q.sources[source_index].in_flight = true;
                        q.sources[source_index].last_served = turn;
                        q.next_harness = (index + 1) % harnesses.len();
                        selected = Some(result);
                        break;
                    }
                }
            }
            selected
        };
        let Some((id, harness, locator, kind, permit)) = work else {
            return false;
        };
        // Capture before reading: an append during the read remains detectable afterwards.
        let observed = fingerprint(&locator, kind);
        let result = self
            .registry
            .get_for_source(&harness, &locator)
            .map(|strategy| {
                self.runner
                    .run_once(self.writer.as_ref(), strategy, id, DEFAULT_READ_BUDGET)
            });
        let (idle, delay, success) = match result {
            Some(Ok(RunOutcome::Committed(ref stats) | RunOutcome::Empty(ref stats))) => {
                let idle = !stats.has_more || !stats.cursor_advanced;
                (idle, if idle { 30_000 } else { 0 }, true)
            }
            _ => (
                false,
                if kind == SourceKind::Other {
                    30_000
                } else {
                    RECONCILE_MS
                },
                false,
            ),
        };
        {
            let mut q = self.queue.lock().expect("source queue");
            if let Some(s) = q.sources.iter_mut().find(|s| s.id == id) {
                s.observed = observed;
                s.idle = idle;
                s.ready_at = now_ms() + delay;
                s.in_flight = false;
            }
        }
        drop(permit);
        if success {
            self.acquired.fetch_add(1, Ordering::Relaxed);
        } else {
            self.failures.fetch_add(1, Ordering::Relaxed);
        }
        self.work_available.notify_waiters();
        true
    }

    fn drain_metrics(&self) -> usize {
        [Consumer::Hour, Consumer::Day, Consumer::Month]
            .into_iter()
            .map(|consumer| {
                self.writer
                    .drain_metrics_consumer(consumer, 64, DEFAULT_LEASE_MS)
                    .map(|s| s.applied)
                    .unwrap_or(0)
            })
            .sum()
    }

    pub fn collection_status(&self, harness: &str) -> Option<&'static str> {
        self.registry
            .get(harness)
            .and_then(|s| s.collection_status())
    }

    pub fn rebuild(&self) -> Result<super::reconstruction::RebuildStatus, String> {
        let _lifecycle = self.lifecycle.write().map_err(|_| "pipeline lifecycle")?;
        let status = self
            .writer
            .rebuild(super::reconstruction::RebuildAction::Begin(
                env!("CARGO_PKG_VERSION").into(),
            ))
            .map_err(|e| e.to_string())?;
        *self.queue.lock().expect("source queue") = WorkQueue::default();
        *self.discovery.lock().expect("discovery") = DiscoveryState::default();
        self.discovery_complete.store(false, Ordering::Release);
        self.scan_dirty.store(true, Ordering::Release);
        self.work_available.notify_waiters();
        Ok(status)
    }

    pub fn writer(&self) -> Arc<PipelineWriter> {
        Arc::clone(&self.writer)
    }
}

#[derive(Debug, Clone, Default)]
pub struct PipelineTickStats {
    pub rebuilding: bool,
    pub discovered: usize,
    pub acquired: usize,
    pub metrics: usize,
    pub failures: usize,
    pub sources: usize,
    pub backlog: usize,
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
        cursor_usage: Some(super::adapters::CursorUsagePaths::for_home(&home)),
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
        pi_sessions: source_path(detection, OfficialAgent::Pi).unwrap_or_else(|| home.join(".pi")),
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
        cursor_usage: None,
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

#[cfg(test)]
mod tests;
