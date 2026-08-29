//! Adapter registry, version handshake, panic isolation, and circuit breaking.

#![forbid(unsafe_code)]

mod isolate;
mod registry;

pub use isolate::isolate;
pub use registry::{AdapterHost, AdapterOutcome};

#[cfg(test)]
mod tests;
