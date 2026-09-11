//! Event-pipeline v3 SQLite store: dimensions, atomic source commit, leases, TTL.

use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use rusqlite::{params, Connection, OptionalExtension, Transaction};
use uuid::Uuid;

use super::schema::{self, BUSINESS_TABLES};
use super::types::{
    Consumer, ConsumerStatus, CursorKind, EventCandidate, LeasedTask, PipelineError,
    RegisterSource, SourceCheckpointSnapshot, SourceCommitBatch, SourceCommitResult, SourceKind,
    TaskComplete, TaskRetry, MAX_BATCH_BYTES, MAX_BATCH_EVENTS, DB_FILE,
};

#[derive(Debug, Clone, Default)]
pub struct DrainStats {
    pub claimed: usize,
    pub applied: usize,
    pub failed: usize,
}

pub struct PipelineStore {
    conn: Connection,
    path: PathBuf,
    /// Injected clock for deterministic tests (UTC ms).
    clock_ms: Option<i64>,
}

impl PipelineStore {
    pub fn open(dir: &Path) -> Result<Self, PipelineError> {
        std::fs::create_dir_all(dir).map_err(|e| PipelineError::Sqlite(e.to_string()))?;
        let path = dir.join(DB_FILE);
        let conn = Connection::open(&path)?;
        schema::configure_connection(&conn)?;
        let mut store = Self {
            conn,
            path,
            clock_ms: None,
        };
        store.ensure_initialized()?;
        Ok(store)
    }

    pub fn open_in_memory() -> Result<Self, PipelineError> {
        let conn = Connection::open_in_memory()?;
        schema::configure_connection(&conn)?;
        let mut store = Self {
            conn,
            path: PathBuf::from(":memory:"),
            clock_ms: None,
        };
        store.ensure_initialized()?;
        Ok(store)
    }

    pub fn set_clock_ms(&mut self, now_ms: i64) {
        self.clock_ms = Some(now_ms);
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn now_ms(&self) -> i64 {
        self.clock_ms.unwrap_or_else(system_now_ms)
    }

    pub fn is_ready(&self) -> Result<bool, PipelineError> {
        schema::is_pipeline_ready(&self.conn)
    }

    pub fn ensure_initialized(&mut self) -> Result<bool, PipelineError> {
        let now = self.now_ms();
        schema::initialize_empty(&mut self.conn, now)
    }

    pub fn business_table_count(&self) -> Result<usize, PipelineError> {
        let mut n = 0usize;
        for table in BUSINESS_TABLES {
            let exists: Option<i64> = self
                .conn
                .query_row(
                    "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?1",
                    params![table],
                    |row| row.get(0),
                )
                .optional()?;
            if exists.is_some() {
                n += 1;
            }
        }
        Ok(n)
    }

    // ── dimensions ──────────────────────────────────────────────────────────

    pub fn upsert_model(&mut self, provider_id: &str, model_id: &str) -> Result<i64, PipelineError> {
        schema::assert_ready(&self.conn)?;
        if provider_id.is_empty() || model_id.is_empty() {
            return Err(PipelineError::InvalidArgument(
                "provider_id/model_id required".into(),
            ));
        }
        let now = self.now_ms();
        let tx = self.conn.transaction()?;
        let existing: Option<i64> = tx
            .query_row(
                "SELECT id FROM model_dimensions WHERE provider_id=?1 AND model_id=?2",
                params![provider_id, model_id],
                |row| row.get(0),
            )
            .optional()?;
        if let Some(id) = existing {
            tx.commit()?;
            return Ok(id);
        }
        tx.execute(
            "INSERT INTO model_dimensions (created_at, updated_at, provider_id, model_id)
             VALUES (?1, ?1, ?2, ?3)",
            params![now, provider_id, model_id],
        )?;
        let id = tx.last_insert_rowid();
        tx.commit()?;
        Ok(id)
    }

    /// Register a skill identity once. Subsequent calls with the same skill_key
    /// return the existing id and do not mutate public_name.
    pub fn register_skill(
        &mut self,
        skill_key: &[u8; 32],
        public_name: Option<&str>,
    ) -> Result<i64, PipelineError> {
        schema::assert_ready(&self.conn)?;
        let now = self.now_ms();
        let tx = self.conn.transaction()?;
        let existing: Option<(i64, Option<String>)> = tx
            .query_row(
                "SELECT id, public_name FROM skill_dimensions WHERE skill_key=?1",
                params![skill_key.as_slice()],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .optional()?;
        if let Some((id, _)) = existing {
            tx.commit()?;
            return Ok(id);
        }
        tx.execute(
            "INSERT INTO skill_dimensions (created_at, updated_at, skill_key, public_name)
             VALUES (?1, ?1, ?2, ?3)",
            params![now, skill_key.as_slice(), public_name],
        )?;
        let id = tx.last_insert_rowid();
        tx.commit()?;
        Ok(id)
    }

    // ── sources ─────────────────────────────────────────────────────────────

    pub fn register_source(&mut self, src: &RegisterSource) -> Result<i64, PipelineError> {
        schema::assert_ready(&self.conn)?;
        validate_json_object(&src.cursor_json, "cursor_json")?;
        validate_json_object(&src.decoder_state_json, "decoder_state_json")?;
        validate_json_object(&src.observed_boundary_json, "observed_boundary_json")?;
        if src.decoder_state_version <= 0 {
            return Err(PipelineError::InvalidArgument(
                "decoder_state_version must be > 0".into(),
            ));
        }
        let now = self.now_ms();
        let tx = self.conn.transaction()?;
        let existing: Option<i64> = tx
            .query_row(
                "SELECT id FROM collection_sources
                 WHERE harness_id=?1 AND source_key=?2 AND stream_key=?3",
                params![
                    src.harness_id,
                    src.source_key.as_slice(),
                    src.stream_key
                ],
                |row| row.get(0),
            )
            .optional()?;
        if let Some(id) = existing {
            tx.commit()?;
            return Ok(id);
        }
        tx.execute(
            "INSERT INTO collection_sources (
                created_at, updated_at, harness_id, source_key, source_kind, locator_ref,
                stream_key, cursor_kind, cursor_json, decoder_state_version, decoder_state_json,
                observed_boundary_json, next_poll_at
             ) VALUES (?1, ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12)",
            params![
                now,
                src.harness_id,
                src.source_key.as_slice(),
                src.source_kind.as_str(),
                src.locator_ref,
                src.stream_key,
                src.cursor_kind.as_str(),
                src.cursor_json,
                src.decoder_state_version,
                src.decoder_state_json,
                src.observed_boundary_json,
                src.next_poll_at,
            ],
        )?;
        let id = tx.last_insert_rowid();
        tx.commit()?;
        Ok(id)
    }

    /// Acquire a collection-source lease (CAS). Clears next_poll_at while leased.
    pub fn lease_source(
        &mut self,
        source_id: i64,
        lease_ms: i64,
    ) -> Result<(String, i64, i64), PipelineError> {
        schema::assert_ready(&self.conn)?;
        let now = self.now_ms();
        let lease_until = now + lease_ms.max(1);
        let token = new_lease_token();
        let tx = self.conn.transaction()?;
        let row: Option<(Option<String>, Option<i64>, i64, i64, Option<i64>)> = tx
            .query_row(
                "SELECT lease_token, lease_until, commit_seq, enabled, delete_at
                 FROM collection_sources WHERE id=?1",
                params![source_id],
                |row| {
                    Ok((
                        row.get(0)?,
                        row.get(1)?,
                        row.get(2)?,
                        row.get(3)?,
                        row.get(4)?,
                    ))
                },
            )
            .optional()?;
        let Some((lease_token, lease_until_existing, commit_seq, enabled, delete_at)) = row else {
            return Err(PipelineError::SourceNotFound(source_id));
        };
        if delete_at.is_some() || enabled == 0 {
            return Err(PipelineError::SourceDisabledOrDeleted);
        }
        if let (Some(_), Some(until)) = (lease_token, lease_until_existing) {
            if until > now {
                return Err(PipelineError::SourceLeaseMismatch);
            }
        }
        tx.execute(
            "UPDATE collection_sources
             SET lease_token=?1, lease_until=?2, next_poll_at=NULL, updated_at=?3
             WHERE id=?4",
            params![token, lease_until, now, source_id],
        )?;
        tx.commit()?;
        Ok((token, lease_until, commit_seq))
    }

    pub fn source_commit_seq(&self, source_id: i64) -> Result<i64, PipelineError> {
        schema::assert_ready(&self.conn)?;
        self.conn
            .query_row(
                "SELECT commit_seq FROM collection_sources WHERE id=?1",
                params![source_id],
                |row| row.get(0),
            )
            .optional()?
            .ok_or(PipelineError::SourceNotFound(source_id))
    }

    /// Load a durable checkpoint snapshot for out-of-transaction read/decode.
    pub fn load_source_checkpoint(
        &self,
        source_id: i64,
    ) -> Result<SourceCheckpointSnapshot, PipelineError> {
        schema::assert_ready(&self.conn)?;
        let row = self
            .conn
            .query_row(
                "SELECT harness_id, source_kind, locator_ref, stream_key, cursor_kind,
                        cursor_json, decoder_state_version, decoder_state_json,
                        observed_boundary_json, commit_seq, lease_token, lease_until,
                        ignored_record_count, last_ignored_code, next_poll_at, enabled
                 FROM collection_sources WHERE id=?1 AND delete_at IS NULL",
                params![source_id],
                |row| {
                    Ok((
                        row.get::<_, String>(0)?,
                        row.get::<_, String>(1)?,
                        row.get::<_, String>(2)?,
                        row.get::<_, String>(3)?,
                        row.get::<_, String>(4)?,
                        row.get::<_, String>(5)?,
                        row.get::<_, i64>(6)?,
                        row.get::<_, String>(7)?,
                        row.get::<_, String>(8)?,
                        row.get::<_, i64>(9)?,
                        row.get::<_, Option<String>>(10)?,
                        row.get::<_, Option<i64>>(11)?,
                        row.get::<_, i64>(12)?,
                        row.get::<_, Option<String>>(13)?,
                        row.get::<_, Option<i64>>(14)?,
                        row.get::<_, i64>(15)?,
                    ))
                },
            )
            .optional()?
            .ok_or(PipelineError::SourceNotFound(source_id))?;
        Ok(SourceCheckpointSnapshot {
            source_id,
            harness_id: row.0,
            source_kind: parse_source_kind(&row.1)?,
            locator_ref: row.2,
            stream_key: row.3,
            cursor_kind: parse_cursor_kind(&row.4)?,
            cursor_json: row.5,
            decoder_state_version: row.6,
            decoder_state_json: row.7,
            observed_boundary_json: row.8,
            commit_seq: row.9,
            lease_token: row.10,
            lease_until: row.11,
            ignored_record_count: row.12,
            last_ignored_code: row.13,
            next_poll_at: row.14,
            enabled: row.15 != 0,
        })
    }

    /// List due enabled sources for fair scheduling (no lease held).
    pub fn list_due_sources(&self, now_ms: i64, limit: usize) -> Result<Vec<i64>, PipelineError> {
        schema::assert_ready(&self.conn)?;
        let limit = limit.clamp(1, 256);
        let mut stmt = self.conn.prepare(
            "SELECT id FROM collection_sources
             WHERE delete_at IS NULL AND enabled = 1
               AND (next_poll_at IS NULL OR next_poll_at <= ?1)
               AND (lease_token IS NULL OR lease_until IS NULL OR lease_until <= ?1)
             ORDER BY COALESCE(next_poll_at, 0) ASC, id ASC
             LIMIT ?2",
        )?;
        let rows = stmt
            .query_map(params![now_ms, limit as i64], |row| row.get::<_, i64>(0))?
            .collect::<Result<Vec<_>, _>>()?;
        Ok(rows)
    }

    // ── atomic source commit ────────────────────────────────────────────────

    pub fn commit_source(
        &mut self,
        batch: SourceCommitBatch,
    ) -> Result<SourceCommitResult, PipelineError> {
        schema::assert_ready(&self.conn)?;
        validate_batch_limits(&batch)?;
        validate_json_object(&batch.cursor_json, "cursor_json")?;
        validate_json_object(&batch.decoder_state_json, "decoder_state_json")?;
        validate_json_object(&batch.observed_boundary_json, "observed_boundary_json")?;
        if batch.decoder_state_version <= 0 {
            return Err(PipelineError::InvalidArgument(
                "decoder_state_version must be > 0".into(),
            ));
        }
        for event in &batch.events {
            validate_event_candidate(event)?;
        }

        let now = batch.created_at_override.unwrap_or_else(|| self.now_ms());
        let tx = self.conn.transaction()?;

        let source = load_source_for_commit(&tx, batch.source_id)?;
        if source.delete_at.is_some() || source.enabled == 0 {
            return Err(PipelineError::SourceDisabledOrDeleted);
        }
        match &source.lease_token {
            Some(token) if token == &batch.lease_token => {}
            _ => return Err(PipelineError::SourceLeaseMismatch),
        }
        if source.commit_seq != batch.expected_commit_seq {
            return Err(PipelineError::SourceCommitSeqMismatch {
                expected: batch.expected_commit_seq,
                actual: source.commit_seq,
            });
        }

        let mut inserted = 0usize;
        let mut duplicates = 0usize;
        for event in &batch.events {
            match insert_event_with_tasks(&tx, batch.source_id, &source.harness_id, event, now)? {
                InsertOutcome::Inserted => inserted += 1,
                InsertOutcome::Duplicate => duplicates += 1,
            }
        }

        let new_seq = source.commit_seq + 1;
        let ignored = source.ignored_record_count + batch.ignored_record_count_delta.max(0);
        tx.execute(
            "UPDATE collection_sources SET
                cursor_json=?1,
                decoder_state_version=?2,
                decoder_state_json=?3,
                observed_boundary_json=?4,
                ignored_record_count=?5,
                last_ignored_code=?6,
                commit_seq=?7,
                lease_token=NULL,
                lease_until=NULL,
                next_poll_at=?8,
                updated_at=?9
             WHERE id=?10",
            params![
                batch.cursor_json,
                batch.decoder_state_version,
                batch.decoder_state_json,
                batch.observed_boundary_json,
                ignored,
                batch.last_ignored_code,
                new_seq,
                batch.next_poll_at,
                now,
                batch.source_id,
            ],
        )?;
        tx.commit()?;
        Ok(SourceCommitResult {
            commit_seq: new_seq,
            inserted_events: inserted,
            duplicate_events: duplicates,
        })
    }

    // ── task lease / complete ───────────────────────────────────────────────

    pub fn claim_tasks(
        &mut self,
        consumer: Consumer,
        limit: usize,
        lease_ms: i64,
    ) -> Result<Vec<LeasedTask>, PipelineError> {
        schema::assert_ready(&self.conn)?;
        let limit = limit.clamp(1, 256);
        let now = self.now_ms();
        let lease_until = now + lease_ms.max(1);
        let tx = self.conn.transaction()?;

        let mut stmt = tx.prepare(
            "SELECT t.id, t.event_row_id, t.attempt_count
             FROM processing_tasks t
             JOIN events e ON e.id = t.event_row_id
             WHERE t.consumer = ?1
               AND t.delete_at IS NULL
               AND t.runnable_at IS NOT NULL
               AND t.runnable_at <= ?2
               AND e.delete_at IS NULL
               AND e.expire_at > ?2
               AND CAST(json_extract(e.status_json, ?3) AS INTEGER) IN (0, 1)
             ORDER BY t.runnable_at ASC, t.event_row_id ASC
             LIMIT ?4",
        )?;
        let rows = stmt
            .query_map(
                params![consumer.as_str(), now, consumer.status_path(), limit as i64],
                |row| {
                    Ok((
                        row.get::<_, i64>(0)?,
                        row.get::<_, i64>(1)?,
                        row.get::<_, i64>(2)?,
                    ))
                },
            )?
            .collect::<Result<Vec<_>, _>>()?;
        drop(stmt);

        let mut leased = Vec::with_capacity(rows.len());
        for (task_id, event_row_id, attempt_count) in rows {
            let token = new_lease_token();
            let updated = tx.execute(
                "UPDATE processing_tasks
                 SET lease_token=?1, lease_until=?2, runnable_at=NULL,
                     attempt_count=attempt_count+1, updated_at=?3
                 WHERE id=?4
                   AND delete_at IS NULL
                   AND runnable_at IS NOT NULL
                   AND lease_token IS NULL",
                params![token, lease_until, now, task_id],
            )?;
            if updated != 1 {
                continue;
            }
            tx.execute(
                "UPDATE events
                 SET status_json=json_set(status_json, ?1, ?2), updated_at=?3
                 WHERE id=?4 AND delete_at IS NULL AND expire_at>?3",
                params![
                    consumer.status_path(),
                    ConsumerStatus::InFlight as i64,
                    now,
                    event_row_id
                ],
            )?;
            leased.push(LeasedTask {
                task_id,
                event_row_id,
                consumer,
                lease_token: token,
                lease_until,
                attempt_count: attempt_count + 1,
            });
        }
        tx.commit()?;
        Ok(leased)
    }

    pub fn complete_task(&mut self, complete: TaskComplete) -> Result<(), PipelineError> {
        schema::assert_ready(&self.conn)?;
        match complete.status {
            ConsumerStatus::Applied
            | ConsumerStatus::NotApplicable
            | ConsumerStatus::Blocked
            | ConsumerStatus::Quarantined => {}
            other => {
                return Err(PipelineError::InvalidArgument(format!(
                    "complete_task status {:?} not terminal",
                    other as u8
                )));
            }
        }
        let now = self.now_ms();
        let tx = self.conn.transaction()?;
        verify_task_lease(
            &tx,
            complete.task_id,
            complete.event_row_id,
            complete.consumer,
            &complete.lease_token,
            now,
        )?;
        tx.execute(
            "UPDATE events
             SET status_json=json_set(status_json, ?1, ?2), updated_at=?3
             WHERE id=?4 AND delete_at IS NULL AND expire_at>?3",
            params![
                complete.consumer.status_path(),
                complete.status as i64,
                now,
                complete.event_row_id
            ],
        )?;
        if let Some(code) = &complete.error_code {
            tx.execute(
                "UPDATE processing_tasks SET last_error_code=?1, updated_at=?2 WHERE id=?3",
                params![code, now, complete.task_id],
            )?;
        }
        // Success / terminal: delete the scheduling row (status lives on events).
        tx.execute(
            "DELETE FROM processing_tasks WHERE id=?1",
            params![complete.task_id],
        )?;
        tx.commit()?;
        Ok(())
    }

    /// Apply hour/day/month metrics and mark the lane applied in one transaction.
    /// Upload must use `complete_task` (P7).
    pub fn apply_and_complete_metrics(
        &mut self,
        task: &LeasedTask,
    ) -> Result<(), PipelineError> {
        schema::assert_ready(&self.conn)?;
        if matches!(task.consumer, Consumer::Upload) {
            return Err(PipelineError::InvalidArgument(
                "apply_and_complete_metrics does not handle upload".into(),
            ));
        }
        let now = self.now_ms();
        let tx = self.conn.transaction()?;
        super::apply::apply_and_complete_in_tx(
            &tx,
            task.task_id,
            task.event_row_id,
            task.consumer,
            &task.lease_token,
            now,
        )?;
        tx.commit()?;
        Ok(())
    }

    /// Claim due tasks for a metrics consumer and apply each in its own txn.
    pub fn drain_metrics_consumer(
        &mut self,
        consumer: Consumer,
        limit: usize,
        lease_ms: i64,
    ) -> Result<DrainStats, PipelineError> {
        if matches!(consumer, Consumer::Upload) {
            return Err(PipelineError::InvalidArgument(
                "drain_metrics_consumer does not handle upload".into(),
            ));
        }
        let leased = self.claim_tasks(consumer, limit, lease_ms)?;
        let mut stats = DrainStats {
            claimed: leased.len(),
            applied: 0,
            failed: 0,
        };
        for task in leased {
            match self.apply_and_complete_metrics(&task) {
                Ok(()) => stats.applied += 1,
                Err(_) => {
                    stats.failed += 1;
                    let _ = self.retry_task(TaskRetry {
                        task_id: task.task_id,
                        event_row_id: task.event_row_id,
                        consumer: task.consumer,
                        lease_token: task.lease_token.clone(),
                        runnable_at: self.now_ms() + 5_000,
                        error_code: Some("metrics_apply_failed".into()),
                    });
                }
            }
        }
        Ok(stats)
    }

    pub fn retry_task(&mut self, retry: TaskRetry) -> Result<(), PipelineError> {
        schema::assert_ready(&self.conn)?;
        let now = self.now_ms();
        let tx = self.conn.transaction()?;
        verify_task_lease(
            &tx,
            retry.task_id,
            retry.event_row_id,
            retry.consumer,
            &retry.lease_token,
            now,
        )?;
        tx.execute(
            "UPDATE events
             SET status_json=json_set(status_json, ?1, ?2), updated_at=?3
             WHERE id=?4 AND delete_at IS NULL AND expire_at>?3",
            params![
                retry.consumer.status_path(),
                ConsumerStatus::Retry as i64,
                now,
                retry.event_row_id
            ],
        )?;
        tx.execute(
            "UPDATE processing_tasks
             SET lease_token=NULL, lease_until=NULL, runnable_at=?1,
                 last_error_code=?2, updated_at=?3
             WHERE id=?4",
            params![
                retry.runnable_at,
                retry.error_code,
                now,
                retry.task_id
            ],
        )?;
        tx.commit()?;
        Ok(())
    }

    /// Reclaim task leases whose lease_until has passed (crash / timeout recovery).
    pub fn reclaim_expired_leases(&mut self) -> Result<usize, PipelineError> {
        schema::assert_ready(&self.conn)?;
        let now = self.now_ms();
        let tx = self.conn.transaction()?;
        let mut stmt = tx.prepare(
            "SELECT id, event_row_id, consumer FROM processing_tasks
             WHERE lease_until IS NOT NULL AND lease_until <= ?1",
        )?;
        let rows = stmt
            .query_map(params![now], |row| {
                Ok((
                    row.get::<_, i64>(0)?,
                    row.get::<_, i64>(1)?,
                    row.get::<_, String>(2)?,
                ))
            })?
            .collect::<Result<Vec<_>, _>>()?;
        drop(stmt);

        let mut n = 0usize;
        for (task_id, event_row_id, consumer_raw) in rows {
            let consumer =
                Consumer::parse(&consumer_raw).map_err(PipelineError::InvalidArgument)?;
            tx.execute(
                "UPDATE processing_tasks
                 SET lease_token=NULL, lease_until=NULL, runnable_at=?1, updated_at=?1
                 WHERE id=?2",
                params![now, task_id],
            )?;
            // Only roll status back when still in_flight.
            let status: i64 = tx.query_row(
                "SELECT CAST(json_extract(status_json, ?1) AS INTEGER) FROM events WHERE id=?2",
                params![consumer.status_path(), event_row_id],
                |row| row.get(0),
            )?;
            if status == ConsumerStatus::InFlight as i64 {
                tx.execute(
                    "UPDATE events
                     SET status_json=json_set(status_json, ?1, ?2), updated_at=?3
                     WHERE id=?4",
                    params![
                        consumer.status_path(),
                        ConsumerStatus::Retry as i64,
                        now,
                        event_row_id
                    ],
                )?;
            }
            n += 1;
        }
        tx.commit()?;
        Ok(n)
    }

    // ── TTL ─────────────────────────────────────────────────────────────────

    /// Physically delete events with expire_at <= now. Cascades tasks.
    /// Soft-delete / updated_at do not extend TTL. Incomplete expired events
    /// increment collection_sources.expired_incomplete_event_count once.
    /// Related in-scope pending work is blocked with dependency_expired.
    pub fn expire_due_events(&mut self, limit: usize) -> Result<usize, PipelineError> {
        schema::assert_ready(&self.conn)?;
        let limit = limit.clamp(1, 512);
        let now = self.now_ms();
        let tx = self.conn.transaction()?;

        let mut stmt = tx.prepare(
            "SELECT id, collection_source_id, harness_id, session_key, cost_scope_key, status_json
             FROM events
             WHERE expire_at <= ?1
             ORDER BY expire_at ASC, id ASC
             LIMIT ?2",
        )?;
        let rows = stmt
            .query_map(params![now, limit as i64], |row| {
                Ok(ExpireRow {
                    id: row.get(0)?,
                    collection_source_id: row.get(1)?,
                    harness_id: row.get(2)?,
                    session_key: row.get(3)?,
                    cost_scope_key: row.get(4)?,
                    status_json: row.get(5)?,
                })
            })?
            .collect::<Result<Vec<_>, _>>()?;
        drop(stmt);

        let mut deleted = 0usize;
        for row in rows {
            let incomplete = event_has_incomplete_work(&row.status_json)?;
            block_dependent_tasks(&tx, &row, now)?;
            if incomplete {
                tx.execute(
                    "UPDATE collection_sources
                     SET expired_incomplete_event_count = expired_incomplete_event_count + 1,
                         updated_at=?1
                     WHERE id=?2",
                    params![now, row.collection_source_id],
                )?;
            }
            // CASCADE deletes processing_tasks.
            tx.execute("DELETE FROM events WHERE id=?1", params![row.id])?;
            deleted += 1;
        }
        tx.commit()?;
        Ok(deleted)
    }

    // ── test / inspection helpers ───────────────────────────────────────────

    pub fn event_count(&self) -> Result<i64, PipelineError> {
        Ok(self
            .conn
            .query_row("SELECT COUNT(*) FROM events", [], |r| r.get(0))?)
    }

    pub fn task_count(&self) -> Result<i64, PipelineError> {
        Ok(self
            .conn
            .query_row("SELECT COUNT(*) FROM processing_tasks", [], |r| r.get(0))?)
    }

    pub fn event_status_json(&self, event_row_id: i64) -> Result<String, PipelineError> {
        Ok(self.conn.query_row(
            "SELECT status_json FROM events WHERE id=?1",
            params![event_row_id],
            |r| r.get(0),
        )?)
    }

    pub fn event_row_id_by_event_id(&self, event_id: &[u8; 32]) -> Result<Option<i64>, PipelineError> {
        Ok(self
            .conn
            .query_row(
                "SELECT id FROM events WHERE event_id=?1",
                params![event_id.as_slice()],
                |r| r.get(0),
            )
            .optional()?)
    }

    pub fn source_cursor_json(&self, source_id: i64) -> Result<String, PipelineError> {
        Ok(self.conn.query_row(
            "SELECT cursor_json FROM collection_sources WHERE id=?1",
            params![source_id],
            |r| r.get(0),
        )?)
    }

    pub fn expired_incomplete_count(&self, source_id: i64) -> Result<i64, PipelineError> {
        Ok(self.conn.query_row(
            "SELECT expired_incomplete_event_count FROM collection_sources WHERE id=?1",
            params![source_id],
            |r| r.get(0),
        )?)
    }

    pub fn with_connection<T>(
        &mut self,
        f: impl FnOnce(&mut Connection) -> Result<T, PipelineError>,
    ) -> Result<T, PipelineError> {
        f(&mut self.conn)
    }
}

// ── helpers ─────────────────────────────────────────────────────────────────

struct SourceRow {
    harness_id: String,
    commit_seq: i64,
    lease_token: Option<String>,
    enabled: i64,
    delete_at: Option<i64>,
    ignored_record_count: i64,
}

struct ExpireRow {
    id: i64,
    collection_source_id: i64,
    harness_id: String,
    session_key: Option<Vec<u8>>,
    cost_scope_key: Option<Vec<u8>>,
    status_json: String,
}

enum InsertOutcome {
    Inserted,
    Duplicate,
}

fn system_now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

fn new_lease_token() -> String {
    Uuid::new_v4().to_string()
}

fn validate_json_object(raw: &str, field: &str) -> Result<(), PipelineError> {
    let value: serde_json::Value = serde_json::from_str(raw)
        .map_err(|e| PipelineError::InvalidArgument(format!("{field}: {e}")))?;
    if !value.is_object() {
        return Err(PipelineError::InvalidArgument(format!(
            "{field} must be a JSON object"
        )));
    }
    Ok(())
}

fn parse_source_kind(raw: &str) -> Result<SourceKind, PipelineError> {
    match raw {
        "jsonl" => Ok(SourceKind::Jsonl),
        "sqlite" => Ok(SourceKind::Sqlite),
        "other" => Ok(SourceKind::Other),
        other => Err(PipelineError::InvalidArgument(format!(
            "unknown source_kind {other}"
        ))),
    }
}

fn parse_cursor_kind(raw: &str) -> Result<CursorKind, PipelineError> {
    match raw {
        "byte_offset" => Ok(CursorKind::ByteOffset),
        "sqlite_change" => Ok(CursorKind::SqliteChange),
        "opaque" => Ok(CursorKind::Opaque),
        other => Err(PipelineError::InvalidArgument(format!(
            "unknown cursor_kind {other}"
        ))),
    }
}

fn validate_batch_limits(batch: &SourceCommitBatch) -> Result<(), PipelineError> {
    let bytes = batch
        .events
        .iter()
        .map(|e| e.payload_json.len() + 64)
        .sum::<usize>()
        + batch.cursor_json.len()
        + batch.decoder_state_json.len()
        + batch.observed_boundary_json.len();
    if batch.events.len() > MAX_BATCH_EVENTS || bytes > MAX_BATCH_BYTES {
        return Err(PipelineError::BatchTooLarge {
            events: batch.events.len(),
            bytes,
        });
    }
    Ok(())
}

fn validate_event_candidate(event: &EventCandidate) -> Result<(), PipelineError> {
    if event.fact_revision <= 0 {
        return Err(PipelineError::InvalidArgument(
            "fact_revision must be > 0".into(),
        ));
    }
    if event.schema_version <= 0 || event.metric_semantics_version <= 0 {
        return Err(PipelineError::InvalidArgument(
            "schema/metric versions must be > 0".into(),
        ));
    }
    if event.event_type.is_empty() {
        return Err(PipelineError::InvalidArgument(
            "event_type required".into(),
        ));
    }
    validate_json_object(&event.payload_json, "payload_json")?;
    let mut seen = HashSet::new();
    for c in &event.applicable_consumers {
        if !seen.insert(*c) {
            return Err(PipelineError::InvalidArgument(
                "duplicate consumer in applicable_consumers".into(),
            ));
        }
    }
    Ok(())
}

fn load_source_for_commit(tx: &Transaction<'_>, source_id: i64) -> Result<SourceRow, PipelineError> {
    tx.query_row(
        "SELECT harness_id, commit_seq, lease_token, enabled, delete_at, ignored_record_count
         FROM collection_sources WHERE id=?1",
        params![source_id],
        |row| {
            Ok(SourceRow {
                harness_id: row.get(0)?,
                commit_seq: row.get(1)?,
                lease_token: row.get(2)?,
                enabled: row.get(3)?,
                delete_at: row.get(4)?,
                ignored_record_count: row.get(5)?,
            })
        },
    )
    .optional()?
    .ok_or(PipelineError::SourceNotFound(source_id))
}

fn build_status_json(applicable: &[Consumer]) -> String {
    let mut hour = ConsumerStatus::NotApplicable as u8;
    let mut day = ConsumerStatus::NotApplicable as u8;
    let mut month = ConsumerStatus::NotApplicable as u8;
    let mut upload = ConsumerStatus::NotApplicable as u8;
    for c in applicable {
        match c {
            Consumer::Hour => hour = ConsumerStatus::Pending as u8,
            Consumer::Day => day = ConsumerStatus::Pending as u8,
            Consumer::Month => month = ConsumerStatus::Pending as u8,
            Consumer::Upload => upload = ConsumerStatus::Pending as u8,
        }
    }
    format!(
        r#"{{"hour":{hour},"day":{day},"month":{month},"upload":{upload}}}"#
    )
}

fn insert_event_with_tasks(
    tx: &Transaction<'_>,
    source_id: i64,
    harness_id: &str,
    event: &EventCandidate,
    now: i64,
) -> Result<InsertOutcome, PipelineError> {
    // Prefer fact_key+revision identity; also detect event_id collisions.
    let existing: Option<(i64, Vec<u8>, String)> = tx
        .query_row(
            "SELECT id, content_hash, status_json FROM events
             WHERE fact_key=?1 AND fact_revision=?2",
            params![event.fact_key.as_slice(), event.fact_revision],
            |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
        )
        .optional()?;

    if let Some((_id, hash, _status)) = existing {
        if hash.as_slice() == event.content_hash.as_slice() {
            // Idempotent: keep old row/tasks/status; do not recreate finished work.
            return Ok(InsertOutcome::Duplicate);
        }
        return Err(PipelineError::IdentityContentConflict {
            fact_key: event.fact_key,
            fact_revision: event.fact_revision,
        });
    }

    // event_id unique collision with different fact key would also be a conflict.
    let by_event_id: Option<Vec<u8>> = tx
        .query_row(
            "SELECT content_hash FROM events WHERE event_id=?1",
            params![event.event_id.as_slice()],
            |row| row.get(0),
        )
        .optional()?;
    if let Some(hash) = by_event_id {
        if hash.as_slice() == event.content_hash.as_slice() {
            return Ok(InsertOutcome::Duplicate);
        }
        return Err(PipelineError::IdentityContentConflict {
            fact_key: event.fact_key,
            fact_revision: event.fact_revision,
        });
    }

    let status_json = build_status_json(&event.applicable_consumers);
    tx.execute(
        "INSERT INTO events (
            created_at, updated_at, event_id, fact_key, fact_revision,
            collection_source_id, harness_id, event_type, schema_version,
            metric_semantics_version, content_hash, occurred_at, model_key, skill_id,
            session_key, turn_key, cost_scope_key, payload_json, status_json
         ) VALUES (
            ?1, ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, ?17, ?18
         )",
        params![
            now,
            event.event_id.as_slice(),
            event.fact_key.as_slice(),
            event.fact_revision,
            source_id,
            harness_id,
            event.event_type,
            event.schema_version,
            event.metric_semantics_version,
            event.content_hash.as_slice(),
            event.occurred_at,
            event.model_key,
            event.skill_id,
            event.session_key.as_ref().map(|b| b.as_slice()),
            event.turn_key.as_ref().map(|b| b.as_slice()),
            event.cost_scope_key.as_ref().map(|b| b.as_slice()),
            event.payload_json,
            status_json,
        ],
    )?;
    let event_row_id = tx.last_insert_rowid();
    for consumer in &event.applicable_consumers {
        tx.execute(
            "INSERT INTO processing_tasks (
                created_at, updated_at, event_row_id, consumer, runnable_at
             ) VALUES (?1, ?1, ?2, ?3, ?1)",
            params![now, event_row_id, consumer.as_str()],
        )?;
    }
    Ok(InsertOutcome::Inserted)
}

fn verify_task_lease(
    tx: &Transaction<'_>,
    task_id: i64,
    event_row_id: i64,
    consumer: Consumer,
    lease_token: &str,
    now: i64,
) -> Result<(), PipelineError> {
    let row: Option<(i64, String, Option<String>, Option<i64>, Option<i64>)> = tx
        .query_row(
            "SELECT event_row_id, consumer, lease_token, lease_until, delete_at
             FROM processing_tasks WHERE id=?1",
            params![task_id],
            |row| {
                Ok((
                    row.get(0)?,
                    row.get(1)?,
                    row.get(2)?,
                    row.get(3)?,
                    row.get(4)?,
                ))
            },
        )
        .optional()?;
    let Some((row_event_id, row_consumer, token, lease_until, delete_at)) = row else {
        return Err(PipelineError::TaskNotRunnable);
    };
    if delete_at.is_some()
        || row_event_id != event_row_id
        || row_consumer != consumer.as_str()
    {
        return Err(PipelineError::TaskNotRunnable);
    }
    match (token.as_deref(), lease_until) {
        (Some(t), Some(until)) if t == lease_token && until > now => {}
        _ => return Err(PipelineError::TaskLeaseMismatch),
    }

    let event: Option<(Option<i64>, i64, i64)> = tx
        .query_row(
            "SELECT delete_at, expire_at, CAST(json_extract(status_json, ?1) AS INTEGER)
             FROM events WHERE id=?2",
            params![consumer.status_path(), event_row_id],
            |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
        )
        .optional()?;
    let Some((delete_at, expire_at, status)) = event else {
        return Err(PipelineError::EventExpiredOrDeleted);
    };
    if delete_at.is_some() || expire_at <= now {
        return Err(PipelineError::EventExpiredOrDeleted);
    }
    if status != ConsumerStatus::InFlight as i64 {
        return Err(PipelineError::TaskNotRunnable);
    }
    Ok(())
}

fn event_has_incomplete_work(status_json: &str) -> Result<bool, PipelineError> {
    let v: serde_json::Value = serde_json::from_str(status_json)
        .map_err(|e| PipelineError::Sqlite(e.to_string()))?;
    for key in ["hour", "day", "month", "upload"] {
        let status = v
            .get(key)
            .and_then(|x| x.as_i64())
            .ok_or_else(|| PipelineError::Sqlite(format!("bad status_json missing {key}")))?;
        // Incomplete: pending/retry/in_flight/blocked (not applied/N/A/quarantined terminal-ish).
        // Quarantined (6) is terminal for auto-retry but still "not finished successfully";
        // count it as incomplete for expired_incomplete_event_count.
        if matches!(status, 0 | 1 | 2 | 5 | 6) {
            return Ok(true);
        }
    }
    Ok(false)
}

fn block_dependent_tasks(
    tx: &Transaction<'_>,
    expired: &ExpireRow,
    now: i64,
) -> Result<(), PipelineError> {
    // Block not-yet-expired siblings that share session or cost scope so metrics
    // consumers cannot apply with a missing historical contribution.
    let mut ids: HashSet<i64> = HashSet::new();
    if let Some(session_key) = &expired.session_key {
        let mut stmt = tx.prepare(
            "SELECT id FROM events
             WHERE harness_id=?1 AND session_key=?2 AND id!=?3
               AND expire_at > ?4 AND delete_at IS NULL",
        )?;
        for id in stmt.query_map(
            params![expired.harness_id, session_key.as_slice(), expired.id, now],
            |row| row.get::<_, i64>(0),
        )? {
            ids.insert(id?);
        }
    }
    if let Some(cost_scope_key) = &expired.cost_scope_key {
        let mut stmt = tx.prepare(
            "SELECT id FROM events
             WHERE harness_id=?1 AND cost_scope_key=?2 AND id!=?3
               AND expire_at > ?4 AND delete_at IS NULL",
        )?;
        for id in stmt.query_map(
            params![
                expired.harness_id,
                cost_scope_key.as_slice(),
                expired.id,
                now
            ],
            |row| row.get::<_, i64>(0),
        )? {
            ids.insert(id?);
        }
    }

    for event_row_id in ids {
        for consumer in [Consumer::Hour, Consumer::Day, Consumer::Month] {
            let status: i64 = tx.query_row(
                "SELECT CAST(json_extract(status_json, ?1) AS INTEGER) FROM events WHERE id=?2",
                params![consumer.status_path(), event_row_id],
                |row| row.get(0),
            )?;
            if !matches!(
                status,
                s if s == ConsumerStatus::Pending as i64
                    || s == ConsumerStatus::Retry as i64
                    || s == ConsumerStatus::InFlight as i64
            ) {
                continue;
            }
            tx.execute(
                "UPDATE events
                 SET status_json=json_set(status_json, ?1, ?2), updated_at=?3
                 WHERE id=?4",
                params![
                    consumer.status_path(),
                    ConsumerStatus::Blocked as i64,
                    now,
                    event_row_id
                ],
            )?;
            // Cancel lease / scheduling for this consumer task if still present.
            tx.execute(
                "UPDATE processing_tasks
                 SET lease_token=NULL, lease_until=NULL, runnable_at=NULL,
                     last_error_code='dependency_expired', delete_at=?1, updated_at=?1
                 WHERE event_row_id=?2 AND consumer=?3 AND delete_at IS NULL",
                params![now, event_row_id, consumer.as_str()],
            )?;
        }
    }
    Ok(())
}
