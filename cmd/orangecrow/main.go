// Command orangecrow is the single-binary entry point for the Orange Crow
// agent-native backend.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dibakshya01/orange-crow/internal/agent/advisor"
	agentdocs "github.com/dibakshya01/orange-crow/internal/agent/docs"
	"github.com/dibakshya01/orange-crow/internal/agent/memory"
	"github.com/dibakshya01/orange-crow/internal/agent/meta"
	"github.com/dibakshya01/orange-crow/internal/auth"
	"github.com/dibakshya01/orange-crow/internal/buildinfo"
	"github.com/dibakshya01/orange-crow/internal/catalog"
	"github.com/dibakshya01/orange-crow/internal/config"
	"github.com/dibakshya01/orange-crow/internal/data"
	"github.com/dibakshya01/orange-crow/internal/data/migrate"
	"github.com/dibakshya01/orange-crow/internal/httpapi"
	"github.com/dibakshya01/orange-crow/internal/idgen"
	"github.com/dibakshya01/orange-crow/internal/mcp"
	"github.com/dibakshya01/orange-crow/internal/observability"
	"github.com/dibakshya01/orange-crow/internal/policy"
	"github.com/dibakshya01/orange-crow/internal/records"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
	}

	var err error
	switch cmd {
	case "serve":
		err = run()
	case "mcp":
		err = runMCP()
	case "mcp-config":
		printMCPConfig()
		return
	case "version":
		info := buildinfo.Get()
		fmt.Printf("%s %s (%s, %s)\n", info.Name, info.Version, info.Commit, info.Date)
		return
	case "help", "-h", "--help":
		printHelp()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printHelp()
		os.Exit(2)
	}
	if err != nil {
		os.Stderr.WriteString("fatal: " + err.Error() + "\n")
		os.Exit(1)
	}
}

// runMCP runs the MCP stdio server. It speaks JSON-RPC on stdout, so it must not
// write anything else there; diagnostics go to stderr.
func runMCP() error {
	baseURL := os.Getenv("OC_API_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787"
	}
	apiKey := os.Getenv("OC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OC_ADMIN_API_KEY")
	}
	fmt.Fprintf(os.Stderr, "orange-crow mcp: proxying to %s\n", baseURL)
	return mcp.New(baseURL, apiKey).Run(context.Background(), os.Stdin, os.Stdout)
}

func printMCPConfig() {
	baseURL := os.Getenv("OC_API_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787"
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"orange-crow": map[string]any{
				"command": "orangecrow",
				"args":    []string{"mcp"},
				"env": map[string]string{
					"OC_API_URL": baseURL,
					"OC_API_KEY": "<your-api-key>",
				},
			},
		},
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(out))
}

func printHelp() {
	fmt.Print(`orange-crow — agent-native backend

Usage:
  orangecrow [serve]     Run the backend server (default)
  orangecrow mcp         Run the MCP stdio server (for coding agents)
  orangecrow mcp-config  Print an MCP client config snippet
  orangecrow version     Print version
  orangecrow help        Show this help

Configuration is via OC_-prefixed environment variables (see the docs/README).
`)
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := observability.NewLogger(cfg.LogLevel, cfg.LogFormat)
	info := buildinfo.Get()
	logger.Info("starting orange-crow",
		"version", info.Version, "commit", info.Commit,
		"addr", cfg.Addr, "tier", string(cfg.Tier), "effective_tier", string(cfg.EffectiveTier()))

	// 0700: the data dir holds the SQLite DB (password hashes, the RSA signing key,
	// API-key hashes). Restricting the directory protects the DB and its -wal/-shm
	// sidecars regardless of their individual file modes.
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}

	// Data engine: Postgres when OC_DATABASE_URL is set, else embedded SQLite.
	var eng data.Engine
	if cfg.DatabaseURL != "" {
		pg, perr := data.OpenPostgres(cfg.DatabaseURL)
		if perr != nil {
			return perr
		}
		eng = pg
		logger.Info("using postgres engine")
	} else {
		sq, serr := data.OpenSQLite(cfg.DatabasePath())
		if serr != nil {
			return serr
		}
		eng = sq
		logger.Info("using embedded sqlite engine", "path", cfg.DatabasePath())
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

	authSvc, err := auth.NewService(context.Background(), eng)
	if err != nil {
		return err
	}

	// Seed an admin API key. For the solo tier we generate an ephemeral one and log
	// it (local-dev convenience). For any non-solo tier we refuse to boot without an
	// explicit key rather than mint-and-log a live admin credential: a per-restart
	// secret in production logs is both a leak and operationally useless.
	adminKey := cfg.AdminAPIKey
	if adminKey == "" {
		if cfg.EffectiveTier() != config.TierSolo {
			return fmt.Errorf("OC_ADMIN_API_KEY is required for the %q tier; refusing to generate and log an ephemeral admin credential in a non-solo deployment", cfg.EffectiveTier())
		}
		adminKey = "oc_sk_" + idgen.NewUUID()
		logger.Warn("OC_ADMIN_API_KEY not set; generated an ephemeral admin key for this solo-tier run",
			"admin_api_key", adminKey,
			"hint", "set OC_ADMIN_API_KEY to keep it stable and out of logs")
	}
	if err := authSvc.SeedAdminKey(context.Background(), adminKey); err != nil {
		return err
	}

	srv := httpapi.New(cfg, logger, httpapi.Deps{
		Catalog: cat,
		Records: rec,
		Policy:  pol,
		Meta:    mta,
		Auth:    authSvc,
		Docs:    agentdocs.New(),
		Memory:  memory.New(eng),
		Advisor: advisor.New(cat, pol),
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
