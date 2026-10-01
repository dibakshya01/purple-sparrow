// Package config loads and validates Orange Crow's runtime configuration from
// environment variables (prefix OC_) with sane defaults. Config is resolved once
// at boot; invalid values fail fast with actionable messages rather than booting
// partially.
package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	// AdminAPIKey (OC_ADMIN_API_KEY, prefix oc_sk_) grants project_admin. Empty in
	// config means "not set"; main generates an ephemeral one at boot for the solo
	// tier and logs it. M2 replaces this bridge with full auth.
	AdminAPIKey string
	// DatabaseURL (OC_DATABASE_URL) selects the Postgres engine when set; empty
	// uses the embedded SQLite engine. Carries credentials — never logged.
	DatabaseURL string
}

// DatabasePath returns the SQLite database file path within the data dir.
func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "orangecrow.db")
}

// Defaults returns the baseline configuration before env overrides.
//
// Addr defaults to loopback (secure by default): a solo dev's laptop server is
// not exposed on the LAN. Container/production deployments opt into a public bind
// explicitly via OC_ADDR (e.g. "0.0.0.0:8787"), which the Docker image sets.
func Defaults() Config {
	return Config{
		Addr:      "127.0.0.1:8787",
		LogLevel:  "info",
		LogFormat: LogJSON,
		DataDir:   "./.orangecrow",
		Tier:      TierAuto,
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

// Load reads OC_-prefixed environment variables over the defaults and validates
// the result.
func Load() (Config, error) {
	c := Defaults()

	if v := env("OC_ADDR"); v != "" {
		c.Addr = v
	}
	if v := env("OC_LOG_LEVEL"); v != "" {
		c.LogLevel = strings.ToLower(v)
	}
	if v := env("OC_LOG_FORMAT"); v != "" {
		c.LogFormat = LogFormat(strings.ToLower(v))
	}
	if v := env("OC_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := env("OC_TIER"); v != "" {
		c.Tier = Tier(strings.ToLower(v))
	}
	if v := env("OC_ADMIN_API_KEY"); v != "" {
		c.AdminAPIKey = v
	}
	if v := env("OC_DATABASE_URL"); v != "" {
		c.DatabaseURL = v
	} else if v := env("OC_POSTGRES_DSN"); v != "" {
		c.DatabaseURL = v
	}

	return c, c.Validate()
}

// Validate checks the config and returns the first problem found.
func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("OC_ADDR must not be empty (e.g. \":8787\")")
	}
	if !validLogLevel(c.LogLevel) {
		return fmt.Errorf("OC_LOG_LEVEL %q is invalid; use one of debug, info, warn, error", c.LogLevel)
	}
	if !c.LogFormat.valid() {
		return fmt.Errorf("OC_LOG_FORMAT %q is invalid; use json or text", c.LogFormat)
	}
	if !c.Tier.valid() {
		return fmt.Errorf("OC_TIER %q is invalid; use auto, solo, startup, or enterprise", c.Tier)
	}
	if c.DataDir == "" {
		return fmt.Errorf("OC_DATA_DIR must not be empty")
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
