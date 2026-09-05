use protocol::{EventEnvelope, UploadAck, UploadBatch};
use std::collections::HashSet;
use wal_spool::AckPayload;

// One bounded batch per tick keeps collection and sign-out responsive.
pub fn batch(installation_id: &str, events: Vec<EventEnvelope>) -> Result<UploadBatch, String> {
    let mut selected = Vec::new();
    let mut bytes = 0;
    for event in events.into_iter().take(100) {
        let size = serde_json::to_vec(&event)
            .map_err(|_| "INVALID_EVENT")?
            .len();
        if bytes + size > 128 * 1024 {
            break;
        }
        bytes += size;
        selected.push(event);
    }
    if selected.is_empty() {
        return Err("EVENT_TOO_LARGE".into());
    }
    Ok(UploadBatch {
        batch_id: wal_spool::new_prefixed_id("bat"),
        installation_id: installation_id.into(),
        created_at: chrono::Utc::now().to_rfc3339(),
        events: selected,
    })
}

// Never infer acceptance from HTTP success or a smaller local queue.
pub fn checked_ack(batch: &UploadBatch, ack: &UploadAck) -> Result<AckPayload, String> {
    let ids: HashSet<_> = batch
        .events
        .iter()
        .map(|event| event.event_id.as_str())
        .collect();
    let rejected: HashSet<_> = ack
        .rejected
        .iter()
        .map(|item| item.event_id.as_str())
        .collect();
    if ack.batch_id != batch.batch_id
        || ids.len() != batch.events.len()
        || rejected.len() != ack.rejected.len()
        || !rejected.is_subset(&ids)
        || u64::from(ack.accepted) + u64::from(ack.duplicates) + ack.rejected.len() as u64
            != batch.events.len() as u64
        || chrono::DateTime::parse_from_rfc3339(&ack.server_time).is_err()
    {
        return Err("INVALID_ACK".into());
    }
    Ok(AckPayload {
        batch_id: ack.batch_id.clone(),
        acked_event_ids: batch
            .events
            .iter()
            .filter(|event| !rejected.contains(event.event_id.as_str()))
            .map(|event| event.event_id.clone())
            .collect(),
        server_acked_at: ack.server_time.clone(),
    })
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    pub(crate) fn event(marker: char) -> EventEnvelope {
        serde_json::from_value(serde_json::json!({
            "schemaVersion":"1.0", "eventId":marker.to_string().repeat(43),
            "adapterId":"dev.tokenshow.adapter.mock", "adapterVersion":"1.0.0", "agentId":"mock-agent",
            "installationId":format!("ins_{}", "0".repeat(26)), "occurredAt":"2026-09-05T00:00:00Z",
            "source":{"kind":"jsonl_tail", "cursorHmac":format!("hmac-sha256:{}", "A".repeat(43)), "rawFingerprintHmac":format!("hmac-sha256:{}", "A".repeat(43))},
            "accuracy":"exact", "payload":{"type":"model_usage_recorded", "providerId":"mock-provider", "modelId":"mock-model", "tokens":{"inputTokens":"10","outputTokens":"5","totalTokens":"15"}}
        })).unwrap()
    }
    pub(crate) async fn seeded_app() -> (tempfile::TempDir, crate::state::AppState) {
        let (root, app) = crate::tests::state().await;
        let events = vec![event('B'), event('C')]
            .into_iter()
            .map(|event| privacy::PrivacyFilter.filter(event).unwrap())
            .collect();
        let checkpoint = wal_spool::SourceCheckpoint {
            source_id: "sync-fixture".into(),
            path_template_id: "AGENT_CONFIG_HOME".into(),
            file_identity: "vol:fixture".into(),
            generation: 1,
            file_len: 2,
            offset: 2,
            last_record_hash: None,
            driver_checkpoint: None,
            status: protocol::SourceCheckpointStatus::Current,
        };
        app.service
            .lock()
            .await
            .wal
            .append_txn(
                wal_spool::Transaction::new("", "sync-fixture", None, checkpoint, events, ""),
                wal_spool::AppendClass::Realtime,
            )
            .unwrap();
        (root, app)
    }
    #[test]
    fn acknowledgments_must_account_for_exact_batch_and_rejections() {
        let batch = batch("ins_fixture", vec![event('B'), event('C')]).unwrap();
        let valid = UploadAck {
            batch_id: batch.batch_id.clone(),
            accepted: 1,
            duplicates: 1,
            rejected: vec![],
            server_time: "2026-09-05T00:00:00Z".into(),
        };
        assert_eq!(
            checked_ack(&batch, &valid).unwrap().acked_event_ids.len(),
            2
        );
        let mut wrong = valid.clone();
        wrong.batch_id = "different".into();
        assert!(checked_ack(&batch, &wrong).is_err());
        let mut wrong = valid.clone();
        wrong.accepted = 0;
        assert!(checked_ack(&batch, &wrong).is_err());
        let mut partial = valid.clone();
        partial.duplicates = 0;
        partial.rejected.push(protocol::RejectedEvent {
            event_id: event('C').event_id,
            error_code: protocol::RejectedEventErrorCode::InternalRetryable,
            retryable: true,
        });
        assert_eq!(
            checked_ack(&batch, &partial).unwrap().acked_event_ids,
            vec![event('B').event_id]
        );
        partial.rejected[0].event_id = event('D').event_id;
        assert!(checked_ack(&batch, &partial).is_err());
    }
}
