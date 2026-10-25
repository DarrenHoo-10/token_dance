//! Server-published model folding rules (public deployment configuration,
//! never credentials): exact alias pairs served from `GET /v1/model-aliases`,
//! cached next to the local store, and applied to usage folding before the
//! OpenRouter catalog aliases, so a new spelling folds without a release.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::Path;
use std::time::Duration;

const URL: &str = "https://www.nexorai.com.cn/token-dance/v1/model-aliases";
const MAX_RULES: usize = 4096;
const MAX_NAME_LENGTH: usize = 256;
const MAX_BODY_BYTES: usize = 256 * 1024;

/// Cache file next to the local store, shared with the desktop writer.
pub const CACHE_FILE_NAME: &str = "model-aliases.json";

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AliasRule {
    pub from: String,
    pub to: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AliasConfig {
    #[serde(default)]
    pub revision: u64,
    #[serde(default)]
    pub aliases: Vec<AliasRule>,
    /// Set when the config is fetched, so the cache carries its own age.
    #[serde(default)]
    pub fetched_at: i64,
}

impl AliasConfig {
    /// Loads the last-known rules cached next to the store; a missing or
    /// invalid cache degrades to "no rules".
    pub fn load(root: &Path) -> Self {
        std::fs::read(root.join(CACHE_FILE_NAME))
            .ok()
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
            .unwrap_or_default()
    }

    /// Whether the cached rules were fetched recently enough to skip a refetch.
    pub fn fresh(&self) -> bool {
        (0..3600).contains(&(chrono::Utc::now().timestamp() - self.fetched_at))
    }

    /// Index for exact-match folding lookups.
    pub fn alias_map(&self) -> HashMap<String, String> {
        self.aliases
            .iter()
            .map(|rule| (rule.from.clone(), rule.to.clone()))
            .collect()
    }

    pub async fn fetch() -> Result<Self, String> {
        let endpoint = std::env::var("TOKENDANCE_MODEL_ALIASES_URL").unwrap_or_else(|_| URL.into());
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(15))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| "ALIAS_NETWORK_ERROR".to_string())?;
        let mut response = client
            .get(endpoint)
            .send()
            .await
            .map_err(|_| "ALIAS_NETWORK_ERROR".to_string())?
            .error_for_status()
            .map_err(|_| "ALIAS_HTTP_ERROR".to_string())?;
        let mut body = Vec::new();
        while let Some(chunk) = response.chunk().await.map_err(|_| "ALIAS_NETWORK_ERROR".to_string())? {
            if body.len() + chunk.len() > MAX_BODY_BYTES {
                return Err("ALIAS_CONFIG_TOO_LARGE".into());
            }
            body.extend_from_slice(&chunk);
        }
        let mut config = Self::parse(&body)?;
        config.fetched_at = chrono::Utc::now().timestamp();
        Ok(config)
    }

    /// Validates and normalizes a configuration document with the same rules
    /// as the server (`internal/modelalias` in the server): names trim and
    /// lowercase within the model-id charset, rules fold distinct non-empty
    /// spellings, and from spellings stay unique. Matching stays exact and is
    /// applied in `local_store::pipeline::query::server_alias_match`.
    pub fn parse(bytes: &[u8]) -> Result<Self, String> {
        if bytes.len() > MAX_BODY_BYTES {
            return Err("ALIAS_CONFIG_TOO_LARGE".into());
        }
        let mut config: Self = serde_json::from_slice(bytes).map_err(|_| "ALIAS_INVALID_CONFIG".to_string())?;
        if config.aliases.len() > MAX_RULES {
            return Err("ALIAS_TOO_MANY_RULES".into());
        }
        let mut seen = std::collections::HashSet::new();
        let mut aliases = Vec::with_capacity(config.aliases.len());
        for rule in config.aliases.drain(..) {
            let from = normalize_name(&rule.from).ok_or("ALIAS_INVALID_NAME")?;
            let to = normalize_name(&rule.to).ok_or("ALIAS_INVALID_NAME")?;
            if from == to {
                return Err("ALIAS_IDENTITY_RULE".into());
            }
            if !seen.insert(from.clone()) {
                return Err("ALIAS_DUPLICATE_RULE".into());
            }
            aliases.push(AliasRule { from, to });
        }
        config.aliases = aliases;
        Ok(config)
    }
}

fn normalize_name(name: &str) -> Option<String> {
    let lowered = name.trim().to_lowercase();
    if lowered.is_empty() || lowered.len() > MAX_NAME_LENGTH {
        return None;
    }
    let valid = lowered
        .chars()
        .all(|c| matches!(c, 'a'..='z' | '0'..='9' | '-' | '_' | '.' | '/' | '@' | ':' | '+' | '~'));
    valid.then_some(lowered)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_normalizes_and_indexes_rules() {
        let config = AliasConfig::parse(
            br#"{"revision":3,"aliases":[
                {"from":"  DeepSeek-V4P1-Flash ","to":"DeepSeek-V4.1-Flash"},
                {"from":"kimi-k3-thinking@x","to":"kimi-k3-thinking"}
            ]}"#,
        )
        .unwrap();
        assert_eq!(config.revision, 3);
        assert_eq!(config.aliases[0].from, "deepseek-v4p1-flash");
        assert_eq!(config.aliases[0].to, "deepseek-v4.1-flash");
        let map = config.alias_map();
        assert_eq!(map.get("deepseek-v4p1-flash").map(String::as_str), Some("deepseek-v4.1-flash"));
        assert_eq!(map.get("kimi-k3-thinking@x").map(String::as_str), Some("kimi-k3-thinking"));
        assert!(!config.fresh());
    }

    #[test]
    fn parse_rejects_invalid_documents() {
        for doc in [
            &br#"{"revision":1,"secret":"x"}"#[..],
            br#"{"aliases":[{"from":"  ","to":"x"}]}"#,
            br#"{"aliases":[{"from":"x","to":" X"}]}"#,
            br#"{"aliases":[{"from":"x","to":"y"},{"from":"X","to":"z"}]}"#,
            br#"{"aliases":[{"from":"a b","to":"y"}]}"#,
            br#"{"revision":true}"#,
        ] {
            assert!(AliasConfig::parse(doc).is_err(), "{:?} must be rejected", std::str::from_utf8(doc));
        }
    }

    #[test]
    fn parse_accepts_an_empty_rule_set() {
        let config = AliasConfig::parse(br#"{"revision":0,"aliases":[]}"#).unwrap();
        assert!(config.aliases.is_empty());
        assert!(config.alias_map().is_empty());
    }

    #[test]
    fn load_degrades_to_no_rules_for_missing_or_invalid_caches() {
        let dir = tempfile::tempdir().unwrap();
        assert_eq!(AliasConfig::load(dir.path()), AliasConfig::default());
        std::fs::write(dir.path().join(CACHE_FILE_NAME), b"not json").unwrap();
        assert_eq!(AliasConfig::load(dir.path()), AliasConfig::default());
    }
}
