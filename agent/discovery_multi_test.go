package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const nomadMixedServices = `[{"Namespace":"default","Services":[
  {"ServiceName":"minio","Tags":["fqdn:files.example.com"]},
  {"ServiceName":"traefik","Tags":["fqdn:edge.example.com"]},
  {"ServiceName":"caddy","Tags":[]}
]}]`

func fakeNomadPerService(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/services":
			w.Write([]byte(nomadMixedServices))
		case strings.HasPrefix(r.URL.Path, "/v1/service/"):
			name := strings.TrimPrefix(r.URL.Path, "/v1/service/")
			w.Write([]byte(`[{"ServiceName":"` + name + `","Address":"10.0.1.5","Port":8080,"Tags":["fqdn:` + name + `.example.com"],"Namespace":"default"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func urlOf(targets []ScrapeTarget) string {
	if len(targets) == 0 {
		return ""
	}
	return targets[0].URL
}

// Each piece of software decides where it serves its metrics, so a single
// global metrics_path cannot cover a real cluster.
func TestNomadBlockCarriesItsOwnMetricsPath(t *testing.T) {
	server := fakeNomadPerService(t)

	minio, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Service: "^minio$", MetricsPath: "/minio/v2/metrics/cluster"})
	if err != nil {
		t.Fatalf("minio discovery: %v", err)
	}
	if len(minio) != 1 || !strings.HasSuffix(urlOf(minio), "/minio/v2/metrics/cluster") {
		t.Fatalf("minio target %+v", minio)
	}
}

// A service name regex is what lets one block describe one family of services.
func TestNomadServiceRegexFiltersServices(t *testing.T) {
	server := fakeNomadPerService(t)

	targets, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Service: "^(caddy|traefik)$"})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want caddy and traefik: %+v", len(targets), targets)
	}
}

// Traefik registers on its application port and serves metrics on another one,
// so the registered port has to be overridable.
func TestNomadPortOverride(t *testing.T) {
	server := fakeNomadPerService(t)

	targets, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Service: "^traefik$", Port: 8081})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if !strings.Contains(urlOf(targets), ":8081/") {
		t.Fatalf("target %q does not use the overridden port", urlOf(targets))
	}
	if targets[0].Labels["instance"] != "10.0.1.5:8081" {
		t.Fatalf("instance label %q should follow the scraped port", targets[0].Labels["instance"])
	}
}

// Nomad tags are where operators put the information the platform has no field
// for; without a mapping rule that information is lost.
func TestNomadTagLabels(t *testing.T) {
	server := fakeNomadPerService(t)

	targets, err := DiscoverNomad(context.Background(), server.Client(), NomadConfig{
		Address:   server.URL,
		Service:   "^minio$",
		TagLabels: []TagLabel{{Prefix: "fqdn:", Label: "fqdn"}},
	})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if targets[0].Labels["fqdn"] != "minio.example.com" {
		t.Fatalf("fqdn label %q", targets[0].Labels["fqdn"])
	}
}

// Agents already deployed carry the single-block form; upgrading the binary
// must not silently stop their discovery.
func TestNomadSingleBlockFormStillParses(t *testing.T) {
	var cfg ScrapeConfig
	err := yaml.Unmarshal([]byte(`
nomad:
  address: http://localhost:4646
  tag: middle-monitor
  metrics_path: /metrics
`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Nomad) != 1 || cfg.Nomad[0].Tag != "middle-monitor" {
		t.Fatalf("single block parsed as %+v", cfg.Nomad)
	}
	if !cfg.usesDiscovery() {
		t.Fatal("a configured single block must count as discovery")
	}
}

// The old example file ships an empty nomad block, which meant "off".
func TestNomadEmptyBlockIsOff(t *testing.T) {
	var cfg ScrapeConfig
	if err := yaml.Unmarshal([]byte("nomad:\n  address: \"\"\n  tag: middle-monitor\n"), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.usesDiscovery() {
		t.Fatalf("an empty address must not turn discovery on: %+v", cfg.Nomad)
	}
}

func TestNomadListFormParses(t *testing.T) {
	var cfg ScrapeConfig
	err := yaml.Unmarshal([]byte(`
nomad:
  - address: http://localhost:4646
    service: '^minio$'
    metrics_path: /minio/v2/metrics/cluster
  - address: http://localhost:4646
    service: '^traefik$'
    port: 8081
    tag_labels:
      - prefix: 'fqdn:'
        label: fqdn
`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Nomad) != 2 {
		t.Fatalf("got %d blocks, want 2", len(cfg.Nomad))
	}
	if cfg.Nomad[1].Port != 8081 || cfg.Nomad[1].TagLabels[0].Label != "fqdn" {
		t.Fatalf("second block parsed as %+v", cfg.Nomad[1])
	}
}

// http_sd is the escape hatch: any orchestrator can be described by a list the
// operator produces, without the agent learning about it.
func TestDiscoverHTTPReadsTheStandardFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sd-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`[
		  {"targets":["10.0.1.5:9100"],"labels":{"job":"node","project":"demo"}},
		  {"targets":["10.0.1.6:8080"],"labels":{"job":"minio","__metrics_path__":"/minio/v2/metrics/cluster"}}
		]`))
	}))
	defer server.Close()

	targets, err := DiscoverHTTP(context.Background(), server.Client(), HTTPSDConfig{
		URL:         server.URL,
		BearerToken: "sd-token",
	})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}

	byName := map[string]ScrapeTarget{}
	for _, target := range targets {
		byName[target.Labels["job"]] = target
	}
	if byName["node"].URL != "http://10.0.1.5:9100/metrics" {
		t.Fatalf("node url %q", byName["node"].URL)
	}
	// The reserved label directs the scraper; it must not survive as a series
	// dimension.
	if byName["minio"].URL != "http://10.0.1.6:8080/minio/v2/metrics/cluster" {
		t.Fatalf("minio url %q", byName["minio"].URL)
	}
	if _, leaked := byName["minio"].Labels["__metrics_path__"]; leaked {
		t.Fatalf("reserved label leaked into the series: %+v", byName["minio"].Labels)
	}
	if byName["node"].Labels["project"] != "demo" || byName["node"].Labels["instance"] != "10.0.1.5:9100" {
		t.Fatalf("labels %+v", byName["node"].Labels)
	}
}

// A source that answers with an error must not be read as an empty list, or
// every target it feeds would be torn down.
func TestDiscoverHTTPFailsLoudly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if _, err := DiscoverHTTP(context.Background(), server.Client(), HTTPSDConfig{URL: server.URL}); err == nil {
		t.Fatal("expected an error for a failing http_sd endpoint")
	}
}

// One failing source must not drop the targets of the healthy ones.
func TestDiscoverKeepsTargetsWhenHTTPSDFails(t *testing.T) {
	m := &scrapeManager{
		cfg:     ScrapeConfig{HTTPSD: []HTTPSDConfig{{URL: "http://127.0.0.1:1/sd"}}},
		running: map[string]*runningTarget{},
	}

	targets, ok := m.discover(context.Background(), http.DefaultClient)
	if ok || targets != nil {
		t.Fatalf("expected a reported failure, got %+v ok=%v", targets, ok)
	}
}
