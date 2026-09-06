use std::collections::HashSet;
use std::path::PathBuf;

use rusqlite::{params, OptionalExtension, Transaction};
use wal_spool::SourceCheckpoint;

use super::{load_checkpoint, rfc3339_now, LocalStore};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiscoveredSource {
    pub adapter_id: String,
    pub source_id: String,
    pub scan_root: String,
    pub path: String,
    pub mtime_unix: i64,
    pub file_len: i64,
    pub kind: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RebuildFileProgress {
    pub job_id: String,
    pub file_path: String,
    pub file_identity: String,
    pub mtime_unix: i64,
    pub file_len: i64,
    pub status: String,
}

#[derive(Debug, Clone)]
pub struct ScanWorkItem {
    pub job_id: String,
    pub adapter_id: String,
    pub source_id: String,
    pub path: PathBuf,
    pub mtime_unix: i64,
    pub file_len: i64,
    pub kind: String,
    pub hot: bool,
    pub checkpoint: Option<SourceCheckpoint>,
}

impl LocalStore {
    pub fn discover_sources(&mut self, files: &[DiscoveredSource]) -> Result<(), String> {
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let now = rfc3339_now();
        let mut seen_jobs = HashSet::new();
        for file in files {
            let job_id = job_id(&file.adapter_id, &file.source_id);
            if seen_jobs.insert(job_id.clone()) {
                ensure_job(
                    &tx,
                    &job_id,
                    &file.scan_root,
                    &file.adapter_id,
                    &file.source_id,
                    &now,
                )?;
            }
            tx.execute(
                "INSERT INTO rebuild_job_files (
                    job_id, file_path, file_identity, mtime_unix, file_len, status
                 ) VALUES (?1, ?2, '', ?3, ?4, 'pending')
                 ON CONFLICT(job_id, file_path) DO UPDATE SET
                    mtime_unix = excluded.mtime_unix,
                    file_len = excluded.file_len,
                    status = CASE
                        WHEN rebuild_job_files.status IN ('caught_up', 'skipped')
                             AND (excluded.mtime_unix != rebuild_job_files.mtime_unix
                                  OR excluded.file_len != rebuild_job_files.file_len)
                        THEN 'pending'
                        ELSE rebuild_job_files.status
                    END",
                params![job_id, file.path, file.mtime_unix, file.file_len],
            )
            .map_err(|error| error.to_string())?;
        }
        let jobs: Vec<(String, String, String)> = {
            let mut stmt = tx
                .prepare("SELECT job_id, adapter_id, source_id FROM rebuild_jobs")
                .map_err(|error| error.to_string())?;
            let rows = stmt
                .query_map([], |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)))
                .map_err(|error| error.to_string())?;
            rows.filter_map(Result::ok).collect()
        };
        for (job_id, adapter_id, source_id) in jobs {
            if files
                .iter()
                .any(|file| file.adapter_id == adapter_id && file.source_id == source_id)
            {
                refresh_job_counts(&tx, &job_id, &now)?;
            }
        }
        tx.commit().map_err(|error| error.to_string())
    }

    pub fn select_scan_work(
        &self,
        active_limit: usize,
        rebuild_limit: usize,
    ) -> Result<Vec<ScanWorkItem>, String> {
        let mut stmt = self
            .conn
            .prepare(
                "SELECT f.job_id, j.adapter_id, j.source_id, f.file_path, f.mtime_unix,
                        f.file_len, f.status, f.file_identity
                 FROM rebuild_job_files f
                 JOIN rebuild_jobs j ON j.job_id = f.job_id
                 WHERE j.status IN ('pending', 'running')
                 ORDER BY f.mtime_unix DESC, f.file_path ASC",
            )
            .map_err(|error| error.to_string())?;
        let rows = stmt
            .query_map([], |row| {
                Ok(WorkRow {
                    job_id: row.get(0)?,
                    adapter_id: row.get(1)?,
                    source_id: row.get(2)?,
                    file_path: row.get(3)?,
                    mtime_unix: row.get(4)?,
                    file_len: row.get(5)?,
                    status: row.get(6)?,
                    file_identity: row.get(7)?,
                })
            })
            .map_err(|error| error.to_string())?;
        let rows: Vec<WorkRow> = rows.filter_map(Result::ok).collect();
        let mut selected = Vec::new();
        let mut selected_paths = HashSet::new();
        let mut hot_count = 0usize;
        for row in &rows {
            let sqlite = is_sqlite_path(&row.file_path);
            if hot_count >= active_limit && !sqlite {
                continue;
            }
            selected.push(self.work_item(row, true));
            selected_paths.insert(row.file_path.clone());
            if !sqlite {
                hot_count += 1;
            }
        }
        let mut rebuild_count = 0usize;
        for row in &rows {
            if selected_paths.contains(&row.file_path) {
                continue;
            }
            if row.status != "pending" && row.status != "in_progress" {
                continue;
            }
            if rebuild_count >= rebuild_limit {
                break;
            }
            selected.push(self.work_item(row, false));
            selected_paths.insert(row.file_path.clone());
            rebuild_count += 1;
        }
        Ok(selected)
    }

    pub fn rebuild_in_progress(&self) -> bool {
        self.conn
            .query_row(
                "SELECT 1 FROM rebuild_jobs WHERE status IN ('pending', 'running') LIMIT 1",
                [],
                |_| Ok(1),
            )
            .optional()
            .ok()
            .flatten()
            .is_some()
    }

    pub fn rebuild_job_status(&self, adapter_id: &str, source_id: &str) -> Option<String> {
        self.conn
            .query_row(
                "SELECT status FROM rebuild_jobs WHERE adapter_id = ?1 AND source_id = ?2",
                params![adapter_id, source_id],
                |row| row.get(0),
            )
            .optional()
            .ok()
            .flatten()
    }

    pub fn rebuild_file_status(
        &self,
        adapter_id: &str,
        source_id: &str,
        path: &str,
    ) -> Option<String> {
        let job_id = job_id(adapter_id, source_id);
        self.conn
            .query_row(
                "SELECT status FROM rebuild_job_files WHERE job_id = ?1 AND file_path = ?2",
                params![job_id, path],
                |row| row.get(0),
            )
            .optional()
            .ok()
            .flatten()
    }

    fn work_item(&self, row: &WorkRow, hot: bool) -> ScanWorkItem {
        let kind = if is_sqlite_path(&row.file_path) {
            "sqlite"
        } else {
            "jsonl"
        };
        let path = PathBuf::from(&row.file_path);
        let checkpoint = self.checkpoint_for_path(&row.source_id, &path).or_else(|| {
            if row.file_identity.is_empty() {
                None
            } else {
                load_checkpoint(&self.conn, &row.source_id, &row.file_identity)
            }
        });
        ScanWorkItem {
            job_id: row.job_id.clone(),
            adapter_id: row.adapter_id.clone(),
            source_id: row.source_id.clone(),
            path,
            mtime_unix: row.mtime_unix,
            file_len: row.file_len,
            kind: kind.into(),
            hot,
            checkpoint,
        }
    }
}

struct WorkRow {
    job_id: String,
    adapter_id: String,
    source_id: String,
    file_path: String,
    mtime_unix: i64,
    file_len: i64,
    status: String,
    file_identity: String,
}

pub fn apply_file_progress(tx: &Transaction, item: &RebuildFileProgress) -> Result<(), String> {
    tx.execute(
        "UPDATE rebuild_job_files
         SET file_identity = ?1, mtime_unix = ?2, file_len = ?3, status = ?4
         WHERE job_id = ?5 AND file_path = ?6",
        params![
            item.file_identity,
            item.mtime_unix,
            item.file_len,
            item.status,
            item.job_id,
            item.file_path,
        ],
    )
    .map_err(|error| error.to_string())?;
    refresh_job_counts(tx, &item.job_id, &rfc3339_now())
}

fn is_sqlite_path(path: &str) -> bool {
    path.rsplit('.')
        .next()
        .is_some_and(|ext| ext.eq_ignore_ascii_case("sqlite") || ext.eq_ignore_ascii_case("vscdb"))
}

fn job_id(adapter_id: &str, source_id: &str) -> String {
    format!("rebuild:{adapter_id}:{source_id}")
}

fn ensure_job(
    tx: &Transaction,
    job_id: &str,
    scan_root: &str,
    adapter_id: &str,
    source_id: &str,
    now: &str,
) -> Result<(), String> {
    tx.execute(
        "INSERT INTO rebuild_jobs (
            job_id, scan_root, adapter_id, source_id, discovery_cursor,
            discovered_files, processed_files, status, created_at, updated_at
         ) VALUES (?1, ?2, ?3, ?4, 'enumerated', 0, 0, 'running', ?5, ?5)
         ON CONFLICT(job_id) DO UPDATE SET
            scan_root = excluded.scan_root,
            discovery_cursor = 'enumerated',
            status = CASE
                WHEN rebuild_jobs.status = 'failed' THEN rebuild_jobs.status
                ELSE 'running'
            END,
            updated_at = excluded.updated_at",
        params![job_id, scan_root, adapter_id, source_id, now],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

fn refresh_job_counts(tx: &Transaction, job_id: &str, now: &str) -> Result<(), String> {
    let discovered: i64 = tx
        .query_row(
            "SELECT COUNT(*) FROM rebuild_job_files WHERE job_id = ?1",
            params![job_id],
            |row| row.get(0),
        )
        .map_err(|error| error.to_string())?;
    let processed: i64 = tx
        .query_row(
            "SELECT COUNT(*) FROM rebuild_job_files
             WHERE job_id = ?1 AND status IN ('caught_up', 'skipped')",
            params![job_id],
            |row| row.get(0),
        )
        .map_err(|error| error.to_string())?;
    let pending: i64 = tx
        .query_row(
            "SELECT COUNT(*) FROM rebuild_job_files
             WHERE job_id = ?1 AND status IN ('pending', 'in_progress')",
            params![job_id],
            |row| row.get(0),
        )
        .map_err(|error| error.to_string())?;
    let status = if pending == 0 { "completed" } else { "running" };
    tx.execute(
        "UPDATE rebuild_jobs
         SET discovered_files = ?1, processed_files = ?2, status = ?3,
             discovery_cursor = 'enumerated', updated_at = ?4
         WHERE job_id = ?5",
        params![discovered, processed, status, now, job_id],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::local_store::{test_envelope_at, LocalStore};
    use chrono::Local;
    use protocol::Accuracy;

    fn open_store() -> (tempfile::TempDir, LocalStore) {
        let dir = tempfile::tempdir().unwrap();
        let store = LocalStore::open(dir.path()).unwrap();
        (dir, store)
    }

    fn file(adapter: &str, source: &str, path: &str, mtime: i64) -> DiscoveredSource {
        DiscoveredSource {
            adapter_id: adapter.into(),
            source_id: source.into(),
            scan_root: "/logs".into(),
            path: path.into(),
            mtime_unix: mtime,
            file_len: 10,
            kind: "jsonl".into(),
        }
    }

    fn progress(path: &str, mtime: i64, status: &str) -> RebuildFileProgress {
        RebuildFileProgress {
            job_id: job_id("dev.tokenshow.adapter.codex", "codex-jsonl"),
            file_path: path.into(),
            file_identity: format!("id-{path}"),
            mtime_unix: mtime,
            file_len: 10,
            status: status.into(),
        }
    }

    #[test]
    fn rebuild_resume_after_interrupt_keeps_remaining_files() {
        let dir = tempfile::tempdir().unwrap();
        let older = test_envelope_at("codex", "evt-old", &local_noon(1), 11, Accuracy::Exact);
        let newer = test_envelope_at("codex", "evt-new", &local_noon(0), 22, Accuracy::Exact);
        {
            let mut store = LocalStore::open(dir.path()).unwrap();
            store
                .discover_sources(&[
                    file(
                        "dev.tokenshow.adapter.codex",
                        "codex-jsonl",
                        "/logs/old.jsonl",
                        1,
                    ),
                    file(
                        "dev.tokenshow.adapter.codex",
                        "codex-jsonl",
                        "/logs/new.jsonl",
                        9,
                    ),
                ])
                .unwrap();
            let work = store.select_scan_work(1, 1).unwrap();
            assert_eq!(work[0].path.to_string_lossy(), "/logs/new.jsonl");
            assert!(work.iter().any(|item| !item.hot));
            store
                .commit_scan_batch(
                    &[newer.clone()],
                    &[],
                    &[progress("/logs/new.jsonl", 9, "caught_up")],
                )
                .unwrap();
            assert_eq!(
                store
                    .rebuild_file_status(
                        "dev.tokenshow.adapter.codex",
                        "codex-jsonl",
                        "/logs/old.jsonl"
                    )
                    .as_deref(),
                Some("pending")
            );
        }
        let mut store = LocalStore::open(dir.path()).unwrap();
        store
            .discover_sources(&[
                file(
                    "dev.tokenshow.adapter.codex",
                    "codex-jsonl",
                    "/logs/old.jsonl",
                    1,
                ),
                file(
                    "dev.tokenshow.adapter.codex",
                    "codex-jsonl",
                    "/logs/new.jsonl",
                    9,
                ),
            ])
            .unwrap();
        let work = store.select_scan_work(1, 4).unwrap();
        assert!(work
            .iter()
            .any(|item| item.path.ends_with("old.jsonl") && !item.hot));
        store
            .commit_scan_batch(
                &[older],
                &[],
                &[progress("/logs/old.jsonl", 1, "caught_up")],
            )
            .unwrap();
        assert_eq!(
            store
                .rebuild_job_status("dev.tokenshow.adapter.codex", "codex-jsonl")
                .as_deref(),
            Some("completed")
        );
        let snapshot = store
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.total_tokens, 33);
    }

    #[test]
    fn duplicate_rebuild_does_not_double_tokens() {
        let (_dir, mut store) = open_store();
        let event = test_envelope_at("codex", "evt-dup", &local_noon(0), 40, Accuracy::Exact);
        let sources = [file(
            "dev.tokenshow.adapter.codex",
            "codex-jsonl",
            "/logs/session.jsonl",
            5,
        )];
        store.discover_sources(&sources).unwrap();
        store
            .commit_scan_batch(
                &[event.clone()],
                &[],
                &[progress("/logs/session.jsonl", 5, "caught_up")],
            )
            .unwrap();
        store.discover_sources(&sources).unwrap();
        store
            .commit_scan_batch(
                &[event],
                &[],
                &[progress("/logs/session.jsonl", 5, "caught_up")],
            )
            .unwrap();
        let snapshot = store
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.today_tokens, 40);
        assert_eq!(snapshot.total_tokens, 40);
        assert_eq!(store.event_count(), 1);
    }

    fn local_noon(days_ago: i64) -> String {
        (Local::now().date_naive() - chrono::Duration::days(days_ago))
            .and_hms_opt(12, 0, 0)
            .unwrap()
            .and_local_timezone(Local)
            .unwrap()
            .to_rfc3339()
    }
}
