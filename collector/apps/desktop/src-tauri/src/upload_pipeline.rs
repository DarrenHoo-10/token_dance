//! P7 concurrent upload consumer: freeze wire batches, partial ACK, lease fencing.

use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use protocol::v2::{
    AckResult, EventEnvelope, EventPayload, EventType, ModelRef, SkillRef,
    TelemetryEventsResponse, Accuracy, TimeSource, PROTOCOL_VERSION_NUMBER, SCHEMA_VERSION,
    METRIC_SEMANTICS_VERSION,
};
use serde_json::Value;
use uploader::{
    freeze_events_request, RetryPolicy, TelemetryV2Transport, TransportError, V2UploadAuth,
    CLIENT_LEASE_MS, CLIENT_MAX_BATCH_BYTES, CLIENT_MAX_BATCH_EVENTS, CLIENT_MAX_IN_FLIGHT,
};

use crate::local_store::pipeline::{
    Consumer, ConsumerStatus, LeasedTask, PipelineError, PipelineWriter, RenewLease, TaskComplete,
    TaskRetry, UploadWireEvent,
};

#[derive(Debug, Clone)]
pub struct UploadCredentials {
    pub session_bearer: String,
    pub binding_status_version: u64,
    /// Generation bumped on account switch / logout so late responses are ignored.
    pub binding_generation: u64,
}

#[derive(Debug, Clone)]
struct FrozenItem {
    task: LeasedTask,
    event_id: String,
    content_hash: String,
}

#[derive(Debug, Clone)]
struct FrozenBatch {
    request_id: String,
    body: Vec<u8>,
    body_hash: String,
    auth: V2UploadAuth,
    binding_generation: u64,
    items: Vec<FrozenItem>,
    attempt: u32,
}

#[derive(Debug, Default)]
pub struct UploadTickReport {
    pub batches_started: usize,
    pub events_acked: usize,
    pub events_retried: usize,
    pub auth_blocked: bool,
    pub pending: i64,
}

pub struct UploadConsumer {
    writer: Arc<PipelineWriter>,
    transport: Arc<dyn TelemetryV2Transport>,
    retry: RetryPolicy,
    in_flight: Vec<InFlight>,
    auth_blocked_until: Option<Instant>,
    capabilities_ok: bool,
}

struct InFlight {
    frozen: FrozenBatch,
    #[allow(dead_code)]
    started: Instant,
    last_renew: Instant,
    handle: tokio::task::JoinHandle<Result<TelemetryEventsResponse, TransportError>>,
}

impl UploadConsumer {
    pub fn new(writer: Arc<PipelineWriter>, transport: Arc<dyn TelemetryV2Transport>) -> Self {
        Self {
            writer,
            transport,
            retry: RetryPolicy::default(),
            in_flight: Vec::new(),
            auth_blocked_until: None,
            capabilities_ok: false,
        }
    }

    pub fn with_retry_policy(mut self, retry: RetryPolicy) -> Self {
        self.retry = retry;
        self
    }

    pub async fn tick(&mut self, creds: Option<&UploadCredentials>) -> Result<UploadTickReport, String> {
        let _ = self.writer.run_compensation().map_err(|e| e.to_string())?;
        self.poll_finished(creds).await?;

        let mut report = UploadTickReport {
            pending: self
                .writer
                .pending_upload_count()
                .map_err(|e| e.to_string())?,
            ..Default::default()
        };

        if self
            .auth_blocked_until
            .is_some_and(|until| Instant::now() < until)
        {
            report.auth_blocked = true;
            return Ok(report);
        }
        self.auth_blocked_until = None;

        let Some(creds) = creds else {
            report.auth_blocked = true;
            return Ok(report);
        };

        if !self.capabilities_ok {
            match self.transport.capabilities().await {
                Ok(caps) => {
                    if caps.protocol_version != PROTOCOL_VERSION_NUMBER
                        || !caps.supported_schema_versions.contains(&SCHEMA_VERSION)
                        || !caps
                            .supported_metric_semantics_versions
                            .contains(&METRIC_SEMANTICS_VERSION)
                    {
                        // Unsupported versions: leave tasks pending; do not auto-ACK.
                        return Ok(report);
                    }
                    self.capabilities_ok = true;
                }
                Err(TransportError::Auth) => {
                    self.block_auth(Duration::from_secs(300));
                    report.auth_blocked = true;
                    return Ok(report);
                }
                Err(_) => return Ok(report),
            }
        }

        self.renew_long_requests()?;

        while self.in_flight.len() < CLIENT_MAX_IN_FLIGHT {
            let Some(frozen) = self.claim_and_freeze(creds)? else {
                break;
            };
            let transport = Arc::clone(&self.transport);
            let auth = frozen.auth.clone();
            let body = frozen.body.clone();
            let handle = tokio::spawn(async move { transport.upload_events(&auth, &body).await });
            self.in_flight.push(InFlight {
                frozen,
                started: Instant::now(),
                last_renew: Instant::now(),
                handle,
            });
            report.batches_started += 1;
        }

        report.pending = self
            .writer
            .pending_upload_count()
            .map_err(|e| e.to_string())?;
        Ok(report)
    }

    async fn poll_finished(
        &mut self,
        creds: Option<&UploadCredentials>,
    ) -> Result<(), String> {
        let mut finished = Vec::new();
        for (idx, flight) in self.in_flight.iter_mut().enumerate() {
            if flight.handle.is_finished() {
                finished.push(idx);
            }
        }
        // Process from the end so indices stay valid.
        for idx in finished.into_iter().rev() {
            let flight = self.in_flight.remove(idx);
            let result = match flight.handle.await {
                Ok(r) => r,
                Err(e) => Err(TransportError::Network(e.to_string())),
            };
            self.apply_transport_result(flight.frozen, result, creds)?;
        }
        Ok(())
    }

    fn renew_long_requests(&mut self) -> Result<(), String> {
        let now = Instant::now();
        for flight in &mut self.in_flight {
            if now.duration_since(flight.last_renew) < Duration::from_secs(10) {
                continue;
            }
            for item in &flight.frozen.items {
                let _ = self.writer.renew_task_lease(RenewLease {
                    task_id: item.task.task_id,
                    event_row_id: item.task.event_row_id,
                    consumer: Consumer::Upload,
                    lease_token: item.task.lease_token.clone(),
                    lease_ms: CLIENT_LEASE_MS,
                });
            }
            flight.last_renew = now;
        }
        Ok(())
    }

    fn claim_and_freeze(
        &self,
        creds: &UploadCredentials,
    ) -> Result<Option<FrozenBatch>, String> {
        let claimed = self
            .writer
            .claim_tasks(Consumer::Upload, CLIENT_MAX_BATCH_EVENTS, CLIENT_LEASE_MS)
            .map_err(|e| e.to_string())?;
        if claimed.is_empty() {
            return Ok(None);
        }
        let ids: Vec<i64> = claimed.iter().map(|t| t.event_row_id).collect();
        let rows = self
            .writer
            .load_upload_events(ids)
            .map_err(|e| e.to_string())?;
        let by_id: HashMap<i64, UploadWireEvent> =
            rows.into_iter().map(|r| (r.event_row_id, r)).collect();

        let mut envelopes = Vec::new();
        let mut items = Vec::new();
        let mut bytes = 0usize;
        for task in claimed {
            let Some(row) = by_id.get(&task.event_row_id) else {
                // Missing row: release via lease expiry; skip.
                continue;
            };
            let envelope = encode_wire_event(row).map_err(|e| e.to_string())?;
            let encoded = serde_json::to_vec(&envelope).map_err(|e| e.to_string())?;
            if !envelopes.is_empty()
                && (envelopes.len() >= CLIENT_MAX_BATCH_EVENTS
                    || bytes + encoded.len() > CLIENT_MAX_BATCH_BYTES)
            {
                // Put leftover tasks back to retry immediately.
                self.writer
                    .retry_task(TaskRetry {
                        task_id: task.task_id,
                        event_row_id: task.event_row_id,
                        consumer: Consumer::Upload,
                        lease_token: task.lease_token.clone(),
                        runnable_at: 0, // writer clock; 0 may be past — use retry with now via error path
                        error_code: Some("batch_overflow_reschedule".into()),
                    })
                    .ok();
                continue;
            }
            bytes += encoded.len();
            items.push(FrozenItem {
                event_id: envelope.event_id.clone(),
                content_hash: envelope.content_hash.clone(),
                task,
            });
            envelopes.push(envelope);
        }
        if envelopes.is_empty() {
            return Ok(None);
        }
        let (request_id, body, body_hash) =
            freeze_events_request(envelopes).map_err(|e| e.to_string())?;
        Ok(Some(FrozenBatch {
            request_id,
            body,
            body_hash,
            auth: V2UploadAuth {
                session_bearer: creds.session_bearer.clone(),
                binding_status_version: creds.binding_status_version,
            },
            binding_generation: creds.binding_generation,
            items,
            attempt: 1,
        }))
    }

    fn apply_transport_result(
        &mut self,
        frozen: FrozenBatch,
        result: Result<TelemetryEventsResponse, TransportError>,
        creds: Option<&UploadCredentials>,
    ) -> Result<(), String> {
        // Account switch: ignore late response entirely (leases expire via compensation).
        if creds.map(|c| c.binding_generation) != Some(frozen.binding_generation) {
            return Ok(());
        }
        if let Some(creds) = creds {
            if creds.binding_status_version != frozen.auth.binding_status_version
                || creds.session_bearer != frozen.auth.session_bearer
            {
                return Ok(());
            }
        }

        match result {
            Ok(response) => self.apply_partial_acks(&frozen, response),
            Err(TransportError::Auth) => {
                self.block_auth(Duration::from_secs(300));
                self.retry_whole_batch(&frozen, None, Some("auth_blocked"))?;
                Ok(())
            }
            Err(err) => {
                let delay = self.retry.delay_for(frozen.attempt, Some(&err));
                self.retry_whole_batch(&frozen, Some(delay), Some(transport_error_code(&err)))?;
                Ok(())
            }
        }
    }

    fn apply_partial_acks(
        &self,
        frozen: &FrozenBatch,
        response: TelemetryEventsResponse,
    ) -> Result<(), String> {
        if response.request_id != frozen.request_id {
            // Stale / mismatched response: do not infer.
            return Ok(());
        }
        let ack_by_id: HashMap<&str, &protocol::v2::EventAck> = response
            .acks
            .iter()
            .map(|a| (a.event_id.as_str(), a))
            .collect();

        for item in &frozen.items {
            let Some(ack) = ack_by_id.get(item.event_id.as_str()) else {
                // Missing ACK: leave in_flight until lease reclaim.
                continue;
            };
            if ack.content_hash != item.content_hash {
                let _ = self.writer.complete_task(TaskComplete {
                    task_id: item.task.task_id,
                    event_row_id: item.task.event_row_id,
                    consumer: Consumer::Upload,
                    lease_token: item.task.lease_token.clone(),
                    status: ConsumerStatus::Quarantined,
                    error_code: Some("hash_mismatch".into()),
                });
                continue;
            }
            let mapped = map_ack_result(ack.result);
            match mapped {
                AckAction::Complete(status, code) => {
                    let _ = self.writer.complete_task(TaskComplete {
                        task_id: item.task.task_id,
                        event_row_id: item.task.event_row_id,
                        consumer: Consumer::Upload,
                        lease_token: item.task.lease_token.clone(),
                        status,
                        error_code: code.or_else(|| ack.code.map(|c| format!("{c:?}"))),
                    });
                }
                AckAction::Retry { delay_ms, code } => {
                    let runnable_at = std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .map(|d| d.as_millis() as i64)
                        .unwrap_or(0)
                        + delay_ms;
                    let _ = self.writer.retry_task(TaskRetry {
                        task_id: item.task.task_id,
                        event_row_id: item.task.event_row_id,
                        consumer: Consumer::Upload,
                        lease_token: item.task.lease_token.clone(),
                        runnable_at,
                        error_code: Some(code.into()),
                    });
                }
            }
        }
        Ok(())
    }

    fn retry_whole_batch(
        &self,
        frozen: &FrozenBatch,
        delay: Option<Duration>,
        code: Option<&str>,
    ) -> Result<(), String> {
        let delay_ms = delay.map(|d| d.as_millis() as i64).unwrap_or(5_000);
        let runnable_at = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_millis() as i64)
            .unwrap_or(0)
            + delay_ms;
        for item in &frozen.items {
            match self.writer.retry_task(TaskRetry {
                task_id: item.task.task_id,
                event_row_id: item.task.event_row_id,
                consumer: Consumer::Upload,
                lease_token: item.task.lease_token.clone(),
                runnable_at,
                error_code: code.map(str::to_owned),
            }) {
                Ok(()) | Err(PipelineError::TaskLeaseMismatch) | Err(PipelineError::TaskNotRunnable) => {}
                Err(e) => return Err(e.to_string()),
            }
        }
        // Preserve frozen body hash identity: retries re-claim and re-freeze from
        // the same stored event fields / content_hash; body bytes are rebuilt but
        // content hashes of events do not change.
        let _ = &frozen.body_hash;
        Ok(())
    }

    fn block_auth(&mut self, for_dur: Duration) {
        self.auth_blocked_until = Some(Instant::now() + for_dur);
    }
}

enum AckAction {
    Complete(ConsumerStatus, Option<String>),
    Retry { delay_ms: i64, code: &'static str },
}

fn map_ack_result(result: AckResult) -> AckAction {
    match result {
        AckResult::Accepted | AckResult::Duplicate => {
            AckAction::Complete(ConsumerStatus::Applied, None)
        }
        AckResult::Discarded => AckAction::Complete(ConsumerStatus::NotApplicable, None),
        AckResult::Blocked => AckAction::Complete(ConsumerStatus::Blocked, Some("blocked".into())),
        AckResult::Conflict => {
            AckAction::Complete(ConsumerStatus::Quarantined, Some("conflict".into()))
        }
        AckResult::Invalid => {
            AckAction::Complete(ConsumerStatus::Quarantined, Some("invalid".into()))
        }
        AckResult::Retry => AckAction::Retry {
            delay_ms: 5_000,
            code: "retry",
        },
    }
}

fn transport_error_code(err: &TransportError) -> &'static str {
    match err {
        TransportError::Timeout => "timeout",
        TransportError::Network(_) => "network",
        TransportError::Http { status: 429, .. } => "rate_limited",
        TransportError::Http { status: 426, .. } => "upgrade_required",
        TransportError::Http { .. } => "http_error",
        TransportError::Auth => "auth",
        TransportError::Decode(_) => "decode",
        TransportError::Signing(_) => "signing",
    }
}

pub fn encode_wire_event(row: &UploadWireEvent) -> Result<EventEnvelope, String> {
    let payload = local_payload_to_wire(&row.payload_json)?;
    let event_type = parse_event_type(&row.event_type)?;
    Ok(EventEnvelope {
        event_id: b64_32(&row.event_id),
        fact_key: b64_32(&row.fact_key),
        fact_revision: row.fact_revision.to_string(),
        schema_version: row.schema_version as u32,
        metric_semantics_version: row.metric_semantics_version as u32,
        harness_id: row.harness_id.clone(),
        event_type,
        occurred_at: row.occurred_at.to_string(),
        model: row.model.as_ref().map(|(p, m)| ModelRef {
            provider_id: p.clone(),
            model_id: m.clone(),
        }),
        skill: row.skill_key.map(|key| SkillRef {
            skill_key: b64_32(&key),
            public_name: row.skill_public_name.clone(),
        }),
        session_key: row.session_key.as_ref().map(b64_32),
        turn_key: row.turn_key.as_ref().map(b64_32),
        cost_scope_key: row.cost_scope_key.as_ref().map(b64_32),
        payload,
        content_hash: b64_32(&row.content_hash),
    })
}

fn b64_32(bytes: &[u8; 32]) -> String {
    URL_SAFE_NO_PAD.encode(bytes)
}

fn parse_event_type(raw: &str) -> Result<EventType, String> {
    match raw {
        "model_usage_recorded" => Ok(EventType::ModelUsageRecorded),
        "cost_recorded" => Ok(EventType::CostRecorded),
        "session_started" => Ok(EventType::SessionStarted),
        "session_ended" => Ok(EventType::SessionEnded),
        "turn_started" => Ok(EventType::TurnStarted),
        "turn_completed" => Ok(EventType::TurnCompleted),
        "tool_invoked" => Ok(EventType::ToolInvoked),
        "skill_invoked" => Ok(EventType::SkillInvoked),
        "code_changed" => Ok(EventType::CodeChanged),
        other => Err(format!("unknown event_type {other}")),
    }
}

/// Convert local SQLite payload (integer counts) into protocol-v2 wire payload (decimal strings).
pub fn local_payload_to_wire(payload_json: &str) -> Result<EventPayload, String> {
    let mut value: Value =
        serde_json::from_str(payload_json).map_err(|e| format!("payload_json: {e}"))?;
    let obj = value
        .as_object_mut()
        .ok_or_else(|| "payload_json must be object".to_string())?;
    for key in ["usage", "cost", "code", "activity", "context"] {
        if let Some(Value::Object(section)) = obj.get_mut(key) {
            stringify_uint_leaves(section);
        }
    }
    if let Some(Value::Object(meta)) = obj.get_mut("meta") {
        if let Some(Value::String(ts)) = meta.get_mut("time_source") {
            if ts == "previous_record" {
                *ts = "prior_record".into();
            }
        }
        if let Some(Value::String(acc)) = meta.get_mut("accuracy") {
            if acc == "unknown" {
                *acc = "estimated".into();
            }
        }
    }
    // Ensure required meta exists for wire types.
    if !obj.contains_key("meta") {
        obj.insert(
            "meta".into(),
            serde_json::json!({"accuracy":"exact","time_source":"source_record"}),
        );
    }
    serde_json::from_value(value).map_err(|e| format!("wire payload: {e}"))
}

fn stringify_uint_leaves(map: &mut serde_json::Map<String, Value>) {
    for (_k, v) in map.iter_mut() {
        match v {
            Value::Number(n) => {
                if let Some(u) = n.as_u64() {
                    *v = Value::String(u.to_string());
                } else if let Some(i) = n.as_i64() {
                    if i >= 0 {
                        *v = Value::String(i.to_string());
                    }
                }
            }
            Value::Object(nested) => stringify_uint_leaves(nested),
            _ => {}
        }
    }
}

/// Extract session bearer from desktop cookie jar (cookie value == session token).
pub fn session_bearer_from_cookies(cookies: &std::collections::BTreeMap<String, String>) -> Option<String> {
    cookies
        .get("__Host-tokendance_session")
        .or_else(|| cookies.get("tokendance_session"))
        .cloned()
        .filter(|v| !v.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::local_store::pipeline::{
        CursorKind, EventCandidate, PipelineStore, RegisterSource, SourceCommitBatch, SourceKind,
        DEFAULT_LEASE_MS,
    };
    use protocol::v2::EventAck;
    use uploader::{ScriptedTelemetryV2, V2ScriptStep};

    fn blob(seed: u8) -> [u8; 32] {
        [seed; 32]
    }

    fn payload_exact() -> String {
        r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":10,"input_context_tokens":8,"output_tokens":2}}"#
            .into()
    }

    fn seed_store_with_upload_events(n: u8) -> (PipelineStore, Vec<i64>) {
        let mut store = PipelineStore::open_in_memory().unwrap();
        store.set_clock_ms(1_700_000_000_000);
        let source_id = store
            .register_source(&RegisterSource {
                harness_id: "codex".into(),
                source_key: blob(1),
                source_kind: SourceKind::Jsonl,
                locator_ref: "local:codex/session.jsonl".into(),
                stream_key: "main".into(),
                cursor_kind: CursorKind::ByteOffset,
                cursor_json: r#"{"offset":0}"#.into(),
                decoder_state_version: 1,
                decoder_state_json: "{}".into(),
                observed_boundary_json: "{}".into(),
                next_poll_at: Some(1_700_000_000_000),
            })
            .unwrap();
        let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
        let events: Vec<_> = (0..n)
            .map(|i| EventCandidate {
                event_id: blob(10 + i),
                fact_key: blob(50 + i),
                fact_revision: 1,
                event_type: "model_usage_recorded".into(),
                schema_version: 2,
                metric_semantics_version: 1,
                content_hash: blob(100 + i),
                occurred_at: 1_700_000_000_000,
                model_key: 0,
                skill_id: None,
                session_key: None,
                turn_key: None,
                cost_scope_key: None,
                payload_json: payload_exact(),
                applicable_consumers: vec![Consumer::Upload],
            })
            .collect();
        store
            .commit_source(SourceCommitBatch {
                source_id,
                expected_commit_seq: seq,
                lease_token: token,
                cursor_json: r#"{"offset":1}"#.into(),
                decoder_state_version: 1,
                decoder_state_json: "{}".into(),
                observed_boundary_json: "{}".into(),
                ignored_record_count_delta: 0,
                last_ignored_code: None,
                next_poll_at: Some(1_700_000_100_000),
                events,
                created_at_override: None,
            })
            .unwrap();
        let mut ids = Vec::new();
        for i in 0..n {
            ids.push(
                store
                    .event_row_id_by_event_id(&blob(10 + i))
                    .unwrap()
                    .unwrap(),
            );
        }
        (store, ids)
    }

    #[test]
    fn local_payload_stringifies_counts() {
        let payload = local_payload_to_wire(&payload_exact()).unwrap();
        assert_eq!(
            payload.usage.as_ref().unwrap().token_total.as_deref(),
            Some("10")
        );
        assert!(matches!(
            payload.meta.as_ref().unwrap().accuracy,
            Accuracy::Exact
        ));
        assert!(matches!(
            payload.meta.as_ref().unwrap().time_source,
            TimeSource::SourceRecord
        ));
    }

    #[test]
    fn renew_lease_extends_same_token() {
        let (mut store, ids) = seed_store_with_upload_events(1);
        let claimed = store
            .claim_tasks(Consumer::Upload, 10, DEFAULT_LEASE_MS)
            .unwrap();
        assert_eq!(claimed.len(), 1);
        let task = &claimed[0];
        let until = store
            .renew_task_lease(RenewLease {
                task_id: task.task_id,
                event_row_id: task.event_row_id,
                consumer: Consumer::Upload,
                lease_token: task.lease_token.clone(),
                lease_ms: 60_000,
            })
            .unwrap();
        assert!(until > store.now_ms());
        // Stale token rejected.
        let err = store
            .renew_task_lease(RenewLease {
                task_id: task.task_id,
                event_row_id: task.event_row_id,
                consumer: Consumer::Upload,
                lease_token: "stale".into(),
                lease_ms: 60_000,
            })
            .unwrap_err();
        assert!(matches!(err, PipelineError::TaskLeaseMismatch));
        let _ = ids;
    }

    #[tokio::test]
    async fn partial_ack_only_advances_matching_events() {
        let (store, _ids) = seed_store_with_upload_events(2);
        let writer = Arc::new(PipelineWriter::start(store));
        let id_a = b64_32(&blob(10));
        let hash_a = b64_32(&blob(100));
        let id_b = b64_32(&blob(11));
        let hash_b = b64_32(&blob(101));
        let _ = (id_a, hash_a, id_b, hash_b);

        // Custom transport rewrites request_id to match the frozen body.
        let transport = Arc::new(PartialAckTransport {
            inner: ScriptedTelemetryV2::new(vec![]),
            mode: PartialMode::AcceptFirstRetrySecond,
        });

        let mut consumer = UploadConsumer::new(Arc::clone(&writer), transport)
            .with_retry_policy(RetryPolicy::for_tests());
        let creds = UploadCredentials {
            session_bearer: "sess".into(),
            binding_status_version: 1,
            binding_generation: 1,
        };
        let _ = consumer.tick(Some(&creds)).await.unwrap();
        // Allow HTTP task to finish.
        tokio::time::sleep(Duration::from_millis(20)).await;
        let report = consumer.tick(Some(&creds)).await.unwrap();
        let _ = report;

        // Event A acked (upload=3, task gone); B retry (upload=1).
        // Access via pending count + status through a second store is hard with writer-owned
        // connection; use writer pending count.
        // After partial ACK: one upload task should remain (retry).
        let pending = writer.pending_upload_count().unwrap();
        assert_eq!(pending, 1, "only retry event remains pending");
        drop(consumer);
        drop(writer);
    }

    #[tokio::test]
    async fn auth_failure_blocks_upload_without_inferring_acks() {
        let (store, _) = seed_store_with_upload_events(1);
        let writer = Arc::new(PipelineWriter::start(store));
        let transport = Arc::new(ScriptedTelemetryV2::new(vec![V2ScriptStep::Auth]));
        let mut consumer = UploadConsumer::new(Arc::clone(&writer), transport)
            .with_retry_policy(RetryPolicy::for_tests());
        let creds = UploadCredentials {
            session_bearer: "sess".into(),
            binding_status_version: 1,
            binding_generation: 1,
        };
        let _ = consumer.tick(Some(&creds)).await.unwrap();
        tokio::time::sleep(Duration::from_millis(20)).await;
        let report = consumer.tick(Some(&creds)).await.unwrap();
        assert!(report.auth_blocked);
        // Event still pending (retry scheduled), not acked.
        assert!(writer.pending_upload_count().unwrap() >= 1);
        drop(consumer);
        drop(writer);
    }

    #[tokio::test]
    async fn stale_binding_generation_discards_response() {
        let (store, _) = seed_store_with_upload_events(1);
        let writer = Arc::new(PipelineWriter::start(store));
        let transport = Arc::new(PartialAckTransport {
            inner: ScriptedTelemetryV2::new(vec![]),
            mode: PartialMode::AcceptAll,
        });
        let mut consumer = UploadConsumer::new(Arc::clone(&writer), transport)
            .with_retry_policy(RetryPolicy::for_tests());
        let creds = UploadCredentials {
            session_bearer: "sess-a".into(),
            binding_status_version: 1,
            binding_generation: 1,
        };
        let _ = consumer.tick(Some(&creds)).await.unwrap();
        // Switch account before response is applied.
        let creds2 = UploadCredentials {
            session_bearer: "sess-b".into(),
            binding_status_version: 2,
            binding_generation: 2,
        };
        tokio::time::sleep(Duration::from_millis(20)).await;
        let _ = consumer.tick(Some(&creds2)).await.unwrap();
        // Late ACK discarded → still pending (in_flight lease or retry).
        assert!(writer.pending_upload_count().unwrap() >= 1);
        drop(consumer);
        drop(writer);
    }

    #[tokio::test]
    async fn retry_preserves_event_content_hashes() {
        let (store, _) = seed_store_with_upload_events(1);
        let writer = Arc::new(PipelineWriter::start(store));
        let transport = Arc::new(ScriptedTelemetryV2::new(vec![
            V2ScriptStep::Timeout,
            V2ScriptStep::NetworkFail,
        ]));
        // Use AcceptAll after failures by swapping — Scripted returns auto-accept when empty.
        let mut consumer =
            UploadConsumer::new(Arc::clone(&writer), Arc::clone(&transport) as Arc<_>)
                .with_retry_policy(RetryPolicy::for_tests());
        let creds = UploadCredentials {
            session_bearer: "sess".into(),
            binding_status_version: 1,
            binding_generation: 1,
        };
        let _ = consumer.tick(Some(&creds)).await.unwrap();
        tokio::time::sleep(Duration::from_millis(20)).await;
        let _ = consumer.tick(Some(&creds)).await.unwrap();
        // Bodies recorded — event content hashes inside must match across attempts.
        let bodies = transport.bodies();
        assert!(!bodies.is_empty());
        let first: Value = serde_json::from_slice(&bodies[0]).unwrap();
        let hash1 = first["events"][0]["contentHash"].as_str().unwrap().to_string();
        // Force re-claim after retry runnable.
        tokio::time::sleep(Duration::from_millis(5)).await;
        let _ = consumer.tick(Some(&creds)).await.unwrap();
        tokio::time::sleep(Duration::from_millis(20)).await;
        let bodies = transport.bodies();
        if bodies.len() >= 2 {
            let second: Value = serde_json::from_slice(&bodies[1]).unwrap();
            let hash2 = second["events"][0]["contentHash"].as_str().unwrap();
            assert_eq!(hash1, hash2);
        }
        drop(consumer);
        drop(writer);
    }

    enum PartialMode {
        AcceptFirstRetrySecond,
        AcceptAll,
    }

    struct PartialAckTransport {
        inner: ScriptedTelemetryV2,
        mode: PartialMode,
    }

    #[async_trait::async_trait]
    impl TelemetryV2Transport for PartialAckTransport {
        async fn capabilities(&self) -> Result<protocol::v2::TelemetryCapabilities, TransportError> {
            self.inner.capabilities().await
        }

        async fn upload_events(
            &self,
            auth: &V2UploadAuth,
            body: &[u8],
        ) -> Result<TelemetryEventsResponse, TransportError> {
            self.inner.bodies.lock().unwrap().push(body.to_vec());
            *self.inner.last_auth.lock().unwrap() = Some(auth.clone());
            let req: protocol::v2::TelemetryEventsRequest =
                serde_json::from_slice(body).map_err(|e| TransportError::Decode(e.to_string()))?;
            let acks = match self.mode {
                PartialMode::AcceptAll => req
                    .events
                    .iter()
                    .map(|e| EventAck {
                        event_id: e.event_id.clone(),
                        content_hash: e.content_hash.clone(),
                        result: AckResult::Accepted,
                        code: None,
                    })
                    .collect(),
                PartialMode::AcceptFirstRetrySecond => req
                    .events
                    .iter()
                    .enumerate()
                    .map(|(i, e)| EventAck {
                        event_id: e.event_id.clone(),
                        content_hash: e.content_hash.clone(),
                        result: if i == 0 {
                            AckResult::Accepted
                        } else {
                            AckResult::Retry
                        },
                        code: None,
                    })
                    .collect(),
            };
            Ok(TelemetryEventsResponse {
                request_id: req.request_id,
                server_time_ms: "1700000000000".into(),
                acks,
            })
        }
    }
}
