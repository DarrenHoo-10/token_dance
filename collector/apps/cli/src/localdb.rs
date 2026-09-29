//! Read-only view of the event database. Nothing here migrates, initializes or writes.

use std::path::PathBuf;

use collector_service::local_store::pipeline::{DB_FILE, PIPELINE_SCHEMA_VERSION};
use collector_service::AppPaths;
use rusqlite::{Connection, OpenFlags, OptionalExtension};
use serde::Serialize;

use crate::error::{CliError, CliResult, Kind};

pub fn db_path(paths: &AppPaths) -> PathBuf {
    paths.collector.join(DB_FILE)
}

/// `Ok(None)` when the database has not been created yet.
pub fn open_read_only(paths: &AppPaths) -> CliResult<Option<Connection>> {
    let path = db_path(paths);
    if !path.exists() {
        return Ok(None);
    }
    let conn = Connection::open_with_flags(
        &path,
        OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX,
    )
    .map_err(|e| CliError::internal(format!("cannot open the event database read-only: {e}")))?;
    conn.busy_timeout(std::time::Duration::from_millis(2_000))
        .map_err(|e| CliError::internal(e.to_string()))?;
    Ok(Some(conn))
}

pub fn schema_version(conn: &Connection) -> Option<i64> {
    conn.query_row(
        "SELECT schema_version FROM schema_meta WHERE id=1",
        [],
        |r| r.get(0),
    )
    .optional()
    .ok()
    .flatten()
}

/// Refuse to write to a database created by a newer build.
pub fn ensure_writable_schema(paths: &AppPaths) -> CliResult<()> {
    if let Some(conn) = open_read_only(paths)? {
        if let Some(version) = schema_version(&conn) {
            if version > PIPELINE_SCHEMA_VERSION {
                return Err(CliError::new(
                    Kind::Incompatible,
                    format!(
                        "the event database uses schema {version}, this build supports up to {PIPELINE_SCHEMA_VERSION}"
                    ),
                )
                .hint("upgrade tokendance; an older build will not write to a newer database"));
            }
        }
    }
    Ok(())
}

#[derive(Debug, Default, Clone, Serialize)]
pub struct SourceStat {
    pub harness: String,
    pub streams: i64,
    pub enabled_streams: i64,
    pub last_updated_ms: Option<i64>,
    pub with_errors: i64,
}

#[derive(Debug, Default, Clone, Serialize)]
pub struct Stats {
    pub schema_version: Option<i64>,
    pub events: i64,
    pub metric_tasks_pending: i64,
    pub upload_tasks_pending: i64,
    pub sources: Vec<SourceStat>,
}

pub fn stats(conn: &Connection) -> CliResult<Stats> {
    let map = |e: rusqlite::Error| CliError::internal(format!("event database query failed: {e}"));
    let count =
        |sql: &str| -> CliResult<i64> { conn.query_row(sql, [], |r| r.get(0)).map_err(map) };
    let mut stats = Stats {
        schema_version: schema_version(conn),
        events: count("SELECT count(*) FROM events WHERE delete_at IS NULL")?,
        metric_tasks_pending: count(
            "SELECT count(*) FROM processing_tasks
             WHERE delete_at IS NULL AND consumer IN ('hour','day','month')
               AND (runnable_at IS NOT NULL OR lease_token IS NOT NULL)",
        )?,
        upload_tasks_pending: count(
            "SELECT count(*) FROM processing_tasks
             WHERE delete_at IS NULL AND consumer='upload'
               AND (runnable_at IS NOT NULL OR lease_token IS NOT NULL)",
        )?,
        sources: Vec::new(),
    };
    let mut stmt = conn
        .prepare(
            "SELECT harness_id, count(*), COALESCE(SUM(enabled),0), max(updated_at),
                    COALESCE(SUM(last_error_code IS NOT NULL),0)
             FROM collection_sources WHERE delete_at IS NULL
             GROUP BY harness_id ORDER BY harness_id",
        )
        .map_err(map)?;
    let rows = stmt
        .query_map([], |r| {
            Ok(SourceStat {
                harness: r.get(0)?,
                streams: r.get(1)?,
                enabled_streams: r.get(2)?,
                last_updated_ms: r.get(3)?,
                with_errors: r.get(4)?,
            })
        })
        .map_err(map)?;
    for row in rows {
        stats.sources.push(row.map_err(map)?);
    }
    Ok(stats)
}
