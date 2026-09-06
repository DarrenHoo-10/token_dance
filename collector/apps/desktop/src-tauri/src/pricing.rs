//! Local OpenRouter estimates. Network requests contain no usage or credentials.
use protocol::{EventEnvelope, EventPayload};
use serde::{Deserialize, Serialize};
use std::{collections::BTreeMap, path::Path, time::Duration};
const URL: &str = "https://openrouter.ai/api/v1/models";
const SCALE: u128 = 1_000_000_000_000_000;
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Rates {
    #[serde(default)]
    pub prompt: Option<String>,
    #[serde(default)]
    pub completion: Option<String>,
    #[serde(default)]
    pub input_cache_read: Option<String>,
    #[serde(default)]
    pub input_cache_write: Option<String>,
    #[serde(default)]
    pub request: Option<String>,
    #[serde(default)]
    pub overrides: Vec<Tier>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Tier {
    pub min_prompt_tokens: Option<u64>,
    pub utc_start: Option<u64>,
    pub utc_end: Option<u64>,
    pub utc_days: Option<Vec<String>>,
    #[serde(flatten)]
    pub rates: Rates,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Model {
    pub id: String,
    pub pricing: Rates,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Catalog {
    #[serde(default)]
    pub fetched_at: i64,
    pub data: Vec<Model>,
}
impl Catalog {
    pub fn load(root: &Path) -> Self {
        std::fs::read(root.join("openrouter-prices.json"))
            .ok()
            .and_then(|b| serde_json::from_slice(&b).ok())
            .unwrap_or_default()
    }
    pub fn fresh(&self) -> bool {
        !self.data.is_empty()
            && (0..21600).contains(&(chrono::Utc::now().timestamp() - self.fetched_at))
    }
    pub async fn fetch() -> Result<Self, String> {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(15))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| "PRICE_NETWORK_ERROR")?;
        let mut r = client
            .get(URL)
            .send()
            .await
            .map_err(|_| "PRICE_NETWORK_ERROR")?
            .error_for_status()
            .map_err(|_| "PRICE_HTTP_ERROR")?;
        let mut body = Vec::new();
        while let Some(chunk) = r.chunk().await.map_err(|_| "PRICE_NETWORK_ERROR")? {
            if body.len() + chunk.len() > 16 * 1024 * 1024 {
                return Err("PRICE_CATALOG_TOO_LARGE".into());
            }
            body.extend_from_slice(&chunk);
        }
        let mut c: Self = serde_json::from_slice(&body).map_err(|_| "PRICE_INVALID_CATALOG")?;
        if c.data.is_empty() {
            return Err("PRICE_EMPTY_CATALOG".into());
        }
        c.fetched_at = chrono::Utc::now().timestamp();
        Ok(c)
    }
    pub fn model(&self, id: &str) -> Option<&Model> {
        let id = match id {
            "grok-4.6-build" => "x-ai/grok-4.6",
            "gemini-3.7-flash-high" => "google/gemini-3.7-flash",
            "claude-opus-4-6-thinking" => "anthropic/claude-opus-4.6",
            other => other,
        };
        if let Some(m) = self.data.iter().find(|m| m.id.eq_ignore_ascii_case(id)) {
            return Some(m);
        }
        let mut matches = self.data.iter().filter(|m| {
            m.id.rsplit('/')
                .next()
                .is_some_and(|slug| slug.eq_ignore_ascii_case(id))
        });
        let m = matches.next()?;
        if matches.next().is_some() {
            None
        } else {
            Some(m)
        }
    }
}
// Decimal arithmetic to 10^-15 USD/token, rounded once to 10^-8 USD/call.
fn rate(s: &str) -> Option<u128> {
    let (whole, frac) = s.split_once('.').unwrap_or((s, ""));
    if whole.is_empty()
        || !whole
            .bytes()
            .chain(frac.bytes())
            .all(|b| b.is_ascii_digit())
        || frac.len() > 15
    {
        return None;
    }
    whole
        .parse::<u128>()
        .ok()?
        .checked_mul(SCALE)?
        .checked_add(format!("{frac:0<15}").parse().ok()?)
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Request {
    pub agent: String,
    pub date: String,
    pub model: String,
    pub group: String,
    pub input: Option<u64>,
    pub output: Option<u64>,
    pub read: u64,
    pub write: u64,
    pub tokens: u64,
    pub eligible: bool,
    pub estimate: Option<u64>,
    pub priced_at: Option<i64>,
    pub matched_model: Option<String>,
}
pub fn estimate(m: &Model, u: &Request) -> Option<u64> {
    if !u.eligible {
        return None;
    }
    let (mut input, output) = (u.input?, u.output?);
    let prompt_size = if u.agent == "codex" {
        input
    } else {
        input.checked_add(u.read)?.checked_add(u.write)?
    };
    let mut rates = m.pricing.clone();
    let mut tiers: Vec<_> = m
        .pricing
        .overrides
        .iter()
        .filter(|t| {
            t.min_prompt_tokens.is_some_and(|n| prompt_size >= n)
                && t.utc_start.is_none()
                && t.utc_end.is_none()
                && t.utc_days.is_none()
        })
        .collect();
    tiers.sort_by_key(|t| t.min_prompt_tokens);
    for t in tiers {
        macro_rules! replace {
            ($f:ident) => {
                if t.rates.$f.is_some() {
                    rates.$f = t.rates.$f.clone();
                }
            };
        }
        replace!(prompt);
        replace!(completion);
        replace!(input_cache_read);
        replace!(input_cache_write);
        replace!(request);
    }
    let prompt = rate(rates.prompt.as_deref()?)?;
    let completion = rate(rates.completion.as_deref()?)?;
    let read = rate(
        rates
            .input_cache_read
            .as_deref()
            .or(rates.prompt.as_deref())?,
    )?;
    let write = rate(
        rates
            .input_cache_write
            .as_deref()
            .or(rates.prompt.as_deref())?,
    )?;
    let request = rate(rates.request.as_deref().unwrap_or("0"))?;
    if u.agent == "codex" {
        input = input.checked_sub(u.read.checked_add(u.write)?)?;
    }
    let total = (input as u128)
        .checked_mul(prompt)?
        .checked_add((output as u128).checked_mul(completion)?)?
        .checked_add((u.read as u128).checked_mul(read)?)?
        .checked_add((u.write as u128).checked_mul(write)?)?
        .checked_add(request)?;
    u64::try_from(total.checked_add(5_000_000)? / 10_000_000).ok()
}
#[derive(Debug, Default, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CostCoverage {
    pub estimated_usd: u64,
    pub estimated_requests: u64,
    pub unpriced_requests: u64,
    pub detailed_tokens: u64,
}
impl CostCoverage {
    pub fn add(&mut self, other: &Self) {
        self.estimated_usd = self.estimated_usd.saturating_add(other.estimated_usd);
        self.estimated_requests += other.estimated_requests;
        self.unpriced_requests += other.unpriced_requests;
        self.detailed_tokens = self.detailed_tokens.saturating_add(other.detailed_tokens);
    }
}
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct CostLedger {
    pub backfilled: bool,
    requests: BTreeMap<String, Request>,
    reported_groups: BTreeMap<String, (String, String)>,
}
fn group(e: &EventEnvelope) -> String {
    format!(
        "{}:{}:{}",
        e.agent_id,
        e.session_hash.as_deref().unwrap_or(""),
        e.turn_hash.as_deref().unwrap_or(&e.event_id)
    )
}
impl CostLedger {
    pub fn record(
        &mut self,
        e: &EventEnvelope,
        date: &str,
        tokens: u64,
        catalog: &Catalog,
    ) -> bool {
        if matches!(e.payload, EventPayload::CostRecorded(_)) {
            return self
                .reported_groups
                .insert(group(e), (e.agent_id.clone(), date.into()))
                .is_none();
        }
        let EventPayload::ModelUsageRecorded(p) = &e.payload else {
            return false;
        };
        if let Some(old) = self.requests.get(&e.event_id) {
            if old.model != "unknown" || p.model_id == "unknown" {
                return false;
            }
        }
        let n = |v: &Option<String>| v.as_deref().and_then(|s| s.parse::<u64>().ok());
        let mut r = Request {
            agent: e.agent_id.clone(),
            date: date.into(),
            model: p.model_id.clone(),
            group: group(e),
            input: n(&p.tokens.input_tokens),
            output: n(&p.tokens.output_tokens),
            read: n(&p.tokens.cache_read_tokens).unwrap_or(0),
            write: n(&p.tokens.cache_write_tokens).unwrap_or(0),
            tokens,
            eligible: matches!(
                e.accuracy,
                protocol::Accuracy::Exact | protocol::Accuracy::Derived
            ),
            estimate: None,
            priced_at: None,
            matched_model: None,
        };
        Self::price(&mut r, catalog);
        self.requests.insert(e.event_id.clone(), r);
        true
    }
    fn price(r: &mut Request, c: &Catalog) -> bool {
        if r.estimate.is_some() {
            return false;
        }
        if let Some(m) = c.model(&r.model) {
            if let Some(amount) = estimate(m, r) {
                r.estimate = Some(amount);
                r.priced_at = Some(c.fetched_at);
                r.matched_model = Some(m.id.clone());
                return true;
            }
        }
        false
    }
    pub fn reprice(&mut self, c: &Catalog) -> bool {
        let mut changed = false;
        for r in self.requests.values_mut() {
            changed |= Self::price(r, c);
        }
        changed
    }
    pub fn days(
        &self,
        agent: &str,
        recorded_days: &std::collections::HashSet<String>,
    ) -> BTreeMap<String, CostCoverage> {
        let known_report_days: std::collections::HashSet<_> = self
            .reported_groups
            .values()
            .filter(|(a, _)| a == agent)
            .map(|(_, d)| d.as_str())
            .collect();
        let mut days = BTreeMap::<String, CostCoverage>::new();
        for r in self.requests.values().filter(|r| r.agent == agent) {
            let day = days.entry(r.date.clone()).or_default();
            day.detailed_tokens = day.detailed_tokens.saturating_add(r.tokens);
            if self.reported_groups.contains_key(&r.group) {
                continue;
            }
            // Older aggregate costs without retained call metadata cannot safely be combined.
            if recorded_days.contains(&r.date) && !known_report_days.contains(r.date.as_str()) {
                day.unpriced_requests += 1;
                continue;
            }
            if let Some(amount) = r.estimate {
                day.estimated_usd = day.estimated_usd.saturating_add(amount);
                day.estimated_requests += 1;
            } else {
                day.unpriced_requests += 1;
            }
        }
        days
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn catalog() -> Catalog {
        serde_json::from_str(r#"{"fetched_at":1,"data":[{"id":"openai/test","pricing":{"prompt":"0.000002","completion":"0.00001","input_cache_read":"0.0000002","overrides":[{"min_prompt_tokens":1500,"prompt":"0.000004"}]}}]}"#).unwrap()
    }
    fn event(marker: char) -> EventEnvelope {
        let mut e = crate::auto_sync::tests::event(marker);
        e.agent_id = "codex".into();
        e.occurred_at = chrono::Local::now().to_rfc3339();
        e.turn_hash = Some(format!("hmac-sha256:{}", "T".repeat(43)));
        if let EventPayload::ModelUsageRecorded(p) = &mut e.payload {
            p.model_id = "test".into();
            p.tokens.input_tokens = Some("1000".into());
            p.tokens.output_tokens = Some("100".into());
            p.tokens.cache_read_tokens = Some("800".into());
            p.tokens.total_tokens = Some("1100".into());
        }
        e
    }
    #[test]
    fn decimal_cache_tiers_missing_and_free_prices() {
        let mut l = CostLedger::default();
        let c = catalog();
        let e = event('A');
        l.record(&e, "2026-09-06", 1100, &c);
        let r = &l.requests[&e.event_id];
        assert_eq!(r.estimate, Some(156000));
        let mut r = r.clone();
        r.input = Some(2000);
        assert_eq!(estimate(&c.data[0], &r), Some(596000));
        r.input = Some(799);
        assert_eq!(estimate(&c.data[0], &r), None);
        assert!(c.model("test:batch").is_none());
        let mut m = c.data[0].clone();
        m.pricing.prompt = Some("-1".into());
        assert_eq!(estimate(&m, l.requests.values().next().unwrap()), None);
        m.pricing = serde_json::from_str(r#"{"prompt":"0","completion":"0"}"#).unwrap();
        assert_eq!(estimate(&m, l.requests.values().next().unwrap()), Some(0));
        assert_eq!(rate("0.000000005"), Some(5_000_000));
    }
    #[test]
    fn retained_history_dedupes_and_reported_turn_replaces_estimates_after_reload() {
        let root = tempfile::tempdir().unwrap();
        let mut l = crate::usage_ledger::UsageLedger::load(root.path());
        let a = event('A');
        let b = event('B');
        assert!(l.record(&[a.clone(), b.clone()])); // no network/catalog yet
        assert_eq!(
            l.agent_usage("codex", chrono::Local::now().date_naive())
                .unwrap()
                .pricing
                .unpriced_requests,
            2
        );
        l.apply_prices(catalog()).unwrap();
        let mut l = crate::usage_ledger::UsageLedger::load(root.path());
        assert!(!l.record(&[a.clone(), b.clone()]));
        let s = l
            .agent_usage("codex", chrono::Local::now().date_naive())
            .unwrap();
        assert_eq!(s.total_tokens, 2200);
        assert_eq!(s.pricing.estimated_usd, 312000);
        let mut reported = event('C');
        reported.payload=serde_json::from_value(serde_json::json!({"type":"cost_recorded","amount":"0.5","currency":"USD","source":"provider_reported"})).unwrap();
        assert!(l.record(&[reported.clone()]));
        assert!(!l.record(&[reported]));
        l.save().unwrap();
        let l = crate::usage_ledger::UsageLedger::load(root.path());
        let s = l
            .agent_usage("codex", chrono::Local::now().date_naive())
            .unwrap();
        assert_eq!(s.total_costs["USD"], 50_000_000);
        assert_eq!(s.pricing.estimated_requests, 0);
        assert_eq!(s.pricing.unpriced_requests, 0);
    }
    #[test]
    fn replay_can_enrich_previously_seen_unknown_model_without_adding_tokens() {
        let root = tempfile::tempdir().unwrap();
        let mut l = crate::usage_ledger::UsageLedger::load(root.path());
        l.apply_prices(catalog()).unwrap();
        let e = event('A');
        let mut unknown = e.clone();
        if let EventPayload::ModelUsageRecorded(p) = &mut unknown.payload {
            p.model_id = "unknown".into();
        }
        l.record(&[unknown]);
        assert!(l.record(&[e]));
        let s = l
            .agent_usage("codex", chrono::Local::now().date_naive())
            .unwrap();
        assert_eq!(s.total_tokens, 1100);
        assert_eq!(s.pricing.estimated_usd, 156000);
    }
    #[tokio::test]
    #[ignore = "explicit public catalog integration check"]
    async fn fetch_public_catalog() {
        let catalog = Catalog::fetch()
            .await
            .expect("public catalog should decode");
        assert!(catalog.data.len() > 100);
        assert!(catalog.model("gpt-6-astra").is_some());
    }
    #[test]
    fn time_discount_entries_do_not_break_catalog_or_become_unconditional_tiers() {
        let m:Model=serde_json::from_str(r#"{"id":"openai/test","pricing":{"prompt":"0.000002","completion":"0.00001","input_cache_read":"0.0000002","overrides":[{"utc_start":0,"utc_end":100,"prompt":"0"}]}}"#).unwrap();
        let mut l = CostLedger::default();
        let c = Catalog {
            data: vec![m],
            fetched_at: 1,
        };
        let e = event('A');
        l.record(&e, "2026-09-06", 1100, &c);
        assert_eq!(l.requests[&e.event_id].estimate, Some(156000));
    }
    #[test]
    fn unseen_historical_observations_do_not_change_tokens_or_cost_coverage() {
        let root = tempfile::tempdir().unwrap();
        let mut l = crate::usage_ledger::UsageLedger::load(root.path());
        l.apply_prices(catalog()).unwrap();
        l.record(&[event('A')]);
        assert!(!l.record_pricing(&[event('B')]));
        let s = l
            .agent_usage("codex", chrono::Local::now().date_naive())
            .unwrap();
        assert_eq!(s.total_tokens, 1100);
        assert_eq!(s.pricing.estimated_requests, 1);
    }
    #[test]
    fn aliases_are_explicit_and_ambiguous_slugs_unpriced() {
        let mut c = catalog();
        c.data.push(Model {
            id: "other/test".into(),
            pricing: Rates::default(),
        });
        assert!(c.model("test").is_none());
        assert!(c.model("openai/test").is_some());
        c.data.push(Model {
            id: "x-ai/grok-4.6".into(),
            pricing: Rates::default(),
        });
        assert_eq!(c.model("grok-4.6-build").unwrap().id, "x-ai/grok-4.6");
    }
}
