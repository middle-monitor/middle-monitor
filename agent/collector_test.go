package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakePlatform plays the receiver's two collector endpoints.
type fakePlatform struct {
	mu       sync.Mutex
	checks   []collectorCheck
	synced   []collectorSyncRequest
	results  []collectorResult
	failPost bool
}

func (p *fakePlatform) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/collectors/sync", func(w http.ResponseWriter, r *http.Request) {
		var req collectorSyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode sync: %v", err)
		}
		p.mu.Lock()
		p.synced = append(p.synced, req)
		checks := p.checks
		p.mu.Unlock()
		json.NewEncoder(w).Encode(collectorSyncResponse{Checks: checks, RefreshSeconds: 1})
	})
	mux.HandleFunc("/api/v1/collectors/results", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.failPost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var batch collectorResultBatch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("decode results: %v", err)
		}
		p.results = append(p.results, batch.Results...)
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

func (p *fakePlatform) set(f func(p *fakePlatform)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

func (p *fakePlatform) received() []collectorResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]collectorResult(nil), p.results...)
}

func startTestCollector(t *testing.T, platform *fakePlatform, commands map[string]Command) *Collector {
	t.Helper()
	server := httptest.NewServer(platform.handler(t))
	t.Cleanup(server.Close)
	config := &AgentConfig{}
	config.API.URL = server.URL
	config.Host.Name = "sat1"
	config.Collector = CollectorConfig{Enabled: true, Zone: "paris", commands: commands}
	c := newCollector(config)
	c.flushEvery = 50 * time.Millisecond
	c.start()
	t.Cleanup(c.Stop)
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCollectorRunsAssignedChecksAndPostsResults(t *testing.T) {
	plugin := writePlugin(t, `echo "DISK WARNING - $1 | used=80%;70;90"; exit 1`)
	platform := &fakePlatform{checks: []collectorCheck{{ID: 42, Command: "disk", Vars: map[string][]string{"path": {"/var"}}, Interval: 1}}}
	startTestCollector(t, platform, map[string]Command{"disk": {Path: plugin, Args: []string{"$path$"}}})

	waitFor(t, "a posted result", func() bool { return len(platform.received()) > 0 })
	got := platform.received()[0]
	if got.ServiceID != 42 || got.State != stateWarning || got.Output != "DISK WARNING - /var" {
		t.Fatalf("got %+v", got)
	}
	if len(got.Perfdata) != 1 || got.Perfdata[0].Value != 80 {
		t.Fatalf("perfdata %+v", got.Perfdata)
	}
	platform.set(func(p *fakePlatform) {
		// The platform lists the commands a zone can offer from this.
		if p.synced[0].Zone != "paris" || len(p.synced[0].Commands) != 1 || p.synced[0].Commands[0] != "disk" {
			t.Errorf("sync request %+v", p.synced[0])
		}
	})
}

// A check created in the UI for a command the satellite does not declare must
// show up as unknown with the reason, not stay silently without data.
func TestUndeclaredCommandIsReportedUnknown(t *testing.T) {
	platform := &fakePlatform{checks: []collectorCheck{{ID: 7, Command: "check_missing", Interval: 1}}}
	startTestCollector(t, platform, map[string]Command{})

	waitFor(t, "a posted result", func() bool { return len(platform.received()) > 0 })
	if got := platform.received()[0]; got.State != stateUnknown || got.Output == "" {
		t.Fatalf("got %+v, want unknown with a reason", got)
	}
}

// A check deleted in the UI must stop polling the machine.
func TestRemovedCheckStops(t *testing.T) {
	plugin := writePlugin(t, "echo OK")
	platform := &fakePlatform{checks: []collectorCheck{{ID: 1, Command: "ok", Interval: 1}}}
	startTestCollector(t, platform, map[string]Command{"ok": {Path: plugin}})

	waitFor(t, "a first result", func() bool { return len(platform.received()) > 0 })
	platform.set(func(p *fakePlatform) { p.checks = nil })
	time.Sleep(2500 * time.Millisecond) // one refresh to drop it, one interval to drain
	before := len(platform.received())
	time.Sleep(2 * time.Second)
	if after := len(platform.received()); after != before {
		t.Fatalf("check still running after removal: %d results then %d", before, after)
	}
}

// A platform restart must delay results, not lose them: what ran during the
// outage arrives once the platform answers again.
func TestResultsSurviveAPlatformOutage(t *testing.T) {
	plugin := writePlugin(t, "echo OK")
	platform := &fakePlatform{failPost: true, checks: []collectorCheck{{ID: 1, Command: "ok", Interval: 1}}}
	startTestCollector(t, platform, map[string]Command{"ok": {Path: plugin}})

	time.Sleep(1500 * time.Millisecond)
	// The first run lands within the first second, so a result this old was
	// refused by at least a few flushes before the platform came back.
	outageEnd := time.Now().UTC().Add(-400 * time.Millisecond)
	platform.set(func(p *fakePlatform) { p.failPost = false })

	waitFor(t, "results from the outage", func() bool {
		for _, r := range platform.received() {
			if r.ExecutedAt.Before(outageEnd) {
				return true
			}
		}
		return false
	})
}
