package services

import (
	"errors"
	"fmt"
	"testing"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func strAttr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

var splitResource = &resourcepb.Resource{Attributes: []*commonpb.KeyValue{strAttr("host.name", "web-01")}}

func point(i int) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		Attributes: []*commonpb.KeyValue{strAttr("route", fmt.Sprintf("/route/%d", i)), strAttr("le", "0.25")},
		Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
	}
}

// The agent's shape: one gauge per scraped sample, thousands of them.
func scrapeLikeRequest(samples int) []byte {
	sm := &metricspb.ScopeMetrics{Scope: &commonpb.InstrumentationScope{Name: "scrape"}}
	for i := 0; i < samples; i++ {
		sm.Metrics = append(sm.Metrics, &metricspb.Metric{
			Name: fmt.Sprintf("dns_responsetime_seconds_bucket_%d", i%20),
			Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{point(i)}}},
		})
	}
	b, _ := proto.Marshal(&colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{
		{Resource: splitResource, ScopeMetrics: []*metricspb.ScopeMetrics{sm}},
	}})
	return b
}

func decodeMetricChunks(t *testing.T, chunks [][]byte, max int) (points int) {
	t.Helper()
	for i, c := range chunks {
		if len(c) > max {
			t.Errorf("chunk %d is %d bytes, over the %d limit", i, len(c), max)
		}
		req := &colmetricspb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(c, req); err != nil {
			t.Fatalf("chunk %d does not decode: %v", i, err)
		}
		for _, rm := range req.ResourceMetrics {
			// Without its resource a chunk's points lose their host and land unattributed.
			if !proto.Equal(rm.Resource, splitResource) {
				t.Errorf("chunk %d lost its resource", i)
			}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					points += len(m.GetGauge().GetDataPoints())
				}
			}
		}
	}
	return points
}

// A scrape over the broker's 1 MiB used to be rejected whole, every interval.
// Splitting must keep every point and never produce a message over the limit.
func TestSplitOTLPKeepsEveryPointUnderTheLimit(t *testing.T) {
	body := scrapeLikeRequest(3000)
	max := 50 << 10

	chunks, err := SplitOTLP("metrics", body, max)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("a %d byte request under a %d limit must split, got %d chunk", len(body), max, len(chunks))
	}
	if got := decodeMetricChunks(t, chunks, max); got != 3000 {
		t.Errorf("got %d points across chunks, want 3000", got)
	}
}

// The common case must not pay for a decode and re-encode.
func TestSplitOTLPLeavesASmallRequestUntouched(t *testing.T) {
	body := scrapeLikeRequest(10)
	chunks, err := SplitOTLP("metrics", body, MaxOTLPMessageBytes)
	if err != nil || len(chunks) != 1 || &chunks[0][0] != &body[0] {
		t.Fatalf("want the original bytes back, got %d chunks, err %v", len(chunks), err)
	}
}

// An SDK histogram can carry thousands of attribute sets in one metric.
func TestSplitOTLPSplitsOneMetricWithManyPoints(t *testing.T) {
	gauge := &metricspb.Gauge{}
	for i := 0; i < 2000; i++ {
		gauge.DataPoints = append(gauge.DataPoints, point(i))
	}
	body, _ := proto.Marshal(&colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		Resource:     splitResource,
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "requests", Data: &metricspb.Metric_Gauge{Gauge: gauge}}}}},
	}}})
	max := 20 << 10

	chunks, err := SplitOTLP("metrics", body, max)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeMetricChunks(t, chunks, max); got != 2000 {
		t.Errorf("got %d points, want 2000", got)
	}
}

func TestSplitOTLPRefusesAnItemThatCannotFit(t *testing.T) {
	huge := make([]byte, 4096)
	body, _ := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{
			{Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: huge}}},
			{Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "small"}}},
		}}},
	}}})
	if _, err := SplitOTLP("logs", body, 1024); !errors.Is(err, ErrOTLPItemTooLarge) {
		t.Errorf("got %v, want ErrOTLPItemTooLarge", err)
	}
}

func TestSplitOTLPSplitsTracesAndLogs(t *testing.T) {
	ss := &tracepb.ScopeSpans{}
	sl := &logspb.ScopeLogs{}
	for i := 0; i < 1000; i++ {
		ss.Spans = append(ss.Spans, &tracepb.Span{Name: fmt.Sprintf("GET /route/%d", i), TraceId: make([]byte, 16), SpanId: make([]byte, 8)})
		sl.LogRecords = append(sl.LogRecords, &logspb.LogRecord{Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("request %d failed", i)}}})
	}
	traces, _ := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: splitResource, ScopeSpans: []*tracepb.ScopeSpans{ss}}}})
	logs, _ := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{Resource: splitResource, ScopeLogs: []*logspb.ScopeLogs{sl}}}})
	max := 8 << 10

	chunks, err := SplitOTLP("traces", traces, max)
	if err != nil {
		t.Fatal(err)
	}
	spans := 0
	for _, c := range chunks {
		req := &coltracepb.ExportTraceServiceRequest{}
		if len(c) > max || proto.Unmarshal(c, req) != nil {
			t.Fatal("bad trace chunk")
		}
		for _, rs := range req.ResourceSpans {
			if !proto.Equal(rs.Resource, splitResource) {
				t.Error("trace chunk lost its resource")
			}
			for _, s := range rs.ScopeSpans {
				spans += len(s.Spans)
			}
		}
	}
	if spans != 1000 || len(chunks) < 2 {
		t.Errorf("traces: %d spans in %d chunks, want 1000 in several", spans, len(chunks))
	}

	chunks, err = SplitOTLP("logs", logs, max)
	if err != nil {
		t.Fatal(err)
	}
	records := 0
	for _, c := range chunks {
		req := &collogspb.ExportLogsServiceRequest{}
		if len(c) > max || proto.Unmarshal(c, req) != nil {
			t.Fatal("bad log chunk")
		}
		for _, rl := range req.ResourceLogs {
			for _, s := range rl.ScopeLogs {
				records += len(s.LogRecords)
			}
		}
	}
	if records != 1000 || len(chunks) < 2 {
		t.Errorf("logs: %d records in %d chunks, want 1000 in several", records, len(chunks))
	}
}
