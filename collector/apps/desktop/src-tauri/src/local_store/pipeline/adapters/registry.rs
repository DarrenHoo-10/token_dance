//! Harness strategy registry for P3 adapters.

use std::collections::HashSet;
use std::path::PathBuf;
use std::sync::Arc;

use super::codex::CodexStrategy;
use super::common::SkillBook;
use super::cursor::CursorStrategy;
use super::jsonl_harness::{JsonlHarnessStrategy, CLAUDE, DEEPSEEK, GROK, PI, WORKBUDDY};
use super::opencode::OpenCodeStrategy;
use super::zcode::ZcodeStrategy;
use crate::local_store::pipeline::runner::HarnessStrategy;

pub type SkillAllocator = Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>;

#[derive(Clone)]
pub struct AdapterRoots {
    pub identity_secret: Vec<u8>,
    /// All supported Codex detection sources (sessions, archived, …) keyed by source_id.
    pub codex_roots: Vec<(String, PathBuf)>,
    pub claude_projects: PathBuf,
    pub cursor_transcripts: PathBuf,
    pub cursor_usage: Option<super::cursor_usage::CursorUsagePaths>,
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
    /// Parallel to strategies: optional detection source_id for multi-root harnesses.
    strategy_source_ids: Vec<Option<String>>,
}

impl HarnessRegistry {
    pub fn from_roots(roots: AdapterRoots, skill_allocator: SkillAllocator) -> Self {
        let book = SkillBook::new();
        let secret = roots.identity_secret.clone();
        let mut strategies: Vec<Box<dyn HarnessStrategy>> = Vec::new();
        let mut strategy_source_ids: Vec<Option<String>> = Vec::new();

        let codex_roots = if roots.codex_roots.is_empty() {
            vec![("codex-sessions".into(), PathBuf::from(".codex/sessions"))]
        } else {
            roots.codex_roots
        };
        strategies.push(Box::new(CodexStrategy::with_roots(
            secret.clone(),
            codex_roots,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);

        strategies.push(Box::new(JsonlHarnessStrategy::new(
            CLAUDE,
            secret.clone(),
            roots.claude_projects,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);
        let mut cursor = CursorStrategy::new(
            secret.clone(),
            roots.cursor_transcripts,
            book.clone(),
            skill_allocator.clone(),
        );
        if let Some(paths) = roots.cursor_usage {
            cursor = cursor.with_usage(paths);
        }
        strategies.push(Box::new(cursor));
        strategy_source_ids.push(None);
        strategies.push(Box::new(ZcodeStrategy::new(
            secret.clone(),
            roots.zcode_db,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);
        strategies.push(Box::new(OpenCodeStrategy::new(
            secret.clone(),
            roots.opencode_db,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            GROK,
            secret.clone(),
            roots.grok_updates,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            DEEPSEEK,
            secret.clone(),
            roots.deepseek_sessions,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            PI,
            secret.clone(),
            roots.pi_sessions,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);
        strategies.push(Box::new(JsonlHarnessStrategy::new(
            WORKBUDDY,
            secret.clone(),
            roots.workbuddy_history,
            book.clone(),
            skill_allocator.clone(),
        )));
        strategy_source_ids.push(None);
        strategies.push(Box::new(super::doubao::DoubaoStrategy::new(
            secret,
            roots.doubao_history,
            book.clone(),
            skill_allocator,
        )));
        strategy_source_ids.push(None);

        Self {
            skill_book: book,
            strategies,
            strategy_source_ids,
        }
    }

    pub fn set_model_allocator(
        &mut self,
        allocate: crate::local_store::pipeline::runner::ModelAllocator,
    ) {
        for strategy in &mut self.strategies {
            strategy.set_model_allocator(Arc::clone(&allocate));
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

    /// Prefer a strategy that owns `locator_ref` via detection source roots; fall back to harness.
    pub fn get_for_source(
        &self,
        harness_id: &str,
        locator_ref: &str,
    ) -> Option<&dyn HarnessStrategy> {
        if harness_id == super::codex::HARNESS_ID {
            if let Some(strategy) = self.get(harness_id) {
                // CodexStrategy keeps all roots; locator selection is inside read via path.
                let _ = locator_ref;
                let _ = &self.strategy_source_ids;
                return Some(strategy);
            }
        }
        self.get(harness_id)
    }

    pub fn harness_ids(&self) -> Vec<&str> {
        let mut seen = HashSet::new();
        let mut out = Vec::new();
        for s in &self.strategies {
            if seen.insert(s.harness_id()) {
                out.push(s.harness_id());
            }
        }
        out
    }
}
