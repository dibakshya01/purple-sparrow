// Command purplesparrow is the single-binary entry point for the Purple Sparrow
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

	"github.com/dibakshya01/purple-sparrow/internal/agent/advisor"
	agentdocs "github.com/dibakshya01/purple-sparrow/internal/agent/docs"
	"github.com/dibakshya01/purple-sparrow/internal/agent/memory"
	"github.com/dibakshya01/purple-sparrow/internal/agent/meta"
	"github.com/dibakshya01/purple-sparrow/internal/auth"
	"github.com/dibakshya01/purple-sparrow/internal/buildinfo"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/httpapi"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
	"github.com/dibakshya01/purple-sparrow/internal/mcp"
	"github.com/dibakshya01/purple-sparrow/internal/observability"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
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
	baseURL := os.Getenv("PS_API_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787"
	}
	apiKey := os.Getenv("PS_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("PS_ADMIN_API_KEY")
	}
	fmt.Fprintf(os.Stderr, "purple-sparrow mcp: proxying to %s\n", baseURL)
	return mcp.New(baseURL, apiKey).Run(context.Background(), os.Stdin, os.Stdout)
}

func printMCPConfig() {
	baseURL := os.Getenv("PS_API_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787"
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"purple-sparrow": map[string]any{
				"command": "purplesparrow",
				"args":    []string{"mcp"},
				"env": map[string]string{
					"PS_API_URL": baseURL,
					"PS_API_KEY": "<your-api-key>",
				},
			},
		},
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(out))
}

func printHelp() {
	fmt.Print(`purple-sparrow — agent-native backend

Usage:
  purplesparrow [serve]     Run the backend server (default)
  purplesparrow mcp         Run the MCP stdio server (for coding agents)
  purplesparrow mcp-config  Print an MCP client config snippet
  purplesparrow version     Print version
  purplesparrow help        Show this help

Configuration is via PS_-prefixed environment variables (see the docs/README).
`)
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

	authSvc, err := auth.NewService(context.Background(), eng)
	if err != nil {
		return err
	}

	// Seed an admin API key. If PS_ADMIN_API_KEY is unset, generate an ephemeral
	// one and log it so a solo dev has admin access; production sets it explicitly.
	adminKey := cfg.AdminAPIKey
	if adminKey == "" {
		adminKey = "ps_sk_" + idgen.NewUUID()
		logger.Warn("PS_ADMIN_API_KEY not set; generated an ephemeral admin key for this run",
			"admin_api_key", adminKey,
			"hint", "set PS_ADMIN_API_KEY to keep it stable across restarts")
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
