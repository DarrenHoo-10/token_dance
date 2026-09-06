package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	"tokendance/internal/pricing"
	"tokendance/internal/provider"
	"tokendance/internal/store/mysql"
	"tokendance/internal/worker"
)

func main() {
	log.Println("Starting TokenDance Worker service...")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Fatal configuration error: %v", err)
	}

	cipher, err := crypto.NewAEADCipherKeyring(cfg.AEADKeys.Keys, cfg.AEADKeys.CurrentVersion)
	if err != nil {
		log.Fatalf("Fatal cipher initialization error: %v", err)
	}

	emailProvider, err := email.NewProvider(cfg)
	if err != nil {
		log.Fatalf("Fatal email provider configuration error: %v", err)
	}
	storage, err := provider.NewObjectStorage(cfg)
	if err != nil {
		log.Fatalf("Fatal object storage configuration error: %v", err)
	}

	clk := clock.RealClock{}
	var db *sql.DB
	var wrk *worker.Worker

	if cfg.MySQLDSN != "" {
		log.Printf("Connecting worker to MySQL backend (pool: max_open=15, max_idle=5)...")
		workerPoolCfg := mysql.DBConfig{
			MaxOpenConns:    15,
			MaxIdleConns:    5,
			ConnMaxLifetime: 15 * time.Minute,
			ConnMaxIdleTime: 5 * time.Minute,
			PingTimeout:     5 * time.Second,
		}
		dbConn, err := mysql.OpenDB(cfg.MySQLDSN, workerPoolCfg)
		if err != nil {
			log.Fatalf("Fatal MySQL worker connection error: %v", err)
		}
		db = dbConn
		defer db.Close()

		// Validate schema compatibility without running DDL
		migRunner := migrate.NewRunner(db)
		ctxCheck, cancelCheck := context.WithTimeout(context.Background(), 10*time.Second)
		if err := migRunner.ValidateSchemaCompatibility(ctxCheck); err != nil {
			cancelCheck()
			log.Fatalf("Fatal database schema compatibility check failed: %v", err)
		}
		cancelCheck()
		log.Println("Database schema compatibility verified for worker.")

		wrk = worker.NewWorkerWithFull(db, clk, cipher, emailProvider, storage)
		wrk.SetPricing(pricing.NewClient())
		log.Printf("Worker registered with durable lease ID: %s", wrk.WorkerID())
	} else {
		if cfg.Environment == "production" {
			log.Fatalf("Fatal configuration error: TOKENDANCE_MYSQL_DSN_FILE or TOKENDANCE_MYSQL_DSN is required in production environment")
		}
		log.Printf("WARNING: Running worker in memory test/dev mode (env: %s)", cfg.Environment)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("TokenDance Worker loop started (env: %s)", cfg.Environment)

	// Background worker loops
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if wrk != nil {
					wrk.RunPass(ctx)
				}
			}
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	sig := <-shutdown
	log.Printf("Received signal %v, shutting down worker tasks...", sig)
	cancel()
	time.Sleep(200 * time.Millisecond)
	log.Println("TokenDance Worker stopped cleanly.")
}
