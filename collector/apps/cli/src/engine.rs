//! Opens the collection pipeline the same way the desktop app does, minus the UI.

use std::path::PathBuf;
use std::sync::Arc;

use collector_service::local_store::pipeline::runtime::adapter_roots_from_detection;
use collector_service::local_store::{PipelineRuntime, PipelineStore, PipelineWriter};
use collector_service::{detect_local, InstanceLock};

use crate::config::Config;
use crate::ctx::Ctx;
use crate::error::{CliError, CliResult, Kind};
use crate::{keys, localdb, sources};

pub struct Engine {
    pub runtime: Arc<PipelineRuntime>,
    pub log_file: PathBuf,
    /// Held for the life of the engine; released on drop.
    _lock: InstanceLock,
}

impl Engine {
    pub fn open(ctx: &Ctx, config: &Config) -> CliResult<Self> {
        ctx.paths.ensure().map_err(CliError::internal)?;
        let lock = ctx.lock()?;
        localdb::ensure_writable_schema(&ctx.paths)?;
        let backend = ctx.backend(config)?;
        let secret = keys::load(backend, &ctx.paths, false)?;
        collector_service::runtime::load_or_create_installation_id(&ctx.paths.collector)
            .map_err(CliError::internal)?;
        // Unlike the desktop app the CLI never schedules a historical rebuild here.
        let store = PipelineStore::open(&ctx.paths.collector)
            .map_err(|e| CliError::internal(format!("cannot open the event database: {e}")))?;
        match store.workers_allowed() {
            Ok(true) => {}
            Ok(false) => {
                return Err(CliError::new(
                    Kind::Incomplete,
                    "the event pipeline is paused (TOKENDANCE_EVENT_PIPELINE_V2_CLIENT is off or initialization is incomplete)",
                ))
            }
            Err(e) => return Err(CliError::internal(format!("event pipeline gate: {e}"))),
        }
        let writer = Arc::new(PipelineWriter::start(store));
        let detection = detect_local();
        let mut roots = adapter_roots_from_detection(secret, &detection);
        for source in sources::ALL.iter() {
            if let Some(path) = config.source_path(source.name) {
                sources::override_root(&mut roots, source.name, path.to_path_buf());
            }
        }
        let runtime = Arc::new(PipelineRuntime::from_roots(writer, roots));
        for source in sources::ALL.iter() {
            runtime.set_harness_enabled(source.name, config.source_enabled(source.name));
        }
        Ok(Self {
            runtime,
            log_file: ctx.paths.log_file(),
            _lock: lock,
        })
    }

    pub fn log(&self, message: &str) {
        collector_service::platform::append_rotated_log(&self.log_file, message);
    }
}
