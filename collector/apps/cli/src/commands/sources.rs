use std::path::PathBuf;

use serde_json::json;

use crate::cli::SourcesCommand;
use crate::ctx::Ctx;
use crate::error::{CliError, CliResult};
use crate::{sources, Outcome};

pub fn run(ctx: &Ctx, action: SourcesCommand) -> CliResult<Outcome> {
    let (name, change) = match &action {
        SourcesCommand::Enable { name } => (name, Change::Enabled(true)),
        SourcesCommand::Disable { name } => (name, Change::Enabled(false)),
        SourcesCommand::SetPath { name, path } => (name, Change::Path(absolute(path)?)),
    };
    if sources::find(name).is_none() {
        return Err(CliError::invalid(format!(
            "unknown source `{name}`; available: {}",
            sources::names()
        )));
    }
    if let Change::Path(path) = &change {
        let (exists, readable) = sources::probe(path);
        if !exists {
            return Err(CliError::invalid(format!(
                "{} does not exist",
                path.display()
            )));
        }
        if !readable {
            return Err(CliError::invalid(format!(
                "{} is not readable",
                path.display()
            )));
        }
    }
    ctx.paths.ensure().map_err(CliError::internal)?;
    let _lock = ctx.lock()?;
    let mut config = ctx.config()?;
    let entry = config.sources.entry(name.clone()).or_default();
    let summary = match change {
        Change::Enabled(enabled) => {
            entry.enabled = Some(enabled);
            format!("{name}: {}", if enabled { "enabled" } else { "disabled" })
        }
        Change::Path(path) => {
            let text = format!("{name}: path set to {}", path.display());
            entry.path = Some(path);
            text
        }
    };
    config.save(&ctx.config_path)?;
    let report: Vec<_> = sources::report(&config)
        .into_iter()
        .filter(|r| r.name == name)
        .collect();
    Ok(Outcome {
        command: "sources",
        data: json!({ "source": report.first() }),
        human: format!("{summary}\nApplies the next time `collect` or `run` starts.\n"),
    })
}

enum Change {
    Enabled(bool),
    Path(PathBuf),
}

fn absolute(path: &std::path::Path) -> CliResult<PathBuf> {
    if path.is_absolute() {
        Ok(path.to_path_buf())
    } else {
        std::env::current_dir()
            .map(|cwd| cwd.join(path))
            .map_err(|e| CliError::internal(e.to_string()))
    }
}
