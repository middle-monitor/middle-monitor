package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// A series is identified by its hash across datapoints. Go map iteration order is
// random, so if the hash depended on it the same series would split into several
// on a chart and alerting would aggregate over a moving target.
func TestSeriesHashIsStableAcrossMapOrder(t *testing.T) {
	labels := map[string]string{"route": "/checkout", "method": "GET", "status": "200"}

	first := seriesHash("http_requests_total", encodeLabels(labels))
	for i := 0; i < 50; i++ {
		again := seriesHash("http_requests_total", encodeLabels(labels))
		if again != first {
			t.Fatalf("hash changed between calls: %s != %s", again, first)
		}
	}
}

// Distinct label sets must not collide: a collision would merge two unrelated
// series into one chart line.
func TestSeriesHashSeparatesLabelSets(t *testing.T) {
	base := seriesHash("http_requests_total", encodeLabels(map[string]string{"route": "/checkout"}))

	cases := map[string]map[string]string{
		"different value":  {"route": "/cart"},
		"different key":    {"path": "/checkout"},
		"additional label": {"route": "/checkout", "method": "GET"},
		"empty label set":  {},
	}
	for name, labels := range cases {
		if got := seriesHash("http_requests_total", encodeLabels(labels)); got == base {
			t.Errorf("%s: hash collided with base", name)
		}
	}

	if seriesHash("other_metric", encodeLabels(map[string]string{"route": "/checkout"})) == base {
		t.Error("same labels on a different metric collided")
	}
}

// Label values routinely contain "=" (query strings, filters). Splitting on the
// last one instead of the first would corrupt the value and make the label
// unfilterable.
func TestDecodeLabelKeepsEqualsInValue(t *testing.T) {
	key, value, err := DecodeLabel("query=a=b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "query" || value != "a=b" {
		t.Fatalf("got key=%q value=%q, want key=query value=a=b", key, value)
	}
}

func TestDecodeLabelRejectsMalformed(t *testing.T) {
	for _, kv := range []string{"noseparator", "=novalue"} {
		if _, _, err := DecodeLabel(kv); err == nil {
			t.Errorf("%q: expected an error", kv)
		}
	}
}

// Runs against a live OpenSearch (MM_OPENSEARCH_IT=1). The mapping is strict and
// the labels arrive from exporters we do not control, so acceptance of an unknown
// label key can only be proven against the real engine.
func TestIndexSeriesPointAgainstLiveOpenSearch(t *testing.T) {
	if os.Getenv("MM_OPENSEARCH_IT") == "" {
		t.Skip("set MM_OPENSEARCH_IT=1 to run against a live OpenSearch")
	}

	s := NewOpenSearchService()
	if err := s.EnsureSeriesTemplate(); err != nil {
		t.Fatalf("template: %v", err)
	}

	now := time.Now().UTC()
	marker := fmt.Sprintf("it_%d", now.UnixNano())
	point := SeriesPoint{
		Timestamp:      now,
		OrganizationID: 1,
		MetricName:     "http_requests_total",
		MetricType:     "sum",
		Unit:           "1",
		Value:          4294967296.5, // beyond float32 precision
		Labels: map[string]string{
			"route":  "/checkout",
			"query":  "a=b",
			marker:   "1", // a label key the mapping has never seen
			"le.tag": "0.5",
		},
		ServiceName: "checkout",
		Hostname:    "host-1",
	}
	s.initialized = true
	if err := s.IndexSeriesPoint(point); err != nil {
		t.Fatalf("index: %v", err)
	}

	index := seriesIndexFor(now)
	if _, err := http.Post(s.baseURL+"/"+index+"/_refresh", "application/json", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	body := fmt.Sprintf(`{"query":{"bool":{"filter":[
		{"term":{"labels_kv":"%s=1"}},
		{"term":{"labels_kv":"query=a=b"}}]}}}`, marker)
	resp, err := http.Post(s.baseURL+"/"+index+"/_search", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("search returned %d", resp.StatusCode)
	}

	var result struct {
		Hits struct {
			Hits []struct {
				Source struct {
					Value      float64 `json:"value"`
					SeriesHash string  `json:"series_hash"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Hits.Hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(result.Hits.Hits))
	}
	if got := result.Hits.Hits[0].Source.Value; got != point.Value {
		t.Errorf("value round-trip lost precision: got %v, want %v", got, point.Value)
	}
	if result.Hits.Hits[0].Source.SeriesHash == "" {
		t.Error("series_hash was not stored")
	}
}

// A histogram has no single value, so it becomes two series the way Prometheus
// splits it. Collapsing it to one point would report a request count as a latency.
func TestConvertHistogramSplitsCountAndSum(t *testing.T) {
	sum := 12.5
	metric := &metricspb.Metric{
		Name: "http_server_duration",
		Unit: "ms",
		Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
			DataPoints: []*metricspb.HistogramDataPoint{{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Count:        4,
				Sum:          &sum,
				Attributes: []*commonpb.KeyValue{{
					Key:   "route",
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "/checkout"}},
				}},
			}},
		}},
	}

	points := convertMetricToSeriesPoints(metric, "checkout", "host-1", 7)

	if len(points) != 2 {
		t.Fatalf("got %d points, want 2 (count and sum)", len(points))
	}
	byName := map[string]SeriesPoint{}
	for _, p := range points {
		byName[p.MetricName] = p
	}
	if got := byName["http_server_duration_count"].Value; got != 4 {
		t.Errorf("count: got %v, want 4", got)
	}
	if got := byName["http_server_duration_sum"].Value; got != sum {
		t.Errorf("sum: got %v, want %v", got, sum)
	}
	for name, p := range byName {
		if p.Labels["route"] != "/checkout" {
			t.Errorf("%s: lost the datapoint attributes", name)
		}
		if p.OrganizationID != 7 {
			t.Errorf("%s: org id not carried, got %d", name, p.OrganizationID)
		}
	}
}

// A histogram without a sum must not emit a zero-valued sum series: zero is a
// legitimate latency total and would be indistinguishable from a real one.
func TestConvertHistogramSkipsMissingSum(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "http_server_duration",
		Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
			DataPoints: []*metricspb.HistogramDataPoint{{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Count:        4,
			}},
		}},
	}

	points := convertMetricToSeriesPoints(metric, "checkout", "host-1", 1)

	if len(points) != 1 || points[0].MetricName != "http_server_duration_count" {
		t.Fatalf("got %d points, want only the count series", len(points))
	}
}

// Encoding must be sorted, otherwise the hash depends on insertion order.
func TestEncodeLabelsIsSorted(t *testing.T) {
	got := encodeLabels(map[string]string{"z": "1", "a": "2", "m": "3"})
	want := []string{"a=2", "m=3", "z=1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// The subtle part of index retention is the boundary: an index is only wholly
// expired once its day has ENDED before the cutoff. Dropping it on the cutoff's
// own day would destroy documents still inside their retention.
func TestSeriesIndexExpiredHonorsTheDayBoundary(t *testing.T) {
	cutoff := time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC)

	cases := map[string]struct {
		index string
		want  bool
	}{
		"day fully before cutoff":     {"middle-monitor-series-2026.08.01", true},
		"day ended before the cutoff": {"middle-monitor-series-2026.08.02", true},
		"cutoff's own day":            {"middle-monitor-series-2026.08.03", false},
		"future day":                  {"middle-monitor-series-2026.08.04", false},
		"foreign index":               {"middle-monitor-series-old-backup", false},
	}
	for name, tc := range cases {
		if got := seriesIndexExpired(tc.index, cutoff); got != tc.want {
			t.Errorf("%s (%s): got %v, want %v", name, tc.index, got, tc.want)
		}
	}

	// Midnight exactly: the day 08.02 ends AT a midnight cutoff, nothing in it
	// can be younger, so it must go.
	midnight := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	if !seriesIndexExpired("middle-monitor-series-2026.08.02", midnight) {
		t.Error("a day ending exactly at the cutoff is fully expired and must be dropped")
	}
}
