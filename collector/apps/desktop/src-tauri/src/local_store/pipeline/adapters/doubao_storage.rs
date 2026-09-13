//! Read-only LevelDB/Snappy and Blink/V8 decoding. Never open the app database for writing.
use serde_json::{json, Map, Value};
use std::path::Path;
const MAX: usize = 32 * 1024 * 1024;
type Result<T> = std::result::Result<T, String>;
fn take<'a>(b: &'a [u8], p: &mut usize, n: usize) -> Result<&'a [u8]> {
    let end = p.checked_add(n).ok_or("length")?;
    let v = b.get(*p..end).ok_or("truncated")?;
    *p = end;
    Ok(v)
}
fn var(b: &[u8], p: &mut usize) -> Result<u64> {
    let mut n = 0;
    for shift in (0..64).step_by(7) {
        let x = take(b, p, 1)?[0];
        if shift == 63 && x > 1 {
            return Err("varint overflow".into());
        }
        n |= ((x & 127) as u64) << shift;
        if x < 128 {
            return Ok(n);
        }
    }
    Err("varint".into())
}
fn size(b: &[u8], p: &mut usize) -> Result<usize> {
    let n = var(b, p)?;
    if n > MAX as u64 {
        return Err("size limit".into());
    }
    Ok(n as usize)
}
pub(super) fn snappy(b: &[u8]) -> Result<Vec<u8>> {
    let mut p = 0;
    let n = size(b, &mut p)?;
    let mut out = Vec::with_capacity(n);
    while out.len() < n {
        let tag = take(b, &mut p, 1)?[0];
        let kind = tag & 3;
        if kind == 0 {
            let mut len = (tag >> 2) as usize;
            if len < 60 {
                len += 1
            } else {
                let bytes = take(b, &mut p, len - 59)?;
                len = bytes
                    .iter()
                    .enumerate()
                    .fold(0usize, |s, (i, v)| s | ((*v as usize) << (i * 8)))
                    + 1
            }
            if len > n - out.len() {
                return Err("literal length".into());
            }
            out.extend_from_slice(take(b, &mut p, len)?);
        } else {
            let (len, offset) = match kind {
                1 => (
                    4 + ((tag >> 2) & 7) as usize,
                    (((tag & 224) as usize) << 3) | take(b, &mut p, 1)?[0] as usize,
                ),
                2 => (
                    (tag >> 2) as usize + 1,
                    u16::from_le_bytes(take(b, &mut p, 2)?.try_into().unwrap()) as usize,
                ),
                _ => (
                    1 + (tag >> 2) as usize,
                    u32::from_le_bytes(take(b, &mut p, 4)?.try_into().unwrap()) as usize,
                ),
            };
            if offset == 0 || offset > out.len() || len > n - out.len() {
                return Err("copy length".into());
            }
            for _ in 0..len {
                out.push(out[out.len() - offset]);
            }
        }
    }
    Ok(out)
}
fn entries(b: &[u8]) -> Result<Vec<(Vec<u8>, Vec<u8>)>> {
    if b.len() < 4 {
        return Err("block footer".into());
    }
    let n = u32::from_le_bytes(b[b.len() - 4..].try_into().unwrap()) as usize;
    let end = b
        .len()
        .checked_sub(
            n.checked_add(1)
                .and_then(|n| n.checked_mul(4))
                .ok_or("restarts")?,
        )
        .ok_or("restarts")?;
    let (mut p, mut last, mut out) = (0, Vec::new(), Vec::new());
    while p < end {
        let shared = size(b, &mut p)?;
        let kn = size(b, &mut p)?;
        let vn = size(b, &mut p)?;
        if shared > last.len() {
            return Err("shared key".into());
        }
        let mut key = last[..shared].to_vec();
        key.extend_from_slice(take(b, &mut p, kn)?);
        let value = take(b, &mut p, vn)?.to_vec();
        if p > end {
            return Err("entry overflow".into());
        }
        last = key.clone();
        out.push((key, value));
    }
    Ok(out)
}
fn masked_crc(b: &[u8]) -> u32 {
    let mut crc = !0u32;
    for byte in b {
        crc ^= *byte as u32;
        for _ in 0..8 {
            crc = (crc >> 1) ^ (0x82f63b78u32.wrapping_mul(crc & 1));
        }
    }
    (!crc).rotate_right(15).wrapping_add(0xa282ead8)
}
fn block(b: &[u8], offset: u64, len: u64) -> Result<Vec<u8>> {
    let mut p = usize::try_from(offset).map_err(|_| "offset")?;
    let n = usize::try_from(len).map_err(|_| "length")?;
    let raw = take(b, &mut p, n)?;
    let kind = take(b, &mut p, 1)?[0];
    let expected = u32::from_le_bytes(take(b, &mut p, 4)?.try_into().unwrap());
    let mut checked = raw.to_vec();
    checked.push(kind);
    if masked_crc(&checked) != expected {
        return Err("block checksum".into());
    }
    match kind {
        0 => Ok(raw.to_vec()),
        1 => snappy(raw),
        _ => Err("unsupported block compression".into()),
    }
}
fn sst(b: &[u8]) -> Result<Vec<(Vec<u8>, Vec<u8>)>> {
    if b.len() < 48 || b[b.len() - 8..] != 0xdb4775248b80fb57u64.to_le_bytes() {
        return Err("SST footer".into());
    }
    let footer = &b[b.len() - 48..];
    let mut p = 0;
    var(footer, &mut p)?;
    var(footer, &mut p)?;
    let off = var(footer, &mut p)?;
    let len = var(footer, &mut p)?;
    let mut out = Vec::new();
    for (_, handle) in entries(&block(b, off, len)?)? {
        let mut p = 0;
        let off = var(&handle, &mut p)?;
        let len = var(&handle, &mut p)?;
        out.extend(entries(&block(b, off, len)?)?);
    }
    Ok(out)
}
fn log_records(b: &[u8]) -> Result<Vec<Vec<u8>>> {
    let mut out = Vec::new();
    let mut pending = Vec::new();
    for chunk in b.chunks(32768) {
        let mut p = 0;
        while p + 7 <= chunk.len() {
            let len = u16::from_le_bytes(chunk[p + 4..p + 6].try_into().unwrap()) as usize;
            let kind = chunk[p + 6];
            let expected = u32::from_le_bytes(chunk[p..p + 4].try_into().unwrap());
            p += 7;
            if len == 0 {
                break;
            }
            if p + len > chunk.len() {
                break;
            }
            let value = &chunk[p..p + len];
            p += len;
            let mut checked = vec![kind];
            checked.extend_from_slice(value);
            if masked_crc(&checked) != expected {
                return Err("log checksum".into());
            }
            match kind {
                1 => {
                    pending.clear();
                    out.push(value.to_vec())
                }
                2 => pending = value.to_vec(),
                3 | 4 => {
                    if pending.is_empty() {
                        return Err("orphan log fragment".into());
                    }
                    if pending.len() + len > MAX {
                        return Err("log record limit".into());
                    }
                    pending.extend_from_slice(value);
                    if kind == 4 {
                        out.push(std::mem::take(&mut pending));
                    }
                }
                _ => return Err("log fragment".into()),
            }
        }
    }
    Ok(out)
}
/// Return key, sequence, tombstone, value; SST/log contain multiple revisions of keys.
pub(super) fn read_file(path: &Path) -> Result<Vec<(Vec<u8>, u64, bool, Vec<u8>)>> {
    let meta = path.metadata().map_err(|_| "cache metadata")?;
    if meta.len() > MAX as u64 {
        return Err("cache file limit".into());
    }
    let b = std::fs::read(path).map_err(|_| "cache read")?;
    let mut out = Vec::new();
    if path.extension().is_some_and(|s| s == "ldb" || s == "sst") {
        for (mut key, value) in sst(&b)? {
            if key.len() < 8 {
                continue;
            }
            let tag = u64::from_le_bytes(key[key.len() - 8..].try_into().unwrap());
            key.truncate(key.len() - 8);
            out.push((key, tag >> 8, tag & 255 == 0, value));
        }
    } else {
        for record in log_records(&b)? {
            if record.len() < 12 {
                return Err("write batch".into());
            }
            let seq = u64::from_le_bytes(record[..8].try_into().unwrap());
            let count = u32::from_le_bytes(record[8..12].try_into().unwrap());
            let mut p = 12;
            for i in 0..count {
                let tag = take(&record, &mut p, 1)?[0];
                let n = size(&record, &mut p)?;
                let key = take(&record, &mut p, n)?.to_vec();
                let value = if tag == 1 {
                    let n = size(&record, &mut p)?;
                    take(&record, &mut p, n)?.to_vec()
                } else if tag == 0 {
                    Vec::new()
                } else {
                    return Err("batch tag".into());
                };
                out.push((key, seq + i as u64, tag == 0, value));
            }
        }
    }
    Ok(out)
}
struct V8<'a> {
    b: &'a [u8],
    p: usize,
    refs: Vec<Value>,
    nodes: usize,
}
impl V8<'_> {
    fn value(&mut self, depth: usize) -> Result<Value> {
        self.nodes += 1;
        if depth > 100 || self.nodes > 200_000 {
            return Err("V8 limit".into());
        }
        let mut tag = take(self.b, &mut self.p, 1)?[0];
        while tag == 0 {
            tag = take(self.b, &mut self.p, 1)?[0]
        }
        match tag {
            b'_' | b'0' | b'-' => Ok(Value::Null),
            b'T' => Ok(json!(true)),
            b'F' => Ok(json!(false)),
            b'I' => {
                let n = var(self.b, &mut self.p)? as u32;
                Ok(json!(((n >> 1) as i32) ^ -((n & 1) as i32)))
            }
            b'U' => Ok(json!(var(self.b, &mut self.p)?)),
            b'N' => {
                let n = f64::from_le_bytes(take(self.b, &mut self.p, 8)?.try_into().unwrap());
                Ok(json!(n))
            }
            b'"' | b'S' | b'c' => {
                let n = size(self.b, &mut self.p)?;
                let b = take(self.b, &mut self.p, n)?;
                let s = if tag == b'c' {
                    if n % 2 != 0 {
                        return Err("utf16".into());
                    }
                    String::from_utf16_lossy(
                        &b.chunks_exact(2)
                            .map(|c| u16::from_le_bytes([c[0], c[1]]))
                            .collect::<Vec<_>>(),
                    )
                } else if tag == b'"' {
                    b.iter().map(|c| char::from(*c)).collect()
                } else {
                    String::from_utf8_lossy(b).into_owned()
                };
                Ok(json!(s))
            }
            b'^' => {
                let id = size(self.b, &mut self.p)?;
                self.refs.get(id).cloned().ok_or("V8 reference".into())
            }
            b'o' | b'A' | b'a' => {
                let id = self.refs.len();
                self.refs.push(Value::Null);
                let len = if tag != b'o' {
                    size(self.b, &mut self.p)?
                } else {
                    0
                };
                if len > 200_000 {
                    return Err("array limit".into());
                }
                let mut array = Vec::new();
                if tag == b'A' {
                    for _ in 0..len {
                        array.push(self.value(depth + 1)?);
                    }
                }
                let end = if tag == b'o' {
                    b'{'
                } else if tag == b'A' {
                    b'$'
                } else {
                    b'@'
                };
                let mut map = Map::new();
                while *self.b.get(self.p).ok_or("object end")? != end {
                    let key = self.value(depth + 1)?;
                    let value = self.value(depth + 1)?;
                    let key = key
                        .as_str()
                        .map(str::to_owned)
                        .unwrap_or_else(|| key.to_string());
                    map.insert(key, value);
                }
                self.p += 1;
                let count = size(self.b, &mut self.p)?;
                if count != map.len() {
                    return Err("object count".into());
                }
                if tag != b'o' {
                    if size(self.b, &mut self.p)? != len {
                        return Err("array count".into());
                    }
                    if tag == b'a' {
                        array = vec![Value::Null; len];
                        for (k, v) in map {
                            if let Ok(i) = k.parse::<usize>() {
                                if i < len {
                                    array[i] = v
                                }
                            }
                        }
                    }
                    let value = json!(array);
                    self.refs[id] = value.clone();
                    Ok(value)
                } else {
                    let value = Value::Object(map);
                    self.refs[id] = value.clone();
                    Ok(value)
                }
            }
            b'D' => {
                let n = f64::from_le_bytes(take(self.b, &mut self.p, 8)?.try_into().unwrap());
                let v = json!(n);
                self.refs.push(v.clone());
                Ok(v)
            }
            _ => Err(format!("unsupported V8 tag {tag:02x}")),
        }
    }
}
/// Blink wrappers precede the V8 header; large IndexedDB values may be Snappy wrapped.
pub(super) fn objects(b: &[u8]) -> Vec<Value> {
    let mut buffers = Vec::new();
    for (i, w) in b.windows(3).enumerate() {
        if w == [255, 17, 2] {
            if let Ok(raw) = snappy(&b[i + 3..]) {
                buffers.push(raw);
            }
        }
    }
    let mut out = Vec::new();
    for bytes in std::iter::once(b).chain(buffers.iter().map(Vec::as_slice)) {
        for (i, w) in bytes.windows(2).enumerate() {
            if w == [255, 15] {
                let mut parser = V8 {
                    b: bytes,
                    p: i + 2,
                    refs: Vec::new(),
                    nodes: 0,
                };
                if let Ok(v) = parser.value(0) {
                    out.push(v)
                }
            }
        }
    }
    out
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn v8_object_and_truncation() {
        let bytes = [255, 15, b'o', b'"', 1, b'x', b'I', 14, b'{', 1];
        assert_eq!(objects(&bytes), vec![json!({"x":7})]);
        assert!(objects(&bytes[..bytes.len() - 1]).is_empty());
    }
    #[test]
    fn compressed_wrapper_and_array_references() {
        let raw = [
            255, 15, b'A', 2, b'o', b'"', 1, b'x', b'I', 14, b'{', 1, b'^', 1, b'$', 0, 2,
        ];
        let mut wrapped = vec![255, 17, 2, raw.len() as u8, ((raw.len() - 1) * 4) as u8];
        wrapped.extend(raw);
        let decoded = objects(&wrapped);
        assert!(!decoded.is_empty());
        assert!(decoded.iter().all(|v| *v == json!([{"x":7},{"x":7}])));
    }
    #[test]
    fn wal_checksums_and_incomplete_tail() {
        let data = b"test";
        let mut checked = vec![1];
        checked.extend(data);
        let mut b = masked_crc(&checked).to_le_bytes().to_vec();
        b.extend((data.len() as u16).to_le_bytes());
        b.push(1);
        b.extend(data);
        assert_eq!(log_records(&b).unwrap(), vec![data.to_vec()]);
        assert!(log_records(&b[..b.len() - 1]).unwrap().is_empty());
        let mut broken = b.clone();
        broken[7] ^= 1;
        assert!(log_records(&broken).is_err());
        b.extend([0, 0, 0]);
        assert_eq!(log_records(&b).unwrap().len(), 1);
    }
    #[test]
    fn snappy_checks_bounds() {
        assert_eq!(snappy(&[3, 8, b'a', b'b', b'c']).unwrap(), b"abc");
        assert!(snappy(&[3, 1, 0]).is_err());
        assert!(snappy(&[2, 8, b'a', b'b', b'c']).is_err());
    }
}
