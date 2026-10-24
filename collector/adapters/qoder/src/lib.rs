#![forbid(unsafe_code)]

//! Qoder adapter.
//!
//! Qoder (Alibaba AIDC) CLI / desktop transcripts live at
//! `~/.qoder/projects/<slug>/<session-id>.jsonl` (CN variant `~/.qoder-cn/`).
//! The shape mirrors the Claude Code transcript: one JSON line per content
//! block, `sessionId` / `uuid` / `timestamp` on every record. Usage lands once
//! per LLM response on the final assistant line (`message.stop_reason ==
//! "end_turn"`) inside `message.usage`:
//!
//! - token fields are **session-cumulative** and zeroed unless Qoder runs with
//!   `QODER_EXPOSE_TOKEN_USAGE` set (runtime gates them via env). Cumulative
//!   counters must be high-water-mark differenced per request, and
//!   `input_tokens` **includes** `cache_read_input_tokens`;
//! - `credits` is a **per-request** value (non-monotonic across records) and is
//!   the only consumption signal visible without the env switch.
//!
//! Only metric fields (timestamps, token components, credits, hashed
//! session/turn identities) are decoded; prompt text, thinking, tool inputs and
//! tool outputs never leave this module.

use std::collections::HashMap;

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    ConfigMutation, EventEnvelope, EventPayload, EventSource, NormalizedEvent, ProbeContext,
    ProbeReport, RawFrame, RollbackStep, SetupContext, SetupPlan, SourceContext, SourceKind,
    SourceSpec, TokenUsage, VerifyStep,
};
use async_trait::async_trait;
use protocol::{CostRecordedPayload, CostSource, ModelUsageRecordedPayload};
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.qoder";
pub const HISTORY_SOURCE_ID: &str = "qoder-projects";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const KNOWN_LOG: &str = include_str!("../fixtures/contract/qoder-transcript.jsonl");

/// Credits currency label (avoid clashing with future credit-based harnesses).
pub const CREDITS_CURRENCY: &str = "QODER_CREDIT";

/// Env switch that makes Qoder write real token counts instead of zeros.
pub const EXPOSE_TOKEN_USAGE_ENV: &str = "QODER_EXPOSE_TOKEN_USAGE";

const MIN_SUPPORTED_VERSION: (u64, u64, u64) = (0, 1, 0);
const MAX_SUPPORTED_VERSION_EXCLUSIVE: (u64, u64, u64) = (2, 0, 0);

/// Cumulative counters tracked per session: input (includes cache_read),
/// cache_read, cache_write, output.
type Counters = [u64; 4];

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("Qoder manifest fixture")
}

pub fn version_supported(version: &str) -> bool {
    match version_triplet(version) {
        Some(parsed) => parsed >= MIN_SUPPORTED_VERSION && parsed < MAX_SUPPORTED_VERSION_EXCLUSIVE,
        None => false,
    }
}

pub struct QoderAdapter {
    manifest: AdapterManifest,
    detected: bool,
    agent_version: Option<String>,
    hmac_key: Vec<u8>,
}

impl QoderAdapter {
    pub fn for_version(version: impl Into<String>, hmac_key: impl Into<Vec<u8>>) -> Self {
        Self {
            manifest: load_manifest(),
            detected: true,
            agent_version: Some(version.into()),
            hmac_key: hmac_key.into(),
        }
    }

    pub fn undetected(hmac_key: impl Into<Vec<u8>>) -> Self {
        Self {
            manifest: load_manifest(),
            detected: false,
            agent_version: None,
            hmac_key: hmac_key.into(),
        }
    }

    fn version_gate_open(&self) -> bool {
        self.agent_version.as_deref().is_some_and(version_supported)
    }

    fn available_capabilities(&self) -> Vec<Capability> {
        if !self.detected {
            vec![]
        } else if self.version_gate_open() {
            self.manifest.capabilities.clone()
        } else {
            // Credits and session identity survive a version drift; token
            // details are only claimed against the verified transcript shape.
            vec![Capability::Cost, Capability::Sessions]
        }
    }

    fn capability_report(&self) -> CapabilityReport {
        let available = self.available_capabilities();
        let capabilities = self
            .manifest
            .capabilities
            .iter()
            .copied()
            .map(|capability| {
                let is_available = available.contains(&capability);
                CapabilityStatus {
                    capability,
                    availability: if is_available {
                        CapabilityAvailability::Available
                    } else {
                        CapabilityAvailability::Unavailable
                    },
                    accuracy: is_available.then_some(Accuracy::Exact),
                    safe_reason_code: (!is_available)
                        .then(|| "QODER_SCHEMA_FINGERPRINT_UNSUPPORTED".into()),
                }
            })
            .collect();
        CapabilityReport {
            adapter_id: self.manifest.id.clone(),
            adapter_version: self.manifest.version.clone(),
            capabilities,
        }
    }
}

#[async_trait]
impl AgentAdapter for QoderAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        let supported = self.version_gate_open();
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: self.agent_version.clone(),
            needs_permission: false,
            needs_setup: self.detected,
            capability: self.capability_report(),
            detail: if self.detected && !supported {
                Some("unknown qoder version; verified transcript schema only".into())
            } else {
                None
            },
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        // Product decision: enabling token exposure is not a dangerous
        // permission, so the plan carries no approval gate. Credits collection
        // does not depend on it; the env switch only reveals token detail.
        Ok(SetupPlan {
            plan_id: "qoder-expose-token-usage-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Set QODER_EXPOSE_TOKEN_USAGE=true (user scope) so Qoder transcripts carry real token counts".into(),
            mutations: vec![ConfigMutation::EnvironmentSet {
                scope: "User".into(),
                key: EXPOSE_TOKEN_USAGE_ENV.into(),
                value_ref: "value://true".into(),
            }],
            required_permissions: vec![],
            verify: vec![VerifyStep {
                id: "qoder-env-readback".into(),
                summary: format!("read {EXPOSE_TOKEN_USAGE_ENV} back from the user environment"),
            }],
            rollback: vec![RollbackStep {
                id: "qoder-env-restore".into(),
                summary: "restore the prior value (or remove the variable) from the backup".into(),
            }],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        Ok(vec![SourceSpec::JsonlTail {
            id: HISTORY_SOURCE_ID.into(),
            path_template: "${USER_HOME}/.qoder/projects/**".into(),
        }])
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if !matches!(
            (frame.source_kind, frame.source_id.as_str()),
            (SourceKind::JsonlTail, HISTORY_SOURCE_ID)
        ) {
            return Err(AdapterError::decode_failed(
                "frame source kind/id does not match qoder manifest source",
            ));
        }
        decode_transcript(
            &self.manifest,
            self.agent_version.as_deref(),
            &self.hmac_key,
            &frame,
        )
    }

    async fn health(&self) -> AdapterHealth {
        if self.detected && !self.version_gate_open() {
            AdapterHealth::Degraded {
                reason: "unknown qoder version; credits/session only".into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

pub fn decode_transcript(
    manifest: &AdapterManifest,
    agent_version: Option<&str>,
    hmac_key: &[u8],
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let text = std::str::from_utf8(&frame.payload)
        .map_err(|err| AdapterError::decode_failed(err.to_string()))?;
    let mut events = Vec::new();
    let mut baselines: HashMap<String, Counters> = HashMap::new();
    for (index, raw) in text.lines().enumerate() {
        let line = raw.trim();
        if line.is_empty() {
            continue;
        }
        let Ok(value) = serde_json::from_str::<Value>(line) else {
            continue;
        };
        let Some(object) = value.as_object() else {
            continue;
        };
        if string(object, "type").as_deref() != Some("assistant") {
            continue;
        }
        let Some(session) = string(object, "sessionId").filter(|s| !s.is_empty()) else {
            continue;
        };
        let Some(occurred_at) = string(object, "timestamp").filter(|s| !s.is_empty()) else {
            continue;
        };
        // Usage appears once per response on the final assistant line.
        let Some(usage) = object
            .get("message")
            .and_then(Value::as_object)
            .and_then(|message| message.get("usage"))
            .and_then(Value::as_object)
        else {
            continue;
        };
        let request = string(usage, "request_id")
            .or_else(|| {
                value
                    .pointer("/requestTokenAnchor/requestId")
                    .and_then(Value::as_str)
                    .map(str::to_owned)
            })
            .filter(|s| !s.is_empty());
        let model = value
            .pointer("/message/model")
            .and_then(Value::as_str)
            .filter(|s| !s.is_empty())
            .unwrap_or("unknown")
            .to_owned();
        let cursor = format!("{}:{}", frame.cursor, index + 1);

        // Session-cumulative counters: diff against the high-water mark.
        let counters = [
            u64_of(usage, "input_tokens"),
            u64_of(usage, "cache_read_input_tokens"),
            u64_of(usage, "cache_creation_input_tokens"),
            u64_of(usage, "output_tokens"),
        ];
        if let Some(delta) = cumulative_delta(&mut baselines, &session, counters) {
            let (d_in, d_read, d_write, d_out) = (delta[0], delta[1], delta[2], delta[3]);
            // Qoder's input_tokens already includes cache_read (verified:
            // cr ⊂ in and context_usage_ratio == in/1e6).
            let input_context = d_in.saturating_add(d_write);
            let total = input_context.saturating_add(d_out);
            events.push(envelope(                EnvelopeInput {
                    manifest,
                    agent_version,
                    hmac_key,
                    frame,
                    cursor: &cursor,
                    raw_line: line.as_bytes(),
                    occurred_at: &occurred_at,
                    session: &session,
                    request: request.as_deref(),
                    kind: "model_usage_recorded",
                    accuracy: Accuracy::Exact,
                },
                EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                    provider_id: "qoder".into(),
                    model_id: model.clone(),
                    tokens: TokenUsage {
                        input_tokens: Some(d_in.saturating_sub(d_read).to_string()),
                        output_tokens: Some(d_out.to_string()),
                        cache_read_tokens: Some(d_read.to_string()),
                        cache_write_tokens: Some(d_write.to_string()),
                        reasoning_tokens: None,
                        tool_tokens: None,
                        total_tokens: Some(total.to_string()),
                    },
                }),
            ));
        }

        // Credits are per-request (non-monotonic): report verbatim.
        let credits = f64_of(usage, "credits");
        if credits.is_finite() && credits > 0.0 {
            let original = f64_of(usage, "original_credits");
            let discount = if original.is_finite() && original > credits {
                Some(decimal_string(original - credits))
            } else {
                None
            };
            events.push(envelope(
                EnvelopeInput {
                    manifest,
                    agent_version,
                    hmac_key,
                    frame,
                    cursor: &cursor,
                    raw_line: line.as_bytes(),
                    occurred_at: &occurred_at,
                    session: &session,
                    request: request.as_deref(),
                    kind: "cost_recorded",
                    accuracy: Accuracy::Exact,
                },
                EventPayload::CostRecorded(CostRecordedPayload {
                    amount: decimal_string(credits),
                    currency: CREDITS_CURRENCY.into(),
                    source: CostSource::ProviderReported,
                    discount_amount: discount,
                }),
            ));
        }
    }
    Ok(events)
}

/// Per-session high-water mark over the four cumulative counters.
///
/// All-zero observations (env switch off) carry no information and do not
/// touch the baseline. The first non-zero observation only establishes the
/// baseline: prior consumption is unknowable, so no request is invented. Any
/// counter decrease resets the baseline (new session generation) and skips
/// the record.
fn cumulative_delta(
    baselines: &mut HashMap<String, Counters>,
    session: &str,
    current: Counters,
) -> Option<Counters> {
    if current == [0, 0, 0, 0] {
        return None;
    }
    match baselines.get_mut(session) {
        None => {
            baselines.insert(session.to_owned(), current);
            None
        }
        Some(prev) => {
            if current.iter().zip(prev.iter()).any(|(c, p)| c < p) {
                *prev = current;
                return None;
            }
            let delta = [
                current[0] - prev[0],
                current[1] - prev[1],
                current[2] - prev[2],
                current[3] - prev[3],
            ];
            *prev = current;
            (delta != [0, 0, 0, 0]).then_some(delta)
        }
    }
}

struct EnvelopeInput<'a> {
    manifest: &'a AdapterManifest,
    agent_version: Option<&'a str>,
    hmac_key: &'a [u8],
    frame: &'a RawFrame,
    cursor: &'a str,
    raw_line: &'a [u8],
    occurred_at: &'a str,
    session: &'a str,
    request: Option<&'a str>,
    kind: &'a str,
    accuracy: Accuracy,
}

fn envelope(input: EnvelopeInput<'_>, payload: EventPayload) -> NormalizedEvent {
    let EnvelopeInput {
        manifest,
        agent_version,
        hmac_key,
        frame,
        cursor,
        raw_line,
        occurred_at,
        session,
        request,
        kind,
        accuracy,
    } = input;
    EventEnvelope {
        schema_version: "1.0".into(),
        event_id: event_id(
            hmac_key,
            &frame.installation_id,
            &manifest.id,
            &frame.source_id,
            cursor,
            kind,
            request.unwrap_or("0"),
        ),
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: agent_version.map(ToOwned::to_owned),
        installation_id: frame.installation_id.clone(),
        occurred_at: occurred_at.to_string(),
        session_hash: Some(hash(hmac_key, &[session])),
        turn_hash: request.map(|request| hash(hmac_key, &[session, request])),
        tool_call_hash: None,
        source: EventSource {
            kind: frame.source_kind,
            cursor_hmac: format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[cursor])),
            raw_fingerprint_hmac: format!(
                "hmac-sha256:{}",
                keyed_hmac(hmac_key, &[&raw_fingerprint(raw_line)])
            ),
        },
        accuracy,
        payload,
    }
}

/// `1.1.75-hotfix.1` → `(1, 1, 75)`; each part keeps its leading digits and
/// trailing suffix parts are tolerated.
fn version_triplet(version: &str) -> Option<(u64, u64, u64)> {
    let mut parts = version.trim_start_matches('v').split('.');
    let parse = |part: Option<&str>| -> Option<u64> {
        let digits = part?
            .split(|ch: char| !ch.is_ascii_digit())
            .next()
            .filter(|s| !s.is_empty())?;
        digits.parse().ok()
    };
    Some((parse(parts.next())?, parse(parts.next())?, parse(parts.next())?))
}

fn hash(hmac_key: &[u8], parts: &[&str]) -> String {
    format!("hmac-sha256:{}", keyed_hmac(hmac_key, parts))
}

fn string(object: &Map<String, Value>, key: &str) -> Option<String> {
    object.get(key).and_then(Value::as_str).map(str::to_owned)
}

fn u64_of(object: &Map<String, Value>, key: &str) -> u64 {
    object
        .get(key)
        .and_then(Value::as_u64)
        .or_else(|| string(object, key).and_then(|s| s.parse().ok()))
        .unwrap_or(0)
}

fn f64_of(object: &Map<String, Value>, key: &str) -> f64 {
    object
        .get(key)
        .and_then(Value::as_f64)
        .or_else(|| string(object, key).and_then(|s| s.parse().ok()))
        .unwrap_or(f64::NAN)
}

/// Render a float without binary noise: round to 10 decimals, trim zeros.
fn decimal_string(value: f64) -> String {
    let text = format!("{value:.10}");
    let text = text.trim_end_matches('0').trim_end_matches('.');
    text.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    const KEY: &[u8] = b"qoder-unit-hmac";

    fn frame(payload: &str) -> RawFrame {
        RawFrame {
            installation_id: "ins_00000000000000000000000000".into(),
            source_kind: SourceKind::JsonlTail,
            source_id: HISTORY_SOURCE_ID.into(),
            cursor: "offset:0".into(),
            payload: payload.as_bytes().to_vec(),
        }
    }

    fn usage_line(session: &str, request: &str, in_tok: u64, read: u64, write: u64, out: u64) -> String {
        format!(
            r#"{{"type":"assistant","uuid":"u-{request}","timestamp":"2026-10-24T10:00:00Z","sessionId":"{session}","message":{{"id":"m-{request}","role":"assistant","model":"kmodel_latest","stop_reason":"end_turn","content":[{{"type":"text","text":"redacted"}}],"usage":{{"input_tokens":{in_tok},"cache_creation_input_tokens":{write},"cache_read_input_tokens":{read},"output_tokens":{out},"credits":0.1,"request_id":"{request}"}}}}}}"#
        )
    }

    #[test]
    fn version_suffixes_parse() {
        assert_eq!(version_triplet("1.1.75-hotfix.1"), Some((1, 1, 75)));
        assert_eq!(version_triplet("1.1.75"), Some((1, 1, 75)));
        assert_eq!(version_triplet("v0.9.0"), Some((0, 9, 0)));
        assert_eq!(version_triplet("beta"), None);
    }

    #[test]
    fn cumulative_delta_baselines_first_nonzero_and_resets_on_decrease() {
        let mut baselines = HashMap::new();
        assert_eq!(
            cumulative_delta(&mut baselines, "s", [0, 0, 0, 0]),
            None,
            "zeros carry no information"
        );
        assert_eq!(cumulative_delta(&mut baselines, "s", [10, 4, 2, 1]), None);
        assert_eq!(
            cumulative_delta(&mut baselines, "s", [13, 5, 2, 3]),
            Some([3, 1, 0, 2])
        );
        assert_eq!(
            cumulative_delta(&mut baselines, "s", [13, 5, 2, 3]),
            None,
            "unchanged counters are not a request"
        );
        assert_eq!(
            cumulative_delta(&mut baselines, "s", [1, 0, 0, 0]),
            None,
            "decrease resets the baseline"
        );
        assert_eq!(
            cumulative_delta(&mut baselines, "s", [5, 2, 1, 1]),
            Some([4, 2, 1, 1]),
            "post-reset deltas are trusted against the new baseline"
        );
    }

    #[test]
    fn exposed_tokens_diff_to_per_request_values() {
        let payload = [
            usage_line("s1", "r1", 0, 0, 0, 0),
            usage_line("s1", "r2", 27215, 25728, 100, 93),
            usage_line("s1", "r3", 30000, 28000, 120, 150),
        ]
        .join("\n");
        let events = decode_transcript(&load_manifest(), Some("1.1.75"), KEY, &frame(&payload))
            .unwrap();
        let usage: Vec<_> = events
            .iter()
            .filter(|e| matches!(e.payload, EventPayload::ModelUsageRecorded(_)))
            .collect();
        assert_eq!(usage.len(), 1, "first nonzero is baseline only");
        if let EventPayload::ModelUsageRecorded(recorded) = &usage[0].payload {
            assert_eq!(recorded.tokens.total_tokens.as_deref(), Some("2862"));
            // input includes cache_read: uncached input = 2785 - 2272.
            assert_eq!(recorded.tokens.input_tokens.as_deref(), Some("513"));
            assert_eq!(recorded.tokens.cache_read_tokens.as_deref(), Some("2272"));
            assert_eq!(recorded.tokens.cache_write_tokens.as_deref(), Some("20"));
            assert_eq!(recorded.tokens.output_tokens.as_deref(), Some("57"));
        } else {
            panic!("expected usage payload");
        }
        // Three credits observations (r1 zeros still billed credits).
        let costs: Vec<_> = events
            .iter()
            .filter(|e| matches!(e.payload, EventPayload::CostRecorded(_)))
            .collect();
        assert_eq!(costs.len(), 3);
    }

    #[test]
    fn zero_tokens_emit_no_usage_event_but_keep_credits() {
        let payload = usage_line("s1", "r1", 0, 0, 0, 0);
        let events = decode_transcript(&load_manifest(), Some("1.1.75"), KEY, &frame(&payload))
            .unwrap();
        assert_eq!(events.len(), 1);
        assert!(matches!(events[0].payload, EventPayload::CostRecorded(_)));
    }

    #[test]
    fn non_positive_credits_are_dropped() {
        let payload = usage_line("s1", "r1", 0, 0, 0, 0).replace("\"credits\":0.1", "\"credits\":0");
        let events = decode_transcript(&load_manifest(), Some("1.1.75"), KEY, &frame(&payload))
            .unwrap();
        assert!(events.is_empty());
    }

    #[test]
    fn decimal_string_trims_float_noise() {
        assert_eq!(decimal_string(0.5), "0.5");
        assert_eq!(decimal_string(0.0226), "0.0226");
        assert_eq!(decimal_string(0.6 - 0.5), "0.1");
        assert_eq!(decimal_string(3.0), "3");
    }
}
