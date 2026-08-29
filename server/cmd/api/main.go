package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/oklog/ulid/v2"
	"github.com/tokendance/token-collector/server/internal/api"
	"github.com/tokendance/token-collector/server/internal/auth"
	"github.com/tokendance/token-collector/server/internal/ingest"
	"github.com/tokendance/token-collector/server/internal/store"
	"github.com/tokendance/token-collector/server/internal/store/mysqlstore"
	"github.com/tokendance/token-collector/server/internal/store/redisstore"
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "root:tokenshow_test_root@tcp(127.0.0.1:33068)/?charset=utf8mb4"
	}
	s, err := mysqlstore.Open(context.Background(), dsn, true)
	if err != nil {
		log.Fatalf("open MySQL store: %v", err)
	}
	defer s.Close()

	ingestSvc := &ingest.Service{
		Batches:  s,
		Events:   s,
		Installs: s,
		Users:    s,
	}

	var cache *redisstore.Cache
	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		cache = redisstore.New(redisAddr)
		defer cache.Close()
		if err := cache.Ping(context.Background()); err != nil {
			log.Printf("Redis cache unavailable at %s; using MySQL fallback: %v", redisAddr, err)
		}
	}

	nonces := &store.FallbackNonceStore{Cache: cache, Fallback: s}
	leaderboards := &store.FallbackLeaderboardStore{Cache: cache, Fallback: s}
	deviceAuth := &auth.DeviceAuth{Installations: s, Nonces: nonces}
	userSessions := &auth.StoredUserSessionResolver{Users: s}
	handler := &api.Handler{
		Users:         s,
		Installations: s,
		Batches:       s,
		Events:        s,
		Leaderboards:  leaderboards,
		Ingest:        ingestSvc,
		DeviceAuth:    deviceAuth,
		UserSessions:  userSessions,
		IDGenerator:   generateID,
	}

	mux := api.NewMux(handler)

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("TokenShow API listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func generateID() string {
	return "ins_" + ulid.Make().String()
}
