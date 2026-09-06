//! Cross-platform orb window-group contract.
//!
//! HWND/HRGN work stays in `windows.rs`. Non-Windows backends return `暂不可用`.

use std::sync::atomic::{AtomicU64, Ordering};

use tauri::PhysicalPosition;

#[cfg(not(windows))]
mod unsupported;
#[cfg(windows)]
mod windows;

#[cfg(not(windows))]
pub use unsupported::OrbWindowGroup;
#[cfg(windows)]
pub use windows::OrbWindowGroup;

#[cfg(not(windows))]
pub use unsupported::{is_foreground_fullscreen_at, is_foreground_fullscreen_excluding_self};
#[cfg(windows)]
pub use windows::{is_foreground_fullscreen_at, is_foreground_fullscreen_excluding_self};

pub const ORB_LABEL: &str = "orb";
pub const EFFECTS_LABEL: &str = "orb-effects";
pub const DETAILS_LABEL: &str = "orb-details";

pub const DEFAULT_DIAMETER_DIP: f64 = 112.0;
pub const EFFECTS_INSET_DIP: f64 = 16.0;
pub const DETAILS_MIN_WIDTH_DIP: f64 = 344.0;
pub const DETAILS_MIN_HEIGHT_DIP: f64 = 420.0;
pub const PEEK_WIDTH_DIP: f64 = 280.0;
pub const PEEK_HEIGHT_DIP: f64 = 96.0;

pub const UNSUPPORTED_PLATFORM_ERROR: &str = "暂不可用";

static NEXT_GENERATION: AtomicU64 = AtomicU64::new(1);

/// Frontend `ready(label, generation)` payload. IPC is wired by the integrator.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OrbReady {
    pub label: String,
    pub generation: u64,
}

pub fn next_instance_generation() -> u64 {
    NEXT_GENERATION.fetch_add(1, Ordering::Relaxed)
}

pub fn unsupported_platform_create<T>() -> Result<T, String> {
    Err(UNSUPPORTED_PLATFORM_ERROR.into())
}

pub fn dip_to_physical(dip: f64, scale: f64) -> i32 {
    (dip * scale).round() as i32
}

pub fn effects_size_dip(diameter_dip: f64) -> f64 {
    diameter_dip + (EFFECTS_INSET_DIP * 2.0)
}

/// Derive the companion surface from the sphere's actual physical rectangle.
/// The two HWNDs can report different DPI while crossing a monitor boundary.
pub fn effects_bounds(
    orb: super::placement::PixelRect,
    diameter_dip: f64,
) -> super::placement::PixelRect {
    let inset = (EFFECTS_INSET_DIP * orb.width as f64 / diameter_dip).round() as i32;
    super::placement::PixelRect {
        x: orb.x - inset,
        y: orb.y - inset,
        width: orb.width + inset * 2,
        height: orb.height + inset * 2,
    }
}

/// Effects origin is orb origin plus `-16 DIP` at the **same** scale.
/// Do not convert orb and effects logical points independently.
pub fn effects_origin(orb_origin: PhysicalPosition<i32>, scale: f64) -> PhysicalPosition<i32> {
    let inset = dip_to_physical(-EFFECTS_INSET_DIP, scale);
    PhysicalPosition::new(orb_origin.x + inset, orb_origin.y + inset)
}

pub fn orb_origin_from_center(
    center: PhysicalPosition<i32>,
    diameter_dip: f64,
    scale: f64,
) -> PhysicalPosition<i32> {
    let size = dip_to_physical(diameter_dip, scale);
    PhysicalPosition::new(center.x - size / 2, center.y - size / 2)
}

pub fn accept_instance_ready(
    current_generation: u64,
    labels: &[&str],
    ready: &OrbReady,
) -> Result<(), String> {
    if ready.generation != current_generation {
        return Err(format!(
            "stale orb ready for {}: generation {} != {}",
            ready.label, ready.generation, current_generation
        ));
    }
    if !labels.iter().any(|label| *label == ready.label) {
        return Err(format!("unexpected orb window label: {}", ready.label));
    }
    Ok(())
}

impl OrbWindowGroup {
    pub fn accept_ready(&self, ready: &OrbReady) -> Result<(), String> {
        accept_instance_ready(self.generation(), &self.labels(), ready)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn effects_remain_concentric_at_every_scale_and_screen_origin() {
        for diameter in [112.0, 128.0, 160.0] {
            for scale in [1.0, 1.25, 1.5, 1.75, 2.0] {
                for (x, y) in [(100, 200), (-1920, -500), (3800, 1200)] {
                    let size = dip_to_physical(diameter, scale);
                    let orb = super::super::placement::PixelRect { x, y, width: size, height: size };
                    let effects = effects_bounds(orb, diameter);
                    assert_eq!(effects.x * 2 + effects.width, orb.x * 2 + orb.width);
                    assert_eq!(effects.y * 2 + effects.height, orb.y * 2 + orb.height);
                    assert_eq!(effects.width, effects.height);
                    assert_eq!(orb.x - effects.x, effects.right() - orb.right());
                }
            }
        }
    }

    #[test]
    fn effects_origin_uses_same_scale_without_independent_rounding() {
        let scale = 1.25;
        let orb_logical = 0.4;
        let orb_physical = dip_to_physical(orb_logical, scale);
        assert_eq!(orb_physical, 1);

        let independent = dip_to_physical(orb_logical - EFFECTS_INSET_DIP, scale);
        let same_scale = orb_physical + dip_to_physical(-EFFECTS_INSET_DIP, scale);
        assert_eq!(independent, -20);
        assert_eq!(same_scale, -19);
        assert_ne!(
            independent, same_scale,
            "independent logical rounding must be able to diverge"
        );

        let orb_origin = PhysicalPosition::new(orb_physical, 200);
        assert_eq!(
            effects_origin(orb_origin, scale),
            PhysicalPosition::new(same_scale, 200 + dip_to_physical(-EFFECTS_INSET_DIP, scale))
        );
        assert_eq!(effects_size_dip(DEFAULT_DIAMETER_DIP), 144.0);
    }

    #[test]
    fn generation_rejects_stale_ready() {
        let current = next_instance_generation();
        let labels = [ORB_LABEL, EFFECTS_LABEL];
        assert!(accept_instance_ready(
            current,
            &labels,
            &OrbReady {
                label: ORB_LABEL.into(),
                generation: current,
            }
        )
        .is_ok());
        assert!(accept_instance_ready(
            current,
            &labels,
            &OrbReady {
                label: ORB_LABEL.into(),
                generation: current.saturating_sub(1),
            }
        )
        .is_err());
        assert!(accept_instance_ready(
            current,
            &labels,
            &OrbReady {
                label: "main".into(),
                generation: current,
            }
        )
        .is_err());
    }

    #[test]
    fn unsupported_platform_create_returns_clear_error_without_panic() {
        let result = std::panic::catch_unwind(unsupported_platform_create::<()>);
        let inner = result.expect("unsupported create must not panic");
        assert_eq!(inner.unwrap_err(), "暂不可用");
    }
}
