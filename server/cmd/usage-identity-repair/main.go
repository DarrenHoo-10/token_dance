// usage-identity-repair restores retained facts incorrectly shadowed by the old
// mixed-batch identity / cross-event-type dedup bugs. See docs/usage-identity-repair.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"
	"tokendance/internal/config"
	"tokendance/internal/store/mysql"
	"tokendance/internal/worker"
)

func main() {
	confirm := flag.Bool("confirm", false, "apply repair; default is dry-run")
	after := flag.Uint64("after-id", 0, "resume after this scan cursor")
	limit := flag.Int("batch-size", 200, "events per transaction pass (1..1000)")
	timeout := flag.Duration("timeout", time.Hour, "overall timeout")
	flag.Parse()
	if *limit < 1 || *limit > 1000 {
		log.Fatal("batch-size must be 1..1000")
	}
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal("cannot load configuration")
	}
	if cfg.MySQLDSN == "" {
		log.Fatal("TOKENDANCE_MYSQL_DSN required")
	}
	db, err := mysql.OpenDB(cfg.MySQLDSN, mysql.DefaultDBConfig())
	if err != nil {
		log.Fatal("cannot open database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	w := worker.NewWorker(db, nil)
	for {
		r, err := w.RepairUsageIdentity(ctx, *after, *limit, !*confirm, time.Now().UTC())
		if err != nil {
			log.Fatalf("repair failed after cursor %d; inspect database before resuming", r.NextID)
		}
		fmt.Printf("dry_run=%t cursor=%d examined=%d normalized=%d requeued_grains=%d\n", !*confirm, r.NextID, r.Examined, r.Normalized, r.Requeued)
		if r.Examined == 0 {
			break
		}
		*after = r.NextID
	}
}
