package main

import (
	"math"
	"strings"
	"testing"
)

const nodeExporterSample = `# HELP node_cpu_seconds_total Seconds the cpus spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 12345.67
node_cpu_seconds_total{cpu="0",mode="user"} 890.12
# HELP node_load1 1m load average.
# TYPE node_load1 gauge
node_load1 0.42
# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{le="0.1"} 24054
http_request_duration_seconds_bucket{le="+Inf"} 144320
http_request_duration_seconds_sum 53423
http_request_duration_seconds_count 144320
`

func findSample(samples []ScrapedSample, name string, labels map[string]string) *ScrapedSample {
	for i := range samples {
		if samples[i].Name != name {
			continue
		}
		match := true
		for k, v := range labels {
			if samples[i].Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return &samples[i]
		}
	}
	return nil
}

func TestParseExpositionReadsRealExporterOutput(t *testing.T) {
	samples, err := ParseExposition(strings.NewReader(nodeExporterSample))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 7 {
		t.Fatalf("got %d samples, want 7: %+v", len(samples), samples)
	}

	idle := findSample(samples, "node_cpu_seconds_total", map[string]string{"cpu": "0", "mode": "idle"})
	if idle == nil || idle.Value != 12345.67 {
		t.Errorf("idle cpu sample wrong: %+v", idle)
	}

	// A metric with no labels must still parse.
	load := findSample(samples, "node_load1", nil)
	if load == nil || load.Value != 0.42 {
		t.Errorf("load sample wrong: %+v", load)
	}

	// Histogram lines are already separate series in the format; they must come
	// through as ordinary samples rather than be dropped or merged.
	if findSample(samples, "http_request_duration_seconds_sum", nil) == nil {
		t.Error("histogram _sum was dropped")
	}
	if findSample(samples, "http_request_duration_seconds_count", nil) == nil {
		t.Error("histogram _count was dropped")
	}
	// "+Inf" here is the bucket boundary carried in the le label, not the value.
	inf := findSample(samples, "http_request_duration_seconds_bucket", map[string]string{"le": "+Inf"})
	if inf == nil || inf.Value != 144320 {
		t.Errorf("+Inf bucket wrong: %+v", inf)
	}
}

// The format allows +Inf and -Inf as sample values, distinct from the le label.
func TestParseExpositionAcceptsInfiniteValues(t *testing.T) {
	samples, err := ParseExposition(strings.NewReader("saturation +Inf\ndeficit -Inf\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s := findSample(samples, "saturation", nil); s == nil || !math.IsInf(s.Value, 1) {
		t.Errorf("saturation: %+v", s)
	}
	if s := findSample(samples, "deficit", nil); s == nil || !math.IsInf(s.Value, -1) {
		t.Errorf("deficit: %+v", s)
	}
}

// One malformed line from an exporter must not cost every other metric on the
// endpoint: a scrape returns what it could read.
func TestParseExpositionSkipsBadLinesWithoutLosingGoodOnes(t *testing.T) {
	input := `good_before 1
this line has no value
broken{unterminated="x 2
another_broken{} notanumber
good_after 3
`
	samples, err := ParseExposition(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findSample(samples, "good_before", nil) == nil {
		t.Error("lost the sample before the bad lines")
	}
	if findSample(samples, "good_after", nil) == nil {
		t.Error("lost the sample after the bad lines")
	}
}

// Label values routinely contain commas, equals signs, braces and escaped
// quotes. Getting this wrong silently mislabels series.
func TestParseExpositionHandlesAwkwardLabelValues(t *testing.T) {
	input := `http_requests_total{path="/a,b",query="x=1",note="say \"hi\"",re="{2}"} 7` + "\n"
	samples, err := ParseExposition(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(samples))
	}

	want := map[string]string{
		"path":  "/a,b",
		"query": "x=1",
		"note":  `say "hi"`,
		"re":    "{2}",
	}
	for key, value := range want {
		if got := samples[0].Labels[key]; got != value {
			t.Errorf("label %s: got %q, want %q", key, got, value)
		}
	}
	if samples[0].Value != 7 {
		t.Errorf("value: got %v, want 7", samples[0].Value)
	}
}

// A trailing timestamp is part of the format and must not be mistaken for the
// value, which would report a millisecond epoch as the metric.
func TestParseExpositionIgnoresTheTimestamp(t *testing.T) {
	samples, err := ParseExposition(strings.NewReader("queue_depth 42 1785529556833\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 1 || samples[0].Value != 42 {
		t.Fatalf("got %+v, want a single sample valued 42", samples)
	}
}

// NaN cannot be aggregated or charted, so it is dropped rather than stored as 0,
// which would read as a real measurement.
func TestParseExpositionDropsNaN(t *testing.T) {
	samples, err := ParseExposition(strings.NewReader("broken_metric NaN\nfine_metric 1\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findSample(samples, "broken_metric", nil) != nil {
		t.Error("NaN sample was kept")
	}
	if findSample(samples, "fine_metric", nil) == nil {
		t.Error("the valid sample was lost")
	}
}
