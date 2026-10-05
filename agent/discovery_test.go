package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeNomad serves the two endpoints discovery uses.
func fakeNomad(t *testing.T, services, instances string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/services":
			w.Write([]byte(services))
		case strings.HasPrefix(r.URL.Path, "/v1/service/"):
			w.Write([]byte(instances))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

const nomadServices = `[{"Namespace":"default","Services":[
  {"ServiceName":"checkout","Tags":["middle-monitor","http"]},
  {"ServiceName":"batch","Tags":["internal"]}
]}]`

const nomadInstances = `[
  {"ServiceName":"checkout","Address":"10.0.1.5","Port":9102,"JobID":"checkout-job","AllocID":"abc123","Namespace":"default"},
  {"ServiceName":"checkout","Address":"10.0.1.6","Port":9102,"JobID":"checkout-job","AllocID":"def456","Namespace":"default"}
]`

func TestDiscoverNomadBuildsOneTargetPerInstance(t *testing.T) {
	server := fakeNomad(t, nomadServices, nomadInstances)
	defer server.Close()

	targets, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Tag: "middle-monitor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(targets), targets)
	}

	if targets[0].URL != "http://10.0.1.5:9102/metrics" {
		t.Errorf("url: got %s", targets[0].URL)
	}
	// The job and allocation are what make a series traceable back to Nomad.
	for _, want := range []struct{ key, value string }{
		{"nomad_service", "checkout"},
		{"nomad_job", "checkout-job"},
		{"instance", "10.0.1.5:9102"},
	} {
		if got := targets[0].Labels[want.key]; got != want.value {
			t.Errorf("label %s: got %q, want %q", want.key, got, want.value)
		}
	}
	// Two instances of the same service must not collide into one target.
	if targets[0].URL == targets[1].URL {
		t.Error("both instances produced the same target")
	}
}

// The tag is how an operator says which services to scrape; ignoring it would
// scrape the whole cluster.
func TestDiscoverNomadHonoursTheTagFilter(t *testing.T) {
	server := fakeNomad(t, nomadServices, nomadInstances)
	defer server.Close()

	targets, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Tag: "does-not-exist"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("got %d targets, want none: %+v", len(targets), targets)
	}
}

func TestDiscoverNomadUsesConfiguredSchemeAndPath(t *testing.T) {
	server := fakeNomad(t, nomadServices, nomadInstances)
	defer server.Close()

	targets, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Tag: "middle-monitor", Scheme: "https", MetricsPath: "/internal/metrics"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) == 0 || targets[0].URL != "https://10.0.1.5:9102/internal/metrics" {
		t.Fatalf("got %+v", targets)
	}
}

// An instance with no address or port cannot be scraped; keeping it would make
// every cycle log a failure for a target that never existed.
func TestDiscoverNomadSkipsIncompleteInstances(t *testing.T) {
	server := fakeNomad(t, nomadServices,
		`[{"ServiceName":"checkout","Address":"","Port":0},{"ServiceName":"checkout","Address":"10.0.1.9","Port":9102}]`)
	defer server.Close()

	targets, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Tag: "middle-monitor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
	}
}

func TestDiscoverNomadReportsAnUnreachableCluster(t *testing.T) {
	_, err := DiscoverNomad(context.Background(), &http.Client{Timeout: time.Second},
		NomadConfig{Address: "http://127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected an error from an unreachable Nomad")
	}
}

// A non-200 answer must carry the status code as structured data, not just in
// a formatted string, so a caller can react to it with errors.As.
func TestNomadRequestReturnsATypedStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := nomadRequest(context.Background(), http.DefaultClient, NomadConfig{Address: server.URL}, "/v1/services", &struct{}{})

	var statusErr *nomadRequestError
	if !errors.As(err, &statusErr) {
		t.Fatalf("got %v (%T), want a *nomadRequestError", err, err)
	}
	if statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want %d", statusErr.StatusCode, http.StatusServiceUnavailable)
	}
}

// Discovery is opt-in per source: no address means no discovery, not an error.
func TestDiscoverNomadIsInertWithoutAnAddress(t *testing.T) {
	targets, err := DiscoverNomad(context.Background(), http.DefaultClient, NomadConfig{})
	if err != nil || len(targets) != 0 {
		t.Fatalf("got %+v / %v", targets, err)
	}
}

// A Nomad API behind a private CA is the common case in a cluster: without
// tls_config on the discovery block the whole cluster is unreachable, however
// well the static targets are configured.
func TestDiscoverNomadUsesItsOwnTLSConfig(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/services":
			w.Write([]byte(nomadServices))
		case strings.HasPrefix(r.URL.Path, "/v1/service/"):
			w.Write([]byte(nomadInstances))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := NomadConfig{Address: server.URL, Tag: "middle-monitor"}
	// The default client has no reason to trust the test certificate.
	if _, err := DiscoverNomad(context.Background(), &http.Client{Timeout: time.Second}, cfg); err == nil {
		t.Fatal("expected the untrusted certificate to fail discovery")
	}

	cfg.TLSConfig = &TLSConfig{InsecureSkipVerify: true}
	targets, err := DiscoverNomad(context.Background(), &http.Client{Timeout: time.Second}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}
}

// Block labels are the only place to tag the targets of one discovery block:
// scrape.labels applies to every target of the agent.
func TestDiscoverNomadAppliesBlockLabels(t *testing.T) {
	server := fakeNomad(t, nomadServices, nomadInstances)
	defer server.Close()

	targets, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Tag: "middle-monitor", Labels: map[string]string{"project": "checkout"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := targets[0].Labels["project"]; got != "checkout" {
		t.Errorf("label project: got %q, want %q", got, "checkout")
	}
	// The discovered dimensions must survive the added labels.
	if targets[0].Labels["nomad_service"] != "checkout" {
		t.Error("block labels overwrote the discovered labels")
	}
}

// Without the region the query reaches whichever region answers, which is not
// the one the operator configured.
func TestDiscoverNomadSendsTheRegion(t *testing.T) {
	seen := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.Query().Get("region")
		switch {
		case r.URL.Path == "/v1/services":
			w.Write([]byte(nomadServices))
		default:
			w.Write([]byte(nomadInstances))
		}
	}))
	defer server.Close()

	if _, err := DiscoverNomad(context.Background(), server.Client(),
		NomadConfig{Address: server.URL, Tag: "middle-monitor", Region: "eu-west"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := <-seen; got != "eu-west" {
		t.Errorf("region: got %q, want %q", got, "eu-west")
	}
}

func TestDiscoverSRVIsInertWithoutARecord(t *testing.T) {
	targets, err := DiscoverSRV(SRVConfig{})
	if err != nil || len(targets) != 0 {
		t.Fatalf("got %+v / %v", targets, err)
	}
}

func TestDiscoverSRVReportsAnUnresolvableRecord(t *testing.T) {
	_, err := DiscoverSRV(SRVConfig{Name: "_nothing._tcp.invalid."})
	if err == nil {
		t.Fatal("expected an error for an unresolvable record")
	}
}

// A source that fails for one cycle must leave the running targets alone: a
// scrape gap is indistinguishable from a real outage in the data.
func TestDiscoverKeepsTargetsWhenASourceFails(t *testing.T) {
	m := &scrapeManager{
		cfg:     ScrapeConfig{Nomad: NomadConfigs{{Address: "http://127.0.0.1:1"}}},
		running: map[string]*runningTarget{},
	}

	targets, ok := m.discover(context.Background(), &http.Client{Timeout: time.Second})
	if ok {
		t.Fatal("expected discovery to report failure")
	}
	if targets != nil {
		t.Fatalf("expected no partial list, got %+v", targets)
	}
}
