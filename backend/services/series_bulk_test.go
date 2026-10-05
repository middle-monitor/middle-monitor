package services

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBulk struct {
	mu       sync.Mutex
	requests int
	docs     int
	indices  map[string]int
	answer   string
}

func (f *fakeBulk) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for line := 0; sc.Scan(); line++ {
		if line%2 == 1 {
			f.docs++
			continue
		}
		var action struct {
			Index struct {
				Index string `json:"_index"`
			} `json:"index"`
		}
		json.Unmarshal(sc.Bytes(), &action)
		f.indices[action.Index.Index]++
	}
	answer := f.answer
	if answer == "" {
		answer = `{"errors":false}`
	}
	w.Write([]byte(answer))
}

func bulkService(t *testing.T, f *fakeBulk) *OpenSearchService {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	s := NewOpenSearchService()
	s.baseURL = srv.URL
	s.client = srv.Client()
	s.initialized = true
	return s
}

func bulkPoints(n int, start time.Time) []SeriesPoint {
	points := make([]SeriesPoint, n)
	for i := range points {
		points[i] = SeriesPoint{Timestamp: start.Add(time.Duration(i) * time.Second), OrganizationID: 7,
			MetricName: "dns_bucket", Value: 1, Labels: map[string]string{"le": fmt.Sprint(i)}}
	}
	return points
}

// One request per point was what pinned the worker and OpenSearch at 20 000
// points a minute: a scrape must now cost a handful of requests.
func TestIndexSeriesPointsBatchesRequests(t *testing.T) {
	f := &fakeBulk{indices: map[string]int{}}
	s := bulkService(t, f)

	if err := s.IndexSeriesPoints(context.Background(), bulkPoints(4500, time.Now())); err != nil {
		t.Fatal(err)
	}
	if f.requests != 3 || f.docs != 4500 {
		t.Errorf("%d requests carrying %d documents, want 3 carrying 4500", f.requests, f.docs)
	}
}

// Indices are daily: a batch scraped across midnight must split between them,
// or retention would drop a day's points with the wrong index.
func TestIndexSeriesPointsRoutesEachPointToItsDay(t *testing.T) {
	f := &fakeBulk{indices: map[string]int{}}
	s := bulkService(t, f)
	beforeMidnight := time.Date(2026, 10, 3, 23, 59, 0, 0, time.UTC)

	if err := s.IndexSeriesPoints(context.Background(), bulkPoints(120, beforeMidnight)); err != nil {
		t.Fatal(err)
	}
	if f.indices["middle-monitor-series-2026.10.03"] != 60 || f.indices["middle-monitor-series-2026.10.04"] != 60 {
		t.Errorf("got %v, want 60 points in each day", f.indices)
	}
}

// A bulk answers 200 even when documents are refused; staying silent would be
// the "status 400 for two months" mistake again, one level down.
func TestIndexSeriesPointsReportsRefusedDocuments(t *testing.T) {
	f := &fakeBulk{indices: map[string]int{}, answer: `{"errors":true,"items":[{"index":{"error":{"type":"mapper_parsing_exception","reason":"failed to parse field [value]. Preview of field's value: 'secret'"}}},{"index":{}}]}`}
	s := bulkService(t, f)

	err := s.IndexSeriesPoints(context.Background(), bulkPoints(2, time.Now()))
	var bulkErr *SeriesBulkError
	if !errors.As(err, &bulkErr) || bulkErr.Failed != 1 || bulkErr.Total != 2 {
		t.Fatalf("got %v, want 1 of 2 refused", err)
	}
	if !errors.Is(err, ErrSeriesIndex) || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "mapper_parsing_exception") {
		t.Errorf("want the reason without the rejected value: %v", err)
	}
}

func TestIndexSeriesPointsLive(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC().Truncate(time.Second)
	name := fmt.Sprintf("it_bulk_%d", end.UnixNano())
	points := bulkPoints(5000, end.Add(-2*time.Hour))
	for i := range points {
		points[i].MetricName = name
		points[i].OrganizationID = 1
	}
	if err := s.IndexSeriesPoints(context.Background(), points); err != nil {
		t.Fatal(err)
	}
	refreshSeries(t, s)
	got, err := s.AggregateSeries(context.Background(), SeriesQuery{OrganizationID: 1, MetricName: name, Aggregation: "count",
		Start: end.Add(-3 * time.Hour), End: end})
	if err != nil || got == nil || *got != 5000 {
		t.Fatalf("stored %v (err %v), want 5000", got, err)
	}
}
