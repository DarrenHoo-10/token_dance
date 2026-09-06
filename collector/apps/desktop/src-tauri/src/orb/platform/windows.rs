//! Windows HWND/HRGN adapter for the orb window group.
//! Native handles never leave this module.

use tauri::{
    AppHandle, Manager, Monitor, PhysicalPosition, WebviewUrl, WebviewWindow, WebviewWindowBuilder,
};
use windows::Win32::Foundation::{HWND, POINT, RECT};
use windows::Win32::Graphics::Gdi::{
    CombineRgn, CreateRectRgn, RGN_AND, CreateEllipticRgn, DeleteObject, GetMonitorInfoW, MonitorFromPoint, SetWindowRgn, MONITORINFO,
    MONITOR_DEFAULTTONEAREST, RedrawWindow, RDW_ALLCHILDREN, RDW_INVALIDATE, RDW_UPDATENOW,
};
use windows::Win32::UI::WindowsAndMessaging::{
    GetClassNameW, GetDesktopWindow, GetForegroundWindow, GetShellWindow,
    GetWindowLongPtrW, GetWindowRect, GetWindowThreadProcessId, IsIconic, IsWindow,
    IsWindowVisible, SetWindowPos, ShowWindow, GWL_STYLE, HWND_TOPMOST,
    SWP_NOACTIVATE, SWP_NOCOPYBITS, SWP_NOMOVE, SWP_NOSIZE, SWP_NOZORDER, SWP_SHOWWINDOW, SW_HIDE,
    SW_SHOWNOACTIVATE, WS_MAXIMIZE,
};

use super::{
    dip_to_physical, effects_bounds, effects_size_dip, next_instance_generation, orb_origin_from_center,
    DETAILS_LABEL, DETAILS_MIN_HEIGHT_DIP, DETAILS_MIN_WIDTH_DIP, EFFECTS_LABEL, ORB_LABEL,
};
use super::super::placement::PixelRect;

const FULLSCREEN_TOLERANCE_PX: i32 = 4;

enum ShellKind {
    Orb,
    Effects,
    Details,
}

pub struct OrbWindowGroup {
    generation: u64,
    diameter_dip: f64,
    orb: WebviewWindow,
    effects: Option<WebviewWindow>,
    orb_hwnd: isize,
    effects_hwnd: Option<isize>,
    details_hwnd: std::cell::Cell<Option<isize>>,
    docked: std::cell::Cell<bool>,
    region_size: std::cell::Cell<(i32, i32)>,
}

impl OrbWindowGroup {
    pub fn create(app: &AppHandle, diameter_dip: f64, effects: bool) -> Result<Self, String> {
        let diameter_dip = if diameter_dip > 0.0 {
            diameter_dip
        } else {
            super::DEFAULT_DIAMETER_DIP
        };
        destroy_labeled(app, DETAILS_LABEL);
        destroy_labeled(app, EFFECTS_LABEL);
        destroy_labeled(app, ORB_LABEL);

        let generation = next_instance_generation();
        let orb = create_shell(
            app,
            ORB_LABEL,
            "orb",
            diameter_dip,
            diameter_dip,
            ShellKind::Orb,
            generation,
        )?;
        let orb_hwnd = capture_hwnd(&orb)?;
        if let Err(error) = apply_elliptic_region(hwnd_from_raw(orb_hwnd)) {
            let _ = orb.destroy();
            return Err(error);
        }

        let (effects, effects_hwnd) = if effects {
            match create_shell(
                app,
                EFFECTS_LABEL,
                "orb-effects",
                effects_size_dip(diameter_dip),
                effects_size_dip(diameter_dip),
                ShellKind::Effects,
                generation,
            ) {
                Ok(window) => match capture_hwnd(&window) {
                    Ok(hwnd) => (Some(window), Some(hwnd)),
                    Err(error) => {
                        eprintln!("orb-effects hwnd capture failed; degrading to orb-only: {error}");
                        let _ = window.destroy();
                        (None, None)
                    }
                },
                Err(error) => {
                    eprintln!("orb-effects create failed; degrading to orb-only: {error}");
                    (None, None)
                }
            }
        } else {
            (None, None)
        };

        let group = Self {
            generation,
            diameter_dip,
            orb,
            effects,
            orb_hwnd,
            effects_hwnd,
            details_hwnd: std::cell::Cell::new(None),
            docked: std::cell::Cell::new(false),
            region_size: std::cell::Cell::new((0, 0)),
        };
        let _ = group.restack();
        Ok(group)
    }

    pub fn generation(&self) -> u64 {
        self.generation
    }

    pub fn labels(&self) -> Vec<&'static str> {
        let mut labels = vec![ORB_LABEL];
        if self.effects_hwnd.is_some() {
            labels.push(EFFECTS_LABEL);
        }
        if self.details_hwnd.get().is_some() {
            labels.push(DETAILS_LABEL);
        }
        labels
    }

    pub fn show_without_activation(&self) -> Result<(), String> {
        self.sync_geometry()?;
        if let Some(hwnd) = self.effects_hwnd.filter(|_| !self.docked.get()) {
            let _ = show_without_activation_hwnd(hwnd_from_raw(hwnd));
        }
        show_without_activation_hwnd(hwnd_from_raw(self.orb_hwnd))?;
        self.restack()?;
        if !actual_visibility_hwnd(hwnd_from_raw(self.orb_hwnd)) {
            return Err("orb window is not visible after show".into());
        }
        Ok(())
    }

    pub fn hide(&self) -> Result<(), String> {
        if let Some(hwnd) = self.details_hwnd.get() {
            hide_hwnd(hwnd_from_raw(hwnd));
        }
        if let Some(hwnd) = self.effects_hwnd {
            hide_hwnd(hwnd_from_raw(hwnd));
        }
        hide_hwnd(hwnd_from_raw(self.orb_hwnd));
        if actual_visibility_hwnd(hwnd_from_raw(self.orb_hwnd)) {
            return Err("orb window is still visible after hide".into());
        }
        Ok(())
    }

    pub fn destroy(&self) {
        if let Some(details) = self.orb.app_handle().get_webview_window(DETAILS_LABEL) {
            let _ = details.destroy();
        }
        if let Some(effects) = &self.effects {
            let _ = effects.destroy();
        }
        let _ = self.orb.destroy();
    }

    pub fn move_orb_center(&self, physical: PhysicalPosition<i32>) -> Result<(), String> {
        let scale = self.orb.app_handle()
            .monitor_from_point(physical.x as f64, physical.y as f64)
            .map_err(|error| error.to_string())?
            .map(|monitor| monitor.scale_factor())
            .unwrap_or(self.orb.scale_factor().map_err(|error| error.to_string())?);
        let origin = orb_origin_from_center(physical, self.diameter_dip, scale);
        let size = dip_to_physical(self.diameter_dip, scale);
        set_bounds_noactivate(hwnd_from_raw(self.orb_hwnd), PixelRect {
            x: origin.x, y: origin.y, width: size, height: size,
        })?;
        self.restore_effects_follow()
    }

    pub fn restore_effects_follow(&self) -> Result<(), String> {
        self.sync_geometry()?;
        if let Some(hwnd) = self.effects_hwnd {
            let hwnd = hwnd_from_raw(hwnd);
            if !self.docked.get() && actual_visibility_hwnd(hwnd_from_raw(self.orb_hwnd)) {
                if !actual_visibility_hwnd(hwnd) { show_without_activation_hwnd(hwnd)?; }
            } else {
                hide_hwnd(hwnd);
            }
        }
        self.restack()
    }

    pub fn settle_motion(&self) -> Result<(), String> {
        if !actual_visibility_hwnd(hwnd_from_raw(self.orb_hwnd)) { return Ok(()); }
        self.restore_effects_follow()?;
        for hwnd in [Some(self.orb_hwnd),self.effects_hwnd].into_iter().flatten() {
            let hwnd = hwnd_from_raw(hwnd);
            if actual_visibility_hwnd(hwnd) {
                unsafe { let _ = RedrawWindow(Some(hwnd),None,None,RDW_INVALIDATE|RDW_ALLCHILDREN|RDW_UPDATENOW); }
            }
        }
        Ok(())
    }

    /// Edge animations stay on one monitor. Avoid monitor queries, DPI changes
    /// and repeated topmost restacking in the frame loop.
    pub fn move_edge_frame(&self, orb: PixelRect, work_area: PixelRect) -> Result<(), String> {
        self.docked.set(true);
        let hwnd = hwnd_from_raw(self.orb_hwnd);
        set_bounds_noactivate(hwnd, orb)?;
        apply_work_area_region(hwnd, work_area, true)?;
        if let Some(effects) = self.effects_hwnd {
            let effects = hwnd_from_raw(effects);
            set_bounds_noactivate(effects, effects_bounds(orb, self.diameter_dip))?;
            apply_work_area_region(effects, work_area, false)?;
            if actual_visibility_hwnd(hwnd) && !actual_visibility_hwnd(effects) {
                show_without_activation_hwnd(effects)?;
                self.restack()?;
            }
        }
        Ok(())
    }

    fn sync_geometry(&self) -> Result<(), String> {
        let orb = window_bounds(hwnd_from_raw(self.orb_hwnd))?;
        if !self.docked.get() && self.region_size.get() != (orb.width, orb.height) {
            self.apply_circular_region()?;
        }
        if let Some(hwnd) = self.effects_hwnd {
            set_bounds_noactivate(hwnd_from_raw(hwnd), effects_bounds(orb, self.diameter_dip))?;
        }
        Ok(())
    }

    pub fn orb_origin(&self) -> Result<PhysicalPosition<i32>, String> {
        let hwnd = hwnd_from_raw(self.orb_hwnd);
        let mut rect = RECT::default();
        unsafe { GetWindowRect(hwnd, &mut rect) }.map_err(|error| error.to_string())?;
        Ok(PhysicalPosition::new(rect.left, rect.top))
    }

    pub fn orb_window(&self) -> WebviewWindow {
        self.orb.clone()
    }

    pub fn apply_circular_region(&self) -> Result<(), String> {
        let hwnd = hwnd_from_raw(self.orb_hwnd);
        apply_elliptic_region(hwnd)?;
        let bounds = window_bounds(hwnd)?;
        self.region_size.set((bounds.width, bounds.height));
        Ok(())
    }

    /// Clip the parked sphere to this monitor's work area, including at a
    /// shared monitor boundary. The hidden portion must not steal clicks.
    pub fn set_docked_clip(&self, bounds: Option<super::super::placement::PixelRect>) -> Result<(), String> {
        self.docked.set(bounds.is_some());
        let Some(bounds) = bounds else {
            self.apply_circular_region()?;
            if let Some(hwnd) = self.effects_hwnd {
                unsafe { SetWindowRgn(hwnd_from_raw(hwnd), None, true) };
            }
            return self.restore_effects_follow();
        };
        if let Some(hwnd) = self.effects_hwnd { hide_hwnd(hwnd_from_raw(hwnd)); }
        apply_work_area_region(hwnd_from_raw(self.orb_hwnd), bounds, true)
    }

    pub fn begin_native_drag(orb: &WebviewWindow) -> Result<(), String> {
        if orb.label() != ORB_LABEL {
            return Err("native drag is only allowed for the orb window".into());
        }
        orb.start_dragging().map_err(|error| error.to_string())
    }

    pub fn ensure_details(&self) -> Result<WebviewWindow, String> {
        let app = self.orb.app_handle();
        if let Some(existing) = app.get_webview_window(DETAILS_LABEL) {
            return Ok(existing);
        }
        let details = create_shell(
            app,
            DETAILS_LABEL,
            "orb-details",
            DETAILS_MIN_WIDTH_DIP,
            DETAILS_MIN_HEIGHT_DIP,
            ShellKind::Details,
            self.generation,
        )?;
        self.details_hwnd.set(Some(capture_hwnd(&details)?));
        let _ = self.restack();
        Ok(details)
    }

    pub fn show_details_without_activation(&self) -> Result<(), String> {
        let hwnd = self
            .details_hwnd
            .get()
            .ok_or_else(|| "orb-details window is not created".to_string())?;
        show_without_activation_hwnd(hwnd_from_raw(hwnd))?;
        self.restack()?;
        if !actual_visibility_hwnd(hwnd_from_raw(hwnd)) {
            return Err("orb-details is not visible after show".into());
        }
        Ok(())
    }

    fn restack(&self) -> Result<(), String> {
        let flags = SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE;
        if let Some(hwnd) = self.effects_hwnd {
            unsafe { SetWindowPos(hwnd_from_raw(hwnd), Some(HWND_TOPMOST), 0, 0, 0, 0, flags) }
                .map_err(|error| error.to_string())?;
        }
        unsafe { SetWindowPos(hwnd_from_raw(self.orb_hwnd), Some(HWND_TOPMOST), 0, 0, 0, 0, flags) }
            .map_err(|error| error.to_string())?;
        if let Some(hwnd) = self.details_hwnd.get() {
            unsafe { SetWindowPos(hwnd_from_raw(hwnd), Some(HWND_TOPMOST), 0, 0, 0, 0, flags) }
                .map_err(|error| error.to_string())?;
        }
        Ok(())
    }
}

pub fn is_foreground_fullscreen_at(app: &AppHandle, origin: PhysicalPosition<i32>) -> bool {
    let Some((left, top, right, bottom)) = monitor_bounds_containing(origin) else {
        return false;
    };
    is_foreground_fullscreen_bounds(app, left, top, right, bottom)
}

pub fn is_foreground_fullscreen_excluding_self(app: &AppHandle, monitor: &Monitor) -> bool {
    let left = monitor.position().x;
    let top = monitor.position().y;
    let right = left.saturating_add(monitor.size().width as i32);
    let bottom = top.saturating_add(monitor.size().height as i32);
    is_foreground_fullscreen_bounds(app, left, top, right, bottom)
}

fn monitor_bounds_containing(origin: PhysicalPosition<i32>) -> Option<(i32, i32, i32, i32)> {
    let point = POINT {
        x: origin.x,
        y: origin.y,
    };
    let monitor = unsafe { MonitorFromPoint(point, MONITOR_DEFAULTTONEAREST) };
    if monitor.is_invalid() {
        return None;
    }
    let mut info = MONITORINFO {
        cbSize: std::mem::size_of::<MONITORINFO>() as u32,
        ..Default::default()
    };
    if !unsafe { GetMonitorInfoW(monitor, &mut info) }.as_bool() {
        return None;
    }
    Some((
        info.rcMonitor.left,
        info.rcMonitor.top,
        info.rcMonitor.right,
        info.rcMonitor.bottom,
    ))
}

fn is_foreground_fullscreen_bounds(
    app: &AppHandle,
    mon_left: i32,
    mon_top: i32,
    mon_right: i32,
    mon_bottom: i32,
) -> bool {
    let foreground = unsafe { GetForegroundWindow() };
    if foreground.is_invalid() || !unsafe { IsWindow(Some(foreground)) }.as_bool() {
        return false;
    }
    if !unsafe { IsWindowVisible(foreground) }.as_bool()
        || unsafe { IsIconic(foreground) }.as_bool()
    {
        return false;
    }
    if is_current_process_window(foreground) {
        return false;
    }
    let _ = app;
    if is_desktop_or_taskbar(foreground) {
        return false;
    }

    let mut rect = RECT::default();
    if unsafe { GetWindowRect(foreground, &mut rect) }.is_err() {
        return false;
    }
    let (center_x, center_y) = rect_center(rect);
    if center_x < mon_left || center_x >= mon_right || center_y < mon_top || center_y >= mon_bottom
    {
        return false;
    }

    if !covers_bounds(
        rect,
        mon_left,
        mon_top,
        mon_right,
        mon_bottom,
        FULLSCREEN_TOLERANCE_PX,
    ) {
        return false;
    }

    // Ordinary maximized windows fill the work area, not the monitor, and keep WS_MAXIMIZE.
    let style = unsafe { GetWindowLongPtrW(foreground, GWL_STYLE) } as u32;
    if style & WS_MAXIMIZE.0 != 0 {
        return false;
    }
    true
}

fn create_shell(
    app: &AppHandle,
    label: &str,
    view: &str,
    width_dip: f64,
    height_dip: f64,
    kind: ShellKind,
    generation: u64,
) -> Result<WebviewWindow, String> {
    let focusable = !matches!(kind, ShellKind::Effects);
    let ignore_cursor = matches!(kind, ShellKind::Effects);
    let title = match kind {
        ShellKind::Orb => "TokenDance Orb",
        ShellKind::Effects => "TokenDance Orb Effects",
        ShellKind::Details => "TokenDance Orb Details",
    };
    let window = WebviewWindowBuilder::new(
        app,
        label,
        WebviewUrl::App(format!("index.html?view={view}&generation={generation}&mode=full").into()),
    )
    .title(title)
    .decorations(false)
    .resizable(false)
    .transparent(true)
    .shadow(false)
    .always_on_top(true)
    .skip_taskbar(true)
    .visible(false)
    .focused(false)
    .focusable(focusable)
    .inner_size(width_dip, height_dip)
    .background_color(tauri::window::Color(0, 0, 0, 0))
    .build()
    .map_err(|error| error.to_string())?;

    let _ = window.set_shadow(false);
    if ignore_cursor {
        window
            .set_ignore_cursor_events(true)
            .map_err(|error| error.to_string())?;
        let _ = window.set_focusable(false);
    }
    Ok(window)
}

fn destroy_labeled(app: &AppHandle, label: &str) {
    if let Some(window) = app.get_webview_window(label) {
        let _ = window.destroy();
    }
}

fn capture_hwnd(window: &WebviewWindow) -> Result<isize, String> {
    window
        .hwnd()
        .map(|hwnd| hwnd.0 as isize)
        .map_err(|error| error.to_string())
}

fn hwnd_from_raw(raw: isize) -> HWND {
    HWND(raw as *mut _)
}

fn actual_visibility_hwnd(hwnd: HWND) -> bool {
    unsafe { IsWindowVisible(hwnd) }.as_bool()
}

fn show_without_activation_hwnd(hwnd: HWND) -> Result<(), String> {
    unsafe {
        let _ = ShowWindow(hwnd, SW_SHOWNOACTIVATE);
        SetWindowPos(
            hwnd,
            Some(HWND_TOPMOST),
            0,
            0,
            0,
            0,
            SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE | SWP_SHOWWINDOW,
        )
    }
    .map_err(|error| error.to_string())
}

fn hide_hwnd(hwnd: HWND) {
    unsafe {
        let _ = ShowWindow(hwnd, SW_HIDE);
    }
}

fn window_bounds(hwnd: HWND) -> Result<PixelRect, String> {
    let mut rect = RECT::default();
    unsafe { GetWindowRect(hwnd, &mut rect) }.map_err(|error| error.to_string())?;
    Ok(PixelRect { x: rect.left, y: rect.top, width: rect.right - rect.left, height: rect.bottom - rect.top })
}

fn apply_work_area_region(hwnd: HWND, work_area: PixelRect, circular: bool) -> Result<(), String> {
    let rect = window_bounds(hwnd)?;
    let shape = unsafe {
        if circular { CreateEllipticRgn(0, 0, rect.width, rect.height) }
        else { CreateRectRgn(0, 0, rect.width, rect.height) }
    };
    let clip = unsafe { CreateRectRgn(work_area.x-rect.x, work_area.y-rect.y, work_area.right()-rect.x, work_area.bottom()-rect.y) };
    if shape.is_invalid() || clip.is_invalid() {
        unsafe { let _ = DeleteObject(shape.into()); let _ = DeleteObject(clip.into()); }
        return Err("edge region allocation failed".into());
    }
    let combined = unsafe { CombineRgn(Some(shape), Some(shape), Some(clip), RGN_AND) };
    unsafe { let _ = DeleteObject(clip.into()); }
    if combined.0 == 0 || unsafe { SetWindowRgn(hwnd, Some(shape), true) } == 0 {
        unsafe { let _ = DeleteObject(shape.into()); }
        return Err("edge region update failed".into());
    }
    Ok(())
}

fn set_bounds_noactivate(hwnd: HWND, bounds: PixelRect) -> Result<(), String> {
    let previous = window_bounds(hwnd)?;
    if previous == bounds { return Ok(()); }
    let flags = move_flags(previous,bounds);
    unsafe {
        SetWindowPos(
            hwnd,
            None,
            bounds.x,
            bounds.y,
            bounds.width,
            bounds.height,
            flags,
        )
    }
    .map_err(|error| error.to_string())
}

fn move_flags(previous: PixelRect, next: PixelRect) -> windows::Win32::UI::WindowsAndMessaging::SET_WINDOW_POS_FLAGS {
    // Preserve WebView2's composed surface during translation. Discarding its
    // pixels or sending a resize on every frame can leave the final frame blank.
    let sizing = if (previous.width,previous.height)==(next.width,next.height) { SWP_NOSIZE } else { SWP_NOCOPYBITS };
    SWP_NOZORDER | SWP_NOACTIVATE | sizing
}

fn apply_elliptic_region(hwnd: HWND) -> Result<(), String> {
    // Clip the top-level surface only. Its region already clips descendants;
    // separate child ellipses become stale when WebView2 changes its viewport.
    let mut rect = RECT::default();
    unsafe { GetWindowRect(hwnd, &mut rect) }.map_err(|error| error.to_string())?;
    let width = rect.right.saturating_sub(rect.left);
    let height = rect.bottom.saturating_sub(rect.top);
    if width <= 0 || height <= 0 {
        return Err("window has no physical size for circular region".into());
    }
    let hrgn = unsafe { CreateEllipticRgn(0, 0, width, height) };
    if hrgn.is_invalid() {
        return Err("CreateEllipticRgn failed".into());
    }
    // SetWindowRgn takes ownership of HRGN on success; free only on failure.
    let ok = unsafe { SetWindowRgn(hwnd, Some(hrgn), true) };
    if ok == 0 {
        let _ = unsafe { DeleteObject(hrgn.into()) };
        return Err("SetWindowRgn failed".into());
    }
    Ok(())
}

fn is_current_process_window(hwnd: HWND) -> bool {
    let mut pid = 0u32;
    unsafe { GetWindowThreadProcessId(hwnd, Some(&mut pid)) };
    pid == std::process::id()
}

fn is_desktop_or_taskbar(hwnd: HWND) -> bool {
    if hwnd == unsafe { GetDesktopWindow() } || hwnd == unsafe { GetShellWindow() } {
        return true;
    }
    matches!(
        class_name(hwnd).as_str(),
        "Progman"
            | "WorkerW"
            | "Shell_TrayWnd"
            | "Shell_SecondaryTrayWnd"
            | "NotifyIconOverflowWindow"
    )
}

fn class_name(hwnd: HWND) -> String {
    let mut buf = [0u16; 256];
    let len = unsafe { GetClassNameW(hwnd, &mut buf) };
    if len <= 0 {
        String::new()
    } else {
        String::from_utf16_lossy(&buf[..len as usize])
    }
}

fn rect_center(rect: RECT) -> (i32, i32) {
    (
        ((rect.left as i64 + rect.right as i64) / 2) as i32,
        ((rect.top as i64 + rect.bottom as i64) / 2) as i32,
    )
}

fn covers_bounds(rect: RECT, left: i32, top: i32, right: i32, bottom: i32, tol: i32) -> bool {
    rect.left <= left.saturating_add(tol)
        && rect.top <= top.saturating_add(tol)
        && rect.right >= right.saturating_sub(tol)
        && rect.bottom >= bottom.saturating_sub(tol)
}

#[cfg(test)]
mod tests {
    use super::*;
    use windows::core::w;
    use windows::Win32::Graphics::Gdi::{GetWindowRgn, PtInRegion};
    use windows::Win32::UI::WindowsAndMessaging::{CreateWindowExW, DestroyWindow, WINDOW_EX_STYLE, WS_POPUP};

    struct HiddenWindow(HWND);
    impl HiddenWindow {
        fn new() -> Self {
            // No WS_VISIBLE: native regression coverage never opens a test UI.
            Self(unsafe { CreateWindowExW(WINDOW_EX_STYLE::default(), w!("STATIC"), w!("orb geometry test"),
                WS_POPUP, 0, 0, 112, 112, None, None, None, None) }.unwrap())
        }
    }
    impl Drop for HiddenWindow {
        fn drop(&mut self) { unsafe { let _ = DestroyWindow(self.0); } }
    }

    #[test]
    fn translation_preserves_surface_and_dpi_resize_repaints() {
        let rect = PixelRect{x:0,y:0,width:168,height:168};
        let moved = move_flags(rect,PixelRect{x:3000,y:-400,..rect});
        assert!(moved.contains(SWP_NOSIZE));assert!(!moved.contains(SWP_NOCOPYBITS));
        let resized = move_flags(rect,PixelRect{width:224,height:224,..rect});
        assert!(!resized.contains(SWP_NOSIZE));assert!(resized.contains(SWP_NOCOPYBITS));
    }

    #[test]
    fn native_move_resize_and_clip_restore_preserve_the_complete_circle() {
        let orb = HiddenWindow::new();
        let effects = HiddenWindow::new();
        for size in [112, 168, 224, 140, 112] {
            let target = PixelRect { x: -500, y: 300, width: size, height: size };
            set_bounds_noactivate(orb.0, target).unwrap();
            assert_eq!(window_bounds(orb.0).unwrap(), target);
            let halo = effects_bounds(target, 112.0);
            set_bounds_noactivate(effects.0, halo).unwrap();
            assert_eq!(window_bounds(effects.0).unwrap(), halo);
            // Simulate a docked strip, then restore at the new physical size.
            let strip = unsafe { CreateRectRgn(0, 0, 20, size) };
            assert_ne!(unsafe { SetWindowRgn(orb.0, Some(strip), true) }, 0);
            apply_elliptic_region(orb.0).unwrap();
            let region = unsafe { CreateRectRgn(0, 0, 0, 0) };
            assert_ne!(unsafe { GetWindowRgn(orb.0, region) }.0, 0);
            assert!(unsafe { PtInRegion(region, size / 2, size / 2) }.as_bool());
            assert!(unsafe { PtInRegion(region, size - 2, size / 2) }.as_bool());
            assert!(!unsafe { PtInRegion(region, 0, 0) }.as_bool());
            unsafe { let _ = DeleteObject(region.into()); }
            assert!(!actual_visibility_hwnd(orb.0));
            assert!(!actual_visibility_hwnd(effects.0));
        }
    }

    #[test]
    fn sliding_sphere_and_glow_never_draw_outside_the_work_area() {
        let orb = HiddenWindow::new();
        let effects = HiddenWindow::new();
        let area = PixelRect { x: -800, y: -200, width: 800, height: 700 };
        for (x, y) in [(-900, 100), (-30, 100), (-500, -300), (-500, 470)] {
            let rect = PixelRect { x, y, width: 168, height: 168 };
            for (hwnd, bounds, circular) in [(orb.0, rect, true), (effects.0, effects_bounds(rect, 112.0), false)] {
                set_bounds_noactivate(hwnd, bounds).unwrap();
                apply_work_area_region(hwnd, area, circular).unwrap();
                let region = unsafe { CreateRectRgn(0, 0, 0, 0) };
                assert_ne!(unsafe { GetWindowRgn(hwnd, region) }.0, 0);
                for local_y in (0..bounds.height).step_by(8) {
                    for local_x in (0..bounds.width).step_by(8) {
                        if unsafe { PtInRegion(region, local_x, local_y) }.as_bool() {
                            let px = bounds.x + local_x;
                            let py = bounds.y + local_y;
                            assert!(px >= area.x && px < area.right() && py >= area.y && py < area.bottom());
                        }
                    }
                }
                unsafe { let _ = DeleteObject(region.into()); }
                assert!(!actual_visibility_hwnd(hwnd));
            }
        }
    }
}
