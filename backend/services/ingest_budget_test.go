package services

import (
	"errors"
	"sync"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// The pricing page promises these numbers; the budget must derive the same.
func TestPointsPerMinuteFollowsThePlanHosts(t *testing.T) {
	cases := map[string]struct {
		limits PlanLimits
		want   int
	}{
		"free":                 {LimitsForPlan("free"), 250},
		"pro":                  {LimitsForPlan("pro"), 2500},
		"custom with 25 hosts": {PlanLimits{MaxHosts: 25}, 6250},
		"unlimited hosts":      {PlanLimits{MaxHosts: -1}, -1},
	}
	for name, c := range cases {
		if got := PointsPerMinute(c.limits); got != c.want {
			t.Errorf("%s: got %d, want %d", name, got, c.want)
		}
	}
}

type flushed struct {
	org, minute    int64
	accepted, over int
}

func testBudget(limit int, lookupErr error) (*IngestBudget, *time.Time, *[]flushed, *sync.Mutex) {
	now := time.Date(2026, 10, 2, 12, 0, 10, 0, time.UTC)
	var mu sync.Mutex
	var got []flushed
	b := &IngestBudget{
		windows: map[int64]*budgetWindow{},
		limits:  map[int64]cachedBudget{},
		lookup:  func(int64) (int, error) { return limit, lookupErr },
		flush: func(org, minute int64, a, r int) {
			mu.Lock()
			got = append(got, flushed{org, minute, a, r})
			mu.Unlock()
		},
		now: func() time.Time { return now },
	}
	return b, &now, &got, &mu
}

func TestIngestBudgetGrantsUpToTheLimitThenResetsEachMinute(t *testing.T) {
	b, now, got, mu := testBudget(1000, nil)

	if g, _ := b.Take(7, 600); g != 600 {
		t.Fatalf("first 600 of 1000: granted %d", g)
	}
	if g, l := b.Take(7, 600); g != 400 || l != 1000 {
		t.Fatalf("next 600 with 400 left: granted %d limit %d, want 400 and 1000", g, l)
	}
	if g, _ := b.Take(7, 10); g != 0 {
		t.Fatalf("over budget: granted %d, want 0", g)
	}

	*now = now.Add(time.Minute)
	if g, _ := b.Take(7, 900); g != 900 {
		t.Errorf("a new minute must start a new budget, granted %d", g)
	}
	b.FlushIdle()
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 || (*got)[0].accepted != 1000 || (*got)[0].over != 210 {
		t.Errorf("the finished minute must be recorded as 1000 accepted, 210 over the limit, got %+v", *got)
	}
}

// A 503 makes the client resend the same points: charging both attempts would
// reject data that was never stored.
func TestIngestBudgetRefundsPointsThatWereNotStored(t *testing.T) {
	b, _, _, _ := testBudget(1000, nil)
	b.Take(7, 800)
	b.Refund(7, 800)
	if g, _ := b.Take(7, 800); g != 800 {
		t.Errorf("after a refund the retry must fit, granted %d", g)
	}
}

// Losing the plan lookup must not reject a paying organization's data.
func TestIngestBudgetFailsOpen(t *testing.T) {
	b, _, _, _ := testBudget(0, errors.New("db down"))
	if g, l := b.Take(7, 5000); g != 5000 || l != -1 {
		t.Errorf("granted %d limit %d, want everything unmetered", g, l)
	}
	if g, _ := b.Take(0, 5000); g != 5000 {
		t.Errorf("unattributed points are not metered, granted %d", g)
	}
}

func histogramPoint(withSum bool) *metricspb.HistogramDataPoint {
	dp := &metricspb.HistogramDataPoint{Count: 3}
	if withSum {
		s := 1.5
		dp.Sum = &s
	}
	return dp
}

func budgetRequest() *colmetricspb.ExportMetricsServiceRequest {
	gauge := &metricspb.Gauge{}
	for i := 0; i < 5; i++ {
		gauge.DataPoints = append(gauge.DataPoints, point(i))
	}
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{
			{Name: "g", Data: &metricspb.Metric_Gauge{Gauge: gauge}},
			{Name: "h", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{histogramPoint(true), histogramPoint(false)}}}},
			{Name: "s", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{DataPoints: []*metricspb.NumberDataPoint{point(9)}}}},
		}}},
	}}}
}

// A histogram point is stored as _count and _sum: the budget must count what
// lands in the store, or the limit shown would not match the documents written.
func TestCountMetricPointsCountsStoredDocuments(t *testing.T) {
	if got := CountMetricPoints(budgetRequest()); got != 5+2+1+1 {
		t.Errorf("got %d, want 9 (5 gauge, 2+1 histogram, 1 sum)", got)
	}
}

func TestTrimMetricPointsKeepsExactlyTheBudget(t *testing.T) {
	for budget := 0; budget <= 9; budget++ {
		req := budgetRequest()
		TrimMetricPoints(req, budget)
		if got := CountMetricPoints(req); got > budget {
			t.Errorf("budget %d: %d points kept", budget, got)
		}
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if CountMetricPoints(&colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{m}}}}}}) == 0 {
						t.Errorf("budget %d: empty metric %s left in the request", budget, m.Name)
					}
				}
			}
		}
	}
	// Budget 6: the 5 gauge points fit, the histogram point with a sum costs 2
	// with 1 left and is dropped, the one without a sum costs 1 and still fits.
	req := budgetRequest()
	TrimMetricPoints(req, 6)
	if got := CountMetricPoints(req); got != 6 {
		t.Errorf("budget 6: kept %d points, want 6", got)
	}
}

// Agents slow down on what the organization sent in the last finished minute:
// the minute in progress is partial, and an older one no longer describes now.
func TestIngestBudgetStatusReportsTheLastFinishedMinute(t *testing.T) {
	b, now, _, _ := testBudget(1000, nil)
	b.Take(7, 600)
	b.Take(7, 900)

	if limit, last := b.Status(7); limit != 1000 || last != 0 {
		t.Errorf("during the first minute: limit %d last %d, want 1000 and 0", limit, last)
	}
	*now = now.Add(time.Minute)
	if _, last := b.Status(7); last != 1500 {
		t.Errorf("next minute: last %d, want 1500 (accepted and over the limit)", last)
	}
	b.Take(7, 10)
	if _, last := b.Status(7); last != 1500 {
		t.Errorf("after a new minute started: last %d, want 1500 still", last)
	}
	*now = now.Add(2 * time.Minute)
	if _, last := b.Status(7); last != 0 {
		t.Errorf("an organization silent for a minute sent nothing: last %d, want 0", last)
	}
}
