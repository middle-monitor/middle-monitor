package services

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func f(v float64) *float64 { return &v }

var rateT0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func cumulativeTimeline(group string, step time.Duration, values ...*float64) rawSeriesTimeline {
	tl := rawSeriesTimeline{temporality: TemporalityCumulative, groupValue: group}
	for i, v := range values {
		tl.buckets = append(tl.buckets, rawRateBucket{timestamp: rateT0.Add(time.Duration(i) * step), last: v})
	}
	return tl
}

// A process restart resets its counter to zero. Reading that drop as a negative
// increase would draw a large negative spike on every deploy.
func TestSeriesRateTreatsACounterDropAsAReset(t *testing.T) {
	step := time.Minute
	tl := cumulativeTimeline("", step, f(600), f(1200), f(60))

	points := seriesRate(tl, step)

	if points[0].Value != nil {
		t.Errorf("first bucket has no predecessor, got %v", *points[0].Value)
	}
	if got := *points[1].Value; got != 10 {
		t.Errorf("steady increase: got %v/s, want 10/s", got)
	}
	if got := *points[2].Value; got != 1 {
		t.Errorf("after reset the counter value itself is the increase: got %v/s, want 1/s", got)
	}
}

// A scrape gap must spread the increase over the real elapsed time, not one step,
// or a missed sample would double the reported rate.
func TestSeriesRateSpansGapsWithTheElapsedTime(t *testing.T) {
	step := time.Minute
	tl := cumulativeTimeline("", step, f(0), nil, f(1200))

	points := seriesRate(tl, step)

	if points[1].Value != nil {
		t.Errorf("empty bucket must stay empty, got %v", *points[1].Value)
	}
	if got := *points[2].Value; got != 10 {
		t.Errorf("got %v/s, want 10/s over the two-minute gap", got)
	}
}

// Delta points already are increments: differencing them would report the change
// in throughput instead of the throughput.
func TestSeriesRateReadsDeltaAsIncrements(t *testing.T) {
	step := time.Minute
	tl := rawSeriesTimeline{temporality: TemporalityDelta, buckets: []rawRateBucket{
		{timestamp: rateT0, total: f(120)},
		{timestamp: rateT0.Add(step), total: f(120)},
	}}

	points := seriesRate(tl, step)

	for i, p := range points {
		if p.Value == nil || *p.Value != 2 {
			t.Errorf("bucket %d: want 2/s for 120 per minute", i)
		}
	}
}

// Two hosts each serving 10 req/s make 20 req/s for the route; the lookback
// bucket fetched before the window must not leak onto the chart.
func TestSumRatesByGroupAddsSeriesAndDropsLookback(t *testing.T) {
	step := time.Minute
	hostA := cumulativeTimeline("/checkout", step, f(0), f(600), f(1200))
	hostB := cumulativeTimeline("/checkout", step, f(5000), f(5600), f(6200))
	other := cumulativeTimeline("", step, f(0), f(6000), f(12000))

	results := sumRatesByGroup([]rawSeriesTimeline{hostA, hostB, other}, "http_requests", "route", step, rateT0.Add(step))

	if len(results) != 1 {
		t.Fatalf("series without the group-by label must be dropped, got %d groups", len(results))
	}
	r := results[0]
	if r.Labels["route"] != "/checkout" {
		t.Errorf("labels: got %v", r.Labels)
	}
	if len(r.Points) != 2 {
		t.Fatalf("lookback bucket leaked: got %d points, want 2", len(r.Points))
	}
	for _, p := range r.Points {
		if p.Value == nil || *p.Value != 20 {
			t.Errorf("%s: want 20/s summed across hosts", p.Timestamp)
		}
	}
}

// Steps that do not divide a day put year-1 truncation and epoch buckets out of
// phase; the visible window must still start on the histogram's own bucket.
func TestSumRatesByGroupAlignsOnTheUnixEpoch(t *testing.T) {
	step := 7 * time.Minute
	first := time.Unix(rateT0.Unix()-rateT0.Unix()%420, 0).UTC()
	tl := rawSeriesTimeline{temporality: TemporalityCumulative}
	for i := -1; i < 3; i++ {
		tl.buckets = append(tl.buckets, rawRateBucket{timestamp: first.Add(time.Duration(i) * step), last: f(float64(i+1) * 420)})
	}

	results := sumRatesByGroup([]rawSeriesTimeline{tl}, "m", "", step, rateT0)

	if got := results[0].Points[0].Timestamp; !got.Equal(first) {
		t.Errorf("first visible bucket: got %s, want %s", got, first)
	}
	if results[0].Points[0].Value == nil {
		t.Error("first visible bucket lost its predecessor")
	}
}

// Without the temporality a delta counter would be differenced like a cumulative one.
func TestConvertSumCarriesTemporality(t *testing.T) {
	for otlp, want := range map[metricspb.AggregationTemporality]string{
		metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA:       TemporalityDelta,
		metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE:  TemporalityCumulative,
		metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_UNSPECIFIED: TemporalityCumulative,
	} {
		metric := &metricspb.Metric{
			Name: "requests",
			Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
				AggregationTemporality: otlp,
				DataPoints: []*metricspb.NumberDataPoint{{
					TimeUnixNano: uint64(rateT0.UnixNano()),
					Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 3},
				}},
			}},
		}
		points := convertMetricToSeriesPoints(metric, "checkout", "host-1", 1)
		if got := points[0].Temporality; got != want {
			t.Errorf("%s: got %q, want %q", otlp, got, want)
		}
	}
}

// rate is opt-in per expression query; GET /query and alerting must keep rejecting it.
func TestRateIsNotAPlainAggregation(t *testing.T) {
	if validAggregations[AggregationRate] {
		t.Error("rate must not be accepted where no per-series pass runs")
	}
}

// End to end against OpenSearch: two hosts each counting 10 req/s on one route,
// one of them restarting mid-window, must read as a flat 20 req/s.
func TestQueryRateSeriesLive(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC().Truncate(time.Minute)
	metricName := fmt.Sprintf("it_counter_%d", end.UnixNano())
	for _, host := range []string{"host-1", "host-2"} {
		for i := 0; i <= 10; i++ {
			value := float64(600 * i)
			if host == "host-2" && i >= 6 {
				value = float64(600 * (i - 5))
			}
			if err := s.IndexSeriesPoint(SeriesPoint{
				Timestamp:      end.Add(time.Duration(i-10) * time.Minute),
				OrganizationID: 1,
				MetricName:     metricName,
				MetricType:     "sum",
				Temporality:    TemporalityCumulative,
				Value:          value,
				Labels:         map[string]string{"route": "/checkout", "host": host},
				Hostname:       host,
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}
	if _, err := http.Post(s.baseURL+"/"+SeriesIndexPattern+"/_refresh", "application/json", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	results, err := s.QueryExpression(context.Background(), SeriesExpressionQuery{
		OrganizationID: 1,
		Queries: []NamedSeriesQuery{
			{Ref: "A", MetricName: metricName, GroupBy: "route", Aggregation: AggregationRate},
		},
		Expression: "$A * 60",
		Start:      end.Add(-8 * time.Minute),
		End:        end.Add(-time.Second),
		Step:       time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Labels["route"] != "/checkout" {
		t.Fatalf("got %+v, want one /checkout series", results)
	}
	for _, p := range results[0].Points {
		if p.Value == nil || *p.Value != 1200 {
			t.Errorf("%s: want 1200 req/min across both hosts", p.Timestamp)
		}
	}
}

func seedCounter(t *testing.T, s *OpenSearchService, orgID int64, metricName string, labels map[string]string, perMinute float64, end time.Time) {
	t.Helper()
	for i := 0; i <= 10; i++ {
		if err := s.IndexSeriesPoint(SeriesPoint{
			Timestamp:      end.Add(time.Duration(i-10) * time.Minute),
			OrganizationID: orgID,
			MetricName:     metricName,
			MetricType:     "sum",
			Temporality:    TemporalityCumulative,
			Value:          perMinute * float64(i),
			Labels:         labels,
			Hostname:       "host-1",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func refreshSeries(t *testing.T, s *OpenSearchService) {
	t.Helper()
	if _, err := http.Post(s.baseURL+"/"+SeriesIndexPattern+"/_refresh", "application/json", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

// rate runs its own aggregation; another tenant's counter on the same metric
// name must not be added into the result.
func TestQueryRateSeriesIsScopedToOrganizationLive(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC().Truncate(time.Minute)
	metricName := fmt.Sprintf("it_tenant_counter_%d", end.UnixNano())
	seedCounter(t, s, 1, metricName, map[string]string{"route": "/a"}, 600, end)
	seedCounter(t, s, 2, metricName, map[string]string{"route": "/a"}, 60000, end)
	refreshSeries(t, s)

	results, err := s.QueryRateSeries(context.Background(), SeriesQuery{
		OrganizationID: 1, MetricName: metricName,
		Start: end.Add(-5 * time.Minute), End: end.Add(-time.Second), Step: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range results[0].Points {
		if p.Value == nil || *p.Value != 10 {
			t.Errorf("%s: want 10/s from org 1 alone, org 2 counts 1000/s", p.Timestamp)
		}
	}
}

// The typical use: error ratio per route across two metrics, each route divided
// by its own traffic.
func TestQueryExpressionErrorRatioPerRouteLive(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC().Truncate(time.Minute)
	suffix := end.UnixNano()
	errorsMetric := fmt.Sprintf("it_errors_%d", suffix)
	requestsMetric := fmt.Sprintf("it_requests_%d", suffix)
	seedCounter(t, s, 1, errorsMetric, map[string]string{"route": "/cart"}, 6, end)
	seedCounter(t, s, 1, errorsMetric, map[string]string{"route": "/checkout"}, 60, end)
	seedCounter(t, s, 1, requestsMetric, map[string]string{"route": "/cart"}, 600, end)
	seedCounter(t, s, 1, requestsMetric, map[string]string{"route": "/checkout"}, 600, end)
	refreshSeries(t, s)

	results, err := s.QueryExpression(context.Background(), SeriesExpressionQuery{
		OrganizationID: 1,
		Queries: []NamedSeriesQuery{
			{Ref: "A", MetricName: errorsMetric, GroupBy: "route", Aggregation: AggregationRate},
			{Ref: "B", MetricName: requestsMetric, GroupBy: "route", Aggregation: AggregationRate},
		},
		Expression: "$A / $B * 100",
		Start:      end.Add(-5 * time.Minute),
		End:        end.Add(-time.Second),
		Step:       time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"/cart": 1, "/checkout": 10}
	if len(results) != 2 {
		t.Fatalf("got %d series, want one per route", len(results))
	}
	for _, r := range results {
		for _, p := range r.Points {
			if p.Value == nil || *p.Value != want[r.Labels["route"]] {
				t.Errorf("%s %s: want %v%%", r.Labels["route"], p.Timestamp, want[r.Labels["route"]])
			}
		}
	}
}

// search.max_buckets caps a whole search, and the rate path multiplies buckets
// by series. A tight step has to lower the series cap: past the limit
// OpenSearch answers too_many_buckets_exception, which is not a series error
// and would reach the client as a 500 instead of ErrSeriesTooMany.
func TestRateSeriesLimitStaysInsideTheBucketBudget(t *testing.T) {
	for _, tc := range []struct {
		span time.Duration
		step time.Duration
	}{
		{6 * time.Hour, 30 * time.Second}, // the reported repro
		{24 * time.Hour, time.Minute},     // widest range at the finest useful step
		{time.Hour, time.Second},          // step floor
	} {
		end := rateT0
		fetchStart := end.Add(-tc.span - tc.step)
		limit := rateSeriesLimit(fetchStart, end, tc.step)
		if limit < 1 {
			t.Errorf("span %s step %s: %d series leaves no query at all", tc.span, tc.step, limit)
			continue
		}
		buckets := limit * (int(end.Sub(fetchStart)/tc.step) + 1)
		if buckets > openSearchMaxBuckets {
			t.Errorf("span %s step %s: %d series x buckets = %d, over the %d OpenSearch limit",
				tc.span, tc.step, limit, buckets, openSearchMaxBuckets)
		}
	}
}

// The cap only has to give way to the bucket budget: an ordinary dashboard step
// must still reach the full 200 series rather than silently losing hosts.
func TestRateSeriesLimitKeepsTheFullCapOnACoarseStep(t *testing.T) {
	end := rateT0
	if got := rateSeriesLimit(end.Add(-6*time.Hour), end, 5*time.Minute); got != maxRateSeries {
		t.Errorf("got %d series, want the full %d cap", got, maxRateSeries)
	}
}
