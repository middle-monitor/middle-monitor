package services

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/models"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortraces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// OTLPReceiverService handles receiving OTLP data and exporting to OpenSearch
type OTLPReceiverService struct {
	opensearch *OpenSearchService
	db         *sql.DB
}

// NewOTLPReceiverService creates a new OTLP receiver service
func NewOTLPReceiverService(opensearch *OpenSearchService, db *sql.DB) *OTLPReceiverService {
	return &OTLPReceiverService{
		opensearch: opensearch,
		db:         db,
	}
}

// ReceiveTraces handles OTLP trace export requests
func (s *OTLPReceiverService) ReceiveTraces(ctx context.Context, data []byte, orgID int64) error {
	// Parse OTLP trace request
	var request collectortraces.ExportTraceServiceRequest
	if err := proto.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("%w: %w", ErrTraceRequestUnmarshal, err)
	}

	var indexed int
	// Convert and index each trace
	for _, resourceSpans := range request.ResourceSpans {
		resource := resourceSpans.Resource
		serviceName := extractServiceName(resource)
		hostname := extractHostname(resource)

		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				doc := s.convertSpanToDoc(span, resource, serviceName, hostname)
				if orgID > 0 {
					doc["organization_id"] = orgID
				}
				if err := s.opensearch.IndexTrace(ctx, doc); err != nil {
					slog.Error("failed to index trace", "error", err)
					// Continue with other spans even if one fails
				} else {
					indexed++
				}

				if appErr, ok := errorFromReportSpan(span, serviceName, orgID); ok && s.db != nil {
					s.recordErrorReport(ctx, appErr)
				}

				// Check for slow traces asynchronously (e.g. > 1000ms)
				durationMs := float64(span.EndTimeUnixNano-span.StartTimeUnixNano) / 1e6
				if durationMs > 1000 && s.db != nil {
					go func(svcName, spanName string, dur float64) {
						var serviceID int64
						var failureThreshold sql.NullFloat64
						var orgID int64
						qSvc := `SELECT id, organization_id, failure_threshold FROM services
								 WHERE service = $1 AND type LIKE 'trace_service_%' LIMIT 1`
						errSvc := s.db.QueryRow(qSvc, svcName).Scan(&serviceID, &orgID, &failureThreshold)

						if errSvc == nil && failureThreshold.Valid {
							if dur > failureThreshold.Float64 {
								channels, _ := NewAlertService(s.db).GetChannels(orgID)
								svcContext := ServiceAlertContext(s.db, serviceID)
								for _, ch := range channels {
									if ch.Enabled {
										title := fmt.Sprintf("Slow trace: %s", svcName)
										msg := fmt.Sprintf("Error: trace '%s' took %.0f ms (threshold: %.0f ms).", spanName, dur, failureThreshold.Float64) + svcContext
										SendNotification(ch, title, msg)
									}
								}
							}
						}
					}(serviceName, span.Name, durationMs)
				}
			}
		}
	}
	if indexed > 0 {
		slog.Debug("indexed traces to opensearch", "count", indexed)
	}

	return nil
}

// errorFromReportSpan reads the error an SDK's reportError sent as an
// "error.report" span. Middlewares post their errors to /api/v1/errors instead,
// so the two paths never describe the same failure.
func errorFromReportSpan(span *tracepb.Span, serviceName string, orgID int64) (models.ApplicationError, bool) {
	if span.Name != "error.report" || orgID <= 0 {
		return models.ApplicationError{}, false
	}
	attrs := attributesToLabels(span.Attributes)
	message := attrs["error.message"]
	if message == "" && span.Status != nil {
		message = span.Status.Message
	}
	if message == "" {
		return models.ApplicationError{}, false
	}
	appErr := models.ApplicationError{
		OrganizationID: orgID,
		Name:           attrs["error.type"],
		Message:        message,
		File:           attrs["error.file"],
		Timestamp:      timestampToTime(span.StartTimeUnixNano).UTC(),
		Service:        serviceName,
	}
	if appErr.Name == "" {
		appErr.Name = "Error"
	}
	if appErr.File == "" {
		appErr.File = "unknown"
	}
	appErr.Line, _ = strconv.Atoi(attrs["error.line"])
	if method := attrs["http.method"]; method != "" {
		appErr.HTTPMethod = &method
	}
	if url := attrs["http.url"]; url != "" {
		appErr.HTTPURL = &url
	}
	if traceID := bytesToHex(span.TraceId); traceID != "" {
		appErr.TraceID = &traceID
	}
	return appErr, true
}

// recordErrorReport writes the same row and document as POST /api/v1/errors.
func (s *OTLPReceiverService) recordErrorReport(ctx context.Context, appErr models.ApplicationError) {
	result, err := NewErrorService(s.db).CreateError(appErr)
	if err != nil {
		slog.Error("failed to save error report", "service", appErr.Service, "error", err)
		return
	}
	if err := s.opensearch.IndexError(ctx, ErrorDoc(result)); err != nil {
		slog.Error("failed to index error report", "error", err)
	}
}

// ReceiveLogs handles OTLP log export requests
func (s *OTLPReceiverService) ReceiveLogs(ctx context.Context, data []byte, orgID int64) error {
	// Parse OTLP log request
	var request collectorlogs.ExportLogsServiceRequest
	if err := proto.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("%w: %w", ErrLogRequestUnmarshal, err)
	}

	// Convert and index each log
	for _, resourceLogs := range request.ResourceLogs {
		resource := resourceLogs.Resource
		serviceName := extractServiceName(resource)
		hostname := extractHostname(resource)

		for _, scopeLogs := range resourceLogs.ScopeLogs {
			for _, logRecord := range scopeLogs.LogRecords {
				doc := s.convertLogToDoc(logRecord, resource, serviceName, hostname)
				if orgID > 0 {
					doc["organization_id"] = orgID
				}
				if err := s.opensearch.IndexLog(ctx, doc); err != nil {
					slog.Error("failed to index log", "error", err)
					// Continue with other logs even if one fails
				}
			}
		}
	}

	return nil
}

// ReceiveMetrics handles OTLP metrics export requests
func (s *OTLPReceiverService) ReceiveMetrics(ctx context.Context, data []byte, orgID int64) error {
	// Parse OTLP metrics request
	var request collectormetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("%w: %w", ErrMetricsRequestUnmarshal, err)
	}

	// Convert every metric, then index the request's points in bulk.
	var points []SeriesPoint
	for _, resourceMetrics := range request.ResourceMetrics {
		resource := resourceMetrics.Resource
		serviceName := extractServiceName(resource)
		hostname := extractHostname(resource)

		for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
			for _, metric := range scopeMetrics.Metrics {
				points = append(points, convertMetricToSeriesPoints(metric, serviceName, hostname, orgID)...)
			}
		}
	}

	// A refused document is logged by the bulk and does not fail the others.
	if err := s.opensearch.IndexSeriesPoints(ctx, points); err != nil {
		slog.Error("failed to index metrics", "points", len(points), "error", err)
	}
	return nil
}

// convertSpanToDoc converts an OTLP span to an OpenSearch document
func (s *OTLPReceiverService) convertSpanToDoc(
	span *tracepb.Span,
	resource *resourcepb.Resource,
	serviceName, hostname string,
) map[string]interface{} {
	doc := map[string]interface{}{
		"trace_id":       bytesToHex(span.TraceId),
		"span_id":        bytesToHex(span.SpanId),
		"parent_span_id": bytesToHex(span.ParentSpanId),
		"operation_name": span.Name,
		"span_kind":      span.Kind.String(),
		"start_time":     timestampToTime(span.StartTimeUnixNano).Format(time.RFC3339),
		"end_time":       timestampToTime(span.EndTimeUnixNano).Format(time.RFC3339),
		"duration_ms":    float64(span.EndTimeUnixNano-span.StartTimeUnixNano) / 1e6,
		"service_name":   serviceName,
		"hostname":       hostname,
		"@timestamp":     timestampToTime(span.StartTimeUnixNano).Format(time.RFC3339),
	}

	if span.Status != nil {
		doc["status_code"] = span.Status.Code.String()
		if span.Status.Message != "" {
			doc["status_message"] = span.Status.Message
		}
	}

	if span.TraceState != "" {
		doc["trace_state"] = span.TraceState
	}

	// Convert attributes
	if len(span.Attributes) > 0 {
		doc["attributes"] = sanitizeAttrKeys(attributesToMap(span.Attributes))
	}

	// Convert events
	if len(span.Events) > 0 {
		events := make([]map[string]interface{}, 0, len(span.Events))
		for _, event := range span.Events {
			eventDoc := map[string]interface{}{
				"name":       event.Name,
				"timestamp":  timestampToTime(event.TimeUnixNano).Format(time.RFC3339),
				"attributes": sanitizeAttrKeys(attributesToMap(event.Attributes)),
			}
			events = append(events, eventDoc)
		}
		doc["events"] = events
	}

	// Convert links
	if len(span.Links) > 0 {
		links := make([]map[string]interface{}, 0, len(span.Links))
		for _, link := range span.Links {
			linkDoc := map[string]interface{}{
				"trace_id":    bytesToHex(link.TraceId),
				"span_id":     bytesToHex(link.SpanId),
				"trace_state": link.TraceState,
				"attributes":  sanitizeAttrKeys(attributesToMap(link.Attributes)),
			}
			links = append(links, linkDoc)
		}
		doc["links"] = links
	}

	return doc
}

// convertLogToDoc converts an OTLP log record to an OpenSearch document
func (s *OTLPReceiverService) convertLogToDoc(
	logRecord *logspb.LogRecord,
	resource *resourcepb.Resource,
	serviceName, hostname string,
) map[string]interface{} {
	recordAttrs := attributesToMap(logRecord.Attributes)

	// Some SDKs send service identity as log-record attributes instead of on the
	// OTLP resource (the resource then defaults to "unknown_service"). Fall back
	// to the record attributes so the dashboard can filter by the real service.
	if isUnknownService(serviceName) {
		if v, ok := recordAttrs["service.name"].(string); ok && v != "" {
			serviceName = v
		}
	}

	// Event time may be unset (0); fall back to observed time, then to now, so
	// records never land at the Unix epoch (invisible in the default time window).
	ts := logRecord.TimeUnixNano
	if ts == 0 {
		ts = logRecord.ObservedTimeUnixNano
	}
	logTime := timestampToTime(ts)
	if ts == 0 {
		logTime = time.Now()
	}

	doc := map[string]interface{}{
		"service_name": serviceName,
		"hostname":     hostname,
		"@timestamp":   logTime.UTC().Format(time.RFC3339),
	}

	if len(logRecord.TraceId) > 0 {
		doc["trace_id"] = bytesToHex(logRecord.TraceId)
	}
	if len(logRecord.SpanId) > 0 {
		doc["span_id"] = bytesToHex(logRecord.SpanId)
	}

	if logRecord.SeverityNumber != logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED {
		doc["severity"] = logRecord.SeverityNumber.String()
	}
	if logRecord.SeverityText != "" {
		doc["severity_text"] = logRecord.SeverityText
	}

	// Convert body (AnyValue to string)
	if logRecord.Body != nil {
		doc["body"] = anyValueToString(logRecord.Body)
	}

	// Convert attributes
	if len(recordAttrs) > 0 {
		doc["attributes"] = sanitizeAttrKeys(recordAttrs)
		if doc["hostname"] == "" {
			if v, ok := recordAttrs["host.name"].(string); ok && v != "" {
				doc["hostname"] = v
			} else if v, ok := recordAttrs["hostname"].(string); ok && v != "" {
				doc["hostname"] = v
			}
		}
	}

	// Convert resource attributes
	if len(resource.Attributes) > 0 {
		doc["resource"] = sanitizeAttrKeys(attributesToMap(resource.Attributes))
	}

	return doc
}

// convertMetricToSeriesPoints converts an OTLP metric to series datapoints. The
// series labels are the datapoint attributes: resource identity is already carried
// by service_name and hostname.
func convertMetricToSeriesPoints(
	metric *metricspb.Metric,
	serviceName, hostname string,
	orgID int64,
) []SeriesPoint {
	points := make([]SeriesPoint, 0)

	newPoint := func(metricType, temporality, name string, nanos uint64, value float64, attrs []*commonpb.KeyValue) SeriesPoint {
		return SeriesPoint{
			Timestamp:      timestampToTime(nanos),
			OrganizationID: orgID,
			MetricName:     name,
			MetricType:     metricType,
			Temporality:    temporality,
			Unit:           metric.Unit,
			Value:          value,
			Labels:         attributesToLabels(attrs),
			ServiceName:    serviceName,
			Hostname:       hostname,
		}
	}

	switch data := metric.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range data.Gauge.DataPoints {
			points = append(points, newPoint("gauge", "", metric.Name, dp.TimeUnixNano, getNumberValue(dp), dp.Attributes))
		}
	case *metricspb.Metric_Sum:
		temporality := temporalityName(data.Sum.AggregationTemporality)
		for _, dp := range data.Sum.DataPoints {
			points = append(points, newPoint("sum", temporality, metric.Name, dp.TimeUnixNano, getNumberValue(dp), dp.Attributes))
		}
	case *metricspb.Metric_Histogram:
		// Buckets are dropped; count and sum become their own series, as Prometheus does.
		temporality := temporalityName(data.Histogram.AggregationTemporality)
		for _, dp := range data.Histogram.DataPoints {
			points = append(points,
				newPoint("histogram", temporality, metric.Name+"_count", dp.TimeUnixNano, float64(dp.Count), dp.Attributes))
			if dp.Sum != nil {
				points = append(points,
					newPoint("histogram", temporality, metric.Name+"_sum", dp.TimeUnixNano, *dp.Sum, dp.Attributes))
			}
		}
	}

	return points
}

// temporalityName maps OTLP temporality; unspecified reads as cumulative, the SDK default.
func temporalityName(t metricspb.AggregationTemporality) string {
	if t == metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
		return TemporalityDelta
	}
	return TemporalityCumulative
}

// attributesToLabels renders OTLP attributes as string labels. Keys keep their
// dots: labels_kv is a single field, so no key can collide with a mapping path.
func attributesToLabels(attrs []*commonpb.KeyValue) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	labels := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		labels[attr.Key] = anyValueToString(attr.Value)
	}
	return labels
}

// Helper functions

func extractServiceName(resource *resourcepb.Resource) string {
	for _, attr := range resource.Attributes {
		if attr.Key == "service.name" {
			return attr.Value.GetStringValue()
		}
	}
	return "unknown"
}

// isUnknownService reports whether a service name is missing or one of the OTLP
// SDK default placeholders (e.g. "unknown_service", "unknown_service:rust").
func isUnknownService(name string) bool {
	return name == "" || name == "unknown" || strings.HasPrefix(name, "unknown_service")
}

func extractHostname(resource *resourcepb.Resource) string {
	for _, attr := range resource.Attributes {
		if attr.Key == "host.name" {
			return attr.Value.GetStringValue()
		}
	}
	return ""
}

func attributesToMap(attrs []*commonpb.KeyValue) map[string]interface{} {
	result := make(map[string]interface{})
	for _, attr := range attrs {
		result[attr.Key] = anyValueToInterface(attr.Value)
	}
	return result
}

// sanitizeAttrKeys replaces dots in attribute keys with underscores. OpenSearch
// treats a dotted field name as a nested object path, so a key like
// "error.message" collides with a scalar attribute named "error" (mapper
// exception). Flattening the keys keeps attributes safely indexable.
func sanitizeAttrKeys(m map[string]interface{}) map[string]interface{} {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[strings.ReplaceAll(k, ".", "_")] = v
	}
	return out
}

func anyValueToInterface(value *commonpb.AnyValue) interface{} {
	switch v := value.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_IntValue:
		return v.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return v.DoubleValue
	case *commonpb.AnyValue_BoolValue:
		return v.BoolValue
	case *commonpb.AnyValue_ArrayValue:
		arr := make([]interface{}, len(v.ArrayValue.Values))
		for i, item := range v.ArrayValue.Values {
			arr[i] = anyValueToInterface(item)
		}
		return arr
	case *commonpb.AnyValue_KvlistValue:
		result := make(map[string]interface{})
		for _, kv := range v.KvlistValue.Values {
			result[kv.Key] = anyValueToInterface(kv.Value)
		}
		return result
	default:
		return nil
	}
}

func anyValueToString(value *commonpb.AnyValue) string {
	switch v := value.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", v.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%f", v.DoubleValue)
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprintf("%t", v.BoolValue)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func getNumberValue(dp *metricspb.NumberDataPoint) float64 {
	switch v := dp.Value.(type) {
	case *metricspb.NumberDataPoint_AsInt:
		return float64(v.AsInt)
	case *metricspb.NumberDataPoint_AsDouble:
		return v.AsDouble
	default:
		return 0
	}
}

func timestampToTime(nanos uint64) time.Time {
	return time.Unix(0, int64(nanos))
}

func bytesToHex(bytes []byte) string {
	if len(bytes) == 0 {
		return ""
	}
	return fmt.Sprintf("%x", bytes)
}
