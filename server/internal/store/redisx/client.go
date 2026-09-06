package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"tokendance/internal/config"
)

type ClientConfig struct {
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
	MinIdleConns int
	MaxRetries   int
	PingTimeout  time.Duration
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		DialTimeout:  2 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
		PoolSize:     20,
		MinIdleConns: 4,
		MaxRetries:   1,
		PingTimeout:  5 * time.Second,
	}
}

func RateLimitClientConfig() ClientConfig {
	cfg := DefaultClientConfig()
	cfg.DialTimeout = 100 * time.Millisecond
	cfg.ReadTimeout = 100 * time.Millisecond
	cfg.WriteTimeout = 100 * time.Millisecond
	cfg.MaxRetries = 0
	cfg.PingTimeout = 200 * time.Millisecond
	return cfg
}

func Options(cfg *config.Config, clientCfg ClientConfig) *redis.Options {
	if cfg == nil {
		return &redis.Options{}
	}
	return &redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		DialTimeout:  clientCfg.DialTimeout,
		ReadTimeout:  clientCfg.ReadTimeout,
		WriteTimeout: clientCfg.WriteTimeout,
		PoolSize:     clientCfg.PoolSize,
		MinIdleConns: clientCfg.MinIdleConns,
		MaxRetries:   clientCfg.MaxRetries,
	}
}

// OpenClient opens a Redis client and pings it, matching mysql.OpenDB.
func OpenClient(cfg *config.Config, clientCfg ClientConfig) (*redis.Client, error) {
	if cfg == nil || !cfg.RedisConfigured() {
		return nil, fmt.Errorf("Redis URL or address is not configured")
	}
	if cfg.RedisAddr == "" {
		return nil, fmt.Errorf("Redis address is empty")
	}
	if clientCfg.PingTimeout <= 0 {
		clientCfg = DefaultClientConfig()
	}
	client := redis.NewClient(Options(cfg, clientCfg))
	ctx, cancel := context.WithTimeout(context.Background(), clientCfg.PingTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("Redis ping readiness failed: %w", err)
	}
	return client, nil
}
