// Derived cost uses the same request scope and revision as its usage fact.
// A stored quote is immutable; later catalog refreshes must not change replay hashes.
fn derived_cost(
    tx: &Transaction<'_>,
    harness: &str,
    event: &EventCandidate,
    catalog: &crate::pricing::Catalog,
) -> Result<Option<EventCandidate>, PipelineError> {
    use serde_json::json;
    use sha2::{Digest, Sha256};
    if event.event_type != "model_usage_recorded" || event.model_key == 0 {
        return Ok(None);
    }
    let mut hash = Sha256::new();
    hash.update(b"tokendance:request-cost:v1");
    hash.update(event.fact_key);
    let key: [u8; 32] = hash.finalize().into();
    // Preserve the usage revision ordering while giving a refreshed price
    // catalog its own immutable quote version, including after a local rebuild.
    const PRICE_VERSION_BASE:i64=10_000_000_000;
    if !(0..PRICE_VERSION_BASE).contains(&catalog.fetched_at) { return Ok(None) }
    let revision=event.fact_revision.checked_mul(PRICE_VERSION_BASE).and_then(|v|v.checked_add(catalog.fetched_at)).ok_or_else(||PipelineError::InvalidArgument("cost revision overflow".into()))?;
    let exists: bool = tx.query_row(
        "SELECT EXISTS(SELECT 1 FROM events WHERE fact_key=?1 AND fact_revision=?2)",
        params![key.as_slice(), revision],
        |r| r.get(0),
    )?;
    if exists {
        return Ok(None);
    }
    let (provider, model): (String, String) = tx.query_row(
        "SELECT provider_id,model_id FROM model_dimensions WHERE id=?1 AND delete_at IS NULL",
        [event.model_key],
        |r| Ok((r.get(0)?, r.get(1)?)),
    )?;
    let Some(rate) = catalog.model(&model) else {
        return Ok(None);
    };
    let value: serde_json::Value = serde_json::from_str(&event.payload_json)
        .map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
    let u = &value["usage"];
    let req = crate::pricing::Request {
        agent: harness.into(),
        date: String::new(),
        model: model.clone(),
        group: String::new(),
        input: u["input_context_tokens"].as_u64(),
        output: u["output_tokens"].as_u64(),
        read: u["cache_read_tokens"].as_u64().unwrap_or(0),
        write: u["cache_write_tokens"].as_u64().unwrap_or(0),
        tokens: u["token_total"].as_u64().unwrap_or(0),
        eligible: true,
        estimate: None,
        priced_at: None,
        matched_model: None,
    };
    let Some(units) =
        crate::pricing::estimate_context(rate, &req).and_then(|n| i64::try_from(n).ok())
    else {
        return Ok(None);
    };
    let prior:Option<(i64,i64)>=tx.query_row("SELECT fact_revision,json_extract(payload_json,'$.cost.units') FROM events WHERE fact_key=?1 ORDER BY fact_revision DESC LIMIT 1",[key.as_slice()],|r|Ok((r.get(0)?,r.get(1)?))).optional()?;
    if prior.is_some_and(|(v,n)|v/PRICE_VERSION_BASE==event.fact_revision && n==units){return Ok(None)}
    let mut hash = Sha256::new();
    hash.update(b"tokendance:request-cost-event:v1");
    hash.update(key);
    hash.update(revision.to_be_bytes());
    let fact = super::runner::FactDraft {
        event_id: hash.finalize().into(),
        fact_key: key,
        fact_revision: revision,
        event_type: "cost_recorded".into(),
        schema_version: event.schema_version,
        metric_semantics_version: event.metric_semantics_version,
        occurred_at: event.occurred_at,
        time_source: value["meta"]["time_source"].as_str().and_then(super::runner::TimeSource::parse).unwrap_or(super::runner::TimeSource::SourceRecord),
        model_key: event.model_key,
        model_identity: Some((provider, model)),
        skill_id: None,
        skill_key: None,
        session_key: event.session_key,
        turn_key: event.turn_key,
        cost_scope_key: Some(event.fact_key),
        accuracy: super::runner::TokenAccuracy::Derived,
        payload_sections: json!({"cost":{"units":units,"currency":"USD","source":"estimated_price_table"}}),
    };
    Ok(Some(
        fact.into_event_candidate(harness, event.applicable_consumers.clone())
            .map_err(PipelineError::InvalidArgument)?,
    ))
}

impl PipelineStore {
    fn refresh_price_catalog(&mut self) {
        let root = self.path.parent().unwrap_or(Path::new(""));
        let stamp = std::fs::metadata(root.join("openrouter-prices.json"))
            .ok()
            .and_then(|m| m.modified().ok().map(|t| (t, m.len())));
        if stamp != self.price_stamp {
            self.price_catalog = crate::pricing::Catalog::load(root);
            self.price_stamp = stamp;
            self.maintenance_cursor = 0;
        }
    }

    /// Bounded scan on the writer's compensation tick. A restart may replay the
    /// scan: immutable costs and monotonic session extents make that idempotent.
    pub fn backfill_derived_metrics(&mut self, limit: usize) -> Result<usize, PipelineError> {
        self.refresh_price_catalog();
        let now = self.now_ms();
        let rows = {
            let mut q = self.conn.prepare("SELECT id,collection_source_id,model_key,status_json FROM events WHERE id>?1 AND delete_at IS NULL AND expire_at>?2 ORDER BY id LIMIT ?3")?;
            let rows = q
                .query_map(
                    params![self.maintenance_cursor, now, limit.min(128) as i64],
                    |r| {
                        Ok((
                            r.get::<_, i64>(0)?,
                            r.get::<_, i64>(1)?,
                            r.get::<_, i64>(2)?,
                            r.get::<_, String>(3)?,
                        ))
                    },
                )?
                .collect::<Result<Vec<_>, _>>()?;
            rows
        };

        for (id, source, model, status) in &rows {
            let wire = self.load_upload_events(&[*id])?.remove(0);
            let event = EventCandidate {
                event_id: wire.event_id,
                fact_key: wire.fact_key,
                fact_revision: wire.fact_revision,
                schema_version: wire.schema_version,
                metric_semantics_version: wire.metric_semantics_version,
                event_type: wire.event_type.clone(),
                occurred_at: wire.occurred_at,
                content_hash: wire.content_hash,
                model_key: *model,
                skill_id: None,
                session_key: wire.session_key,
                turn_key: wire.turn_key,
                cost_scope_key: wire.cost_scope_key,
                payload_json: wire.payload_json.clone(),
                applicable_consumers: vec![
                    Consumer::Hour,
                    Consumer::Day,
                    Consumer::Month,
                    Consumer::Upload,
                ],
            };
            let tx = self.conn.transaction()?;
            if let Some(cost) = derived_cost(&tx, &wire.harness_id, &event, &self.price_catalog)? {
                insert_event_with_tasks(&tx, *source, &wire.harness_id, &cost, now)?;
            }
            let status: serde_json::Value = serde_json::from_str(status)
                .map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
            let extent = super::apply::EventRow {
                id: *id,
                harness_id: wire.harness_id,
                event_type: wire.event_type,
                occurred_at: wire.occurred_at,
                model_key: *model,
                skill_id: None,
                session_key: wire.session_key.map(|v| v.to_vec()),
                turn_key: None,
                cost_scope_key: None,
                payload_json: wire.payload_json,
                metric_semantics_version: wire.metric_semantics_version,
            };
            for grain in [
                super::buckets::Grain::Hour,
                super::buckets::Grain::Day,
                super::buckets::Grain::Month,
            ] {
                if status[grain.as_str()].as_i64() == Some(3) {
                    super::apply::apply_session_extent(
                        &tx,
                        grain,
                        &extent,
                        extent.metric_semantics_version,
                        now,
                    )?;
                }
            }
            tx.commit()?;
            self.maintenance_cursor = *id;
        }
        Ok(rows.len())
    }
}

impl PipelineStore {
    /// Repair only the known pre-wire-source hash bug, in bounded writer batches.
    /// Preserve fact/event IDs, revisions, amounts and every local metric lane.
    pub fn repair_cost_upload_hashes(&mut self, limit: usize) -> Result<usize, PipelineError> {
        use base64::Engine;
        let now = self.now_ms();
        let ids = {
            let mut q = self.conn.prepare("SELECT id FROM events WHERE id>?1 AND event_type='cost_recorded' AND delete_at IS NULL AND expire_at>?2 ORDER BY id LIMIT ?3")?;
            let rows=q.query_map(params![self.cost_hash_repair_cursor,now,limit.clamp(1,512) as i64],|r|r.get::<_,i64>(0))?.collect::<Result<Vec<_>,_>>()?;
            rows
        };
        for id in &ids {
            let status:i64=self.conn.query_row("SELECT json_extract(status_json,'$.upload') FROM events WHERE id=?1",[id],|r|r.get(0))?;
            // Do not mutate a hash while an upload holds a frozen envelope.
            if status == ConsumerStatus::InFlight as i64 { return Ok(0); }
            if matches!(status,0|1|6) {
                let row=self.load_upload_events(&[*id])?.remove(0);
                let envelope=crate::upload_pipeline::encode_wire_event(&row).map_err(PipelineError::InvalidArgument)?;
                let wire=serde_json::to_value(envelope).map_err(|e|PipelineError::InvalidArgument(e.to_string()))?;
                let corrected=protocol::v2::compute_content_hash(&wire).map_err(|e|PipelineError::InvalidArgument(e.to_string()))?;
                let mut legacy=wire.clone();
                legacy["payload"]["cost"]["source"]=serde_json::json!("estimated_price_table");
                let legacy_hash=protocol::v2::compute_content_hash(&legacy).map_err(|e|PipelineError::InvalidArgument(e.to_string()))?;
                let stored=base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(row.content_hash);
                if corrected != stored && legacy_hash == stored && wire["payload"]["cost"]["source"] == "calculated_price" {
                    let hash=base64::engine::general_purpose::URL_SAFE_NO_PAD.decode(corrected).map_err(|e|PipelineError::InvalidArgument(e.to_string()))?;
                    let tx=self.conn.transaction()?;
                    let updated=tx.execute("UPDATE events SET content_hash=?1,status_json=json_set(status_json,'$.upload',0),updated_at=?2 WHERE id=?3 AND content_hash=?4 AND json_extract(status_json,'$.upload') IN (0,1,6)",params![hash,now,id,row.content_hash.as_slice()])?;
                    if updated == 1 {
                        tx.execute("INSERT INTO processing_tasks(created_at,updated_at,event_row_id,consumer,runnable_at) VALUES(?1,?1,?2,'upload',?1) ON CONFLICT(event_row_id,consumer) DO UPDATE SET updated_at=excluded.updated_at,delete_at=NULL,runnable_at=excluded.runnable_at,lease_token=NULL,lease_until=NULL,last_error_code=NULL",params![now,id])?;
                    }
                    tx.commit()?;
                }
            }
            self.cost_hash_repair_cursor=*id;
        }
        Ok(ids.len())
    }
}
