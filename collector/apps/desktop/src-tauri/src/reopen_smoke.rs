//! Native window restoration checks, available only in the isolated debug flow.
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;
use tauri::{AppHandle, Manager};
static STARTED: AtomicBool = AtomicBool::new(false);

async fn on_ui<T: Send + 'static>(
    app: &AppHandle,
    action: impl FnOnce(&AppHandle) -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    let (send, receive) = tokio::sync::oneshot::channel();
    let handle = app.clone();
    app.run_on_main_thread(move || {
        let result = action(&handle);
        let _ = send.send(result);
    })
    .map_err(|e| e.to_string())?;
    receive.await.map_err(|e| e.to_string())?
}

pub(crate) fn start(app: &AppHandle) {
    if !crate::startup_error_smoke()
        || !std::env::args().any(|arg| arg == "--smoke-reopen")
        || STARTED.swap(true, Ordering::AcqRel)
    {
        return;
    }
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        let result = async {
            tokio::time::sleep(Duration::from_millis(700)).await;
            for label in ["main", "settings"] {
                let original_window = on_ui(&app, move |app| {
                    if label == "settings" {
                        crate::commands::window::open_settings(app.clone())?;
                    }
                    let window = app
                        .get_webview_window(label)
                        .ok_or("missing smoke window")?;
                    let identity = window.ns_window().map_err(|e| e.to_string())? as usize;
                    window.minimize().map_err(|e| e.to_string())?;
                    Ok(identity)
                })
                .await?;
                // The old delayed blur callback would hide a miniaturized main window.
                tokio::time::sleep(Duration::from_millis(200)).await;
                on_ui(&app, move |app| {
                    let window = app
                        .get_webview_window(label)
                        .ok_or("missing smoke window")?;
                    if !window.is_minimized().map_err(|e| e.to_string())? {
                        return Err("window did not minimize".into());
                    }
                    crate::commands::window::activate_primary_window(app)
                })
                .await?;
                tokio::time::sleep(Duration::from_millis(400)).await;
                on_ui(&app, move |app| {
                    let window = app
                        .get_webview_window(label)
                        .ok_or("window replaced during reopen")?;
                    if window.is_minimized().map_err(|e| e.to_string())?
                        || !window.is_visible().map_err(|e| e.to_string())?
                    {
                        return Err(format!("{label} was not restored"));
                    }
                    // The optional orb can create its own windows during minimization.
                    // Verify the original primary NSWindow was restored, not recreated.
                    if window.ns_window().map_err(|e| e.to_string())? as usize != original_window {
                        return Err("reopen replaced the primary window".into());
                    }
                    Ok(())
                })
                .await?;
            }
            Ok::<(), String>(())
        }
        .await;
        match result {
            Ok(()) => println!("TOKENDANCE_REOPEN_PASSED"),
            Err(error) => {
                eprintln!("Reopen smoke failed: {error}");
                app.exit(3);
            }
        }
    });
}
