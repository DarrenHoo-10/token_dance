use std::path::PathBuf;
use std::time::Duration;

use clap::{Args, Parser, Subcommand};

#[derive(Parser, Debug)]
#[command(
    name = "tokendance",
    version,
    about = "TokenDance command-line collector",
    propagate_version = true
)]
pub struct Cli {
    /// Print one structured JSON object on stdout (results and errors); logs go to stderr.
    #[arg(long, global = true)]
    pub json: bool,
    /// Never wait for a prompt; fail immediately when input would be needed.
    #[arg(long, global = true)]
    pub no_input: bool,
    /// Use this data directory instead of the platform default (separate environments).
    #[arg(long, global = true, env = "TOKENDANCE_DATA_DIR", value_name = "DIR")]
    pub data_dir: Option<PathBuf>,
    /// Use this config file.
    #[arg(long, global = true, env = "TOKENDANCE_CONFIG", value_name = "FILE")]
    pub config: Option<PathBuf>,
    #[command(subcommand)]
    pub command: Command,
}

#[derive(Subcommand, Debug)]
pub enum Command {
    /// Create the private data directory, pick the credential backend and the sources.
    Init(InitArgs),
    /// Enable, disable or relocate a collection source.
    Sources {
        #[command(subcommand)]
        action: SourcesCommand,
    },
    /// Collect new usage from the enabled sources.
    Collect(CollectArgs),
    /// Collect continuously in the foreground until interrupted.
    Run(RunArgs),
    /// Read-only diagnostics of paths, permissions, credentials and the database.
    Doctor,
    /// Serve the read-only local dashboard, or print one JSON snapshot with `--json`.
    Dashboard(DashboardArgs),
}

#[derive(Args, Debug)]
pub struct DashboardArgs {
    /// `local` (this machine's recorded usage). `account` needs `tokendance login`, which this build lacks.
    #[arg(long, default_value = "local", value_name = "SCOPE")]
    pub scope: String,
    /// Time window: 24h (rolling), day (UTC+8 calendar day), 7d, 30d or all. Used by `--json`; the page has its own selector.
    #[arg(long, default_value = "24h", value_name = "RANGE")]
    pub range: String,
    /// Do not open a browser; only print the address.
    #[arg(long)]
    pub no_open: bool,
    /// Listen on this loopback port (default: let the system choose a free one).
    #[arg(long, default_value_t = 0, value_name = "PORT")]
    pub port: u16,
}

#[derive(Args, Debug)]
pub struct InitArgs {
    /// Where the identity secret lives: `os` (system keystore) or `private-file`.
    #[arg(long, env = "TOKENDANCE_CREDENTIAL_BACKEND", value_name = "BACKEND")]
    pub credential_backend: Option<String>,
    /// Sources to enable, comma separated (default: every source stays enabled).
    #[arg(long, value_delimiter = ',', value_name = "NAME")]
    pub sources: Vec<String>,
}

#[derive(Subcommand, Debug)]
pub enum SourcesCommand {
    Enable {
        name: String,
    },
    Disable {
        name: String,
    },
    /// Point a source at an explicit path (checked for readability).
    SetPath {
        name: String,
        path: PathBuf,
    },
}

#[derive(Args, Debug)]
pub struct CollectArgs {
    /// Collect what is available now, then exit.
    #[arg(long, required = true)]
    pub once: bool,
    /// Give up after this long and report what is left, e.g. 90s, 5m.
    #[arg(long, default_value = "5m", value_parser = parse_duration, value_name = "DURATION")]
    pub timeout: Duration,
}

#[derive(Args, Debug)]
pub struct RunArgs {
    /// Only log warnings and errors.
    #[arg(long)]
    pub quiet: bool,
}

pub fn parse_duration(value: &str) -> Result<Duration, String> {
    let value = value.trim();
    let (digits, unit) = value.split_at(
        value
            .find(|c: char| !c.is_ascii_digit())
            .unwrap_or(value.len()),
    );
    let amount: u64 = digits
        .parse()
        .map_err(|_| format!("`{value}` is not a duration such as 90s or 5m"))?;
    let seconds = match unit {
        "" | "s" => 1,
        "m" => 60,
        "h" => 3_600,
        _ => {
            return Err(format!(
                "unknown duration unit in `{value}` (use s, m or h)"
            ))
        }
    };
    if amount == 0 {
        return Err("duration must be greater than zero".into());
    }
    amount
        .checked_mul(seconds)
        .map(Duration::from_secs)
        .ok_or_else(|| "duration is too large".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durations() {
        assert_eq!(parse_duration("90s").unwrap(), Duration::from_secs(90));
        assert_eq!(parse_duration("5m").unwrap(), Duration::from_secs(300));
        assert_eq!(parse_duration("2h").unwrap(), Duration::from_secs(7200));
        assert_eq!(parse_duration("45").unwrap(), Duration::from_secs(45));
        assert!(parse_duration("0s").is_err());
        assert!(parse_duration("abc").is_err());
        assert!(parse_duration("5d").is_err());
        assert!(parse_duration("").is_err());
    }
}
