package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// recordingExporter captures what the scraper ships instead of sending it.
type recordingExporter struct {
	batches []*metricdata.ResourceMetrics
}

func (e *recordingExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (e *recordingExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (e *recordingExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	// Copy: the caller may reuse the struct after Export returns.
	copied := *rm
	e.batches = append(e.batches, &copied)
	return nil
}

func (e *recordingExporter) ForceFlush(context.Context) error { return nil }
func (e *recordingExporter) Shutdown(context.Context) error   { return nil }

func withRecordingExporter(t *testing.T) *recordingExporter {
	t.Helper()
	previous := scrapeExporter
	rec := &recordingExporter{}
	scrapeExporter = rec
	scrapeResource = resource.Empty()
	t.Cleanup(func() { scrapeExporter = previous })
	return rec
}

func attrValue(dp metricdata.DataPoint[float64], key string) string {
	for _, kv := range dp.Attributes.ToSlice() {
		if string(kv.Key) == key {
			return kv.Value.Emit()
		}
	}
	return ""
}

// The whole point of the feature: point the agent at an exporter and the series
// come out the other side, labels intact.
func TestScrapeOnceExportsWhatTheExporterServes(t *testing.T) {
	rec := withRecordingExporter(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("node_load1 0.42\nhttp_requests_total{route=\"/checkout\"} 128\n"))
	}))
	defer server.Close()

	target := ScrapeTarget{Name: "node", URL: server.URL, Labels: map[string]string{"env": "prod"}}
	scrapeOnce(context.Background(), &http.Client{Timeout: time.Second}, target, nil)

	if len(rec.batches) != 1 {
		t.Fatalf("got %d batches, want 1", len(rec.batches))
	}
	metrics := rec.batches[0].ScopeMetrics[0].Metrics
	if len(metrics) != 2 {
		t.Fatalf("got %d metrics, want 2", len(metrics))
	}

	byName := map[string]metricdata.DataPoint[float64]{}
	for _, m := range metrics {
		gauge, ok := m.Data.(metricdata.Gauge[float64])
		if !ok || len(gauge.DataPoints) != 1 {
			t.Fatalf("%s: unexpected data shape", m.Name)
		}
		byName[m.Name] = gauge.DataPoints[0]
	}

	if dp, ok := byName["node_load1"]; !ok || dp.Value != 0.42 {
		t.Errorf("node_load1: %+v", dp)
	}
	requests, ok := byName["http_requests_total"]
	if !ok || requests.Value != 128 {
		t.Errorf("http_requests_total: %+v", requests)
	}
	if got := attrValue(requests, "route"); got != "/checkout" {
		t.Errorf("series label lost: route=%q", got)
	}
	// Target labels and the target name ride along so several targets serving the
	// same metric stay tellable apart.
	if got := attrValue(requests, "env"); got != "prod" {
		t.Errorf("target label lost: env=%q", got)
	}
	if got := attrValue(requests, "scrape_target"); got != "node" {
		t.Errorf("scrape_target lost: %q", got)
	}
}

// A target label must win over a colliding series label: it is what the operator
// configured deliberately.
func TestTargetLabelsOverrideSeriesLabels(t *testing.T) {
	rec := withRecordingExporter(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`up{env="whatever"} 1` + "\n"))
	}))
	defer server.Close()

	scrapeOnce(context.Background(), &http.Client{Timeout: time.Second},
		ScrapeTarget{Name: "t", URL: server.URL, Labels: map[string]string{"env": "prod"}}, nil)

	if len(rec.batches) != 1 {
		t.Fatalf("got %d batches, want 1", len(rec.batches))
	}
	gauge := rec.batches[0].ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[float64])
	if got := attrValue(gauge.DataPoints[0], "env"); got != "prod" {
		t.Fatalf("env=%q, want prod", got)
	}
}

// A dead or erroring target must not export anything, and must not panic: the
// other targets keep running in their own goroutines.
func TestScrapeOnceExportsNothingOnFailure(t *testing.T) {
	rec := withRecordingExporter(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &http.Client{Timeout: time.Second}
	scrapeOnce(context.Background(), client, ScrapeTarget{Name: "broken", URL: server.URL}, nil)
	scrapeOnce(context.Background(), client, ScrapeTarget{Name: "gone", URL: "http://127.0.0.1:1/metrics"}, nil)

	if len(rec.batches) != 0 {
		t.Fatalf("exported %d batches from failing targets, want 0", len(rec.batches))
	}
}

// A non-200 answer must carry the status code as structured data, not just in
// a formatted string, so a caller can react to it with errors.As.
func TestFetchTargetReturnsATypedStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := fetchTarget(context.Background(), &http.Client{Timeout: time.Second}, ScrapeTarget{URL: server.URL})

	var statusErr *scrapeStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("got %v (%T), want a *scrapeStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want %d", statusErr.StatusCode, http.StatusServiceUnavailable)
	}
}

// An exporter answering with megabytes must not exhaust the agent's memory:
// the reader stops at the budget rather than reading the rest of the stream.
func TestLimitedReaderStopsAtTheByteBudget(t *testing.T) {
	body := strings.Repeat("x", 100)
	lr := &limitedReader{r: strings.NewReader(body), remaining: 10}

	n, err := io.Copy(io.Discard, lr)

	if !errors.Is(err, errScrapeBodyTooLarge) {
		t.Fatalf("got %v, want errScrapeBodyTooLarge", err)
	}
	if n != 10 {
		t.Errorf("read %d bytes before erroring, want exactly the 10-byte budget", n)
	}
}

// A body within the budget must read to completion normally, with no error.
func TestLimitedReaderPassesThroughUnderBudget(t *testing.T) {
	lr := &limitedReader{r: strings.NewReader("short body"), remaining: 100}

	got, err := io.ReadAll(lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "short body" {
		t.Errorf("got %q, want %q", got, "short body")
	}
}

// An endpoint answering 200 with nothing useful must not ship an empty batch.
func TestScrapeOnceExportsNothingWhenThereAreNoSamples(t *testing.T) {
	rec := withRecordingExporter(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("# HELP only_comments here\n# TYPE only_comments gauge\n"))
	}))
	defer server.Close()

	scrapeOnce(context.Background(), &http.Client{Timeout: time.Second},
		ScrapeTarget{Name: "empty", URL: server.URL}, nil)

	if len(rec.batches) != 0 {
		t.Fatalf("exported %d batches, want 0", len(rec.batches))
	}
}

// Per-target settings must win over the global ones, otherwise a target that
// needs a slower cadence cannot get one.
func TestScrapeIntervalAndTimeoutResolution(t *testing.T) {
	cfg := ScrapeConfig{Interval: 30, Timeout: 5}

	if got := cfg.intervalFor(ScrapeTarget{}); got != 30*time.Second {
		t.Errorf("global interval: got %v, want 30s", got)
	}
	if got := cfg.intervalFor(ScrapeTarget{Interval: 60}); got != 60*time.Second {
		t.Errorf("target interval: got %v, want 60s", got)
	}
	if got := cfg.timeoutFor(ScrapeTarget{}); got != 5*time.Second {
		t.Errorf("global timeout: got %v, want 5s", got)
	}
	if got := cfg.timeoutFor(ScrapeTarget{Timeout: 20}); got != 20*time.Second {
		t.Errorf("target timeout: got %v, want 20s", got)
	}

	empty := ScrapeConfig{}
	if got := empty.intervalFor(ScrapeTarget{}); got != defaultScrapeInterval {
		t.Errorf("default interval: got %v, want %v", got, defaultScrapeInterval)
	}
	if got := empty.timeoutFor(ScrapeTarget{}); got != defaultScrapeTimeout {
		t.Errorf("default timeout: got %v, want %v", got, defaultScrapeTimeout)
	}
}

// Discovery adds and removes instances as jobs move. A target that disappears
// must have its loop stopped, otherwise the agent keeps hammering a dead address
// forever and logs a failure every interval.
func TestReconcileStartsAndStopsTargetLoops(t *testing.T) {
	withRecordingExporter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &scrapeManager{
		cfg:     ScrapeConfig{Interval: 3600},
		running: map[string]*runningTarget{},
	}

	first := ScrapeTarget{Name: "a", URL: "http://127.0.0.1:1/metrics"}
	second := ScrapeTarget{Name: "b", URL: "http://127.0.0.1:2/metrics"}

	m.reconcile(ctx, []ScrapeTarget{first, second})
	if len(m.running) != 2 {
		t.Fatalf("got %d running, want 2", len(m.running))
	}

	// Second disappears from discovery.
	m.reconcile(ctx, []ScrapeTarget{first})
	if len(m.running) != 1 {
		t.Fatalf("got %d running, want 1", len(m.running))
	}
	if _, still := m.running[second.URL]; still {
		t.Error("the removed target is still running")
	}

	// Reconciling with an unchanged set must be a no-op.
	m.reconcile(ctx, []ScrapeTarget{first})
	if len(m.running) != 1 {
		t.Fatalf("got %d running, want 1", len(m.running))
	}

	m.reconcile(ctx, nil)
	if len(m.running) != 0 {
		t.Fatalf("got %d running, want 0", len(m.running))
	}
}

// A target without a url cannot be scraped and must not occupy a slot.
func TestReconcileSkipsTargetsWithoutURL(t *testing.T) {
	withRecordingExporter(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &scrapeManager{cfg: ScrapeConfig{Interval: 3600}, running: map[string]*runningTarget{}}
	m.reconcile(ctx, []ScrapeTarget{{Name: "broken"}})

	if len(m.running) != 0 {
		t.Fatalf("got %d running, want 0", len(m.running))
	}
}

// Scraping is opt-in: an agent upgraded without touching its config must keep
// behaving exactly as before.
func TestStartScrapingIsInertWhenDisabled(t *testing.T) {
	rec := withRecordingExporter(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("should_not_be_scraped 1\n"))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartScraping(ctx, ScrapeConfig{Targets: []ScrapeTarget{{Name: "t", URL: server.URL}}})
	time.Sleep(150 * time.Millisecond)

	if len(rec.batches) != 0 {
		t.Fatalf("scraped while disabled: %d batches", len(rec.batches))
	}
}
