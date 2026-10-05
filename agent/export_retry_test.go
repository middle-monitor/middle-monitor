package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/resource"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// A receiver restart used to cost a data point: the first 503 was final.
func TestSendMetricsRetriesATransientFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cfg := &AgentConfig{Interval: 10}
	cfg.API.URL = srv.URL

	if err := sendMetrics(cfg, &AgentMetrics{Hostname: "web-01"}); err != nil {
		t.Fatalf("want success after retries, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("got %d calls, want 3", got)
	}
}

// A refused token fails the same way every time; retrying it only adds load.
func TestSendMetricsDoesNotRetryAClientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	cfg := &AgentConfig{Interval: 10}
	cfg.API.URL = srv.URL

	if err := sendMetrics(cfg, &AgentMetrics{Hostname: "web-01"}); err == nil {
		t.Fatal("want the 401 reported")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("got %d calls, want 1", got)
	}
}

// A busy exporter used to go out as one request over the receiver's 1 MiB message
// limit and be dropped whole. Each request must now be bounded and compressed,
// and together they must carry every sample.
func TestScrapeExportIsBatchedAndCompressed(t *testing.T) {
	var requests, points atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("request not gzip-compressed")
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(zr)
		req := &colmetricspb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(body, req); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					n += len(m.GetGauge().GetDataPoints())
				}
			}
		}
		if n > maxSamplesPerExport {
			t.Errorf("one request carried %d samples, over %d", n, maxSamplesPerExport)
		}
		points.Add(int32(n))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exporter, err := otlpmetrichttp.New(context.Background(),
		otlpmetrichttp.WithEndpointURL(srv.URL+"/v1/metrics"),
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
	if err != nil {
		t.Fatal(err)
	}
	previous, previousRes := scrapeExporter, scrapeResource
	scrapeExporter, scrapeResource = exporter, resource.Empty()
	t.Cleanup(func() { scrapeExporter, scrapeResource = previous, previousRes })

	samples := make([]ScrapedSample, 5000)
	for i := range samples {
		samples[i] = ScrapedSample{Name: fmt.Sprintf("dns_bucket_%d", i%20), Labels: map[string]string{"le": fmt.Sprint(i)}, Value: float64(i)}
	}
	if err := exportScrapedSamples(context.Background(), ScrapeTarget{Name: "dns"}, samples); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 || points.Load() != 5000 {
		t.Errorf("got %d requests carrying %d samples, want 3 carrying 5000", requests.Load(), points.Load())
	}
}
