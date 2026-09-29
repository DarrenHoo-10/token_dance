use std::fs;

use serde::Serialize;
use serde_json::json;

use crate::ctx::Ctx;
use crate::error::{CliError, CliResult, Kind};
use crate::keys::{self, KeyState};
use crate::{localdb, sources, Outcome};

#[derive(Serialize)]
struct Check {
    id: &'static str,
    /// ok | warn | fail | unknown
    status: &'static str,
    detail: String,
    #[serde(skip)]
    exit: Option<Kind>,
}

impl Check {
    fn ok(id: &'static str, detail: impl Into<String>) -> Self {
        Self {
            id,
            status: "ok",
            detail: detail.into(),
            exit: None,
        }
    }
    fn warn(id: &'static str, detail: impl Into<String>) -> Self {
        Self {
            id,
            status: "warn",
            detail: detail.into(),
            exit: None,
        }
    }
    fn fail(id: &'static str, detail: impl Into<String>, exit: Kind) -> Self {
        Self {
            id,
            status: "fail",
            detail: detail.into(),
            exit: Some(exit),
        }
    }
    fn unknown(id: &'static str, detail: impl Into<String>) -> Self {
        Self {
            id,
            status: "unknown",
            detail: detail.into(),
            exit: None,
        }
    }
}

/// Diagnostics never create, migrate, lock-for-write or repair anything.
pub fn run(ctx: &Ctx) -> CliResult<Outcome> {
    let mut checks = Vec::new();

    checks.push(data_dir(ctx));
    let config = match ctx.config() {
        Ok(config) => {
            checks.push(Check::ok(
                "config",
                format!("{} parsed", ctx.config_path.display()),
            ));
            Some(config)
        }
        Err(e) => {
            checks.push(Check::fail("config", e.message.clone(), e.kind));
            None
        }
    };
    if let Some(config) = &config {
        checks.push(credentials(ctx, config));
    }
    checks.push(database(ctx));
    checks.push(lock(ctx));
    if let Some(config) = &config {
        for report in sources::report(config) {
            checks.push(source_check(&report));
        }
    }

    let failed = checks.iter().find(|c| c.status == "fail");
    let mut human = String::new();
    for c in &checks {
        human.push_str(&format!("[{:<7}] {:<22} {}\n", c.status, c.id, c.detail));
    }
    let data = json!({ "healthy": failed.is_none(), "checks": checks });
    match failed {
        None => Ok(Outcome {
            command: "doctor",
            data,
            human,
        }),
        Some(check) => Err(CliError::new(
            check.exit.unwrap_or(Kind::Incomplete),
            format!("diagnostics found problems:\n{}", human.trim_end()),
        )
        .with_data(data)),
    }
}

fn data_dir(ctx: &Ctx) -> Check {
    let dir = &ctx.paths.collector;
    let meta = match fs::symlink_metadata(dir) {
        Ok(m) => m,
        Err(_) => {
            return Check::warn(
                "data_dir",
                format!(
                    "{} does not exist yet (run `tokendance init`)",
                    dir.display()
                ),
            )
        }
    };
    if !meta.is_dir() {
        return Check::fail(
            "data_dir",
            format!("{} is not a directory", dir.display()),
            Kind::Incompatible,
        );
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if meta.permissions().mode() & 0o077 != 0 {
            return Check::warn(
                "data_dir",
                format!(
                    "{} is accessible by group or others (expected 0700)",
                    dir.display()
                ),
            );
        }
    }
    Check::ok("data_dir", dir.display().to_string())
}

fn credentials(ctx: &Ctx, config: &crate::config::Config) -> Check {
    let chosen = std::env::var_os("TOKENDANCE_CREDENTIAL_BACKEND").is_some()
        || config.credential_backend.is_some();
    if !chosen && crate::keys::Backend::platform_default().is_none() {
        return Check::warn(
            "credentials",
            "no credential backend chosen yet (run `tokendance init --credential-backend private-file`)",
        );
    }
    let backend = match ctx.backend(config) {
        Ok(b) => b,
        Err(e) => {
            return Check::fail(
                "credentials",
                format!("{} ({})", e.message, e.hint.unwrap_or_default()),
                Kind::Credentials,
            )
        }
    };
    match keys::inspect(backend, &ctx.paths) {
        KeyState::Present => Check::ok(
            "credentials",
            format!("{} backend, identity secret readable", backend.as_str()),
        ),
        KeyState::Missing => Check::warn(
            "credentials",
            format!(
                "{} backend, no identity secret yet (run `tokendance init`)",
                backend.as_str()
            ),
        ),
        KeyState::Problem(m) => Check::fail(
            "credentials",
            format!("{} backend: {m}", backend.as_str()),
            Kind::Credentials,
        ),
    }
}

fn database(ctx: &Ctx) -> Check {
    let conn = match localdb::open_read_only(&ctx.paths) {
        Ok(Some(conn)) => conn,
        Ok(None) => {
            return Check::warn(
                "database",
                "not created yet; the first `collect` or `run` creates it",
            )
        }
        Err(e) => return Check::fail("database", e.message, Kind::Incomplete),
    };
    let supported = collector_service::local_store::pipeline::PIPELINE_SCHEMA_VERSION;
    match localdb::schema_version(&conn) {
        Some(v) if v > supported => Check::fail(
            "database",
            format!(
                "schema {v} is newer than this build supports ({supported}); upgrade tokendance"
            ),
            Kind::Incompatible,
        ),
        Some(v) => match localdb::stats(&conn) {
            Ok(s) => Check::ok(
                "database",
                format!(
                    "schema {v}, {} event(s), {} statistic task(s) pending",
                    s.events, s.metric_tasks_pending
                ),
            ),
            Err(e) => Check::fail("database", e.message, Kind::Incomplete),
        },
        None => Check::unknown("database", "schema version unavailable"),
    }
}

fn lock(ctx: &Ctx) -> Check {
    let process = crate::status::probe(&ctx.paths);
    match process.state {
        "never_started" => Check::ok("collector_process", "no collector has run here yet"),
        "stopped" => Check::ok("collector_process", "not running"),
        "running" => Check::ok(
            "collector_process",
            format!(
                "running (pid {})",
                process.pid.map(|p| p.to_string()).unwrap_or_default()
            ),
        ),
        _ => Check::unknown("collector_process", "cannot probe the instance lock"),
    }
}

fn source_check(r: &sources::SourceReport) -> Check {
    let id: &'static str = Box::leak(format!("source:{}", r.name).into_boxed_str());
    let path = r.path.clone().unwrap_or_else(|| "(none)".into());
    if !r.enabled {
        return Check::ok(id, format!("disabled; {path}"));
    }
    match (r.exists, r.readable) {
        (true, true) => Check::ok(id, format!("{} {path}", r.path_origin)),
        (true, false) => Check::warn(id, format!("{path} exists but is not readable")),
        (false, _) if r.path_origin == "configured" => {
            Check::warn(id, format!("configured path {path} does not exist"))
        }
        (false, _) if r.detected => Check::warn(id, format!("detected but {path} was not found")),
        (false, _) => Check::ok(id, format!("not installed here ({path} not found)")),
    }
}
