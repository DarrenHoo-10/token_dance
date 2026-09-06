use super::model::{OrbPlacement, PlacementAnchor};
use tauri::PhysicalPosition;

pub const EFFECTS_GUTTER_DIP: f64 = 16.0;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct PixelRect {
    pub x: i32,
    pub y: i32,
    pub width: i32,
    pub height: i32,
}

impl PixelRect {
    pub fn right(self) -> i32 {
        self.x.saturating_add(self.width)
    }

    pub fn bottom(self) -> i32 {
        self.y.saturating_add(self.height)
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct MonitorInfo {
    pub key: String,
    pub is_primary: bool,
    pub origin_px: (i32, i32),
    pub work_area: PixelRect,
    pub bounds: PixelRect,
    pub scale: f64,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DetailsSide {
    Left,
    Right,
}

#[derive(Clone, Debug, PartialEq)]
pub struct OrbLayout {
    pub monitor_key: String,
    pub diameter_px: i32,
    pub orb: PixelRect,
    pub effects: PixelRect,
    pub center_px: (i32, i32),
    pub details_side: DetailsSide,
}

pub fn layout_orb(
    placement: &OrbPlacement,
    diameter_dip: u32,
    monitors: &[MonitorInfo],
) -> Option<OrbLayout> {
    let monitor = resolve_monitor(placement, monitors)?;
    let scale = if monitor.scale.is_finite() && monitor.scale > 0.0 {
        monitor.scale
    } else {
        1.0
    };
    let diameter_px = scale_dip(f64::from(diameter_dip), scale).max(1);
    let gutter_px = scale_dip(EFFECTS_GUTTER_DIP, scale).max(0);
    let edge_px = scale_dip(placement.edge_gap_dip.max(0.0), scale).max(gutter_px);
    let work = monitor.work_area;
    let vertical = placement.vertical_ratio.clamp(0.0, 1.0);
    let (min_x, max_x, min_y, max_y) = movable_range(work, diameter_px, gutter_px);
    let (orb_x, orb_y) = match placement.anchor {
        PlacementAnchor::Right => {
            let x = work.right().saturating_sub(edge_px).saturating_sub(diameter_px);
            (x, lerp_i32(min_y, max_y, vertical))
        }
        PlacementAnchor::Free => {
            let x_ratio = placement.x_ratio.unwrap_or(1.0).clamp(0.0, 1.0);
            let y_ratio = placement.y_ratio.unwrap_or(vertical).clamp(0.0, 1.0);
            (lerp_i32(min_x, max_x, x_ratio), lerp_i32(min_y, max_y, y_ratio))
        }
    };
    let orb_x = orb_x.clamp(min_x, max_x);
    let orb_y = orb_y.clamp(min_y, max_y);
    let orb = PixelRect {
        x: orb_x,
        y: orb_y,
        width: diameter_px,
        height: diameter_px,
    };
    let effects = PixelRect {
        x: orb_x.saturating_sub(gutter_px),
        y: orb_y.saturating_sub(gutter_px),
        width: diameter_px.saturating_add(gutter_px.saturating_mul(2)),
        height: diameter_px.saturating_add(gutter_px.saturating_mul(2)),
    };
    let center_x = orb_x.saturating_add(diameter_px / 2);
    let details_side = if i64::from(center_x) >= i64::from(work.x) + i64::from(work.width) / 2 {
        DetailsSide::Left
    } else {
        DetailsSide::Right
    };
    Some(OrbLayout {
        monitor_key: monitor.key.clone(),
        diameter_px,
        orb,
        effects,
        center_px: (center_x, orb_y.saturating_add(diameter_px / 2)),
        details_side,
    })
}

pub fn monitor_containing(monitors: &[MonitorInfo], x: i32, y: i32) -> Option<&MonitorInfo> {
    monitors
        .iter()
        .find(|monitor| {
            x >= monitor.bounds.x
                && y >= monitor.bounds.y
                && x < monitor.bounds.right()
                && y < monitor.bounds.bottom()
        })
        .or_else(|| monitors.iter().find(|monitor| monitor.is_primary))
        .or_else(|| monitors.first())
}

pub fn placement_from_origin(
    origin: PhysicalPosition<i32>,
    diameter_dip: u32,
    monitors: &[MonitorInfo],
    edge_gap_dip: f64,
) -> Option<OrbPlacement> {
    let center_x = origin.x.saturating_add(1);
    let center_y = origin.y.saturating_add(1);
    let monitor = monitor_containing(monitors, origin.x, origin.y)
        .or_else(|| monitor_containing(monitors, center_x, center_y))?;
    let scale = if monitor.scale.is_finite() && monitor.scale > 0.0 {
        monitor.scale
    } else {
        1.0
    };
    let diameter_px = scale_dip(f64::from(diameter_dip), scale).max(1);
    let gutter_px = scale_dip(EFFECTS_GUTTER_DIP, scale).max(0);
    let (min_x, max_x, min_y, max_y) = movable_range(monitor.work_area, diameter_px, gutter_px);
    let x_ratio = ratio(origin.x, min_x, max_x);
    let y_ratio = ratio(origin.y, min_y, max_y);
    Some(OrbPlacement {
        monitor_key: Some(monitor.key.clone()),
        anchor: PlacementAnchor::Free,
        edge_gap_dip,
        vertical_ratio: y_ratio,
        x_ratio: Some(x_ratio),
        y_ratio: Some(y_ratio),
    })
}

fn ratio(value: i32, min: i32, max: i32) -> f64 {
    if max <= min {
        return 0.0;
    }
    ((i64::from(value) - i64::from(min)) as f64 / (i64::from(max) - i64::from(min)) as f64).clamp(0.0, 1.0)
}

pub fn details_rect(
    layout: &OrbLayout,
    work: PixelRect,
    width: i32,
    height: i32,
    gap: i32,
) -> PixelRect {
    let x = match layout.details_side {
        DetailsSide::Left => layout.orb.x.saturating_sub(gap).saturating_sub(width),
        DetailsSide::Right => layout.orb.x.saturating_add(layout.orb.width).saturating_add(gap),
    };
    clamp_rect(
        PixelRect {
            x,
            y: layout.orb.y,
            width,
            height,
        },
        work,
    )
}

fn resolve_monitor<'a>(placement: &OrbPlacement, monitors: &'a [MonitorInfo]) -> Option<&'a MonitorInfo> {
    placement
        .monitor_key
        .as_ref()
        .and_then(|key| monitors.iter().find(|monitor| &monitor.key == key))
        .or_else(|| monitors.iter().find(|monitor| monitor.is_primary))
        .or_else(|| monitors.first())
}

fn scale_dip(dip: f64, scale: f64) -> i32 {
    let px = dip * scale;
    if !px.is_finite() {
        return 0;
    }
    px.round().clamp(i32::MIN as f64, i32::MAX as f64) as i32
}

fn movable_range(work: PixelRect, diameter: i32, gutter: i32) -> (i32, i32, i32, i32) {
    let min_x = work.x.saturating_add(gutter);
    let min_y = work.y.saturating_add(gutter);
    let max_x = work.right().saturating_sub(gutter).saturating_sub(diameter);
    let max_y = work.bottom().saturating_sub(gutter).saturating_sub(diameter);
    (
        min_x,
        if max_x < min_x { min_x } else { max_x },
        min_y,
        if max_y < min_y { min_y } else { max_y },
    )
}

fn lerp_i32(min: i32, max: i32, t: f64) -> i32 {
    if max <= min {
        return min;
    }
    let span = (i64::from(max) - i64::from(min)) as f64 * t.clamp(0.0, 1.0);
    min.saturating_add(span.round() as i32)
}

fn clamp_rect(mut rect: PixelRect, work: PixelRect) -> PixelRect {
    if rect.x < work.x {
        rect.x = work.x;
    }
    if rect.y < work.y {
        rect.y = work.y;
    }
    let max_x = work.right().saturating_sub(rect.width);
    let max_y = work.bottom().saturating_sub(rect.height);
    if rect.x > max_x {
        rect.x = max_x.max(work.x);
    }
    if rect.y > max_y {
        rect.y = max_y.max(work.y);
    }
    rect
}

#[cfg(test)]
mod tests {
    use super::*;

    fn monitor(key: &str, primary: bool, origin: (i32, i32), work: PixelRect, scale: f64) -> MonitorInfo {
        MonitorInfo {
            key: key.into(),
            is_primary: primary,
            origin_px: origin,
            bounds: PixelRect {
                x: origin.0,
                y: origin.1,
                width: work.width,
                height: work.height + 40,
            },
            work_area: work,
            scale,
        }
    }

    #[test]
    fn negative_origin_clamp_stays_signed() {
        let left = monitor(
            "left",
            false,
            (-1920, 0),
            PixelRect {
                x: -1920,
                y: 0,
                width: 1920,
                height: 1040,
            },
            1.0,
        );
        let primary = monitor(
            "primary",
            true,
            (0, 0),
            PixelRect {
                x: 0,
                y: 0,
                width: 1920,
                height: 1040,
            },
            1.0,
        );
        let placement = OrbPlacement {
            monitor_key: Some("left".into()),
            anchor: PlacementAnchor::Right,
            edge_gap_dip: 16.0,
            vertical_ratio: 0.5,
            x_ratio: None,
            y_ratio: None,
        };
        let layout = layout_orb(&placement, 112, &[left.clone(), primary.clone()]).unwrap();
        assert!(layout.orb.x < 0);
        let restored = placement_from_origin(
            PhysicalPosition::new(layout.orb.x, layout.orb.y),
            112,
            &[left, primary],
            16.0,
        )
        .unwrap();
        assert_eq!(restored.monitor_key.as_deref(), Some("left"));
        assert_eq!(restored.anchor, PlacementAnchor::Free);
        assert!(layout.orb.x < 0, "{}", layout.orb.x);
        assert!(layout.effects.x < 0);
        assert!(layout.orb.x >= -1920);
        assert!(layout.effects.x >= -1920);
        assert_eq!(layout.effects.width, 144);
        assert_eq!(layout.details_side, DetailsSide::Left);
        assert_eq!(layout.orb.x, layout.effects.x + 16);
    }

    #[test]
    fn mixed_dpi_converts_dip_to_physical_pixels() {
        let primary = monitor(
            "p",
            true,
            (0, 0),
            PixelRect {
                x: 0,
                y: 0,
                width: 1920,
                height: 1080,
            },
            1.0,
        );
        let retina = monitor(
            "r",
            false,
            (1920, 0),
            PixelRect {
                x: 1920,
                y: 0,
                width: 2560,
                height: 1440,
            },
            2.0,
        );
        let placement = OrbPlacement {
            monitor_key: Some("r".into()),
            anchor: PlacementAnchor::Right,
            edge_gap_dip: 16.0,
            vertical_ratio: 0.25,
            x_ratio: None,
            y_ratio: None,
        };
        let layout = layout_orb(&placement, 112, &[primary, retina]).unwrap();
        assert_eq!(layout.diameter_px, 224);
        assert_eq!(layout.orb.width, 224);
        assert_eq!(layout.effects.width, 288);
        assert_eq!(layout.orb.x, layout.effects.x + 32);
        assert!(layout.orb.x >= 1920);
    }

    #[test]
    fn tiny_work_area_keeps_full_orb_and_gutter() {
        let tiny = monitor(
            "tiny",
            true,
            (0, 0),
            PixelRect {
                x: 0,
                y: 0,
                width: 50,
                height: 50,
            },
            1.0,
        );
        let layout = layout_orb(&OrbPlacement::default(), 112, &[tiny]).unwrap();
        assert_eq!(layout.orb.width, 112);
        assert_eq!(layout.orb.height, 112);
        assert_eq!(layout.effects.width, 144);
        assert_eq!(layout.effects.height, 144);
        assert_eq!(layout.orb.x, layout.effects.x + 16);
        assert_eq!(layout.orb.y, layout.effects.y + 16);
    }

    #[test]
    fn missing_monitor_falls_back_to_primary_keeping_vertical_ratio() {
        let primary = monitor(
            "p",
            true,
            (0, 0),
            PixelRect {
                x: 0,
                y: 0,
                width: 1000,
                height: 1000,
            },
            1.0,
        );
        let placement = OrbPlacement {
            monitor_key: Some("gone".into()),
            anchor: PlacementAnchor::Right,
            edge_gap_dip: 16.0,
            vertical_ratio: 0.0,
            x_ratio: None,
            y_ratio: None,
        };
        let layout = layout_orb(&placement, 112, &[primary]).unwrap();
        assert_eq!(layout.monitor_key, "p");
        assert_eq!(layout.orb.y, 16);
    }
}
