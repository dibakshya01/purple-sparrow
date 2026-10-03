// Package config loads and validates Purple Sparrow's runtime configuration from
// environment variables (prefix PS_) with sane defaults. Config is resolved once
// at boot; invalid values fail fast with actionable messages rather than booting
// partially.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Tier selects the deployment profile that decides which adapters are wired.
type Tier string

const (
	TierAuto       Tier = "auto"
	TierSolo       Tier = "solo"       // single binary, embedded SQLite, local everything
	TierStartup    Tier = "startup"    // external Postgres, local/S3 blobs
	TierEnterprise Tier = "enterprise" // Postgres HA, S3, external event bus
)

func (t Tier) valid() bool {
	switch t {
	case TierAuto, TierSolo, TierStartup, TierEnterprise:
		return true
	default:
		return false
	}
}

// LogFormat selects the log encoder.
type LogFormat string

const (
	LogJSON LogFormat = "json"
	LogText LogFormat = "text"
)

func (f LogFormat) valid() bool { return f == LogJSON || f == LogText }

// Config is the fully-resolved, validated runtime configuration.
type Config struct {
	Addr      string
	LogLevel  string
	LogFormat LogFormat
	DataDir   string
	Tier      Tier
	// AdminAPIKey (PS_ADMIN_API_KEY, prefix ps_sk_) grants project_admin. Empty in
	// config means "not set"; main generates an ephemeral one at boot for the solo
	// tier and logs it. M2 replaces this bridge with full auth.
	AdminAPIKey string
	// DatabaseURL (PS_DATABASE_URL) selects the Postgres engine when set; empty
	// uses the embedded SQLite engine. Carries credentials — never logged.
	DatabaseURL string

	// --- Storage (M6) ---
	// StorageBackend selects the blob adapter: "local" (default) or "s3".
	StorageBackend string
	// StorageMaxObjectBytes caps a single uploaded object (default 100 MiB).
	StorageMaxObjectBytes int64
	// S3* configure the S3-compatible adapter (used when StorageBackend == "s3").
	// Secret/access keys carry credentials — never logged.
	S3Endpoint, S3Region, S3Bucket, S3AccessKey, S3SecretKey string

	// --- Functions (M7) ---
	// FnMaxMemoryMB caps the WASM runtime's linear memory (default 128).
	FnMaxMemoryMB int

	// --- Hardening (M10) ---
	// RateLimitRPS is the per-client-IP request rate (0 disables; default 100).
	RateLimitRPS int
	// RateLimitBurst is the per-client-IP burst allowance (default 200).
	RateLimitBurst int
}

// StoragePath returns the local blob root within the data dir.
func (c Config) StoragePath() string { return filepath.Join(c.DataDir, "storage") }

// DatabasePath returns the SQLite database file path within the data dir.
func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "purplesparrow.db")
}

// Defaults returns the baseline configuration before env overrides.
//
// Addr defaults to loopback (secure by default): a solo dev's laptop server is
// not exposed on the LAN. Container/production deployments opt into a public bind
// explicitly via PS_ADDR (e.g. "0.0.0.0:8787"), which the Docker image sets.
func Defaults() Config {
	return Config{
		Addr:                  "127.0.0.1:8787",
		LogLevel:              "info",
		LogFormat:             LogJSON,
		DataDir:               "./.purplesparrow",
		Tier:                  TierAuto,
		StorageBackend:        "local",
		StorageMaxObjectBytes: 100 << 20, // 100 MiB
		FnMaxMemoryMB:         128,
		RateLimitRPS:          100,
		RateLimitBurst:        200,
	}
}

// EffectiveTier resolves TierAuto to a concrete tier. "auto" means the single-
// binary solo profile unless a Postgres DSN is configured, which implies a
// non-solo (startup) deployment — and therefore requires an explicit admin key.
func (c Config) EffectiveTier() Tier {
	if c.Tier != TierAuto {
		return c.Tier
	}
	if c.DatabaseURL != "" {
		return TierStartup
	}
	return TierSolo
}

// Load reads PS_-prefixed environment variables over the defaults and validates
// the result.
func Load() (Config, error) {
	c := Defaults()

	if v := env("PS_ADDR"); v != "" {
		c.Addr = v
	}
	if v := env("PS_LOG_LEVEL"); v != "" {
		c.LogLevel = strings.ToLower(v)
	}
	if v := env("PS_LOG_FORMAT"); v != "" {
		c.LogFormat = LogFormat(strings.ToLower(v))
	}
	if v := env("PS_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := env("PS_TIER"); v != "" {
		c.Tier = Tier(strings.ToLower(v))
	}
	if v := env("PS_ADMIN_API_KEY"); v != "" {
		c.AdminAPIKey = v
	}
	if v := env("PS_DATABASE_URL"); v != "" {
		c.DatabaseURL = v
	} else if v := env("PS_POSTGRES_DSN"); v != "" {
		c.DatabaseURL = v
	}

	if v := env("PS_STORAGE_BACKEND"); v != "" {
		c.StorageBackend = strings.ToLower(v)
	}
	if v := env("PS_STORAGE_MAX_OBJECT_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("PS_STORAGE_MAX_OBJECT_BYTES %q is invalid; use a positive integer (bytes)", v)
		}
		c.StorageMaxObjectBytes = n
	}
	c.S3Endpoint = env("PS_S3_ENDPOINT")
	c.S3Region = env("PS_S3_REGION")
	c.S3Bucket = env("PS_S3_BUCKET")
	c.S3AccessKey = env("PS_S3_ACCESS_KEY_ID")
	c.S3SecretKey = env("PS_S3_SECRET_ACCESS_KEY")

	if v := env("PS_FN_MAX_MEMORY_MB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("PS_FN_MAX_MEMORY_MB %q is invalid; use a positive integer (MiB)", v)
		}
		c.FnMaxMemoryMB = n
	}
	if v := env("PS_RATE_LIMIT_RPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c, fmt.Errorf("PS_RATE_LIMIT_RPS %q is invalid; use a non-negative integer (0 disables)", v)
		}
		c.RateLimitRPS = n
	}
	if v := env("PS_RATE_LIMIT_BURST"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("PS_RATE_LIMIT_BURST %q is invalid; use a positive integer", v)
		}
		c.RateLimitBurst = n
	}

	return c, c.Validate()
}

// Validate checks the config and returns the first problem found.
func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("PS_ADDR must not be empty (e.g. \":8787\")")
	}
	if !validLogLevel(c.LogLevel) {
		return fmt.Errorf("PS_LOG_LEVEL %q is invalid; use one of debug, info, warn, error", c.LogLevel)
	}
	if !c.LogFormat.valid() {
		return fmt.Errorf("PS_LOG_FORMAT %q is invalid; use json or text", c.LogFormat)
	}
	if !c.Tier.valid() {
		return fmt.Errorf("PS_TIER %q is invalid; use auto, solo, startup, or enterprise", c.Tier)
	}
	if c.DataDir == "" {
		return fmt.Errorf("PS_DATA_DIR must not be empty")
	}
	if c.StorageBackend != "local" && c.StorageBackend != "s3" {
		return fmt.Errorf("PS_STORAGE_BACKEND %q is invalid; use local or s3", c.StorageBackend)
	}
	if c.StorageBackend == "s3" && (c.S3Endpoint == "" || c.S3Bucket == "" || c.S3AccessKey == "" || c.S3SecretKey == "") {
		return fmt.Errorf("PS_STORAGE_BACKEND=s3 requires PS_S3_ENDPOINT, PS_S3_BUCKET, PS_S3_ACCESS_KEY_ID, and PS_S3_SECRET_ACCESS_KEY")
	}
	return nil
}

func validLogLevel(l string) bool {
	switch l {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func env(key string) string { return strings.TrimSpace(os.Getenv(key)) }
