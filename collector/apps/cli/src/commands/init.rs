use serde_json::json;

use crate::cli::InitArgs;
use crate::config::SourceConfig;
use crate::ctx::Ctx;
use crate::error::{CliError, CliResult};
use crate::keys::{self, Backend};
use crate::{sources, Outcome};

pub fn run(ctx: &Ctx, args: InitArgs) -> CliResult<Outcome> {
    for name in &args.sources {
        if sources::find(name).is_none() {
            return Err(CliError::invalid(format!(
                "unknown source `{name}`; available: {}",
                sources::names()
            )));
        }
    }
    ctx.paths.ensure().map_err(CliError::internal)?;
    let _lock = ctx.lock()?;
    let mut config = ctx.config()?;

    let backend = match &args.credential_backend {
        Some(value) => Backend::parse(value)?,
        None => match config.credential_backend.as_deref() {
            Some(value) => Backend::parse(value)?,
            None => Backend::platform_default().ok_or_else(|| {
                CliError::invalid("this host has no system keystore backend; choose one explicitly")
                    .hint("run `tokendance init --credential-backend private-file` (a 0600 key file in the data directory)")
            })?,
        },
    };
    crate::localdb::ensure_writable_schema(&ctx.paths)?;
    keys::load(backend, &ctx.paths, true)?;
    collector_service::runtime::load_or_create_installation_id(&ctx.paths.collector)
        .map_err(CliError::internal)?;

    config.credential_backend = Some(backend.as_str().into());
    if !args.sources.is_empty() {
        for source in sources::ALL.iter() {
            let entry = config
                .sources
                .entry(source.name.to_string())
                .or_insert_with(SourceConfig::default);
            entry.enabled = Some(args.sources.iter().any(|n| n == source.name));
        }
    }
    config.save(&ctx.config_path)?;

    let reports = sources::report(&config);
    let mut human = format!(
        "Initialized TokenDance CLI\n  data:        {}\n  config:      {}\n  credentials: {}\n\nSources:\n",
        ctx.paths.collector.display(),
        ctx.config_path.display(),
        backend.as_str(),
    );
    for r in &reports {
        let state = match (r.enabled, r.detected || r.exists) {
            (false, _) => "disabled",
            (true, true) => "enabled, found",
            (true, false) => "enabled, not found",
        };
        human.push_str(&format!("  {:<18} {state}\n", r.name));
    }
    human.push_str("\nNext: `tokendance collect --once` to collect now, or `tokendance run` to keep collecting.\nLogin, sync and the dashboard are not part of this build yet.\n");
    Ok(Outcome {
        command: "init",
        data: json!({
            "data_dir": ctx.paths.collector,
            "config": ctx.config_path,
            "credential_backend": backend.as_str(),
            "sources": reports,
        }),
        human,
    })
}
