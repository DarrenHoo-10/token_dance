// Each consumer owns its observed session extent; details may expire independently.
pub(super) fn apply_session_extent(
    tx: &Transaction<'_>,
    grain: Grain,
    event: &EventRow,
    semantics: i64,
    now: i64,
) -> Result<(), PipelineError> {
    let Some(key) = event.session_key.as_deref() else {
        return Ok(());
    };
    if event.event_type == "cost_recorded" {
        return Ok(());
    }
    let old:Option<(i64,i64)>=tx.query_row("SELECT first_event_at,last_event_at FROM session_extents WHERE harness_id=?1 AND session_key=?2 AND grain=?3 AND delete_at IS NULL",params![event.harness_id,key,grain.as_str()],|r|Ok((r.get(0)?,r.get(1)?))).optional()?;
    let start = old.map_or(event.occurred_at, |v| v.0.min(event.occurred_at));
    let end = old.map_or(event.occurred_at, |v| v.1.max(event.occurred_at));
    if old == Some((start, end)) {
        return Ok(());
    }
    tx.execute("INSERT INTO session_extents(created_at,updated_at,harness_id,session_key,grain,first_event_at,last_event_at) VALUES(?1,?1,?2,?3,?4,?5,?6) ON CONFLICT(harness_id,session_key,grain) DO UPDATE SET first_event_at=excluded.first_event_at,last_event_at=excluded.last_event_at,updated_at=excluded.updated_at",params![now,event.harness_id,key,grain.as_str(),start,end])?;
    let mut bucket = bucket_start(grain, start);
    loop {
        let next = match grain {
            Grain::Hour => bucket + 3_600_000,
            Grain::Day => bucket + 86_400_000,
            Grain::Month => bucket_start(Grain::Month, bucket + 32 * 86_400_000),
        };
        let duration = (end.min(next) - start.max(bucket)).max(0);
        let prior = old.map_or(0, |(s, e)| (e.min(next) - s.max(bucket)).max(0));
        let was_known = old.is_some_and(|(s, e)| {
            bucket_start(grain, s) <= bucket && bucket <= bucket_start(grain, e)
        });
        let known = if was_known { 0 } else { 1 };
        if duration != prior || known != 0 {
            bump_harness(
                tx,
                grain,
                bucket,
                &event.harness_id,
                semantics,
                now,
                &[
                    ("active_duration_ms", duration - prior),
                    ("duration_known_count", known),
                ],
            )?;
        }
        if next > end {
            break;
        }
        bucket = next;
        if let Some((s, e)) = old {
            let first = bucket_start(grain, s);
            let last = bucket_start(grain, e);
            if bucket > first && bucket < last {
                bucket = last;
            }
        }
    }
    Ok(())
}
