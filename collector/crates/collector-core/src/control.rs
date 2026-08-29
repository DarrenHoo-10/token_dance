use std::sync::{Arc, RwLock};

/// Cloneable cancellation state shared with acquisition tasks.
#[derive(Debug, Clone)]
pub struct CollectionControl {
    enabled: Arc<RwLock<bool>>,
}

impl Default for CollectionControl {
    fn default() -> Self {
        Self {
            enabled: Arc::new(RwLock::new(true)),
        }
    }
}

impl CollectionControl {
    pub fn is_enabled(&self) -> bool {
        *self.enabled.read().unwrap_or_else(|err| err.into_inner())
    }

    pub fn disable(&self) {
        *self.enabled.write().unwrap_or_else(|err| err.into_inner()) = false;
    }

    pub fn enable(&self) {
        *self.enabled.write().unwrap_or_else(|err| err.into_inner()) = true;
    }

    /// Hold a shared commit lease for the complete action. Disable obtains the
    /// exclusive side, making the enabled check and durable commit linearizable.
    pub fn with_commit_lease<T>(&self, action: impl FnOnce() -> T) -> Option<T> {
        let enabled = self.enabled.read().unwrap_or_else(|err| err.into_inner());
        if *enabled {
            Some(action())
        } else {
            None
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Barrier};
    use std::thread;
    use std::time::Duration;

    use super::*;

    #[test]
    fn disable_waits_for_an_inflight_commit_lease() {
        let control = CollectionControl::default();
        let entered = Arc::new(Barrier::new(2));
        let release = Arc::new(Barrier::new(2));
        let worker_control = control.clone();
        let worker_entered = Arc::clone(&entered);
        let worker_release = Arc::clone(&release);
        let worker = thread::spawn(move || {
            worker_control.with_commit_lease(|| {
                worker_entered.wait();
                worker_release.wait();
            })
        });
        entered.wait();

        let disable_control = control.clone();
        let disable = thread::spawn(move || disable_control.disable());
        thread::sleep(Duration::from_millis(10));
        assert!(
            !disable.is_finished(),
            "disable crossed an active commit lease"
        );
        release.wait();
        worker.join().unwrap().unwrap();
        disable.join().unwrap();
        assert!(!control.is_enabled());
    }
}
