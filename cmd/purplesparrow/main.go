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

	"github.com/dibakshya01/purple-sparrow/internal/agent/meta"
	"github.com/dibakshya01/purple-sparrow/internal/buildinfo"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/httpapi"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
	"github.com/dibakshya01/purple-sparrow/internal/observability"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
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

	// Data engine (SQLite, solo tier) + migrations.
	eng, err := data.OpenSQLite(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer eng.Close()
	if err := migrate.Run(context.Background(), eng); err != nil {
		return err
	}

	cat := catalog.New(eng)
	pol := policy.NewService(eng)
	enf := policy.NewEnforcer(eng)
	rec := records.New(eng, cat, enf)
	mta := meta.New(eng, cat, pol)

	// Admin key bridge (M1). If unset, generate an ephemeral one and log it so a
	// solo dev has admin access; production sets PS_ADMIN_API_KEY explicitly.
	adminKey := cfg.AdminAPIKey
	if adminKey == "" {
		adminKey = "ps_sk_" + idgen.NewUUID()
		logger.Warn("PS_ADMIN_API_KEY not set; generated an ephemeral admin key for this run",
			"admin_api_key", adminKey,
			"hint", "set PS_ADMIN_API_KEY to keep it stable across restarts")
	}

	srv := httpapi.New(cfg, logger, httpapi.Deps{
		Catalog:  cat,
		Records:  rec,
		Policy:   pol,
		Meta:     mta,
		AdminKey: adminKey,
	})
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
