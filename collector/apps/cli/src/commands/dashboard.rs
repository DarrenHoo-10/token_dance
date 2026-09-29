use std::io::IsTerminal;
use std::time::Duration;

use serde_json::json;

use crate::cli::DashboardArgs;
use crate::ctx::Ctx;
use crate::error::{CliError, CliResult, Kind};
use crate::server::Dashboard;
use crate::signals::Signals;
use crate::snapshot::{self, Range};
use crate::Outcome;

pub fn run(ctx: &Ctx, args: DashboardArgs) -> CliResult<Outcome> {
    match args.scope.as_str() {
        "local" => {}
        "account" => {
            return Err(CliError::new(
                Kind::Credentials,
                "the account scope needs a TokenDance login, which this build does not provide yet",
            )
            .hint("use `--scope local`"))
        }
        other => {
            return Err(CliError::invalid(format!(
                "unknown scope `{other}` (use local or account)"
            )))
        }
    }
    let range = Range::parse(&args.range)?;

    if ctx.json {
        return json_snapshot(ctx, range);
    }

    // Fails fast (exit 6) on a database this build must not read; otherwise validates the read path.
    snapshot::build(&ctx.paths, range, snapshot::now_ms())?;
    let signals = Signals::install();
    let dashboard = Dashboard::start(ctx.paths.clone(), args.port)?;
    let url = dashboard.url();
    println!("TokenDance dashboard (read-only): {url}");
    println!("The link works once; keep this terminal open. Press Ctrl+C to stop. Collection is not affected.");
    let over_ssh =
        std::env::var_os("SSH_CONNECTION").is_some() || std::env::var_os("SSH_TTY").is_some();
    if over_ssh {
        println!(
            "Over SSH, forward the port from your own computer, then open the link there:\n  ssh -L {0}:127.0.0.1:{0} <user>@<host>",
            dashboard.port
        );
    } else if !args.no_open && std::io::stdout().is_terminal() {
        open_browser(&url);
    }
    while !signals.is_set() {
        std::thread::sleep(Duration::from_millis(200));
    }
    dashboard.stop();
    Err(CliError::new(Kind::Interrupted, ""))
}

fn json_snapshot(ctx: &Ctx, range: Range) -> CliResult<Outcome> {
    let data = snapshot::build(&ctx.paths, range, snapshot::now_ms())?;
    if data["partial"] == json!(true) {
        return Err(CliError::new(
            Kind::Incomplete,
            "some blocks could not be read; see data.errors",
        )
        .with_data(data));
    }
    Ok(Outcome {
        command: "dashboard",
        data,
        human: String::new(),
    })
}

fn open_browser(url: &str) {
    #[cfg(target_os = "linux")]
    if std::env::var_os("DISPLAY").is_none() && std::env::var_os("WAYLAND_DISPLAY").is_none() {
        return;
    }
    let mut command = if cfg!(target_os = "macos") {
        std::process::Command::new("open")
    } else if cfg!(windows) {
        let mut c = std::process::Command::new("cmd");
        c.args(["/C", "start", ""]);
        c
    } else {
        std::process::Command::new("xdg-open")
    };
    let _ = command
        .arg(url)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn();
}
