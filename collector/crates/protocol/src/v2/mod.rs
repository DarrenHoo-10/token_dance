//! Protocol v2 types and content-hash helpers.

#[rustfmt::skip]
mod generated;
mod canonical;

pub use canonical::*;
pub use generated::*;

#[cfg(test)]
mod golden_test;
