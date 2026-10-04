//! `tokendance`: command-line collector. See docs/process/cli-tool/README.md.

mod cli;
mod commands;
mod config;
mod ctx;
mod engine;
mod error;
mod keys;
mod localdb;
mod server;
mod signals;
mod snapshot;
mod sources;
mod status;

use std::process::ExitCode;

use clap::Parser;
use serde_json::{json, Value};

use crate::cli::{Cli, Command};
use crate::ctx::{Ctx, Overrides};
use crate::error::{CliError, CliResult};

pub const SCHEMA_VERSION: u32 = 1;

/// What a command produced: structured data for `--json`, prose for people.
pub struct Outcome {
    pub command: &'static str,
    pub data: Value,
    pub human: String,
}

fn main() -> ExitCode {
    let wants_json = std::env::args_os().any(|a| a == "--json");
    let cli = match Cli::try_parse() {
        Ok(cli) => cli,
        Err(e) => {
            use clap::error::ErrorKind;
            if matches!(e.kind(), ErrorKind::DisplayHelp | ErrorKind::DisplayVersion) {
                let _ = e.print();
                return ExitCode::SUCCESS;
            }
            let plain = e.render().to_string();
            let message = plain
                .lines()
                .next()
                .unwrap_or("invalid arguments")
                .trim_start_matches("error: ")
                .to_string();
            return fail(
                wants_json,
                CliError::invalid(message).hint("run `tokendance --help`"),
                Some(plain),
            );
        }
    };
    let json = cli.json;
    match run(cli) {
        Ok(out) => {
            if json {
                let doc = json!({
                    "schema_version": SCHEMA_VERSION,
                    "ok": true,
                    "command": out.command,
                    "data": out.data,
                });
                println!("{}", serde_json::to_string_pretty(&doc).expect("json"));
            } else if !out.human.is_empty() {
                println!("{}", out.human.trim_end());
            }
            ExitCode::SUCCESS
        }
        Err(error) => fail(json, error, None),
    }
}

fn fail(json: bool, error: CliError, clap_text: Option<String>) -> ExitCode {
    if json {
        println!(
            "{}",
            serde_json::to_string_pretty(&error.to_json()).expect("json")
        );
    } else if let Some(text) = clap_text {
        eprint!("{text}");
    } else if error.kind == crate::error::Kind::Interrupted && error.message.is_empty() {
        // A deliberate Ctrl+C on a foreground server needs no error text.
    } else {
        eprintln!("error: {}", error.message);
        if let Some(hint) = &error.hint {
            eprintln!("hint: {hint}");
        }
        if let Some(data) = &error.data {
            eprintln!("{}", serde_json::to_string_pretty(data).unwrap_or_default());
        }
    }
    ExitCode::from(error.kind.exit_code())
}

fn run(cli: Cli) -> CliResult<Outcome> {
    let ctx = Ctx::resolve(Overrides {
        json: cli.json,
        data_dir: cli.data_dir,
        config: cli.config,
    })?;
    match cli.command {
        Command::Init(args) => commands::init::run(&ctx, args),
        Command::Sources { action } => commands::sources::run(&ctx, action),
        Command::Collect(args) => commands::collect::run(&ctx, args),
        Command::Run(args) => commands::run::run(&ctx, args),
        Command::Doctor => commands::doctor::run(&ctx),
        Command::Dashboard(args) => commands::dashboard::run(&ctx, args),
    }
}
