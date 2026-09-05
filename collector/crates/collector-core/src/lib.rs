//! Collector Core: lifecycle, status, and capability aggregation.
//!
//! This crate talks to Adapters only through `adapter-host` and Adapter SDK
//! types. It must not branch on concrete product names.

#![forbid(unsafe_code)]

mod collector;
mod control;
mod runtime;

pub use collector::Collector;
pub use control::CollectionControl;
pub use runtime::{derive_status, AdapterRuntime};

#[cfg(test)]
mod tests;
