//! Read-only dashboard snapshot of the local event database (scope `local`).
//!
//! Windows follow the metric contract: business buckets are fixed to UTC+8, the rolling
//! 24 hours use hour buckets, `day` is the UTC+8 calendar day, 7/30 days use day buckets.
//! Token counts and money are decimal strings so JavaScript never rounds them.

use std::collections::BTreeMap;

use chrono::{FixedOffset, TimeZone};
use collector_service::local_store::pipeline::{
    bucket_start, query_consumer_backlog, query_skill_ranks, query_usage_summary, CoveredValue,
    Grain, PIPELINE_SCHEMA_VERSION,
};
use collector_service::AppPaths;
use rusqlite::{params, Connection};
use serde_json::{json, Map, Value};

use crate::error::{CliError, CliResult, Kind};
use crate::localdb;

pub const HOUR_MS: i64 = 3_600_000;
pub const DAY_MS: i64 = 86_400_000;
const COST_SCALE: i64 = 100_000_000;
const CALENDAR_DAYS: i64 = 365;
const SKILL_LIMIT: usize = 20;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Range {
    Last24h,
    Day,
    Days7,
    Days30,
    All,
}

impl Range {
    pub fn parse(value: &str) -> CliResult<Self> {
        match value {
            "24h" => Ok(Self::Last24h),
            "day" => Ok(Self::Day),
            "7d" => Ok(Self::Days7),
            "30d" => Ok(Self::Days30),
            "all" => Ok(Self::All),
            other => Err(CliError::invalid(format!(
                "unknown range `{other}` (use 24h, day, 7d, 30d or all)"
            ))),
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Last24h => "24h",
            Self::Day => "day",
            Self::Days7 => "7d",
            Self::Days30 => "30d",
            Self::All => "all",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Window {
    pub summary_grain: Grain,
    pub trend_grain: Grain,
    /// Half-open `[start, end)` in UTC milliseconds.
    pub start: i64,
    pub end: i64,
    pub trend_start: i64,
    pub trend_end: i64,
    /// Skill metrics exist at day grain only; `aligned` says whether that matches `[start, end)`.
    pub skills: (i64, i64),
    pub skills_aligned: bool,
}

pub fn window(range: Range, now_ms: i64) -> Window {
    let today = bucket_start(Grain::Day, now_ms);
    let hour = bucket_start(Grain::Hour, now_ms);
    let day_end = today + DAY_MS;
    match range {
        Range::Last24h => Window {
            summary_grain: Grain::Hour,
            trend_grain: Grain::Hour,
            start: hour - 23 * HOUR_MS,
            end: hour + HOUR_MS,
            trend_start: hour - 23 * HOUR_MS,
            trend_end: hour + HOUR_MS,
            skills: (today - DAY_MS, day_end),
            skills_aligned: false,
        },
        Range::Day => Window {
            summary_grain: Grain::Day,
            trend_grain: Grain::Hour,
            start: today,
            end: day_end,
            trend_start: today,
            trend_end: hour + HOUR_MS,
            skills: (today, day_end),
            skills_aligned: true,
        },
        Range::Days7 | Range::Days30 => {
            let days = if range == Range::Days7 { 6 } else { 29 };
            Window {
                summary_grain: Grain::Day,
                trend_grain: Grain::Day,
                start: today - days * DAY_MS,
                end: day_end,
                trend_start: today - days * DAY_MS,
                trend_end: day_end,
                skills: (today - days * DAY_MS, day_end),
                skills_aligned: true,
            }
        }
        Range::All => Window {
            summary_grain: Grain::Month,
            trend_grain: Grain::Month,
            start: 0,
            end: day_end,
            trend_start: 0,
            trend_end: day_end,
            skills: (0, day_end),
            skills_aligned: true,
        },
    }
}

fn beijing_date(ms: i64) -> String {
    FixedOffset::east_opt(8 * 3600)
        .and_then(|tz| tz.timestamp_millis_opt(ms).single())
        .map(|dt| dt.format("%Y-%m-%d").to_string())
        .unwrap_or_default()
}

fn text(n: i64) -> Value {
    Value::String(n.to_string())
}

fn covered(value: &CoveredValue) -> Value {
    json!({
        "value": value.value.map(text),
        "known_count": value.known_count,
        "observed_count": value.observed_count,
        "coverage": serde_json::to_value(value.coverage).unwrap_or(Value::Null),
    })
}

fn costs(costs: &[collector_service::local_store::pipeline::CostByCurrency]) -> Value {
    Value::Array(
        costs
            .iter()
            .map(|c| {
                json!({
                    "currency": c.currency,
                    "unit_scale": COST_SCALE,
                    "reported": text(c.reported_cost_units),
                    "calculated": text(c.calculated_cost_units),
                    "total": text(c.reported_cost_units.saturating_add(c.calculated_cost_units)),
                    "reported_requests": c.reported_request_count,
                    "calculated_requests": c.calculated_request_count,
                    "unpriced_requests": c.unpriced_request_count,
                    "known_count": c.cost_known_count,
                })
            })
            .collect(),
    )
}

type Block = Result<Value, String>;

fn q<T>(r: Result<T, impl std::fmt::Display>) -> Result<T, String> {
    r.map_err(|e| e.to_string())
}

fn summary(conn: &Connection, w: &Window) -> Block {
    let s = q(query_usage_summary(
        conn,
        w.summary_grain,
        w.start,
        w.end,
        None,
    ))?;
    let (input, input_known, output, output_known, cache_read): (i64, i64, i64, i64, i64) = q(conn.query_row(
        "SELECT COALESCE(SUM(input_context_tokens),0), COALESCE(SUM(input_context_known_count),0),
                COALESCE(SUM(output_tokens),0), COALESCE(SUM(output_known_count),0),
                COALESCE(SUM(cache_read_tokens),0)
         FROM model_metrics
         WHERE delete_at IS NULL AND grain=?1 AND bucket_start>=?2 AND bucket_start<?3",
        params![w.summary_grain.as_str(), w.start, w.end],
        |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?)),
    ))?;
    // Counts that are additive but only meaningful when some record actually reported them:
    // "not reported" is null, never zero.
    let (message_known, code_known): (i64, i64) = q(conn.query_row(
        "SELECT COALESCE(SUM(message_known_count),0), COALESCE(SUM(code_known_count),0)
         FROM harness_metrics
         WHERE delete_at IS NULL AND grain=?1 AND bucket_start>=?2 AND bucket_start<?3",
        params![w.summary_grain.as_str(), w.start, w.end],
        |r| Ok((r.get(0)?, r.get(1)?)),
    ))?;
    let known = |ok: bool, n: i64| if ok { text(n) } else { Value::Null };
    let per_line = match (s.total_tokens.value, s.code_generated_lines) {
        (Some(tokens), lines) if lines > 0 && code_known > 0 => {
            Some(format!("{:.1}", tokens as f64 / lines as f64))
        }
        _ => None,
    };
    Ok(json!({
        "tokens": covered(&s.total_tokens),
        "input_context_tokens": { "value": (input_known > 0).then(|| text(input)), "known_count": input_known },
        "output_tokens": { "value": (output_known > 0).then(|| text(output)), "known_count": output_known },
        "cache_read_tokens": text(cache_read),
        "cache_hit_rate": s.cache_hit_rate,
        "model_requests": text(s.model_request_count),
        "messages": known(message_known > 0, s.message_count),
        "user_messages": known(message_known > 0, s.user_message_count),
        "active_duration_ms": covered(&s.active_duration_ms),
        "tool_calls": text(s.tool_call_count),
        "skill_uses": text(s.skill_use_count),
        "code_generated_lines": known(code_known > 0, s.code_generated_lines),
        "tokens_per_code_line": per_line,
        "costs": costs(&s.costs),
    }))
}

fn trend(conn: &Connection, w: &Window) -> Block {
    let mut stmt = q(conn.prepare(
        "SELECT bucket_start, harness_id, SUM(exact_token_total)+SUM(derived_token_total)
         FROM model_metrics
         WHERE delete_at IS NULL AND grain=?1 AND bucket_start>=?2 AND bucket_start<?3
         GROUP BY bucket_start, harness_id ORDER BY bucket_start, harness_id",
    ))?;
    let rows = q(stmt.query_map(
        params![w.trend_grain.as_str(), w.trend_start, w.trend_end],
        |r| {
            Ok((
                r.get::<_, i64>(0)?,
                r.get::<_, String>(1)?,
                r.get::<_, i64>(2)?,
            ))
        },
    ))?;
    let mut buckets: BTreeMap<i64, BTreeMap<String, i64>> = BTreeMap::new();
    for row in rows {
        let (t, harness, tokens) = q(row)?;
        buckets.entry(t).or_default().insert(harness, tokens);
    }
    // Hours and days without rows are drawn as zero; months are only listed where data exists.
    let step = match w.trend_grain {
        Grain::Hour => Some(HOUR_MS),
        Grain::Day => Some(DAY_MS),
        Grain::Month => None,
    };
    if let Some(step) = step {
        let mut t = w.trend_start;
        while t < w.trend_end {
            buckets.entry(t).or_default();
            t += step;
        }
    }
    let points: Vec<Value> = buckets
        .into_iter()
        .map(|(t, by_agent)| {
            let total: i64 = by_agent.values().sum();
            let per: Map<String, Value> = by_agent.into_iter().map(|(k, v)| (k, text(v))).collect();
            json!({ "bucket_start_ms": t, "tokens": text(total), "by_agent": per })
        })
        .collect();
    Ok(json!({ "grain": w.trend_grain.as_str(), "points": points }))
}

fn agents(conn: &Connection, w: &Window) -> Block {
    let mut stmt = q(conn.prepare(
        "SELECT DISTINCT harness_id FROM model_metrics
         WHERE delete_at IS NULL AND grain=?1 AND bucket_start>=?2 AND bucket_start<?3
         ORDER BY harness_id",
    ))?;
    let ids: Vec<String> = q(stmt
        .query_map(params![w.summary_grain.as_str(), w.start, w.end], |r| {
            r.get(0)
        }))?
    .collect::<Result<_, _>>()
    .map_err(|e| e.to_string())?;
    let mut rows = Vec::new();
    for id in ids {
        let s = q(query_usage_summary(
            conn,
            w.summary_grain,
            w.start,
            w.end,
            Some(&id),
        ))?;
        rows.push((s.total_tokens.value.unwrap_or(0), id, s));
    }
    rows.sort_by(|a, b| b.0.cmp(&a.0).then_with(|| a.1.cmp(&b.1)));
    Ok(Value::Array(
        rows.into_iter()
            .map(|(_, id, s)| {
                json!({
                    "agent": id,
                    "tokens": covered(&s.total_tokens),
                    "model_requests": text(s.model_request_count),
                    "costs": costs(&s.costs),
                })
            })
            .collect(),
    ))
}

fn skills(conn: &Connection, w: &Window) -> Block {
    let rows = q(query_skill_ranks(conn, w.skills.0, w.skills.1))?;
    Ok(json!({
        "window": { "start_ms": w.skills.0, "end_ms": w.skills.1, "aligned_with_range": w.skills_aligned },
        "items": rows.into_iter().take(SKILL_LIMIT).map(|r| json!({
            // Names the collector could not make public are not shown.
            "name": r.public_name,
            "uses": text(r.use_count),
            "success_rate": r.success_rate,
            "duration_ms": text(r.duration_ms),
            "active_days": r.active_days,
        })).collect::<Vec<_>>(),
    }))
}

fn calendar(conn: &Connection, now_ms: i64) -> Block {
    let today = bucket_start(Grain::Day, now_ms);
    let start = today - (CALENDAR_DAYS - 1) * DAY_MS;
    let mut stmt = q(conn.prepare(
        "SELECT bucket_start, SUM(exact_token_total)+SUM(derived_token_total)
         FROM model_metrics
         WHERE delete_at IS NULL AND grain='day' AND bucket_start>=?1 AND bucket_start<?2
         GROUP BY bucket_start ORDER BY bucket_start",
    ))?;
    let rows = q(stmt.query_map(params![start, today + DAY_MS], |r| {
        Ok((r.get::<_, i64>(0)?, r.get::<_, i64>(1)?))
    }))?;
    let mut days = Vec::new();
    for row in rows {
        let (t, tokens) = q(row)?;
        days.push(json!({ "date": beijing_date(t), "tokens": text(tokens) }));
    }
    Ok(json!({ "start": beijing_date(start), "end": beijing_date(today), "days": days }))
}

fn collection(conn: Option<&Connection>, paths: &AppPaths) -> Block {
    let process = crate::status::probe(paths);
    let Some(conn) = conn else {
        return Ok(json!({ "process": process, "database": null }));
    };
    let stats = localdb::stats(conn).map_err(|e| e.message)?;
    let last_commit: Option<i64> = q(conn.query_row(
        "SELECT max(updated_at) FROM collection_sources WHERE delete_at IS NULL",
        [],
        |r| r.get(0),
    ))?;
    let backlog = q(query_consumer_backlog(conn))?;
    Ok(json!({
        "process": process,
        "database": {
            "schema_version": stats.schema_version,
            "events": text(stats.events),
            "last_source_update_ms": last_commit,
            "metric_tasks_pending": stats.metric_tasks_pending,
            // Rows recorded here but not uploaded; this build never uploads, so it only grows until sync exists.
            "upload_tasks_pending": stats.upload_tasks_pending,
            "backlog": backlog,
            "sources": stats.sources,
        },
    }))
}

/// Builds the snapshot. Each block fails on its own and is reported under `errors`;
/// only an unreadable or newer database fails the whole call.
pub fn build(paths: &AppPaths, range: Range, now_ms: i64) -> CliResult<Value> {
    let w = window(range, now_ms);
    let conn = localdb::open_read_only(paths)?;
    let mut doc = json!({
        "schema_version": crate::SCHEMA_VERSION,
        "scope": "local",
        "business_timezone": "Asia/Shanghai",
        "generated_at_ms": now_ms,
        "range": range.as_str(),
        "window": {
            "summary_grain": w.summary_grain.as_str(),
            "trend_grain": w.trend_grain.as_str(),
            "start_ms": w.start,
            "end_ms": w.end,
        },
    });
    let ready = match &conn {
        Some(conn) => match localdb::schema_version(conn) {
            Some(v) if v > PIPELINE_SCHEMA_VERSION => {
                return Err(CliError::new(
                    Kind::Incompatible,
                    format!("the event database uses schema {v}, this build supports up to {PIPELINE_SCHEMA_VERSION}"),
                )
                .hint("upgrade tokendance"))
            }
            Some(_) => true,
            None => false,
        },
        None => false,
    };
    let mut errors = Map::new();
    let mut fetched = Map::new();
    let mut put = |doc: &mut Value, name: &str, block: Block| match block {
        Ok(value) => {
            doc[name] = value;
            fetched.insert(name.into(), json!(now_ms));
        }
        Err(message) => {
            doc[name] = Value::Null;
            errors.insert(
                name.into(),
                json!({ "code": "QUERY_FAILED", "message": message }),
            );
        }
    };
    if ready {
        let conn = conn.as_ref().expect("ready implies a connection");
        put(&mut doc, "summary", summary(conn, &w));
        put(&mut doc, "trend", trend(conn, &w));
        put(&mut doc, "agents", agents(conn, &w));
        put(&mut doc, "skills", skills(conn, &w));
        put(&mut doc, "calendar", calendar(conn, now_ms));
        put(&mut doc, "collection", collection(Some(conn), paths));
        doc["state"] = json!("ok");
    } else {
        for name in ["summary", "trend", "agents", "skills", "calendar"] {
            doc[name] = Value::Null;
        }
        put(&mut doc, "collection", collection(None, paths));
        doc["state"] = json!("no_data");
        doc["hint"] = json!("no usage has been recorded yet; run `tokendance init`, then `tokendance collect --once` or `tokendance run`");
    }
    doc["partial"] = json!(!errors.is_empty());
    doc["errors"] = Value::Object(errors);
    doc["fetched_at_ms"] = Value::Object(fetched);
    Ok(doc)
}

pub fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ms(y: i32, m: u32, d: u32, h: u32, min: u32) -> i64 {
        FixedOffset::east_opt(8 * 3600)
            .unwrap()
            .with_ymd_and_hms(y, m, d, h, min, 0)
            .unwrap()
            .timestamp_millis()
    }

    #[test]
    fn windows_follow_beijing_buckets_not_the_host_timezone() {
        // 2026-09-29 00:30 in Beijing is still 09-28 in UTC.
        let now = ms(2026, 9, 29, 0, 30);
        let today = ms(2026, 9, 29, 0, 0);
        let day = window(Range::Day, now);
        assert_eq!((day.start, day.end), (today, today + DAY_MS));
        assert_eq!(day.trend_end, ms(2026, 9, 29, 1, 0));
        let h24 = window(Range::Last24h, now);
        assert_eq!(h24.end - h24.start, 24 * HOUR_MS);
        assert_eq!(h24.end, ms(2026, 9, 29, 1, 0));
        assert!(!h24.skills_aligned);
        let d7 = window(Range::Days7, now);
        assert_eq!((d7.start, d7.end), (today - 6 * DAY_MS, today + DAY_MS));
        let d30 = window(Range::Days30, now);
        assert_eq!(d30.start, today - 29 * DAY_MS);
        let all = window(Range::All, now);
        assert_eq!(all.summary_grain, Grain::Month);
        assert_eq!(all.start, 0);
    }

    #[test]
    fn range_names() {
        for name in ["24h", "day", "7d", "30d", "all"] {
            assert_eq!(Range::parse(name).unwrap().as_str(), name);
        }
        assert!(Range::parse("today").is_err());
    }

    #[test]
    fn dates_are_beijing_dates() {
        assert_eq!(beijing_date(ms(2026, 1, 1, 0, 0)), "2026-01-01");
        assert_eq!(beijing_date(ms(2026, 1, 1, 0, 0) - 1), "2025-12-31");
    }
}
