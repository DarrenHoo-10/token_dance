package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/tokendance/token-collector/server/internal/aggregator"
	"github.com/tokendance/token-collector/server/internal/store/memstore"
)

func main() {
	s := memstore.New()

	worker := &aggregator.Worker{
		Events:  s,
		Metrics: s,
		Users:   s,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	interval := 10 * time.Second
	log.Printf("TokenShow aggregation worker started (interval: %v)", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("worker shutting down")
			return
		case <-ticker.C:
			n, err := worker.RunOnce(ctx)
			if err != nil {
				log.Printf("aggregation error: %v", err)
			} else if n > 0 {
				log.Printf("aggregated %d events (watermark pk=%d)", n, worker.LastProcessedPK)
			}
		}
	}
}
