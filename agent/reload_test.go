package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// countingTarget serves one series and counts how many times it was scraped.
func countingTarget(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Write([]byte("up 1\n"))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func waitForHits(t *testing.T, hits *int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(hits) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("got %d scrapes, want %d", atomic.LoadInt64(hits), want)
}

// startManager stops its loops before the test's exporter is restored, so a
// cancelled loop cannot still be exporting while the cleanup runs.
func startManager(t *testing.T, cfg ScrapeConfig) (*scrapeManager, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m := StartScraping(ctx, cfg)
	t.Cleanup(func() {
		cancel()
		m.Wait()
	})
	return m, ctx
}

func runningCount(m *scrapeManager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running)
}

// The point of SIGHUP: the target list changes far more often than the binary,
// and a restart would drop every running scrape cycle.
func TestReloadPicksUpANewTarget(t *testing.T) {
	withRecordingExporter(t)
	server, hits := countingTarget(t)
	cfg := ScrapeConfig{Enabled: true, Interval: 3600}
	m, ctx := startManager(t, cfg)
	if runningCount(m) != 0 {
		t.Fatal("nothing should be running yet")
	}

	cfg.Targets = []ScrapeTarget{{Name: "node", URL: server.URL}}
	m.Reload(ctx, cfg)

	waitForHits(t, hits, 1)
	if runningCount(m) != 1 {
		t.Fatalf("%d loops running, want 1", runningCount(m))
	}
}

// A target removed from the config must stop being scraped.
func TestReloadStopsARemovedTarget(t *testing.T) {
	withRecordingExporter(t)
	server, hits := countingTarget(t)
	cfg := ScrapeConfig{Enabled: true, Interval: 3600, Targets: []ScrapeTarget{{Name: "node", URL: server.URL}}}
	m, ctx := startManager(t, cfg)
	waitForHits(t, hits, 1)

	cfg.Targets = nil
	m.Reload(ctx, cfg)

	if runningCount(m) != 0 {
		t.Fatalf("%d loops still running, want 0", runningCount(m))
	}
}

// The subtle case: the URL is unchanged but the filters or credentials are not.
// The loop compiled them at start, so it has to be restarted or the reload is a
// silent no-op.
func TestReloadRestartsAChangedTarget(t *testing.T) {
	withRecordingExporter(t)
	server, hits := countingTarget(t)
	target := ScrapeTarget{Name: "node", URL: server.URL}
	cfg := ScrapeConfig{Enabled: true, Interval: 3600, Targets: []ScrapeTarget{target}}
	m, ctx := startManager(t, cfg)
	waitForHits(t, hits, 1)

	// Same config: no restart, so no second scrape.
	m.Reload(ctx, cfg)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("%d scrapes after an identical reload, want 1", got)
	}

	changed := target
	changed.DropMetrics = []string{"^go_.*"}
	cfg.Targets = []ScrapeTarget{changed}
	m.Reload(ctx, cfg)

	waitForHits(t, hits, 2)
}

// Turning scraping off has to stop the loops, not just stop adding to them.
func TestReloadDisablingStopsEverything(t *testing.T) {
	withRecordingExporter(t)
	server, hits := countingTarget(t)
	cfg := ScrapeConfig{Enabled: true, Interval: 3600, Targets: []ScrapeTarget{{Name: "node", URL: server.URL}}}
	m, ctx := startManager(t, cfg)
	waitForHits(t, hits, 1)

	cfg.Enabled = false
	m.Reload(ctx, cfg)

	if runningCount(m) != 0 {
		t.Fatalf("%d loops still running after disabling", runningCount(m))
	}
}

// An agent started with scraping off must be able to turn it on with a reload,
// without the restart the operator was trying to avoid.
func TestReloadCanEnableAnAgentStartedDisabled(t *testing.T) {
	withRecordingExporter(t)
	server, hits := countingTarget(t)
	cfg := ScrapeConfig{Enabled: false, Interval: 3600, Targets: []ScrapeTarget{{Name: "node", URL: server.URL}}}
	m, ctx := startManager(t, cfg)

	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(hits); got != 0 {
		t.Fatalf("%d scrapes while disabled, want 0", got)
	}

	cfg.Enabled = true
	m.Reload(ctx, cfg)
	waitForHits(t, hits, 1)
}

// A reload must not leave the previous discovery loop running: two of them would
// fight over the target set with two different configurations.
func TestReloadReplacesTheDiscoveryLoop(t *testing.T) {
	withRecordingExporter(t)
	cfg := ScrapeConfig{Enabled: true, Nomad: NomadConfigs{{Address: "http://127.0.0.1:1"}}}
	m, ctx := startManager(t, cfg)

	m.mu.Lock()
	first := m.discoveryCancel
	m.mu.Unlock()
	if first == nil {
		t.Fatal("discovery should be running")
	}

	cfg.Nomad = nil
	m.Reload(ctx, cfg)

	m.mu.Lock()
	second := m.discoveryCancel
	m.mu.Unlock()
	if second != nil {
		t.Fatal("discovery should have been stopped with its source")
	}
}
