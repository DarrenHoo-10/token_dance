package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tokendance/internal/analytics"
	"tokendance/internal/auth"
	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/device"
	"tokendance/internal/export"
	"tokendance/internal/httpapi"
	"tokendance/internal/leaderboard"
	"tokendance/internal/media"
	"tokendance/internal/privacy"
	"tokendance/internal/profile"
	"tokendance/internal/search"
	"tokendance/internal/store/memory"
)

func main() {
	log.Println("Starting TokenDance API server...")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Fatal configuration error: %v", err)
	}

	clk := clock.RealClock{}
	st := memory.NewMemoryStore()

	authService := auth.NewService(st, cfg, clk)
	profileService := profile.NewService(st, clk)
	privacyService := privacy.NewService(st, clk)
	analyticsService := analytics.NewService(st, clk)
	deviceService := device.NewService(st, cfg, clk)
	exportService := export.NewService(st, clk)
	mediaService := media.NewService(st, cfg, clk)
	searchService := search.NewService(st, clk)
	leaderboardService := leaderboard.NewService(st)

	router := httpapi.NewRouter(
		authService,
		profileService,
		privacyService,
		analyticsService,
		deviceService,
		exportService,
		mediaService,
		searchService,
		leaderboardService,
	)

	server := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("TokenDance API listening on %s (env: %s)", cfg.HTTPAddr, cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("server listen error: %w", err)
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Fatalf("Server startup failed: %v", err)
	case sig := <-shutdown:
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Error during graceful shutdown: %v", err)
			_ = server.Close()
		}
		log.Println("TokenDance API stopped cleanly.")
	}
}
