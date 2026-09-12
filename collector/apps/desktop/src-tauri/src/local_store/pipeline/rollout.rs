//! P8 client empty-DB rollout state machine.
//!
//! Flow: stop_legacy → init_empty → seed_unknown → mark_ready → (workers may start).
//! Successful generation is persisted in `schema_meta.extra`; restarts must not wipe.

use rusqlite::{params, Connection, OptionalExtension};
use serde::{Deserialize, Serialize};

use super::flags;
use super::schema::{self, BUSINESS_TABLES};
use super::types::PipelineError;

pub const CLOSED_BETA_GENERATION: i64 = 1;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum RolloutPhase {
    Pending,
    StoppingLegacy,
    Initializing,
    Ready,
    Failed,
    Paused,
}

impl RolloutPhase {
    fn as_str(self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::StoppingLegacy => "stopping_legacy",
            Self::Initializing => "initializing",
            Self::Ready => "ready",
            Self::Failed => "failed",
            Self::Paused => "paused",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RolloutStatus {
    pub generation: i64,
    pub phase: RolloutPhase,
    pub client_flag_enabled: bool,
    pub pipeline_ready: bool,
    pub workers_may_start: bool,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
struct RolloutExtra {
    #[serde(default)]
    rollout_generation: i64,
    #[serde(default)]
    rollout_phase: String,
    #[serde(default)]
    legacy_stopped_at: Option<i64>,
    #[serde(default)]
    init_attempts: i64,
    #[serde(default)]
    last_error: Option<String>,
}

/// Drive recoverable empty-DB initialization. Idempotent once Ready for generation.
pub fn ensure_rollout(conn: &mut Connection, now_ms: i64) -> Result<RolloutStatus, PipelineError> {
    let client_enabled = flags::event_pipeline_v2_client_enabled();
    if !client_enabled {
        // Pause new path; never reopen legacy upload from this branch.
        let mut extra = read_extra(conn)?;
        if extra.rollout_phase != RolloutPhase::Paused.as_str()
            && extra.rollout_phase != RolloutPhase::Ready.as_str()
        {
            extra.rollout_phase = RolloutPhase::Paused.as_str().into();
            write_extra(conn, &extra)?;
        }
        return Ok(RolloutStatus {
            generation: extra.rollout_generation.max(CLOSED_BETA_GENERATION),
            phase: RolloutPhase::Paused,
            client_flag_enabled: false,
            pipeline_ready: schema::is_pipeline_ready(conn)?,
            workers_may_start: false,
            message: "event_pipeline_v2_client disabled; sync paused without legacy reopen".into(),
        });
    }

    schema::configure_connection(conn)?;

    if schema::is_pipeline_ready(conn)? {
        let mut extra = read_extra(conn)?;
        if extra.rollout_generation >= CLOSED_BETA_GENERATION
            && extra.rollout_phase == RolloutPhase::Ready.as_str()
        {
            return Ok(ready_status(extra.rollout_generation, true));
        }
        // Already initialized (P1 path) but missing rollout stamp — seal without wipe.
        extra.rollout_generation = CLOSED_BETA_GENERATION;
        extra.rollout_phase = RolloutPhase::Ready.as_str().into();
        extra.legacy_stopped_at = extra.legacy_stopped_at.or(Some(now_ms));
        extra.last_error = None;
        write_extra(conn, &extra)?;
        return Ok(ready_status(CLOSED_BETA_GENERATION, true));
    }

    // Interruptible init: mark phases, then initialize_empty (atomic).
    let mut extra = read_extra(conn)?;
    extra.rollout_generation = CLOSED_BETA_GENERATION;
    extra.rollout_phase = RolloutPhase::StoppingLegacy.as_str().into();
    extra.legacy_stopped_at = Some(now_ms);
    extra.init_attempts = extra.init_attempts.saturating_add(1);
    write_extra(conn, &extra)?;

    extra.rollout_phase = RolloutPhase::Initializing.as_str().into();
    write_extra(conn, &extra)?;

    match schema::initialize_empty(conn, now_ms) {
        Ok(_) => {
            // initialize_empty inserts schema_meta with extra='{}' — restore stamp.
            extra.rollout_generation = CLOSED_BETA_GENERATION;
            extra.rollout_phase = RolloutPhase::Ready.as_str().into();
            extra.legacy_stopped_at = Some(now_ms);
            extra.last_error = None;
            write_extra(conn, &extra)?;
            verify_unknown_model(conn)?;
            verify_business_tables(conn)?;
            Ok(ready_status(CLOSED_BETA_GENERATION, true))
        }
        Err(err) => {
            extra.rollout_phase = RolloutPhase::Failed.as_str().into();
            extra.last_error = Some(err.to_string());
            let _ = write_extra(conn, &extra);
            Err(err)
        }
    }
}

fn ready_status(generation: i64, workers: bool) -> RolloutStatus {
    RolloutStatus {
        generation,
        phase: RolloutPhase::Ready,
        client_flag_enabled: true,
        pipeline_ready: true,
        workers_may_start: workers,
        message: "event pipeline v3 empty-DB ready".into(),
    }
}

fn verify_unknown_model(conn: &Connection) -> Result<(), PipelineError> {
    let count: i64 = conn.query_row(
        "SELECT COUNT(*) FROM model_dimensions WHERE id = 0 AND provider_id='unknown' AND model_id='unknown'",
        [],
        |r| r.get(0),
    )?;
    if count != 1 {
        return Err(PipelineError::InvalidArgument(
            "unknown model dimension missing after init".into(),
        ));
    }
    Ok(())
}

fn verify_business_tables(conn: &Connection) -> Result<(), PipelineError> {
    for table in BUSINESS_TABLES {
        let exists: Option<i64> = conn
            .query_row(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?1",
                params![table],
                |r| r.get(0),
            )
            .optional()?;
        if exists.is_none() {
            return Err(PipelineError::InvalidArgument(format!(
                "missing business table {table}"
            )));
        }
    }
    Ok(())
}

fn read_extra(conn: &Connection) -> Result<RolloutExtra, PipelineError> {
    let exists: Option<i64> = conn
        .query_row(
            "SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_meta'",
            [],
            |r| r.get(0),
        )
        .optional()?;
    if exists.is_none() {
        return Ok(RolloutExtra::default());
    }
    let raw: Option<String> = conn
        .query_row(
            "SELECT extra FROM schema_meta WHERE id = 1",
            [],
            |r| r.get(0),
        )
        .optional()?;
    match raw {
        None => Ok(RolloutExtra::default()),
        Some(s) => Ok(serde_json::from_str(&s).unwrap_or_default()),
    }
}

fn write_extra(conn: &mut Connection, extra: &RolloutExtra) -> Result<(), PipelineError> {
    let payload = serde_json::to_string(extra)
        .map_err(|e| PipelineError::InvalidArgument(format!("rollout extra: {e}")))?;
    let has_meta: Option<i64> = conn
        .query_row(
            "SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_meta'",
            [],
            |r| r.get(0),
        )
        .optional()?;
    if has_meta.is_none() {
        // Create meta shell so interruptible phase survives crash before DDL.
        conn.execute_batch(
            r#"
            CREATE TABLE IF NOT EXISTS schema_meta (
              id INTEGER PRIMARY KEY CHECK (id = 1),
              schema_version INTEGER NOT NULL DEFAULT 0,
              event_pipeline_v3 INTEGER NOT NULL DEFAULT 0 CHECK(event_pipeline_v3 IN (0,1)),
              event_pipeline_initialized_at INTEGER,
              extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object')
            ) STRICT;
            "#,
        )?;
        conn.execute(
            "INSERT OR IGNORE INTO schema_meta (id, schema_version, event_pipeline_v3, extra)
             VALUES (1, 0, 0, '{}')",
            [],
        )?;
    }
    conn.execute(
        "UPDATE schema_meta SET extra = ?1 WHERE id = 1",
        params![payload],
    )?;
    Ok(())
}

/// True when new acquisition / metrics / upload workers are allowed to run.
pub fn workers_allowed(conn: &Connection) -> Result<bool, PipelineError> {
    if !flags::event_pipeline_v2_client_enabled() {
        return Ok(false);
    }
    if !schema::is_pipeline_ready(conn)? {
        return Ok(false);
    }
    let extra = read_extra(conn)?;
    Ok(extra.rollout_phase == RolloutPhase::Ready.as_str()
        && extra.rollout_generation >= CLOSED_BETA_GENERATION)
}

#[cfg(test)]
mod tests {
    use super::*;
    use rusqlite::Connection;
    use tempfile::TempDir;

    #[test]
    fn empty_db_init_is_idempotent_and_survives_restart() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("rollout.sqlite3");
        {
            let mut conn = Connection::open(&path).unwrap();
            let status = ensure_rollout(&mut conn, 1_000).unwrap();
            assert_eq!(status.phase, RolloutPhase::Ready);
            assert!(status.workers_may_start);
            assert!(workers_allowed(&conn).unwrap());

            // Insert sentinel via events count only — no schema-fragile fixture insert.
            let n: i64 = conn
                .query_row("SELECT COUNT(*) FROM events", [], |r| r.get(0))
                .unwrap();
            assert_eq!(n, 0);
        }
        {
            let mut conn = Connection::open(&path).unwrap();
            let before: i64 = conn
                .query_row(
                    "SELECT event_pipeline_initialized_at FROM schema_meta WHERE id=1",
                    [],
                    |r| r.get(0),
                )
                .unwrap();
            let status = ensure_rollout(&mut conn, 9_000).unwrap();
            assert_eq!(status.phase, RolloutPhase::Ready);
            let after: i64 = conn
                .query_row(
                    "SELECT event_pipeline_initialized_at FROM schema_meta WHERE id=1",
                    [],
                    |r| r.get(0),
                )
                .unwrap();
            assert_eq!(before, after, "repeat start must not re-init / clear");
            assert_eq!(before, 1_000);
        }
    }

    #[test]
    fn interrupted_init_can_recover() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("partial.sqlite3");
        {
            let mut conn = Connection::open(&path).unwrap();
            // Simulate crash after stopping_legacy stamp, before tables.
            let extra = RolloutExtra {
                rollout_generation: CLOSED_BETA_GENERATION,
                rollout_phase: RolloutPhase::Initializing.as_str().into(),
                legacy_stopped_at: Some(500),
                init_attempts: 1,
                last_error: Some("simulated".into()),
            };
            write_extra(&mut conn, &extra).unwrap();
        }
        let mut conn = Connection::open(&path).unwrap();
        let status = ensure_rollout(&mut conn, 2_000).unwrap();
        assert_eq!(status.phase, RolloutPhase::Ready);
        assert!(schema::is_pipeline_ready(&conn).unwrap());
    }
}
