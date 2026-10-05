package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRetention struct {
	mu         sync.Mutex
	tasks      string // body answered on GET /_tasks
	tasksFail  bool
	seriesURLs []string
	otherPaths []string
}

func (f *fakeRetention) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/_tasks"):
		if f.tasksFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(f.tasks))
	case strings.Contains(r.URL.Path, SeriesIndexPattern) && strings.HasSuffix(r.URL.Path, "_delete_by_query"):
		f.seriesURLs = append(f.seriesURLs, r.URL.String())
		w.Write([]byte(`{"task":"node:42"}`))
	case strings.HasSuffix(r.URL.Path, "_delete_by_query"):
		f.otherPaths = append(f.otherPaths, r.URL.Path)
		w.Write([]byte(`{"deleted":3}`))
	default:
		w.Write([]byte(`{}`))
	}
}

func retentionService(t *testing.T, f *fakeRetention) *OpenSearchService {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	s := NewOpenSearchService()
	s.baseURL, s.client, s.initialized = srv.URL, srv.Client(), true
	return s
}

var testRetentions = []OrgRetention{{OrgID: 7, Cutoff: time.Now().Add(-7 * 24 * time.Hour)}}

// Unthrottled, a downgrade's 216 million expired points held OpenSearch at 450%
// CPU for hours: the series purge must run in the background at a set pace.
func TestSeriesRetentionIsThrottledAndBackground(t *testing.T) {
	f := &fakeRetention{tasks: `{"nodes":{}}`}
	s := retentionService(t, f)

	if _, err := s.DeleteByRetention(testRetentions); err != nil {
		t.Fatal(err)
	}
	if len(f.seriesURLs) != 1 {
		t.Fatalf("got %d series purges, want 1", len(f.seriesURLs))
	}
	for _, want := range []string{"wait_for_completion=false", "requests_per_second=200", "conflicts=proceed"} {
		if !strings.Contains(f.seriesURLs[0], want) {
			t.Errorf("series purge %s lacks %s", f.seriesURLs[0], want)
		}
	}
	// The small indices keep their synchronous purge.
	if len(f.otherPaths) != 4 {
		t.Errorf("got %d other index purges, want 4: %v", len(f.otherPaths), f.otherPaths)
	}
}

// A slow purge outlives the run that started it; the nightly run and every
// worker start must not stack a second, unthrottled one on top.
func TestSeriesRetentionDoesNotStackOnARunningPurge(t *testing.T) {
	f := &fakeRetention{tasks: `{"nodes":{"n1":{"tasks":{"n1:9":{"description":"delete-by-query [middle-monitor-series-*]"}}}}}`}
	s := retentionService(t, f)

	if _, err := s.DeleteByRetention(testRetentions); err != nil {
		t.Fatal(err)
	}
	if len(f.seriesURLs) != 0 {
		t.Errorf("a purge is running, yet another was started: %v", f.seriesURLs)
	}
}

// Not knowing whether a purge runs is treated as "it does": a duplicate purge
// is what overloaded prod, a skipped one is retried at the next run.
func TestSeriesRetentionSkipsWhenTasksAreUnknown(t *testing.T) {
	f := &fakeRetention{tasksFail: true}
	s := retentionService(t, f)

	if _, err := s.DeleteByRetention(testRetentions); err != nil {
		t.Fatal(err)
	}
	if len(f.seriesURLs) != 0 {
		t.Errorf("started a purge without knowing what runs: %v", f.seriesURLs)
	}
}
