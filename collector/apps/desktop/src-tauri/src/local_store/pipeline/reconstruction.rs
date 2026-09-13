//! Software-release-triggered and manual reconstruction. Device credentials live outside this DB.
use super::types::PipelineError;
use rusqlite::Connection;
use serde::{Deserialize, Serialize};

/// Keep historical milestones so skipping a release still triggers its reconstruction.
const REBUILD_RELEASES: &[&str] = &["0.1.27", "0.1.35"];

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RebuildStatus {
    pub active: bool,
    pub target_version: String,
    pub completed_version: String,
    pub discovered: bool,
    pub total_sources: i64,
    pub completed_sources: i64,
}

pub enum RebuildAction {
    Status,
    Begin(String),
    Reconcile { discovery_ok: bool },
}

pub fn required_release(current: &str, completed: &str) -> Option<String> {
    let current = semver::Version::parse(current).ok()?;
    let completed = semver::Version::parse(completed).ok();
    REBUILD_RELEASES
        .iter()
        .filter_map(|v| semver::Version::parse(v).ok())
        .filter(|v| v <= &current && completed.as_ref().is_none_or(|c| v > c))
        .max()
        .map(|v| v.to_string())
}

pub fn status(conn: &Connection) -> Result<RebuildStatus, PipelineError> {
    let raw: String =
        conn.query_row("SELECT extra FROM schema_meta WHERE id=1", [], |r| r.get(0))?;
    let value: serde_json::Value =
        serde_json::from_str(&raw).map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
    let mut s: RebuildStatus = serde_json::from_value(
        value
            .get("rebuild")
            .cloned()
            .unwrap_or(serde_json::json!({})),
    )
    .unwrap_or_default();
    (s.total_sources, s.completed_sources) = conn.query_row(
        "SELECT count(*), coalesce(sum(CASE WHEN json_extract(observed_boundary_json,'$._rebuild_pending')=1 THEN 0 ELSE 1 END),0) FROM collection_sources WHERE enabled=1 AND delete_at IS NULL",
        [], |r| Ok((r.get(0)?,r.get(1)?)))?;
    Ok(s)
}

fn save(conn: &Connection, s: &RebuildStatus) -> Result<(), PipelineError> {
    let raw =
        serde_json::to_string(s).map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
    conn.execute(
        "UPDATE schema_meta SET extra=json_set(extra,'$.rebuild',json(?1)) WHERE id=1",
        [raw],
    )?;
    Ok(())
}

pub fn begin(conn: &mut Connection, target: &str) -> Result<RebuildStatus, PipelineError> {
    let mut s = status(conn)?;
    // Repeated clicks/restarts resume; never erase work already in progress.
    if s.active {
        return Ok(s);
    }
    let tx = conn.transaction()?;
    for table in [
        "session_extents",
        "processing_tasks",
        "bucket_entity_state",
        "harness_metrics",
        "model_metrics",
        "skill_metrics",
        "cost_metrics",
        "events",
        "collection_sources",
    ] {
        tx.execute(&format!("DELETE FROM {table}"), [])?;
    }
    // Dimension ids are caches in adapter strategies; preserve public model identity mappings.
    s.active = true;
    s.target_version = target.into();
    s.discovered = false;
    s.total_sources = 0;
    s.completed_sources = 0;
    save(&tx, &s)?;
    tx.commit()?;
    Ok(s)
}

pub fn ensure(conn: &mut Connection, current: &str) -> Result<RebuildStatus, PipelineError> {
    let s = status(conn)?;
    if s.active {
        return Ok(s);
    }
    if let Some(target) = required_release(current, &s.completed_version) {
        begin(conn, &target)
    } else {
        Ok(s)
    }
}

pub fn reconcile(
    conn: &mut Connection,
    discovery_ok: bool,
) -> Result<RebuildStatus, PipelineError> {
    let mut s = status(conn)?;
    if !s.active {
        return Ok(s);
    }
    s.discovered |= discovery_ok;
    let pending: i64 = conn.query_row("SELECT count(*) FROM events WHERE delete_at IS NULL AND (json_extract(status_json,'$.hour') NOT IN (3,4) OR json_extract(status_json,'$.day') NOT IN (3,4) OR json_extract(status_json,'$.month') NOT IN (3,4))", [], |r| r.get(0))?;
    if s.discovered && s.total_sources == s.completed_sources && pending == 0 {
        s.active = false;
        s.completed_version = s.target_version.clone();
    }
    save(conn, &s)?;
    Ok(s)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::local_store::pipeline::PipelineStore;
    #[test]
    fn release_jump_and_resume() {
        // The old Mac 0.1.27 package predates reconstruction; 0.1.30 must
        // reconstruct it, while keeping progress from Windows 0.1.27 tests.
        assert_eq!(required_release("0.1.30", "").as_deref(), Some("0.1.27"));
        assert_eq!(required_release("0.1.30", "0.1.27"), None);
        assert_eq!(
            required_release("0.2.0", "0.1.26").as_deref(),
            Some("0.1.35")
        );
        assert_eq!(required_release("0.1.26", ""), None);
        assert_eq!(required_release("0.2.0", "0.1.27").as_deref(), Some("0.1.35"));
        assert_eq!(required_release("0.1.35", "0.1.35"), None);
        let mut store = PipelineStore::open_in_memory().unwrap();
        store
            .with_connection(|c| {
                assert!(ensure(c, "0.1.27")?.active);
                c.execute(
                    "UPDATE schema_meta SET extra=json_set(extra,'$.sentinel',42)",
                    [],
                )?;
                assert!(ensure(c, "0.1.27")?.active);
                assert!(!reconcile(c, true)?.active);
                assert!(!ensure(c, "0.1.27")?.active);
                assert_eq!(
                    c.query_row(
                        "SELECT json_extract(extra,'$.sentinel') FROM schema_meta",
                        [],
                        |r| r.get::<_, i64>(0)
                    )?,
                    42
                );
                assert!(begin(c, "0.1.27")?.active);
                Ok(())
            })
            .unwrap();
    }
}
