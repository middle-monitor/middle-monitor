package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func configServer(t *testing.T, fragment, version string, seen *http.Header) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		if r.URL.Query().Get("host") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"scrape_config": fragment,
			"updated_at":    version,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func agentConfigFor(url string) *AgentConfig {
	config := &AgentConfig{}
	config.API.URL = url
	config.API.APIKey = "install-token"
	config.Host.Name = "web-prod-01"
	return config
}

// The point of the feature: a target added on the platform reaches the machine
// without anyone editing a file on it.
func TestRemoteTargetsAreFetched(t *testing.T) {
	var seen http.Header
	server := configServer(t, "- name: remote\n  url: http://localhost:9101/metrics\n", "2026-09-02T10:00:00Z", &seen)

	targets, version, err := fetchRemoteTargets(context.Background(), server.Client(), agentConfigFor(server.URL))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "remote" {
		t.Fatalf("targets %+v", targets)
	}
	if version != "2026-09-02T10:00:00Z" {
		t.Fatalf("version %q, the agent needs it to skip an unchanged config", version)
	}
	// The install token is the agent's only credential; without it the platform
	// cannot tell which organization is asking.
	if seen.Get("X-Install-Token") != "install-token" {
		t.Fatalf("token header %q", seen.Get("X-Install-Token"))
	}
}

// The `targets:` block form has to work too, since it is one of the two shapes
// the local fragments accept.
func TestRemoteConfigAcceptsTheTargetsBlock(t *testing.T) {
	server := configServer(t, "targets:\n  - name: remote\n    url: http://localhost:9101/metrics\n", "v1", nil)

	targets, _, err := fetchRemoteTargets(context.Background(), server.Client(), agentConfigFor(server.URL))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets %+v", targets)
	}
}

// An empty configuration is the normal state of most hosts and must not be an
// error.
func TestEmptyRemoteConfigIsNotAnError(t *testing.T) {
	server := configServer(t, "", "", nil)

	targets, _, err := fetchRemoteTargets(context.Background(), server.Client(), agentConfigFor(server.URL))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("targets %+v", targets)
	}
}

// A platform that answers an error must not take the running scrape down with
// it: the agent keeps what it has.
func TestRemoteConfigFailureIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if _, _, err := fetchRemoteTargets(context.Background(), server.Client(), agentConfigFor(server.URL)); err == nil {
		t.Fatal("expected an error for a failing endpoint")
	}
}

// A target written on the machine must not be replaced by one the platform
// happens to name the same way.
func TestLocalTargetsWinOverRemoteOnes(t *testing.T) {
	local := ScrapeTarget{Name: "node", URL: "http://from-machine:9100/metrics"}
	remote := ScrapeTarget{Name: "node", URL: "http://from-platform:9100/metrics"}

	merged := dedupByName([]ScrapeTarget{local, remote})
	if len(merged) != 1 || merged[0].URL != local.URL {
		t.Fatalf("merged %+v, the local target must win", merged)
	}
}

// Remote configuration stays off unless the operator turns it on: an upgrade
// must not make an agent start taking instructions from the network.
func TestRemoteConfigIsOffByDefault(t *testing.T) {
	var config AgentConfig
	if err := yaml.Unmarshal([]byte("api:\n  url: https://api.middlemonitor.io\n"), &config); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if config.RemoteConfig.Enabled {
		t.Fatal("remote config must be opt-in")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := StartScraping(ctx, ScrapeConfig{})
	t.Cleanup(func() { cancel(); manager.Wait() })
	// Must return without starting anything.
	StartRemoteConfig(ctx, manager, &config)
}

func TestRemoteConfigIntervalDefaults(t *testing.T) {
	if got := (RemoteConfigConfig{}).interval(); got != 5*time.Minute {
		t.Fatalf("interval %v, want 5m", got)
	}
	if got := (RemoteConfigConfig{Interval: 30}).interval(); got != 30*time.Second {
		t.Fatalf("interval %v, want 30s", got)
	}
}

// The whole loop: the platform serves a target and the agent starts scraping it.
func TestRemoteTargetIsScraped(t *testing.T) {
	withRecordingExporter(t)
	exporter, hits := countingTarget(t)
	server := configServer(t, "- name: remote\n  url: "+exporter.URL+"\n", "v1", nil)

	config := agentConfigFor(server.URL)
	config.RemoteConfig = RemoteConfigConfig{Enabled: true, Interval: 3600}
	config.Scrape = ScrapeConfig{Enabled: true, Interval: 3600}

	ctx, cancel := context.WithCancel(context.Background())
	manager := StartScraping(ctx, config.Scrape)
	t.Cleanup(func() { cancel(); manager.Wait() })

	StartRemoteConfig(ctx, manager, config)
	waitForHits(t, hits, 1)
}
