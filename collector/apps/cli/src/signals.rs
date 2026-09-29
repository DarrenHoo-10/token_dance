//! SIGINT / SIGTERM as a flag that long loops poll between units of work.

use std::sync::atomic::{AtomicU8, Ordering};
use std::sync::Arc;

pub const NONE: u8 = 0;
pub const INTERRUPT: u8 = 1;
pub const TERMINATE: u8 = 2;

#[derive(Clone)]
pub struct Signals(Arc<AtomicU8>);

impl Signals {
    pub fn install() -> Self {
        let flag = Arc::new(AtomicU8::new(NONE));
        let writer = Arc::clone(&flag);
        std::thread::Builder::new()
            .name("signals".into())
            .spawn(move || {
                let Ok(runtime) = tokio::runtime::Builder::new_current_thread()
                    .enable_all()
                    .build()
                else {
                    return;
                };
                runtime.block_on(async move {
                    #[cfg(unix)]
                    {
                        use tokio::signal::unix::{signal, SignalKind};
                        let Ok(mut term) = signal(SignalKind::terminate()) else {
                            return;
                        };
                        tokio::select! {
                            _ = tokio::signal::ctrl_c() => writer.store(INTERRUPT, Ordering::Release),
                            _ = term.recv() => writer.store(TERMINATE, Ordering::Release),
                        }
                    }
                    #[cfg(not(unix))]
                    {
                        let _ = tokio::signal::ctrl_c().await;
                        writer.store(INTERRUPT, Ordering::Release);
                    }
                });
            })
            .ok();
        Self(flag)
    }

    pub fn received(&self) -> u8 {
        self.0.load(Ordering::Acquire)
    }

    pub fn is_set(&self) -> bool {
        self.received() != NONE
    }
}
