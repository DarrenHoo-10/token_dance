package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"tokendance/internal/clock"
	"tokendance/internal/migrate"
	"tokendance/internal/pricing"
)

func TestOpenRouterBackfillMySQL(t *testing.T) {
	db := getTestMySQLDB(t)
	defer db.Close()
	if err := migrate.NewRunner(db).ResetCleanSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := migrate.NewRunner(db).RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	seedDeletionUser(t, db, "usr_price", "active")
	now := time.Now().UTC()
	for _, id := range []string{"ins_price_a", "ins_price_b", "ins_unknown"} {
		seedDeletionUsage(t, db, "usr_price", id, now)
	}
	_, err := db.Exec(`UPDATE usage_events SET agent_id='codex',model_id=IF(installation_id='ins_unknown','unknown','test'),token_input=1000,token_output=100,token_total=1100,turn_hash=UNHEX(SHA2('shared-turn',256))`)
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	priceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"data":[{"id":"openai/test","pricing":{"prompt":"0.000002","completion":"0.00001"}}]}`))
	}))
	defer priceServer.Close()
	client := pricing.NewClient()
	client.URL = priceServer.URL
	worker := NewWorker(db, clock.RealClock{})
	worker.SetPricing(client)
	n, err := worker.ProcessPrices(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("backfill=%d, %v", n, err)
	}
	var amount string
	var priced int
	if err = db.QueryRow(`SELECT SUM(cost_amount),COUNT(cost_amount) FROM usage_events`).Scan(&amount, &priced); err != nil || amount != "0.00600000" || priced != 2 {
		t.Fatalf("cost %q/%d %v", amount, priced, err)
	}
	if err = db.QueryRow(`SELECT cost_amount FROM daily_user_agent_metrics WHERE user_id='usr_price'`).Scan(&amount); err != nil || amount != "0.00600000" {
		t.Fatalf("per-request cost aggregation %q %v", amount, err)
	}
	worker.priceCursor = 0
	n, err = worker.ProcessPrices(context.Background())
	if err != nil || n != 0 || hits != 1 {
		t.Fatalf("non-idempotent second pass %d %v", n, err)
	}
	// A reported turn charge supersedes all estimates in that turn.
	_, err = db.Exec(`UPDATE usage_events SET cost_amount=0.5,cost_currency='USD',cost_source='provider_reported' WHERE installation_id='ins_unknown'`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = rebuildUserAggregates(context.Background(), tx, "usr_price", []string{now.Format("2006-01-02")}); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`SELECT cost_amount FROM daily_user_agent_metrics WHERE user_id='usr_price'`).Scan(&amount); err != nil || amount != "0.50000000" {
		t.Fatalf("reported cost precedence %q %v", amount, err)
	}
}
