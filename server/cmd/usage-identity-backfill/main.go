// Command usage-identity-backfill repairs account usage totals that were
// double counted when Claude Code SSH remote sessions mirrored the same
// transcript to two machines before wire usageIdentity support existed.
//
// The command is idempotent and resumable; re-run it until the report shows no
// examined groups. Dirty days are enqueued per repaired group, and the normal
// worker refreshes window scores and community stats from there.
//
//	go run ./cmd/usage-identity-backfill --dry-run
//	go run ./cmd/usage-identity-backfill --confirm
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"tokendance/internal/config"
	"tokendance/internal/store/mysql"
	"tokendance/internal/telemetry"
	"tokendance/internal/worker"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would be repaired without mutating")
	confirm := flag.Bool("confirm", false, "required to mutate (or set --dry-run)")
	sinceDays := flag.Int("since-days", 0, "only repair events newer than this many days (0 = all history)")
	maxGroups := flag.Int("max-groups", 200, "maximum duplicate groups repaired per pass")
	singlePass := flag.Bool("single-pass", false, "run one pass instead of looping until no groups remain")
	flag.Parse()

	if !*dryRun && !*confirm {
		log.Fatal("refusing to mutate without --confirm (or pass --dry-run)")
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.MySQLDSN == "" {
		log.Fatal("TOKENDANCE_MYSQL_DSN required")
	}

	db, err := mysql.OpenDB(cfg.MySQLDSN, mysql.DefaultDBConfig())
	if err != nil {
		log.Fatalf("mysql: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	w := worker.NewWorker(db, nil)
	opts := worker.UsageIdentityBackfillOptions{
		HMACKey:   telemetry.UsageIdentityKey(cfg.HMACSecret),
		MaxGroups: *maxGroups,
		SinceDays: *sinceDays,
		Now:       time.Now().UTC(),
		DryRun:    *dryRun,
	}

	for {
		report, err := w.BackfillUsageIdentity(ctx, opts)
		if err != nil {
			log.Fatalf("backfill: %v", err)
		}
		fmt.Printf("examined=%d repaired=%d skipped=%d rows_identified=%d rows_reversed=%d dirty_days=%d\n",
			report.GroupsExamined, report.GroupsRepaired, report.GroupsSkipped,
			report.RowsIdentified, report.RowsReversed, report.DirtyDaysMarked)
		if *dryRun {
			fmt.Println("dry-run: nothing was mutated")
			return
		}
		if report.GroupsExamined == 0 || *singlePass {
			return
		}
	}
}
