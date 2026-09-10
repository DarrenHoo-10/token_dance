package grayscale

import (
	"fmt"
	"net"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

const (
	DefaultLimit       = 10
	DefaultRefreshDays = 2
	DefaultInterval    = 5 * time.Minute
	DefaultBatchSize   = 200
	DefaultSourceDB    = "tokendance_prod"
	DefaultTargetDB    = "tokendance_dev"
	stateTable         = "grayscale_mirror_state"
)

var forbiddenTargetDatabases = map[string]struct{}{
	"tokendance_prod":    {},
	"mysql":              {},
	"information_schema": {},
	"performance_schema": {},
	"sys":                {},
}

type Config struct {
	SourceDSN    string
	TargetDSN    string
	SourceSchema string
	TargetSchema string
	Limit        int
	RefreshDays  int
	BatchSize    int
	Interval     time.Duration
	Loop         bool
	Password     string
	AllowTarget  bool
}

func (c Config) withDefaults() Config {
	if c.SourceSchema == "" {
		c.SourceSchema = DefaultSourceDB
	}
	if c.TargetSchema == "" {
		c.TargetSchema = DefaultTargetDB
	}
	if c.Limit <= 0 {
		c.Limit = DefaultLimit
	}
	if c.RefreshDays <= 0 {
		c.RefreshDays = DefaultRefreshDays
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	return c
}

func (c Config) Validate() error {
	c = c.withDefaults()
	if strings.TrimSpace(c.SourceDSN) == "" {
		return fmt.Errorf("source MySQL DSN is required")
	}
	if strings.TrimSpace(c.TargetDSN) == "" {
		return fmt.Errorf("target MySQL DSN is required")
	}
	if !safeIdentifier.MatchString(c.SourceSchema) {
		return fmt.Errorf("unsafe source schema %q", c.SourceSchema)
	}
	if !safeIdentifier.MatchString(c.TargetSchema) {
		return fmt.Errorf("unsafe target schema %q", c.TargetSchema)
	}
	if c.Limit > 50 {
		return fmt.Errorf("limit %d exceeds 50", c.Limit)
	}
	source, err := parseDSN(c.SourceDSN)
	if err != nil {
		return fmt.Errorf("parse source DSN: %w", err)
	}
	target, err := parseDSN(c.TargetDSN)
	if err != nil {
		return fmt.Errorf("parse target DSN: %w", err)
	}
	sourceDB := firstNonEmpty(source.DBName, c.SourceSchema)
	targetDB := firstNonEmpty(target.DBName, c.TargetSchema)
	if _, forbidden := forbiddenTargetDatabases[strings.ToLower(targetDB)]; forbidden {
		return fmt.Errorf("refusing to write %s; grayscale mirror may only target a test database", targetDB)
	}
	if !c.AllowTarget && !strings.EqualFold(targetDB, DefaultTargetDB) {
		return fmt.Errorf("target database %s is not %s (set AllowTarget to override)", targetDB, DefaultTargetDB)
	}
	if canonicalAddr(source.Addr) == canonicalAddr(target.Addr) && strings.EqualFold(sourceDB, targetDB) {
		return fmt.Errorf("source and target resolve to the same MySQL database")
	}
	return nil
}

func parseDSN(dsn string) (*mysqldriver.Config, error) {
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func canonicalAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.ToLower(addr)
	}
	return strings.ToLower(host) + ":" + port
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
