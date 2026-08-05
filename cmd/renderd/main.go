// Command renderd is the HTTP entry point for the content rendering service.
//
// Its job is intentionally small: load config, build the server with its
// dependencies, then wait until either a shutdown signal arrives or the
// server crashes. All real logic lives in internal/.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"content-pipeline-insider/internal/api"
	"content-pipeline-insider/internal/config"
	"content-pipeline-insider/internal/logger"
)

func main() {
	// Config first. If this fails, nothing else has started, so a plain
	// stderr message and non-zero exit is the right response.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// One logger for the whole process; passed explicitly into every
	// subsystem. Avoid package-level globals — they make tests painful.
	log := logger.New(cfg.LogLevel)
	log.Info("starting renderd", "env", cfg.Env, "http_addr", cfg.HTTPAddr)

	server := api.NewServer(cfg, log)

	// Root context is cancelled when SIGINT/SIGTERM arrives. This is how
	// we notice "user hit Ctrl-C" or "Kubernetes is stopping the pod".
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Run the HTTP server on a goroutine so main can select{} below.
	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until either a signal fires or the server dies unexpectedly.
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-serverErr:
		log.Error("server crashed", "error", err)
		os.Exit(1)
	}

	// Give in-flight requests up to 10s to finish before killing the socket.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}
