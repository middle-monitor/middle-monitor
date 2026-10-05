// Package logging installs the process-wide slog handler.
package logging

import (
	"log/slog"
	"os"
)

// Configure installs a JSON handler on stderr at the level named by LOG_LEVEL
// (debug, info, warn, error; info when unset or unreadable). Without it slog
// falls back to the text handler of the standard logger, which drops every
// Debug line: agent metrics, indexed traces, HTTP and SQL latencies.
func Configure() {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}
