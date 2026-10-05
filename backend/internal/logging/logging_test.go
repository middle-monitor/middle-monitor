package logging

import (
	"context"
	"log/slog"
	"testing"
)

// Operational signals live at Debug since the slog migration. An operator has
// to be able to turn them back on without a rebuild, and they must stay off by
// default so a production process does not log every SQL latency.
func TestDebugSignalsAreReachableThroughTheEnvironment(t *testing.T) {
	ctx := context.Background()

	t.Setenv("LOG_LEVEL", "debug")
	Configure()
	if !slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Fatal("LOG_LEVEL=debug leaves the debug signals unreachable")
	}

	t.Setenv("LOG_LEVEL", "")
	Configure()
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Fatal("debug is on without being asked for")
	}
	if !slog.Default().Enabled(ctx, slog.LevelInfo) {
		t.Fatal("info is off by default")
	}
}

// An unreadable level must not silence the process.
func TestUnknownLevelFallsBackToInfo(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	Configure()
	if !slog.Default().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("an unknown LOG_LEVEL silenced info")
	}
}
