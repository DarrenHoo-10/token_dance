use super::*;

fn seed_pipeline(root: &std::path::Path) {
    let now = chrono::Utc::now().timestamp_millis();
    let today = chrono::Local::now().date_naive();
    let bucket = beijing_bounds_for_date(today).0;
    let conn = rusqlite::Connection::open(root.join("tokendance-events.sqlite3")).unwrap();
    conn.execute(
        "INSERT INTO model_metrics (created_at,updated_at,grain,bucket_start,harness_id,model_key,
         metric_semantics_version,exact_token_total,token_total_known_count,usage_observed_count,model_request_count)
         VALUES (?1,?1,'day',?2,'codex',0,1,42,1,1,1)",
        rusqlite::params![now, bucket],
    ).unwrap();
    conn.execute(
        "INSERT INTO cost_metrics (created_at,updated_at,grain,bucket_start,harness_id,model_key,
         currency,reported_cost_units,reported_request_count,cost_known_count,metric_semantics_version)
         VALUES (?1,?1,'day',?2,'codex',0,'USD',250000000,1,1,1)",
        rusqlite::params![now, bucket],
    ).unwrap();
}

#[tokio::test]
async fn review3_agent_list_uses_pipeline_even_when_legacy_has_data() {
    let (root, app) = crate::tests::state().await;
    let mut old_event = crate::auto_sync::tests::event('B');
    old_event.agent_id = "codex".into();
    old_event.occurred_at = chrono::Local::now().to_rfc3339();
    assert!(app.record_usage(&[old_event]));
    seed_pipeline(root.path());
    let orb = app.get_usage_summary(chrono::Local::now().date_naive());
    assert_eq!(orb.today_tokens.as_deref(), Some("42"));
    let agents = app.get_agents().await;
    let codex = agents.iter().find(|a| a.id == "codex").unwrap();
    assert_eq!(
        codex.today_tokens, 42,
        "plugin card and orb must read the same active pipeline"
    );
}

#[tokio::test]
async fn review3_agent_list_preserves_pipeline_costs() {
    let (root, app) = crate::tests::state().await;
    seed_pipeline(root.path());
    let agents = app.get_agents().await;
    let codex = agents.iter().find(|a| a.id == "codex").unwrap();
    assert_eq!(codex.today_tokens, 42);
    assert_eq!(
        codex.total_costs.get("USD").copied(),
        Some(250000000),
        "known pipeline costs must reach the desktop card"
    );
}

#[tokio::test]
async fn review3_pipeline_history_keeps_all_time_totals_and_currency_coverage() {
    let (root, app) = crate::tests::state().await;
    seed_pipeline(root.path());
    let today = chrono::Local::now().date_naive();
    let old_day = today - chrono::Duration::days(DISPLAY_DAYS + 1);
    let conn = rusqlite::Connection::open(root.path().join("tokendance-events.sqlite3")).unwrap();
    conn.execute("UPDATE cost_metrics SET estimated_cost_units=50000000, estimated_request_count=1, cost_known_count=2", []).unwrap();
    conn.execute("INSERT INTO cost_metrics (created_at,updated_at,grain,bucket_start,harness_id,model_key,currency,estimated_cost_units,estimated_request_count,cost_known_count,unpriced_request_count,metric_semantics_version)
        VALUES (1,1,'day',?1,'codex',0,'CNY',700000000,1,1,1,1)", [beijing_bounds_for_date(today).0]).unwrap();
    conn.execute("INSERT INTO model_metrics (created_at,updated_at,grain,bucket_start,harness_id,model_key,exact_token_total,token_total_known_count,usage_observed_count,metric_semantics_version)
        VALUES (1,1,'day',?1,'codex',0,10,1,1,1)", [beijing_bounds_for_date(old_day).0]).unwrap();
    let agents = app.get_agents().await;
    let codex = agents.iter().find(|a| a.id == "codex").unwrap();
    assert_eq!(codex.today_tokens, 42);
    assert_eq!(
        codex.total_tokens, 52,
        "all-time totals cannot be cut off by the chart display window"
    );
    assert_eq!(
        codex.history_start,
        Some(old_day.format("%Y-%m-%d").to_string())
    );
    assert_eq!(codex.total_costs.get("USD"), Some(&250000000));
    assert_eq!(codex.pricing.estimated_costs.get("USD"), Some(&50000000));
    assert_eq!(codex.pricing.estimated_costs.get("CNY"), Some(&700000000));
    assert_eq!(codex.pricing.estimated_usd, 50000000);
    assert_eq!(codex.pricing.estimated_requests, 2);
    assert_eq!(codex.pricing.unpriced_requests, 1);
    assert_eq!(
        codex.daily_usage.last().unwrap().pricing.estimated_costs,
        codex.pricing.estimated_costs
    );
}

#[tokio::test]
async fn review3_pipeline_known_zero_is_preserved() {
    let (root, app) = crate::tests::state().await;
    seed_pipeline(root.path());
    let conn = rusqlite::Connection::open(root.path().join("tokendance-events.sqlite3")).unwrap();
    conn.execute("UPDATE model_metrics SET exact_token_total=0", [])
        .unwrap();
    conn.execute("UPDATE cost_metrics SET reported_cost_units=0", [])
        .unwrap();
    let agents = app.get_agents().await;
    let codex = agents.iter().find(|a| a.id == "codex").unwrap();
    assert_eq!(codex.accuracy, "exact");
    assert_eq!(codex.total_costs.get("USD"), Some(&0));
    assert_eq!(
        app.get_usage_summary(chrono::Local::now().date_naive())
            .today_tokens
            .as_deref(),
        Some("0")
    );
}

#[tokio::test]
async fn review3_restore_backup_applies_pipeline_harness_toggle() {
    let (_root, app) = crate::tests::state().await;
    let enabled_backup = app
        .create_config_backup(Some("codex enabled".into()))
        .await
        .unwrap();
    app.set_agent_status("codex", false).await.unwrap();
    let backup = app
        .create_config_backup(Some("codex disabled".into()))
        .await
        .unwrap();
    app.set_agent_status("codex", true).await.unwrap();
    let restored = app.restore_config_backup(&backup.id).await.unwrap();
    assert!(!restored.state.agent_toggles["codex"]);
    assert!(!app.pipeline_runtime().unwrap().is_harness_enabled("codex"), "restored disabled switch must stop the actual pipeline, not only the legacy collector and UI");
    app.restore_config_backup(&enabled_backup.id).await.unwrap();
    assert!(app.pipeline_runtime().unwrap().is_harness_enabled("codex"));
}
