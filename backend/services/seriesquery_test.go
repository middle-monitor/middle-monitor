package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// seedSeries indexes one point per route per minute for the given org and
// returns the metric name it used.
func seedSeries(t *testing.T, s *OpenSearchService, orgID int64, metricName string, routes []string, end time.Time) {
	t.Helper()
	for i := 0; i < 10; i++ {
		ts := end.Add(-time.Duration(i) * time.Minute)
		for routeIndex, route := range routes {
			point := SeriesPoint{
				Timestamp:      ts,
				OrganizationID: orgID,
				MetricName:     metricName,
				MetricType:     "gauge",
				Value:          float64(10 * (routeIndex + 1)),
				Labels:         map[string]string{"route": route, "method": "GET"},
				ServiceName:    "checkout",
				Hostname:       "host-1",
			}
			if err := s.IndexSeriesPoint(point); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}
	if _, err := http.Post(s.baseURL+"/"+SeriesIndexPattern+"/_refresh", "application/json", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func newLiveService(t *testing.T) *OpenSearchService {
	t.Helper()
	if os.Getenv("MM_OPENSEARCH_IT") == "" {
		t.Skip("set MM_OPENSEARCH_IT=1 to run against a live OpenSearch")
	}
	s := NewOpenSearchService()
	if err := s.EnsureSeriesTemplate(); err != nil {
		t.Fatalf("template: %v", err)
	}
	s.initialized = true
	return s
}

func TestSeriesDiscoveryAndQuery(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()
	metricName := fmt.Sprintf("it_requests_%d", end.UnixNano())
	routes := []string{"/checkout", "/cart", "/search"}
	seedSeries(t, s, 1, metricName, routes, end)

	start := end.Add(-30 * time.Minute)

	names, err := s.ListMetricNames(context.Background(), 1, start, end, "it_requests_", 100, nil)
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if !contains(names, metricName) {
		t.Errorf("metric %s missing from %v", metricName, names)
	}

	keys, err := s.ListLabelKeys(context.Background(), 1, metricName, start, end, 100, nil)
	if err != nil {
		t.Fatalf("label keys: %v", err)
	}
	for _, want := range []string{"route", "method"} {
		if !contains(keys, want) {
			t.Errorf("label key %q missing from %v", want, keys)
		}
	}

	values, err := s.ListLabelValues(context.Background(), 1, metricName, "route", "", start, end, 100, nil)
	if err != nil {
		t.Fatalf("label values: %v", err)
	}
	if len(values) != len(routes) {
		t.Errorf("got %v, want the %d seeded routes", values, len(routes))
	}

	results, err := s.QuerySeries(context.Background(), SeriesQuery{
		OrganizationID: 1,
		MetricName:     metricName,
		GroupBy:        "route",
		Aggregation:    "avg",
		Start:          start,
		End:            end,
		Step:           time.Minute,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != len(routes) {
		t.Fatalf("got %d series, want %d", len(results), len(routes))
	}
	for _, result := range results {
		if len(result.Points) == 0 {
			t.Errorf("series %v has no points", result.Labels)
		}
		if result.Labels["route"] == "" {
			t.Errorf("series lost its group-by label: %v", result.Labels)
		}
	}
}

// A label filter must actually narrow the result, otherwise a chart filtered on
// one route silently shows every route.
func TestSeriesFilterNarrowsResult(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()
	metricName := fmt.Sprintf("it_filtered_%d", end.UnixNano())
	seedSeries(t, s, 1, metricName, []string{"/checkout", "/cart"}, end)

	results, err := s.QuerySeries(context.Background(), SeriesQuery{
		OrganizationID: 1,
		MetricName:     metricName,
		Filters:        []SeriesFilter{{Key: "route", Value: "/checkout"}},
		GroupBy:        "route",
		Aggregation:    "avg",
		Start:          end.Add(-30 * time.Minute),
		End:            end,
		Step:           time.Minute,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 || results[0].Labels["route"] != "/checkout" {
		t.Fatalf("filter did not narrow the result: %+v", results)
	}
}

// Metric labels carry customer infrastructure names. A query for one org must
// never surface another org's series, on any of the four endpoints.
func TestSeriesQueriesAreScopedToOrganization(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()
	metricName := fmt.Sprintf("it_tenant_%d", end.UnixNano())
	seedSeries(t, s, 424242, metricName, []string{"/secret-route"}, end)

	start := end.Add(-30 * time.Minute)
	const otherOrg = 1

	names, err := s.ListMetricNames(context.Background(), otherOrg, start, end, "it_tenant_", 100, nil)
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if contains(names, metricName) {
		t.Error("metric name leaked across organizations")
	}

	keys, err := s.ListLabelKeys(context.Background(), otherOrg, metricName, start, end, 100, nil)
	if err != nil {
		t.Fatalf("label keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("label keys leaked across organizations: %v", keys)
	}

	values, err := s.ListLabelValues(context.Background(), otherOrg, metricName, "route", "", start, end, 100, nil)
	if err != nil {
		t.Fatalf("label values: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("label values leaked across organizations: %v", values)
	}

	results, err := s.QuerySeries(context.Background(), SeriesQuery{
		OrganizationID: otherOrg,
		MetricName:     metricName,
		GroupBy:        "route",
		Aggregation:    "avg",
		Start:          start,
		End:            end,
		Step:           time.Minute,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("series leaked across organizations: %+v", results)
	}
}

// Retention collects documents it cannot attribute with must_not exists on
// organization_id. Writing a concrete 0 would make them belong to no
// organization and never expire, so the field has to stay absent.
func TestUnattributedPointOmitsOrganizationID(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()
	metricName := fmt.Sprintf("it_orphan_%d", end.UnixNano())

	if err := s.IndexSeriesPoint(SeriesPoint{
		Timestamp:  end,
		MetricName: metricName,
		MetricType: "gauge",
		Value:      1,
	}); err != nil {
		t.Fatalf("index: %v", err)
	}
	if _, err := http.Post(s.baseURL+"/"+SeriesIndexPattern+"/_refresh", "application/json", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	body := fmt.Sprintf(`{"query":{"bool":{"filter":[{"term":{"metric_name":"%s"}}],`+
		`"must_not":[{"exists":{"field":"organization_id"}}]}}}`, metricName)
	resp, err := http.Post(s.baseURL+"/"+SeriesIndexPattern+"/_search", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Hits.Total.Value != 1 {
		t.Fatalf("orphan point is not collectable by retention: got %d hits", result.Hits.Total.Value)
	}
}

// Alerting compares one number to a threshold. If the statistic were wrong, or
// the label filter ignored, a rule would fire on the wrong series.
func TestAggregateSeriesReducesTheWindowToOneValue(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()
	metricName := fmt.Sprintf("it_alert_%d", end.UnixNano())
	// seedSeries writes 10 points per route, valued 10 for the first route and
	// 20 for the second.
	seedSeries(t, s, 1, metricName, []string{"/slow", "/fast"}, end)

	base := SeriesQuery{
		OrganizationID: 1,
		MetricName:     metricName,
		Start:          end.Add(-30 * time.Minute),
		End:            end,
	}

	cases := map[string]struct {
		aggregation string
		filters     []SeriesFilter
		want        float64
	}{
		"max over both routes":  {"max", nil, 20},
		"min over both routes":  {"min", nil, 10},
		"avg over both routes":  {"avg", nil, 15},
		"count over both":       {"count", nil, 20},
		"avg on the slow route": {"avg", []SeriesFilter{{Key: "route", Value: "/slow"}}, 10},
		"avg on the fast route": {"avg", []SeriesFilter{{Key: "route", Value: "/fast"}}, 20},
		// A rule watching a percentile must read a value like any other. Scoped to
		// one route so every point is identical and the percentile is exact.
		"p50 on the slow route": {"p50", []SeriesFilter{{Key: "route", Value: "/slow"}}, 10},
		"p95 on the fast route": {"p95", []SeriesFilter{{Key: "route", Value: "/fast"}}, 20},
	}

	for name, tc := range cases {
		q := base
		q.Aggregation = tc.aggregation
		q.Filters = tc.filters
		got, err := s.AggregateSeries(context.Background(), q)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got == nil {
			t.Errorf("%s: got no value", name)
			continue
		}
		if *got != tc.want {
			t.Errorf("%s: got %v, want %v", name, *got, tc.want)
		}
	}
}

// No datapoint in the window must read as "nothing to evaluate", not as zero:
// zero would breach a "less than" rule and fire a false incident.
func TestAggregateSeriesReturnsNilWhenTheWindowIsEmpty(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()

	got, err := s.AggregateSeries(context.Background(), SeriesQuery{
		OrganizationID: 1,
		MetricName:     fmt.Sprintf("it_absent_%d", end.UnixNano()),
		Aggregation:    "avg",
		Start:          end.Add(-30 * time.Minute),
		End:            end,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil for an empty window", *got)
	}
}

func TestAggregateSeriesIsScopedToOrganization(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()
	metricName := fmt.Sprintf("it_alert_tenant_%d", end.UnixNano())
	seedSeries(t, s, 424242, metricName, []string{"/secret"}, end)

	got, err := s.AggregateSeries(context.Background(), SeriesQuery{
		OrganizationID: 1,
		MetricName:     metricName,
		Aggregation:    "avg",
		Start:          end.Add(-30 * time.Minute),
		End:            end,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("another organization's metric was aggregated: %v", *got)
	}
}

// Correlation scopes a question to a host or its group. If the host filter were
// ignored, an incident on one machine would show the metrics of the whole fleet.
func TestQuerySeriesScopesToHosts(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC()
	metricName := fmt.Sprintf("it_hosts_%d", end.UnixNano())

	for _, host := range []string{"web-01", "web-02", "db-01"} {
		if err := s.IndexSeriesPoint(SeriesPoint{
			Timestamp:      end,
			OrganizationID: 1,
			MetricName:     metricName,
			MetricType:     "gauge",
			Value:          1,
			Labels:         map[string]string{"role": "app"},
			Hostname:       host,
		}); err != nil {
			t.Fatalf("seed %s: %v", host, err)
		}
	}
	if _, err := http.Post(s.baseURL+"/"+SeriesIndexPattern+"/_refresh", "application/json", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	base := SeriesQuery{
		OrganizationID: 1,
		MetricName:     metricName,
		Aggregation:    "count",
		Start:          end.Add(-30 * time.Minute),
		End:            end,
	}

	cases := map[string]struct {
		hostnames []string
		want      float64
	}{
		"whole fleet":      {nil, 3},
		"one host":         {[]string{"web-01"}, 1},
		"a group of hosts": {[]string{"web-01", "web-02"}, 2},
	}
	for name, tc := range cases {
		q := base
		q.Hostnames = tc.hostnames
		got, err := s.AggregateSeries(context.Background(), q)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got == nil || *got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}

	// Discovery must narrow too, otherwise a host-scoped explorer still offers
	// metrics that host never reported.
	names, err := s.ListMetricNames(context.Background(), 1, base.Start, end, "it_hosts_", 100, []string{"db-01"})
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if !contains(names, metricName) {
		t.Errorf("metric missing for its own host: %v", names)
	}
	names, err = s.ListMetricNames(context.Background(), 1, base.Start, end, "it_hosts_", 100, []string{"nowhere"})
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if contains(names, metricName) {
		t.Errorf("metric surfaced for an unrelated host: %v", names)
	}
}

// Percentile buckets are read out of the response by percent, and OpenSearch
// answers "50.0" where the query asked for 50. Reading the key back the way it
// was written loses every percentile silently — the chart empties and an alert
// rule on a percentile stops evaluating without erroring. This runs without a
// live cluster, unlike the tests above, so the regression cannot slip through CI.
func TestParseTimelineReadsPercentilesWhateverTheKeyLooksLike(t *testing.T) {
	for name, key := range map[string]string{
		"decimal key": "95.0",
		"integer key": "95",
	} {
		aggs := map[string]json.RawMessage{
			"timeline": json.RawMessage(fmt.Sprintf(
				`{"buckets":[{"key":1700000000000,"stat":{"values":{%q:42.5}}}]}`, key)),
		}

		points := parseTimeline(aggs, "p95")
		if len(points) != 1 {
			t.Fatalf("%s: got %d points, want 1", name, len(points))
		}
		if points[0].Value == nil {
			t.Fatalf("%s: percentile read as no data", name)
		}
		if *points[0].Value != 42.5 {
			t.Errorf("%s: got %v, want 42.5", name, *points[0].Value)
		}
	}
}

// A percentile the response does not carry is absent, not zero: zero would
// breach a "less than" rule and fire an incident on missing data.
func TestParseTimelineReportsAMissingPercentileAsNoData(t *testing.T) {
	aggs := map[string]json.RawMessage{
		"timeline": json.RawMessage(`{"buckets":[{"key":1700000000000,"stat":{"values":{"50.0":1}}}]}`),
	}

	points := parseTimeline(aggs, "p95")
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != nil {
		t.Fatalf("got %v, want no value", *points[0].Value)
	}
}

func TestQuerySeriesRejectsBadInput(t *testing.T) {
	s := &OpenSearchService{}
	now := time.Now().UTC()

	cases := map[string]SeriesQuery{
		"no metric":         {Aggregation: "avg", Start: now.Add(-time.Hour), End: now},
		"bad aggregation":   {MetricName: "m", Aggregation: "median", Start: now.Add(-time.Hour), End: now},
		"end before start":  {MetricName: "m", Aggregation: "avg", Start: now, End: now.Add(-time.Hour)},
		"zero-length range": {MetricName: "m", Aggregation: "avg", Start: now, End: now},
	}
	for name, q := range cases {
		if _, err := s.QuerySeries(context.Background(), q); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
