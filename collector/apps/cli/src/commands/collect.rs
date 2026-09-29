use std::time::Instant;

use serde_json::json;

use crate::cli::CollectArgs;
use crate::ctx::Ctx;
use crate::engine::Engine;
use crate::error::{CliError, CliResult, Kind};
use crate::signals::Signals;
use crate::{localdb, Outcome};

/// Bounded incremental collection: discover, acquire until nothing is due, apply
/// the statistics for what was committed. Never logs in and never uploads.
pub fn run(ctx: &Ctx, args: CollectArgs) -> CliResult<Outcome> {
    let config = ctx.config()?;
    let engine = Engine::open(ctx, &config)?;
    let signals = Signals::install();
    let runtime = &engine.runtime;
    let started = Instant::now();
    engine.log("collect --once start");

    let mut stats = runtime.tick();
    let mut batches = 0usize;
    let mut metrics = 0usize;
    let should_stop = || -> Option<Kind> {
        if signals.is_set() {
            Some(Kind::Interrupted)
        } else if started.elapsed() >= args.timeout {
            Some(Kind::Incomplete)
        } else {
            None
        }
    };
    let mut stop = None;
    while runtime.acquire_one() {
        batches += 1;
        stop = should_stop();
        if stop.is_some() {
            break;
        }
    }
    // Statistics are applied by separate consumers; drain until none is left.
    while stop.is_none() {
        let applied = runtime.drain_metrics();
        metrics += applied;
        if applied == 0 {
            break;
        }
        stop = should_stop();
    }
    let tail = runtime.tick();
    stats.failures += tail.failures;

    let database = localdb::open_read_only(&ctx.paths)?
        .map(|conn| localdb::stats(&conn))
        .transpose()?;
    let complete = stop.is_none();
    let data = json!({
        "complete": complete,
        "elapsed_ms": started.elapsed().as_millis() as u64,
        "sources_known": tail.sources,
        "sources_pending": tail.backlog,
        "batches_committed": batches,
        "metrics_applied": metrics,
        "failures": stats.failures,
        "database": database,
    });
    engine.log(&format!(
        "collect --once end complete={complete} batches={batches} metrics={metrics} failures={}",
        stats.failures
    ));
    let events = database.as_ref().map(|d| d.events).unwrap_or(0);
    let human = format!(
        "Collected {batches} batch(es) from {} source stream(s); {metrics} statistic update(s); {events} event(s) stored; {} failure(s).\n",
        tail.sources, stats.failures
    );
    match stop {
        None => Ok(Outcome {
            command: "collect",
            data,
            human,
        }),
        Some(Kind::Interrupted) => Err(CliError::new(
            Kind::Interrupted,
            "interrupted before collection finished",
        )
        .with_data(data)),
        Some(kind) => Err(CliError::new(
            kind,
            format!(
                "stopped after {}s with {} source stream(s) still pending; run again to continue",
                args.timeout.as_secs(),
                tail.backlog
            ),
        )
        .with_data(data)),
    }
}
