package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func samples(names ...string) []ScrapedSample {
	out := make([]ScrapedSample, 0, len(names))
	for _, name := range names {
		out = append(out, ScrapedSample{Name: name, Labels: map[string]string{}, Value: 1})
	}
	return out
}

func names(samples []ScrapedSample) []string {
	out := make([]string, 0, len(samples))
	for _, sample := range samples {
		out = append(out, sample.Name)
	}
	return out
}

func filterFor(t *testing.T, target ScrapeTarget) *seriesFilter {
	t.Helper()
	filter, err := target.compileFilter()
	if err != nil {
		t.Fatalf("compile filter: %v", err)
	}
	return filter
}

// The reason the feature exists: one node_exporter serves over a thousand
// series and an operator keeps a few dozen. A non-empty keep list is a
// whitelist, so anything it does not name is gone.
func TestKeepMetricsIsAWhitelist(t *testing.T) {
	target := ScrapeTarget{KeepMetrics: []string{`^node_(cpu|memory)_.*`, `^node_load(1|5|15)$`}}
	filter := filterFor(t, target)

	kept := filter.apply(samples(
		"node_cpu_seconds_total",
		"node_memory_MemFree_bytes",
		"node_load1",
		"node_scrape_collector_duration_seconds",
		"go_goroutines",
	))

	got := strings.Join(names(kept), ",")
	want := "node_cpu_seconds_total,node_memory_MemFree_bytes,node_load1"
	if got != want {
		t.Fatalf("kept %q, want %q", got, want)
	}
}

// The other half of the volume problem: the runtime series every Go exporter
// ships are pure noise for a customer paying by ingested series.
func TestDropMetricsRemovesExporterNoise(t *testing.T) {
	filter := filterFor(t, ScrapeTarget{DropMetrics: []string{`^(go_|prometheus_|promhttp_).*`}})

	kept := filter.apply(samples("go_goroutines", "promhttp_metric_handler_requests_total", "adguard_queries_total"))

	if got := strings.Join(names(kept), ","); got != "adguard_queries_total" {
		t.Fatalf("kept %q, want only adguard_queries_total", got)
	}
}

// Order is part of the contract, and it has to be documented as such: a series
// named by both lists is dropped, because drop runs on what keep left.
func TestDropRunsAfterKeep(t *testing.T) {
	filter := filterFor(t, ScrapeTarget{
		KeepMetrics: []string{`^node_.*`},
		DropMetrics: []string{`^node_scrape_.*`},
	})

	kept := filter.apply(samples("node_load1", "node_scrape_collector_success"))

	if got := strings.Join(names(kept), ","); got != "node_load1" {
		t.Fatalf("kept %q, want only node_load1", got)
	}
}

// Filesystem and network series are per-device: keeping every device multiplies
// the cardinality by the number of loopback and container mounts.
func TestKeepIfLabelsFiltersOnALabelValue(t *testing.T) {
	filter := filterFor(t, ScrapeTarget{
		KeepIfLabels: []LabelFilter{{Metric: `^node_filesystem_.*`, Label: "fstype", Matches: `^ext4$`}},
	})

	kept := filter.apply([]ScrapedSample{
		{Name: "node_filesystem_avail_bytes", Labels: map[string]string{"fstype": "ext4"}},
		{Name: "node_filesystem_avail_bytes", Labels: map[string]string{"fstype": "tmpfs"}},
		{Name: "node_filesystem_size_bytes", Labels: map[string]string{}},
		// A metric the rule does not name is left alone, whatever its labels.
		{Name: "node_load1", Labels: map[string]string{}},
	})

	if len(kept) != 2 {
		t.Fatalf("kept %d series, want 2: %+v", len(kept), kept)
	}
	if kept[0].Labels["fstype"] != "ext4" || kept[1].Name != "node_load1" {
		t.Fatalf("kept the wrong series: %+v", kept)
	}
}

// Dropping a high-cardinality label must keep the series: the point is to lose
// the dimension, not the measurement.
func TestDropLabelsKeepsTheSeries(t *testing.T) {
	filter := filterFor(t, ScrapeTarget{DropLabels: []string{"id", "uuid"}})

	kept := filter.apply([]ScrapedSample{
		{Name: "container_cpu", Labels: map[string]string{"id": "abc", "uuid": "def", "name": "api"}},
	})

	if len(kept) != 1 {
		t.Fatalf("kept %d series, want 1", len(kept))
	}
	if _, present := kept[0].Labels["id"]; present {
		t.Fatal("id should have been dropped")
	}
	if kept[0].Labels["name"] != "api" {
		t.Fatalf("name label lost: %+v", kept[0].Labels)
	}
}

// A target with no filter must not pay for one, and must not alter the samples.
func TestNoFilterConfiguredIsANilFilter(t *testing.T) {
	filter := filterFor(t, ScrapeTarget{Name: "plain"})
	if filter != nil {
		t.Fatalf("expected no filter, got %+v", filter)
	}
	input := samples("a", "b")
	if got := filter.apply(input); len(got) != 2 {
		t.Fatalf("nil filter changed the samples: %+v", got)
	}
}

// A regex typo has to name the field it came from: the operator reads this
// message from --config-check and needs to know which line to fix.
func TestBadRegexIsAConfigError(t *testing.T) {
	_, err := ScrapeTarget{KeepMetrics: []string{"("}}.compileFilter()
	if err == nil {
		t.Fatal("expected an error for an invalid regex")
	}
	if !strings.Contains(err.Error(), "keep_metrics") {
		t.Fatalf("error does not name the field: %v", err)
	}
}

// End to end, and this is the whole business case: filtered series must never
// reach the exporter, so they never cross the network and are never billed.
func TestFilteredSeriesNeverReachTheExporter(t *testing.T) {
	rec := withRecordingExporter(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("node_load1 0.42\ngo_goroutines 31\npromhttp_metric_handler_errors_total 0\n"))
	}))
	defer server.Close()

	target := ScrapeTarget{Name: "node", URL: server.URL, DropMetrics: []string{`^(go_|promhttp_).*`}}
	scrapeOnce(context.Background(), &http.Client{Timeout: time.Second}, target, filterFor(t, target))

	if len(rec.batches) != 1 {
		t.Fatalf("got %d batches, want 1", len(rec.batches))
	}
	metrics := rec.batches[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 || metrics[0].Name != "node_load1" {
		t.Fatalf("exported %d metrics, want only node_load1: %+v", len(metrics), metrics)
	}
}

// A target that filters everything away must not be reported as an export.
func TestEverythingFilteredExportsNothing(t *testing.T) {
	rec := withRecordingExporter(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("go_goroutines 31\n"))
	}))
	defer server.Close()

	target := ScrapeTarget{Name: "noise", URL: server.URL, KeepMetrics: []string{`^node_.*`}}
	scrapeOnce(context.Background(), &http.Client{Timeout: time.Second}, target, filterFor(t, target))

	if len(rec.batches) != 0 {
		t.Fatalf("exported %d batches, want 0", len(rec.batches))
	}
}

// The filters are written in YAML, so the YAML shape is part of the contract.
func TestFilterFieldsParseFromYAML(t *testing.T) {
	var cfg ScrapeConfig
	err := yaml.Unmarshal([]byte(`
enabled: true
targets:
  - name: node
    url: http://localhost:9100/metrics
    keep_metrics:
      - '^node_cpu_.*'
    drop_metrics:
      - '^go_.*'
    keep_if_labels:
      - metric: '^node_filesystem_.*'
        label: fstype
        matches: '^ext4$'
    drop_labels: [id]
`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	target := cfg.Targets[0]
	if len(target.KeepMetrics) != 1 || len(target.DropMetrics) != 1 || len(target.DropLabels) != 1 {
		t.Fatalf("filters not parsed: %+v", target)
	}
	if target.KeepIfLabels[0].Label != "fstype" || target.KeepIfLabels[0].Matches != "^ext4$" {
		t.Fatalf("keep_if_labels not parsed: %+v", target.KeepIfLabels)
	}
}
