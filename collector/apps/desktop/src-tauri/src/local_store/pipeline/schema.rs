//! Empty-DB bootstrap for the v3 event-pipeline schema.

use rusqlite::{params, Connection, OptionalExtension};

use super::types::{PipelineError, PIPELINE_SCHEMA_VERSION};

/// Mirrored from `docs/event-pipeline-schema-v3.sqlite.sql` (canonical DDL).
const BUSINESS_DDL: &str = include_str!("../../../sql/event-pipeline-schema-v3.sqlite.sql");

const META_DDL: &str = r#"
CREATE TABLE schema_meta (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  schema_version INTEGER NOT NULL,
  event_pipeline_v3 INTEGER NOT NULL DEFAULT 0 CHECK(event_pipeline_v3 IN (0,1)),
  event_pipeline_initialized_at INTEGER,
  extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object')
) STRICT;
"#;

pub const BUSINESS_TABLES: [&str; 10] = [
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
    // Strip leading PRAGMA from the mirrored DDL; connection already configured.
    let ddl = BUSINESS_DDL
        .lines()
        .filter(|line| !line.trim().starts_with("PRAGMA "))
        .collect::<Vec<_>>()
        .join("\n");
    tx.execute_batch(&ddl)?;
    tx.execute(
        "INSERT INTO schema_meta (id, schema_version, event_pipeline_v3, event_pipeline_initialized_at, extra)
         VALUES (1, ?1, 1, ?2, '{}')",
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
    fn initializes_ten_tables_and_is_idempotent() {
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
        assert_eq!(count, 10);

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
}
