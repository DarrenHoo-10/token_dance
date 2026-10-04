//! Beijing (UTC+8) bucket boundaries for hour / day / month grains.

use chrono::{Datelike, FixedOffset, NaiveDate, TimeZone, Timelike};

use super::types::{Consumer, PipelineError};

pub const BEIJING_OFFSET_SECS: i32 = 8 * 3600;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Grain {
    Hour,
    Day,
    Month,
}

impl Grain {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Hour => "hour",
            Self::Day => "day",
            Self::Month => "month",
        }
    }

    pub fn from_consumer(consumer: Consumer) -> Result<Self, PipelineError> {
        match consumer {
            Consumer::Hour => Ok(Self::Hour),
            Consumer::Day => Ok(Self::Day),
            Consumer::Month => Ok(Self::Month),
            Consumer::Upload => Err(PipelineError::InvalidArgument(
                "upload consumer has no metrics grain".into(),
            )),
        }
    }
}

fn beijing() -> FixedOffset {
    FixedOffset::east_opt(BEIJING_OFFSET_SECS).expect("UTC+8")
}

/// Half-open Beijing bucket start as UTC epoch ms for `occurred_at`.
pub fn bucket_start(grain: Grain, occurred_at_ms: i64) -> i64 {
    let tz = beijing();
    let dt = tz
        .timestamp_millis_opt(occurred_at_ms)
        .single()
        .expect("valid millis");
    let naive = match grain {
        Grain::Hour => dt
            .date_naive()
            .and_hms_opt(dt.hour(), 0, 0)
            .expect("hour"),
        Grain::Day => dt.date_naive().and_hms_opt(0, 0, 0).expect("day"),
        Grain::Month => NaiveDate::from_ymd_opt(dt.year(), dt.month(), 1)
            .expect("month")
            .and_hms_opt(0, 0, 0)
            .expect("month midnight"),
    };
    tz.from_local_datetime(&naive)
        .single()
        .expect("beijing local")
        .timestamp_millis()
}

/// Beijing calendar-day bucket start (same as `Grain::Day`).
pub fn beijing_day_start(occurred_at_ms: i64) -> i64 {
    bucket_start(Grain::Day, occurred_at_ms)
}

/// Exclusive end of the Beijing day containing `occurred_at_ms`.
pub fn beijing_day_end(occurred_at_ms: i64) -> i64 {
    beijing_day_start(occurred_at_ms) + 86_400_000
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hour_day_month_boundaries_utc8() {
        // 2023-11-14 16:00:00 UTC = 2023-11-15 00:00:00 CST
        let cst_midnight = 1_700_000_000_000i64; // approx; compute exactly
        let tz = beijing();
        let midnight = tz
            .with_ymd_and_hms(2023, 11, 15, 0, 0, 0)
            .single()
            .unwrap()
            .timestamp_millis();
        let mid_hour = midnight + 90 * 60 * 1000; // 01:30 CST
        assert_eq!(bucket_start(Grain::Hour, mid_hour), midnight + 3_600_000);
        assert_eq!(bucket_start(Grain::Day, mid_hour), midnight);
        assert_eq!(
            bucket_start(Grain::Month, mid_hour),
            tz.with_ymd_and_hms(2023, 11, 1, 0, 0, 0)
                .single()
                .unwrap()
                .timestamp_millis()
        );
        let _ = cst_midnight;
    }
}
