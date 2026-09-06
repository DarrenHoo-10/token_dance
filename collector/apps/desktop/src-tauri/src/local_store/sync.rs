use protocol::EventEnvelope;
use rusqlite::{params, OptionalExtension};

use super::{rfc3339_now, unix_now, LocalStore};

const ASSIGN_BATCH: usize = 1000;
const LEASE_TTL_SECS: i64 = 60;

#[derive(Debug, Clone)]
pub struct LeasedBatch {
    pub target_id: String,
    pub account_id: String,
    pub lease_id: String,
    pub lease_generation: i64,
    pub events: Vec<EventEnvelope>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DeliveryRecord {
    pub target_id: String,
    pub event_id: String,
    pub local_seq: i64,
    pub status: String,
    pub last_error: Option<String>,
}

impl LocalStore {
    pub fn sync_enabled(&self) -> bool {
        self.conn
            .query_row(
                "SELECT sync_enabled FROM sync_state WHERE id = 1",
                [],
                |row| row.get::<_, i64>(0),
            )
            .ok()
            .map(|value| value != 0)
            .unwrap_or(true)
    }

    pub fn set_sync_enabled(&mut self, enabled: bool) -> Result<(), String> {
        self.conn
            .execute(
                "UPDATE sync_state SET sync_enabled = ?1 WHERE id = 1",
                params![i64::from(enabled)],
            )
            .map_err(|error| error.to_string())?;
        Ok(())
    }

    pub fn active_target_id(&self) -> Option<String> {
        self.conn
            .query_row(
                "SELECT active_target_id FROM sync_state WHERE id = 1",
                [],
                |row| row.get::<_, Option<String>>(0),
            )
            .ok()
            .flatten()
    }

    pub fn active_account_id(&self) -> Option<String> {
        let target_id = self.active_target_id()?;
        self.conn
            .query_row(
                "SELECT account_id FROM sync_targets WHERE target_id = ?1",
                params![target_id],
                |row| row.get(0),
            )
            .optional()
            .ok()
            .flatten()
    }

    pub fn pending_sync_count(&self) -> usize {
        let Some(target_id) = self.active_target_id() else {
            return 0;
        };
        self.conn
            .query_row(
                "SELECT COUNT(*) FROM sync_delivery
                 WHERE target_id = ?1 AND status IN ('pending', 'retry', 'leased')",
                params![target_id],
                |row| row.get::<_, i64>(0),
            )
            .unwrap_or(0) as usize
    }

    pub fn delivery_for_event(&self, event_id: &str) -> Option<DeliveryRecord> {
        self.conn
            .query_row(
                "SELECT target_id, event_id, local_seq, status, last_error
                 FROM sync_delivery WHERE event_id = ?1",
                params![event_id],
                |row| {
                    Ok(DeliveryRecord {
                        target_id: row.get(0)?,
                        event_id: row.get(1)?,
                        local_seq: row.get(2)?,
                        status: row.get(3)?,
                        last_error: row.get(4)?,
                    })
                },
            )
            .optional()
            .ok()
            .flatten()
    }

    pub fn peek_pending_events(&self, limit: usize) -> Vec<EventEnvelope> {
        let Some(target_id) = self.active_target_id() else {
            return Vec::new();
        };
        let mut stmt = match self.conn.prepare(
            "SELECT e.envelope_json
             FROM sync_delivery d
             JOIN events e ON e.event_id = d.event_id
             WHERE d.target_id = ?1 AND d.status IN ('pending', 'retry', 'leased')
             ORDER BY d.local_seq
             LIMIT ?2",
        ) {
            Ok(stmt) => stmt,
            Err(_) => return Vec::new(),
        };
        let rows = stmt
            .query_map(params![target_id, limit as i64], |row| {
                row.get::<_, String>(0)
            })
            .ok();
        let Some(rows) = rows else {
            return Vec::new();
        };
        rows.filter_map(Result::ok)
            .filter_map(|raw| serde_json::from_str(&raw).ok())
            .collect()
    }

    pub fn pending_outbox(&self, limit: usize) -> Vec<(EventEnvelope, String)> {
        let Some(target_id) = self.active_target_id() else {
            return Vec::new();
        };
        let mut stmt = match self.conn.prepare(
            "SELECT e.envelope_json, d.status
             FROM sync_delivery d
             JOIN events e ON e.event_id = d.event_id
             WHERE d.target_id = ?1 AND d.status IN ('pending', 'retry', 'leased', 'isolated')
             ORDER BY d.local_seq
             LIMIT ?2",
        ) {
            Ok(stmt) => stmt,
            Err(_) => return Vec::new(),
        };
        let rows = stmt
            .query_map(params![target_id, limit as i64], |row| {
                Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
            })
            .ok();
        let Some(rows) = rows else {
            return Vec::new();
        };
        rows.filter_map(Result::ok)
            .filter_map(|(raw, status)| {
                let event = serde_json::from_str(&raw).ok()?;
                Some((event, status))
            })
            .collect()
    }

    pub fn activate_account(
        &mut self,
        account_id: &str,
        installation_id: &str,
    ) -> Result<String, String> {
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let now = rfc3339_now();
        let current = tx
            .query_row(
                "SELECT active_target_id FROM sync_state WHERE id = 1",
                [],
                |row| row.get::<_, Option<String>>(0),
            )
            .map_err(|error| error.to_string())?;
        if let Some(current) = current.as_deref() {
            let current_account: Option<String> = tx
                .query_row(
                    "SELECT account_id FROM sync_targets WHERE target_id = ?1",
                    params![current],
                    |row| row.get(0),
                )
                .optional()
                .map_err(|error| error.to_string())?;
            if current_account.as_deref() != Some(account_id) {
                deactivate_target(&tx, current)?;
            }
        }
        let target_id = format!("tgt:{account_id}");
        let existing: Option<(i64, String)> = tx
            .query_row(
                "SELECT binding_generation, status FROM sync_targets WHERE target_id = ?1",
                params![target_id],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .optional()
            .map_err(|error| error.to_string())?;
        let already_active = current.as_deref() == Some(target_id.as_str())
            && existing
                .as_ref()
                .is_some_and(|(_, status)| status == "active");
        let generation = match existing {
            Some((generation, _)) if already_active => generation,
            Some((generation, _)) => generation.saturating_add(1),
            None => 1,
        };
        tx.execute(
            "INSERT INTO sync_targets (
                target_id, account_id, binding_generation, installation_id, status, created_at
             ) VALUES (?1, ?2, ?3, ?4, 'active', ?5)
             ON CONFLICT(target_id) DO UPDATE SET
                binding_generation = excluded.binding_generation,
                installation_id = excluded.installation_id,
                status = 'active'",
            params![target_id, account_id, generation, installation_id, now],
        )
        .map_err(|error| error.to_string())?;
        tx.execute(
            "UPDATE sync_delivery
             SET status = CASE WHEN status = 'leased' THEN 'pending' ELSE status END,
                 lease_id = NULL,
                 lease_until = NULL,
                 lease_generation = ?1
             WHERE target_id = ?2 AND status = 'leased'",
            params![generation, target_id],
        )
        .map_err(|error| error.to_string())?;
        tx.execute(
            "UPDATE sync_state SET active_target_id = ?1 WHERE id = 1",
            params![target_id],
        )
        .map_err(|error| error.to_string())?;
        assign_unassigned(&tx, &target_id)?;
        tx.commit().map_err(|error| error.to_string())?;
        Ok(target_id)
    }

    pub fn deactivate_account(&mut self) -> Result<(), String> {
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let current = tx
            .query_row(
                "SELECT active_target_id FROM sync_state WHERE id = 1",
                [],
                |row| row.get::<_, Option<String>>(0),
            )
            .map_err(|error| error.to_string())?;
        if let Some(current) = current.as_deref() {
            deactivate_target(&tx, current)?;
        }
        tx.execute(
            "UPDATE sync_state SET active_target_id = NULL WHERE id = 1",
            [],
        )
        .map_err(|error| error.to_string())?;
        tx.commit().map_err(|error| error.to_string())
    }

    pub fn assign_unassigned_events(&mut self) -> Result<usize, String> {
        let Some(target_id) = self.active_target_id() else {
            return Ok(0);
        };
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let count = assign_unassigned(&tx, &target_id)?;
        tx.commit().map_err(|error| error.to_string())?;
        Ok(count)
    }

    pub fn lease_batch(&mut self, lease_id: &str, limit: usize) -> Result<LeasedBatch, String> {
        let Some(target_id) = self.active_target_id() else {
            return Ok(LeasedBatch {
                target_id: String::new(),
                account_id: String::new(),
                lease_id: lease_id.into(),
                lease_generation: 0,
                events: Vec::new(),
            });
        };
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        assign_unassigned(&tx, &target_id)?;
        let (account_id, generation): (String, i64) = tx
            .query_row(
                "SELECT account_id, binding_generation FROM sync_targets WHERE target_id = ?1",
                params![target_id],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .map_err(|error| error.to_string())?;
        let now = unix_now();
        let ids: Vec<String> = {
            let mut stmt = tx
                .prepare(
                    "SELECT event_id FROM sync_delivery
                     WHERE target_id = ?1
                       AND status IN ('pending', 'retry', 'leased')
                       AND (status != 'leased' OR lease_until IS NULL OR lease_until <= ?2)
                       AND (next_attempt_at IS NULL OR next_attempt_at <= ?2)
                     ORDER BY local_seq
                     LIMIT ?3",
                )
                .map_err(|error| error.to_string())?;
            let rows = stmt
                .query_map(params![target_id, now, limit as i64], |row| row.get(0))
                .map_err(|error| error.to_string())?;
            rows.filter_map(Result::ok).collect()
        };
        let until = now.saturating_add(LEASE_TTL_SECS);
        for event_id in &ids {
            tx.execute(
                "UPDATE sync_delivery
                 SET status = 'leased', lease_id = ?1, lease_until = ?2, lease_generation = ?3
                 WHERE target_id = ?4 AND event_id = ?5",
                params![lease_id, until, generation, target_id, event_id],
            )
            .map_err(|error| error.to_string())?;
        }
        let mut events = Vec::new();
        for event_id in &ids {
            let raw: Option<String> = tx
                .query_row(
                    "SELECT envelope_json FROM events WHERE event_id = ?1",
                    params![event_id],
                    |row| row.get(0),
                )
                .optional()
                .map_err(|error| error.to_string())?;
            match raw.and_then(|raw| serde_json::from_str(&raw).ok()) {
                Some(event) => events.push(event),
                None => {
                    isolate_row(&tx, &target_id, event_id, "missing_envelope")?;
                }
            }
        }
        tx.commit().map_err(|error| error.to_string())?;
        Ok(LeasedBatch {
            target_id,
            account_id,
            lease_id: lease_id.into(),
            lease_generation: generation,
            events,
        })
    }

    pub fn ack_leased(
        &mut self,
        target_id: &str,
        lease_id: &str,
        event_ids: &[String],
    ) -> Result<usize, String> {
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let mut count = 0usize;
        for event_id in event_ids {
            count += tx
                .execute(
                    "UPDATE sync_delivery
                     SET status = 'acked', lease_id = NULL, lease_until = NULL, last_error = NULL
                     WHERE target_id = ?1 AND lease_id = ?2 AND event_id = ?3 AND status = 'leased'",
                    params![target_id, lease_id, event_id],
                )
                .map_err(|error| error.to_string())?;
        }
        tx.commit().map_err(|error| error.to_string())?;
        Ok(count)
    }

    pub fn isolate_events(
        &mut self,
        target_id: &str,
        event_ids: &[String],
        error: &str,
    ) -> Result<(), String> {
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        for event_id in event_ids {
            isolate_row(&tx, target_id, event_id, error)?;
        }
        tx.commit().map_err(|error| error.to_string())
    }

    pub fn retry_events(
        &mut self,
        target_id: &str,
        lease_id: &str,
        event_ids: &[String],
        error: &str,
        delay_secs: i64,
    ) -> Result<(), String> {
        let tx = self.conn.transaction().map_err(|error| error.to_string())?;
        let next = unix_now().saturating_add(delay_secs);
        for event_id in event_ids {
            tx.execute(
                "UPDATE sync_delivery
                 SET status = 'retry',
                     lease_id = NULL,
                     lease_until = NULL,
                     attempt_count = attempt_count + 1,
                     next_attempt_at = ?1,
                     last_error = ?2
                 WHERE target_id = ?3 AND event_id = ?4
                   AND (lease_id = ?5 OR lease_id IS NULL)",
                params![next, error, target_id, event_id, lease_id],
            )
            .map_err(|error| error.to_string())?;
        }
        tx.commit().map_err(|error| error.to_string())
    }

    pub fn release_lease(&mut self, target_id: &str, lease_id: &str) -> Result<(), String> {
        self.conn
            .execute(
                "UPDATE sync_delivery
                 SET status = CASE WHEN status = 'leased' THEN 'pending' ELSE status END,
                     lease_id = NULL,
                     lease_until = NULL
                 WHERE target_id = ?1 AND lease_id = ?2 AND status = 'leased'",
                params![target_id, lease_id],
            )
            .map_err(|error| error.to_string())?;
        Ok(())
    }
}

fn deactivate_target(tx: &rusqlite::Transaction, target_id: &str) -> Result<(), String> {
    tx.execute(
        "UPDATE sync_delivery
         SET status = CASE WHEN status = 'leased' THEN 'pending' ELSE status END,
             lease_id = NULL,
             lease_until = NULL
         WHERE target_id = ?1 AND status = 'leased'",
        params![target_id],
    )
    .map_err(|error| error.to_string())?;
    tx.execute(
        "UPDATE sync_targets SET status = 'inactive' WHERE target_id = ?1",
        params![target_id],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

fn assign_unassigned(tx: &rusqlite::Transaction, target_id: &str) -> Result<usize, String> {
    let changed = tx
        .execute(
            "INSERT OR IGNORE INTO sync_delivery (target_id, event_id, local_seq, status)
             SELECT ?1, event_id, local_seq, 'pending'
             FROM events
             WHERE event_id NOT IN (SELECT event_id FROM sync_delivery)
             ORDER BY local_seq
             LIMIT ?2",
            params![target_id, ASSIGN_BATCH as i64],
        )
        .map_err(|error| error.to_string())?;
    Ok(changed)
}

fn isolate_row(
    tx: &rusqlite::Transaction,
    target_id: &str,
    event_id: &str,
    error: &str,
) -> Result<(), String> {
    tx.execute(
        "UPDATE sync_delivery
         SET status = 'isolated',
             lease_id = NULL,
             lease_until = NULL,
             last_error = ?1
         WHERE target_id = ?2 AND event_id = ?3",
        params![error, target_id, event_id],
    )
    .map_err(|error| error.to_string())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::local_store::test_envelope;
    use protocol::Accuracy;

    fn open_store() -> (tempfile::TempDir, LocalStore) {
        let dir = tempfile::tempdir().unwrap();
        let store = LocalStore::open(dir.path()).unwrap();
        (dir, store)
    }

    #[test]
    fn bad_record_is_isolated_without_blocking_later_events() {
        let (_dir, mut store) = open_store();
        let first = test_envelope("codex", "evt-good-1", 10, Accuracy::Exact);
        let poison = test_envelope("codex", "evt-poison", 11, Accuracy::Exact);
        let later = test_envelope("codex", "evt-good-2", 12, Accuracy::Exact);
        store
            .commit_batch(&[first.clone(), poison.clone(), later.clone()], &[])
            .unwrap();
        store.activate_account("acct-a", "ins_test").unwrap();
        store
            .isolate_events("tgt:acct-a", &[poison.event_id.clone()], "invalid_payload")
            .unwrap();
        let leased = store.lease_batch("lease-1", 10).unwrap();
        let ids: Vec<_> = leased
            .events
            .iter()
            .map(|event| event.event_id.as_str())
            .collect();
        assert_eq!(ids, vec!["evt-good-1", "evt-good-2"]);
        assert!(!ids.contains(&"evt-poison"));
        store
            .ack_leased(
                &leased.target_id,
                "lease-1",
                &leased
                    .events
                    .iter()
                    .map(|event| event.event_id.clone())
                    .collect::<Vec<_>>(),
            )
            .unwrap();
        assert_eq!(
            store.delivery_for_event("evt-poison").unwrap().status,
            "isolated"
        );
        assert_eq!(
            store.delivery_for_event("evt-good-1").unwrap().status,
            "acked"
        );
        assert_eq!(
            store.delivery_for_event("evt-good-2").unwrap().status,
            "acked"
        );
        assert_eq!(store.event_count(), 3);
        assert_eq!(store.pending_sync_count(), 0);
    }

    #[test]
    fn account_switch_does_not_send_previous_account_history() {
        let (_dir, mut store) = open_store();
        let event_a = test_envelope("codex", "evt-account-a", 15, Accuracy::Exact);
        store.commit_batch(&[event_a.clone()], &[]).unwrap();
        store.activate_account("acct-a", "ins_test").unwrap();
        assert_eq!(
            store.delivery_for_event("evt-account-a").unwrap().target_id,
            "tgt:acct-a"
        );
        store.deactivate_account().unwrap();
        store.activate_account("acct-b", "ins_test").unwrap();
        let leased = store.lease_batch("lease-b", 10).unwrap();
        assert!(leased.events.is_empty());
        assert_eq!(
            store.delivery_for_event("evt-account-a").unwrap().target_id,
            "tgt:acct-a"
        );
        assert_eq!(
            store.delivery_for_event("evt-account-a").unwrap().status,
            "pending"
        );
        let event_b = test_envelope("codex", "evt-account-b", 7, Accuracy::Exact);
        store.commit_batch(&[event_b.clone()], &[]).unwrap();
        let leased = store.lease_batch("lease-b2", 10).unwrap();
        assert_eq!(leased.events.len(), 1);
        assert_eq!(leased.events[0].event_id, "evt-account-b");
        assert_eq!(leased.account_id, "acct-b");
    }
}
