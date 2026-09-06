use super::alerts::{AlertInput, AlertStore};
use super::model::{
    agent_display_name, build_orb_snapshot, build_render_snapshot, collector_state_from_daemon,
    details_window_from_record, CollectorSnapshot, EffectsMode, OrbPreferences, OrbPulse,
    OrbQuotaSnapshot, OrbRenderSnapshot, OrbSnapshot, PulseKind, QuotaRecord, QuotaState,
    QuotaWindowRecord, SCHEMA_VERSION, TodaySourceTotal, UsageSummary,
};
use super::model::{OrbInteraction, SuspendReason};
use super::motion::{Ball, Edge, EdgeDrag, EdgeSlide};
use super::placement::{layout_orb, placement_from_origin, MonitorInfo, PixelRect};
use super::platform::{
    accept_instance_ready, is_foreground_fullscreen_at, OrbReady, OrbWindowGroup, ORB_LABEL,
};
use super::preferences::{PreferencesPatch, PreferencesStore};
use super::quota_broker::QuotaBroker;

use crate::commands::quotas::AgentQuota;
use crate::state::{app_data_root, AppState};
use serde::Serialize;
use std::collections::HashSet;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::ThreadId;
use std::time::{Duration, Instant};
use tauri::{AppHandle, Emitter, Manager, PhysicalPosition, WebviewWindow};
use uuid::Uuid;

const SNAPSHOT_EVENT: &str = "orb://snapshot";
const RENDER_EVENT: &str = "orb://render";
const PREFERENCES_EVENT: &str = "orb://preferences";
const USAGE_PULSE_MS: i64 = 500;

#[derive(Clone)]
pub struct OrbHandle {
    inner: Arc<Mutex<OrbController>>,
    app: AppHandle,
    ui_thread: ThreadId,
}

struct OrbController {
    app: AppHandle,
    state: AppState,
    prefs: PreferencesStore,
    alerts: AlertStore,
    broker: QuotaBroker,
    stream_id: String,
    revision: u128,
    snapshot: OrbSnapshot,
    group: Option<OrbWindowGroup>,
    ready: HashSet<String>,
    last_today_tokens: Option<String>,
    last_date: String,
    pulse: Option<OrbPulse>,
    suspend_reasons: Vec<SuspendReason>,
    interaction: OrbInteraction,
    main_visible: bool,
    native_visible: bool,
    building: bool,
    build_failed: bool,
    window_epoch: u64,
    early_ready: Vec<OrbReady>,
    ball: Option<Ball>,
    docked: bool,
    drag_start: Option<(f64, f64)>,
    pulling: bool,
    edge_slide: Option<EdgeSlide>,
    edge_drag: Option<EdgeDrag>,
    pending_placement: Option<PhysicalPosition<i32>>,
    motion_active: Arc<AtomicBool>,
    last_frame: Instant,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct DetailsUsage {
    local_date: String,
    state: super::model::UsageState,
    today_tokens: Option<String>,
    known_source_count: u32,
    has_unmeasured_sources: bool,
    captured_at_ms: i64,
    last_recorded_change_at_ms: Option<i64>,
    sources: Vec<TodaySourceTotal>,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct QuotaOption {
    agent_id: String,
    agent_name: String,
    window_id: String,
    window_label: String,
    state: QuotaState,
    remaining_percent: Option<f64>,
    last_known_remaining_percent: Option<f64>,
    observed_at_ms: Option<i64>,
    resets_at_ms: Option<i64>,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OrbDetailsView {
    schema_version: u32,
    stream_id: String,
    revision: String,
    emitted_at_ms: i64,
    preferences_revision: String,
    hidden: bool,
    usage: DetailsUsage,
    collector: CollectorSnapshot,
    quota: OrbQuotaSnapshot,
    options: Vec<QuotaOption>,
}

impl OrbHandle {
    pub fn install(app: &AppHandle, state: AppState) -> Self {
        let handle = Self {
            inner: Arc::new(Mutex::new(OrbController::new(app.clone(), state))),
            app: app.clone(),
            ui_thread: std::thread::current().id(),
        };
        // No WebView construction during setup: the primary windows must be
        // able to finish startup even if the optional orb cannot be created.
        let visibility = handle.clone();
        let motion = handle.clone();
        let active = handle.inner.lock().expect("orb controller").motion_active.clone();
        tauri::async_runtime::spawn(async move {
            let queued = Arc::new(AtomicBool::new(false));
            let mut interval = tokio::time::interval(Duration::from_millis(16));
            interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            loop {
                interval.tick().await;
                if !active.load(Ordering::Relaxed) || queued.swap(true, Ordering::Relaxed) { continue; }
                let handle = motion.clone();
                let completed = queued.clone();
                if motion.app.run_on_main_thread(move || {
                    let primary_visible = crate::commands::window::primary_ui_visible(&handle.app);
                    if let Ok(mut inner) = handle.inner.try_lock() {
                        if primary_visible { inner.motion_active.store(false, Ordering::Relaxed); }
                        else if let Err(error) = inner.motion_frame() {
                            inner.motion_active.store(false, Ordering::Relaxed);
                            eprintln!("orb motion stopped: {error}");
                        }
                    }
                    completed.store(false, Ordering::Relaxed);
                }).is_err() { queued.store(false, Ordering::Relaxed); }
            }
        });
        tauri::async_runtime::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_millis(250));
            loop {
                interval.tick().await;
                visibility.queue_visibility_sync();
            }
        });
        let ui = handle.inner.clone();
        let app = handle.app.clone();
        let tick_queued = Arc::new(AtomicBool::new(false));
        tauri::async_runtime::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(3));
            loop {
                interval.tick().await;
                let state = {
                    let Ok(inner) = ui.lock() else { continue };
                    inner.state.clone()
                };
                let daemon = state.get_daemon_status().await;
                if tick_queued
                    .compare_exchange(false, true, Ordering::SeqCst, Ordering::SeqCst)
                    .is_err()
                {
                    continue;
                }
                let inner = ui.clone();
                let queued = tick_queued.clone();
                if app
                    .run_on_main_thread(move || {
                        if let Ok(mut guard) = inner.lock() {
                            guard.tick(&daemon);
                        }
                        queued.store(false, Ordering::SeqCst);
                    })
                    .is_err()
                {
                    tick_queued.store(false, Ordering::SeqCst);
                }
            }
        });
        let quotas = handle.inner.clone();
        let inflight = Arc::new(AtomicBool::new(false));
        tauri::async_runtime::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(3));
            loop {
                interval.tick().await;
                let should_refresh = quotas.lock().expect("orb controller").should_sample();
                if !should_refresh {
                    continue;
                }
                if inflight
                    .compare_exchange(false, true, Ordering::SeqCst, Ordering::SeqCst)
                    .is_err()
                {
                    continue;
                }
                let result = crate::commands::quotas::get_agent_quotas().await;
                {
                    let mut inner = quotas.lock().expect("orb controller");
                    if let Ok(list) = result {
                        inner.ingest_quotas(list);
                    }
                    if !inner.motion_active.load(Ordering::Relaxed) && inner.interaction != OrbInteraction::Dragging {
                        inner.refresh_snapshot(false, None);
                        inner.emit_all();
                    }
                }
                inflight.store(false, Ordering::SeqCst);
            }
        });
        handle
    }

    fn dispatch_ui<R, F>(&self, f: F) -> Result<R, String>
    where
        R: Send + 'static,
        F: FnOnce(&mut OrbController) -> R + Send + 'static,
    {
        if std::thread::current().id() == self.ui_thread {
            let mut inner = self.inner.lock().map_err(|error| error.to_string())?;
            return Ok(f(&mut inner));
        }
        let inner = self.inner.clone();
        let (tx, rx) = std::sync::mpsc::sync_channel(1);
        self.app
            .run_on_main_thread(move || {
                let result = inner
                    .lock()
                    .map(|mut guard| f(&mut guard))
                    .map_err(|error| error.to_string());
                let _ = tx.send(result);
            })
            .map_err(|error| error.to_string())?;
        rx.recv().map_err(|error| error.to_string())?
    }

    pub fn snapshot(&self) -> OrbSnapshot {
        self.inner.lock().expect("orb controller").snapshot.clone()
    }

    pub fn render_snapshot(&self) -> OrbRenderSnapshot {
        let inner = self.inner.lock().expect("orb controller");
        build_render_snapshot(&inner.snapshot, inner.prefs.snapshot().diameter_dip)
    }

    pub fn details(&self) -> OrbDetailsView {
        self.inner.lock().expect("orb controller").details_view()
    }

    pub fn preferences(&self) -> OrbPreferences {
        self.inner.lock().expect("orb controller").prefs.snapshot()
    }

    pub fn patch_preferences(&self, patch: PreferencesPatch) -> Result<OrbPreferences, String> {
        self.dispatch_ui(move |inner| {
            let rebuild = patch.enabled.is_some()
                || patch.diameter_dip.is_some()
                || patch.effects_mode.is_some();
            let hide_on_fullscreen_changed = patch.hide_on_fullscreen.is_some();
            let saved = inner.prefs.patch(patch).map_err(|error| error.to_string())?;
            inner.broker.restore_selection(saved.selection.clone());
            if rebuild {
                inner.rebuild_windows();
            } else {
                if hide_on_fullscreen_changed {
                    inner.reconcile_visibility();
                }
                inner.refresh_snapshot(false, None);
            }
            inner.emit_all();
            Ok(saved)
        })?
    }

    pub fn ready(&self, label: &str, generation: u64) -> Result<(), String> {
        let label = label.to_owned();
        self.dispatch_ui(move |inner| inner.on_ready(&label, generation))?
    }

    pub fn action(&self, label: &str, action: &str, paused: Option<bool>) -> Result<(), String> {
        if action == "activate" {
            let restored = self.dispatch_ui(|inner| {
                inner.motion_active.store(false, Ordering::Relaxed);
                inner.restore_docked()
            })??;
            if restored { return Ok(()); }
            return self.action(label, "open_main", None);
        }
        // Opening the primary UI must never run while holding the orb lock.
        // Only an explicit activation (double-click or keyboard) opens main.
        if matches!(action, "open_main" | "open_details" | "open_settings") {
            let app = self.app.clone();
            let settings = action == "open_settings";
            return self.app.run_on_main_thread(move || {
                if settings {
                    let _ = crate::commands::window::open_settings(app);
                } else {
                    let _ = crate::commands::window::request_initial_panel(&app);
                }
            }).map_err(|error| error.to_string());
        }
        let label = label.to_string();
        let action = action.to_string();
        self.dispatch_ui(move |inner| inner.action(&label, &action, paused))?
    }

    pub fn begin_drag(&self, window: &WebviewWindow) -> Result<(), String> {
        let window = window.clone();
        self.dispatch_ui(move |inner| inner.begin_drag(&window, false))?
    }

    /// Called before presenting main/settings; hiding the orb is synchronous
    /// on the UI thread so the two presentations cannot overlap.
    pub fn main_opened(&self) {
        let _ = self.dispatch_ui(|inner| {
            inner.main_visible = true;
            inner.reconcile_visibility();
        });
    }

    pub fn queue_visibility_sync(&self) {
        let handle = self.clone();
        let _ = self.app.run_on_main_thread(move || {
            let main_visible = crate::commands::window::primary_ui_visible(&handle.app);
            if let Ok(mut inner) = handle.inner.try_lock() {
                inner.main_visible = main_visible;
                inner.reconcile_visibility();
                if !inner.motion_active.load(Ordering::Relaxed) && inner.interaction != OrbInteraction::Dragging {
                    inner.flush_pending_placement();
                }
            }
        });
    }

    pub fn end_drag(&self) -> Result<(), String> {
        self.dispatch_ui(|inner| inner.end_drag())?
    }

    pub fn move_drag(&self, dx: f64, dy: f64) -> Result<(), String> {
        self.dispatch_ui(move |inner| inner.move_drag(dx, dy))?
    }

    pub fn fling(&self, dx: f64, dy: f64) -> Result<(), String> {
        self.dispatch_ui(move |inner| inner.fling(dx, dy))?
    }

    pub async fn set_paused(&self, paused: bool) -> Result<(), String> {
        let state = self.inner.lock().expect("orb controller").state.clone();
        state.set_global_pause(paused).await?;
        self.dispatch_ui(|inner| {
            inner.refresh_snapshot(false, None);
            inner.emit_all();
        })
    }

    pub fn on_close(&self, label: &str) {
        let label = label.to_string();
        let _ = self.dispatch_ui(move |inner| match label.as_str() {
            "orb" | "orb-effects" => inner.hide_user(),
            "orb-details" => {
                inner.close_details();
                Ok(())
            }
            _ => Ok(()),
        });
    }

    pub fn toggle_enabled(&self) -> Result<bool, String> {
        self.dispatch_ui(|inner| {
            let current = inner.prefs.snapshot();
            let enabled = !current.enabled;
            let saved = inner
                .prefs
                .patch(PreferencesPatch {
                    expected_revision: current.revision,
                    enabled: Some(enabled),
                    ..PreferencesPatch::default()
                })
                .map_err(|error| error.to_string())?;
            inner.broker.restore_selection(saved.selection.clone());
            inner.rebuild_windows();
            inner.emit_all();
            Ok(enabled)
        })?
    }

    pub fn handle_context_menu(&self, id: &str) -> Result<(), String> {
        match id {
            "orb_ctx_details" => self.action("orb", "open_details", None),
            "orb_ctx_pause" => {
                let paused = matches!(
                    self.snapshot().collector.state,
                    super::model::CollectorState::Paused | super::model::CollectorState::Stopped
                );
                let handle = self.clone();
                tauri::async_runtime::spawn(async move {
                    let _ = handle.set_paused(!paused).await;
                });
                Ok(())
            }
            "orb_ctx_settings" => self.action("orb", "open_settings", None),
            "orb_ctx_hide" => self.action("orb", "hide", None),
            "orb_ctx_fx_orbit" | "orb_ctx_fx_soft" | "orb_ctx_fx_off" => {
                let mode = match id {
                    "orb_ctx_fx_soft" => super::model::EffectsMode::Soft,
                    "orb_ctx_fx_off" => super::model::EffectsMode::Off,
                    _ => super::model::EffectsMode::Orbit,
                };
                let current = self.preferences();
                self.patch_preferences(PreferencesPatch {
                    expected_revision: current.revision,
                    effects_mode: Some(mode),
                    ..PreferencesPatch::default()
                })?;
                Ok(())
            }
            _ => Ok(()),
        }
    }
}

impl OrbController {
    fn new(app: AppHandle, state: AppState) -> Self {
        let root = app_data_root();
        let prefs = PreferencesStore::load(&root);
        let alerts = AlertStore::load(&root);
        let mut broker = QuotaBroker::new();
        let saved = prefs.snapshot();
        broker.restore_selection(saved.selection.clone());
        let usage = empty_usage();
        let snapshot = build_orb_snapshot(
            &Uuid::new_v4().to_string(),
            0,
            now_ms(),
            &saved,
            usage,
            CollectorSnapshot {
                state: super::model::CollectorState::Stopped,
                sync_state: String::new(),
            },
            OrbQuotaSnapshot::empty(QuotaState::Loading),
            false,
            None,
        );
        Self {
            app,
            state,
            prefs,
            alerts,
            broker,
            stream_id: snapshot.stream_id.clone(),
            revision: 0,
            last_today_tokens: snapshot.usage.today_tokens.clone(),
            last_date: snapshot.usage.local_date.clone(),
            snapshot,
            group: None,
            ready: HashSet::new(),
            pulse: None,
            suspend_reasons: Vec::new(),
            interaction: OrbInteraction::Idle,
            main_visible: true,
            native_visible: false,
            building: false,
            build_failed: false,
            window_epoch: 0,
            early_ready: Vec::new(),
            ball: None,
            docked: false,
            drag_start: None,
            pulling: false,
            edge_slide: None,
            edge_drag: None,
            pending_placement: None,
            motion_active: Arc::new(AtomicBool::new(false)),
            last_frame: Instant::now(),
        }
    }

    fn should_sample(&self) -> bool {
        self.orb_visible()
    }

    fn reconcile_visibility(&mut self) {
        self.sync_fullscreen_visibility();
        if self.prefs.snapshot().enabled && !self.main_visible && self.group.is_none()
            && !self.building && !self.build_failed
            // Destroy is asynchronous in Tauri; do not reuse labels until
            // the previous native windows have actually left the manager.
            && ["orb", "orb-effects", "orb-details"].iter()
                .all(|label| self.app.get_webview_window(label).is_none()) {
            self.start_window_build();
        }
        let show = self.orb_visible();
        if !show {
            self.motion_active.store(false, Ordering::Relaxed);
            self.drag_start = None;
            self.interaction = OrbInteraction::Idle;
        }
        if show != self.native_visible {
            if let Some(group) = &self.group {
                let result = if show { group.show_without_activation() } else { group.hide() };
                if let Err(error) = result {
                    eprintln!("orb presentation failed: {error}");
                    return;
                }
            }
            self.native_visible = show;
            if show && self.edge_slide.is_some() {
                self.last_frame = Instant::now();
                self.motion_active.store(true, Ordering::Relaxed);
            }
            self.refresh_snapshot(false, None);
            self.emit_all();
        }
    }

    fn start_window_build(&mut self) {
        self.building = true;
        let epoch = self.window_epoch;
        let prefs = self.prefs.snapshot();
        let app = self.app.clone();
        tauri::async_runtime::spawn(async move {
            let factory_app = app.clone();
            // Tauri waits for the UI thread when building a WebView. No
            // controller lock may be held here, including by a UI callback.
            let result = tokio::task::spawn_blocking(move || {
                OrbWindowGroup::create(&factory_app, f64::from(prefs.diameter_dip),
                    prefs.effects_mode != EffectsMode::Off)
            }).await.map_err(|error| error.to_string()).and_then(|result| result);
            let callback_app = app.clone();
            let _ = app.run_on_main_thread(move || {
                let Some(handle) = callback_app.try_state::<OrbHandle>() else { return };
                let Ok(mut inner) = handle.inner.lock() else { return };
                inner.building = false;
                if epoch != inner.window_epoch {
                    if let Ok(group) = result { group.destroy(); }
                    inner.early_ready.clear();
                    inner.reconcile_visibility();
                    return;
                }
                match result {
                    Ok(group) => {
                        let prefs = inner.prefs.snapshot();
                        if let Some(layout) = layout_orb(&prefs.placement, prefs.diameter_dip, &monitors(&callback_app)) {
                            let _ = group.move_orb_center(PhysicalPosition::new(layout.center_px.0, layout.center_px.1));
                        }
                        inner.group = Some(group);
                        let ready = std::mem::take(&mut inner.early_ready);
                        for event in ready { let _ = inner.on_ready(&event.label, event.generation); }
                        inner.main_visible = crate::commands::window::primary_ui_visible(&callback_app);
                        inner.reconcile_visibility();
                    }
                    Err(error) => {
                        inner.build_failed = true;
                        eprintln!("optional orb creation failed: {error}");
                    }
                }
            });
        });
    }

    fn ingest_quotas(&mut self, quotas: Vec<AgentQuota>) {
        let records: Vec<QuotaRecord> = quotas.into_iter().map(quota_record).collect();
        let present: HashSet<String> = records.iter().map(|record| record.agent_id.clone()).collect();
        for record in records {
            let generation = self.broker.generation(&record.agent_id);
            self.broker.ingest_async(generation, record);
        }
        self.broker.drop_missing(&present);
        let now = now_ms();
        self.broker.maybe_auto_select(now);
        if self.prefs.snapshot().selection.is_none() {
            if let Some(selection) = self.broker.selection().cloned() {
                let current = self.prefs.snapshot();
                let _ = self.prefs.patch(PreferencesPatch {
                    expected_revision: current.revision,
                    selection: Some(Some(selection)),
                    ..PreferencesPatch::default()
                });
            }
        }
    }

    fn tick(&mut self, daemon: &crate::state::DaemonStatus) {
        self.expire_pulse();
        self.reconcile_visibility();
        // Usage aggregation is synchronous. Keep it out of animation frames.
        if self.motion_active.load(Ordering::Relaxed) || self.interaction == OrbInteraction::Dragging { return; }
        if !self.should_sample() && self.group.is_none() {
            return;
        }
        self.refresh_snapshot(true, Some(daemon));
        self.emit_all();
    }

    fn expire_pulse(&mut self) {
        if let Some(pulse) = &self.pulse {
            if pulse.expires_at_ms <= now_ms() {
                self.pulse = None;
            }
        }
    }

    fn sync_fullscreen_visibility(&mut self) {
        if self.interaction == OrbInteraction::Dragging {
            return;
        }
        if !self.prefs.snapshot().hide_on_fullscreen {
            self.clear_fullscreen_suspend(true);
            return;
        }
        self.apply_fullscreen_suspend();
    }

    fn clear_fullscreen_suspend(&mut self, _show: bool) {
        self.suspend_reasons.retain(|reason| *reason != SuspendReason::Fullscreen);
    }

    fn apply_fullscreen_suspend(&mut self) {
        let Some(group) = &self.group else { return };
        let Ok(origin) = group.orb_origin() else { return };
        let fullscreen = is_foreground_fullscreen_at(&self.app, origin);
        if fullscreen {
            if !self.suspend_reasons.contains(&SuspendReason::Fullscreen) {
                self.suspend_reasons.push(SuspendReason::Fullscreen);
            }
        } else {
            self.clear_fullscreen_suspend(false);
        }
    }

    fn refresh_snapshot(&mut self, detect_usage_pulse: bool, daemon: Option<&crate::state::DaemonStatus>) {
        let prefs = self.prefs.snapshot();
        let now = now_ms();
        let date = chrono::Local::now().date_naive();
        let usage = self.state.get_usage_summary(date);
        let collector = daemon
            .map(|status| CollectorSnapshot {
                state: collector_state_from_daemon(&status.status, status.global_paused),
                sync_state: status.sync_status.clone(),
            })
            .unwrap_or_else(|| self.snapshot.collector.clone());
        self.broker.maybe_auto_select(now);
        let mut quota = self.broker.view(now);
        if quota.identity_confidence == super::model::IdentityConfidence::Unavailable
            && quota.selection.as_ref().is_some_and(|item| item.agent_id == "codex")
        {
            quota.identity_note = Some("来自最近本地日志".into());
        }
        let visible = self.orb_visible();
        if detect_usage_pulse {
            self.pulse = usage_pulse(
                detect_usage_pulse,
                &self.last_date,
                self.last_today_tokens.as_deref(),
                &usage,
                self.pulse.clone(),
                now,
            );
            if let Some(selection) = quota.selection.clone() {
                let cycle = quota.resets_at_ms.map(|ms| ms.to_string());
                if let Some(pulse) = self.alerts.consider(&AlertInput {
                    agent_id: &selection.agent_id,
                    window_id: &selection.window_id,
                    account_scope: None,
                    reset_cycle: cycle.as_deref(),
                    remaining_percent: quota.remaining_percent,
                    quota_state: quota.state,
                    orb_visible: visible,
                    collector_paused: matches!(
                        collector.state,
                        super::model::CollectorState::Paused | super::model::CollectorState::Stopped
                    ),
                    now_ms: now,
                }) {
                    self.pulse = Some(pulse);
                }
            }
        }
        if detect_usage_pulse {
            self.last_today_tokens = usage.today_tokens.clone();
            self.last_date = usage.local_date.clone();
        }
        self.revision = self.revision.saturating_add(1);
        let mut snapshot = build_orb_snapshot(
            &self.stream_id,
            self.revision,
            now,
            &prefs,
            usage,
            collector,
            quota,
            false,
            self.pulse.clone(),
        );
        snapshot.hidden = !self.orb_visible();
        self.snapshot = snapshot;
    }

    fn orb_visible(&self) -> bool {
        should_show_orb(
            self.prefs.snapshot().enabled,
            self.main_visible,
            self.group.is_some() && self.ready.contains(ORB_LABEL),
            !self.suspend_reasons.is_empty(),
        )
    }

    fn details_view(&self) -> OrbDetailsView {
        let prefs = self.prefs.snapshot();
        let now = now_ms();
        let sources = self.state.orb_today_sources();
        let options = self
            .broker
            .cached_records()
            .iter()
            .flat_map(|record| {
                record.windows.iter().filter_map(|window| {
                    let details = details_window_from_record(record, window, now)?;
                    Some(QuotaOption {
                        agent_id: record.agent_id.clone(),
                        agent_name: agent_display_name(&record.agent_id).to_string(),
                        window_id: details.window_id,
                        window_label: details.label.unwrap_or_default(),
                        state: details.state,
                        remaining_percent: details.remaining_percent,
                        last_known_remaining_percent: details.last_known_remaining_percent,
                        observed_at_ms: details.observed_at_ms,
                        resets_at_ms: details.resets_at_ms,
                    })
                })
            })
            .collect();
        OrbDetailsView {
            schema_version: SCHEMA_VERSION,
            stream_id: self.snapshot.stream_id.clone(),
            revision: self.snapshot.revision.clone(),
            emitted_at_ms: self.snapshot.emitted_at_ms,
            preferences_revision: prefs.revision,
            hidden: self.snapshot.hidden,
            usage: DetailsUsage {
                local_date: self.snapshot.usage.local_date.clone(),
                state: self.snapshot.usage.state,
                today_tokens: self.snapshot.usage.today_tokens.clone(),
                known_source_count: self.snapshot.usage.known_source_count,
                has_unmeasured_sources: self.snapshot.usage.has_unmeasured_sources,
                captured_at_ms: self.snapshot.usage.captured_at_ms,
                last_recorded_change_at_ms: self.snapshot.usage.last_recorded_change_at_ms,
                sources,
            },
            collector: self.snapshot.collector.clone(),
            quota: self.snapshot.quota.clone(),
            options,
        }
    }

    fn rebuild_windows(&mut self) {
        self.edge_drag = None;
        self.edge_slide = None;
        self.motion_active.store(false, Ordering::Relaxed);
        self.ball = None;
        self.docked = false;
        self.drag_start = None;
        self.window_epoch = self.window_epoch.wrapping_add(1);
        self.build_failed = false;
        self.early_ready.clear();
        if let Some(group) = self.group.take() { group.destroy(); }
        self.ready.clear();
        self.native_visible = false;
        self.suspend_reasons.clear();
        self.interaction = OrbInteraction::Idle;
        self.reconcile_visibility();
        self.refresh_snapshot(false, None);
    }

    fn on_ready(&mut self, label: &str, generation: u64) -> Result<(), String> {
        let Some(group) = &self.group else {
            if self.building { self.early_ready.push(OrbReady { label: label.into(), generation }); }
            return Ok(());
        };
        accept_instance_ready(
            group.generation(),
            &group.labels(),
            &OrbReady {
                label: label.into(),
                generation,
            },
        )?;
        self.ready.insert(label.to_string());
        if label == ORB_LABEL {
            self.main_visible = crate::commands::window::primary_ui_visible(&self.app);
            self.reconcile_visibility();
            self.refresh_snapshot(false, None);
            self.emit_all();
        }
        Ok(())
    }

    fn action(&mut self, _label: &str, action: &str, paused: Option<bool>) -> Result<(), String> {
        match action {
            "reveal" => {
                self.restore_docked()?;
                Ok(())
            }
            "begin_pull" => {
                if !self.orb_visible() { return Ok(()); }
                let window = self.group.as_ref().ok_or("orb unavailable")?.orb_window();
                self.begin_drag(&window, true)?;
                self.pulling = true;
                Ok(())
            }
            "cancel_pull" => self.cancel_pull(),
            "close_details" => {
                self.close_details();
                Ok(())
            }
            "hide" => self.hide_user(),
            "open_main" => crate::commands::window::request_initial_panel(&self.app)
                .map_err(|error| error.to_string()),
            "open_settings" => crate::commands::window::open_settings(self.app.clone()),
            "set_paused" => {
                let _ = paused;
                self.refresh_snapshot(false, None);
                self.emit_all();
                Ok(())
            }
            "stop_motion" => {
                self.motion_active.store(false, Ordering::Relaxed);
                self.restore_docked()?;
                Ok(())
            }
            "grab" => {
                self.motion_active.store(false, Ordering::Relaxed);
                if !self.docked {
                    if let Some(group) = &self.group { group.settle_motion()?; }
                }
                Ok(())
            }
            "release_grab" => {
                if self.edge_slide.is_some() && self.orb_visible() {
                    self.last_frame = Instant::now();
                    self.motion_active.store(true, Ordering::Relaxed);
                }
                Ok(())
            }
            other => Err(format!("unsupported orb action {other}")),
        }
    }

    fn close_details(&mut self) {
        if let Some(details) = self.app.get_webview_window("orb-details") {
            let _ = details.hide();
        }
    }

    fn hide_user(&mut self) -> Result<(), String> {
        self.edge_drag = None;
        self.edge_slide = None;
        self.motion_active.store(false, Ordering::Relaxed);
        self.docked = false;
        self.ball = None;
        self.drag_start = None;
        let current = self.prefs.snapshot();
        self.prefs.patch(PreferencesPatch {
            expected_revision: current.revision,
            enabled: Some(false),
            ..PreferencesPatch::default()
        }).map_err(|error| error.to_string())?;
        self.window_epoch = self.window_epoch.wrapping_add(1);
        self.native_visible = false;
        if let Some(group) = self.group.take() {
            group.destroy();
        }
        self.ready.clear();
        self.suspend_reasons.clear();
        self.interaction = OrbInteraction::Idle;
        self.refresh_snapshot(false, None);
        self.emit_all();
        Ok(())
    }

    fn begin_drag(&mut self, _window: &WebviewWindow, slingshot: bool) -> Result<(), String> {
        self.pulling = false;
        self.motion_active.store(false, Ordering::Relaxed);
        self.edge_slide = None;
        self.edge_drag = None;
        let parked = self.docked && !slingshot;
        if self.docked && slingshot {
            if let Some(mut ball) = self.ball.clone() {
                ball.clamp_inside();
                self.place_ball(&ball)?;
            }
            self.docked = false;
            if let Some(group) = &self.group { group.set_docked_clip(None)?; }
        }
        self.close_details();
        self.interaction = OrbInteraction::Dragging;
        let ball = if parked {
            // Never read the monitor from an off-screen center: it may belong
            // to the adjacent display. Preserve the parked ball's monitor/DPI.
            let mut ball = self.ball.clone().ok_or("docked position missing")?;
            let origin = self.group.as_ref().ok_or("orb unavailable")?.orb_origin()?;
            ball.x = origin.x as f64; ball.y = origin.y as f64;
            self.edge_drag = Some(EdgeDrag::new(ball.clone()));
            self.docked = false;
            ball
        } else { self.read_ball()? };
        self.drag_start = Some((ball.x, ball.y));
        self.ball = Some(ball);
        Ok(())
    }

    fn end_drag(&mut self) -> Result<(), String> {
        self.pulling = false;
        self.drag_start = None;
        self.interaction = OrbInteraction::Idle;
        let edge_drag = self.edge_drag.take();
        let ball = if let Some(drag) = edge_drag {
            // A partial pull returns smoothly to the edge from the last mouse
            // position; do not jump to a fully expanded position on release.
            drag.current
        } else {
            let mut ball = self.read_ball()?;
            ball.clamp_inside();
            self.place_ball(&ball)?;
            ball
        };
        if let Some(edge) = ball.near_edge() { self.dock_ball(ball, edge)?; }
        else { self.persist_current_placement(); }
        Ok(())
    }

    fn read_ball(&self) -> Result<Ball, String> {
        let group = self.group.as_ref().ok_or("orb unavailable")?;
        let origin = group.orb_origin()?;
        let window = group.orb_window();
        let scale = window.scale_factor().map_err(|e| e.to_string())?;
        let diameter = window.inner_size().map_err(|e| e.to_string())?.width as f64;
        let displays = monitors(&self.app);
        let monitor = super::placement::monitor_containing(&displays, origin.x + diameter as i32 / 2, origin.y + diameter as i32 / 2).ok_or("monitor unavailable")?;
        Ok(Ball { x:origin.x as f64, y:origin.y as f64, vx:0.0, vy:0.0, diameter, scale, bounds:monitor.work_area })
    }

    fn place_ball(&self, ball: &Ball) -> Result<(), String> {
        if let Some(group) = &self.group {
            group.move_orb_center(PhysicalPosition::new((ball.x + ball.diameter / 2.0).round() as i32, (ball.y + ball.diameter / 2.0).round() as i32))?;
        }
        Ok(())
    }

    fn move_drag(&mut self, dx: f64, dy: f64) -> Result<(), String> {
        if !dx.is_finite() || !dy.is_finite() { return Err("invalid drag position".into()); }
        let Some((x, y)) = self.drag_start else { return Ok(()); };
        if let Some(drag) = &mut self.edge_drag {
            let released = drag.update(dx,dy)?;
            let ball = &drag.current;
            if let Some(group) = &self.group {
                group.move_edge_frame(PixelRect {
                    x: ball.x.round() as i32, y: ball.y.round() as i32,
                    width: ball.diameter.round() as i32, height: ball.diameter.round() as i32,
                }, ball.bounds)?;
                if released { group.set_docked_clip(None)?; }
            }
            if released { self.edge_drag = None; }
            return Ok(());
        }
        let mut ball = self.ball.clone().ok_or("drag not started")?;
        if self.pulling {
            ball.pull(dx,dy)?;
        } else {
            ball.x = x + dx.clamp(-100000.0, 100000.0) * ball.scale;
            ball.y = y + dy.clamp(-100000.0, 100000.0) * ball.scale;
        }
        self.place_ball(&ball)?;
        Ok(())
    }

    fn cancel_pull(&mut self) -> Result<(), String> {
        if !self.pulling { return Ok(()); }
        if let Some(ball) = &self.ball { self.place_ball(ball)?; }
        self.pulling = false;
        self.drag_start = None;
        self.interaction = OrbInteraction::Idle;
        Ok(())
    }

    fn fling(&mut self, dx: f64, dy: f64) -> Result<(), String> {
        if !self.orb_visible() || !self.pulling { return self.cancel_pull(); }
        self.restore_docked()?;
        self.drag_start = None;
        self.interaction = OrbInteraction::Idle;
        let mut ball = self.read_ball()?;
        ball.clamp_inside();
        ball.launch(dx, dy)?;
        self.edge_slide = None;
        self.edge_drag = None;
        self.docked = false;
        if let Some(group) = &self.group { group.set_docked_clip(None)?; }
        self.pulling = false;
        self.ball = Some(ball);
        self.last_frame = Instant::now();
        self.motion_active.store(true, Ordering::Relaxed);
        Ok(())
    }

    fn motion_frame(&mut self) -> Result<(), String> {
        if !self.motion_active.load(Ordering::Relaxed) || !self.orb_visible() { return Ok(()); }
        let now = Instant::now();
        let dt = now.duration_since(self.last_frame).as_secs_f64();
        self.last_frame = now;
        if let Some(mut slide) = self.edge_slide.take() {
            let (ball, finished) = slide.step(dt);
            if let Some(group) = &self.group {
                group.move_edge_frame(PixelRect {
                    x: ball.x.round() as i32, y: ball.y.round() as i32,
                    width: ball.diameter.round() as i32, height: ball.diameter.round() as i32,
                }, ball.bounds)?;
            }
            self.ball = Some(ball);
            if finished {
                self.docked = !slide.opening;
                if let Some(group) = &self.group {
                    group.set_docked_clip(if self.docked { Some(slide.target.bounds) } else { None })?;
                }
                self.motion_active.store(false, Ordering::Relaxed);
                let placement = slide.visible_placement();
                self.persist_placement_at(PhysicalPosition::new(placement.x.round() as i32, placement.y.round() as i32));
            } else { self.edge_slide = Some(slide); }
            return Ok(());
        }
        let Some(mut ball) = self.ball.take() else { self.motion_active.store(false, Ordering::Relaxed); return Ok(()); };
        ball.step(dt);
        self.place_ball(&ball)?;
        if ball.speed() == 0.0 {
            self.motion_active.store(false, Ordering::Relaxed);
            if let Some(group) = &self.group { group.settle_motion()?; }
            self.persist_current_placement();
        }
        self.ball = Some(ball);
        Ok(())
    }

    fn dock_ball(&mut self, ball: Ball, edge: Edge) -> Result<(), String> {
        let mut target = ball.clone();
        target.dock(edge);
        self.edge_slide = Some(EdgeSlide::new(ball.clone(), target, false));
        self.docked = true;
        self.ball = Some(ball);
        self.last_frame = Instant::now();
        self.motion_active.store(true, Ordering::Relaxed);
        Ok(())
    }

    fn restore_docked(&mut self) -> Result<bool, String> {
        if !self.docked { return Ok(false); }
        if !self.edge_slide.as_ref().is_some_and(|slide| slide.opening) {
            let ball = self.ball.clone().ok_or("docked position missing")?;
            let mut target = ball.clone();
            target.expand();
            self.edge_slide = Some(EdgeSlide::new(ball, target, true));
        }
        self.last_frame = Instant::now();
        self.motion_active.store(true, Ordering::Relaxed);
        Ok(true)
    }

    fn persist_current_placement(&mut self) {
        let Some(group) = &self.group else {
            return;
        };
        let Ok(origin) = group.orb_origin() else {
            return;
        };
        self.persist_placement_at(origin);
    }

    fn persist_placement_at(&mut self, origin: PhysicalPosition<i32>) {
        // Coalesce saves and let the UI present the final animation frame first.
        self.pending_placement = Some(origin);
    }

    fn flush_pending_placement(&mut self) {
        let Some(origin) = self.pending_placement.take() else { return; };
        let prefs = self.prefs.snapshot();
        let Some(placement) = placement_from_origin(
            origin,
            prefs.diameter_dip,
            &monitors(&self.app),
            prefs.placement.edge_gap_dip,
        ) else {
            return;
        };
        if let Ok(saved) = self.prefs.set_placement(placement) {
            let _ = self.app.emit(PREFERENCES_EVENT, &saved);
        }
    }

    fn emit_all(&self) {
        let prefs = self.prefs.snapshot();
        let render = build_render_snapshot(&self.snapshot, prefs.diameter_dip);
        let _ = self.app.emit(SNAPSHOT_EVENT, &self.snapshot);
        let _ = self.app.emit(RENDER_EVENT, &render);
        let _ = self.app.emit(PREFERENCES_EVENT, &prefs);
    }
}

fn should_show_orb(enabled: bool, primary_visible: bool, ready: bool, suspended: bool) -> bool {
    enabled && !primary_visible && ready && !suspended
}

#[cfg(test)]
mod presentation_tests {
    use super::should_show_orb;

    #[test]
    fn minimize_restore_and_disabled_startup() {
        assert!(!should_show_orb(true, true, false, false));
        assert!(!should_show_orb(true, false, false, false));
        assert!(should_show_orb(true, false, true, false));
        assert!(!should_show_orb(true, true, true, false));
        assert!(!should_show_orb(false, false, true, false));
    }

    #[test]
    fn late_ready_cannot_override_reopened_main_or_fullscreen() {
        assert!(!should_show_orb(true, true, true, false));
        assert!(!should_show_orb(true, false, true, true));
        assert!(should_show_orb(true, false, true, false));
    }
}

fn usage_pulse(
    enabled: bool,
    last_date: &str,
    last_tokens: Option<&str>,
    usage: &UsageSummary,
    current: Option<OrbPulse>,
    now: i64,
) -> Option<OrbPulse> {
    if !enabled {
        return current.filter(|pulse| pulse.expires_at_ms > now);
    }
    if usage.local_date != last_date {
        return None;
    }
    let Some(next) = usage.today_tokens.as_deref() else {
        return None;
    };
    let Some(previous) = last_tokens else {
        return None;
    };
    if next == previous {
        return current.filter(|pulse| pulse.expires_at_ms > now);
    }
    let Ok(next_n) = next.parse::<u128>() else {
        return None;
    };
    let Ok(prev_n) = previous.parse::<u128>() else {
        return None;
    };
    if next_n <= prev_n {
        return None;
    }
    Some(OrbPulse {
        id: Uuid::new_v4().to_string(),
        kind: PulseKind::Usage,
        expires_at_ms: now + USAGE_PULSE_MS,
    })
}

fn quota_record(quota: AgentQuota) -> QuotaRecord {
    let agent = quota.agent_id.clone();
    QuotaRecord {
        agent_id: quota.agent_id,
        observed_at: quota.observed_at,
        plan: quota.plan,
        status: quota.status,
        windows: quota
            .windows
            .into_iter()
            .enumerate()
            .map(|(index, window)| QuotaWindowRecord {
                used_percent: window.used_percent,
                window_minutes: window.window_minutes,
                resets_at: window.resets_at,
                provider: window.provider,
                label: window.label.clone(),
                key: if agent == "codex" {
                    Some(if index == 0 { "primary".into() } else { "secondary".into() })
                } else {
                    window.label
                },
            })
            .collect(),
    }
}

fn empty_usage() -> UsageSummary {
    UsageSummary {
        local_date: chrono::Local::now().date_naive().format("%Y-%m-%d").to_string(),
        state: super::model::UsageState::Unknown,
        today_tokens: None,
        known_source_count: 0,
        has_unmeasured_sources: true,
        captured_at_ms: now_ms(),
        last_recorded_change_at_ms: None,
    }
}

fn now_ms() -> i64 {
    chrono::Utc::now().timestamp_millis()
}

fn monitors(app: &AppHandle) -> Vec<MonitorInfo> {
    let Ok(list) = app.available_monitors() else {
        return Vec::new();
    };
    let primary = app.primary_monitor().ok().flatten().and_then(|item| item.name().map(|name| name.to_string()));
    list.into_iter()
        .enumerate()
        .map(|(index, monitor)| {
            let name = monitor
                .name()
                .map(|value| value.to_string())
                .unwrap_or_else(|| format!("monitor-{index}"));
            let work = monitor.work_area();
            let position = monitor.position();
            let size = monitor.size();
            MonitorInfo {
                key: name.clone(),
                is_primary: primary.as_ref() == Some(&name),
                origin_px: (position.x, position.y),
                work_area: PixelRect {
                    x: work.position.x,
                    y: work.position.y,
                    width: work.size.width as i32,
                    height: work.size.height as i32,
                },
                bounds: PixelRect {
                    x: position.x,
                    y: position.y,
                    width: size.width as i32,
                    height: size.height as i32,
                },
                scale: monitor.scale_factor(),
            }
        })
        .collect()
}

pub fn window_is_orb(label: &str) -> bool {
    matches!(label, "orb" | "orb-effects" | "orb-details")
}
