//! Explicit, retryable authorization; authorized secrets stay in this process.
use crate::state::AppState;
use std::sync::{
    atomic::{AtomicBool, AtomicUsize, Ordering},
    Arc,
};
use tauri::{AppHandle, Manager};
const LABEL: &str = "startup-error";

fn escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}
fn friendly_error(error: &str) -> &'static str {
    if error.contains("-128") {
        "已取消授权。准备好后，可以再次点击继续。"
    } else if error.contains("keystore") || error.contains("keychain") {
        "尚未取得密钥访问权限。请重试，并按系统窗口的提示允许访问。"
    } else {
        "暂时未能继续启动，请重试。详细原因可在下方查看。"
    }
}
#[derive(Default)]
struct AttemptGate(AtomicBool);
impl AttemptGate {
    fn begin(&self) -> bool {
        self.0
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .is_ok()
    }
    fn retry(&self) {
        self.0.store(false, Ordering::Release);
    }
}
fn update(app: &AppHandle, phase: &str, message: &str, detail: Option<&str>) {
    if let Some(window) = app.get_webview_window(LABEL) {
        let payload = serde_json::json!({"phase":phase,"message":message,"detail":detail});
        let _ = window.eval(format!("window.recoveryUpdate({payload})"));
    }
}
fn failed(app: &AppHandle, gate: &AttemptGate, error: String) {
    crate::write_crash_log(&format!("startup recovery failed: {error}"));
    gate.retry();
    update(app, "error", friendly_error(&error), Some(&error));
}
fn page(error: &str, nonce: &str, authorize: bool, auto_test: bool) -> String {
    include_str!("startup_recovery.html")
        .replace("__NONCE__", nonce)
        .replace(
            "__TITLE__",
            if authorize {
                "允许访问本机密钥"
            } else {
                "暂时无法继续启动"
            },
        )
        .replace(
            "__INTRO__",
            if authorize {
                "TokenDance 需要读取本机密钥，才能继续使用已有数据。"
            } else {
                "本机存储暂时不可用。处理完成后，点击下方按钮重试。"
            },
        )
        .replace(
            "__HINT__",
            if authorize {
                "点击后按系统提示允许访问；macOS 可能要求输入登录钥匙串密码。"
            } else {
                "无需关闭应用，重试结果会显示在这里。"
            },
        )
        .replace(
            "__BUTTON__",
            if authorize {
                "授权并继续"
            } else {
                "重试启动"
            },
        )
        .replace(
            "__ACTION__",
            &format!("tokendance-recovery://continue/{nonce}"),
        )
        .replace(
            "__TEST_DONE__",
            &format!("tokendance-recovery://test-done/{nonce}"),
        )
        .replace("__AUTO_TEST__", if auto_test { "true" } else { "false" })
        .replace("__DETAIL__", &escape(error))
}

#[cfg(debug_assertions)]
async fn smoke_state() -> Result<AppState, String> {
    if !crate::startup_error_smoke() {
        return Err("Smoke mode is disabled".into());
    }
    let root = std::env::var_os("TOKENDANCE_RECOVERY_SMOKE_DIR")
        .map(std::path::PathBuf::from)
        .ok_or("Missing smoke fixture directory")?
        .canonicalize()
        .map_err(|e| e.to_string())?;
    let temporary = std::env::temp_dir()
        .canonicalize()
        .map_err(|e| e.to_string())?;
    if root.parent() != Some(temporary.as_path())
        || !root
            .file_name()
            .and_then(|n| n.to_str())
            .is_some_and(|n| n.starts_with("tokendance-recovery-fixture-"))
    {
        return Err("Smoke startup only accepts its disposable fixture directory".into());
    }
    AppState::test(
        root,
        Arc::new(crate::autostart::SystemAutostartManager::new("TokenDance")),
    )
    .await
}

#[cfg(not(debug_assertions))]
async fn smoke_state() -> Result<AppState, String> {
    Err("Smoke mode is unavailable in release builds".into())
}

pub(crate) fn show(app: &AppHandle, error: String) -> Result<(), String> {
    use base64::Engine;
    #[cfg(target_os = "macos")]
    let _ = app.set_activation_policy(tauri::ActivationPolicy::Regular);
    let smoke = crate::startup_error_smoke();
    let auto_test = smoke && std::env::args().any(|arg| arg == "--smoke-recovery-flow");
    let authorize = smoke
        || (platform_credentials::uses_login_keychain()
            && (error.contains("keystore") || error.contains("keychain")));
    let nonce = uuid::Uuid::new_v4().simple().to_string();
    let action = format!("tokendance-recovery://continue/{nonce}");
    let test_done = format!("tokendance-recovery://test-done/{nonce}");
    let html = page(&error, &nonce, authorize, auto_test);
    let url = format!(
        "data:text/html;base64,{}",
        base64::engine::general_purpose::STANDARD.encode(html)
    );
    let initial = tauri::Url::parse(&url).map_err(|e| e.to_string())?;
    let allowed_url = initial.clone();
    let app_handle = app.clone();
    let gate = Arc::new(AttemptGate::default());
    let attempts = Arc::new(AtomicUsize::new(0));
    tauri::WebviewWindowBuilder::new(app, LABEL, tauri::WebviewUrl::External(initial))
        .on_navigation(move |url| {
            if *url == allowed_url {
                return true;
            }
            if auto_test && url.as_str() == test_done && attempts.load(Ordering::Acquire) == 2 {
                println!("TOKENDANCE_RECOVERY_FLOW_PASSED");
                if let Some(window) = app_handle.get_webview_window(LABEL) {
                    let _ = window.destroy();
                }
                return false;
            }
            if url.as_str() != action || !gate.begin() {
                return false;
            }
            let app = app_handle.clone();
            let gate = Arc::clone(&gate);
            let attempt = attempts.fetch_add(1, Ordering::AcqRel);
            tauri::async_runtime::spawn(async move {
                if smoke {
                    // The debug+local-test fixture never calls production authorization or storage.
                    tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                    if attempt == 0 {
                        failed(&app, &gate, "authorization canceled (-128)".into());
                        return;
                    }
                    match smoke_state().await {
                        Err(error) => failed(&app, &gate, error),
                        Ok(state) => {
                            let handle = app.clone();
                            let completion_gate = Arc::clone(&gate);
                            if let Err(error) = app.run_on_main_thread(move || {
                                match crate::complete_startup(&handle, state, true) {
                                    Ok(()) => update(
                                        &handle,
                                        "done",
                                        "授权流程测试完成；进程保持运行。",
                                        None,
                                    ),
                                    Err(error) => failed(&handle, &completion_gate, error),
                                }
                            }) {
                                failed(&app, &gate, error.to_string());
                            }
                        }
                    }
                    return;
                }
                if authorize {
                    if let Err(error) =
                        crate::commands::account::authorize_saved_credentials(None).await
                    {
                        failed(&app, &gate, error);
                        return;
                    }
                }
                update(&app, "busy", "正在继续启动…", None);
                let existing = app
                    .try_state::<AppState>()
                    .map(|state| state.inner().clone());
                let result = match existing {
                    Some(state) => Ok(state),
                    None => AppState::production().await,
                };
                match result {
                    Err(error) => failed(&app, &gate, error),
                    Ok(state) => {
                        if app.get_webview_window(LABEL).is_none() {
                            return;
                        }
                        let handle = app.clone();
                        let completion_gate = Arc::clone(&gate);
                        if let Err(error) = app.run_on_main_thread(move || {
                            match crate::complete_startup(&handle, state, true) {
                                Ok(()) => {
                                    if let Some(window) = handle.get_webview_window(LABEL) {
                                        let _ = window.destroy();
                                    }
                                }
                                Err(error) => failed(&handle, &completion_gate, error),
                            }
                        }) {
                            failed(&app, &gate, error.to_string());
                        }
                    }
                }
            });
            false
        })
        .title("TokenDance · 继续启动")
        .inner_size(540.0, 480.0)
        .resizable(true)
        .center()
        .build()
        .map_err(|e| e.to_string())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn duplicate_actions_are_blocked_and_failure_allows_retry() {
        let gate = AttemptGate::default();
        assert!(gate.begin());
        assert!(!gate.begin());
        gate.retry();
        assert!(gate.begin());
        assert!(!gate.begin());
    }
    #[test]
    fn recovery_page_escapes_errors_and_scopes_the_action_to_its_nonce() {
        let html = page(
            "</pre><script>bad()</script>\"",
            "fixture_nonce",
            true,
            false,
        );
        assert!(!html.contains("<script>bad()"));
        assert!(html.contains("&lt;script&gt;bad()"));
        assert!(html.contains("nonce-fixture_nonce"));
        assert!(html.contains("tokendance-recovery://continue/fixture_nonce"));
        assert!(html.contains("autoTest=false"));
        assert!(html.contains("授权并继续"));
        assert!(!html.contains("始终允许"));
    }
    #[test]
    fn storage_errors_do_not_offer_keychain_authorization() {
        let html = page("disk full", "nonce", false, false);
        assert!(html.contains("重试启动"));
        assert!(!html.contains("授权并继续"));
        assert!(friendly_error("os keystore denied (-128)").contains("已取消"));
        assert!(!friendly_error("secret value (-25293)").contains("secret value"));
    }
}
