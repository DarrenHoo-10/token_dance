//! Read facade over local harness/model/skill/cost metrics (P4).

use rusqlite::{params, Connection, OptionalExtension};
use serde::Serialize;
use std::collections::BTreeMap;

use super::buckets::Grain;
use super::types::PipelineError;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum CoverageLevel {
    None,
    Partial,
    Complete,
}

#[derive(Debug, Clone, Serialize)]
pub struct CoveredValue {
    pub value: Option<i64>,
    pub known_count: i64,
    pub observed_count: i64,
    pub coverage: CoverageLevel,
}

impl CoveredValue {
    fn from_counts(value: i64, known: i64, observed: i64) -> Self {
        let coverage = if observed <= 0 || known <= 0 {
            CoverageLevel::None
        } else if known >= observed {
            CoverageLevel::Complete
        } else {
            CoverageLevel::Partial
        };
        Self {
            value: if known > 0 { Some(value) } else { None },
            known_count: known,
            observed_count: observed,
            coverage,
        }
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct RatioValue {
    /// Null when denominator is 0.
    pub value: Option<f64>,
    pub numerator: i64,
    pub denominator: i64,
}

#[derive(Debug, Clone, Serialize)]
pub struct CostByCurrency {
    pub currency: String,
    pub reported_cost_units: i64,
    pub calculated_cost_units: i64,
    pub reported_request_count: i64,
    pub calculated_request_count: i64,
    pub unpriced_request_count: i64,
    pub cost_known_count: i64,
}

#[derive(Debug, Default)]
pub struct HarnessDayUsage {
    pub bucket_start: i64,
    pub exact_tokens: i64,
    pub derived_tokens: i64,
    pub token_known_count: i64,
    pub costs: Vec<CostByCurrency>,
}

#[derive(Debug)]
pub struct HarnessUsageHistory {
    pub days: Vec<HarnessDayUsage>,
    pub hours: Vec<(i64, i64)>,
}

/// Read the complete day history and today's hourly series in one writer
/// command, so a metrics commit cannot split the token and cost snapshot.
pub fn query_harness_usage_history(
    conn: &Connection,
    harness_id: &str,
    today_start: i64,
    range_end: i64,
) -> Result<HarnessUsageHistory, PipelineError> {
    let mut days = BTreeMap::<i64, HarnessDayUsage>::new();
    let mut stmt = conn.prepare(
        "SELECT bucket_start,SUM(exact_token_total),SUM(derived_token_total),SUM(token_total_known_count)
         FROM model_metrics
         WHERE delete_at IS NULL AND grain='day' AND harness_id=?1 AND bucket_start<?2
         GROUP BY bucket_start ORDER BY bucket_start",
    )?;
    for row in stmt.query_map(params![harness_id, range_end], |r| {
        Ok(HarnessDayUsage {
            bucket_start: r.get(0)?,
            exact_tokens: r.get(1)?,
            derived_tokens: r.get(2)?,
            token_known_count: r.get(3)?,
            costs: Vec::new(),
        })
    })? {
        let row = row?;
        days.insert(row.bucket_start, row);
    }
    let mut stmt = conn.prepare(
        "SELECT bucket_start,currency,SUM(reported_cost_units),SUM(estimated_cost_units),
                SUM(reported_request_count),SUM(estimated_request_count),SUM(unpriced_request_count),SUM(cost_known_count)
         FROM cost_metrics
         WHERE delete_at IS NULL AND grain='day' AND harness_id=?1 AND bucket_start<?2
         GROUP BY bucket_start,currency ORDER BY bucket_start,currency",
    )?;
    for row in stmt.query_map(params![harness_id, range_end], |r| {
        Ok((
            r.get::<_, i64>(0)?,
            CostByCurrency {
                currency: r.get(1)?,
                reported_cost_units: r.get(2)?,
                calculated_cost_units: r.get(3)?,
                reported_request_count: r.get(4)?,
                calculated_request_count: r.get(5)?,
                unpriced_request_count: r.get(6)?,
                cost_known_count: r.get(7)?,
            },
        ))
    })? {
        let (bucket_start, cost) = row?;
        days.entry(bucket_start)
            .or_insert_with(|| HarnessDayUsage {
                bucket_start,
                ..Default::default()
            })
            .costs
            .push(cost);
    }
    let hours = query_harness_token_series(conn, Grain::Hour, today_start, range_end, harness_id)?;
    Ok(HarnessUsageHistory {
        days: days.into_values().collect(),
        hours,
    })
}

#[derive(Debug, Clone, Serialize)]
pub struct UsageSummary {
    pub grain: String,
    pub range_start: i64,
    pub range_end: i64,
    pub total_tokens: CoveredValue,
    pub model_request_count: i64,
    pub cache_hit_rate: RatioValue,
    pub session_count: i64,
    pub interaction_turn_count: i64,
    pub message_count: i64,
    pub user_message_count: i64,
    pub active_duration_ms: CoveredValue,
    pub tool_call_count: i64,
    pub skill_use_count: i64,
    pub code_generated_lines: i64,
    pub costs: Vec<CostByCurrency>,
}

#[derive(Debug, Clone, Serialize)]
pub struct SkillRankRow {
    pub skill_id: i64,
    pub public_name: Option<String>,
    pub use_count: i64,
    pub success_rate: RatioValue,
    pub duration_ms: i64,
    pub active_days: i64,
}

/// Half-open `[range_start, range_end)` query over a single grain.
pub fn query_usage_summary(
    conn: &Connection,
    grain: Grain,
    range_start: i64,
    range_end: i64,
    harness_id: Option<&str>,
) -> Result<UsageSummary, PipelineError> {
    let (exact, derived, token_known, usage_observed, model_requests, cache_in, cache_read) = conn
        .query_row(
            "SELECT
                COALESCE(SUM(exact_token_total),0),
                COALESCE(SUM(derived_token_total),0),
                COALESCE(SUM(token_total_known_count),0),
                COALESCE(SUM(usage_observed_count),0),
                COALESCE(SUM(model_request_count),0),
                COALESCE(SUM(cache_eligible_input_tokens),0),
                COALESCE(SUM(cache_eligible_read_tokens),0)
             FROM model_metrics
             WHERE delete_at IS NULL
               AND grain=?1
               AND bucket_start >= ?2 AND bucket_start < ?3
               AND (?4 IS NULL OR harness_id=?4)",
            params![grain.as_str(), range_start, range_end, harness_id],
            |r| {
                Ok((
                    r.get::<_, i64>(0)?,
                    r.get::<_, i64>(1)?,
                    r.get::<_, i64>(2)?,
                    r.get::<_, i64>(3)?,
                    r.get::<_, i64>(4)?,
                    r.get::<_, i64>(5)?,
                    r.get::<_, i64>(6)?,
                ))
            },
        )?;

    let (
        session_count,
        turns,
        turn_started,
        turn_completed,
        user_started,
        active_duration,
        duration_known,
        tool_calls,
        skill_uses,
        code_gen,
    ) = conn.query_row(
        "SELECT
            COALESCE(SUM(session_count),0),
            COALESCE(SUM(interaction_turn_count),0),
            COALESCE(SUM(turn_started_count),0),
            COALESCE(SUM(turn_completed_count),0),
            COALESCE(SUM(user_turn_started_count),0),
            COALESCE(SUM(active_duration_ms),0),
            COALESCE(SUM(duration_known_count),0),
            COALESCE(SUM(tool_call_count),0),
            COALESCE(SUM(skill_use_count),0),
            COALESCE(SUM(code_generated_lines),0)
         FROM harness_metrics
         WHERE delete_at IS NULL
           AND grain=?1
           AND bucket_start >= ?2 AND bucket_start < ?3
           AND (?4 IS NULL OR harness_id=?4)",
        params![grain.as_str(), range_start, range_end, harness_id],
        |r| {
            Ok((
                r.get::<_, i64>(0)?,
                r.get::<_, i64>(1)?,
                r.get::<_, i64>(2)?,
                r.get::<_, i64>(3)?,
                r.get::<_, i64>(4)?,
                r.get::<_, i64>(5)?,
                r.get::<_, i64>(6)?,
                r.get::<_, i64>(7)?,
                r.get::<_, i64>(8)?,
                r.get::<_, i64>(9)?,
            ))
        },
    )?;

    let mut cost_stmt = conn.prepare(
        "SELECT currency,
                COALESCE(SUM(reported_cost_units),0),
                COALESCE(SUM(estimated_cost_units),0),
                COALESCE(SUM(reported_request_count),0),
                COALESCE(SUM(estimated_request_count),0),
                COALESCE(SUM(unpriced_request_count),0),
                COALESCE(SUM(cost_known_count),0)
         FROM cost_metrics
         WHERE delete_at IS NULL
           AND grain=?1
           AND bucket_start >= ?2 AND bucket_start < ?3
           AND (?4 IS NULL OR harness_id=?4)
         GROUP BY currency
         ORDER BY currency",
    )?;
    let costs = cost_stmt
        .query_map(
            params![grain.as_str(), range_start, range_end, harness_id],
            |r| {
                Ok(CostByCurrency {
                    currency: r.get(0)?,
                    reported_cost_units: r.get(1)?,
                    calculated_cost_units: r.get(2)?,
                    reported_request_count: r.get(3)?,
                    calculated_request_count: r.get(4)?,
                    unpriced_request_count: r.get(5)?,
                    cost_known_count: r.get(6)?,
                })
            },
        )?
        .collect::<Result<Vec<_>, _>>()?;

    let cache_hit_rate = if cache_in > 0 {
        RatioValue {
            value: Some((cache_read as f64) / (cache_in as f64)),
            numerator: cache_read,
            denominator: cache_in,
        }
    } else {
        RatioValue {
            value: None,
            numerator: cache_read,
            denominator: cache_in,
        }
    };

    Ok(UsageSummary {
        grain: grain.as_str().into(),
        range_start,
        range_end,
        total_tokens: CoveredValue::from_counts(exact + derived, token_known, usage_observed),
        model_request_count: model_requests,
        cache_hit_rate,
        session_count,
        interaction_turn_count: turns,
        message_count: turn_started + turn_completed,
        user_message_count: user_started,
        active_duration_ms: CoveredValue::from_counts(
            active_duration,
            duration_known,
            duration_known,
        ),
        tool_call_count: tool_calls,
        skill_use_count: skill_uses,
        code_generated_lines: code_gen,
        costs,
    })
}

/// Skill ranking for a day-grain range; active_days = DISTINCT day buckets across harnesses.
pub fn query_skill_ranks(
    conn: &Connection,
    range_start: i64,
    range_end: i64,
) -> Result<Vec<SkillRankRow>, PipelineError> {
    let mut stmt = conn.prepare(
        "SELECT sm.skill_id,
                sd.public_name,
                COALESCE(SUM(sm.use_count),0) AS uses,
                COALESCE(SUM(sm.success_count),0) AS ok,
                COALESCE(SUM(sm.failure_count),0) AS fail,
                COALESCE(SUM(sm.duration_ms),0) AS dur
         FROM skill_metrics sm
         JOIN skill_dimensions sd ON sd.id = sm.skill_id
         WHERE sm.delete_at IS NULL
           AND sm.grain='day'
           AND sm.bucket_start >= ?1 AND sm.bucket_start < ?2
         GROUP BY sm.skill_id
         ORDER BY uses DESC, sm.skill_id ASC",
    )?;
    let rows = stmt
        .query_map(params![range_start, range_end], |r| {
            Ok((
                r.get::<_, i64>(0)?,
                r.get::<_, Option<String>>(1)?,
                r.get::<_, i64>(2)?,
                r.get::<_, i64>(3)?,
                r.get::<_, i64>(4)?,
                r.get::<_, i64>(5)?,
            ))
        })?
        .collect::<Result<Vec<_>, _>>()?;

    let mut out = Vec::with_capacity(rows.len());
    for (skill_id, public_name, uses, ok, fail, dur) in rows {
        let denom = ok + fail;
        let success_rate = if denom > 0 {
            RatioValue {
                value: Some((ok as f64) / (denom as f64)),
                numerator: ok,
                denominator: denom,
            }
        } else {
            RatioValue {
                value: None,
                numerator: ok,
                denominator: denom,
            }
        };
        let active_days: i64 = conn.query_row(
            "SELECT COUNT(DISTINCT bucket_start) FROM skill_metrics
             WHERE delete_at IS NULL AND grain='day' AND skill_id=?1
               AND use_count > 0
               AND bucket_start >= ?2 AND bucket_start < ?3",
            params![skill_id, range_start, range_end],
            |r| r.get(0),
        )?;
        out.push(SkillRankRow {
            skill_id,
            public_name,
            use_count: uses,
            success_rate,
            duration_ms: dur,
            active_days,
        });
    }
    Ok(out)
}

/// Ascending `(bucket_start, exact+derived token total)` for one harness/grain.
pub fn query_harness_token_series(
    conn: &Connection,
    grain: Grain,
    range_start: i64,
    range_end: i64,
    harness_id: &str,
) -> Result<Vec<(i64, i64)>, PipelineError> {
    let mut stmt = conn.prepare(
        "SELECT bucket_start,
                COALESCE(SUM(exact_token_total),0) + COALESCE(SUM(derived_token_total),0)
         FROM model_metrics
         WHERE delete_at IS NULL
           AND grain=?1
           AND harness_id=?2
           AND bucket_start >= ?3 AND bucket_start < ?4
         GROUP BY bucket_start
         ORDER BY bucket_start ASC",
    )?;
    let rows = stmt
        .query_map(
            params![grain.as_str(), harness_id, range_start, range_end],
            |r| Ok((r.get::<_, i64>(0)?, r.get::<_, i64>(1)?)),
        )?
        .collect::<Result<Vec<_>, _>>()?;
    Ok(rows)
}

/// Backlog for consumer health surfaces.
#[derive(Debug, Clone, Serialize)]
pub struct ConsumerBacklog {
    pub consumer: String,
    pub pending_or_retry: i64,
    pub oldest_runnable_at: Option<i64>,
}

pub fn query_consumer_backlog(conn: &Connection) -> Result<Vec<ConsumerBacklog>, PipelineError> {
    let mut out = Vec::new();
    for c in ["hour", "day", "month", "upload"] {
        let (n, oldest): (i64, Option<i64>) = conn
            .query_row(
                "SELECT COUNT(*), MIN(runnable_at) FROM processing_tasks
                 WHERE delete_at IS NULL AND consumer=?1 AND runnable_at IS NOT NULL",
                params![c],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .optional()?
            .unwrap_or((0, None));
        out.push(ConsumerBacklog {
            consumer: c.into(),
            pending_or_retry: n,
            oldest_runnable_at: oldest,
        });
    }
    Ok(out)
}
