/// Crash/kill boundary simulation. Each append consumes at most one queued point.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FaultPoint {
    BeforeWrite,
    PartialWrite { bytes: usize },
    WriteSkipFsyncAbort,
    DurableWriteAbort,
}

#[derive(Debug, Default)]
pub struct FaultHook {
    queue: Vec<FaultPoint>,
}

impl FaultHook {
    pub fn push(&mut self, point: FaultPoint) {
        self.queue.push(point);
    }

    pub fn take(&mut self) -> Option<FaultPoint> {
        if self.queue.is_empty() {
            None
        } else {
            Some(self.queue.remove(0))
        }
    }

    pub fn is_empty(&self) -> bool {
        self.queue.is_empty()
    }
}
