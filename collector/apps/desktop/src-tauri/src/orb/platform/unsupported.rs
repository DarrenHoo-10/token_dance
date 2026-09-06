//! Non-Windows stub: the orb native window layer is unavailable.

use tauri::{AppHandle, Monitor, PhysicalPosition, WebviewWindow};

use super::{
    next_instance_generation, unsupported_platform_create, DETAILS_LABEL, EFFECTS_LABEL, ORB_LABEL,
    UNSUPPORTED_PLATFORM_ERROR,
};

pub struct OrbWindowGroup {
    generation: u64,
}

impl OrbWindowGroup {
    pub fn create(_app: &AppHandle, _diameter_dip: f64, _effects: bool) -> Result<Self, String> {
        let _ = next_instance_generation();
        unsupported_platform_create()
    }

    pub fn generation(&self) -> u64 {
        self.generation
    }

    pub fn labels(&self) -> Vec<&'static str> {
        vec![ORB_LABEL, EFFECTS_LABEL, DETAILS_LABEL]
    }

    pub fn show_without_activation(&self) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn hide(&self) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn destroy(&self) {}

    pub fn move_orb_center(&self, _physical: PhysicalPosition<i32>) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn restore_effects_follow(&self) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn settle_motion(&self) -> Result<(), String> { Err(UNSUPPORTED_PLATFORM_ERROR.into()) }

    pub fn move_edge_frame(&self, _orb: super::super::placement::PixelRect, _work_area: super::super::placement::PixelRect) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn orb_origin(&self) -> Result<PhysicalPosition<i32>, String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn orb_window(&self) -> WebviewWindow {
        unreachable!("orb windows are not created on this platform")
    }

    pub fn apply_circular_region(&self) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn set_docked_clip(&self, _bounds: Option<super::super::placement::PixelRect>) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn begin_native_drag(_orb: &WebviewWindow) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn ensure_details(&self) -> Result<WebviewWindow, String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }

    pub fn show_details_without_activation(&self) -> Result<(), String> {
        Err(UNSUPPORTED_PLATFORM_ERROR.into())
    }
}

pub fn is_foreground_fullscreen_excluding_self(_app: &AppHandle, _monitor: &Monitor) -> bool {
    false
}

pub fn is_foreground_fullscreen_at(_app: &AppHandle, _origin: PhysicalPosition<i32>) -> bool {
    false
}
