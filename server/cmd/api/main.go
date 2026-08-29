package main

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/tokendance/token-collector/server/internal/api"
	"github.com/tokendance/token-collector/server/internal/ingest"
	"github.com/tokendance/token-collector/server/internal/store/memstore"
)

func main() {
	s := memstore.New()

	ingestSvc := &ingest.Service{
		Batches:  s,
		Events:   s,
		Installs: s,
		Users:    s,
	}

	handler := &api.Handler{
		Users:         s,
		Installations: s,
		Batches:       s,
		Events:        s,
		Leaderboards:  s,
		Ingest:        ingestSvc,
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
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:30]
}
