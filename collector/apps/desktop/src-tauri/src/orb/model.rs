use serde::{Deserialize, Serialize};
use std::collections::HashSet;

pub const SCHEMA_VERSION: u32 = 1;
pub const ALLOWED_DIAMETERS: [u32; 4] = [112, 128, 144, 160];
pub const DEFAULT_DIAMETER_DIP: u32 = 112;
pub const QUOTA_FRESH_MS: i64 = 30 * 60 * 1000;
pub const INITIAL_SOURCE_ORDER: &[&str] = &[
    "codex",
    "claude-code",
    "cursor",
    "grok-build",
    "zcode",
];

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum QuotaState {
    Loading,
    Fresh,
    Stale,
    NotConnected,
    AuthRequired,
    Unavailable,
    NoQuota,
    Unlimited,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum UsageState {
    Known,
    Unknown,
    Error,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CollectorState {
    Running,
    Paused,
    Degraded,
    Stopped,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum EffectsMode {
    Orbit,
    Soft,
    Off,
}

impl Default for EffectsMode {
    fn default() -> Self {
        Self::Orbit
    }
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum PulseKind {
    Usage,
    LowQuota,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum IdentityConfidence {
    SourceVerified,
    Unavailable,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum OrbVisibility {
    Hidden,
    Creating,
    Visible,
    Suspended,
    Failed,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SuspendReason {
    Fullscreen,
    SessionLocked,
    DisplayOff,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum OrbInteraction {
    Idle,
    Dragging,
    DetailsOpen,
    MenuOpen,
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum PlacementAnchor {
    Right,
    Free,
}

impl Default for PlacementAnchor {
    fn default() -> Self {
        Self::Right
    }
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct QuotaSelection {
    pub agent_id: String,
    pub window_id: String,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbPlacement {
    #[serde(default)]
    pub monitor_key: Option<String>,
    #[serde(default)]
    pub anchor: PlacementAnchor,
    #[serde(default = "default_edge_gap")]
    pub edge_gap_dip: f64,
    #[serde(default = "default_vertical_ratio")]
    pub vertical_ratio: f64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub x_ratio: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub y_ratio: Option<f64>,
}

impl Default for OrbPlacement {
    fn default() -> Self {
        Self {
            monitor_key: None,
            anchor: PlacementAnchor::Right,
            edge_gap_dip: default_edge_gap(),
            vertical_ratio: default_vertical_ratio(),
            x_ratio: None,
            y_ratio: None,
        }
    }
}

fn default_edge_gap() -> f64 {
    16.0
}

fn default_vertical_ratio() -> f64 {
    0.72
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbPreferences {
    #[serde(default = "schema_version")]
    pub schema_version: u32,
    #[serde(default = "default_revision")]
    pub revision: String,
    #[serde(default = "default_true")]
    pub enabled: bool,
    #[serde(default = "default_diameter")]
    pub diameter_dip: u32,
    #[serde(default)]
    pub effects_mode: EffectsMode,
    #[serde(default = "default_true")]
    pub hide_on_fullscreen: bool,
    #[serde(default)]
    pub selection: Option<QuotaSelection>,
    #[serde(default)]
    pub placement: OrbPlacement,
}

impl Default for OrbPreferences {
    fn default() -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            revision: default_revision(),
            enabled: true,
            diameter_dip: DEFAULT_DIAMETER_DIP,
            effects_mode: EffectsMode::Orbit,
            hide_on_fullscreen: true,
            selection: None,
            placement: OrbPlacement::default(),
        }
    }
}

fn schema_version() -> u32 {
    SCHEMA_VERSION
}

fn default_revision() -> String {
    "0".into()
}

fn default_diameter() -> u32 {
    DEFAULT_DIAMETER_DIP
}

fn default_true() -> bool {
    true
}

impl OrbPreferences {
    pub fn sanitize(&mut self) {
        if !ALLOWED_DIAMETERS.contains(&self.diameter_dip) {
            self.diameter_dip = DEFAULT_DIAMETER_DIP;
        }
        if !self.placement.edge_gap_dip.is_finite() || self.placement.edge_gap_dip < 0.0 {
            self.placement.edge_gap_dip = default_edge_gap();
        }
        if !self.placement.vertical_ratio.is_finite() {
            self.placement.vertical_ratio = default_vertical_ratio();
        } else {
            self.placement.vertical_ratio = self.placement.vertical_ratio.clamp(0.0, 1.0);
        }
        if let Some(x) = self.placement.x_ratio {
            self.placement.x_ratio = Some(if x.is_finite() { x.clamp(0.0, 1.0) } else { 0.0 });
        }
        if let Some(y) = self.placement.y_ratio {
            self.placement.y_ratio = Some(if y.is_finite() { y.clamp(0.0, 1.0) } else { 0.0 });
        }
        if self.revision.is_empty() {
            self.revision = default_revision();
        }
        self.schema_version = SCHEMA_VERSION;
    }
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbRuntimeState {
    pub enabled: bool,
    pub visibility: OrbVisibility,
    pub suspend_reasons: Vec<SuspendReason>,
    pub interaction: OrbInteraction,
    pub effects_mode: EffectsMode,
}

impl Default for OrbRuntimeState {
    fn default() -> Self {
        Self {
            enabled: false,
            visibility: OrbVisibility::Hidden,
            suspend_reasons: Vec::new(),
            interaction: OrbInteraction::Idle,
            effects_mode: EffectsMode::Orbit,
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct QuotaRecord {
    pub agent_id: String,
    pub observed_at: String,
    pub plan: Option<String>,
    pub windows: Vec<QuotaWindowRecord>,
    pub status: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct QuotaWindowRecord {
    pub used_percent: f64,
    pub window_minutes: u64,
    pub resets_at: Option<i64>,
    pub provider: Option<String>,
    pub label: Option<String>,
    pub key: Option<String>,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UsageSummary {
    pub local_date: String,
    pub state: UsageState,
    pub today_tokens: Option<String>,
    pub known_source_count: u32,
    pub has_unmeasured_sources: bool,
    pub captured_at_ms: i64,
    pub last_recorded_change_at_ms: Option<i64>,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CollectorSnapshot {
    pub state: CollectorState,
    pub sync_state: String,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbPulse {
    pub id: String,
    pub kind: PulseKind,
    pub expires_at_ms: i64,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct EffectSnapshot {
    pub mode: EffectsMode,
    pub reduced_motion: bool,
    pub pulse: Option<OrbPulse>,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbQuotaSnapshot {
    pub selection: Option<QuotaSelection>,
    pub agent_name: Option<String>,
    pub window_label: Option<String>,
    pub state: QuotaState,
    pub remaining_percent: Option<f64>,
    pub last_known_remaining_percent: Option<f64>,
    pub observed_at_ms: Option<i64>,
    pub resets_at_ms: Option<i64>,
    pub stale_at_ms: Option<i64>,
    pub identity_confidence: IdentityConfidence,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub identity_note: Option<String>,
}

impl OrbQuotaSnapshot {
    pub fn empty(state: QuotaState) -> Self {
        Self {
            selection: None,
            agent_name: None,
            window_label: None,
            state,
            remaining_percent: None,
            last_known_remaining_percent: None,
            observed_at_ms: None,
            resets_at_ms: None,
            stale_at_ms: None,
            identity_confidence: IdentityConfidence::Unavailable,
            identity_note: None,
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbSnapshot {
    pub schema_version: u32,
    pub stream_id: String,
    pub revision: String,
    pub emitted_at_ms: i64,
    pub preferences_revision: String,
    #[serde(default)]
    pub hidden: bool,
    pub usage: UsageSummary,
    pub collector: CollectorSnapshot,
    pub quota: OrbQuotaSnapshot,
    pub effect: EffectSnapshot,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RenderQuota {
    pub state: QuotaState,
    pub remaining_percent: Option<f64>,
    pub stale_at_ms: Option<i64>,
    pub selection_key: Option<String>,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbRenderSnapshot {
    pub schema_version: u32,
    pub stream_id: String,
    pub revision: String,
    pub emitted_at_ms: i64,
    pub diameter_dip: u32,
    pub hidden: bool,
    pub collector_paused: bool,
    pub quota: RenderQuota,
    pub effect: EffectSnapshot,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DetailsWindow {
    pub window_id: String,
    pub label: Option<String>,
    pub window_minutes: u64,
    pub state: QuotaState,
    pub remaining_percent: Option<f64>,
    pub last_known_remaining_percent: Option<f64>,
    pub observed_at_ms: Option<i64>,
    pub resets_at_ms: Option<i64>,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TodaySourceTotal {
    pub agent_id: String,
    pub agent_name: String,
    pub today_tokens: Option<String>,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OrbDetailsSnapshot {
    pub schema_version: u32,
    pub stream_id: String,
    pub revision: String,
    pub emitted_at_ms: i64,
    pub selection: Option<QuotaSelection>,
    pub agent_name: Option<String>,
    pub windows: Vec<DetailsWindow>,
    pub today_sources: Vec<TodaySourceTotal>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WindowIdError {
    Incomplete { agent_id: String },
    Collision { agent_id: String, window_id: String },
}

pub fn agent_display_name(agent_id: &str) -> &str {
    match agent_id {
        "codex" => "Codex",
        "claude-code" => "Claude Code",
        "cursor" => "Cursor",
        "grok-build" => "Grok Build",
        "zcode" => "ZCode",
        "pi" => "Pi",
        "deepseek-harness" => "DeepSeek Harness",
        other => other,
    }
}

pub fn collector_state_from_daemon(status: &str, paused: bool) -> CollectorState {
    if paused || status.eq_ignore_ascii_case("PAUSED") {
        CollectorState::Paused
    } else if status.eq_ignore_ascii_case("DEGRADED") {
        CollectorState::Degraded
    } else if status.eq_ignore_ascii_case("STOPPED") {
        CollectorState::Stopped
    } else {
        CollectorState::Running
    }
}

pub fn remaining_percent(used_percent: f64) -> Option<f64> {
    if !used_percent.is_finite() {
        return None;
    }
    Some((100.0 - used_percent).clamp(0.0, 100.0))
}

pub fn quota_visual_remaining(state: QuotaState, remaining: Option<f64>) -> Option<f64> {
    if state != QuotaState::Fresh {
        return None;
    }
    remaining.filter(|value| value.is_finite())
}

pub fn observed_at_ms(observed_at: &str) -> Option<i64> {
    chrono::DateTime::parse_from_rfc3339(observed_at)
        .ok()
        .map(|time| time.timestamp_millis())
}

pub fn resets_at_ms(resets_at_secs: Option<i64>) -> Option<i64> {
    resets_at_secs.and_then(|secs| secs.checked_mul(1000))
}

pub fn stale_at_ms(observed_ms: i64, resets_at_secs: Option<i64>) -> i64 {
    let from_age = observed_ms.saturating_add(QUOTA_FRESH_MS);
    match resets_at_ms(resets_at_secs) {
        Some(reset) => from_age.min(reset),
        None => from_age,
    }
}

/// Matches `quotaStale` in usage-analytics.ts: status present and not ready,
/// invalid/future observation, older than 30 minutes, or past resetsAt.
pub fn quota_observation_stale(
    status: Option<&str>,
    observed_at_ms: Option<i64>,
    resets_at_secs: Option<i64>,
    now_ms: i64,
) -> bool {
    if status.is_some_and(|value| value != "ready") {
        return true;
    }
    let Some(observed) = observed_at_ms else {
        return true;
    };
    if observed > now_ms {
        return true;
    }
    if now_ms.saturating_sub(observed) > QUOTA_FRESH_MS {
        return true;
    }
    if let Some(reset_ms) = resets_at_ms(resets_at_secs) {
        if reset_ms <= now_ms {
            return true;
        }
    }
    false
}

pub fn terminal_quota_state(status: Option<&str>) -> Option<QuotaState> {
    match status {
        Some("not_connected") => Some(QuotaState::NotConnected),
        Some("auth_required") => Some(QuotaState::AuthRequired),
        Some("no_quota") => Some(QuotaState::NoQuota),
        Some("unlimited") => Some(QuotaState::Unlimited),
        Some("unavailable") | Some("network_error") => Some(QuotaState::Unavailable),
        Some("ready") | None => None,
        Some(_) => Some(QuotaState::Unavailable),
    }
}

pub fn window_label(window: &QuotaWindowRecord) -> String {
    if let Some(label) = &window.label {
        return label.clone();
    }
    if let Some(key) = &window.key {
        return key.clone();
    }
    if window.window_minutes == 0 {
        return "current".into();
    }
    if window.window_minutes % 1440 == 0 {
        format!("{}d", window.window_minutes / 1440)
    } else if window.window_minutes % 60 == 0 {
        format!("{}h", window.window_minutes / 60)
    } else {
        format!("{}m", window.window_minutes)
    }
}

pub fn window_id(agent_id: &str, window: &QuotaWindowRecord) -> Result<String, WindowIdError> {
    let minutes = format!("{}m", window.window_minutes);
    let incomplete = || WindowIdError::Incomplete {
        agent_id: agent_id.into(),
    };
    match agent_id {
        "codex" => {
            let slot = window
                .key
                .as_deref()
                .or(window.label.as_deref())
                .unwrap_or("primary");
            Ok(format!("codex:{slot}:{minutes}"))
        }
        "cursor" => window
            .label
            .as_deref()
            .map(|label| format!("cursor:{label}"))
            .ok_or_else(incomplete),
        "grok-build" => {
            let label = window.label.as_deref().ok_or_else(incomplete)?;
            Ok(format!("grok:{label}"))
        }
        "zcode" => {
            let provider = window.provider.as_deref().ok_or_else(incomplete)?;
            Ok(format!("zcode:{provider}:{minutes}"))
        }
        _ => {
            let mut parts = vec![agent_id.to_string()];
            if let Some(provider) = &window.provider {
                parts.push(provider.clone());
            }
            if let Some(label) = &window.label {
                parts.push(label.clone());
            }
            if let Some(key) = &window.key {
                parts.push(key.clone());
            }
            parts.push(minutes);
            Ok(parts.join(":"))
        }
    }
}

pub fn unique_window_ids(
    agent_id: &str,
    windows: &[QuotaWindowRecord],
) -> Result<Vec<String>, WindowIdError> {
    let mut ids = Vec::with_capacity(windows.len());
    let mut seen = HashSet::new();
    for window in windows {
        let id = window_id(agent_id, window)?;
        if !seen.insert(id.clone()) {
            return Err(WindowIdError::Collision {
                agent_id: agent_id.into(),
                window_id: id,
            });
        }
        ids.push(id);
    }
    Ok(ids)
}

fn source_order(agent_id: &str) -> (u8, usize, &str) {
    match INITIAL_SOURCE_ORDER
        .iter()
        .position(|item| *item == agent_id)
    {
        Some(position) => (0, position, ""),
        None => (1, 0, agent_id),
    }
}

pub fn pick_initial_selection<'a, I>(fresh_sources: I) -> Option<QuotaSelection>
where
    I: IntoIterator<Item = (&'a str, &'a [QuotaWindowRecord])>,
{
    let mut sources: Vec<_> = fresh_sources.into_iter().collect();
    sources.sort_by(|(left, _), (right, _)| source_order(left).cmp(&source_order(right)));
    for (agent_id, windows) in sources {
        if windows.is_empty() {
            continue;
        }
        if let Ok(ids) = unique_window_ids(agent_id, windows) {
            if let Some(window_id) = ids.into_iter().next() {
                return Some(QuotaSelection {
                    agent_id: agent_id.to_string(),
                    window_id,
                });
            }
        }
    }
    None
}

pub fn identity_confidence(agent_id: &str, state: QuotaState) -> IdentityConfidence {
    if agent_id == "codex" {
        return IdentityConfidence::Unavailable;
    }
    match state {
        QuotaState::NotConnected
        | QuotaState::AuthRequired
        | QuotaState::Loading
        | QuotaState::Unavailable => IdentityConfidence::Unavailable,
        _ => IdentityConfidence::SourceVerified,
    }
}

pub fn window_is_fresh(record: &QuotaRecord, window: &QuotaWindowRecord, now_ms: i64) -> bool {
    if terminal_quota_state(record.status.as_deref()).is_some() {
        return false;
    }
    remaining_percent(window.used_percent).is_some()
        && !quota_observation_stale(
            record.status.as_deref(),
            observed_at_ms(&record.observed_at),
            window.resets_at,
            now_ms,
        )
}

fn last_known_for_state(state: QuotaState, used_percent: f64) -> Option<f64> {
    match state {
        QuotaState::NoQuota | QuotaState::Unlimited | QuotaState::NotConnected | QuotaState::AuthRequired
        | QuotaState::Loading => None,
        _ => remaining_percent(used_percent),
    }
}

pub fn evaluate_window(
    record: &QuotaRecord,
    window: &QuotaWindowRecord,
    now_ms: i64,
) -> (QuotaState, Option<f64>, Option<f64>, Option<i64>, Option<i64>, Option<i64>) {
    let observed = observed_at_ms(&record.observed_at);
    let reset_ms = resets_at_ms(window.resets_at);
    let stale_at = observed.map(|ms| stale_at_ms(ms, window.resets_at));
    if let Some(state) = terminal_quota_state(record.status.as_deref()) {
        return (
            state,
            None,
            last_known_for_state(state, window.used_percent),
            observed,
            reset_ms,
            stale_at,
        );
    }
    let last_known = remaining_percent(window.used_percent);
    if last_known.is_none()
        || quota_observation_stale(record.status.as_deref(), observed, window.resets_at, now_ms)
    {
        return (
            QuotaState::Stale,
            None,
            last_known,
            observed,
            reset_ms,
            stale_at,
        );
    }
    (
        QuotaState::Fresh,
        last_known,
        last_known,
        observed,
        reset_ms,
        stale_at,
    )
}

pub fn quota_snapshot_from_record(
    record: &QuotaRecord,
    selection: &QuotaSelection,
    now_ms: i64,
) -> OrbQuotaSnapshot {
    let mut snapshot = OrbQuotaSnapshot::empty(QuotaState::Unavailable);
    snapshot.selection = Some(selection.clone());
    snapshot.agent_name = Some(agent_display_name(&selection.agent_id).to_string());
    let window = record.windows.iter().find(|item| {
        window_id(&record.agent_id, item)
            .ok()
            .is_some_and(|id| id == selection.window_id)
    });
    let Some(window) = window else {
        snapshot.identity_confidence = identity_confidence(&selection.agent_id, snapshot.state);
        return snapshot;
    };
    snapshot.window_label = Some(window_label(window));
    let (state, remaining, last_known, observed, reset_ms, stale_at) =
        evaluate_window(record, window, now_ms);
    snapshot.state = state;
    snapshot.remaining_percent = remaining;
    snapshot.last_known_remaining_percent = last_known;
    snapshot.observed_at_ms = observed;
    snapshot.resets_at_ms = reset_ms;
    snapshot.stale_at_ms = stale_at;
    snapshot.identity_confidence = identity_confidence(&selection.agent_id, state);
    if snapshot.identity_confidence == IdentityConfidence::Unavailable && selection.agent_id == "codex" {
        snapshot.identity_note = Some("来自最近本地日志".into());
    }
    snapshot
}

pub fn details_window_from_record(
    record: &QuotaRecord,
    window: &QuotaWindowRecord,
    now_ms: i64,
) -> Option<DetailsWindow> {
    let window_id = window_id(&record.agent_id, window).ok()?;
    let (state, remaining, last_known, observed, reset_ms, _) =
        evaluate_window(record, window, now_ms);
    Some(DetailsWindow {
        window_id,
        label: Some(window_label(window)),
        window_minutes: window.window_minutes,
        state,
        remaining_percent: remaining,
        last_known_remaining_percent: last_known,
        observed_at_ms: observed,
        resets_at_ms: reset_ms,
    })
}

pub fn build_orb_snapshot(
    stream_id: &str,
    revision: u128,
    emitted_at_ms: i64,
    preferences: &OrbPreferences,
    usage: UsageSummary,
    collector: CollectorSnapshot,
    quota: OrbQuotaSnapshot,
    reduced_motion: bool,
    pulse: Option<OrbPulse>,
) -> OrbSnapshot {
    OrbSnapshot {
        schema_version: SCHEMA_VERSION,
        stream_id: stream_id.into(),
        revision: revision.to_string(),
        emitted_at_ms,
        preferences_revision: preferences.revision.clone(),
        hidden: false,
        usage,
        collector,
        quota,
        effect: EffectSnapshot {
            mode: preferences.effects_mode,
            reduced_motion,
            pulse,
        },
    }
}

pub fn selection_key(selection: &Option<QuotaSelection>) -> Option<String> {
    selection
        .as_ref()
        .map(|item| format!("{}\0{}", item.agent_id, item.window_id))
}

pub fn build_render_snapshot(snapshot: &OrbSnapshot, diameter_dip: u32) -> OrbRenderSnapshot {
    OrbRenderSnapshot {
        schema_version: snapshot.schema_version,
        stream_id: snapshot.stream_id.clone(),
        revision: snapshot.revision.clone(),
        emitted_at_ms: snapshot.emitted_at_ms,
        diameter_dip,
        hidden: snapshot.hidden,
        collector_paused: matches!(
            snapshot.collector.state,
            CollectorState::Paused | CollectorState::Stopped
        ),
        quota: RenderQuota {
            state: snapshot.quota.state,
            remaining_percent: quota_visual_remaining(
                snapshot.quota.state,
                snapshot.quota.remaining_percent,
            ),
            stale_at_ms: snapshot.quota.stale_at_ms,
            selection_key: selection_key(&snapshot.quota.selection),
        },
        effect: snapshot.effect.clone(),
    }
}

pub fn build_details_snapshot(
    stream_id: &str,
    revision: u128,
    emitted_at_ms: i64,
    quota: &OrbQuotaSnapshot,
    windows: Vec<DetailsWindow>,
    today_sources: Vec<TodaySourceTotal>,
) -> OrbDetailsSnapshot {
    OrbDetailsSnapshot {
        schema_version: SCHEMA_VERSION,
        stream_id: stream_id.into(),
        revision: revision.to_string(),
        emitted_at_ms,
        selection: quota.selection.clone(),
        agent_name: quota.agent_name.clone(),
        windows,
        today_sources,
    }
}

pub fn bump_revision(revision: &str) -> String {
    revision.parse::<u128>().unwrap_or(0).saturating_add(1).to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn window(key: &str, minutes: u64, used: f64) -> QuotaWindowRecord {
        QuotaWindowRecord {
            used_percent: used,
            window_minutes: minutes,
            resets_at: None,
            provider: None,
            label: None,
            key: Some(key.into()),
        }
    }

    fn labeled(agent_label: &str, used: f64) -> QuotaWindowRecord {
        QuotaWindowRecord {
            used_percent: used,
            window_minutes: 10080,
            resets_at: None,
            provider: None,
            label: Some(agent_label.into()),
            key: None,
        }
    }

    fn rfc3339(ms: i64) -> String {
        chrono::DateTime::<chrono::Utc>::from_timestamp_millis(ms)
            .unwrap()
            .to_rfc3339()
    }

    #[test]
    fn window_id_matches_stable_spec_examples() {
        let primary = window("primary", 300, 37.0);
        assert_eq!(window_id("codex", &primary).unwrap(), "codex:primary:300m");
        let auto = labeled("auto", 31.4);
        auto_eq_cursor(&auto);
        let week = labeled("shared_week", 65.0);
        assert_eq!(window_id("grok-build", &week).unwrap(), "grok:shared_week");
        let glm = QuotaWindowRecord {
            used_percent: 3.0,
            window_minutes: 300,
            resets_at: None,
            provider: Some("GLM".into()),
            label: None,
            key: None,
        };
        assert_eq!(window_id("zcode", &glm).unwrap(), "zcode:GLM:300m");
    }

    fn auto_eq_cursor(window: &QuotaWindowRecord) {
        assert_eq!(window_id("cursor", window).unwrap(), "cursor:auto");
    }

    #[test]
    fn colliding_window_ids_refuse_auto_select() {
        let colliding = [
            window("primary", 300, 10.0),
            QuotaWindowRecord {
                used_percent: 20.0,
                window_minutes: 300,
                resets_at: None,
                provider: None,
                label: None,
                key: None,
            },
        ];
        assert!(matches!(
            unique_window_ids("codex", &colliding),
            Err(WindowIdError::Collision { .. })
        ));
        let cursor = [labeled("auto", 10.0)];
        let picked = pick_initial_selection([
            ("codex", colliding.as_slice()),
            ("cursor", cursor.as_slice()),
        ]);
        assert_eq!(
            picked,
            Some(QuotaSelection {
                agent_id: "cursor".into(),
                window_id: "cursor:auto".into(),
            })
        );
        assert!(pick_initial_selection([("codex", colliding.as_slice())]).is_none());
    }

    #[test]
    fn pick_initial_selection_uses_fixed_order() {
        let grok = [labeled("shared_week", 40.0)];
        let cursor = [labeled("api", 20.0)];
        let zcode = [QuotaWindowRecord {
            used_percent: 10.0,
            window_minutes: 300,
            resets_at: None,
            provider: Some("Z.ai".into()),
            label: None,
            key: None,
        }];
        let picked = pick_initial_selection([
            ("zcode", zcode.as_slice()),
            ("grok-build", grok.as_slice()),
            ("cursor", cursor.as_slice()),
        ])
        .unwrap();
        assert_eq!(picked.agent_id, "cursor");
        assert_eq!(picked.window_id, "cursor:api");
    }

    #[test]
    fn freshness_boundaries_match_frontend_quota_stale() {
        let now = 1_000_000_000_000i64;
        let ready = Some("ready");
        assert!(!quota_observation_stale(ready, Some(now), Some(now / 1000 + 600), now));
        assert!(quota_observation_stale(ready, Some(now), Some(now / 1000 - 1), now));
        assert!(quota_observation_stale(ready, Some(now), None, now + 31 * 60_000));
        assert!(!quota_observation_stale(ready, Some(now), None, now + 30 * 60_000));
        assert!(quota_observation_stale(ready, Some(now + 1), None, now));
        assert!(quota_observation_stale(ready, None, None, now));
        assert!(quota_observation_stale(Some("unavailable"), Some(now), None, now));
        assert!(quota_observation_stale(Some("network_error"), Some(now), None, now));
        assert!(!quota_observation_stale(None, Some(now), None, now));
        assert_eq!(stale_at_ms(now, Some(now / 1000 + 60)), now + 60_000);
        assert_eq!(stale_at_ms(now, None), now + QUOTA_FRESH_MS);
    }

    #[test]
    fn no_quota_and_unlimited_do_not_map_to_percent() {
        let now = 1_000_000_000_000i64;
        let window = window("primary", 300, 0.0);
        for status in ["no_quota", "unlimited"] {
            let record = QuotaRecord {
                agent_id: "cursor".into(),
                observed_at: rfc3339(now),
                plan: None,
                windows: vec![window.clone()],
                status: Some(status.into()),
            };
            let (state, remaining, last_known, ..) = evaluate_window(&record, &window, now);
            assert_eq!(remaining, None);
            assert_eq!(last_known, None);
            assert_eq!(quota_visual_remaining(state, remaining), None);
            if status == "no_quota" {
                assert_eq!(state, QuotaState::NoQuota);
            } else {
                assert_eq!(state, QuotaState::Unlimited);
            }
        }
    }

    #[test]
    fn quota_visual_remaining_only_when_fresh() {
        assert_eq!(
            quota_visual_remaining(QuotaState::Fresh, Some(72.5)),
            Some(72.5)
        );
        assert_eq!(quota_visual_remaining(QuotaState::Stale, Some(72.5)), None);
        assert_eq!(quota_visual_remaining(QuotaState::Fresh, None), None);
        assert_eq!(remaining_percent(37.0), Some(63.0));
        assert_eq!(remaining_percent(105.0), Some(0.0));
        assert_eq!(remaining_percent(f64::NAN), None);
    }

    #[test]
    fn snapshot_builder_is_pure_and_omits_account_fields_from_render() {
        let prefs = OrbPreferences::default();
        let usage = UsageSummary {
            local_date: "2026-09-06".into(),
            state: UsageState::Known,
            today_tokens: Some("12".into()),
            known_source_count: 1,
            has_unmeasured_sources: true,
            captured_at_ms: 1,
            last_recorded_change_at_ms: None,
        };
        let snapshot = build_orb_snapshot(
            "stream",
            7,
            2,
            &prefs,
            usage,
            CollectorSnapshot {
                state: CollectorState::Running,
                sync_state: "LOGIN_REQUIRED".into(),
            },
            OrbQuotaSnapshot {
                selection: Some(QuotaSelection {
                    agent_id: "codex".into(),
                    window_id: "codex:primary:300m".into(),
                }),
                agent_name: Some("Codex".into()),
                window_label: Some("primary".into()),
                state: QuotaState::Fresh,
                remaining_percent: Some(63.0),
                last_known_remaining_percent: Some(63.0),
                observed_at_ms: Some(1),
                resets_at_ms: None,
                stale_at_ms: Some(1 + QUOTA_FRESH_MS),
                identity_confidence: IdentityConfidence::Unavailable,
                identity_note: None,
            },
            false,
            None,
        );
        let render = build_render_snapshot(&snapshot, 112);
        assert_eq!(render.revision, "7");
        assert_eq!(render.quota.remaining_percent, Some(63.0));
        let encoded = serde_json::to_value(&render).unwrap();
        assert!(encoded.get("usage").is_none());
        assert!(encoded.get("selection").is_none());
        assert!(encoded.get("agentName").is_none());
        assert_eq!(snapshot.schema_version, 1);
    }
}
