//! Compact identity counters, without event envelopes or message contents.
use super::*;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

#[derive(Default, Serialize, Deserialize)]
struct Activity {
    sessions: BTreeMap<String, Option<u64>>,
    turns: BTreeMap<String, Turn>,
}
#[derive(Default, Serialize, Deserialize)]
struct Turn {
    session: Option<String>,
    started: bool,
    user: bool,
    completed: bool,
    duration: u64,
}
fn key(parts: &[&str]) -> String {
    let bytes = serde_json::to_vec(parts).expect("string keys");
    format!("{:x}", Sha256::digest(bytes))
}
impl Activity {
    fn metrics(&self) -> Result<BTreeMap<String, String>, String> {
        let mut duration = 0u64;
        for ms in self.sessions.values().flatten() {
            duration = duration.checked_add(*ms).ok_or("ACTIVITY_OVERFLOW")?;
        }
        for t in self.turns.values() {
            if t.session
                .as_ref()
                .and_then(|s| self.sessions.get(s))
                .copied()
                .flatten()
                .is_none()
            {
                duration = duration
                    .checked_add(t.duration)
                    .ok_or("ACTIVITY_OVERFLOW")?;
            }
        }
        Ok(BTreeMap::from([
            ("session_count".into(), self.sessions.len().to_string()),
            (
                "interaction_turn_count".into(),
                self.turns.len().to_string(),
            ),
            (
                "message_count".into(),
                self.turns
                    .values()
                    .map(|t| u64::from(t.started) + u64::from(t.completed))
                    .sum::<u64>()
                    .to_string(),
            ),
            (
                "user_message_count".into(),
                self.turns.values().filter(|t| t.user).count().to_string(),
            ),
            ("active_duration_ms".into(), duration.to_string()),
        ]))
    }
    fn merge(&mut self, other: Activity) {
        for (k, v) in other.sessions {
            let old = self.sessions.entry(k).or_default();
            *old = (*old).max(v);
        }
        for (k, t) in other.turns {
            let old = self.turns.entry(k).or_default();
            old.session = old.session.clone().or(t.session);
            old.started |= t.started;
            old.user |= t.user;
            old.completed |= t.completed;
            old.duration = old.duration.max(t.duration);
        }
    }
}
fn load(tx: &Transaction, owner: &str, day: &str, agent: &str) -> Result<Activity, String> {
    let raw: Option<String> = tx
        .query_row(
            "SELECT payload FROM aggregate_activity WHERE owner=?1 AND day=?2 AND agent=?3",
            params![owner, day, agent],
            |r| r.get(0),
        )
        .optional()
        .map_err(|e| e.to_string())?;
    raw.map(|s| serde_json::from_str(&s).map_err(|e| e.to_string()))
        .transpose()
        .map(|a| a.unwrap_or_default())
}
fn save(tx: &Transaction, owner: &str, day: &str, agent: &str, a: &Activity) -> Result<(), String> {
    tx.execute("INSERT INTO aggregate_activity(owner,day,agent,payload) VALUES(?1,?2,?3,?4) ON CONFLICT(owner,day,agent) DO UPDATE SET payload=excluded.payload", params![owner,day,agent,serde_json::to_string(a).map_err(|e|e.to_string())?]).map_err(|e|e.to_string())?;
    Ok(())
}
pub(super) fn record(
    tx: &Transaction,
    owner: &str,
    day: &str,
    e: &EventEnvelope,
    metrics: &mut BTreeMap<String, String>,
) -> Result<(), String> {
    if e.session_hash.is_none() && e.turn_hash.is_none() {
        return Ok(());
    }
    let mut a = load(tx, owner, day, &e.agent_id)?;
    let session = e.session_hash.as_ref().map(|s| key(&[s]));
    if let Some(s) = &session {
        let value = a.sessions.entry(s.clone()).or_default();
        if let EventPayload::SessionEnded(p) = &e.payload {
            if let Some(ms) = &p.duration_ms {
                *value = (*value).max(Some(ms.parse().map_err(|_| "INVALID_DURATION")?));
            }
        }
    }
    if let Some(t) = &e.turn_hash {
        let turn = a
            .turns
            .entry(key(&[e.session_hash.as_deref().unwrap_or(""), t]))
            .or_default();
        turn.session = session;
        match &e.payload {
            EventPayload::TurnStarted(p) => {
                turn.started = true;
                turn.user |= p.trigger == Some(protocol::TurnTrigger::User);
            }
            EventPayload::TurnCompleted(p) => {
                turn.completed = true;
                if let Some(ms) = &p.duration_ms {
                    turn.duration = turn
                        .duration
                        .max(ms.parse().map_err(|_| "INVALID_DURATION")?);
                }
            }
            _ => (),
        }
    }
    metrics.extend(a.metrics()?);
    save(tx, owner, day, &e.agent_id, &a)
}
pub(super) fn assign(
    tx: &Transaction,
    target: &str,
    day: &str,
) -> Result<BTreeMap<String, BTreeMap<String, String>>, String> {
    let agents = {
        let mut stmt = tx
            .prepare("SELECT agent FROM aggregate_activity WHERE owner='' AND day=?1")
            .map_err(|e| e.to_string())?;
        let rows = stmt
            .query_map(params![day], |r| r.get::<_, String>(0))
            .map_err(|e| e.to_string())?;
        rows.collect::<Result<Vec<_>, _>>()
            .map_err(|e| e.to_string())?
    };
    let mut metrics = BTreeMap::new();
    for agent in agents {
        let mut a = load(tx, target, day, &agent)?;
        a.merge(load(tx, "", day, &agent)?);
        metrics.insert(agent.clone(), a.metrics()?);
        save(tx, target, day, &agent, &a)?;
    }
    tx.execute(
        "DELETE FROM aggregate_activity WHERE owner='' AND day=?1",
        params![day],
    )
    .map_err(|e| e.to_string())?;
    Ok(metrics)
}
