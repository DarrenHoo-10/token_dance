package redisx

import (
	"testing"

	"tokendance/internal/config"
)

func TestOptionsCopyConnectionSettings(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.RedisAddr = "www.nexorai.com.cn:6380"
	cfg.RedisPassword = "devpass"
	cfg.RedisDB = 0
	opt := Options(cfg, DefaultClientConfig())
	if opt.Addr != cfg.RedisAddr || opt.Password != cfg.RedisPassword || opt.DB != 0 {
		t.Fatalf("options mismatch: %+v", opt)
	}
}

func TestOpenClientRequiresConfiguration(t *testing.T) {
	if _, err := OpenClient(config.DefaultConfig(), DefaultClientConfig()); err == nil {
		t.Fatal("expected error when Redis is not configured")
	}
}
