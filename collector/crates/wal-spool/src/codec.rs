use crate::crypto::{decrypt, encrypt, NONCE_LEN, TAG_LEN};
use crate::error::WalError;

pub const MAGIC: &[u8; 4] = b"TSW1";
pub const TRAILER: &[u8; 4] = b"1WST";
pub const FORMAT_VERSION: u16 = 1;
pub const HEADER_LEN: usize = 20;
pub const FOOTER_LEN: usize = 8;
pub const FLAG_ENCRYPTED: u8 = 0x01;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum FrameType {
    Txn = 1,
    Ack = 2,
    DeadLetter = 3,
    Settings = 4,
}

impl FrameType {
    pub fn from_u8(value: u8) -> Option<Self> {
        match value {
            1 => Some(Self::Txn),
            2 => Some(Self::Ack),
            3 => Some(Self::DeadLetter),
            4 => Some(Self::Settings),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EncodedFrame {
    pub bytes: Vec<u8>,
    pub sequence: u64,
    pub frame_type: FrameType,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DecodedFrame {
    pub sequence: u64,
    pub frame_type: FrameType,
    pub plaintext: Vec<u8>,
    pub frame_len: usize,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FrameRead {
    Frame(DecodedFrame),
    IncompleteTail { good_len: u64 },
    Isolated { good_len: u64, at_offset: u64 },
    End,
}

pub fn encode_frame(
    key: &[u8; 32],
    sequence: u64,
    frame_type: FrameType,
    plaintext: &[u8],
) -> Result<EncodedFrame, WalError> {
    let mut aad = [0u8; 16];
    aad[0..4].copy_from_slice(MAGIC);
    aad[4..6].copy_from_slice(&FORMAT_VERSION.to_le_bytes());
    aad[6] = frame_type as u8;
    aad[7] = FLAG_ENCRYPTED;
    aad[8..16].copy_from_slice(&sequence.to_le_bytes());
    let encrypted = encrypt(key, sequence, &aad, plaintext)?;
    let payload_len = encrypted.len() as u32;
    let mut bytes = Vec::with_capacity(HEADER_LEN + encrypted.len() + FOOTER_LEN);
    bytes.extend_from_slice(MAGIC);
    bytes.extend_from_slice(&FORMAT_VERSION.to_le_bytes());
    bytes.push(frame_type as u8);
    bytes.push(FLAG_ENCRYPTED);
    bytes.extend_from_slice(&sequence.to_le_bytes());
    bytes.extend_from_slice(&payload_len.to_le_bytes());
    bytes.extend_from_slice(&encrypted);
    let crc = crc32c::crc32c(&bytes);
    bytes.extend_from_slice(&crc.to_le_bytes());
    bytes.extend_from_slice(TRAILER);
    Ok(EncodedFrame {
        bytes,
        sequence,
        frame_type,
    })
}

pub fn read_frame(
    data: &[u8],
    offset: u64,
    key: &[u8; 32],
    max_payload: u32,
) -> Result<FrameRead, WalError> {
    let start = offset as usize;
    if start >= data.len() {
        return Ok(FrameRead::End);
    }
    let remaining = data.len() - start;
    if remaining < HEADER_LEN {
        return Ok(FrameRead::IncompleteTail { good_len: offset });
    }
    let header = &data[start..start + HEADER_LEN];
    if &header[0..4] != MAGIC {
        return Ok(isolate_or_tail(data, offset, HEADER_LEN as u64));
    }
    let version = u16::from_le_bytes([header[4], header[5]]);
    if version != FORMAT_VERSION {
        return Ok(isolate_or_tail(data, offset, HEADER_LEN as u64));
    }
    let Some(frame_type) = FrameType::from_u8(header[6]) else {
        return Ok(isolate_or_tail(data, offset, HEADER_LEN as u64));
    };
    let flags = header[7];
    if flags & FLAG_ENCRYPTED == 0 {
        return Err(WalError::PlaintextForbidden);
    }
    let sequence = u64::from_le_bytes([
        header[8], header[9], header[10], header[11], header[12], header[13], header[14],
        header[15],
    ]);
    let payload_len = u32::from_le_bytes([header[16], header[17], header[18], header[19]]);
    if payload_len > max_payload {
        return Ok(isolate_or_tail(data, offset, HEADER_LEN as u64));
    }
    let need = HEADER_LEN + payload_len as usize + FOOTER_LEN;
    if remaining < need {
        if payload_len as usize > max_payload as usize {
            return Ok(FrameRead::Isolated {
                good_len: offset,
                at_offset: offset,
            });
        }
        return Ok(FrameRead::IncompleteTail { good_len: offset });
    }
    let frame = &data[start..start + need];
    let payload = &frame[HEADER_LEN..HEADER_LEN + payload_len as usize];
    let crc = u32::from_le_bytes([
        frame[need - FOOTER_LEN],
        frame[need - FOOTER_LEN + 1],
        frame[need - FOOTER_LEN + 2],
        frame[need - FOOTER_LEN + 3],
    ]);
    let trailer = &frame[need - 4..];
    let header_and_payload = &frame[..HEADER_LEN + payload_len as usize];
    if trailer != TRAILER || crc != crc32c::crc32c(header_and_payload) {
        return Ok(if data.len() > start + need {
            FrameRead::Isolated {
                good_len: offset,
                at_offset: offset,
            }
        } else {
            FrameRead::IncompleteTail { good_len: offset }
        });
    }
    if payload.len() < NONCE_LEN + TAG_LEN {
        return Ok(isolate_or_tail(data, offset, need as u64));
    }
    let mut aad = [0u8; 16];
    aad.copy_from_slice(&header[..16]);
    let plaintext = match decrypt(key, &aad, payload) {
        Ok(plaintext) => plaintext,
        Err(_) => {
            return Ok(if data.len() > start + need {
                FrameRead::Isolated {
                    good_len: offset,
                    at_offset: offset,
                }
            } else {
                FrameRead::IncompleteTail { good_len: offset }
            });
        }
    };
    Ok(FrameRead::Frame(DecodedFrame {
        sequence,
        frame_type,
        plaintext,
        frame_len: need,
    }))
}

fn isolate_or_tail(data: &[u8], offset: u64, declared: u64) -> FrameRead {
    let start = offset as usize;
    let end = start.saturating_add(declared as usize);
    if data.len() > end && data.len() > start + HEADER_LEN {
        FrameRead::Isolated {
            good_len: offset,
            at_offset: offset,
        }
    } else {
        FrameRead::IncompleteTail { good_len: offset }
    }
}
