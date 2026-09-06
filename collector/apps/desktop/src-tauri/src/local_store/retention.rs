//! Seven UTC days of detail; durable, account-partitioned aggregate snapshots.
use super::*;
use chrono::Utc;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

pub(super) const SCHEMA: &str = "
CREATE TABLE event_fingerprints (digest BLOB PRIMARY KEY CHECK(length(digest)=32)) WITHOUT ROWID;
CREATE TABLE aggregate_days (
 owner TEXT NOT NULL, day TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
 acked_revision INTEGER NOT NULL DEFAULT 0, payload TEXT NOT NULL,
 retry_at INTEGER NOT NULL DEFAULT 0, last_error TEXT,
 PRIMARY KEY(owner,day)
);
CREATE TABLE retention_state (id INTEGER PRIMARY KEY CHECK(id=1), last_day TEXT NOT NULL);
CREATE TABLE aggregate_pricing(owner TEXT NOT NULL,day TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(owner,day));
CREATE TABLE aggregate_activity(owner TEXT NOT NULL,day TEXT NOT NULL,agent TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(owner,day,agent));
CREATE INDEX idx_events_retention ON events(occurred_at,local_seq);
";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AggregateRow {
    pub kind: String,
    pub agent_id: String,
    #[serde(default)]
    pub provider_id: String,
    #[serde(default)]
    pub model_id: String,
    #[serde(default)]
    pub skill_key: String,
    #[serde(default)]
    pub skill_name: String,
    pub metrics: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AggregateSnapshot {
    pub schema_version: u32,
    pub day: String,
    pub revision: i64,
    pub rows: Vec<AggregateRow>,
}

#[derive(Debug, Clone)]
pub struct PendingAggregate {
    pub owner: String,
    pub snapshot: AggregateSnapshot,
}

fn add(row: &mut AggregateRow, key: &str, value: u64) -> Result<(), String> {
    let old = row
        .metrics
        .get(key)
        .map(|v| v.parse::<u64>())
        .transpose()
        .map_err(|_| "INVALID_AGGREGATE_NUMBER")?
        .unwrap_or(0);
    row.metrics.insert(
        key.into(),
        old.checked_add(value)
            .ok_or("AGGREGATE_OVERFLOW")?
            .to_string(),
    );
    Ok(())
}

fn apply(row: &mut AggregateRow, e: &EventEnvelope) -> Result<(), String> {
    let trusted = matches!(
        e.accuracy,
        protocol::Accuracy::Exact | protocol::Accuracy::Derived
    );
    match &e.payload {
        EventPayload::ModelUsageRecorded(p) => {
            let total = event_tokens(e).ok_or("INVALID_TOKEN_TOTAL")?;
            let key = match e.accuracy {
                protocol::Accuracy::Exact => "exact_token_total",
                protocol::Accuracy::Derived => "derived_token_total",
                _ => "estimated_token_total",
            };
            add(row, key, total)?;
            add(row, "model_request_count", 1)?;
            if trusted {
                for (key, part) in [
                    ("token_input_total", &p.tokens.input_tokens),
                    ("token_output_total", &p.tokens.output_tokens),
                    ("token_cache_read_total", &p.tokens.cache_read_tokens),
                    ("token_cache_write_total", &p.tokens.cache_write_tokens),
                    ("token_reasoning_total", &p.tokens.reasoning_tokens),
                ] {
                    if let Some(n) = part {
                        add(row, key, n.parse().map_err(|_| "INVALID_TOKEN_PART")?)?;
                    }
                }
            }
        }
        EventPayload::ToolInvoked(_) => add(row, "tool_call_count", 1)?,
        EventPayload::SkillInvoked(p) => {
            if row.kind == "skill" {
                add(row, "use_count", 1)?;
                if let Some(ms) = &p.duration_ms {
                    add(
                        row,
                        "duration_ms",
                        ms.parse().map_err(|_| "INVALID_DURATION")?,
                    )?;
                }
                let key = match e.accuracy {
                    protocol::Accuracy::Exact => "exact_use_count",
                    protocol::Accuracy::Derived => "derived_use_count",
                    protocol::Accuracy::Correlated => "correlated_use_count",
                    _ => "estimated_use_count",
                };
                add(row, key, 1)?;
                add(
                    row,
                    if p.success {
                        "success_count"
                    } else {
                        "failure_count"
                    },
                    1,
                )?;
            } else {
                add(row, "skill_use_count", 1)?;
            }
        }
        EventPayload::CodeChanged(p) => {
            if trusted {
                if let Some(n) = &p.generated_lines {
                    add(
                        row,
                        "code_generated_lines",
                        n.parse().map_err(|_| "INVALID_CODE_COUNT")?,
                    )?;
                }
                if let Some(n) = &p.accepted_lines {
                    add(
                        row,
                        "code_accepted_lines",
                        n.parse().map_err(|_| "INVALID_CODE_COUNT")?,
                    )?;
                }
            } else if e.accuracy == protocol::Accuracy::Correlated {
                if let Some(n) = &p.generated_lines {
                    add(
                        row,
                        "correlated_code_lines",
                        n.parse().map_err(|_| "INVALID_CODE_COUNT")?,
                    )?;
                }
            }
        }
        EventPayload::CostRecorded(p) if p.currency == "USD" => add(
            row,
            "cost_usd_units",
            cost_units(&p.amount).ok_or("INVALID_COST")?,
        )?,
        _ => (),
    }
    Ok(())
}

pub(super) fn fingerprint(tx: &Transaction, e: &EventEnvelope) -> Result<bool, String> {
    let digest = Sha256::digest(e.event_id.as_bytes());
    tx.execute(
        "INSERT OR IGNORE INTO event_fingerprints(digest) VALUES(?1)",
        params![digest.as_slice()],
    )
    .map(|n| n != 0)
    .map_err(|e| e.to_string())
}

pub(super) fn record(
    tx: &Transaction,
    e: &EventEnvelope,
    owner: &str,
    catalog: &Catalog,
) -> Result<(), String> {
    let day = chrono::DateTime::parse_from_rfc3339(&e.occurred_at)
        .map_err(|_| "INVALID_EVENT_TIME")?
        .with_timezone(&Utc)
        .format("%Y-%m-%d")
        .to_string();
    let mut prices = load_bucket_prices(tx, owner, &day)?;
    prices.record(e, &day, event_tokens(e).unwrap_or(0), catalog);
    store_bucket_prices(tx, owner, &day, &prices)?;
    let stored: Option<String> = tx
        .query_row(
            "SELECT payload FROM aggregate_days WHERE owner=?1 AND day=?2",
            params![owner, day],
            |r| r.get(0),
        )
        .optional()
        .map_err(|e| e.to_string())?;
    let mut rows: Vec<AggregateRow> = stored
        .map(|v| serde_json::from_str(&v))
        .transpose()
        .map_err(|e| e.to_string())?
        .unwrap_or_default();
    let mut identities = vec![AggregateRow {
        kind: "agent".into(),
        agent_id: e.agent_id.clone(),
        ..Default::default()
    }];
    if let EventPayload::ModelUsageRecorded(p) = &e.payload {
        identities.push(AggregateRow {
            kind: "model".into(),
            agent_id: e.agent_id.clone(),
            provider_id: p.provider_id.clone(),
            model_id: p.model_id.clone(),
            ..Default::default()
        });
    }
    if let EventPayload::SkillInvoked(p) = &e.payload {
        identities.push(AggregateRow {
            kind: "skill".into(),
            agent_id: e.agent_id.clone(),
            skill_key: p.skill_key.clone(),
            skill_name: p.skill_public_name.clone().unwrap_or_default(),
            ..Default::default()
        });
    }
    for identity in identities {
        let index = rows.iter().position(|r| {
            r.kind == identity.kind
                && r.agent_id == identity.agent_id
                && r.provider_id == identity.provider_id
                && r.model_id == identity.model_id
                && r.skill_key == identity.skill_key
        });
        let index = index.unwrap_or_else(|| {
            rows.push(identity);
            rows.len() - 1
        });
        apply(&mut rows[index], e)?;
        if rows[index].kind == "agent" {
            super::aggregate_activity::record(tx, owner, &day, e, &mut rows[index].metrics)?;
        }
    }
    tx.execute("INSERT INTO aggregate_days(owner,day,payload) VALUES(?1,?2,?3)
        ON CONFLICT(owner,day) DO UPDATE SET payload=excluded.payload,revision=revision+1,retry_at=0,last_error=NULL", params![owner,day,serde_json::to_string(&rows).map_err(|e| e.to_string())?])
        .map_err(|e| e.to_string())?;
    refresh_bucket_price(tx, owner, &day, &prices)?;
    Ok(())
}

pub(super) fn migrate_data(tx: &Transaction) -> Result<(), String> {
    // Existing SQLite facts are replayed once, preserving ownership. JSON ledgers
    // remain excluded. Paging prevents an unbounded migration memory spike.
    let mut after = 0i64;
    loop {
        let batch = {
            let mut stmt = tx.prepare("SELECT e.local_seq,e.envelope_json,COALESCE(d.target_id,'') FROM events e
                LEFT JOIN sync_delivery d ON d.event_id=e.event_id WHERE e.local_seq>?1 ORDER BY e.local_seq LIMIT 500").map_err(|e|e.to_string())?;
            let rows = stmt
                .query_map(params![after], |r| {
                    Ok((
                        r.get::<_, i64>(0)?,
                        r.get::<_, String>(1)?,
                        r.get::<_, String>(2)?,
                    ))
                })
                .map_err(|e| e.to_string())?;
            rows.collect::<Result<Vec<_>, _>>()
                .map_err(|e| e.to_string())?
        };
        if batch.is_empty() {
            break;
        }
        for (seq, raw, owner) in batch {
            let e: EventEnvelope = serde_json::from_str(&raw).map_err(|e| e.to_string())?;
            fingerprint(tx, &e)?;
            record(tx, &e, &owner, &Catalog::default())?;
            after = seq;
        }
    }
    Ok(())
}

pub(super) fn assign(tx: &Transaction, target: &str) -> Result<(), String> {
    // Offline history has one owner. Merge with that account's bucket, never
    // reassign another account's contribution during login changes.
    loop {
        let unowned =
            {
                let mut stmt = tx
            .prepare("SELECT day,payload FROM aggregate_days WHERE owner='' ORDER BY day LIMIT 100")
            .map_err(|e| e.to_string())?;
                let rows = stmt
                    .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))
                    .map_err(|e| e.to_string())?;
                rows.collect::<Result<Vec<_>, _>>()
                    .map_err(|e| e.to_string())?
            };
        if unowned.is_empty() {
            break;
        }
        for (day, raw) in unowned {
            let activity = super::aggregate_activity::assign(tx, target, &day)?;
            let mut prices = load_bucket_prices(tx, target, &day)?;
            prices.merge(load_bucket_prices(tx, "", &day)?);
            store_bucket_prices(tx, target, &day, &prices)?;
            tx.execute(
                "DELETE FROM aggregate_pricing WHERE owner='' AND day=?1",
                params![day],
            )
            .map_err(|e| e.to_string())?;
            let existing: Option<String> = tx
                .query_row(
                    "SELECT payload FROM aggregate_days WHERE owner=?1 AND day=?2",
                    params![target, day],
                    |r| r.get(0),
                )
                .optional()
                .map_err(|e| e.to_string())?;
            let mut rows: Vec<AggregateRow> = existing
                .map(|s| serde_json::from_str(&s))
                .transpose()
                .map_err(|e| e.to_string())?
                .unwrap_or_default();
            for row in serde_json::from_str::<Vec<AggregateRow>>(&raw).map_err(|e| e.to_string())? {
                if let Some(old) = rows.iter_mut().find(|r| {
                    r.kind == row.kind
                        && r.agent_id == row.agent_id
                        && r.provider_id == row.provider_id
                        && r.model_id == row.model_id
                        && r.skill_key == row.skill_key
                }) {
                    for (k, v) in row.metrics {
                        add(old, &k, v.parse().map_err(|_| "INVALID_AGGREGATE_NUMBER")?)?;
                    }
                } else {
                    rows.push(row);
                }
            }
            for row in rows.iter_mut().filter(|r| r.kind == "agent") {
                if let Some(m) = activity.get(&row.agent_id) {
                    row.metrics.extend(m.clone());
                }
            }
            tx.execute("INSERT INTO aggregate_days(owner,day,payload) VALUES(?1,?2,?3) ON CONFLICT(owner,day) DO UPDATE SET payload=excluded.payload,revision=revision+1,retry_at=0,last_error=NULL",params![target,day,serde_json::to_string(&rows).map_err(|e|e.to_string())?]).map_err(|e|e.to_string())?;
            tx.execute(
                "DELETE FROM aggregate_days WHERE owner='' AND day=?1",
                params![day],
            )
            .map_err(|e| e.to_string())?;
        }
    }
    Ok(())
}

impl LocalStore {
    pub fn aggregate_pending_count(&self) -> usize {
        if self.active_target_id().is_none() {
            return 0;
        }
        self.conn.query_row("SELECT COUNT(*) FROM aggregate_days WHERE (owner=(SELECT active_target_id FROM sync_state WHERE id=1) OR owner='') AND revision>acked_revision",[],|r|r.get::<_,i64>(0)).unwrap_or(0) as usize
    }

    pub fn refresh_aggregate_prices(&mut self) -> Result<(), String> {
        let tx = self.conn.transaction().map_err(|e| e.to_string())?;
        let buckets = {
            let mut stmt = tx
                .prepare("SELECT owner,day,payload FROM aggregate_pricing")
                .map_err(|e| e.to_string())?;
            let rows = stmt
                .query_map([], |r| {
                    Ok((
                        r.get::<_, String>(0)?,
                        r.get::<_, String>(1)?,
                        r.get::<_, String>(2)?,
                    ))
                })
                .map_err(|e| e.to_string())?;
            rows.collect::<Result<Vec<_>, _>>()
                .map_err(|e| e.to_string())?
        };
        for (owner, day, raw) in buckets {
            let mut p: CostLedger = serde_json::from_str(&raw).map_err(|e| e.to_string())?;
            if p.reprice(&self.catalog) {
                store_bucket_prices(&tx, &owner, &day, &p)?;
                refresh_bucket_price(&tx, &owner, &day, &p)?;
            }
        }
        tx.commit().map_err(|e| e.to_string())
    }
    pub fn pending_aggregate(&mut self) -> Result<Option<PendingAggregate>, String> {
        if !self.sync_enabled() || self.rebuild_in_progress() {
            return Ok(None);
        }
        let Some(owner) = self.active_target_id() else {
            return Ok(None);
        };
        let tx = self.conn.transaction().map_err(|e| e.to_string())?;
        assign(&tx, &owner)?;
        let row:Option<(String,i64,String)>=tx.query_row("SELECT day,revision,payload FROM aggregate_days WHERE owner=?1 AND revision>acked_revision AND retry_at<=?2 ORDER BY day LIMIT 1",params![owner,unix_now()],|r|Ok((r.get(0)?,r.get(1)?,r.get(2)?))).optional().map_err(|e|e.to_string())?;
        tx.commit().map_err(|e| e.to_string())?;
        row.map(|(day, revision, raw)| {
            Ok(PendingAggregate {
                owner,
                snapshot: AggregateSnapshot {
                    schema_version: 1,
                    day,
                    revision,
                    rows: serde_json::from_str(&raw).map_err(|e| e.to_string())?,
                },
            })
        })
        .transpose()
    }

    pub fn ack_aggregate(&mut self, pending: &PendingAggregate) -> Result<(), String> {
        if self.active_target_id().as_deref() != Some(&pending.owner) {
            return Err("STALE_ACCOUNT".into());
        }
        self.conn.execute("UPDATE aggregate_days SET acked_revision=MAX(acked_revision,?1) WHERE owner=?2 AND day=?3 AND revision>=?1",params![pending.snapshot.revision,pending.owner,pending.snapshot.day]).map_err(|e|e.to_string())?;
        Ok(())
    }

    pub fn defer_aggregate(
        &mut self,
        pending: &PendingAggregate,
        code: &str,
        delay: i64,
    ) -> Result<(), String> {
        self.conn.execute("UPDATE aggregate_days SET retry_at=?1,last_error=?2 WHERE owner=?3 AND day=?4 AND revision=?5",params![unix_now()+delay,code,pending.owner,pending.snapshot.day,pending.snapshot.revision]).map_err(|e|e.to_string())?;
        Ok(())
    }

    pub fn prune_details(&mut self, today: NaiveDate) -> Result<usize, String> {
        let expected = self.retention_clock.0
            + chrono::Duration::from_std(self.retention_clock.1.elapsed())
                .map_err(|_| "RETENTION_CLOCK_INVALID")?;
        if (today - expected.date_naive()).num_days().abs() > 1 {
            return Err("RETENTION_CLOCK_CHANGED".into());
        }
        let last: Option<String> = self
            .conn
            .query_row("SELECT last_day FROM retention_state WHERE id=1", [], |r| {
                r.get(0)
            })
            .optional()
            .map_err(|e| e.to_string())?;
        // A previously persisted clock baseline makes unexpected forward jumps
        // observable instead of silently deleting detail; caller reports error.
        if let Some(last) = last {
            let last = NaiveDate::parse_from_str(&last, "%Y-%m-%d")
                .map_err(|_| "INVALID_RETENTION_DATE")?;
            if today < last {
                return Err("RETENTION_CLOCK_REVERSED".into());
            }
        }
        let cutoff = (today - chrono::Duration::days(6))
            .and_hms_opt(0, 0, 0)
            .unwrap()
            .and_utc()
            .timestamp();
        let mut pricing = self.pricing.clone();
        let tx = self.conn.transaction().map_err(|e| e.to_string())?;
        // Both offsets and Z timestamps are compared by their instant.
        let removed=tx.execute("DELETE FROM events WHERE local_seq IN (SELECT local_seq FROM events WHERE unixepoch(occurred_at)<?1 ORDER BY local_seq LIMIT 2000)",params![cutoff]).map_err(|e|e.to_string())?;
        tx.execute("DELETE FROM sync_delivery WHERE NOT EXISTS(SELECT 1 FROM events e WHERE e.event_id=sync_delivery.event_id)",[]).map_err(|e|e.to_string())?;
        pricing.compact_before(
            &(today - chrono::Duration::days(6))
                .format("%Y-%m-%d")
                .to_string(),
        );
        let cutoff_day = (today - chrono::Duration::days(6)).to_string();
        let buckets = {
            let mut stmt=tx.prepare("SELECT owner,day,payload FROM aggregate_pricing WHERE day<?1 AND (json_extract(payload,'$.requests')!='{}' OR json_extract(payload,'$.reported_groups')!='{}') LIMIT 100").map_err(|e|e.to_string())?;
            let rows = stmt
                .query_map(params![cutoff_day], |r| {
                    Ok((
                        r.get::<_, String>(0)?,
                        r.get::<_, String>(1)?,
                        r.get::<_, String>(2)?,
                    ))
                })
                .map_err(|e| e.to_string())?;
            rows.collect::<Result<Vec<_>, _>>()
                .map_err(|e| e.to_string())?
        };
        for (owner, day, raw) in buckets {
            let mut p: CostLedger = serde_json::from_str(&raw).map_err(|e| e.to_string())?;
            p.reprice(&self.catalog);
            p.compact_before(&cutoff_day);
            store_bucket_prices(&tx, &owner, &day, &p)?;
            refresh_bucket_price(&tx, &owner, &day, &p)?;
        }
        save_pricing(&tx, &pricing)?;
        tx.execute("INSERT INTO retention_state(id,last_day) VALUES(1,?1) ON CONFLICT(id) DO UPDATE SET last_day=excluded.last_day",params![today.to_string()]).map_err(|e|e.to_string())?;
        tx.commit().map_err(|e| e.to_string())?;
        self.pricing = pricing;
        Ok(removed)
    }
}

fn load_bucket_prices(tx: &Transaction, owner: &str, day: &str) -> Result<CostLedger, String> {
    let raw: Option<String> = tx
        .query_row(
            "SELECT payload FROM aggregate_pricing WHERE owner=?1 AND day=?2",
            params![owner, day],
            |r| r.get(0),
        )
        .optional()
        .map_err(|e| e.to_string())?;
    raw.map(|s| serde_json::from_str(&s).map_err(|e| e.to_string()))
        .transpose()
        .map(|p| p.unwrap_or_default())
}
fn store_bucket_prices(
    tx: &Transaction,
    owner: &str,
    day: &str,
    prices: &CostLedger,
) -> Result<(), String> {
    tx.execute("INSERT INTO aggregate_pricing(owner,day,payload) VALUES(?1,?2,?3) ON CONFLICT(owner,day) DO UPDATE SET payload=excluded.payload",params![owner,day,serde_json::to_string(prices).map_err(|e|e.to_string())?]).map_err(|e|e.to_string())?;
    Ok(())
}
fn refresh_bucket_price(
    tx: &Transaction,
    owner: &str,
    day: &str,
    prices: &CostLedger,
) -> Result<(), String> {
    let raw: String = tx
        .query_row(
            "SELECT payload FROM aggregate_days WHERE owner=?1 AND day=?2",
            params![owner, day],
            |r| r.get(0),
        )
        .map_err(|e| e.to_string())?;
    let mut rows: Vec<AggregateRow> = serde_json::from_str(&raw).map_err(|e| e.to_string())?;
    for row in rows.iter_mut().filter(|r| r.kind == "agent") {
        let coverage = prices.days(&row.agent_id, &HashSet::new());
        let amount = coverage.get(day).map_or(0, |c| c.estimated_usd);
        row.metrics
            .insert("estimated_cost_usd_units".into(), amount.to_string());
    }
    let next = serde_json::to_string(&rows).map_err(|e| e.to_string())?;
    if next != raw {
        tx.execute(
            "UPDATE aggregate_days SET payload=?1,revision=revision+1,retry_at=0,last_error=NULL WHERE owner=?2 AND day=?3",
            params![next, owner, day],
        )
        .map_err(|e| e.to_string())?;
    }
    Ok(())
}
