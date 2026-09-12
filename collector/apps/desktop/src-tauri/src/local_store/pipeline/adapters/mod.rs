//! P3 harness adapters: HarnessStrategy implementations + capability matrix.
//!
//! Strategies own discover / read / decode / native_identity. Public runner
//! (P2) owns lease, budget, admission, and checkpoint CAS.

mod capability;
mod codex;
mod common;
mod cursor;
mod cursor_usage;
mod identity;
mod jsonl_harness;
mod jsonl_io;
mod opencode;
mod registry;
mod zcode;

#[cfg(test)]
mod tests;

pub use capability::{
    for_harness, should_throttle, stream_level, CapabilityLevel, HarnessCapability,
    StreamCapability, ALL as CAPABILITY_ALL, CLAUDE as CAP_CLAUDE, CODEX as CAP_CODEX,
    CURSOR as CAP_CURSOR, DEEPSEEK as CAP_DEEPSEEK, DOUBAO as CAP_DOUBAO, GROK as CAP_GROK,
    OPENCODE as CAP_OPENCODE, PI as CAP_PI, WORKBUDDY as CAP_WORKBUDDY, ZCODE as CAP_ZCODE,
};
pub use codex::{decode_otlp_cumulative_for_test, CodexStrategy, HARNESS_ID as CODEX_ID};
pub use common::SkillBook;
pub use cursor::{CursorStrategy, HARNESS_ID as CURSOR_ID};
pub use cursor_usage::CursorUsagePaths;
pub use identity::{
    content_hash, event_id, fact_key, session_key, skill_key, source_key, turn_key, TypedNativeKey,
};
pub use jsonl_harness::{
    JsonlHarnessStrategy, JsonlProfile, CLAUDE, DEEPSEEK, DOUBAO, GROK, PI, WORKBUDDY,
};
pub use opencode::{OpenCodeStrategy, HARNESS_ID as OPENCODE_ID};
pub use registry::{AdapterRoots, HarnessRegistry, SkillAllocator};
pub use zcode::{ZcodeStrategy, HARNESS_ID as ZCODE_ID};
