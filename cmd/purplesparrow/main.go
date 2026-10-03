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
	"github.com/dibakshya01/purple-sparrow/internal/blob"
	"github.com/dibakshya01/purple-sparrow/internal/buildinfo"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/events"
	"github.com/dibakshya01/purple-sparrow/internal/functions"
	"github.com/dibakshya01/purple-sparrow/internal/httpapi"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
	"github.com/dibakshya01/purple-sparrow/internal/mcp"
	"github.com/dibakshya01/purple-sparrow/internal/observability"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
	"github.com/dibakshya01/purple-sparrow/internal/storage"
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

	// 0700: the data dir holds the SQLite DB (password hashes, the RSA signing key,
	// API-key hashes). Restricting the directory protects the DB and its -wal/-shm
	// sidecars regardless of their individual file modes.
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}

	// Data engine: Postgres when PS_DATABASE_URL is set, else embedded SQLite.
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

	// Realtime (M8): record mutations publish to the in-process event hub; the
	// /v1/realtime SSE endpoint fans them out, policy-filtered per subscriber.
	bus := events.NewHub()
	rec.SetPublisher(bus)

	authSvc, err := auth.NewService(context.Background(), eng)
	if err != nil {
		return err
	}

	// Storage (M6): object bytes in a BlobStore (local filesystem or S3), with
	// metadata and ownership authorization over the data engine.
	var store blob.Store
	if cfg.StorageBackend == "s3" {
		s3store, serr := blob.NewS3(blob.S3Config{
			Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		})
		if serr != nil {
			return serr
		}
		store = s3store
		logger.Info("using s3 blob store", "bucket", cfg.S3Bucket)
	} else {
		local, lerr := blob.NewLocal(cfg.StoragePath())
		if lerr != nil {
			return lerr
		}
		store = local
		logger.Info("using local blob store", "path", cfg.StoragePath())
	}
	storageSvc, err := storage.New(context.Background(), eng, store)
	if err != nil {
		return err
	}

	// Functions (M7): WASM (WASI) modules run in a wazero sandbox; module bytes
	// reuse the blob store. The runtime is closed on shutdown.
	fnRunner, err := functions.NewWazero(context.Background(), cfg.FnMaxMemoryMB)
	if err != nil {
		return err
	}
	defer fnRunner.Close(context.Background())
	fnSvc := functions.New(eng, store, fnRunner)

	// Seed an admin API key. For the solo tier we generate an ephemeral one and log
	// it (local-dev convenience). For any non-solo tier we refuse to boot without an
	// explicit key rather than mint-and-log a live admin credential: a per-restart
	// secret in production logs is both a leak and operationally useless.
	adminKey := cfg.AdminAPIKey
	if adminKey == "" {
		if cfg.EffectiveTier() != config.TierSolo {
			return fmt.Errorf("PS_ADMIN_API_KEY is required for the %q tier; refusing to generate and log an ephemeral admin credential in a non-solo deployment", cfg.EffectiveTier())
		}
		adminKey = "ps_sk_" + idgen.NewUUID()
		logger.Warn("PS_ADMIN_API_KEY not set; generated an ephemeral admin key for this solo-tier run",
			"admin_api_key", adminKey,
			"hint", "set PS_ADMIN_API_KEY to keep it stable and out of logs")
	}
	if err := authSvc.SeedAdminKey(context.Background(), adminKey); err != nil {
		return err
	}

	srv := httpapi.New(cfg, logger, httpapi.Deps{
		Catalog:   cat,
		Records:   rec,
		Policy:    pol,
		Meta:      mta,
		Auth:      authSvc,
		Docs:      agentdocs.New(),
		Memory:    memory.New(eng),
		Advisor:   advisor.New(cat, pol),
		Storage:   storageSvc,
		Functions: fnSvc,
		Bus:       bus,
		Enforcer:  enf,
		Engine:    eng,
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
