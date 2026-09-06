//! Deterministic physical-pixel motion. No window calls or wall-clock reads.
use super::placement::PixelRect;

const FRICTION_PER_SECOND: f64 = 1.0;
const PULL_SPEED_GAIN: f64 = 13.75;
const BOUNCE_RETENTION: f64 = 0.74;

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Edge { Left, Right, Top, Bottom }

#[derive(Clone, Debug)]
pub struct Ball {
    pub x: f64,
    pub y: f64,
    pub vx: f64,
    pub vy: f64,
    pub diameter: f64,
    pub scale: f64,
    pub bounds: PixelRect,
}

/// Drag a parked ball from its actual (partly off-screen) position. The anchor
/// and monitor remain fixed until the complete sphere and halo are on-screen.
pub struct EdgeDrag {
    anchor: Ball,
    edge: Edge,
    pub current: Ball,
}

impl EdgeDrag {
    pub fn new(anchor: Ball) -> Self {
        let edge = [(anchor.x-anchor.bounds.x as f64, Edge::Left),
            (anchor.max_x()-anchor.x, Edge::Right),
            (anchor.y-anchor.bounds.y as f64, Edge::Top),
            (anchor.max_y()-anchor.y, Edge::Bottom)]
            .into_iter().min_by(|a,b| a.0.total_cmp(&b.0)).unwrap().1;
        Self { current: anchor.clone(), anchor, edge }
    }

    /// Returns true when ordinary dragging can take over without a visual jump.
    pub fn update(&mut self, dx: f64, dy: f64) -> Result<bool, String> {
        if !dx.is_finite() || !dy.is_finite() { return Err("invalid edge drag".into()); }
        let b = &mut self.current;
        b.x = self.anchor.x + dx.clamp(-100000.0,100000.0) * b.scale;
        b.y = self.anchor.y + dy.clamp(-100000.0,100000.0) * b.scale;
        let peek = (20.0*b.scale).min(b.diameter/2.0);
        match self.edge {
            Edge::Left | Edge::Right => {
                b.x = b.x.clamp(b.bounds.x as f64-b.diameter+peek,b.bounds.right() as f64-peek);
                b.y = b.y.clamp(b.bounds.y as f64,b.max_y());
            }
            Edge::Top | Edge::Bottom => {
                b.x = b.x.clamp(b.bounds.x as f64,b.max_x());
                b.y = b.y.clamp(b.bounds.y as f64-b.diameter+peek,b.bounds.bottom() as f64-peek);
            }
        }
        let gutter = 16.0*b.scale;
        Ok(b.x>=b.bounds.x as f64+gutter && b.x+b.diameter<=b.bounds.right() as f64-gutter
            && b.y>=b.bounds.y as f64+gutter && b.y+b.diameter<=b.bounds.bottom() as f64-gutter)
    }
}

/// A short edge slide uses elapsed time, so missed frames never extend it.
#[derive(Clone, Debug)]
pub struct EdgeSlide {
    from: Ball,
    pub target: Ball,
    pub opening: bool,
    elapsed: f64,
}

impl EdgeSlide {
    pub fn new(from: Ball, target: Ball, opening: bool) -> Self {
        Self { from, target, opening, elapsed: 0.0 }
    }

    pub fn step(&mut self, dt: f64) -> (Ball, bool) {
        if dt.is_finite() { self.elapsed += dt.max(0.0); }
        let t = (self.elapsed / 0.24).clamp(0.0, 1.0);
        let eased = 1.0 - (1.0 - t).powi(3);
        let mut ball = self.target.clone();
        ball.x = self.from.x + (self.target.x - self.from.x) * eased;
        ball.y = self.from.y + (self.target.y - self.from.y) * eased;
        ball.vx = 0.0; ball.vy = 0.0;
        (ball, t == 1.0)
    }

    pub fn visible_placement(&self) -> &Ball {
        if self.opening { &self.target } else { &self.from }
    }
}

impl Ball {
    pub fn launch(&mut self, dx: f64, dy: f64) -> Result<(), String> {
        let length = dx.hypot(dy);
        if !length.is_finite() { return Err("invalid launch vector".into()); }
        self.vx = 0.0; self.vy = 0.0;
        if length <= 4.0 { return Ok(()); }
        let speed = length * PULL_SPEED_GAIN * self.scale;
        if !speed.is_finite() { return Err("invalid launch speed".into()); }
        self.vx = -dx / length * speed;
        self.vy = -dy / length * speed;
        Ok(())
    }

    /// Called on a copy of the initial ball; pulling never parks it off-screen.
    pub fn pull(&mut self, dx: f64, dy: f64) -> Result<(), String> {
        let length = dx.hypot(dy);
        if !length.is_finite() { return Err("invalid pull vector".into()); }
        let x = self.x + dx * self.scale;
        let y = self.y + dy * self.scale;
        if !x.is_finite() || !y.is_finite() { return Err("invalid pull position".into()); }
        self.x = x;
        self.y = y;
        self.clamp_inside();
        Ok(())
    }

    pub fn clamp_inside(&mut self) {
        self.x = self.x.clamp(self.bounds.x as f64, self.max_x());
        self.y = self.y.clamp(self.bounds.y as f64, self.max_y());
    }
    fn max_x(&self) -> f64 { (self.bounds.right() as f64 - self.diameter).max(self.bounds.x as f64) }
    fn max_y(&self) -> f64 { (self.bounds.bottom() as f64 - self.diameter).max(self.bounds.y as f64) }
    pub fn speed(&self) -> f64 { self.vx.hypot(self.vy) }
    pub fn step(&mut self, dt: f64) {
        let dt = if dt.is_finite() { dt.clamp(0.0, 0.05) } else { 0.0 };
        // Exact exponential drag integration; substeps prevent tunnelling.
        let steps = (dt / 0.008).ceil().max(1.0) as usize;
        let h = dt / steps as f64;
        for _ in 0..steps {
            let decay = (-FRICTION_PER_SECOND * h).exp();
            self.x += self.vx * (1.0 - decay) / FRICTION_PER_SECOND;
            self.y += self.vy * (1.0 - decay) / FRICTION_PER_SECOND;
            self.vx *= decay; self.vy *= decay;
            // Long pulls can cross several walls in one frame. Reflect the
            // overshoot as well as velocity, keeping the whole ball visible.
            let (max_x,max_y) = (self.max_x(),self.max_y());
            reflect_axis(&mut self.x,&mut self.vx,self.bounds.x as f64,max_x);
            reflect_axis(&mut self.y,&mut self.vy,self.bounds.y as f64,max_y);
        }
        if self.speed() < 25.0 * self.scale { self.vx = 0.0; self.vy = 0.0; }
    }
    pub fn near_edge(&self) -> Option<Edge> {
        let candidates = [(self.x - self.bounds.x as f64, Edge::Left), (self.max_x() - self.x, Edge::Right),
            (self.y - self.bounds.y as f64, Edge::Top), (self.max_y() - self.y, Edge::Bottom)];
        candidates.into_iter().min_by(|a,b| a.0.total_cmp(&b.0)).filter(|v| v.0 <= 18.0 * self.scale).map(|v| v.1)
    }
    pub fn dock(&mut self, edge: Edge) {
        self.clamp_inside();
        let peek = (20.0 * self.scale).min(self.diameter / 2.0);
        match edge {
            Edge::Left => self.x = self.bounds.x as f64 - self.diameter + peek,
            Edge::Right => self.x = self.bounds.right() as f64 - peek,
            Edge::Top => self.y = self.bounds.y as f64 - self.diameter + peek,
            Edge::Bottom => self.y = self.bounds.bottom() as f64 - peek,
        }
        self.vx = 0.0; self.vy = 0.0;
    }
    pub fn expand(&mut self) {
        self.clamp_inside();
        let gap = 24.0 * self.scale;
        self.x = self.x.clamp((self.bounds.x as f64 + gap).min(self.max_x()), (self.max_x() - gap).max(self.bounds.x as f64 + gap).min(self.max_x()));
        self.y = self.y.clamp((self.bounds.y as f64 + gap).min(self.max_y()), (self.max_y() - gap).max(self.bounds.y as f64 + gap).min(self.max_y()));
    }
}

fn reflect_axis(position: &mut f64, velocity: &mut f64, min: f64, max: f64) {
    if max <= min { *position = min; *velocity = 0.0; return; }
    while *position < min || *position > max {
        if *position < min {
            *position = min + (min - *position) * BOUNCE_RETENTION;
            *velocity = velocity.abs() * BOUNCE_RETENTION;
        } else {
            *position = max - (*position - max) * BOUNCE_RETENTION;
            *velocity = -velocity.abs() * BOUNCE_RETENTION;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn ball() -> Ball { Ball {x:500.0,y:300.0,vx:0.0,vy:0.0,diameter:112.0,scale:1.0,bounds:PixelRect{x:0,y:0,width:1920,height:1040}} }
    #[test] fn dragging_out_starts_at_the_parked_position_and_tracks_pointer_distance() {
        for scale in [1.0,1.25,1.5,2.0] {
            for (edge,dx,dy) in [(Edge::Left,1.0,0.0),(Edge::Right,-1.0,0.0),(Edge::Top,0.0,1.0),(Edge::Bottom,0.0,-1.0)] {
                let mut parked=ball();parked.scale=scale;parked.diameter=112.0*scale;
                parked.bounds=PixelRect{x:-2560,y:-200,width:2560,height:1440};
                parked.x=-1200.0;parked.y=300.0;parked.dock(edge);
                let mut drag=EdgeDrag::new(parked.clone());
                assert!(!drag.update(0.0,0.0).unwrap());
                assert_eq!((drag.current.x,drag.current.y),(parked.x,parked.y));
                assert!(!drag.update(dx*8.0,dy*8.0).unwrap());
                assert_eq!((drag.current.x,drag.current.y),(parked.x+dx*8.0*scale,parked.y+dy*8.0*scale));
                assert_eq!(drag.current.bounds,parked.bounds);
                assert!(drag.update(dx*130.0,dy*130.0).unwrap());
                assert_eq!((drag.current.x,drag.current.y),(parked.x+dx*130.0*scale,parked.y+dy*130.0*scale));
                assert!(!drag.update(0.0,0.0).unwrap());
                assert_eq!((drag.current.x,drag.current.y),(parked.x,parked.y));
            }
        }
    }

    #[test] fn releasing_a_partial_drag_returns_to_edge_without_expanding_first() {
        let mut parked=ball();parked.dock(Edge::Right);
        let mut drag=EdgeDrag::new(parked.clone());drag.update(-30.0,0.0).unwrap();
        let partial=drag.current;
        assert!(partial.x+partial.diameter>partial.bounds.right() as f64);
        let mut target=partial.clone();target.dock(Edge::Right);
        let mut slide=EdgeSlide::new(partial.clone(),target,false);
        assert_eq!(slide.step(0.0).0.x,partial.x);
        assert!(slide.step(0.1).0.x>partial.x);
        assert_eq!(slide.step(0.24).0.x,parked.x);
    }
    #[test] fn edge_slides_are_continuous_frame_independent_and_finish_exactly() {
        for edge in [Edge::Left,Edge::Right,Edge::Top,Edge::Bottom] {
            let from=ball();let mut to=from.clone();to.dock(edge);
            let mut slide=EdgeSlide::new(from.clone(),to.clone(),false);
            let (start,done)=slide.step(0.0);assert!(!done);assert_eq!((start.x,start.y),(from.x,from.y));
            let (mid,done)=slide.step(0.12);assert!(!done);
            let mut fine=EdgeSlide::new(from.clone(),to.clone(),false);
            let mut fine_ball=from.clone();for _ in 0..12 {fine_ball=fine.step(0.01).0;}
            assert!((mid.x-fine_ball.x).abs()<1e-6&&(mid.y-fine_ball.y).abs()<1e-6);
            assert!((mid.x-to.x).hypot(mid.y-to.y)<(from.x-to.x).hypot(from.y-to.y));
            let (end,done)=slide.step(0.2);assert!(done);assert_eq!((end.x,end.y),(to.x,to.y));
            // Reversing a half-finished close starts at its current position.
            let mut reverse=EdgeSlide::new(mid.clone(),from.clone(),true);
            assert_eq!(reverse.step(0.0).0.x,mid.x);
            let (end,done)=reverse.step(0.24);assert!(done);assert_eq!((end.x,end.y),(from.x,from.y));
        }
    }
    #[test] fn friction_stops_and_never_escapes() {
        let mut b=ball(); b.launch(-160.0,-112.0).unwrap();
        for _ in 0..600 { b.step(1.0/60.0); assert!(b.x>=0.0&&b.x<=1808.0&&b.y>=0.0&&b.y<=928.0); }
        assert_eq!(b.speed(),0.0);
    }
    #[test] fn both_fast_and_slow_hits_bounce_and_remain_visible() {
        for speed in [100.0,1200.0] {
            let mut b=ball();b.x=1807.0;b.vx=speed;b.step(0.02);
            assert!(b.vx<0.0&&b.speed()<speed);assert!(b.x<=1808.0);
            for _ in 0..600 {b.step(1.0/60.0);}
            assert_eq!(b.speed(),0.0);assert!(b.x>=0.0&&b.x+b.diameter<=1920.0);
        }
    }
    #[test] fn four_edges_retain_clickable_strip_on_negative_origin_and_high_dpi() {
        for edge in [Edge::Left,Edge::Right,Edge::Top,Edge::Bottom] {
            let mut b=ball();b.bounds=PixelRect{x:-2560,y:-200,width:2560,height:1440};b.scale=2.0;b.diameter=224.0;
            b.dock(edge);
            let width=(b.x+b.diameter).min(b.bounds.right() as f64)-b.x.max(b.bounds.x as f64);
            let height=(b.y+b.diameter).min(b.bounds.bottom() as f64)-b.y.max(b.bounds.y as f64);
            assert!((width.min(height)-40.0).abs()<0.01);
            b.expand();assert!(b.x>=-2560.0&&b.y>=-200.0&&b.x+b.diameter<=0.0&&b.y+b.diameter<=1240.0);
        }
    }
    #[test] fn frame_rate_independent_friction_and_invalid_input() {
        let mut a=ball();a.launch(-40.0,0.0).unwrap();let mut b=a.clone();
        for _ in 0..60 {a.step(1.0/60.0);}for _ in 0..120 {b.step(1.0/120.0);}
        assert!((a.x-b.x).abs()<0.01);assert!(b.launch(f64::NAN,1.0).is_err());
        b.launch(320.0,0.0).unwrap();assert_eq!(b.speed(),4400.0);
    }

    #[test] fn slingshot_reverses_pull_and_longer_pulls_travel_farther() {
        let mut distances=Vec::new();
        for pull in [20.0,80.0,160.0,320.0,640.0] {
            let mut b=ball(); b.bounds.width=100000;
            b.pull(-pull,0.0).unwrap();let start=b.x;
            b.launch(-pull,0.0).unwrap();assert!(b.vx>0.0);
            for _ in 0..600 {b.step(1.0/60.0);}
            distances.push(b.x-start);assert_eq!(b.speed(),0.0);
        }
        assert!(distances.windows(2).all(|pair|pair[0]<pair[1]));
        let mut b=ball();b.scale=1.5;b.launch(30.0,-40.0).unwrap();
        assert!(b.vx<0.0&&b.vy>0.0);assert!((b.vx/b.vy+0.75).abs()<1e-9);
        b.launch(0.0,0.0).unwrap();assert_eq!(b.speed(),0.0);
    }

    #[test] fn pull_tracks_beyond_the_old_limit_while_the_ball_stays_on_screen() {
        let mut b=ball();b.pull(800.0,0.0).unwrap();assert_eq!(b.x,1300.0);
        b.x=1800.0;b.pull(100.0,0.0).unwrap();assert_eq!(b.x,1808.0);
        assert!(b.pull(f64::INFINITY,0.0).is_err());
    }

    #[test] fn very_long_pulls_and_corner_bounces_never_escape_or_park() {
        let mut b=ball();b.scale=1.5;b.diameter=168.0;
        b.bounds=PixelRect{x:-1920,y:-200,width:1920,height:1080};b.x=-500.0;b.y=100.0;
        b.launch(-100000.0,-100000.0).unwrap();
        for _ in 0..1200 {
            b.step(1.0/60.0);
            assert!(b.x.is_finite()&&b.y.is_finite());
            assert!(b.x>=-1920.0&&b.x+b.diameter<=0.0&&b.y>=-200.0&&b.y+b.diameter<=880.0);
        }
        assert_eq!(b.speed(),0.0);
    }

    #[test] fn friction_is_between_the_previous_two_tunings() {
        let mut b=ball();b.vx=300.0;
        for _ in 0..60 {b.step(1.0/60.0);}
        assert!(b.vx<300.0*(-0.85_f64).exp());
        assert!(b.vx>300.0*(-1.15_f64).exp());
    }
}
