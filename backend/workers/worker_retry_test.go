package workers

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"middle-monitor/backend/models"
)

// A service restarting between two cycles must not surface as an error: the
// check retries within the same cycle and only the recovered result is recorded.
func TestRunCheckWithRetriesRecoversFromTransientFailure(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	result := runCheckWithRetries(nil, models.Service{ID: 1, Type: "http", Host: ts.URL, ServiceInterval: 60, MaxAttempts: 3})

	if result.Status != "success" {
		t.Fatalf("status = %q, want success (message: %v)", result.Status, result.Message)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2 (the failure then the retry that recovered)", got)
	}
}

// A real outage must still be reported, and only after max_attempts attempts —
// that is what makes a single stored failure worth alerting on immediately.
func TestRunCheckWithRetriesReportsPersistentFailure(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	result := runCheckWithRetries(nil, models.Service{ID: 1, Type: "http", Host: ts.URL, ServiceInterval: 60, MaxAttempts: 2})

	if result.Status != "failure" {
		t.Fatalf("status = %q, want failure", result.Status)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2 (max_attempts)", got)
	}
}

// Retries must never spill into the next scheduled run: a short interval caps
// the number of attempts instead of letting the check overlap itself.
func TestRunCheckWithRetriesStopsBeforeNextCycle(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	result := runCheckWithRetries(nil, models.Service{ID: 1, Type: "http", Host: ts.URL, ServiceInterval: 1, MaxAttempts: 5})

	if result.Status != "failure" {
		t.Fatalf("status = %q, want failure", result.Status)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (the 2s backoff does not fit in a 1s interval)", got)
	}
}
