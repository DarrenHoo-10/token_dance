//! Read completed Claude Cowork and Code usage from the desktop app's IndexedDB cache.
//! Only model, timestamp, session ID, message ID, and token counts leave this
//! module. Conversation text and tool content are never serialized or logged.

use crate::local_store::pipeline::runner::{admit_occurred_at, AdmissionDecision};
use blob_decoder::v8_value::{deserialize_blink, V8Value};
use forensicnomicon_core::chromium_indexeddb::{
    decode_key_prefix_lengths, OBJECT_STORE_META_TYPE, OS_META_NAME,
};
use leveldb_core::Record;
use std::collections::HashMap;
use std::fs;
use std::path::Path;

const DATABASE: &str = "claude-conversation-store";
const STORE: &str = "trees";
const MAX_BLOB_BYTES: u64 = 16 * 1024 * 1024;
const MAX_DECOMPRESSED_BYTES: usize = 32 * 1024 * 1024;
const MAX_CONVERSATIONS: usize = 1000;
const MAX_COMPLETED_MESSAGES: usize = 10_000;
const MAX_LEVELDB_BYTES: u64 = 256 * 1024 * 1024;
const MAX_LEVELDB_FILES: usize = 1024;

#[derive(Clone)]
struct Usage {
    seq: u64,
    occurred_at: String,
    session_id: String,
    model: String,
    input: u64,
    output: u64,
    cache_read: u64,
    cache_write: u64,
}

/// Return a sanitized Claude-native JSONL batch for the existing adapter.
/// Only messages preceding a completed `result` event are admitted.
pub(super) fn sanitized_snapshot(
    leveldb: &Path,
    blobs: &Path,
    now_ms: i64,
) -> Result<Vec<u8>, String> {
    let rows = read_completed(leveldb, blobs)?;
    Ok(sanitized_jsonl(rows, Some(now_ms))?.unwrap_or_default())
}

fn sanitized_jsonl(
    rows: HashMap<String, Usage>,
    admission_now_ms: Option<i64>,
) -> Result<Option<Vec<u8>>, String> {
    if rows.is_empty() {
        return Ok(None);
    }
    let mut payload = Vec::new();
    let mut rows = rows.into_iter().collect::<Vec<_>>();
    rows.sort_by(|left, right| left.0.cmp(&right.0));
    for (message_id, row) in rows {
        if admission_now_ms.is_some_and(|now| !within_admission_day(&row.occurred_at, now)) {
            continue;
        }
        let line = serde_json::json!({
            "type": "assistant",
            "uuid": message_id,
            "timestamp": row.occurred_at,
            "sessionId": row.session_id,
            "message": {
                "id": message_id,
                "model": row.model,
                "usage": {
                    "input_tokens": row.input,
                    "output_tokens": row.output,
                    "cache_read_input_tokens": row.cache_read,
                    "cache_creation_input_tokens": row.cache_write
                }
            }
        });
        serde_json::to_writer(&mut payload, &line).map_err(|_| "claude_json_encode")?;
        payload.push(b'\n');
    }
    Ok((!payload.is_empty()).then_some(payload))
}

fn within_admission_day(occurred_at: &str, now_ms: i64) -> bool {
    chrono::DateTime::parse_from_rfc3339(occurred_at)
        .ok()
        .is_some_and(|time| {
            admit_occurred_at(time.timestamp_millis(), now_ms) == AdmissionDecision::Admit
        })
}

fn read_completed(leveldb: &Path, blobs: &Path) -> Result<HashMap<String, Usage>, String> {
    bounded_leveldb(leveldb)?;
    // leveldb-core reads raw records without opening Chrome's exclusive LOCK.
    let records = leveldb_core::read_dir(leveldb).map_err(|_| "claude_leveldb_read")?;
    let Some((database_id, store_id)) = conversation_tree_ids(&records) else {
        return Ok(HashMap::new());
    };
    let mut latest_trees: HashMap<Vec<u8>, &Record> = HashMap::new();
    let mut blob_records: HashMap<(u64, u64, Vec<u8>), &Record> = HashMap::new();
    for record in &records {
        match key_prefix(&record.key) {
            Some((db, store, 1, _)) if db == database_id && store == store_id => {
                keep_latest(&mut latest_trees, record.key.clone(), record);
            }
            Some((db, store, 3, tail)) if db == database_id && store == store_id => {
                keep_latest(&mut blob_records, (db, store, tail.to_vec()), record);
            }
            _ => {}
        }
    }
    if latest_trees.len() > MAX_CONVERSATIONS {
        return Err("claude_conversation_limit".into());
    }
    let mut usage = HashMap::new();
    for record in latest_trees.values().copied().filter(|r| !r.deleted) {
        let Some((db, store, 1, tail)) = key_prefix(&record.key) else {
            continue;
        };
        let tree = if let Some((index, _declared_len)) = external_pointer(&record.value) {
            let Some(meta) = blob_records
                .get(&(db, store, tail.to_vec()))
                .filter(|r| !r.deleted)
            else {
                continue;
            };
            let Some(blob_number) = blob_number(&meta.value, index) else {
                continue;
            };
            read_blob(blobs, db, blob_number)
        } else {
            // Small Code/Cowork trees stay inline in the LevelDB record.
            varint(&record.value, 0)
                .and_then(|(_, start)| record.value.get(start..))
                .and_then(decode_blink_value)
        };
        let Some(tree) = tree else { continue };
        collect_tree(&tree, &mut usage);
        if usage.len() > MAX_COMPLETED_MESSAGES {
            return Err("claude_message_limit".into());
        }
    }
    Ok(usage)
}

fn conversation_tree_ids(records: &[Record]) -> Option<(u64, u64)> {
    let mut names = HashMap::new();
    for record in records {
        let Some((0, 0, 0, tail)) = key_prefix(&record.key) else {
            continue;
        };
        if tail.first() == Some(&201) {
            keep_latest(&mut names, record.key.clone(), record);
        }
    }
    let mut database = None;
    for record in names.values().copied().filter(|record| !record.deleted) {
        let Some((0, 0, 0, tail)) = key_prefix(&record.key) else {
            continue;
        };
        if tail.first() != Some(&201) || database_name(&tail[1..]).as_deref() != Some(DATABASE) {
            continue;
        }
        let db_id = little_endian(&record.value)?;
        if database.is_none_or(|(seq, _)| record.seq > seq) {
            database = Some((record.seq, db_id));
        }
    }
    let (_, database_id) = database?;
    let mut store_names = HashMap::new();
    for record in records {
        let Some((db, 0, 0, tail)) = key_prefix(&record.key) else {
            continue;
        };
        if db == database_id && tail.first() == Some(&OBJECT_STORE_META_TYPE) {
            keep_latest(&mut store_names, record.key.clone(), record);
        }
    }
    let mut store = None;
    for record in store_names
        .values()
        .copied()
        .filter(|record| !record.deleted)
    {
        let Some((db, 0, 0, tail)) = key_prefix(&record.key) else {
            continue;
        };
        if db != database_id || tail.first() != Some(&OBJECT_STORE_META_TYPE) {
            continue;
        }
        let Some((store_id, at)) = varint(tail, 1) else {
            continue;
        };
        if tail.get(at) != Some(&OS_META_NAME) || utf16be(&record.value).as_deref() != Some(STORE) {
            continue;
        }
        if store.is_none_or(|(seq, _)| record.seq > seq) {
            store = Some((record.seq, store_id));
        }
    }
    Some((database_id, store?.1))
}

fn database_name(data: &[u8]) -> Option<String> {
    let (origin_units, at) = varint(data, 0)?;
    let at = at.checked_add(usize::try_from(origin_units).ok()?.checked_mul(2)?)?;
    let (name_units, at) = varint(data, at)?;
    let end = at.checked_add(usize::try_from(name_units).ok()?.checked_mul(2)?)?;
    utf16be(data.get(at..end)?)
}

fn utf16be(data: &[u8]) -> Option<String> {
    if data.len() % 2 != 0 {
        return None;
    }
    String::from_utf16(
        &data
            .chunks_exact(2)
            .map(|pair| u16::from_be_bytes([pair[0], pair[1]]))
            .collect::<Vec<_>>(),
    )
    .ok()
}

fn little_endian(data: &[u8]) -> Option<u64> {
    if data.is_empty() || data.len() > 8 {
        return None;
    }
    Some(data.iter().enumerate().fold(0u64, |value, (index, byte)| {
        value | (u64::from(*byte) << (8 * index))
    }))
}

fn bounded_leveldb(path: &Path) -> Result<(), String> {
    let mut count = 0usize;
    let mut bytes = 0u64;
    for entry in fs::read_dir(path).map_err(|_| "claude_leveldb_list")? {
        let entry = entry.map_err(|_| "claude_leveldb_list")?;
        let metadata = fs::symlink_metadata(entry.path()).map_err(|_| "claude_leveldb_stat")?;
        if !metadata.is_file() {
            continue;
        }
        count += 1;
        bytes = bytes
            .checked_add(metadata.len())
            .ok_or("claude_leveldb_limit")?;
        if count > MAX_LEVELDB_FILES || bytes > MAX_LEVELDB_BYTES {
            return Err("claude_leveldb_limit".into());
        }
    }
    Ok(())
}

fn keep_latest<'a, K: std::hash::Hash + Eq>(
    map: &mut HashMap<K, &'a Record>,
    key: K,
    record: &'a Record,
) {
    if map.get(&key).is_none_or(|old| record.seq > old.seq) {
        map.insert(key, record);
    }
}

fn key_prefix(key: &[u8]) -> Option<(u64, u64, u64, &[u8])> {
    let lengths = decode_key_prefix_lengths(*key.first()?);
    let db = usize::from(lengths.database_id_len);
    let store = usize::from(lengths.object_store_id_len);
    let index = usize::from(lengths.index_id_len);
    let offset = 1 + db + store + index;
    Some((
        big_endian(key.get(1..1 + db)?),
        big_endian(key.get(1 + db..1 + db + store)?),
        big_endian(key.get(1 + db + store..offset)?),
        key.get(offset..)?,
    ))
}

fn big_endian(bytes: &[u8]) -> u64 {
    bytes
        .iter()
        .fold(0, |value, byte| (value << 8) | u64::from(*byte))
}

fn varint(bytes: &[u8], start: usize) -> Option<(u64, usize)> {
    let mut value = 0u64;
    for index in 0..10 {
        let byte = *bytes.get(start + index)?;
        if index == 9 && byte > 1 {
            return None;
        }
        value |= u64::from(byte & 0x7f).checked_shl(u32::try_from(7 * index).ok()?)?;
        if byte & 0x80 == 0 {
            return Some((value, start + index + 1));
        }
    }
    None
}

fn external_pointer(value: &[u8]) -> Option<(usize, u64)> {
    let (_, start) = varint(value, 0)?; // IndexedDB wrapper version
    let bytes = value.get(start..)?;
    if !bytes.starts_with(&[0xff, 0x11, 0x01]) {
        return None;
    }
    let (size, next) = varint(bytes, 3)?;
    let (index, _) = varint(bytes, next)?;
    Some((usize::try_from(index).ok()?, size))
}

fn blob_number(metadata: &[u8], wanted: usize) -> Option<u64> {
    let mut at = 0;
    let mut index = 0;
    while at < metadata.len() {
        let kind = *metadata.get(at)?;
        if kind != 0 {
            return None;
        } // Blob only; File needs another schema.
        let (number, next) = varint(metadata, at + 1)?;
        let (mime_chars, next) = varint(metadata, next)?;
        let mime_bytes = usize::try_from(mime_chars).ok()?.checked_mul(2)?;
        let next = next.checked_add(mime_bytes)?;
        metadata.get(..next)?;
        let (_, next) = varint(metadata, next)?;
        if index == wanted {
            return Some(number);
        }
        at = next;
        index += 1;
    }
    None
}

fn read_blob(root: &Path, db: u64, number: u64) -> Option<V8Value> {
    let path = root
        .join(format!("{db:x}"))
        .join(format!("{:02x}", number >> 8))
        .join(format!("{number:x}"));
    let meta = fs::symlink_metadata(&path).ok()?;
    if !meta.is_file() || meta.file_type().is_symlink() || meta.len() > MAX_BLOB_BYTES {
        return None;
    }
    let bytes = fs::read(path).ok()?;
    decode_blink_value(&bytes)
}

fn decode_blink_value(bytes: &[u8]) -> Option<V8Value> {
    let decoded = if bytes.starts_with(&[0xff, 0x11, 0x02]) {
        if snap::raw::decompress_len(&bytes[3..]).ok()? > MAX_DECOMPRESSED_BYTES {
            return None;
        }
        snap::raw::Decoder::new().decompress_vec(&bytes[3..]).ok()?
    } else {
        bytes.to_vec()
    };
    if decoded.len() > MAX_DECOMPRESSED_BYTES {
        return None;
    }
    deserialize_blink(&decoded).ok()
}

fn field<'a>(value: &'a V8Value, key: &str) -> Option<&'a V8Value> {
    let V8Value::Object(fields) = value else {
        return None;
    };
    fields
        .iter()
        .find(|(name, _)| name == key)
        .map(|(_, value)| value)
}

fn string(value: &V8Value) -> Option<&str> {
    if let V8Value::String(value) = value {
        Some(value)
    } else {
        None
    }
}

fn number(value: &V8Value) -> Option<u64> {
    match value {
        V8Value::Int(value) => u64::try_from(*value).ok(),
        V8Value::Double(value)
            if value.is_finite()
                && *value >= 0.0
                && value.fract() == 0.0
                && *value <= 9_007_199_254_740_991.0 =>
        {
            Some(*value as u64)
        }
        _ => None,
    }
}

fn collect_tree(tree: &V8Value, out: &mut HashMap<String, Usage>) {
    if !matches!(
        field(tree, "product").and_then(string),
        Some("cowork" | "code")
    ) {
        return;
    }
    let Some(V8Value::Array(events)) = field(tree, "tree").and_then(|v| field(v, "events")) else {
        return;
    };
    let last_result = events
        .iter()
        .filter_map(|event| {
            (field(event, "payload")
                .and_then(|p| field(p, "type"))
                .and_then(string)
                == Some("result"))
            .then(|| field(event, "seq").and_then(number))
            .flatten()
        })
        .max();
    let Some(last_result) = last_result else {
        return;
    };
    for event in events {
        let Some(seq) = field(event, "seq")
            .and_then(number)
            .filter(|seq| *seq <= last_result)
        else {
            continue;
        };
        let Some(payload) = field(event, "payload") else {
            continue;
        };
        if field(payload, "type").and_then(string) != Some("assistant") {
            continue;
        }
        let Some(message) = field(payload, "message") else {
            continue;
        };
        let Some(message_id) = field(message, "id")
            .and_then(string)
            .filter(|id| !id.is_empty())
        else {
            continue;
        };
        let Some(model) = field(message, "model")
            .and_then(string)
            .filter(|id| !id.is_empty())
        else {
            continue;
        };
        let Some(occurred_at) = field(payload, "timestamp").and_then(string) else {
            continue;
        };
        let Some(session_id) = field(payload, "session_id").and_then(string) else {
            continue;
        };
        let Some(tokens) = field(message, "usage") else {
            continue;
        };
        let values = [
            "input_tokens",
            "output_tokens",
            "cache_read_input_tokens",
            "cache_creation_input_tokens",
        ]
        .map(|name| field(tokens, name).and_then(number));
        let [Some(input), Some(output), Some(cache_read), Some(cache_write)] = values else {
            continue;
        };
        if input == 0 && output == 0 && cache_read == 0 && cache_write == 0 {
            continue;
        }
        let row = Usage {
            seq,
            occurred_at: occurred_at.into(),
            session_id: session_id.into(),
            model: model.into(),
            input,
            output,
            cache_read,
            cache_write,
        };
        if out.get(message_id).is_none_or(|old| seq > old.seq) {
            out.insert(message_id.into(), row);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    #[test]
    fn only_completed_cowork_usage_is_extracted() {
        fn object(fields: Vec<(&str, V8Value)>) -> V8Value {
            V8Value::Object(fields.into_iter().map(|(k, v)| (k.into(), v)).collect())
        }
        fn event(seq: i64, kind: &str, message_id: &str, output_tokens: i64) -> V8Value {
            let mut payload = vec![("type", V8Value::String(kind.into()))];
            if kind == "assistant" {
                payload.push(("timestamp", V8Value::String("2026-09-27T09:00:00Z".into())));
                payload.push(("session_id", V8Value::String("session".into())));
                payload.push((
                    "message",
                    object(vec![
                        ("id", V8Value::String(message_id.into())),
                        ("model", V8Value::String("claude-opus".into())),
                        (
                            "usage",
                            object(vec![
                                ("input_tokens", V8Value::Int(1)),
                                ("output_tokens", V8Value::Int(output_tokens)),
                                ("cache_read_input_tokens", V8Value::Int(3)),
                                ("cache_creation_input_tokens", V8Value::Int(4)),
                            ]),
                        ),
                    ]),
                ));
            }
            object(vec![
                ("seq", V8Value::Int(seq)),
                ("payload", object(payload)),
            ])
        }
        let tree = object(vec![
            ("product", V8Value::String("cowork".into())),
            (
                "tree",
                object(vec![(
                    "events",
                    V8Value::Array(vec![
                        event(1, "assistant", "same", 2),
                        event(2, "assistant", "same", 5),
                        event(3, "result", "unused", 0),
                        event(4, "assistant", "unfinished", 9),
                    ]),
                )]),
            ),
        ]);
        let mut output = HashMap::new();
        collect_tree(&tree, &mut output);
        assert_eq!(output.len(), 1);
        assert_eq!(output["same"].cache_read, 3);
        assert_eq!(output["same"].output, 5);
        assert!(!output.contains_key("unfinished"));
        let mut code = tree.clone();
        if let V8Value::Object(fields) = &mut code {
            fields
                .iter_mut()
                .find(|(key, _)| key == "product")
                .unwrap()
                .1 = V8Value::String("code".into());
        }
        let mut code_output = HashMap::new();
        collect_tree(&code, &mut code_output);
        assert_eq!(code_output.len(), 1);
        let mut chat = tree;
        if let V8Value::Object(fields) = &mut chat {
            fields
                .iter_mut()
                .find(|(key, _)| key == "product")
                .unwrap()
                .1 = V8Value::String("chat".into());
        }
        let mut chat_output = HashMap::new();
        collect_tree(&chat, &mut chat_output);
        assert!(chat_output.is_empty());
    }

    #[test]
    fn external_blob_pointer_and_metadata_are_bounded() {
        assert_eq!(external_pointer(&[1, 0xff, 0x11, 1, 8, 0]), Some((0, 8)));
        assert_eq!(blob_number(&[0, 6, 0, 8], 0), Some(6));
        assert_eq!(blob_number(&[0, 6, 0, 8], 1), None);
        assert!(external_pointer(&[1, 0xff, 0x11, 2, 8, 0]).is_none());
    }

    #[test]
    fn inline_and_compressed_blink_values_decode() {
        let mut blink = vec![0xff, 0x15, 0xfe];
        blink.extend([0u8; 12]);
        blink.extend([0xff, 0x10, 0x00, b'I', 0x02]);
        assert_eq!(decode_blink_value(&blink), Some(V8Value::Int(1)));
        let mut compressed = vec![0xff, 0x11, 0x02];
        compressed.extend(snap::raw::Encoder::new().compress_vec(&blink).unwrap());
        assert_eq!(decode_blink_value(&compressed), Some(V8Value::Int(1)));
    }

    #[test]
    fn cached_usage_is_admitted_only_for_the_current_beijing_day() {
        let now = chrono::DateTime::parse_from_rfc3339("2026-09-27T15:00:00Z")
            .unwrap()
            .timestamp_millis();
        assert!(within_admission_day("2026-09-26T16:00:00Z", now));
        assert!(!within_admission_day("2026-09-26T15:59:59Z", now));
        assert!(!within_admission_day("invalid", now));
        let row = |time: &str| Usage {
            seq: 1,
            occurred_at: time.into(),
            session_id: "s".into(),
            model: "claude-opus".into(),
            input: 1,
            output: 2,
            cache_read: 3,
            cache_write: 4,
        };
        let rows = HashMap::from([
            ("today".into(), row("2026-09-26T16:00:00Z")),
            ("earlier".into(), row("2026-09-26T15:59:59Z")),
        ]);
        let today = sanitized_jsonl(rows.clone(), Some(now)).unwrap().unwrap();
        let all = sanitized_jsonl(rows, None).unwrap().unwrap();
        assert_eq!(std::str::from_utf8(&today).unwrap().lines().count(), 1);
        assert_eq!(std::str::from_utf8(&all).unwrap().lines().count(), 2);
    }

    #[test]
    fn resolves_only_live_conversation_tree_metadata() {
        fn utf16be(text: &str) -> Vec<u8> {
            text.encode_utf16().flat_map(u16::to_be_bytes).collect()
        }
        fn record(key: Vec<u8>, value: Vec<u8>, seq: u64, deleted: bool) -> Record {
            Record {
                key,
                value,
                seq,
                deleted,
                origin_file: PathBuf::new(),
            }
        }
        let mut db_key = vec![0, 0, 0, 0, 201, 1, 0, b'x'];
        db_key.push(DATABASE.len() as u8);
        db_key.extend(utf16be(DATABASE));
        let store_key = vec![0, 3, 0, 0, OBJECT_STORE_META_TYPE, 2, OS_META_NAME];
        let rows = vec![
            record(db_key.clone(), vec![3], 1, false),
            record(store_key, utf16be(STORE), 2, false),
        ];
        assert_eq!(conversation_tree_ids(&rows), Some((3, 2)));
        let mut deleted = rows;
        deleted.push(record(db_key, vec![], 3, true));
        assert_eq!(conversation_tree_ids(&deleted), None);
    }
}
