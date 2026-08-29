package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/store/memory"
)

func main() {
	log.Println("Starting TokenDance Worker service...")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Fatal configuration error: %v", err)
	}

	_ = memory.NewMemoryStore()
	_ = clock.RealClock{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("TokenDance Worker started with env: %s", cfg.Environment)

	// Background worker loops
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Process outbox, export jobs, deletion jobs, expiry tasks
			}
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	sig := <-shutdown
	log.Printf("Received signal %v, shutting down worker tasks...", sig)
	cancel()
	time.Sleep(100 * time.Millisecond)
	log.Println("TokenDance Worker stopped cleanly.")
}
