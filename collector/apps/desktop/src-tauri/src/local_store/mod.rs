//! Local SQLite repository for desktop usage facts and daily rollups.
//!
//! Local commit is the success criterion for collection. Upload ACK is independent
//! and must not be mixed into these totals. Old `usage-ledger.json` is never imported.

use std::collections::{BTreeMap, HashSet};
use std::path::{Path, PathBuf};
use std::time::Duration;

use chrono::{Local, NaiveDate};
use protocol::{EventEnvelope, EventPayload};
use rusqlite::{params, Connection, OptionalExtension, Transaction};
use wal_spool::SourceCheckpoint;

mod aggregate_activity;
mod rebuild;
mod retention;
#[cfg(test)]
mod retention_tests;
mod sync;
pub use retention::{AggregateSnapshot, PendingAggregate};

pub use rebuild::{DiscoveredSource, RebuildFileProgress, ScanWorkItem};
pub use sync::{DeliveryRecord, LeasedBatch};

use crate::pricing::{Catalog, CostCoverage, CostLedger};
use crate::usage_ledger::{
    accuracy_name, accuracy_rank, cost_units, event_tokens, local_date, AgentUsageSnapshot,
    DayUsage, DISPLAY_DAYS,
};

const DB_FILE: &str = "tokendance.sqlite3";
const SCHEMA_VERSION: i64 = 3;
const AGGREGATION_VERSION: i64 = 1;
const PARSE_VERSION: &str = "1";
const BUSY_TIMEOUT_MS: u64 = 5_000;

const SCHEMA_V1: &str = "
CREATE TABLE schema_meta (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    schema_version INTEGER NOT NULL,
    aggregation_version INTEGER NOT NULL,
    pricing_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE source_files (
    source_id TEXT NOT NULL,
    file_identity TEXT NOT NULL,
    path_template_id TEXT NOT NULL DEFAULT '',
    generation INTEGER NOT NULL,
    PRIMARY KEY (source_id, file_identity)
);
CREATE TABLE source_checkpoints (
    source_id TEXT NOT NULL,
    file_identity TEXT NOT NULL,
    generation INTEGER NOT NULL,
    file_offset INTEGER NOT NULL,
    file_len INTEGER NOT NULL,
    cursor TEXT,
    status TEXT NOT NULL,
    PRIMARY KEY (source_id, file_identity)
);
CREATE TABLE events (
    local_seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    agent_id TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    event_type TEXT NOT NULL,
    envelope_json TEXT NOT NULL,
    accuracy TEXT NOT NULL,
    adapter_id TEXT NOT NULL,
    adapter_version TEXT NOT NULL,
    parse_version TEXT NOT NULL
);
CREATE INDEX idx_events_agent_time ON events(agent_id, occurred_at, local_seq);
CREATE INDEX idx_events_type_time ON events(event_type, occurred_at);
CREATE TABLE daily_agent_metrics (
    day TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    aggregation_version INTEGER NOT NULL,
    tokens TEXT NOT NULL,
    accuracy INTEGER NOT NULL,
    cost_json TEXT NOT NULL,
    estimated_usd TEXT NOT NULL,
    estimated_requests INTEGER NOT NULL,
    unpriced_requests INTEGER NOT NULL,
    detailed_tokens TEXT NOT NULL,
    session_count INTEGER NOT NULL DEFAULT 0,
    turn_count INTEGER NOT NULL DEFAULT 0,
    skill_count INTEGER NOT NULL DEFAULT 0,
    code_added INTEGER NOT NULL DEFAULT 0,
    code_removed INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, agent_id, aggregation_version)
);
CREATE TABLE daily_model_metrics (
    day TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    aggregation_version INTEGER NOT NULL,
    tokens TEXT NOT NULL,
    request_count INTEGER NOT NULL,
    PRIMARY KEY (day, agent_id, model_id, aggregation_version)
);
CREATE TABLE daily_skill_metrics (
    day TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    skill_key TEXT NOT NULL,
    aggregation_version INTEGER NOT NULL,
    skill_public_name TEXT,
    use_count INTEGER NOT NULL,
    success_count INTEGER NOT NULL,
    PRIMARY KEY (day, agent_id, skill_key, aggregation_version)
);
";

const SCHEMA_V2: &str = "
ALTER TABLE source_checkpoints ADD COLUMN driver_checkpoint TEXT;
CREATE TABLE rebuild_jobs (
    job_id TEXT PRIMARY KEY,
    scan_root TEXT NOT NULL,
    adapter_id TEXT NOT NULL DEFAULT '',
    source_id TEXT NOT NULL DEFAULT '',
    discovery_cursor TEXT NOT NULL DEFAULT '',
    discovered_files INTEGER NOT NULL DEFAULT 0,
    processed_files INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    error_code TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_rebuild_jobs_source ON rebuild_jobs(adapter_id, source_id);
CREATE TABLE rebuild_job_files (
    job_id TEXT NOT NULL,
    file_path TEXT NOT NULL,
    file_identity TEXT NOT NULL DEFAULT '',
    mtime_unix INTEGER NOT NULL DEFAULT 0,
    file_len INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    error_code TEXT,
    PRIMARY KEY (job_id, file_path)
);
CREATE INDEX idx_rebuild_job_files_work ON rebuild_job_files(job_id, status, mtime_unix);
CREATE TABLE sync_targets (
    target_id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL UNIQUE,
    binding_generation INTEGER NOT NULL,
    installation_id TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE sync_delivery (
    target_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    local_seq INTEGER NOT NULL,
    status TEXT NOT NULL,
    lease_id TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0,
    lease_until INTEGER,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER,
    last_error TEXT,
    PRIMARY KEY (target_id, event_id)
);
CREATE UNIQUE INDEX idx_sync_delivery_event ON sync_delivery(event_id);
CREATE INDEX idx_sync_delivery_work ON sync_delivery(target_id, status, next_attempt_at, local_seq);
CREATE TABLE sync_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    active_target_id TEXT,
    sync_enabled INTEGER NOT NULL DEFAULT 1
);
INSERT INTO sync_state (id, active_target_id, sync_enabled) VALUES (1, NULL, 1);
";

pub struct LocalStore {
    conn: Connection,
    catalog: Catalog,
    pricing: CostLedger,
    dir: PathBuf,
    retention_clock: (chrono::DateTime<chrono::Utc>, std::time::Instant),
}

impl LocalStore {
    pub fn open(dir: &Path) -> Result<Self, String> {
        std::fs::create_dir_all(dir).map_err(|error| error.to_string())?;
        let path = dir.join(DB_FILE);
        let mut conn = Connection::open(&path).map_err(|error| error.to_string())?;
        conn.busy_timeout(Duration::from_millis(BUSY_TIMEOUT_MS))
            .map_err(|error| error.to_string())?;
        conn.execute_batch(
            "PRAGMA journal_mode=WAL;
             PRAGMA synchronous=FULL;
             PRAGMA busy_timeout=5000;
             PRAGMA foreign_keys=ON;",
        )
        .map_err(|error| error.to_string())?;
        migrate(&mut conn)?;
        let catalog = Catalog::load(dir);
        let mut pricing = load_pricing(&conn)?;
        pricing.reprice(&catalog);
        let mut store = Self {
            conn,
            catalog,
            pricing,
            dir: dir.to_path_buf(),
            retention_clock: (chrono::Utc::now(), std::time::Instant::now()),
        };
        store.reprice()?;
        Ok(store)
    }

    pub fn catalog(&self) -> &Catalog {
        &self.catalog
    }

    pub fn commit_batch(
        &mut self,
        events: &[EventEnvelope],
        checkpoints: &[SourceCheckpoint],
    ) -> Result<bool, String> {
        self.commit_batch_inner(events, checkpoints, &[], false)
    }

    pub fn commit_scan_batch(
        &mut self,
        events: &[EventEnvelope],
        checkpoints: &[SourceCheckpoint],
        progress: &[RebuildFileProgress],
    ) -> Result<bool, String> {
        self.commit_batch_inner(events, checkpoints, progress, false)
    }

    fn commit_batch_inner(
        &mut self,
        events: &[EventEnvelope],
        checkpoints: &[SourceCheckpoint],
        progress: &[RebuildFileProgress],
        abort: bool,
    ) -> Result<bool, String> {
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let mut pricing = self.pricing.clone();
        let mut changed = false;
        let mut dirty_agents = HashSet::new();
        let target_id = active_target_id(&tx)?;
        for event in events {
            let outcome = apply_event(&tx, event, &mut pricing, &self.catalog)?;
            changed |= outcome.changed;
            if outcome.inserted {
                retention::record(
                    &tx,
                    event,
                    target_id.as_deref().unwrap_or(""),
                    &self.catalog,
                )?;
                if let Some(target_id) = target_id.as_deref() {
                    enqueue_delivery(&tx, target_id, event)?;
                }
            }
            dirty_agents.insert(event.agent_id.clone());
        }
        for checkpoint in checkpoints {
            upsert_checkpoint(&tx, checkpoint)?;
        }
        for item in progress {
            rebuild::apply_file_progress(&tx, item)?;
        }
        for agent in dirty_agents {
            refresh_agent_coverage(&tx, &agent, &pricing)?;
        }
        save_pricing(&tx, &pricing)?;
        if abort {
            return Err("injected_abort".into());
        }
        tx.commit().map_err(|error| error.to_string())?;
        self.pricing = pricing;
        Ok(changed)
    }

    pub fn load_checkpoint(
        &self,
        source_id: &str,
        file_identity: &str,
    ) -> Option<SourceCheckpoint> {
        load_checkpoint(&self.conn, source_id, file_identity)
    }

    pub fn checkpoint_for_path(&self, source_id: &str, path: &Path) -> Option<SourceCheckpoint> {
        let meta = std::fs::metadata(path).ok()?;
        let identity = acquisition::file_identity(path, &meta);
        self.load_checkpoint(source_id, &identity)
    }

    pub fn event_count(&self) -> u64 {
        self.conn
            .query_row("SELECT COUNT(*) FROM events", [], |row| {
                row.get::<_, i64>(0)
            })
            .unwrap_or(0) as u64
    }

    pub fn agent_usage(&self, agent_id: &str, today: NaiveDate) -> Option<AgentUsageSnapshot> {
        let today_key = today.format("%Y-%m-%d").to_string();
        let mut stmt = self
            .conn
            .prepare(
                "SELECT day, tokens, accuracy, cost_json, estimated_usd, estimated_requests,
                        unpriced_requests, detailed_tokens
                 FROM daily_agent_metrics
                 WHERE agent_id = ?1 AND aggregation_version = ?2
                 ORDER BY day",
            )
            .ok()?;
        let rows = stmt
            .query_map(params![agent_id, AGGREGATION_VERSION], |row| {
                Ok(AgentDayRow {
                    day: row.get(0)?,
                    tokens: row.get(1)?,
                    accuracy: row.get(2)?,
                    cost_json: row.get(3)?,
                    estimated_usd: row.get(4)?,
                    estimated_requests: row.get(5)?,
                    unpriced_requests: row.get(6)?,
                    detailed_tokens: row.get(7)?,
                })
            })
            .ok()?;
        let mut days = BTreeMap::new();
        for row in rows.flatten() {
            days.insert(row.day.clone(), row);
        }
        if days.is_empty() {
            return None;
        }
        let mut total = 0u64;
        let mut accuracy = u8::MAX;
        let mut total_costs = BTreeMap::<String, u64>::new();
        let mut pricing = CostCoverage::default();
        let mut history_start = String::new();
        for (date, row) in &days {
            if date.as_str() > today_key.as_str() {
                continue;
            }
            if history_start.is_empty() {
                history_start = date.clone();
            }
            total = total.saturating_add(parse_u64_text(&row.tokens));
            if row.accuracy > 0 {
                accuracy = accuracy.min(row.accuracy as u8);
            }
            for (currency, amount) in parse_cost_json(&row.cost_json) {
                *total_costs.entry(currency).or_default() += amount;
            }
            pricing.add(&CostCoverage {
                estimated_usd: parse_u64_text(&row.estimated_usd),
                estimated_requests: row.estimated_requests as u64,
                unpriced_requests: row.unpriced_requests as u64,
                detailed_tokens: parse_u64_text(&row.detailed_tokens),
            });
        }
        let mut daily_usage = Vec::with_capacity(DISPLAY_DAYS as usize);
        for offset in (0..DISPLAY_DAYS).rev() {
            let date = (today - chrono::Duration::days(offset))
                .format("%Y-%m-%d")
                .to_string();
            let row = days.get(&date);
            daily_usage.push(DayUsage {
                pricing: row
                    .map(|row| CostCoverage {
                        estimated_usd: parse_u64_text(&row.estimated_usd),
                        estimated_requests: row.estimated_requests as u64,
                        unpriced_requests: row.unpriced_requests as u64,
                        detailed_tokens: parse_u64_text(&row.detailed_tokens),
                    })
                    .unwrap_or_default(),
                tokens: row.map_or(0, |row| parse_u64_text(&row.tokens)),
                costs: row
                    .map(|row| parse_cost_json(&row.cost_json))
                    .unwrap_or_default(),
                date,
            });
        }
        Some(AgentUsageSnapshot {
            today_tokens: daily_usage.last().map_or(0, |day| day.tokens),
            total_tokens: total,
            accuracy: accuracy_name(if accuracy == u8::MAX { 0 } else { accuracy }).into(),
            daily_usage,
            total_costs,
            pricing,
            history_start,
        })
    }

    pub fn apply_prices(&mut self, catalog: Catalog) -> Result<(), String> {
        let mut pricing = self.pricing.clone();
        pricing.reprice(&catalog);
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let agents = {
            let mut stmt = tx
                .prepare(
                    "SELECT DISTINCT agent_id FROM daily_agent_metrics WHERE aggregation_version = ?1",
                )
                .map_err(|error| error.to_string())?;
            let rows = stmt
                .query_map(params![AGGREGATION_VERSION], |row| row.get::<_, String>(0))
                .map_err(|error| error.to_string())?;
            rows.filter_map(Result::ok).collect::<Vec<_>>()
        };
        for agent in agents {
            refresh_agent_coverage(&tx, &agent, &pricing)?;
        }
        save_pricing(&tx, &pricing)?;
        tx.commit().map_err(|error| error.to_string())?;
        self.pricing = pricing;
        self.catalog = catalog;
        self.refresh_aggregate_prices()?;
        std::fs::write(
            self.dir.join("openrouter-prices.json"),
            serde_json::to_vec(&self.catalog).map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())
    }

    pub fn reprice(&mut self) -> Result<(), String> {
        let mut pricing = self.pricing.clone();
        pricing.reprice(&self.catalog);
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let agents = {
            let mut stmt = tx
                .prepare(
                    "SELECT DISTINCT agent_id FROM daily_agent_metrics WHERE aggregation_version = ?1",
                )
                .map_err(|error| error.to_string())?;
            let rows = stmt
                .query_map(params![AGGREGATION_VERSION], |row| row.get::<_, String>(0))
                .map_err(|error| error.to_string())?;
            rows.filter_map(Result::ok).collect::<Vec<_>>()
        };
        for agent in agents {
            refresh_agent_coverage(&tx, &agent, &pricing)?;
        }
        save_pricing(&tx, &pricing)?;
        tx.commit().map_err(|error| error.to_string())?;
        self.pricing = pricing;
        self.refresh_aggregate_prices()
    }
}

struct AgentDayRow {
    day: String,
    tokens: String,
    accuracy: i64,
    cost_json: String,
    estimated_usd: String,
    estimated_requests: i64,
    unpriced_requests: i64,
    detailed_tokens: String,
}

fn migrate(conn: &mut Connection) -> Result<(), String> {
    let current = match conn.query_row(
        "SELECT schema_version FROM schema_meta WHERE id = 1",
        [],
        |row| row.get::<_, i64>(0),
    ) {
        Ok(version) => version,
        Err(rusqlite::Error::QueryReturnedNoRows) => 0,
        Err(error) if error.to_string().contains("no such table") => 0,
        Err(error) => return Err(error.to_string()),
    };
    if current > SCHEMA_VERSION {
        return Err(format!("unsupported schema version {current}"));
    }
    let tx = conn.transaction().map_err(|error| error.to_string())?;
    if current == 0 {
        tx.execute_batch(SCHEMA_V1)
            .map_err(|error| error.to_string())?;
        tx.execute(
            "INSERT INTO schema_meta (id, schema_version, aggregation_version, pricing_json)
             VALUES (1, ?1, ?2, '{}')",
            params![SCHEMA_VERSION, AGGREGATION_VERSION],
        )
        .map_err(|error| error.to_string())?;
    }
    if current < 2 {
        tx.execute_batch(SCHEMA_V2)
            .map_err(|error| error.to_string())?;
    }
    if current < 3 {
        tx.execute_batch(retention::SCHEMA)
            .map_err(|error| error.to_string())?;
        retention::migrate_data(&tx)?;
    }
    if current != 0 && current < SCHEMA_VERSION {
        tx.execute(
            "UPDATE schema_meta SET schema_version = ?1 WHERE id = 1",
            params![SCHEMA_VERSION],
        )
        .map_err(|error| error.to_string())?;
    }
    tx.commit().map_err(|error| error.to_string())
}

fn load_pricing(conn: &Connection) -> Result<CostLedger, String> {
    let raw: String = conn
        .query_row(
            "SELECT pricing_json FROM schema_meta WHERE id = 1",
            [],
            |row| row.get(0),
        )
        .unwrap_or_else(|_| "{}".into());
    serde_json::from_str(&raw).or_else(|_| Ok(CostLedger::default()))
}

fn save_pricing(tx: &Transaction, pricing: &CostLedger) -> Result<(), String> {
    let payload = serde_json::to_string(pricing).map_err(|error| error.to_string())?;
    tx.execute(
        "UPDATE schema_meta SET pricing_json = ?1 WHERE id = 1",
        params![payload],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

struct ApplyOutcome {
    changed: bool,
    inserted: bool,
}

fn apply_event(
    tx: &Transaction,
    event: &EventEnvelope,
    pricing: &mut CostLedger,
    catalog: &Catalog,
) -> Result<ApplyOutcome, String> {
    let fresh = retention::fingerprint(tx, event)?;
    if !fresh
        && !tx
            .query_row(
                "SELECT EXISTS(SELECT 1 FROM events WHERE event_id=?1)",
                params![event.event_id],
                |r| r.get::<_, bool>(0),
            )
            .map_err(|e| e.to_string())?
    {
        return Ok(ApplyOutcome {
            changed: false,
            inserted: false,
        });
    }
    let envelope_json = serde_json::to_string(event).map_err(|error| error.to_string())?;
    let inserted = tx
        .execute(
            "INSERT OR IGNORE INTO events (
                event_id, agent_id, occurred_at, event_type, envelope_json, accuracy,
                adapter_id, adapter_version, parse_version
             ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
            params![
                event.event_id,
                event.agent_id,
                event.occurred_at,
                event_type_name(&event.payload),
                envelope_json,
                format!("{:?}", event.accuracy).to_lowercase(),
                event.adapter_id,
                event.adapter_version,
                PARSE_VERSION,
            ],
        )
        .map_err(|error| error.to_string())?
        > 0;
    if !inserted {
        maybe_enrich_unknown_model(tx, event)?;
    }
    let Some(date) = local_date(&event.occurred_at) else {
        return Ok(ApplyOutcome {
            changed: inserted,
            inserted,
        });
    };
    if date > Local::now().date_naive() {
        return Ok(ApplyOutcome {
            changed: inserted,
            inserted,
        });
    }
    let date_key = date.format("%Y-%m-%d").to_string();
    let tokens = event_tokens(event).unwrap_or(0);
    let pricing_changed = pricing.record(event, &date_key, tokens, catalog);
    if !inserted {
        return Ok(ApplyOutcome {
            changed: pricing_changed,
            inserted,
        });
    }
    bump_agent_metrics(tx, event, &date_key, tokens)?;
    bump_model_metrics(tx, event, &date_key, tokens)?;
    bump_skill_metrics(tx, event, &date_key)?;
    Ok(ApplyOutcome {
        changed: true,
        inserted: true,
    })
}

fn maybe_enrich_unknown_model(tx: &Transaction, event: &EventEnvelope) -> Result<(), String> {
    let EventPayload::ModelUsageRecorded(new_payload) = &event.payload else {
        return Ok(());
    };
    if new_payload.model_id == "unknown" {
        return Ok(());
    }
    let existing: Option<String> = tx
        .query_row(
            "SELECT envelope_json FROM events WHERE event_id = ?1",
            params![event.event_id],
            |row| row.get(0),
        )
        .optional()
        .map_err(|error| error.to_string())?;
    let Some(raw) = existing else {
        return Ok(());
    };
    let mut old: EventEnvelope = serde_json::from_str(&raw).map_err(|error| error.to_string())?;
    let EventPayload::ModelUsageRecorded(old_payload) = &mut old.payload else {
        return Ok(());
    };
    if old_payload.model_id != "unknown" {
        return Ok(());
    }
    old_payload.model_id = new_payload.model_id.clone();
    let envelope_json = serde_json::to_string(&old).map_err(|error| error.to_string())?;
    tx.execute(
        "UPDATE events SET envelope_json = ?1 WHERE event_id = ?2",
        params![envelope_json, event.event_id],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

fn bump_agent_metrics(
    tx: &Transaction,
    event: &EventEnvelope,
    date_key: &str,
    tokens: u64,
) -> Result<(), String> {
    let (mut row_tokens, mut accuracy, mut cost_json, mut session_count, mut turn_count, mut skill_count, mut code_added, mut code_removed) =
        tx.query_row(
            "SELECT tokens, accuracy, cost_json, session_count, turn_count, skill_count, code_added, code_removed
             FROM daily_agent_metrics
             WHERE day = ?1 AND agent_id = ?2 AND aggregation_version = ?3",
            params![date_key, event.agent_id, AGGREGATION_VERSION],
            |row| {
                Ok((
                    row.get::<_, String>(0)?,
                    row.get::<_, i64>(1)?,
                    row.get::<_, String>(2)?,
                    row.get::<_, i64>(3)?,
                    row.get::<_, i64>(4)?,
                    row.get::<_, i64>(5)?,
                    row.get::<_, i64>(6)?,
                    row.get::<_, i64>(7)?,
                ))
            },
        )
        .optional()
        .map_err(|error| error.to_string())?
        .unwrap_or_else(|| ("0".into(), 0, "{}".into(), 0, 0, 0, 0, 0));
    if tokens > 0 {
        row_tokens = add_u64_text(&row_tokens, tokens);
        let rank = i64::from(accuracy_rank(&event.accuracy));
        accuracy = if accuracy == 0 {
            rank
        } else {
            accuracy.min(rank)
        };
    }
    if let EventPayload::CostRecorded(payload) = &event.payload {
        if payload.currency.len() == 3 && payload.currency.bytes().all(|c| c.is_ascii_uppercase()) {
            if let Some(amount) = cost_units(&payload.amount) {
                let mut costs = parse_cost_json(&cost_json);
                let total = costs.entry(payload.currency.clone()).or_default();
                *total = total.saturating_add(amount);
                cost_json = cost_map_json(&costs);
            }
        }
    }
    match &event.payload {
        EventPayload::SessionStarted(_) => session_count = session_count.saturating_add(1),
        EventPayload::TurnCompleted(_) => turn_count = turn_count.saturating_add(1),
        EventPayload::SkillInvoked(_) => skill_count = skill_count.saturating_add(1),
        EventPayload::CodeChanged(payload) => {
            if let Ok(added) = payload.added_lines.parse::<i64>() {
                code_added = code_added.saturating_add(added);
            }
            if let Ok(removed) = payload.removed_lines.parse::<i64>() {
                code_removed = code_removed.saturating_add(removed);
            }
        }
        _ => {}
    }
    tx.execute(
        "INSERT INTO daily_agent_metrics (
            day, agent_id, aggregation_version, tokens, accuracy, cost_json,
            estimated_usd, estimated_requests, unpriced_requests, detailed_tokens,
            session_count, turn_count, skill_count, code_added, code_removed
         ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, '0', 0, 0, '0', ?7, ?8, ?9, ?10, ?11)
         ON CONFLICT(day, agent_id, aggregation_version) DO UPDATE SET
            tokens = excluded.tokens,
            accuracy = excluded.accuracy,
            cost_json = excluded.cost_json,
            session_count = excluded.session_count,
            turn_count = excluded.turn_count,
            skill_count = excluded.skill_count,
            code_added = excluded.code_added,
            code_removed = excluded.code_removed",
        params![
            date_key,
            event.agent_id,
            AGGREGATION_VERSION,
            row_tokens,
            accuracy,
            cost_json,
            session_count,
            turn_count,
            skill_count,
            code_added,
            code_removed,
        ],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

fn bump_model_metrics(
    tx: &Transaction,
    event: &EventEnvelope,
    date_key: &str,
    tokens: u64,
) -> Result<(), String> {
    let EventPayload::ModelUsageRecorded(payload) = &event.payload else {
        return Ok(());
    };
    let current: String = tx
        .query_row(
            "SELECT tokens FROM daily_model_metrics
             WHERE day = ?1 AND agent_id = ?2 AND model_id = ?3 AND aggregation_version = ?4",
            params![
                date_key,
                event.agent_id,
                payload.model_id,
                AGGREGATION_VERSION
            ],
            |row| row.get(0),
        )
        .optional()
        .map_err(|error| error.to_string())?
        .unwrap_or_else(|| "0".into());
    tx.execute(
        "INSERT INTO daily_model_metrics (
            day, agent_id, model_id, aggregation_version, tokens, request_count
         ) VALUES (?1, ?2, ?3, ?4, ?5, 1)
         ON CONFLICT(day, agent_id, model_id, aggregation_version) DO UPDATE SET
            tokens = excluded.tokens,
            request_count = request_count + 1",
        params![
            date_key,
            event.agent_id,
            payload.model_id,
            AGGREGATION_VERSION,
            add_u64_text(&current, tokens),
        ],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

fn bump_skill_metrics(
    tx: &Transaction,
    event: &EventEnvelope,
    date_key: &str,
) -> Result<(), String> {
    let EventPayload::SkillInvoked(payload) = &event.payload else {
        return Ok(());
    };
    let success = i64::from(payload.success);
    tx.execute(
        "INSERT INTO daily_skill_metrics (
            day, agent_id, skill_key, aggregation_version, skill_public_name, use_count, success_count
         ) VALUES (?1, ?2, ?3, ?4, ?5, 1, ?6)
         ON CONFLICT(day, agent_id, skill_key, aggregation_version) DO UPDATE SET
            use_count = use_count + 1,
            success_count = success_count + excluded.success_count,
            skill_public_name = COALESCE(excluded.skill_public_name, skill_public_name)",
        params![
            date_key,
            event.agent_id,
            payload.skill_key,
            AGGREGATION_VERSION,
            payload.skill_public_name,
            success,
        ],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

fn upsert_checkpoint(tx: &Transaction, checkpoint: &SourceCheckpoint) -> Result<(), String> {
    tx.execute(
        "INSERT INTO source_files (source_id, file_identity, path_template_id, generation)
         VALUES (?1, ?2, ?3, ?4)
         ON CONFLICT(source_id, file_identity) DO UPDATE SET
            path_template_id = excluded.path_template_id,
            generation = MAX(generation, excluded.generation)",
        params![
            checkpoint.source_id,
            checkpoint.file_identity,
            checkpoint.path_template_id,
            checkpoint.generation as i64,
        ],
    )
    .map_err(|error| error.to_string())?;
    let driver_checkpoint = checkpoint
        .driver_checkpoint
        .as_ref()
        .map(serde_json::to_string)
        .transpose()
        .map_err(|error| error.to_string())?;
    tx.execute(
        "INSERT INTO source_checkpoints (
            source_id, file_identity, generation, file_offset, file_len, cursor, status, driver_checkpoint
         ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
         ON CONFLICT(source_id, file_identity) DO UPDATE SET
            generation = excluded.generation,
            file_offset = excluded.file_offset,
            file_len = excluded.file_len,
            cursor = excluded.cursor,
            status = excluded.status,
            driver_checkpoint = excluded.driver_checkpoint
         WHERE excluded.generation > source_checkpoints.generation
            OR (excluded.generation = source_checkpoints.generation
                AND excluded.file_offset >= source_checkpoints.file_offset)",
        params![
            checkpoint.source_id,
            checkpoint.file_identity,
            checkpoint.generation as i64,
            checkpoint.offset as i64,
            checkpoint.file_len as i64,
            checkpoint.last_record_hash,
            format!("{:?}", checkpoint.status),
            driver_checkpoint,
        ],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

fn load_checkpoint(
    conn: &Connection,
    source_id: &str,
    file_identity: &str,
) -> Option<SourceCheckpoint> {
    conn.query_row(
        "SELECT COALESCE(f.path_template_id, c.source_id), c.generation, c.file_offset, c.file_len,
                c.cursor, c.status, c.driver_checkpoint
         FROM source_checkpoints c
         LEFT JOIN source_files f
            ON f.source_id = c.source_id AND f.file_identity = c.file_identity
         WHERE c.source_id = ?1 AND c.file_identity = ?2",
        params![source_id, file_identity],
        |row| {
            let driver: Option<String> = row.get(6)?;
            Ok(SourceCheckpoint {
                source_id: source_id.to_string(),
                path_template_id: row.get(0)?,
                file_identity: file_identity.to_string(),
                generation: row.get::<_, i64>(1)? as u64,
                offset: row.get::<_, i64>(2)? as u64,
                file_len: row.get::<_, i64>(3)? as u64,
                last_record_hash: row.get(4)?,
                driver_checkpoint: driver.and_then(|raw| serde_json::from_str(&raw).ok()),
                status: parse_checkpoint_status(&row.get::<_, String>(5)?),
            })
        },
    )
    .optional()
    .ok()
    .flatten()
}

fn parse_checkpoint_status(raw: &str) -> protocol::SourceCheckpointStatus {
    match raw {
        "Discovered" => protocol::SourceCheckpointStatus::Discovered,
        "Scanning" => protocol::SourceCheckpointStatus::Scanning,
        "Stale" => protocol::SourceCheckpointStatus::Stale,
        "Rotated" => protocol::SourceCheckpointStatus::Rotated,
        "Truncated" => protocol::SourceCheckpointStatus::Truncated,
        "PermissionDenied" => protocol::SourceCheckpointStatus::PermissionDenied,
        "Incompatible" => protocol::SourceCheckpointStatus::Incompatible,
        _ => protocol::SourceCheckpointStatus::Current,
    }
}

fn active_target_id(tx: &Transaction) -> Result<Option<String>, String> {
    tx.query_row(
        "SELECT active_target_id FROM sync_state WHERE id = 1",
        [],
        |row| row.get::<_, Option<String>>(0),
    )
    .map_err(|error| error.to_string())
}

fn enqueue_delivery(
    tx: &Transaction,
    target_id: &str,
    event: &EventEnvelope,
) -> Result<(), String> {
    let local_seq: i64 = tx
        .query_row(
            "SELECT local_seq FROM events WHERE event_id = ?1",
            params![event.event_id],
            |row| row.get(0),
        )
        .map_err(|error| error.to_string())?;
    tx.execute(
        "INSERT OR IGNORE INTO sync_delivery (target_id, event_id, local_seq, status)
         VALUES (?1, ?2, ?3, 'pending')",
        params![target_id, event.event_id, local_seq],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

pub(crate) fn unix_now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.as_secs() as i64)
        .unwrap_or(0)
}

pub(crate) fn rfc3339_now() -> String {
    chrono::Utc::now().to_rfc3339()
}

fn refresh_agent_coverage(
    tx: &Transaction,
    agent_id: &str,
    pricing: &CostLedger,
) -> Result<(), String> {
    let recorded_days = {
        let mut stmt = tx
            .prepare(
                "SELECT day, cost_json FROM daily_agent_metrics
                 WHERE agent_id = ?1 AND aggregation_version = ?2",
            )
            .map_err(|error| error.to_string())?;
        let rows = stmt
            .query_map(params![agent_id, AGGREGATION_VERSION], |row| {
                Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
            })
            .map_err(|error| error.to_string())?;
        rows.filter_map(Result::ok)
            .filter(|(_, costs)| !parse_cost_json(costs).is_empty())
            .map(|(day, _)| day)
            .collect::<HashSet<_>>()
    };
    let coverage = pricing.days(agent_id, &recorded_days);
    for (day, day_coverage) in coverage {
        tx.execute(
            "UPDATE daily_agent_metrics
             SET estimated_usd = ?1, estimated_requests = ?2, unpriced_requests = ?3, detailed_tokens = ?4
             WHERE day = ?5 AND agent_id = ?6 AND aggregation_version = ?7",
            params![
                day_coverage.estimated_usd.to_string(),
                day_coverage.estimated_requests as i64,
                day_coverage.unpriced_requests as i64,
                day_coverage.detailed_tokens.to_string(),
                day,
                agent_id,
                AGGREGATION_VERSION,
            ],
        )
        .map_err(|error| error.to_string())?;
    }
    Ok(())
}

fn event_type_name(payload: &EventPayload) -> &'static str {
    match payload {
        EventPayload::SessionStarted(_) => "session_started",
        EventPayload::SessionEnded(_) => "session_ended",
        EventPayload::TurnStarted(_) => "turn_started",
        EventPayload::TurnCompleted(_) => "turn_completed",
        EventPayload::ModelUsageRecorded(_) => "model_usage_recorded",
        EventPayload::ToolInvoked(_) => "tool_invoked",
        EventPayload::SkillInvoked(_) => "skill_invoked",
        EventPayload::CodeChanged(_) => "code_changed",
        EventPayload::CostRecorded(_) => "cost_recorded",
        EventPayload::AgentSpawned(_) => "agent_spawned",
    }
}

fn add_u64_text(current: &str, add: u64) -> String {
    current
        .parse::<u128>()
        .unwrap_or(0)
        .saturating_add(u128::from(add))
        .to_string()
}

fn parse_u64_text(current: &str) -> u64 {
    current
        .parse::<u128>()
        .ok()
        .and_then(|value| u64::try_from(value).ok())
        .unwrap_or(u64::MAX)
}

fn parse_cost_json(raw: &str) -> BTreeMap<String, u64> {
    serde_json::from_str::<BTreeMap<String, String>>(raw)
        .unwrap_or_default()
        .into_iter()
        .map(|(currency, amount)| (currency, parse_u64_text(&amount)))
        .collect()
}

fn cost_map_json(costs: &BTreeMap<String, u64>) -> String {
    let as_text: BTreeMap<String, String> = costs
        .iter()
        .map(|(currency, amount)| (currency.clone(), amount.to_string()))
        .collect();
    serde_json::to_string(&as_text).unwrap_or_else(|_| "{}".into())
}

#[cfg(test)]
pub(crate) fn test_envelope(
    agent_id: &str,
    event_id: &str,
    total: u64,
    accuracy: protocol::Accuracy,
) -> EventEnvelope {
    let occurred_at = chrono::Local::now()
        .date_naive()
        .and_hms_opt(12, 0, 0)
        .unwrap()
        .and_local_timezone(chrono::Local)
        .unwrap()
        .to_rfc3339();
    test_envelope_at(agent_id, event_id, &occurred_at, total, accuracy)
}

#[cfg(test)]
pub(crate) fn test_envelope_at(
    agent_id: &str,
    event_id: &str,
    occurred_at: &str,
    total: u64,
    accuracy: protocol::Accuracy,
) -> EventEnvelope {
    use protocol::{EventSource, SourceKind, TokenUsage};
    EventEnvelope {
        schema_version: "1.0".into(),
        event_id: event_id.into(),
        adapter_id: "adapter-test".into(),
        adapter_version: "1.0.0".into(),
        agent_id: agent_id.into(),
        agent_version: None,
        installation_id: "ins_test".into(),
        occurred_at: occurred_at.into(),
        session_hash: None,
        turn_hash: None,
        tool_call_hash: None,
        source: EventSource {
            kind: SourceKind::Otlp,
            cursor_hmac: String::new(),
            raw_fingerprint_hmac: String::new(),
        },
        accuracy,
        payload: EventPayload::ModelUsageRecorded(protocol::ModelUsageRecordedPayload {
            provider_id: "test".into(),
            model_id: "test-model".into(),
            tokens: TokenUsage {
                input_tokens: Some("10".into()),
                output_tokens: Some("5".into()),
                cache_read_tokens: None,
                cache_write_tokens: None,
                reasoning_tokens: None,
                tool_tokens: None,
                total_tokens: Some(total.to_string()),
            },
        }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use protocol::Accuracy;

    fn envelope(
        agent_id: &str,
        occurred_at: &str,
        total: u64,
        accuracy: Accuracy,
    ) -> EventEnvelope {
        test_envelope_at(
            agent_id,
            &format!("evt-{agent_id}-{occurred_at}-{total}"),
            occurred_at,
            total,
            accuracy,
        )
    }

    fn today_local_noon(days_ago: i64) -> String {
        (Local::now().date_naive() - chrono::Duration::days(days_ago))
            .and_hms_opt(12, 0, 0)
            .unwrap()
            .and_local_timezone(Local)
            .unwrap()
            .to_rfc3339()
    }

    fn open_store() -> (tempfile::TempDir, LocalStore) {
        let dir = tempfile::tempdir().unwrap();
        let store = LocalStore::open(dir.path()).unwrap();
        (dir, store)
    }

    #[test]
    fn opens_wal_full_sync_and_creates_schema() {
        let (_dir, store) = open_store();
        let journal: String = store
            .conn
            .pragma_query_value(None, "journal_mode", |row| row.get(0))
            .unwrap();
        assert_eq!(journal.to_lowercase(), "wal");
        let sync: i64 = store
            .conn
            .pragma_query_value(None, "synchronous", |row| row.get(0))
            .unwrap();
        assert_eq!(sync, 2);
        let version: i64 = store
            .conn
            .query_row(
                "SELECT schema_version FROM schema_meta WHERE id = 1",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(version, SCHEMA_VERSION);
    }

    #[test]
    fn insert_is_idempotent_by_event_id() {
        let (_dir, mut store) = open_store();
        let event = envelope("codex", &today_local_noon(0), 100, Accuracy::Exact);
        assert!(store.commit_batch(&[event.clone()], &[]).unwrap());
        assert!(!store.commit_batch(&[event], &[]).unwrap());
        let snapshot = store
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.today_tokens, 100);
        assert_eq!(snapshot.total_tokens, 100);
        assert_eq!(snapshot.accuracy, "exact");
    }

    #[test]
    fn crash_safe_transaction_commits_or_nothing() {
        let (_dir, mut store) = open_store();
        let first = envelope("codex", &today_local_noon(0), 40, Accuracy::Exact);
        let second = envelope("codex", &today_local_noon(0), 60, Accuracy::Derived);
        let err = store
            .commit_batch_inner(&[first, second], &[], &[], true)
            .unwrap_err();
        assert_eq!(err, "injected_abort");
        assert!(store
            .agent_usage("codex", Local::now().date_naive())
            .is_none());
        let count: i64 = store
            .conn
            .query_row("SELECT COUNT(*) FROM events", [], |row| row.get(0))
            .unwrap();
        assert_eq!(count, 0);
    }

    #[test]
    fn stats_survive_restart_without_json_ledger() {
        let dir = tempfile::tempdir().unwrap();
        let event = envelope("codex", &today_local_noon(1), 42, Accuracy::Derived);
        {
            let mut store = LocalStore::open(dir.path()).unwrap();
            store.commit_batch(&[event], &[]).unwrap();
        }
        assert!(!dir.path().join("usage-ledger.json").exists());
        let store = LocalStore::open(dir.path()).unwrap();
        let snapshot = store
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.total_tokens, 42);
        assert_eq!(snapshot.accuracy, "derived");
        assert_eq!(
            snapshot
                .daily_usage
                .iter()
                .map(|day| day.tokens)
                .sum::<u64>(),
            42
        );
    }

    #[test]
    fn json_ledger_is_not_imported_as_facts() {
        let dir = tempfile::tempdir().unwrap();
        let date = Local::now().format("%Y-%m-%d").to_string();
        let fake = serde_json::json!({
            "days": {date.clone(): {"codex": {"tokens": 99999, "accuracy": 4}}},
            "seen": {date: ["old-id"]}
        });
        std::fs::write(
            dir.path().join("usage-ledger.json"),
            serde_json::to_vec(&fake).unwrap(),
        )
        .unwrap();
        let store = LocalStore::open(dir.path()).unwrap();
        assert!(store
            .agent_usage("codex", Local::now().date_naive())
            .is_none());
    }

    #[test]
    fn costs_use_fixed_precision_and_are_not_sql_floats() {
        let (_dir, mut store) = open_store();
        let mut event = envelope("codex", &today_local_noon(0), 0, Accuracy::Exact);
        event.payload = EventPayload::CostRecorded(protocol::CostRecordedPayload {
            amount: "1.23456789".into(),
            currency: "USD".into(),
            source: protocol::CostSource::ProviderReported,
            discount_amount: None,
        });
        store.commit_batch(&[event], &[]).unwrap();
        let snapshot = store
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(snapshot.total_costs["USD"], 123456789);
        let stored: String = store
            .conn
            .query_row(
                "SELECT cost_json FROM daily_agent_metrics WHERE agent_id = 'codex'",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert!(stored.contains("123456789"), "{stored}");
        assert!(!stored.contains("1.23456789"), "{stored}");
    }

    #[test]
    fn weakest_accuracy_wins_and_token_parts_sum() {
        let (_dir, mut store) = open_store();
        store
            .commit_batch(
                &[
                    envelope("codex", &today_local_noon(0), 10, Accuracy::Exact),
                    envelope("codex", &today_local_noon(0), 20, Accuracy::Estimated),
                ],
                &[],
            )
            .unwrap();
        let mut parts = envelope("cursor", &today_local_noon(0), 0, Accuracy::Derived);
        if let EventPayload::ModelUsageRecorded(payload) = &mut parts.payload {
            payload.tokens.total_tokens = None;
            payload.tokens.cache_read_tokens = Some("7".into());
        }
        store.commit_batch(&[parts], &[]).unwrap();
        let codex = store
            .agent_usage("codex", Local::now().date_naive())
            .unwrap();
        assert_eq!(codex.today_tokens, 30);
        assert_eq!(codex.accuracy, "estimated");
        let cursor = store
            .agent_usage("cursor", Local::now().date_naive())
            .unwrap();
        assert_eq!(cursor.today_tokens, 22);
    }
}
