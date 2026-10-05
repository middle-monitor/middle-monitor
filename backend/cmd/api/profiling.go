package main

import (
	"context"
	"log/slog"
	"time"

	middlemonitor "github.com/middle-monitor/sdk-go"
)

const (
	profileInterval = 10 * time.Minute
	cpuProfileTime  = 30 * time.Second
)

// startSelfProfiling uploads CPU and heap profiles to Middle-Monitor on an
// interval, through the public Go SDK like any instrumented client. The SDK
// profiles this process directly, so no pprof socket is exposed.
func startSelfProfiling() {
	go func() {
		client := middlemonitor.GetGlobalClient()
		if client == nil {
			slog.Warn("self-profiling disabled, sdk client unavailable")
			return
		}
		for {
			ctx := context.Background()
			if err := client.CaptureCPUProfile(ctx, cpuProfileTime); err != nil {
				slog.Error("cpu profile upload failed", "error", err)
			}
			if err := client.CaptureHeapProfile(ctx); err != nil {
				slog.Error("heap profile upload failed", "error", err)
			}
			time.Sleep(profileInterval)
		}
	}()
}
