// Package observability wires structured logging (and, later, metrics and
// tracing) for Orange Crow.
package observability

import (
	"log/slog"
	"os"

	"github.com/dibakshya01/orange-crow/internal/config"
)

// NewLogger builds a slog.Logger for the given level and format. Unknown levels
// default to info (validation happens in config, so this is a safety net).
func NewLogger(level string, format config.LogFormat) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	switch format {
	case config.LogText:
		h = slog.NewTextHandler(os.Stdout, opts)
	default:
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
