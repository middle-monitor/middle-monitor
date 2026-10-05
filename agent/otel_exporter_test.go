package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestExportMetricsOTLPFlushesWithoutDeadlock pins the reason exportMetricsOTLP
// must not hold metricStateMutex while flushing: ForceFlush runs the observation
// callback, which read-locks the same mutex. Go's sync.RWMutex is not reentrant,
// so holding the write lock across the flush blocks the callback until the
// context expires and no metric ever reaches the backend.
func TestExportMetricsOTLPFlushesWithoutDeadlock(t *testing.T) {
	var exports int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/metrics" {
			atomic.AddInt64(&exports, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	config := &AgentConfig{}
	config.API.URL = srv.URL
	config.Host.Service = "test-service"
	config.Interval = 60

	if err := initializeOTELMetrics(config); err != nil {
		t.Fatalf("initializeOTELMetrics: %v", err)
	}
	defer meterProvider.Shutdown(context.Background())

	metrics := &AgentMetrics{
		Hostname:  "test-host",
		Service:   "test-service",
		Metrics:   map[string]float64{"cpu": 42, "ram": 50, "disk": 60},
		Metadata:  map[string]float64{"ram_total_gb": 16, "disk_total_gb": 500, "disk_free_gb": 200},
		Timestamp: time.Now(),
	}

	done := make(chan error, 1)
	go func() { done <- exportMetricsOTLP(metrics) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exportMetricsOTLP: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exportMetricsOTLP deadlocked: the flush never observed the metric state")
	}

	if got := atomic.LoadInt64(&exports); got == 0 {
		t.Fatal("no OTLP export reached the backend")
	}
}
