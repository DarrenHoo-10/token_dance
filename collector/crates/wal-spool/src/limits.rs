use std::time::Duration;

#[derive(Debug, Clone)]
pub struct SpoolLimits {
    pub soft_bytes: u64,
    pub hard_bytes: u64,
    pub soft_events: u64,
    pub hard_events: u64,
    pub rotate_bytes: u64,
    pub rotate_frames: u64,
    pub snapshot_frames: u64,
    pub snapshot_interval: Duration,
    pub max_frame_payload: u32,
}

impl Default for SpoolLimits {
    fn default() -> Self {
        Self {
            soft_bytes: 128 * 1024 * 1024,
            hard_bytes: 256 * 1024 * 1024,
            soft_events: 100_000,
            hard_events: 250_000,
            rotate_bytes: 16 * 1024 * 1024,
            rotate_frames: 10_000,
            snapshot_frames: 10_000,
            snapshot_interval: Duration::from_secs(600),
            max_frame_payload: 4 * 1024 * 1024,
        }
    }
}

impl SpoolLimits {
    pub fn for_tests() -> Self {
        Self {
            soft_bytes: 8 * 1024,
            hard_bytes: 16 * 1024,
            soft_events: 8,
            hard_events: 12,
            rotate_bytes: 768,
            rotate_frames: 3,
            snapshot_frames: 4,
            snapshot_interval: Duration::from_secs(600),
            max_frame_payload: 4 * 1024 * 1024,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AppendClass {
    Realtime,
    Historical,
}
