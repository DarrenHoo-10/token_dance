//! Empty-DB bootstrap for the v3 event-pipeline schema.

use rusqlite::{params, Connection, OptionalExtension};

use super::types::{PipelineError, PIPELINE_SCHEMA_VERSION};

/// Mirrored from `docs/event-pipeline-schema-v3.sqlite.sql` (canonical DDL).
const BUSINESS_DDL: &str = include_str!("../../../sql/event-pipeline-schema-v3.sqlite.sql");

const META_DDL: &str = r#"
CREATE TABLE IF NOT EXISTS schema_meta (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  schema_version INTEGER NOT NULL DEFAULT 0,
  event_pipeline_v3 INTEGER NOT NULL DEFAULT 0 CHECK(event_pipeline_v3 IN (0,1)),
  event_pipeline_initialized_at INTEGER,
  extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object')
) STRICT;
"#;

pub const BUSINESS_TABLES: [&str; 11] = [
    "collection_sources",
    "model_dimensions",
    "skill_dimensions",
    "events",
    "processing_tasks",
    "harness_metrics",
    "model_metrics",
    "skill_metrics",
    "cost_metrics",
    "bucket_entity_state",
    "session_extents",
];

pub fn configure_connection(conn: &Connection) -> Result<(), PipelineError> {
    conn.busy_timeout(std::time::Duration::from_millis(5_000))?;
    conn.execute_batch(
        "PRAGMA foreign_keys=ON;
         PRAGMA journal_mode=WAL;
         PRAGMA synchronous=FULL;
         PRAGMA busy_timeout=5000;",
    )?;
    // Re-assert foreign_keys after journal_mode (some builds reset connection state).
    conn.execute_batch("PRAGMA foreign_keys=ON;")?;
    Ok(())
}

pub fn is_pipeline_ready(conn: &Connection) -> Result<bool, PipelineError> {
    let exists: Option<i64> = conn
        .query_row(
            "SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_meta'",
            [],
            |row| row.get(0),
        )
        .optional()?;
    if exists.is_none() {
        return Ok(false);
    }
    let ready: Option<i64> = conn
        .query_row(
            "SELECT event_pipeline_v3 FROM schema_meta WHERE id = 1",
            [],
            |row| row.get(0),
        )
        .optional()?;
    Ok(ready == Some(1))
}

/// Initialize the 10 business tables on an empty database and mark schema_meta.
/// Re-running after a successful init is a no-op and must not wipe events.
/// Tolerates a pre-existing `schema_meta` shell written by the P8 rollout state machine.
pub fn initialize_empty(conn: &mut Connection, now_ms: i64) -> Result<bool, PipelineError> {
    if is_pipeline_ready(conn)? {
        return Ok(false);
    }

    // Refuse to run business DDL on a partially-initialized file.
    for table in BUSINESS_TABLES {
        let exists: Option<i64> = conn
            .query_row(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?1",
                params![table],
                |row| row.get(0),
            )
            .optional()?;
        if exists.is_some() {
            return Err(PipelineError::InvalidArgument(format!(
                "table {table} exists but schema_meta.event_pipeline_v3 is not ready"
            )));
        }
    }

    let tx = conn.transaction()?;
    tx.execute_batch(META_DDL)?;
    // Preserve any rollout stamp already stored in schema_meta.extra.
    let prior_extra: String = tx
        .query_row("SELECT extra FROM schema_meta WHERE id = 1", [], |r| {
            r.get(0)
        })
        .optional()?
        .unwrap_or_else(|| "{}".into());
    // Strip leading PRAGMA from the mirrored DDL; connection already configured.
    let ddl = BUSINESS_DDL
        .lines()
        .filter(|line| !line.trim().starts_with("PRAGMA "))
        .collect::<Vec<_>>()
        .join("\n");
    tx.execute_batch(&ddl)?;
    tx.execute(
        "INSERT INTO schema_meta (id, schema_version, event_pipeline_v3, event_pipeline_initialized_at, extra)
         VALUES (1, ?1, 1, ?2, ?3)
         ON CONFLICT(id) DO UPDATE SET
           schema_version = excluded.schema_version,
           event_pipeline_v3 = 1,
           event_pipeline_initialized_at = COALESCE(schema_meta.event_pipeline_initialized_at, excluded.event_pipeline_initialized_at),
           extra = CASE
             WHEN schema_meta.extra IS NULL OR schema_meta.extra = '{}' THEN excluded.extra
             ELSE schema_meta.extra
           END",
        params![PIPELINE_SCHEMA_VERSION, now_ms, prior_extra],
    )?;
    // If row was freshly inserted with empty extra from conflict path, ensure initialized_at.
    tx.execute(
        "UPDATE schema_meta
         SET event_pipeline_v3 = 1,
             schema_version = ?1,
             event_pipeline_initialized_at = COALESCE(event_pipeline_initialized_at, ?2)
         WHERE id = 1",
        params![PIPELINE_SCHEMA_VERSION, now_ms],
    )?;
    tx.commit()?;
    Ok(true)
}

pub fn assert_ready(conn: &Connection) -> Result<(), PipelineError> {
    if is_pipeline_ready(conn)? {
        Ok(())
    } else {
        Err(PipelineError::NotInitialized)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn initializes_business_tables_and_is_idempotent() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("t.sqlite3");
        let mut conn = Connection::open(&path).unwrap();
        configure_connection(&conn).unwrap();
        assert!(initialize_empty(&mut conn, 1_000).unwrap());
        assert!(!initialize_empty(&mut conn, 2_000).unwrap());

        let count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_meta'",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(count, 11);

        let unknown: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM model_dimensions WHERE id = 0",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(unknown, 1);

        let init_at: i64 = conn
            .query_row(
                "SELECT event_pipeline_initialized_at FROM schema_meta WHERE id = 1",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(init_at, 1_000);
    }

    #[test]
    fn recovers_from_existing_meta_shell() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("shell.sqlite3");
        let mut conn = Connection::open(&path).unwrap();
        configure_connection(&conn).unwrap();
        conn.execute_batch(META_DDL).unwrap();
        conn.execute(
            "INSERT INTO schema_meta (id, schema_version, event_pipeline_v3, extra)
             VALUES (1, 0, 0, '{\"rollout_phase\":\"initializing\"}')",
            [],
        )
        .unwrap();
        assert!(initialize_empty(&mut conn, 3_000).unwrap());
        assert!(is_pipeline_ready(&conn).unwrap());
        let extra: String = conn
            .query_row("SELECT extra FROM schema_meta WHERE id=1", [], |r| r.get(0))
            .unwrap();
        assert!(extra.contains("initializing"));
    }
}

pub fn ensure_session_extents(conn: &Connection) -> Result<(), PipelineError> {
    let exists: bool = conn.query_row(
        "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='session_extents')",
        [],
        |r| r.get(0),
    )?;
    if exists {
        return Ok(());
    }
    let tx = conn.unchecked_transaction()?;
    tx.execute_batch(
        r#"
 CREATE TABLE IF NOT EXISTS session_extents (
 id INTEGER PRIMARY KEY, -- 本地代理主键
 created_at INTEGER NOT NULL, -- 首次创建 UTC 毫秒
 updated_at INTEGER NOT NULL, -- 最近更新 UTC 毫秒
 delete_at INTEGER, -- 删除 UTC 毫秒；NULL 未删除
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)), -- 可选扩展；不存必需业务状态
 harness_id TEXT NOT NULL, -- 工具标识
 session_key BLOB NOT NULL CHECK(length(session_key)=32), -- 稳定匿名会话身份
 grain TEXT NOT NULL CHECK(grain IN ('hour','day','month')), -- 独立消费者的处理进度
 first_event_at INTEGER NOT NULL, -- 已处理的最早事件 UTC 毫秒
 last_event_at INTEGER NOT NULL CHECK(last_event_at>=first_event_at), -- 已处理的最晚事件 UTC 毫秒
 UNIQUE(harness_id,session_key,grain)
 ) STRICT;
 "#,
    )?;
    // Old duration aggregates measured explicit activity; do not add a different
    // session-span measure onto those values. The bounded worker replays details.
    tx.execute(
        "UPDATE harness_metrics SET active_duration_ms=0,duration_known_count=0",
        [],
    )?;
    tx.commit()?;
    Ok(())
}

/// Re-read existing WorkBuddy sources once to attach the native model to revision 2.
/// The transaction runs before the writer/workers start and never deletes events.
pub fn repair_workbuddy_models(conn: &Connection) -> Result<(), PipelineError> {
    let tx = conn.unchecked_transaction()?;
    let done: bool = tx.query_row("SELECT coalesce(json_extract(extra,'$.workbuddy_model_repair'),0)>=1 FROM schema_meta WHERE id=1", [], |r|r.get(0))?;
    if !done {
        tx.execute("UPDATE collection_sources SET cursor_json='{}',decoder_state_json='{}',commit_seq=commit_seq+1,lease_token=NULL,lease_until=NULL,next_poll_at=0,observed_boundary_json=json_set(observed_boundary_json,'$._rebuild_pending',json('true')) WHERE harness_id='workbuddy' AND delete_at IS NULL AND EXISTS(SELECT 1 FROM events WHERE events.collection_source_id=collection_sources.id AND events.event_type='model_usage_recorded' AND events.model_key=0)", [])?;
        tx.execute("UPDATE schema_meta SET extra=json_set(extra,'$.workbuddy_model_repair',1) WHERE id=1", [])?;
    }
    tx.commit()?;
    Ok(())
}
