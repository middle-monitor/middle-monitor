package services

import (
	"fmt"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// MaxOTLPMessageBytes keeps every Kafka message under the broker's 1 MiB default,
// with room for the headers. One agent scrape of a busy exporter crosses 1 MiB
// on its own, and a rejected message used to drop the whole scrape.
const MaxOTLPMessageBytes = 900 << 10

// SplitOTLP cuts an OTLP export request into requests of at most max bytes. A
// body already under the limit is returned as is, without decoding it.
func SplitOTLP(signal string, body []byte, max int) ([][]byte, error) {
	if len(body) <= max {
		return [][]byte{body}, nil
	}
	switch signal {
	case "metrics":
		req := &colmetricspb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(body, req); err != nil {
			return nil, fmt.Errorf("split metrics: %w", ErrOTLPDecode)
		}
		return splitMetrics(req.ResourceMetrics, max)
	case "traces":
		req := &coltracepb.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(body, req); err != nil {
			return nil, fmt.Errorf("split traces: %w", ErrOTLPDecode)
		}
		return splitTraces(req.ResourceSpans, max)
	case "logs":
		req := &collogspb.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(body, req); err != nil {
			return nil, fmt.Errorf("split logs: %w", ErrOTLPDecode)
		}
		return splitLogs(req.ResourceLogs, max)
	}
	return nil, ErrOTLPSignal
}

// chunker packs leaf items into requests, carrying each item's resource and scope
// along so every chunk stays a self-contained export request.
type chunker[R any] struct {
	max    int
	build  func([]R) proto.Message
	out    [][]byte
	cur    []R
	curLen int
}

func (c *chunker[R]) add(item R, size int) error {
	// An item alone over the limit cannot be split further: refuse it rather than
	// ship a message the broker will reject.
	if size > c.max {
		return ErrOTLPItemTooLarge
	}
	if c.curLen+size > c.max && len(c.cur) > 0 {
		if err := c.flush(); err != nil {
			return err
		}
	}
	c.cur = append(c.cur, item)
	c.curLen += size
	return nil
}

func (c *chunker[R]) flush() error {
	if len(c.cur) == 0 {
		return nil
	}
	b, err := proto.Marshal(c.build(c.cur))
	if err != nil {
		return fmt.Errorf("split: %w", ErrOTLPDecode)
	}
	c.out = append(c.out, b)
	c.cur, c.curLen = nil, 0
	return nil
}

func (c *chunker[R]) done() ([][]byte, error) {
	if err := c.flush(); err != nil {
		return nil, err
	}
	return c.out, nil
}

// Envelope bytes a resource and a scope add around one leaf item.
func envelopeSize(resource, scope proto.Message) int {
	return proto.Size(resource) + proto.Size(scope) + 16
}

func splitMetrics(in []*metricspb.ResourceMetrics, max int) ([][]byte, error) {
	c := &chunker[*metricspb.ResourceMetrics]{max: max, build: func(rs []*metricspb.ResourceMetrics) proto.Message {
		return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: mergeResourceMetrics(rs)}
	}}
	for _, rm := range in {
		for _, sm := range rm.ScopeMetrics {
			env := envelopeSize(rm.Resource, sm.Scope)
			for _, m := range sm.Metrics {
				for _, part := range splitMetricPoints(m, max-env) {
					item := &metricspb.ResourceMetrics{Resource: rm.Resource, SchemaUrl: rm.SchemaUrl,
						ScopeMetrics: []*metricspb.ScopeMetrics{{Scope: sm.Scope, SchemaUrl: sm.SchemaUrl, Metrics: []*metricspb.Metric{part}}}}
					if err := c.add(item, proto.Size(part)+env); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return c.done()
}

// splitMetricPoints splits a metric with many data points into several metrics
// sharing its name, so an SDK histogram over thousands of attribute sets still fits.
func splitMetricPoints(m *metricspb.Metric, max int) []*metricspb.Metric {
	if proto.Size(m) <= max {
		return []*metricspb.Metric{m}
	}
	var parts []*metricspb.Metric
	header := proto.Size(&metricspb.Metric{Name: m.Name, Description: m.Description, Unit: m.Unit}) + 16
	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, pts := range packPoints(d.Gauge.DataPoints, max-header) {
			parts = append(parts, &metricspb.Metric{Name: m.Name, Description: m.Description, Unit: m.Unit,
				Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: pts}}})
		}
	case *metricspb.Metric_Sum:
		for _, pts := range packPoints(d.Sum.DataPoints, max-header) {
			parts = append(parts, &metricspb.Metric{Name: m.Name, Description: m.Description, Unit: m.Unit,
				Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{DataPoints: pts, AggregationTemporality: d.Sum.AggregationTemporality, IsMonotonic: d.Sum.IsMonotonic}}})
		}
	case *metricspb.Metric_Histogram:
		for _, pts := range packPoints(d.Histogram.DataPoints, max-header) {
			parts = append(parts, &metricspb.Metric{Name: m.Name, Description: m.Description, Unit: m.Unit,
				Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{DataPoints: pts, AggregationTemporality: d.Histogram.AggregationTemporality}}})
		}
	default:
		// Summaries and exponential histograms are rare and stored as nothing today.
		return []*metricspb.Metric{m}
	}
	return parts
}

func packPoints[P proto.Message](points []P, max int) [][]P {
	var out [][]P
	var cur []P
	size := 0
	for _, p := range points {
		s := proto.Size(p) + 8
		if size+s > max && len(cur) > 0 {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, p)
		size += s
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func splitTraces(in []*tracepb.ResourceSpans, max int) ([][]byte, error) {
	c := &chunker[*tracepb.ResourceSpans]{max: max, build: func(rs []*tracepb.ResourceSpans) proto.Message {
		return &coltracepb.ExportTraceServiceRequest{ResourceSpans: mergeResourceSpans(rs)}
	}}
	for _, rs := range in {
		for _, ss := range rs.ScopeSpans {
			env := envelopeSize(rs.Resource, ss.Scope)
			for _, span := range ss.Spans {
				item := &tracepb.ResourceSpans{Resource: rs.Resource, SchemaUrl: rs.SchemaUrl,
					ScopeSpans: []*tracepb.ScopeSpans{{Scope: ss.Scope, SchemaUrl: ss.SchemaUrl, Spans: []*tracepb.Span{span}}}}
				if err := c.add(item, proto.Size(span)+env); err != nil {
					return nil, err
				}
			}
		}
	}
	return c.done()
}

func splitLogs(in []*logspb.ResourceLogs, max int) ([][]byte, error) {
	c := &chunker[*logspb.ResourceLogs]{max: max, build: func(rs []*logspb.ResourceLogs) proto.Message {
		return &collogspb.ExportLogsServiceRequest{ResourceLogs: mergeResourceLogs(rs)}
	}}
	for _, rl := range in {
		for _, sl := range rl.ScopeLogs {
			env := envelopeSize(rl.Resource, sl.Scope)
			for _, rec := range sl.LogRecords {
				item := &logspb.ResourceLogs{Resource: rl.Resource, SchemaUrl: rl.SchemaUrl,
					ScopeLogs: []*logspb.ScopeLogs{{Scope: sl.Scope, SchemaUrl: sl.SchemaUrl, LogRecords: []*logspb.LogRecord{rec}}}}
				if err := c.add(item, proto.Size(rec)+env); err != nil {
					return nil, err
				}
			}
		}
	}
	return c.done()
}

// mergeResourceMetrics folds consecutive single-metric items that share a
// resource and scope back together, so the resource is written once per chunk
// rather than once per metric.
func mergeResourceMetrics(items []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	var out []*metricspb.ResourceMetrics
	for _, it := range items {
		sm := it.ScopeMetrics[0]
		if n := len(out); n > 0 && out[n-1].Resource == it.Resource {
			last := out[n-1].ScopeMetrics[len(out[n-1].ScopeMetrics)-1]
			if last.Scope == sm.Scope {
				last.Metrics = append(last.Metrics, sm.Metrics...)
				continue
			}
			out[n-1].ScopeMetrics = append(out[n-1].ScopeMetrics, &metricspb.ScopeMetrics{Scope: sm.Scope, SchemaUrl: sm.SchemaUrl, Metrics: sm.Metrics})
			continue
		}
		out = append(out, &metricspb.ResourceMetrics{Resource: it.Resource, SchemaUrl: it.SchemaUrl,
			ScopeMetrics: []*metricspb.ScopeMetrics{{Scope: sm.Scope, SchemaUrl: sm.SchemaUrl, Metrics: sm.Metrics}}})
	}
	return out
}

func mergeResourceSpans(items []*tracepb.ResourceSpans) []*tracepb.ResourceSpans {
	var out []*tracepb.ResourceSpans
	for _, it := range items {
		ss := it.ScopeSpans[0]
		if n := len(out); n > 0 && out[n-1].Resource == it.Resource {
			last := out[n-1].ScopeSpans[len(out[n-1].ScopeSpans)-1]
			if last.Scope == ss.Scope {
				last.Spans = append(last.Spans, ss.Spans...)
				continue
			}
			out[n-1].ScopeSpans = append(out[n-1].ScopeSpans, &tracepb.ScopeSpans{Scope: ss.Scope, SchemaUrl: ss.SchemaUrl, Spans: ss.Spans})
			continue
		}
		out = append(out, &tracepb.ResourceSpans{Resource: it.Resource, SchemaUrl: it.SchemaUrl,
			ScopeSpans: []*tracepb.ScopeSpans{{Scope: ss.Scope, SchemaUrl: ss.SchemaUrl, Spans: ss.Spans}}})
	}
	return out
}

func mergeResourceLogs(items []*logspb.ResourceLogs) []*logspb.ResourceLogs {
	var out []*logspb.ResourceLogs
	for _, it := range items {
		sl := it.ScopeLogs[0]
		if n := len(out); n > 0 && out[n-1].Resource == it.Resource {
			last := out[n-1].ScopeLogs[len(out[n-1].ScopeLogs)-1]
			if last.Scope == sl.Scope {
				last.LogRecords = append(last.LogRecords, sl.LogRecords...)
				continue
			}
			out[n-1].ScopeLogs = append(out[n-1].ScopeLogs, &logspb.ScopeLogs{Scope: sl.Scope, SchemaUrl: sl.SchemaUrl, LogRecords: sl.LogRecords})
			continue
		}
		out = append(out, &logspb.ResourceLogs{Resource: it.Resource, SchemaUrl: it.SchemaUrl,
			ScopeLogs: []*logspb.ScopeLogs{{Scope: sl.Scope, SchemaUrl: sl.SchemaUrl, LogRecords: sl.LogRecords}}})
	}
	return out
}
