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
	checkFlag := flag.Bool("check", false, "Validate schema compatibility without executing migrations")
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

	if *checkFlag {
		log.Println("Checking database schema compatibility...")
		if err := runner.ValidateSchemaCompatibility(ctx); err != nil {
			log.Fatalf("Schema compatibility check failed: %v", err)
		}
		log.Println("Schema compatibility check passed.")
		return
	}

	log.Println("Applying database migrations with advisory lock and checksum validation...")
	if err := runner.RunMigrations(ctx); err != nil {
		log.Fatalf("Migration execution failed: %v", err)
	}
	log.Println("Database migrations successfully applied and verified.")
}
