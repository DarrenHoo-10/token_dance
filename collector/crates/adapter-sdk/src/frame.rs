use protocol::SourceKind;

/// Minimum raw unit handed from acquisition to an Adapter `decode` call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RawFrame {
    pub installation_id: String,
    pub source_kind: SourceKind,
    pub source_id: String,
    pub cursor: String,
    pub payload: Vec<u8>,
}

impl RawFrame {
    pub fn jsonl(
        installation_id: impl Into<String>,
        source_id: impl Into<String>,
        cursor: impl Into<String>,
        payload: impl Into<Vec<u8>>,
    ) -> Self {
        Self {
            installation_id: installation_id.into(),
            source_kind: SourceKind::JsonlTail,
            source_id: source_id.into(),
            cursor: cursor.into(),
            payload: payload.into(),
        }
    }
}
