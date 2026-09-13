//! Transparent AppKit windows. Native objects are only accessed on the UI thread.
//! Tauri owns each NSWindow; we retain no raw native handles across callbacks.

use std::cell::{Cell, RefCell};

use block2::RcBlock;
use core_foundation::{
    array::CFArray,
    base::{CFType, TCFType},
    dictionary::CFDictionary,
    number::CFNumber,
    string::CFString,
};
use objc2::{rc::Retained, runtime::AnyObject, MainThreadMarker};
use objc2_app_kit::{
    NSColor, NSEvent, NSEventMask, NSFloatingWindowLevel, NSScreen, NSWindow,
    NSWindowCollectionBehavior, NSWorkspace,
};
use objc2_core_graphics::CGPath;
use objc2_foundation::{NSPoint, NSRect, NSSize};
use objc2_quartz_core::{CAShapeLayer, CATransaction};
use tauri::{
    AppHandle, Manager, Monitor, PhysicalPosition, WebviewUrl, WebviewWindow, WebviewWindowBuilder,
};

use super::super::placement::PixelRect;
use super::{
    dip_to_physical, effects_bounds, effects_size_dip, next_instance_generation,
    orb_origin_from_center, DETAILS_LABEL, DETAILS_MIN_HEIGHT_DIP, DETAILS_MIN_WIDTH_DIP,
    EFFECTS_LABEL, ORB_LABEL,
};

pub struct OrbWindowGroup {
    generation: u64,
    diameter_dip: f64,
    orb: WebviewWindow,
    effects: Option<WebviewWindow>,
    clip: Cell<Option<PixelRect>>,
}

impl OrbWindowGroup {
    // The controller calls the factory off-thread with no controller lock held.
    pub fn create(app: &AppHandle, diameter: f64, effects: bool) -> Result<Self, String> {
        let diameter = if diameter.is_finite() && diameter > 0.0 {
            diameter
        } else {
            super::DEFAULT_DIAMETER_DIP
        };
        for label in [DETAILS_LABEL, EFFECTS_LABEL, ORB_LABEL] {
            if let Some(window) = app.get_webview_window(label) {
                window.destroy().map_err(|e| e.to_string())?;
            }
        }
        let generation = next_instance_generation();
        let orb = create_shell(app, ORB_LABEL, "orb", diameter, diameter, generation)?;
        let effects = if effects {
            match create_shell(
                app,
                EFFECTS_LABEL,
                "orb-effects",
                effects_size_dip(diameter),
                effects_size_dip(diameter),
                generation,
            ) {
                Ok(window) => Some(window),
                Err(error) => {
                    eprintln!("orb effects disabled: {error}");
                    None
                }
            }
        } else {
            None
        };
        let native_orb = orb.clone();
        let native_effects = effects.clone();
        let configured = on_main(app, move || {
            configure_window(&native_orb, false)?;
            if let Some(window) = &native_effects {
                configure_window(window, true)?;
            }
            install_pointer_monitors(native_orb);
            Ok(())
        });
        if let Err(error) = configured {
            if let Some(window) = &effects {
                let _ = window.destroy();
            }
            let _ = orb.destroy();
            return Err(error);
        }
        Ok(Self {
            generation,
            diameter_dip: diameter,
            orb,
            effects,
            clip: Cell::new(None),
        })
    }

    pub fn generation(&self) -> u64 {
        self.generation
    }
    pub fn labels(&self) -> Vec<&'static str> {
        let mut labels = vec![ORB_LABEL];
        if self.effects.is_some() {
            labels.push(EFFECTS_LABEL);
        }
        if self
            .orb
            .app_handle()
            .get_webview_window(DETAILS_LABEL)
            .is_some()
        {
            labels.push(DETAILS_LABEL);
        }
        labels
    }

    pub fn show_without_activation(&self) -> Result<(), String> {
        self.restore_effects_follow()?;
        if let Some(window) = &self.effects {
            native_window(window)?.orderFrontRegardless();
        }
        native_window(&self.orb)?.orderFrontRegardless();
        update_pointer_hit();
        diagnostic(&format!(
            "show generation={} visible={} bounds={:?}",
            self.generation,
            self.orb.is_visible().unwrap_or(false),
            window_bounds(&self.orb)
        ));
        Ok(())
    }

    pub fn hide(&self) -> Result<(), String> {
        if let Some(window) = self.orb.app_handle().get_webview_window(DETAILS_LABEL) {
            window.hide().map_err(|e| e.to_string())?;
        }
        if let Some(window) = &self.effects {
            window.hide().map_err(|e| e.to_string())?;
        }
        self.orb.hide().map_err(|e| e.to_string())
    }

    pub fn destroy(&self) {
        diagnostic(&format!("destroy generation={}", self.generation));
        // Controller destruction is dispatched to the UI thread.
        remove_pointer_monitors();
        for label in [DETAILS_LABEL, EFFECTS_LABEL, ORB_LABEL] {
            if let Some(window) = self.orb.app_handle().get_webview_window(label) {
                let _ = window.destroy();
            }
        }
    }

    pub fn move_orb_center(&self, center: PhysicalPosition<i32>) -> Result<(), String> {
        let scale = monitor_at_physical(self.orb.app_handle(), center)
            .map(|m| m.scale_factor())
            .unwrap_or(self.orb.scale_factor().map_err(|e| e.to_string())?);
        let origin = orb_origin_from_center(center, self.diameter_dip, scale);
        let size = dip_to_physical(self.diameter_dip, scale);
        set_bounds(
            &self.orb,
            PixelRect {
                x: origin.x,
                y: origin.y,
                width: size,
                height: size,
            },
            scale,
        )?;
        self.restore_effects_follow()
    }

    pub fn restore_effects_follow(&self) -> Result<(), String> {
        let bounds = window_bounds(&self.orb)?;
        let scale = self.orb.scale_factor().map_err(|e| e.to_string())?;
        apply_mask(&self.orb, bounds, self.clip.get(), true, scale)?;
        if let Some(window) = &self.effects {
            let effects = effects_bounds(bounds, self.diameter_dip);
            set_bounds(window, effects, scale)?;
            apply_mask(window, effects, self.clip.get(), false, scale)?;
            if self.orb.is_visible().unwrap_or(false) {
                native_window(window)?.orderFrontRegardless();
                native_window(&self.orb)?.orderFrontRegardless();
            }
        }
        update_pointer_clip(self.clip.get(), scale);
        Ok(())
    }

    pub fn settle_motion(&self) -> Result<(), String> {
        self.restore_effects_follow()
    }

    pub fn move_edge_frame(&self, orb: PixelRect, work: PixelRect) -> Result<(), String> {
        self.clip.set(Some(work));
        let scale = self.orb.scale_factor().map_err(|e| e.to_string())?;
        set_bounds(&self.orb, orb, scale)?;
        self.restore_effects_follow()
    }

    pub fn orb_origin(&self) -> Result<PhysicalPosition<i32>, String> {
        self.orb.outer_position().map_err(|e| e.to_string())
    }
    pub fn orb_window(&self) -> WebviewWindow {
        self.orb.clone()
    }
    pub fn apply_circular_region(&self) -> Result<(), String> {
        apply_mask(
            &self.orb,
            window_bounds(&self.orb)?,
            None,
            true,
            self.orb.scale_factor().map_err(|e| e.to_string())?,
        )
    }
    pub fn set_docked_clip(&self, clip: Option<PixelRect>) -> Result<(), String> {
        self.clip.set(clip);
        self.restore_effects_follow()
    }
    pub fn begin_native_drag(orb: &WebviewWindow) -> Result<(), String> {
        if orb.label() != ORB_LABEL {
            return Err("native drag is only allowed for orb".into());
        }
        orb.start_dragging().map_err(|e| e.to_string())
    }
    pub fn ensure_details(&self) -> Result<WebviewWindow, String> {
        if let Some(window) = self.orb.app_handle().get_webview_window(DETAILS_LABEL) {
            return Ok(window);
        }
        let window = create_shell(
            self.orb.app_handle(),
            DETAILS_LABEL,
            "orb-details",
            DETAILS_MIN_WIDTH_DIP,
            DETAILS_MIN_HEIGHT_DIP,
            self.generation,
        )?;
        configure_window(&window, false)?;
        Ok(window)
    }
    pub fn show_details_without_activation(&self) -> Result<(), String> {
        let window = self
            .orb
            .app_handle()
            .get_webview_window(DETAILS_LABEL)
            .ok_or("orb details not created")?;
        native_window(&window)?.orderFrontRegardless();
        Ok(())
    }
}

fn create_shell(
    app: &AppHandle,
    label: &str,
    view: &str,
    width: f64,
    height: f64,
    generation: u64,
) -> Result<WebviewWindow, String> {
    let window = WebviewWindowBuilder::new(
        app,
        label,
        WebviewUrl::App(format!("index.html?view={view}&generation={generation}&mode=full").into()),
    )
    .title(match label {
        ORB_LABEL => "TokenDance Orb",
        EFFECTS_LABEL => "TokenDance Orb Effects",
        _ => "TokenDance Orb Details",
    })
    .decorations(false)
    .resizable(false)
    .transparent(true)
    .shadow(false)
    .always_on_top(true)
    .skip_taskbar(true)
    .visible(false)
    .focused(false)
    .focusable(label == DETAILS_LABEL)
    .accept_first_mouse(true)
    .inner_size(width, height)
    .background_color(tauri::window::Color(0, 0, 0, 0))
    .build()
    .map_err(|e| e.to_string())?;
    diagnostic(&format!("created {label} generation={generation}"));
    if label == EFFECTS_LABEL {
        window
            .set_ignore_cursor_events(true)
            .map_err(|e| e.to_string())?;
    }
    Ok(window)
}

fn diagnostic(message: &str) {
    if cfg!(debug_assertions) {
        if let Ok(paths) = collector_service::AppPaths::production() {
            collector_service::runtime::append_log(&paths.logs, &format!("macos-orb {message}"));
        }
    }
}

fn on_main<R: Send + 'static>(
    app: &AppHandle,
    work: impl FnOnce() -> Result<R, String> + Send + 'static,
) -> Result<R, String> {
    if MainThreadMarker::new().is_some() {
        return work();
    }
    let (send, receive) = std::sync::mpsc::sync_channel(1);
    app.run_on_main_thread(move || {
        let _ = send.send(work());
    })
    .map_err(|e| e.to_string())?;
    receive.recv().map_err(|e| e.to_string())?
}

fn native_window(window: &WebviewWindow) -> Result<&NSWindow, String> {
    MainThreadMarker::new().ok_or("orb native work requires the main thread")?;
    let ptr = window
        .ns_window()
        .map_err(|e| e.to_string())?
        .cast::<NSWindow>();
    // Tauri owns the live NSWindow for this WebviewWindow. Use only synchronously.
    unsafe { ptr.as_ref() }.ok_or_else(|| "missing orb NSWindow".into())
}

fn configure_window(window: &WebviewWindow, ignore_mouse: bool) -> Result<(), String> {
    let native = native_window(window)?;
    native.setOpaque(false);
    native.setBackgroundColor(Some(&NSColor::clearColor()));
    native.setHasShadow(false);
    native.setHidesOnDeactivate(false);
    native.setIgnoresMouseEvents(ignore_mouse);
    native.setAcceptsMouseMovedEvents(true);
    native.setLevel(NSFloatingWindowLevel);
    // CanJoinAllSpaces and MoveToActiveSpace are mutually exclusive.
    native.setCollectionBehavior(
        NSWindowCollectionBehavior::CanJoinAllSpaces
            | NSWindowCollectionBehavior::FullScreenAuxiliary
            | NSWindowCollectionBehavior::Transient
            | NSWindowCollectionBehavior::IgnoresCycle,
    );
    Ok(())
}

fn primary_top() -> Result<f64, String> {
    let mtm = MainThreadMarker::new().ok_or("screen lookup requires main thread")?;
    let screens = NSScreen::screens(mtm);
    let screen = screens.firstObject().ok_or("no screen")?;
    let frame = screen.frame();
    Ok(frame.origin.y + frame.size.height)
}

fn set_bounds(window: &WebviewWindow, bounds: PixelRect, scale: f64) -> Result<(), String> {
    let native = native_window(window)?;
    let rect = cocoa_frame(bounds, scale, primary_top()?);
    // Borderless frame and content dimensions coincide; one update avoids resize flicker.
    native.setFrame_display(rect, true);
    Ok(())
}

fn cocoa_frame(bounds: PixelRect, scale: f64, top: f64) -> NSRect {
    NSRect::new(
        NSPoint::new(
            bounds.x as f64 / scale,
            top - bounds.bottom() as f64 / scale,
        ),
        NSSize::new(bounds.width as f64 / scale, bounds.height as f64 / scale),
    )
}

fn window_bounds(window: &WebviewWindow) -> Result<PixelRect, String> {
    let origin = window.outer_position().map_err(|e| e.to_string())?;
    let size = window.inner_size().map_err(|e| e.to_string())?;
    Ok(PixelRect {
        x: origin.x,
        y: origin.y,
        width: size.width as i32,
        height: size.height as i32,
    })
}

fn intersection(a: PixelRect, b: PixelRect) -> PixelRect {
    let x = a.x.max(b.x);
    let y = a.y.max(b.y);
    PixelRect {
        x,
        y,
        width: (a.right().min(b.right()) - x).max(0),
        height: (a.bottom().min(b.bottom()) - y).max(0),
    }
}

fn apply_mask(
    window: &WebviewWindow,
    bounds: PixelRect,
    clip: Option<PixelRect>,
    circular: bool,
    scale: f64,
) -> Result<(), String> {
    let view = native_window(window)?
        .contentView()
        .ok_or("missing orb content view")?;
    view.setWantsLayer(true);
    let layer = view.layer().ok_or("missing orb layer")?;
    CATransaction::begin();
    CATransaction::setDisableActions(true);
    layer.setCornerRadius(if circular {
        bounds.width as f64 / scale / 2.0
    } else {
        0.0
    });
    layer.setMasksToBounds(true);
    if let Some(clip) = clip {
        let visible = intersection(bounds, clip);
        let y = if view.isFlipped() {
            visible.y - bounds.y
        } else {
            bounds.bottom() - visible.bottom()
        };
        let rect = NSRect::new(
            NSPoint::new((visible.x - bounds.x) as f64 / scale, y as f64 / scale),
            NSSize::new(visible.width as f64 / scale, visible.height as f64 / scale),
        );
        let path = unsafe { CGPath::with_rect(rect, std::ptr::null()) };
        let mask = CAShapeLayer::new();
        mask.setPath(Some(&path));
        unsafe {
            layer.setMask(Some(&mask));
        }
    } else {
        unsafe {
            layer.setMask(None);
        }
    }
    CATransaction::commit();
    Ok(())
}

struct PointerMonitors {
    orb: WebviewWindow,
    clip: Option<NSRect>,
    local: Option<Retained<AnyObject>>,
    global: Option<Retained<AnyObject>>,
}
thread_local! { static POINTER: RefCell<Option<PointerMonitors>> = const { RefCell::new(None) }; }

fn install_pointer_monitors(orb: WebviewWindow) {
    remove_pointer_monitors();
    let local = RcBlock::new(|event: std::ptr::NonNull<NSEvent>| {
        update_pointer_hit();
        event.as_ptr()
    });
    let global = RcBlock::new(|_: std::ptr::NonNull<NSEvent>| {
        update_pointer_hit();
    });
    let mask = NSEventMask::MouseMoved | NSEventMask::LeftMouseUp | NSEventMask::RightMouseUp;
    let local = unsafe { NSEvent::addLocalMonitorForEventsMatchingMask_handler(mask, &local) };
    let global = NSEvent::addGlobalMonitorForEventsMatchingMask_handler(mask, &global);
    POINTER.with(|state| {
        *state.borrow_mut() = Some(PointerMonitors {
            orb,
            clip: None,
            local,
            global,
        })
    });
}

fn remove_pointer_monitors() {
    if MainThreadMarker::new().is_none() {
        return;
    }
    let monitors = POINTER.with(|state| state.borrow_mut().take());
    if let Some(monitors) = monitors {
        for monitor in [monitors.local, monitors.global].into_iter().flatten() {
            unsafe {
                NSEvent::removeMonitor(&monitor);
            }
        }
    }
}

fn update_pointer_clip(clip: Option<PixelRect>, scale: f64) {
    let clip = clip.and_then(|bounds| {
        primary_top()
            .ok()
            .map(|top| cocoa_frame(bounds, scale, top))
    });
    POINTER.with(|state| {
        if let Some(state) = state.borrow_mut().as_mut() {
            state.clip = clip;
        }
    });
    update_pointer_hit();
}

fn hit_circle(x: f64, y: f64, frame: NSRect, clip: Option<NSRect>) -> bool {
    if frame.size.width <= 0.0 || frame.size.height <= 0.0 {
        return false;
    }
    let dx = (x - frame.origin.x - frame.size.width / 2.0) / (frame.size.width / 2.0);
    let dy = (y - frame.origin.y - frame.size.height / 2.0) / (frame.size.height / 2.0);
    dx * dx + dy * dy <= 1.0
        && clip.is_none_or(|r| {
            x >= r.origin.x
                && y >= r.origin.y
                && x < r.origin.x + r.size.width
                && y < r.origin.y + r.size.height
        })
}

fn update_pointer_hit() {
    if MainThreadMarker::new().is_none() || NSEvent::pressedMouseButtons() != 0 {
        return;
    }
    POINTER.with(|state| {
        let state = state.borrow();
        let Some(state) = state.as_ref() else {
            return;
        };
        let Ok(native) = native_window(&state.orb) else {
            return;
        };
        let point = NSEvent::mouseLocation();
        native.setIgnoresMouseEvents(!hit_circle(point.x, point.y, native.frame(), state.clip));
    });
}

fn monitor_at_physical(app: &AppHandle, point: PhysicalPosition<i32>) -> Option<Monitor> {
    // Tao's monitor_from_point takes CGDisplay points, unlike its returned physical bounds.
    app.available_monitors()
        .ok()?
        .into_iter()
        .find(|m| {
            let p = m.position();
            let s = m.size();
            point.x >= p.x
                && point.y >= p.y
                && point.x < p.x + s.width as i32
                && point.y < p.y + s.height as i32
        })
        .or_else(|| app.primary_monitor().ok().flatten())
}

pub fn is_foreground_fullscreen_at(app: &AppHandle, origin: PhysicalPosition<i32>) -> bool {
    monitor_at_physical(app, origin)
        .is_some_and(|m| is_foreground_fullscreen_excluding_self(app, &m))
}

pub fn is_foreground_fullscreen_excluding_self(_app: &AppHandle, monitor: &Monitor) -> bool {
    if MainThreadMarker::new().is_none() {
        return false;
    }
    let Some(front) = NSWorkspace::sharedWorkspace().frontmostApplication() else {
        return false;
    };
    let pid = front.processIdentifier();
    if pid == std::process::id() as i32 {
        return false;
    }
    use core_graphics::window::{
        copy_window_info, kCGWindowListExcludeDesktopElements, kCGWindowListOptionOnScreenOnly,
    };
    let Some(list) = copy_window_info(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        0,
    ) else {
        return false;
    };
    // Window metadata only: no screenshots, titles or accessibility permissions.
    let windows: CFArray<CFDictionary<CFString, CFType>> =
        unsafe { CFArray::wrap_under_get_rule(list.as_concrete_TypeRef()) };
    let scale = monitor.scale_factor();
    let origin = monitor.position();
    let size = monitor.size();
    for window in windows.iter() {
        if number(&window, "kCGWindowOwnerPID") != Some(pid as f64)
            || number(&window, "kCGWindowLayer") != Some(0.0)
        {
            continue;
        }
        let Some(raw_bounds) = window
            .find(CFString::new("kCGWindowBounds"))
            .and_then(|v| v.downcast::<CFDictionary>())
        else {
            continue;
        };
        let bounds: CFDictionary<CFString, CFType> =
            unsafe { CFDictionary::wrap_under_get_rule(raw_bounds.as_concrete_TypeRef()) };
        let (Some(x), Some(y), Some(w), Some(h)) = (
            number(&bounds, "X"),
            number(&bounds, "Y"),
            number(&bounds, "Width"),
            number(&bounds, "Height"),
        ) else {
            continue;
        };
        if x + w <= origin.x as f64 / scale || x >= (origin.x as f64 + size.width as f64) / scale {
            continue;
        }
        if y + h <= origin.y as f64 / scale || y >= (origin.y as f64 + size.height as f64) / scale {
            continue;
        }
        return (x - origin.x as f64 / scale).abs() <= 2.0
            && (y - origin.y as f64 / scale).abs() <= 2.0
            && (w - size.width as f64 / scale).abs() <= 2.0
            && (h - size.height as f64 / scale).abs() <= 2.0;
    }
    false
}

fn number(dict: &CFDictionary<CFString, CFType>, key: &str) -> Option<f64> {
    dict.find(CFString::new(key))?
        .downcast::<CFNumber>()?
        .to_f64()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn frames_use_primary_screen_top_and_target_scale() {
        let frame = cocoa_frame(
            PixelRect {
                x: -1600,
                y: 240,
                width: 224,
                height: 224,
            },
            2.0,
            982.0,
        );
        assert_eq!(frame.origin.x, -800.0);
        assert_eq!(frame.origin.y, 750.0);
        assert_eq!(frame.size.width, 112.0);
    }
    #[test]
    fn transparent_corners_and_hidden_edge_reject_clicks() {
        let frame = NSRect::new(NSPoint::new(0.0, 0.0), NSSize::new(100.0, 100.0));
        assert!(hit_circle(50.0, 50.0, frame, None));
        assert!(!hit_circle(1.0, 1.0, frame, None));
        let clip = NSRect::new(NSPoint::new(70.0, 0.0), NSSize::new(30.0, 100.0));
        assert!(!hit_circle(50.0, 50.0, frame, Some(clip)));
        assert!(hit_circle(90.0, 50.0, frame, Some(clip)));
    }
    #[test]
    fn clipping_never_spills_into_adjacent_monitor() {
        let visible = intersection(
            PixelRect {
                x: -70,
                y: 20,
                width: 100,
                height: 100,
            },
            PixelRect {
                x: 0,
                y: 0,
                width: 1000,
                height: 800,
            },
        );
        assert_eq!(
            visible,
            PixelRect {
                x: 0,
                y: 20,
                width: 30,
                height: 100
            }
        );
        assert_eq!(
            intersection(
                PixelRect {
                    x: -110,
                    y: 0,
                    width: 100,
                    height: 100
                },
                PixelRect {
                    x: 0,
                    y: 0,
                    width: 100,
                    height: 100
                }
            )
            .width,
            0
        );
    }
}
