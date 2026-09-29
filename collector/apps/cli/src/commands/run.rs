use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc;
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant};

use serde_json::json;

use crate::cli::RunArgs;
use crate::ctx::Ctx;
use crate::engine::Engine;
use crate::error::{CliError, CliResult, Kind};
use crate::signals::{self, Signals};
use crate::Outcome;

const IDLE_WAIT: Duration = Duration::from_millis(250);
const SHUTDOWN_GRACE: Duration = Duration::from_secs(10);
const SUMMARY_EVERY: Duration = Duration::from_secs(60);

/// Foreground collector: the same worker layout as the desktop daemon
/// (parallel acquisition workers, one metrics consumer, a 1 s maintenance tick).
pub fn run(ctx: &Ctx, args: RunArgs) -> CliResult<Outcome> {
    let config = ctx.config()?;
    let engine = Engine::open(ctx, &config)?;
    let signals = Signals::install();
    let stop = Arc::new(AtomicBool::new(false));
    let (done_tx, done_rx) = mpsc::channel::<()>();
    let mut workers = 0usize;

    for n in
        0..collector_service::local_store::pipeline::runner::DEFAULT_GLOBAL_ACQUISITION_CONCURRENCY
    {
        let runtime = Arc::clone(&engine.runtime);
        let stop = Arc::clone(&stop);
        let done = done_tx.clone();
        thread::Builder::new()
            .name(format!("acquire-{n}"))
            .spawn(move || {
                while !stop.load(Ordering::Acquire) {
                    if !runtime.acquire_one() {
                        thread::sleep(IDLE_WAIT);
                    }
                }
                let _ = done.send(());
            })
            .map_err(|e| CliError::internal(e.to_string()))?;
        workers += 1;
    }
    {
        let runtime = Arc::clone(&engine.runtime);
        let stop = Arc::clone(&stop);
        let done = done_tx.clone();
        thread::Builder::new()
            .name("metrics".into())
            .spawn(move || {
                while !stop.load(Ordering::Acquire) {
                    if runtime.drain_metrics() == 0 {
                        thread::sleep(IDLE_WAIT);
                    }
                }
                let _ = done.send(());
            })
            .map_err(|e| CliError::internal(e.to_string()))?;
        workers += 1;
    }
    drop(done_tx);

    engine.log("run start");
    if !args.quiet {
        eprintln!("tokendance: collecting; press Ctrl+C to stop");
    }
    let started = Instant::now();
    let mut last_summary = Instant::now();
    let (mut acquired, mut applied, mut failures) = (0usize, 0usize, 0usize);
    let mut last_failed = false;
    while !signals.is_set() {
        let stats = engine.runtime.tick();
        acquired += stats.acquired;
        applied += stats.metrics;
        failures += stats.failures;
        if stats.failures > 0 && !last_failed {
            eprintln!(
                "tokendance: {} source read(s) failed; retrying",
                stats.failures
            );
        }
        last_failed = stats.failures > 0;
        if !args.quiet && last_summary.elapsed() >= SUMMARY_EVERY {
            eprintln!(
                "tokendance: {} stream(s), {} pending, {acquired} batch(es), {applied} update(s) so far",
                stats.sources, stats.backlog
            );
            last_summary = Instant::now();
        }
        thread::sleep(Duration::from_secs(1));
    }

    // Stop taking new work, then give in-flight reads a bounded time to finish.
    let cause = signals.received();
    stop.store(true, Ordering::Release);
    if !args.quiet {
        eprintln!("tokendance: stopping");
    }
    let deadline = Instant::now() + SHUTDOWN_GRACE;
    let mut finished = 0;
    while finished < workers {
        let left = deadline.saturating_duration_since(Instant::now());
        if done_rx.recv_timeout(left).is_err() {
            break;
        }
        finished += 1;
    }
    let clean = finished == workers;
    engine.log(&format!("run stop cause={cause} clean={clean}"));
    let data = json!({
        "elapsed_ms": started.elapsed().as_millis() as u64,
        "batches_committed": acquired,
        "metrics_applied": applied,
        "failures": failures,
        "clean_shutdown": clean,
        "stopped_by": if cause == signals::TERMINATE { "sigterm" } else { "interrupt" },
    });
    // Unfinished reads are recovered from their persisted leases on the next start.
    if cause == signals::INTERRUPT {
        return Err(CliError::new(Kind::Interrupted, "stopped by interrupt").with_data(data));
    }
    Ok(Outcome {
        command: "run",
        data,
        human: format!("Stopped after {acquired} batch(es).\n"),
    })
}
