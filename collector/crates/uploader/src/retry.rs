use std::time::Duration;

use rand::Rng;

use crate::error::TransportError;

#[derive(Debug, Clone)]
pub struct RetryPolicy {
    pub initial: Duration,
    pub max: Duration,
    pub max_attempts: u32,
    pub jitter: bool,
}

impl Default for RetryPolicy {
    fn default() -> Self {
        Self {
            initial: Duration::from_secs(2),
            max: Duration::from_secs(15 * 60),
            max_attempts: 12,
            jitter: true,
        }
    }
}

impl RetryPolicy {
    pub fn for_tests() -> Self {
        Self {
            initial: Duration::from_millis(0),
            max: Duration::from_millis(0),
            max_attempts: 4,
            jitter: false,
        }
    }

    pub fn delay_for(&self, attempt: u32, transport: Option<&TransportError>) -> Duration {
        if let Some(after) = transport.and_then(TransportError::retry_after) {
            return after.min(self.max);
        }
        if self.initial.is_zero() {
            return Duration::from_millis(0);
        }
        let mut millis = self.initial.as_millis() as u64;
        for _ in 0..attempt.saturating_sub(1) {
            millis = millis.saturating_mul(2);
        }
        millis = millis.min(self.max.as_millis() as u64);
        if self.jitter && millis > 1 {
            let jitter = rand::thread_rng().gen_range(millis / 2..=millis);
            Duration::from_millis(jitter)
        } else {
            Duration::from_millis(millis)
        }
    }
}
