package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"tokendance/internal/config"
	"tokendance/internal/migrate"
	"tokendance/internal/store/mysql"
)

func main() {
	dsnFlag := flag.String("dsn", "", "MySQL connection DSN (overrides TOKENDANCE_MYSQL_DSN)")
	checkFlag := flag.Bool("check", false, "Validate migration state and schema compatibility without executing migrations")
	repairFlag := flag.Bool("repair", false, "Reconcile MySQL schema state and resume a dirty migration from durable statement progress")
	flag.Parse()

	cfg, _ := config.LoadFromEnv()

	dsn := ""
	if *dsnFlag != "" {
		dsn = *dsnFlag
	} else if cfg != nil && cfg.MySQLDSN != "" {
		dsn = cfg.MySQLDSN
	} else if v := os.Getenv("TOKENDANCE_MYSQL_DSN"); v != "" {
		dsn = v
	}

	if dsn == "" {
		log.Fatalf("Fatal: MySQL DSN must be provided via -dsn flag, TOKENDANCE_MYSQL_DSN, or TOKENDANCE_MYSQL_DSN_FILE")
	}

	dbPoolCfg := mysql.DBConfig{
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 10 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
		PingTimeout:     10 * time.Second,
	}

	db, err := mysql.OpenDB(dsn, dbPoolCfg)
	if err != nil {
		log.Fatalf("Fatal MySQL connection error: %v", err)
	}
	defer db.Close()

	runner := migrate.NewRunner(db)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if *checkFlag && *repairFlag {
		log.Fatalf("Fatal: -check and -repair cannot be used together")
	}
	if *checkFlag {
		log.Println("Checking migration state and database schema compatibility...")
		if err := runner.CheckMigrationState(ctx); err != nil {
			log.Fatalf("Migration state check failed: %v", err)
		}
		if err := runner.ValidateSchemaCompatibility(ctx); err != nil {
			log.Fatalf("Schema compatibility check failed: %v", err)
		}
		log.Println("Migration state and schema compatibility checks passed.")
		return
	}
	if *repairFlag {
		log.Println("Reconciling information_schema and repairing dirty migration progress...")
		if err := runner.RepairMigrations(ctx); err != nil {
			log.Fatalf("Migration repair failed: %v", err)
		}
		log.Println("Database migration repair completed and verified.")
		return
	}

	log.Println("Applying database migrations with advisory lock, checksum validation, and durable statement progress...")
	if err := runner.RunMigrations(ctx); err != nil {
		log.Fatalf("Migration execution failed: %v", err)
	}
	log.Println("Database migrations successfully applied and verified.")
}
