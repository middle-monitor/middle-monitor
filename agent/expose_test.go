package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// getWhenServing retries until the listener accepts, the server being started in
// a goroutine.
func getWhenServing(t *testing.T, url string) string {
	t.Helper()
	for i := 0; i < 20; i++ {
		resp, err := http.Get(url)
		if err == nil {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return string(raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never answered", url)
	return ""
}

func resetExposedSeries(t *testing.T) {
	t.Helper()
	exposedSeries.mu.Lock()
	exposedSeries.samples = map[string]exposedSample{}
	exposedSeries.full = false
	exposedSeries.mu.Unlock()
}

// The output has to be readable by Prometheus itself, which means our own parser
// must round-trip it: rendering and parsing are the two halves of the bridge.
func TestExpositionRoundTripsThroughTheParser(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()

	exposedSeries.Record("http_requests_total", map[string]string{"route": "/checkout", "method": "GET"}, 128, now)
	exposedSeries.Record("node_load1", nil, 0.42, now)

	out := RenderExposition(now, time.Minute, nil)
	samples, err := ParseExposition(strings.NewReader(out))
	if err != nil {
		t.Fatalf("our own output does not parse: %v\n%s", err, out)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples back, want 2:\n%s", len(samples), out)
	}

	requests := findSample(samples, "http_requests_total", map[string]string{"route": "/checkout"})
	if requests == nil || requests.Value != 128 {
		t.Fatalf("round trip lost the value: %+v\n%s", requests, out)
	}
	if requests.Labels["method"] != "GET" {
		t.Errorf("round trip lost a label: %+v", requests.Labels)
	}
}

// A value the agent stopped receiving must disappear, otherwise a dead target
// keeps reporting its last known value forever and reads as healthy.
func TestExpositionDropsStaleSeries(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()

	exposedSeries.Record("fresh_metric", nil, 1, now)
	exposedSeries.Record("stale_metric", nil, 2, now.Add(-10*time.Minute))

	out := RenderExposition(now, 5*time.Minute, nil)
	if !strings.Contains(out, "fresh_metric") {
		t.Errorf("fresh series missing:\n%s", out)
	}
	if strings.Contains(out, "stale_metric") {
		t.Errorf("stale series served:\n%s", out)
	}
}

// The sample carries the time the agent read it. Re-stamping it as now would
// present data the agent read a minute ago as a current measurement.
func TestExpositionKeepsTheOriginalTimestamp(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()
	seen := now.Add(-30 * time.Second)

	exposedSeries.Record("queue_depth", nil, 7, seen)

	out := strings.TrimSpace(RenderExposition(now, time.Minute, nil))
	fields := strings.Fields(out)
	if len(fields) != 3 {
		t.Fatalf("expected name, value and timestamp, got %q", out)
	}
	if fields[2] != strconv.FormatInt(seen.UnixMilli(), 10) {
		t.Fatalf("timestamp: got %s, want %d", fields[2], seen.UnixMilli())
	}
}

// Label values routinely contain quotes and backslashes; unescaped they produce
// output no Prometheus can read.
func TestExpositionEscapesLabelValues(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()

	exposedSeries.Record("weird", map[string]string{"note": `say "hi"`, "path": `C:\tmp`}, 1, now)

	out := RenderExposition(now, time.Minute, nil)
	samples, err := ParseExposition(strings.NewReader(out))
	if err != nil || len(samples) != 1 {
		t.Fatalf("escaped output did not parse: %v\n%s", err, out)
	}
	if samples[0].Labels["note"] != `say "hi"` {
		t.Errorf("note label: got %q", samples[0].Labels["note"])
	}
	if samples[0].Labels["path"] != `C:\tmp` {
		t.Errorf("path label: got %q", samples[0].Labels["path"])
	}
}

// The same series scraped again must update in place, not accumulate.
func TestRecordReplacesTheSameSeries(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()
	labels := map[string]string{"route": "/a"}

	exposedSeries.Record("hits", labels, 1, now)
	exposedSeries.Record("hits", labels, 2, now)

	fresh := exposedSeries.Fresh(now, time.Minute)
	if len(fresh) != 1 {
		t.Fatalf("got %d series, want 1", len(fresh))
	}
	if fresh[0].Value != 2 {
		t.Fatalf("got %v, want the latest value 2", fresh[0].Value)
	}
}

// Exposing is opt-in: it turns the agent into a server, which is not something
// an upgrade should do on its own.
func TestStartExpositionIsInertWhenDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if server := StartExposition(ctx, ExposeConfig{}); server != nil {
		t.Fatal("a server was started while disabled")
	}
}

func TestStartExpositionServesTheCache(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()
	exposedSeries.Record("served_metric", map[string]string{"a": "b"}, 5, now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := StartExposition(ctx, ExposeConfig{Enabled: true, Listen: "127.0.0.1:19099"})
	if server == nil {
		t.Fatal("no server started")
	}
	defer server.Close()

	body := getWhenServing(t, "http://127.0.0.1:19099/metrics")
	if !strings.Contains(body, "served_metric") {
		t.Fatalf("endpoint did not serve the cache: %q", body)
	}
}

// The migration path end to end: the agent pulls from an existing exporter and
// serves the same series back, so the customer's Prometheus keeps working while
// the collection moves over.
func TestScrapedSeriesAreServedBackToPrometheus(t *testing.T) {
	resetExposedSeries(t)
	withRecordingExporter(t)

	exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("# HELP node_load1 load\n# TYPE node_load1 gauge\nnode_load1 0.42\nhttp_requests_total{route=\"/checkout\"} 128\n"))
	}))
	defer exporter.Close()

	target := ScrapeTarget{Name: "node", URL: exporter.URL, Labels: map[string]string{"env": "prod"}}
	scrapeOnce(context.Background(), &http.Client{Timeout: time.Second}, target, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := StartExposition(ctx, ExposeConfig{Enabled: true, Listen: "127.0.0.1:19100"})
	defer server.Close()

	body := getWhenServing(t, "http://127.0.0.1:19100/metrics")
	samples, err := ParseExposition(strings.NewReader(body))
	if err != nil {
		t.Fatalf("served output does not parse: %v\n%s", err, body)
	}

	requests := findSample(samples, "http_requests_total", map[string]string{"route": "/checkout"})
	if requests == nil || requests.Value != 128 {
		t.Fatalf("scraped series not served back: %+v\n%s", requests, body)
	}
	// The labels the operator configured must survive the round trip, otherwise
	// their existing dashboards and rules break the day they switch over.
	if requests.Labels["env"] != "prod" || requests.Labels["scrape_target"] != "node" {
		t.Errorf("target labels lost: %+v", requests.Labels)
	}
	if load := findSample(samples, "node_load1", nil); load == nil || load.Value != 0.42 {
		t.Errorf("unlabelled series not served back: %+v\n%s", load, body)
	}
}

// A runaway exporter must not grow the agent's memory without bound: past the
// cap, a genuinely new series is dropped rather than accepted.
func TestRecordDropsANewSeriesPastTheCap(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()

	for i := 0; i < maxExposedSeries; i++ {
		exposedSeries.Record("filler", map[string]string{"i": strconv.Itoa(i)}, 1, now)
	}
	if got := len(exposedSeries.samples); got != maxExposedSeries {
		t.Fatalf("got %d series after filling to the cap, want %d", got, maxExposedSeries)
	}

	exposedSeries.Record("one_too_many", nil, 1, now)

	if got := len(exposedSeries.samples); got != maxExposedSeries {
		t.Fatalf("got %d series after exceeding the cap, want it to stay at %d", got, maxExposedSeries)
	}
	if _, exists := exposedSeries.samples[seriesKey("one_too_many", nil)]; exists {
		t.Error("the series past the cap was recorded anyway")
	}
}

// Fresh only filters stale series at read time; without EvictStale a series
// that churned out (e.g. a Nomad allocation rescheduled) occupies its slot
// forever. On a long-running agent this reaches the cap and starts dropping
// series that are still live, silently.
func TestEvictStaleReclaimsSlotsUnderTheCap(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()
	old := now.Add(-time.Hour)

	for i := 0; i < maxExposedSeries; i++ {
		exposedSeries.Record("churned", map[string]string{"i": strconv.Itoa(i)}, 1, old)
	}
	// One more push past the cap, to also verify `full` gets un-latched.
	exposedSeries.Record("blocked_by_cap", nil, 1, now)
	if !exposedSeries.full {
		t.Fatal("cap was not reached by the setup")
	}

	exposedSeries.EvictStale(now, time.Minute)

	if got := len(exposedSeries.samples); got != 0 {
		t.Fatalf("got %d series after evicting everything stale, want 0", got)
	}
	if exposedSeries.full {
		t.Error("full was not reset by EvictStale")
	}

	// The cap is no longer occupied by dead entries: a live series is accepted.
	exposedSeries.Record("still_alive", nil, 1, now)
	if _, exists := exposedSeries.samples[seriesKey("still_alive", nil)]; !exists {
		t.Error("a series recorded after eviction was still rejected")
	}
}

// A series refreshed since the last sweep must survive it.
func TestEvictStaleKeepsFreshSeries(t *testing.T) {
	resetExposedSeries(t)
	now := time.Now()

	exposedSeries.Record("fresh", nil, 1, now)
	exposedSeries.EvictStale(now, time.Minute)

	if _, exists := exposedSeries.samples[seriesKey("fresh", nil)]; !exists {
		t.Error("a fresh series was evicted")
	}
}

// Enabling the feature must not publish the endpoint to the network by accident.
func TestExposeDefaultsToLocalhost(t *testing.T) {
	if got := (ExposeConfig{Enabled: true}).listen(); got != "127.0.0.1:9099" {
		t.Fatalf("default listen: got %s, want 127.0.0.1:9099", got)
	}
}
