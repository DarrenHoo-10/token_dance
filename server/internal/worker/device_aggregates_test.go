package worker

import (
	"context"
	"encoding/json"
	"testing"
	"tokendance/internal/domain"
	"tokendance/internal/migrate"
)

func TestDeviceAggregateRebuildMySQL(t *testing.T) {
	db := getTestMySQLDB(t)
	defer db.Close()
	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	defer runner.ResetCleanSchema(ctx)
	_, err := db.Exec(`INSERT INTO users(user_id,auth_subject_hash,display_name,account_status,leaderboard_visibility,timezone_name,created_at,updated_at) VALUES('usr_agg',UNHEX(SHA2('agg',256)),'Aggregate','active','private','UTC',NOW(),NOW())`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO installations(installation_id,user_id,device_public_key,os_type,architecture,collector_version,installation_status,registered_at,updated_at) VALUES('ins_agg','usr_agg',UNHEX(SHA2('key',256)),'windows','x86_64','1','active',NOW(),NOW())`)
	if err != nil {
		t.Fatal(err)
	}
	// A raw event in an aggregate-covered day must not be counted a second time.
	_, err = db.Exec(`INSERT INTO ingest_batches(batch_id,installation_id,request_sha256,event_count,accepted_count,duplicate_count,rejected_count,batch_status,received_at,updated_at) VALUES('bat_agg','ins_agg',UNHEX(SHA2('batch',256)),1,1,0,0,'committed',NOW(),NOW())`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO usage_events(installation_id,user_id,batch_id,event_id,schema_version,adapter_id,adapter_version,agent_id,event_type,accuracy,source_kind,occurred_at,token_total,privacy_policy_version) VALUES('ins_agg','usr_agg','bat_agg',UNHEX(SHA2('event',256)),1,'test','1','codex','model_usage_recorded','exact','runtime_stream','2026-09-06 10:00:00',100,1)`)
	if err != nil {
		t.Fatal(err)
	}
	put := func(total string) {
		t.Helper()
		rows := []domain.AggregateRow{{Kind: "agent", AgentID: "codex", Metrics: map[string]string{"exact_token_total": total, "model_request_count": "1", "cost_usd_units": "100000000", "estimated_cost_usd_units": "50000000"}}}
		payload, _ := json.Marshal(rows)
		_, err = db.Exec(`INSERT INTO device_daily_aggregates(installation_id,user_id,metric_date,revision,content_hash,payload_json,updated_at) VALUES('ins_agg','usr_agg','2026-09-06',1,UNHEX(SHA2('x',256)),?,NOW()) ON DUPLICATE KEY UPDATE payload_json=VALUES(payload_json),revision=revision+1`, payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	rebuild := func() {
		t.Helper()
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = rebuildUserAggregates(ctx, tx, "usr_agg", []string{"2026-09-06"}); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	check := func(want uint64) {
		t.Helper()
		var tokens uint64
		var cost string
		if err = db.QueryRow("SELECT exact_token_total,cost_amount FROM daily_user_agent_metrics WHERE user_id='usr_agg'").Scan(&tokens, &cost); err != nil {
			t.Fatal(err)
		}
		if tokens != want || cost != "1.50000000" {
			t.Fatalf("tokens=%d cost=%s", tokens, cost)
		}
	}
	put("120")
	rebuild()
	check(120)
	rebuild()
	check(120)
	put("80")
	rebuild()
	check(80)
}
