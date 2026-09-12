//! Per-harness capability matrix: unsupported formats degrade / throttle,
//! never stall the global acquisition scheduler.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum CapabilityLevel {
    Available,
    Degraded,
    Unavailable,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct StreamCapability {
    pub stream_key: &'static str,
    pub level: CapabilityLevel,
    pub reason_code: Option<&'static str>,
    /// Recommended poll interval when degraded (ms). None = default.
    pub poll_interval_ms: Option<u64>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct HarnessCapability {
    pub harness_id: &'static str,
    pub streams: &'static [StreamCapability],
}

pub const CODEX: HarnessCapability = HarnessCapability {
    harness_id: "codex",
    streams: &[
        StreamCapability {
            stream_key: "sessions-jsonl",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "otlp",
            level: CapabilityLevel::Degraded,
            reason_code: Some("CODEX_OTLP_SECONDARY_AUTHORITY"),
            poll_interval_ms: Some(30_000),
        },
    ],
};

pub const CLAUDE: HarnessCapability = HarnessCapability {
    harness_id: "claude-code",
    streams: &[
        StreamCapability {
            stream_key: "projects-jsonl",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "otlp",
            level: CapabilityLevel::Degraded,
            reason_code: Some("CLAUDE_OTLP_SECONDARY_AUTHORITY"),
            poll_interval_ms: Some(30_000),
        },
    ],
};

pub const CURSOR: HarnessCapability = HarnessCapability {
    harness_id: "cursor",
    streams: &[
        StreamCapability {
            stream_key: "transcript-jsonl",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "official-usage-events",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "remote-api-aggregate",
            level: CapabilityLevel::Unavailable,
            reason_code: Some("CURSOR_API_AGGREGATE_NOT_REQUEST_DETAIL"),
            poll_interval_ms: Some(300_000),
        },
    ],
};

pub const ZCODE: HarnessCapability = HarnessCapability {
    harness_id: "zcode",
    streams: &[
        StreamCapability {
            stream_key: "sqlite/session",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "sqlite/model_usage",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "sqlite/code_part",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "runtime",
            level: CapabilityLevel::Degraded,
            reason_code: Some("ZCODE_RUNTIME_NEEDS_VERIFICATION"),
            poll_interval_ms: Some(15_000),
        },
    ],
};

pub const OPENCODE: HarnessCapability = HarnessCapability {
    harness_id: "opencode",
    streams: &[
        StreamCapability {
            stream_key: "sqlite/session",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "sqlite/step_finish",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "sqlite/code_part",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
    ],
};

pub const GROK: HarnessCapability = HarnessCapability {
    harness_id: "grok-build",
    streams: &[
        StreamCapability {
            stream_key: "updates-jsonl",
            level: CapabilityLevel::Available,
            reason_code: None,
            poll_interval_ms: None,
        },
        StreamCapability {
            stream_key: "otlp",
            level: CapabilityLevel::Degraded,
            reason_code: Some("GROK_OTLP_SECONDARY_AUTHORITY"),
            poll_interval_ms: Some(30_000),
        },
        StreamCapability {
            stream_key: "chat-history",
            level: CapabilityLevel::Unavailable,
            reason_code: Some("GROK_CHAT_HISTORY_NOT_AUTHORITY"),
            poll_interval_ms: Some(600_000),
        },
    ],
};

pub const DEEPSEEK: HarnessCapability = HarnessCapability {
    harness_id: "deepseek-harness",
    streams: &[StreamCapability {
        stream_key: "sessions-jsonl",
        level: CapabilityLevel::Available,
        reason_code: None,
        poll_interval_ms: None,
    }],
};

pub const PI: HarnessCapability = HarnessCapability {
    harness_id: "pi",
    streams: &[StreamCapability {
        stream_key: "sessions-jsonl",
        level: CapabilityLevel::Available,
        reason_code: None,
        poll_interval_ms: None,
    }],
};

pub const WORKBUDDY: HarnessCapability = HarnessCapability {
    harness_id: "workbuddy",
    streams: &[StreamCapability {
        stream_key: "history-jsonl",
        level: CapabilityLevel::Available,
        reason_code: None,
        poll_interval_ms: None,
    }],
};

pub const DOUBAO: HarnessCapability = HarnessCapability {
    harness_id: "doubao-work",
    streams: &[StreamCapability {
        stream_key: "history-jsonl",
        level: CapabilityLevel::Available,
        reason_code: None,
        poll_interval_ms: None,
    }],
};

pub const ALL: &[&HarnessCapability] = &[
    &CODEX, &CLAUDE, &CURSOR, &ZCODE, &OPENCODE, &GROK, &DEEPSEEK, &PI, &WORKBUDDY, &DOUBAO,
];

pub fn for_harness(harness_id: &str) -> Option<&'static HarnessCapability> {
    ALL.iter().copied().find(|c| c.harness_id == harness_id)
}

pub fn stream_level(harness_id: &str, stream_key: &str) -> Option<CapabilityLevel> {
    for_harness(harness_id).and_then(|h| {
        h.streams
            .iter()
            .find(|s| s.stream_key == stream_key)
            .map(|s| s.level)
    })
}

/// Streams that must not be scheduled at the default high frequency.
pub fn should_throttle(harness_id: &str, stream_key: &str) -> bool {
    matches!(
        stream_level(harness_id, stream_key),
        Some(CapabilityLevel::Degraded | CapabilityLevel::Unavailable)
    )
}
