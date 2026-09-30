// Command purplesparrow is the single-binary entry point for the Purple Sparrow
// agent-native backend.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/buildinfo"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/httpapi"
	"github.com/dibakshya01/purple-sparrow/internal/observability"
)

func main() {
	if err := run(); err != nil {
		// run() logs details; keep the final line minimal and exit non-zero.
		os.Stderr.WriteString("fatal: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := observability.NewLogger(cfg.LogLevel, cfg.LogFormat)
	info := buildinfo.Get()
	logger.Info("starting purple-sparrow",
		"version", info.Version, "commit", info.Commit,
		"addr", cfg.Addr, "tier", string(cfg.Tier), "effective_tier", string(cfg.EffectiveTier()))

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}

	srv := httpapi.New(cfg, logger)
	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Handler(),
		// ReadHeaderTimeout guards against Slowloris (gosec G112). We deliberately
		// do NOT set global ReadTimeout/WriteTimeout: they would strangle future
		// streaming endpoints (uploads, SSE, websockets). Per-route timeout
		// middleware handles those cases in later milestones.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}

	// Signal-aware context for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		srv.SetReady(true)
		logger.Info("listening", "addr", cfg.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining")
		srv.SetReady(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err.Error())
			return err
		}
		logger.Info("stopped cleanly")
		return nil
	}
}
