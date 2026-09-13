//! Native NSStatusItem click target.
//!
//! Tauri's tray-icon crate overlays a custom `NSView` on the status button.
//! macOS 15 menu-bar extra groups often swallow that view's `mouseDown`, so
//! Tauri click events never arrive. The system button action and event
//! monitors still see the click.

use std::io::Write;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
use std::sync::OnceLock;
use std::time::{SystemTime, UNIX_EPOCH};

use block2::RcBlock;
use objc2::rc::Retained;
use objc2::runtime::AnyObject;
use objc2::{define_class, msg_send, sel, AnyThread, MainThreadMarker, MainThreadOnly};
use objc2_app_kit::{
    NSApplication, NSApplicationActivationPolicy, NSEvent, NSEventMask, NSImage, NSMenu,
    NSMenuItem, NSScreen, NSStatusBar, NSStatusBarButton, NSStatusItem,
    NSVariableStatusItemLength, NSWindow, NSWindowCollectionBehavior, NSWindowStyleMask,
    NSWindowTitleVisibility,
};
use objc2_foundation::{NSData, NSObject, NSObjectProtocol, NSSize, NSString};
use tauri::{AppHandle, Manager, PhysicalPosition, PhysicalSize};

use crate::commands::window::{open_settings, show_usage_panel};
use crate::state::AppState;

static APP: OnceLock<AppHandle> = OnceLock::new();
static ITEM_PTR: AtomicUsize = AtomicUsize::new(0);
static LAST_CLICK_MS: AtomicU64 = AtomicU64::new(0);

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[name = "TokenDanceStatusTarget"]
    #[ivars = ()]
    struct StatusTarget;

    impl StatusTarget {
        #[unsafe(method(onClick:))]
        fn on_click(&self, sender: Option<&AnyObject>) {
            log_tray("button-action");
            open_panel(icon_from_sender(sender));
        }

        #[unsafe(method(onOpenSettings:))]
        fn on_open_settings(&self, _sender: Option<&AnyObject>) {
            let Some(app) = APP.get() else {
                return;
            };
            let _ = open_settings(app.clone());
        }

        #[unsafe(method(onTogglePause:))]
        fn on_toggle_pause(&self, _sender: Option<&AnyObject>) {
            let Some(app) = APP.get() else {
                return;
            };
            let state = app.state::<AppState>().inner().clone();
            tauri::async_runtime::spawn(async move {
                let _ = state.toggle_global_pause().await;
            });
        }

        #[unsafe(method(onQuit:))]
        fn on_quit(&self, _sender: Option<&AnyObject>) {
            let Some(app) = APP.get() else {
                return;
            };
            let state = app.state::<AppState>().inner().clone();
            let handle = app.clone();
            tauri::async_runtime::spawn(async move {
                let _ = state.shutdown().await;
                handle.exit(0);
            });
        }
    }
);

unsafe impl NSObjectProtocol for StatusTarget {}

fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

fn log_tray(message: &str) {
    let path = collector_service::AppPaths::production()
        .map(|paths| paths.logs.join("tray.log"))
        .unwrap_or_else(|_| std::path::PathBuf::from("/tmp/tokendance-tray.log"));
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    if let Ok(mut file) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(path)
    {
        let _ = write!(file, "{} {}\n", chrono::Utc::now().to_rfc3339(), message);
    }
}

fn leaked_item<'a>() -> Option<&'a NSStatusItem> {
    let ptr = ITEM_PTR.load(Ordering::SeqCst) as *const NSStatusItem;
    if ptr.is_null() {
        None
    } else {
        Some(unsafe { &*ptr })
    }
}

fn icon_from_sender(
    sender: Option<&AnyObject>,
) -> Option<(PhysicalPosition<f64>, PhysicalSize<f64>)> {
    let sender = sender?;
    let button = sender.downcast_ref::<NSStatusBarButton>()?;
    icon_from_button(button)
}

fn icon_from_button(
    button: &NSStatusBarButton,
) -> Option<(PhysicalPosition<f64>, PhysicalSize<f64>)> {
    let window = button.window()?;
    icon_from_window(&window)
}

fn icon_from_status_item() -> Option<(PhysicalPosition<f64>, PhysicalSize<f64>)> {
    let mtm = MainThreadMarker::new()?;
    let button = leaked_item()?.button(mtm)?;
    icon_from_button(&button)
}

fn icon_from_window(window: &NSWindow) -> Option<(PhysicalPosition<f64>, PhysicalSize<f64>)> {
    let frame = window.frame();
    let scale = window.backingScaleFactor();
    let screen_h = window
        .screen()
        .map(|screen| screen.frame().size.height)
        .or_else(|| {
            MainThreadMarker::new()
                .and_then(NSScreen::mainScreen)
                .map(|screen| screen.frame().size.height)
        })?;
    let x = frame.origin.x * scale;
    let y = (screen_h - frame.origin.y - frame.size.height) * scale;
    let width = frame.size.width * scale;
    let height = frame.size.height * scale;
    Some((
        PhysicalPosition::new(x, y),
        PhysicalSize::new(width, height),
    ))
}

fn pointer_hits_status_item() -> bool {
    let Some(mtm) = MainThreadMarker::new() else {
        return false;
    };
    let Some(item) = leaked_item() else {
        return false;
    };
    let Some(button) = item.button(mtm) else {
        return false;
    };
    let Some(window) = button.window() else {
        return false;
    };
    let frame = window.frame();
    let mouse = NSEvent::mouseLocation();
    mouse.x >= frame.origin.x
        && mouse.x <= frame.origin.x + frame.size.width
        && mouse.y >= frame.origin.y
        && mouse.y <= frame.origin.y + frame.size.height
}

fn click_point() -> PhysicalPosition<f64> {
    let mouse = NSEvent::mouseLocation();
    // Tao's macOS monitor_from_point calls CGDisplayBounds, whose coordinates
    // are points, not backing pixels. NSScreen.mainScreen follows the focused
    // window; only screens[0] supplies the stable Cocoa/CG origin conversion.
    let primary_top = MainThreadMarker::new()
        .and_then(|mtm| NSScreen::screens(mtm).firstObject())
        .map(|screen| screen.frame().origin.y + screen.frame().size.height)
        .unwrap_or(0.0);
    monitor_point_from_cocoa(mouse.x, mouse.y, primary_top)
}

fn monitor_point_from_cocoa(x: f64, y: f64, primary_top: f64) -> PhysicalPosition<f64> {
    PhysicalPosition::new(x, primary_top - y)
}

fn open_panel(icon: Option<(PhysicalPosition<f64>, PhysicalSize<f64>)>) {
    let now = now_ms();
    let previous = LAST_CLICK_MS.swap(now, Ordering::SeqCst);
    if now.saturating_sub(previous) < 250 {
        log_tray("debounced");
        return;
    }
    let Some(app) = APP.get().cloned() else {
        log_tray("no-app-handle");
        return;
    };
    let point = click_point();
    let icon = icon.or_else(icon_from_status_item);
    log_tray(&format!(
        "show x={:.0} y={:.0} icon={}",
        point.x,
        point.y,
        icon.is_some()
    ));
    let queued = app.clone().run_on_main_thread(move || {
        match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            show_usage_panel(&app, point)
        })) {
            Ok(Ok(())) => log_tray("shown"),
            Ok(Err(error)) => log_tray(&format!("show-error {error}")),
            Err(_) => log_tray("show-panic"),
        }
    });
    if let Err(error) = queued {
        log_tray(&format!("queue-error {error}"));
    }
}

fn handle_monitor_click(source: &str) {
    if !pointer_hits_status_item() {
        return;
    }
    log_tray(source);
    open_panel(icon_from_status_item());
}

pub fn install(app: &AppHandle) -> Result<(), String> {
    let _ = APP.set(app.clone());
    if let Some(tray) = app.remove_tray_by_id("main-tray") {
        drop(tray);
        log_tray("removed-tauri-tray");
    }
    let mtm = MainThreadMarker::new().ok_or("status item must be created on the main thread")?;
    let bar = NSStatusBar::systemStatusBar();
    let item = bar.statusItemWithLength(NSVariableStatusItemLength);
    item.setVisible(true);
    let image = load_template_image()?;
    let target = alloc_target(mtm)?;
    let button = item
        .button(mtm)
        .ok_or("NSStatusItem.button is unavailable")?;
    button.setImage(Some(&image));
    button.setToolTip(Some(&NSString::from_str("TokenDance")));
    button.setEnabled(true);
    button.setAppearsDisabled(false);
    unsafe {
        button.setTarget(Some(&target));
        button.setAction(Some(sel!(onClick:)));
        let _ = button.sendActionOn(NSEventMask::LeftMouseUp);
    }
    let menu = build_status_menu(mtm, &target)?;
    // NSView contextual menu (right-click). Do not assign NSStatusItem.menu,
    // which would steal the left-click action.
    unsafe {
        button.setMenu(Some(&menu));
    }
    ITEM_PTR.store(Retained::as_ptr(&item) as usize, Ordering::SeqCst);
    install_click_monitors();
    // Status items and their targets must stay alive for the process lifetime.
    std::mem::forget(item);
    std::mem::forget(target);
    std::mem::forget(menu);
    log_tray("native-status-item-ready");
    Ok(())
}

fn build_status_menu(
    mtm: MainThreadMarker,
    target: &StatusTarget,
) -> Result<Retained<NSMenu>, String> {
    let menu = NSMenu::new(mtm);
    let items = [
        (
            "打开 TokenDance 设置 / Open Settings",
            sel!(onOpenSettings:),
        ),
        ("暂停/恢复数据采集 / Pause Collection", sel!(onTogglePause:)),
        ("退出程序 / Quit TokenDance", sel!(onQuit:)),
    ];
    for (title, action) in items {
        let item = unsafe {
            NSMenuItem::initWithTitle_action_keyEquivalent(
                NSMenuItem::alloc(mtm),
                &NSString::from_str(title),
                Some(action),
                &NSString::from_str(""),
            )
        };
        unsafe {
            item.setTarget(Some(target));
        }
        menu.addItem(&item);
    }
    Ok(menu)
}

fn install_click_monitors() {
    let local = RcBlock::new(|event: std::ptr::NonNull<NSEvent>| -> *mut NSEvent {
        handle_monitor_click("local-mousedown");
        event.as_ptr()
    });
    let local_monitor = unsafe {
        NSEvent::addLocalMonitorForEventsMatchingMask_handler(NSEventMask::LeftMouseDown, &local)
    };
    if local_monitor.is_some() {
        log_tray("local-monitor-ready");
        std::mem::forget(local_monitor);
    } else {
        log_tray("local-monitor-failed");
    }
    std::mem::forget(local);

    let global = RcBlock::new(|_event: std::ptr::NonNull<NSEvent>| {
        handle_monitor_click("global-mousedown");
    });
    let global_monitor =
        NSEvent::addGlobalMonitorForEventsMatchingMask_handler(NSEventMask::LeftMouseDown, &global);
    if global_monitor.is_some() {
        log_tray("global-monitor-ready");
        std::mem::forget(global_monitor);
    } else {
        log_tray("global-monitor-failed");
    }
    std::mem::forget(global);
}

fn alloc_target(mtm: MainThreadMarker) -> Result<Retained<StatusTarget>, String> {
    let allocated = StatusTarget::alloc(mtm).set_ivars(());
    let target: Retained<StatusTarget> = unsafe { msg_send![super(allocated), init] };
    Ok(target)
}

fn load_template_image() -> Result<Retained<NSImage>, String> {
    let bytes = include_bytes!("../icons/tray-template.png");
    let data = NSData::from_vec(bytes.to_vec());
    let image = NSImage::initWithData(NSImage::alloc(), &data)
        .ok_or("failed to decode tray template image")?;
    image.setSize(NSSize::new(18.0, 18.0));
    image.setTemplate(true);
    Ok(image)
}

pub fn activate_app() {
    let Some(mtm) = MainThreadMarker::new() else {
        return;
    };
    let app = NSApplication::sharedApplication(mtm);
    let _ = app.setActivationPolicy(NSApplicationActivationPolicy::Regular);
    #[allow(deprecated)]
    app.activateIgnoringOtherApps(true);
    app.activate();
}

pub fn apply_settings_overlay(window: &tauri::WebviewWindow) {
    let Some(_mtm) = MainThreadMarker::new() else {
        return;
    };
    if let Ok(ptr) = window.ns_window() {
        if ptr.is_null() {
            return;
        }
        let ns_window = ptr.cast::<NSWindow>();
        unsafe {
            (*ns_window).setStyleMask(
                NSWindowStyleMask::Titled
                    | NSWindowStyleMask::Closable
                    | NSWindowStyleMask::Miniaturizable
                    | NSWindowStyleMask::Resizable
                    | NSWindowStyleMask::FullSizeContentView,
            );
            (*ns_window).setTitlebarAppearsTransparent(true);
            (*ns_window).setTitleVisibility(NSWindowTitleVisibility::Hidden);
            (*ns_window).setMovable(true);
            // WKWebView swallows -webkit-app-region / JS startDragging on Overlay
            // windows. tao then calls performWindowDragWithEvent on mouseDown.
            (*ns_window).setMovableByWindowBackground(true);
        }
    }
}

pub fn order_front(window: &tauri::WebviewWindow) {
    let Some(_mtm) = MainThreadMarker::new() else {
        return;
    };
    if let Ok(ptr) = window.ns_window() {
        if !ptr.is_null() {
            let ns_window = ptr.cast::<NSWindow>();
            unsafe {
                (*ns_window).setHidesOnDeactivate(false);
                (*ns_window).setLevel(0);
                // CanJoinAllSpaces and MoveToActiveSpace are mutually exclusive.
                // Transient and FullScreenAuxiliary belong to different groups;
                // mixing CanJoinAllSpaces with MoveToActiveSpace panics AppKit.
                (*ns_window).setCollectionBehavior(
                    NSWindowCollectionBehavior::CanJoinAllSpaces
                        | NSWindowCollectionBehavior::FullScreenAuxiliary,
                );
                (*ns_window).orderFrontRegardless();
                (*ns_window).makeKeyAndOrderFront(None);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn monitor_lookup_uses_primary_origin_and_unscaled_points() {
        // Retina primary display with an external display to its right.
        assert_eq!(monitor_point_from_cocoa(1600.0, 970.0, 982.0), PhysicalPosition::new(1600.0, 12.0));
        // Displays above or left retain their negative CG coordinates.
        assert_eq!(monitor_point_from_cocoa(-1200.0, 1200.0, 982.0), PhysicalPosition::new(-1200.0, -218.0));
    }
}
