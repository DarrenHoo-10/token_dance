//! Claim the Windows application identity before updates, storage or WebViews start.
//! The event retains activation requests even while the first process is loading.

#[cfg(windows)]
mod platform {
    use std::os::windows::io::{AsRawHandle, FromRawHandle, OwnedHandle};
    use std::sync::Mutex;
    use windows::core::PCWSTR;
    use windows::Win32::Foundation::{GetLastError, ERROR_ALREADY_EXISTS, HANDLE, WAIT_OBJECT_0};
    use windows::Win32::System::Threading::{
        CreateEventW, CreateMutexW, SetEvent, WaitForSingleObject,
    };

    pub struct InstanceGuard {
        // No thread owns the mutex: its kernel-object lifetime is the claim.
        claim: Mutex<Option<OwnedHandle>>,
        activation: OwnedHandle,
    }

    fn wide(value: &str) -> Vec<u16> {
        value.encode_utf16().chain(Some(0)).collect()
    }

    impl InstanceGuard {
        pub fn acquire(activate: bool) -> Result<Option<Self>, String> {
            Self::acquire_named("Local\\io.tokendance.desktop", activate)
        }

        fn acquire_named(name: &str, activate: bool) -> Result<Option<Self>, String> {
            let event_name = wide(&format!("{name}.activate"));
            let mutex_name = wide(&format!("{name}.instance"));
            // Create/open the event first, so a concurrent launch can never
            // signal before the primary has an activation endpoint.
            let event = unsafe { CreateEventW(None, false, false, PCWSTR(event_name.as_ptr())) }
                .map_err(|error| error.to_string())?;
            let activation = unsafe { OwnedHandle::from_raw_handle(event.0) };
            let mutex = unsafe { CreateMutexW(None, false, PCWSTR(mutex_name.as_ptr())) }
                .map_err(|error| error.to_string())?;
            let exists = unsafe { GetLastError() } == ERROR_ALREADY_EXISTS;
            let claim = unsafe { OwnedHandle::from_raw_handle(mutex.0) };
            if exists {
                if activate {
                    unsafe { SetEvent(event) }.map_err(|error| error.to_string())?;
                }
                return Ok(None);
            }
            Ok(Some(Self {
                claim: Mutex::new(Some(claim)),
                activation,
            }))
        }

        pub fn release(&self) {
            self.claim.lock().expect("instance claim lock").take();
        }

        pub fn take_activation(&self) -> bool {
            let claim = self.claim.lock().expect("instance claim lock");
            if claim.is_none() {
                return false;
            }
            unsafe {
                WaitForSingleObject(HANDLE(self.activation.as_raw_handle()), 0) == WAIT_OBJECT_0
            }
        }
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn concurrent_processes_share_one_instance_and_queue_activation() {
            let name = format!("Local\\TokenDance-test-{}", uuid::Uuid::new_v4());
            let primary = InstanceGuard::acquire_named(&name, false).unwrap().unwrap();
            let mut children = Vec::new();
            for _ in 0..8 {
                children.push(
                    std::process::Command::new(std::env::current_exe().unwrap())
                        .args([
                            "--exact",
                            "single_instance::platform::tests::secondary_process",
                            "--ignored",
                        ])
                        .env("TOKENDANCE_INSTANCE_TEST_NAME", &name)
                        .spawn()
                        .unwrap(),
                );
            }
            for mut child in children {
                assert!(child.wait().unwrap().success());
            }
            // No window or event consumer existed when the requests arrived.
            assert!(primary.take_activation());
            assert!(!primary.take_activation());
            assert!(InstanceGuard::acquire_named(&name, false)
                .unwrap()
                .is_none());
            assert!(!primary.take_activation(), "autostart must remain quiet");
            primary.release();
            let replacement = InstanceGuard::acquire_named(&name, false).unwrap().unwrap();
            assert!(InstanceGuard::acquire_named(&name, true).unwrap().is_none());
            assert!(
                !primary.take_activation(),
                "old process must not consume replacement requests"
            );
            assert!(replacement.take_activation());
        }

        #[test]
        #[ignore = "spawned by the isolated single-instance process test"]
        fn secondary_process() {
            let Ok(name) = std::env::var("TOKENDANCE_INSTANCE_TEST_NAME") else {
                return;
            };
            assert!(InstanceGuard::acquire_named(&name, true).unwrap().is_none());
        }

        #[test]
        fn process_exit_releases_claim_without_running_destructors() {
            let name = format!("Local\\TokenDance-test-{}", uuid::Uuid::new_v4());
            let status = std::process::Command::new(std::env::current_exe().unwrap())
                .args([
                    "--exact",
                    "single_instance::platform::tests::exiting_primary_process",
                    "--ignored",
                ])
                .env("TOKENDANCE_INSTANCE_TEST_NAME", &name)
                .status()
                .unwrap();
            assert!(status.success());
            assert!(InstanceGuard::acquire_named(&name, false)
                .unwrap()
                .is_some());
        }

        #[test]
        #[ignore = "spawned by the isolated process-exit test"]
        fn exiting_primary_process() {
            let Ok(name) = std::env::var("TOKENDANCE_INSTANCE_TEST_NAME") else {
                return;
            };
            let _primary = InstanceGuard::acquire_named(&name, false).unwrap().unwrap();
            std::process::exit(0);
        }

        #[test]
        fn dropping_primary_allows_relaunch() {
            let name = format!("Local\\TokenDance-test-{}", uuid::Uuid::new_v4());
            let primary = InstanceGuard::acquire_named(&name, false).unwrap().unwrap();
            drop(primary);
            assert!(InstanceGuard::acquire_named(&name, false)
                .unwrap()
                .is_some());
        }
    }
}

#[cfg(not(windows))]
mod platform {
    pub struct InstanceGuard;
    impl InstanceGuard {
        pub fn acquire(_activate: bool) -> Result<Option<Self>, String> {
            Ok(Some(Self))
        }
        pub fn release(&self) {}
        pub fn take_activation(&self) -> bool {
            false
        }
    }
}

pub use platform::InstanceGuard;

#[cfg(windows)]
pub fn listen(app: &tauri::AppHandle) {
    use tauri::Manager;
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        loop {
            if app.state::<InstanceGuard>().take_activation() {
                let handle = app.clone();
                let _ = app.run_on_main_thread(move || {
                    if let Err(error) = crate::commands::window::activate_primary_window(&handle) {
                        eprintln!("failed to activate existing TokenDance window: {error}");
                    }
                });
            }
            tokio::time::sleep(std::time::Duration::from_millis(200)).await;
        }
    });
}

#[cfg(not(windows))]
pub fn listen(_app: &tauri::AppHandle) {}
