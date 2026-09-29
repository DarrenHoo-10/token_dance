//! Beijing-day first admission and event-time resolution.

use chrono::{FixedOffset, TimeZone};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TimeSource {
    SourceRecord,
    PreviousRecord,
    FileMtime,
}

impl TimeSource {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::SourceRecord => "source_record",
            Self::PreviousRecord => "previous_record",
            Self::FileMtime => "file_mtime",
        }
    }

    pub fn parse(raw: &str) -> Option<Self> {
        match raw {
            "source_record" => Some(Self::SourceRecord),
            "previous_record" => Some(Self::PreviousRecord),
            "file_mtime" => Some(Self::FileMtime),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ResolvedTime {
    pub occurred_at: i64,
    pub time_source: TimeSource,
    /// True when the stamp came from the native source record (updates last_source_time).
    pub is_native_source: bool,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AdmissionDecision {
    Admit,
    IgnoreOutsideDay,
}

/// UTC+8 calendar day key `YYYY-MM-DD` for an instant in UTC ms.
pub fn beijing_day_key(utc_ms: i64) -> String {
    let offset = FixedOffset::east_opt(8 * 3600).expect("utc+8");
    let secs = utc_ms.div_euclid(1000);
    let nsecs = (utc_ms.rem_euclid(1000) * 1_000_000) as u32;
    let dt = offset
        .timestamp_opt(secs, nsecs)
        .single()
        .unwrap_or_else(|| offset.timestamp_opt(secs, 0).unwrap());
    dt.format("%Y-%m-%d").to_string()
}

/// First-admission: only same Beijing day as `admission_now_ms` may create events.
pub fn admit_occurred_at(occurred_at_ms: i64, admission_now_ms: i64) -> AdmissionDecision {
    if beijing_day_key(occurred_at_ms) == beijing_day_key(admission_now_ms) {
        AdmissionDecision::Admit
    } else {
        AdmissionDecision::IgnoreOutsideDay
    }
}

/// Resolve event time: source_record → previous_record → file_mtime.
/// Never falls back to epoch or wall-clock collection time.
pub fn resolve_event_time(
    source_record_ms: Option<Result<i64, ()>>,
    last_source_time_ms: Option<i64>,
    file_mtime_ms: Option<i64>,
) -> Result<ResolvedTime, super::strategy::IgnoreCode> {
    match source_record_ms {
        Some(Ok(ms)) => {
            if ms < 0 {
                return Err(super::strategy::IgnoreCode::InvalidEventTime);
            }
            // Reject obvious epoch placeholders that legacy adapters used.
            if ms == 0 {
                return Err(super::strategy::IgnoreCode::InvalidEventTime);
            }
            Ok(ResolvedTime {
                occurred_at: ms,
                time_source: TimeSource::SourceRecord,
                is_native_source: true,
            })
        }
        Some(Err(())) => Err(super::strategy::IgnoreCode::InvalidEventTime),
        None => {
            if let Some(prev) = last_source_time_ms {
                if prev > 0 {
                    return Ok(ResolvedTime {
                        occurred_at: prev,
                        time_source: TimeSource::PreviousRecord,
                        is_native_source: false,
                    });
                }
            }
            if let Some(mtime) = file_mtime_ms {
                if mtime > 0 {
                    return Ok(ResolvedTime {
                        occurred_at: mtime,
                        time_source: TimeSource::FileMtime,
                        is_native_source: false,
                    });
                }
            }
            Err(super::strategy::IgnoreCode::MissingEventTime)
        }
    }
}

/// Helper for tests / diagnostics: UTC ms for Beijing local wall time.
pub fn beijing_wall_to_utc_ms(year: i32, month: u32, day: u32, hour: u32, min: u32, sec: u32) -> i64 {
    let offset = FixedOffset::east_opt(8 * 3600).expect("utc+8");
    offset
        .with_ymd_and_hms(year, month, day, hour, min, sec)
        .single()
        .expect("valid beijing wall time")
        .timestamp_millis()
}
