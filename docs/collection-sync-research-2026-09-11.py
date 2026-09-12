"""Synthetic reproductions using repository SQL; never opens user databases.

Run: python docs/collection-sync-research-2026-09-11.py
These are reduced state-machine/SQL reproductions, not end-to-end tests.
"""
from pathlib import Path
import hashlib
import re
import sqlite3

ROOT = Path(__file__).resolve().parents[1]


def source(path):
    return (ROOT / path).read_text(encoding="utf-8")


def quoted_sql(text, prefix):
    return re.search(r'"(' + re.escape(prefix) + r'[^"]*)"', text).group(1)


retention = source("collector/apps/desktop/src-tauri/src/local_store/retention.rs")
store = source("collector/apps/desktop/src-tauri/src/local_store/mod.rs")
drivers = source("collector/crates/acquisition/src/drivers.rs")

# A timestamp watermark is not an acknowledgment of a day or its revision.
db = sqlite3.connect(":memory:")
db.execute("CREATE TABLE aggregate_days(owner TEXT,day TEXT,revision INTEGER,acked_revision INTEGER)")
db.executemany("INSERT INTO aggregate_days VALUES(?,?,?,?)", [
    ("synthetic", "2026-09-01", 5, 4),
    ("synthetic", "2026-09-02", 1, 0),
])
db.execute(quoted_sql(retention, "UPDATE aggregate_days SET acked_revision=revision WHERE"),
           {"1": "synthetic", "2": "2026-09-02"})
assert db.execute("SELECT COUNT(*) FROM aggregate_days WHERE revision>acked_revision").fetchone()[0] == 0
print("PASS: watermark acknowledges both an unsent correction and a never-sent day")

# A filtered rowid scan misses a lower row that becomes eligible later.
db.execute("CREATE TABLE model_usage(session_id TEXT,provider_id TEXT,model_id TEXT,input_tokens INTEGER,output_tokens INTEGER,computed_total_tokens INTEGER,tool_call_count INTEGER,completed_at INTEGER,status TEXT)")
db.executemany("INSERT INTO model_usage VALUES(?,?,?,?,?,?,?,?,?)", [
    ("s", "p", "m", 1, 1, 2, 0, 1, "running"),
    ("s", "p", "m", 2, 2, 4, 0, 2, "completed"),
])
query = quoted_sql(drivers, "SELECT rowid AS id, session_id, provider_id, model_id, input_tokens")
cursor = max(row[0] for row in db.execute(query, {"1": 0}))
db.execute("UPDATE model_usage SET status='completed' WHERE rowid=1")
assert list(db.execute(query, {"1": cursor})) == []
print("PASS: completing row 1 after row 2 is missed by the next ZCode poll")

# Match Rust's wrapping u64 hash followed by `as i64` for SQLite storage.
def offset(cursor):
    value = 0
    for byte in cursor.encode():
        value = (value * 109 + byte) & ((1 << 64) - 1)
    return value if value < (1 << 63) else value - (1 << 64)


for n in range(1, 100000):
    before, after = f"1:{n}:1700000000000", f"1:{n+1}:1700000000000"
    if offset(after) < offset(before):
        break
else:
    raise AssertionError("expected signed offset regression")
db.execute("CREATE TABLE source_checkpoints(source_id TEXT,file_identity TEXT,generation INTEGER,file_offset INTEGER,file_len INTEGER,cursor TEXT,status TEXT,driver_checkpoint TEXT,PRIMARY KEY(source_id,file_identity))")
upsert = quoted_sql(store, "INSERT INTO source_checkpoints (")
for value in (before, after):
    db.execute(upsert, {"1": "synthetic", "2": "synthetic", "3": 1,
                       "4": offset(value), "5": offset(value), "6": value,
                       "7": "Current", "8": value})
assert db.execute("SELECT cursor FROM source_checkpoints").fetchone()[0] == before
print(f"PASS: real checkpoint UPSERT refuses newer cursor {before} -> {after}")
print(f"      signed offsets {offset(before)} -> {offset(after)}")

# Reproduce v7's loss of compacted history using its actual DELETE statements.
db.execute("CREATE TABLE events(event_id TEXT,agent_id TEXT,event_type TEXT)")
db.execute("CREATE TABLE event_fingerprints(digest BLOB PRIMARY KEY)")
digest = hashlib.sha256(b"synthetic-compacted-event").digest()
db.execute("INSERT INTO event_fingerprints VALUES(?)", (digest,))
v7 = store.split("if current < 7 {", 1)[1].split("rebuild_daily_metrics_from_events", 1)[0]
for table in ("aggregate_days",):
    deletion = re.search(r"DELETE FROM " + table + r";", v7).group(0)
    db.execute(deletion)
assert db.execute("SELECT COUNT(*) FROM events").fetchone()[0] == 0
assert db.execute("SELECT COUNT(*) FROM aggregate_days").fetchone()[0] == 0
fresh = db.execute("INSERT OR IGNORE INTO event_fingerprints VALUES(?)", (digest,)).rowcount
event_exists = db.execute("SELECT EXISTS(SELECT 1 FROM events WHERE event_id=?)", ("synthetic-compacted-event",)).fetchone()[0]
assert not fresh and not event_exists  # apply_event's early-return condition
print("PASS: compacted history loses its day ledger and fingerprint rejects same-ID replay")

# Explicitly a predicate reproduction, not invocation of the Rust decoder.
rebuild = source("collector/apps/desktop/src-tauri/src/rebuild.rs")
assert "let caught_up = batch.events.len() < FRAMES_PER_FILE;" in rebuild
raw_frames, emitted_events, unread_frames = 32, 0, 68
assert emitted_events < raw_frames and unread_frames > 0
print("PASS: completion predicate treats 32 metadata frames / 0 events / 68 unread frames as caught_up")
db.close()
