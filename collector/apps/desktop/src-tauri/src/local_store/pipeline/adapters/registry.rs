//! Harness strategy registry for P3 adapters.

use std::path::PathBuf;
use std::sync::Arc;

use super::codex::CodexStrategy;
use super::common::SkillBook;
use super::cursor::CursorStrategy;
use super::jsonl_harness::{
    JsonlHarnessStrategy, CLAUDE, DEEPSEEK, DOUBAO, GROK, PI, WORKBUDDY,
};
use super::opencode::OpenCodeStrategy;
use super::zcode::ZcodeStrategy;
use crate::local_store::pipeline::runner::HarnessStrategy;

pub type SkillAllocator = Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>;

#[derive(Clone)]
pub struct AdapterRoots {
    pub identity_secret: Vec<u8>,
    pub codex_sessions: PathBuf,
    pub claude_projects: PathBuf,
    pub cursor_transcripts: PathBuf,
    pub zcode_db: PathBuf,
    pub opencode_db: PathBuf,
    pub grok_updates: PathBuf,
    pub deepseek_sessions: PathBuf,
    pub pi_sessions: PathBuf,
    pub workbuddy_history: PathBuf,
    pub doubao_history: PathBuf,
}

pub struct HarnessRegistry {
    pub skill_book: SkillBook,
    strategies: Vec<Box<dyn HarnessStrategy>>,
}

impl HarnessRegistry {
    pub fn from_roots(roots: AdapterRoots, skill_allocator: SkillAllocator) -> Self {
        let book = SkillBook::new();
        let secret = roots.identity_secret.clone();
        let mut strategies: Vec<Box<dyn HarnessStrategy>> = Vec::new();

        strategies.push(Box::new(CodexStrategy::new(
            secret.clone(),
            roots.codex_sessions,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            CLAUDE,
            secret.clone(),
            roots.claude_projects,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(CursorStrategy::new(
            secret.clone(),
            roots.cursor_transcripts,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(ZcodeStrategy::new(
            secret.clone(),
            roots.zcode_db,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(OpenCodeStrategy::new(
            secret.clone(),
            roots.opencode_db,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            GROK,
            secret.clone(),
            roots.grok_updates,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            DEEPSEEK,
            secret.clone(),
            roots.deepseek_sessions,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            PI,
            secret.clone(),
            roots.pi_sessions,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            WORKBUDDY,
            secret.clone(),
            roots.workbuddy_history,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            DOUBAO,
            secret,
            roots.doubao_history,
            book.clone(),
            skill_allocator,
        )));

        Self {
            skill_book: book,
            strategies,
        }
    }

    pub fn strategies(&self) -> &[Box<dyn HarnessStrategy>] {
        &self.strategies
    }

    pub fn get(&self, harness_id: &str) -> Option<&dyn HarnessStrategy> {
        self.strategies
            .iter()
            .find(|s| s.harness_id() == harness_id)
            .map(|s| s.as_ref())
    }

    pub fn harness_ids(&self) -> Vec<&str> {
        self.strategies.iter().map(|s| s.harness_id()).collect()
    }
}
