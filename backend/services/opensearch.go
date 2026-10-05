package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenSearchService handles exporting observability data to OpenSearch
type OpenSearchService struct {
	client      *http.Client
	baseURL     string
	username    string
	password    string
	initialized bool
}

// NewOpenSearchService creates a new OpenSearch service
func NewOpenSearchService() *OpenSearchService {
	baseURL := os.Getenv("OPENSEARCH_URL")
	if baseURL == "" {
		baseURL = "http://localhost:9200"
	}

	return &OpenSearchService{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:     baseURL,
		username:    os.Getenv("OPENSEARCH_USERNAME"),
		password:    os.Getenv("OPENSEARCH_PASSWORD"),
		initialized: false,
	}
}

// Initialize creates necessary indices in OpenSearch
func (s *OpenSearchService) Initialize() error {
	indices := []string{
		"middle-monitor-traces",
		"middle-monitor-logs",
		"middle-monitor-worker-results",
		"middle-monitor-errors",
	}

	for _, index := range indices {
		if err := s.createIndex(index); err != nil {
			slog.Warn("failed to create index", "index", index, "error", err)
			// Continue with other indices even if one fails
		}
	}

	s.initialized = true

	if err := s.EnsureSeriesTemplate(); err != nil {
		slog.Warn("failed to create series index template", "error", err)
	}

	slog.Info("opensearch initialized", "url", s.baseURL)
	return nil
}

// createIndex creates an OpenSearch index with proper mappings for observability data
func (s *OpenSearchService) createIndex(indexName string) error {
	// Check if index already exists
	exists, err := s.indexExists(indexName)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIndexExistenceCheck, err)
	}
	if exists {
		slog.Debug("index already exists", "index", indexName)
		return nil
	}

	// Define index mapping based on type
	var mapping map[string]interface{}
	switch indexName {
	case "middle-monitor-traces":
		mapping = s.getTraceMapping()
	case "middle-monitor-logs":
		mapping = s.getLogMapping()
	case "middle-monitor-worker-results":
		mapping = s.getWorkerResultsMapping()
	case "middle-monitor-errors":
		mapping = s.getErrorsMapping()
	default:
		mapping = s.getDefaultMapping()
	}

	// Create index with mapping
	url := fmt.Sprintf("%s/%s", s.baseURL, indexName)
	body, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMappingMarshal, err)
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRequestCreate, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIndexCreate, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return newOpenSearchStatusError("create index", resp)
	}

	slog.Info("created index", "index", indexName)
	return nil
}

// indexExists checks if an index exists
func (s *OpenSearchService) indexExists(indexName string) (bool, error) {
	url := fmt.Sprintf("%s/%s", s.baseURL, indexName)
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return false, err
	}
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, nil
}

// getTraceMapping returns the mapping for trace documents
func (s *OpenSearchService) getTraceMapping() map[string]interface{} {
	return map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"trace_id":          map[string]string{"type": "keyword"},
				"span_id":           map[string]string{"type": "keyword"},
				"parent_span_id":    map[string]string{"type": "keyword"},
				"trace_state":       map[string]string{"type": "keyword"},
				"service_name":      map[string]string{"type": "keyword"},
				"service_namespace": map[string]string{"type": "keyword"},
				"operation_name":    map[string]interface{}{"type": "text", "fields": map[string]interface{}{"keyword": map[string]string{"type": "keyword"}}},
				"span_kind":         map[string]string{"type": "keyword"},
				"status_code":       map[string]string{"type": "keyword"},
				"status_message":    map[string]string{"type": "text"},
				"start_time":        map[string]string{"type": "date"},
				"end_time":          map[string]string{"type": "date"},
				"duration_ms":       map[string]string{"type": "float"},
				"attributes":        map[string]string{"type": "object", "enabled": "true"},
				"events":            map[string]string{"type": "nested"},
				"links":             map[string]string{"type": "nested"},
				"hostname":          map[string]string{"type": "keyword"},
				"environment":       map[string]string{"type": "keyword"},
				"@timestamp":        map[string]string{"type": "date"},
			},
		},
	}
}

// getLogMapping returns the mapping for log documents
func (s *OpenSearchService) getLogMapping() map[string]interface{} {
	return map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"trace_id":          map[string]string{"type": "keyword"},
				"span_id":           map[string]string{"type": "keyword"},
				"service_name":      map[string]string{"type": "keyword"},
				"service_namespace": map[string]string{"type": "keyword"},
				"severity":          map[string]string{"type": "keyword"},
				"severity_text":     map[string]string{"type": "keyword"},
				"body":              map[string]string{"type": "text"},
				"attributes":        map[string]string{"type": "object", "enabled": "true"},
				"hostname":          map[string]string{"type": "keyword"},
				"environment":       map[string]string{"type": "keyword"},
				"resource":          map[string]string{"type": "object", "enabled": "true"},
				"@timestamp":        map[string]string{"type": "date"},
			},
		},
	}
}

// getWorkerResultsMapping returns the mapping for worker result documents
func (s *OpenSearchService) getWorkerResultsMapping() map[string]interface{} {
	return map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"@timestamp":      map[string]string{"type": "date"},
				"service_id":      map[string]string{"type": "long"},
				"service_name":    map[string]string{"type": "keyword"},
				"service_type":    map[string]string{"type": "keyword"},
				"host_id":         map[string]string{"type": "long"},
				"host_name":       map[string]string{"type": "keyword"},
				"organization_id": map[string]string{"type": "long"},
				"service":         map[string]string{"type": "keyword"},
				"environment":     map[string]string{"type": "keyword"},
				"status":          map[string]string{"type": "keyword"},
				"latency":         map[string]string{"type": "float"},
				"message":         map[string]string{"type": "text"},
				// Object, matching what the worker now sends. Declared as text
				// this rejected the decoded object on any FRESH install, while
				// existing indices — dynamically mapped as object — accepted it.
				"metadata":     map[string]string{"type": "object", "enabled": "true"},
				"metric_type":  map[string]string{"type": "keyword"},
				"metric_value": map[string]string{"type": "float"},
			},
		},
	}
}

// getErrorsMapping returns the mapping for application error documents
func (s *OpenSearchService) getErrorsMapping() map[string]interface{} {
	return map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"@timestamp":      map[string]string{"type": "date"},
				"organization_id": map[string]string{"type": "long"},
				"name":            map[string]string{"type": "keyword"},
				"message":         map[string]string{"type": "text"},
				"file":            map[string]string{"type": "keyword"},
				"line":            map[string]string{"type": "integer"},
				"environment":     map[string]string{"type": "keyword"},
				"service":         map[string]string{"type": "keyword"},
				"http_method":     map[string]string{"type": "keyword"},
				"http_url":        map[string]string{"type": "keyword"},
				"http_headers":    map[string]string{"type": "text"},
				"http_body":       map[string]string{"type": "text"},
			},
		},
	}
}

// getDefaultMapping returns a default mapping
func (s *OpenSearchService) getDefaultMapping() map[string]interface{} {
	return map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"@timestamp": map[string]string{"type": "date"},
			},
		},
	}
}

// IndexTrace indexes a trace span document
func (s *OpenSearchService) IndexTrace(ctx context.Context, doc map[string]interface{}) error {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return fmt.Errorf("%w: %w", ErrOpensearchInitialize, err)
		}
	}

	// Ensure @timestamp is set
	if _, exists := doc["@timestamp"]; !exists {
		doc["@timestamp"] = time.Now().UTC().Format(time.RFC3339)
	}

	return s.indexDocument("middle-monitor-traces", doc)
}

// IndexLog indexes a log document
func (s *OpenSearchService) IndexLog(ctx context.Context, doc map[string]interface{}) error {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return fmt.Errorf("%w: %w", ErrOpensearchInitialize, err)
		}
	}

	// Ensure @timestamp is set
	if _, exists := doc["@timestamp"]; !exists {
		doc["@timestamp"] = time.Now().UTC().Format(time.RFC3339)
	}

	return s.indexDocument("middle-monitor-logs", doc)
}

// IndexError indexes an application error document
func (s *OpenSearchService) IndexError(ctx context.Context, doc map[string]interface{}) error {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return fmt.Errorf("%w: %w", ErrOpensearchInitialize, err)
		}
	}

	if _, exists := doc["@timestamp"]; !exists {
		doc["@timestamp"] = time.Now().UTC().Format(time.RFC3339)
	}

	return s.indexDocument("middle-monitor-errors", doc)
}

// IndexWorkerResult indexes a worker result document
func (s *OpenSearchService) IndexWorkerResult(ctx context.Context, doc map[string]interface{}) error {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return fmt.Errorf("%w: %w", ErrOpensearchInitialize, err)
		}
	}

	if _, exists := doc["@timestamp"]; !exists {
		doc["@timestamp"] = time.Now().UTC().Format(time.RFC3339)
	}

	return s.indexDocument("middle-monitor-worker-results", doc)
}

// indexDocument indexes a single document to OpenSearch
func (s *OpenSearchService) indexDocument(indexName string, doc map[string]interface{}) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDocumentMarshal, err)
	}

	url := fmt.Sprintf("%s/%s/_doc", s.baseURL, indexName)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRequestCreate, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDocumentIndex, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return newOpenSearchStatusError("index document", resp)
	}
	// Log which check/service was indexed for worker-results to help debug
	if indexName == "middle-monitor-worker-results" {
		var sid interface{}
		var sname, stype, host, status string
		if v, ok := doc["service_id"]; ok {
			sid = v
		}
		if v, ok := doc["service_name"].(string); ok {
			sname = v
		}
		if v, ok := doc["service_type"].(string); ok {
			stype = v
		}
		if v, ok := doc["host_name"].(string); ok {
			host = v
		}
		if v, ok := doc["status"].(string); ok {
			status = v
		}
		slog.Debug("indexed worker result", "index", indexName, "service_id", sid, "service_name", sname, "type", stype, "host_name", host, "status", status)
	} else {
		slog.Debug("indexed document", "index", indexName, "status_code", resp.StatusCode)
	}
	return nil
}

// SearchWorkerResultsByTimeAndTargets returns worker results in a time window for the given host IDs and service IDs, filtered by status failure or warning.
// If both hostIDs and serviceIDs are empty, returns empty results (no linked resources to correlate).
func (s *OpenSearchService) SearchWorkerResultsByTimeAndTargets(startTime, endTime string, hostIDs []int64, serviceIDs []int64, includeWarnings bool, size int) ([]map[string]interface{}, int64, error) {
	if !s.initialized {
		return []map[string]interface{}{}, 0, nil
	}
	if len(hostIDs) == 0 && len(serviceIDs) == 0 {
		return []map[string]interface{}{}, 0, nil
	}

	mustClauses := []map[string]interface{}{
		{"range": map[string]interface{}{
			"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
		}},
	}

	statusTerms := []string{"failure"}
	if includeWarnings {
		statusTerms = append(statusTerms, "warning")
	}
	mustClauses = append(mustClauses, map[string]interface{}{
		"terms": map[string]interface{}{"status": statusTerms},
	})

	shouldClauses := []map[string]interface{}{}
	if len(hostIDs) > 0 {
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"terms": map[string]interface{}{"host_id": hostIDs},
		})
	}
	if len(serviceIDs) > 0 {
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"terms": map[string]interface{}{"service_id": serviceIDs},
		})
	}
	mustClauses = append(mustClauses, map[string]interface{}{
		"bool": map[string]interface{}{"should": shouldClauses, "minimum_should_match": 1},
	})

	searchBody := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"sort": []map[string]interface{}{{"@timestamp": map[string]string{"order": "desc"}}},
		"from": 0,
		"size": size,
	}

	return s.executeSearch("middle-monitor-worker-results", searchBody)
}

// SearchTraces searches for traces in OpenSearch
func (s *OpenSearchService) SearchTraces(query string, service string, orgID int64, from, size int, startTime, endTime string) ([]map[string]interface{}, int64, error) {
	mustClauses := []map[string]interface{}{}

	if orgID > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{"organization_id": orgID},
		})
	}
	if clause := BuildTraceQueryClause(query); clause != nil {
		mustClauses = append(mustClauses, clause)
	}
	if service != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]string{"service_name": service},
		})
	}
	if startTime != "" && endTime != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
			},
		})
	}
	if len(mustClauses) == 0 {
		mustClauses = append(mustClauses, map[string]interface{}{"match_all": map[string]interface{}{}})
	}

	searchBody := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"sort": []map[string]interface{}{{"@timestamp": map[string]string{"order": "desc"}}},
		"from": from,
		"size": size,
	}

	docs, total, err := s.executeSearch("middle-monitor-traces", searchBody)
	if err != nil {
		return nil, 0, err
	}
	// The OTLP receiver stores status_code as the raw enum ("STATUS_CODE_ERROR").
	// The UI contract expects the short form ("ERROR"/"OK"/"UNSET"), so strip the
	// prefix on read to cover both existing and newly indexed spans.
	for _, doc := range docs {
		if code, ok := doc["status_code"].(string); ok {
			doc["status_code"] = strings.TrimPrefix(code, "STATUS_CODE_")
		}
		// The SDKs name every error-report span "error.report", which is
		// meaningless in the operation column. Surface the actual error
		// (error.type: error.message, stored with dots flattened to underscores)
		// so each row shows what failed.
		if doc["operation_name"] == "error.report" {
			if label := errorReportLabel(doc["attributes"]); label != "" {
				doc["operation_name"] = label
			}
		}
	}
	return docs, total, nil
}

// errorReportLabel builds a human-readable operation label from an error.report
// span's attributes ("error.type: error.message"), falling back to whichever
// part is present. Returns "" when neither is available so the caller keeps the
// original span name.
func errorReportLabel(attrs interface{}) string {
	m, ok := attrs.(map[string]interface{})
	if !ok {
		return ""
	}
	errType := strings.TrimSpace(asString(m["error_type"]))
	errMsg := strings.TrimSpace(asString(m["error_message"]))
	if len(errMsg) > 120 {
		errMsg = errMsg[:120] + "..."
	}
	switch {
	case errType != "" && errMsg != "":
		return errType + ": " + errMsg
	case errType != "":
		return errType
	default:
		return errMsg
	}
}

type TraceServiceStats struct {
	ServiceName string
	Count       int64
	AvgDuration float64
	MaxDuration float64
}

// AggregateTracesByService returns trace volume and latency stats grouped by
// service_name for a host/time window. It is intentionally small and
// best-effort: OpenSearch being down should not break the main product path.
func (s *OpenSearchService) AggregateTracesByService(startTime, endTime, hostName string, serviceNames []string, size int) ([]TraceServiceStats, error) {
	if !s.initialized {
		return []TraceServiceStats{}, nil
	}
	if size <= 0 {
		size = 10
	}
	mustClauses := []map[string]interface{}{
		{"range": map[string]interface{}{
			"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
		}},
	}
	if hostName != "" {
		mustClauses = append(mustClauses, map[string]interface{}{"term": map[string]string{"hostname": hostName}})
	}
	if len(serviceNames) > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{"terms": map[string]interface{}{"service_name": serviceNames}})
	}

	searchBody := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"aggs": map[string]interface{}{
			"services": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "service_name",
					"size":  size,
					"order": map[string]string{"_count": "desc"},
				},
				"aggs": map[string]interface{}{
					"avg_duration": map[string]interface{}{"avg": map[string]string{"field": "duration_ms"}},
					"max_duration": map[string]interface{}{"max": map[string]string{"field": "duration_ms"}},
				},
			},
		},
	}

	body, err := json.Marshal(searchBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTraceAggregationMarshal, err)
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/%s/_search", s.baseURL, "middle-monitor-traces"), bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		slog.Error("trace aggregation failed", "error", err)
		return []TraceServiceStats{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Error("trace aggregation refused", "status_code", resp.StatusCode)
		return []TraceServiceStats{}, nil
	}

	var result struct {
		Aggregations struct {
			Services struct {
				Buckets []struct {
					Key         string `json:"key"`
					DocCount    int64  `json:"doc_count"`
					AvgDuration struct {
						Value *float64 `json:"value"`
					} `json:"avg_duration"`
					MaxDuration struct {
						Value *float64 `json:"value"`
					} `json:"max_duration"`
				} `json:"buckets"`
			} `json:"services"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTraceAggregationDecode, err)
	}

	stats := make([]TraceServiceStats, 0, len(result.Aggregations.Services.Buckets))
	for _, b := range result.Aggregations.Services.Buckets {
		item := TraceServiceStats{ServiceName: b.Key, Count: b.DocCount}
		if b.AvgDuration.Value != nil {
			item.AvgDuration = *b.AvgDuration.Value
		}
		if b.MaxDuration.Value != nil {
			item.MaxDuration = *b.MaxDuration.Value
		}
		stats = append(stats, item)
	}
	return stats, nil
}

// TraceSpanSummary is a compact view of a span used for root-cause analysis.
type TraceSpanSummary struct {
	SpanID       string  `json:"span_id,omitempty"`
	ParentSpanID string  `json:"parent_span_id,omitempty"`
	ServiceName  string  `json:"service_name,omitempty"`
	Operation    string  `json:"operation,omitempty"`
	SpanKind     string  `json:"span_kind,omitempty"`
	StatusCode   string  `json:"status_code,omitempty"`
	StatusMsg    string  `json:"status_message,omitempty"`
	DurationMS   float64 `json:"duration_ms,omitempty"`
	Timestamp    string  `json:"timestamp,omitempty"`
}

// TraceSummary aggregates the spans of a single distributed trace, surfacing
// the failing span (if any) so the LLM can pinpoint WHERE in the call chain the
// request broke.
type TraceSummary struct {
	TraceID    string             `json:"trace_id"`
	SpanCount  int                `json:"span_count"`
	Services   []string           `json:"services,omitempty"`
	ErrorSpans []TraceSpanSummary `json:"error_spans,omitempty"`
	Spans      []TraceSpanSummary `json:"spans,omitempty"`
}

// traceByIDQuery builds the span lookup for one trace, scoped to one
// organization. A trace id is 16 random bytes, but its owner is not the only
// party who can learn one — ids travel in W3C traceparent headers through
// third-party services — so possession must not grant read access.
func traceByIDQuery(orgID int64, traceID string, maxSpans int) map[string]interface{} {
	return map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": []map[string]interface{}{
					{"term": map[string]interface{}{"trace_id": traceID}},
					{"term": map[string]interface{}{"organization_id": orgID}},
				},
			},
		},
		"sort": []map[string]interface{}{{"start_time": map[string]string{"order": "asc"}}},
		"from": 0,
		"size": maxSpans,
	}
}

// GetTraceByID fetches the spans of a distributed trace ordered chronologically
// and summarizes them, highlighting error spans. Best-effort: returns nil when
// OpenSearch is unavailable or the trace has no spans.
func (s *OpenSearchService) GetTraceByID(orgID int64, traceID string, maxSpans int) (*TraceSummary, error) {
	if !s.initialized || strings.TrimSpace(traceID) == "" {
		return nil, nil
	}
	if maxSpans <= 0 || maxSpans > 200 {
		maxSpans = 100
	}
	hits, _, err := s.executeSearch("middle-monitor-traces", traceByIDQuery(orgID, traceID, maxSpans))
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}

	summary := &TraceSummary{TraceID: traceID}
	seenSvc := map[string]bool{}
	for _, h := range hits {
		span := TraceSpanSummary{
			SpanID:       asString(h["span_id"]),
			ParentSpanID: asString(h["parent_span_id"]),
			ServiceName:  asString(h["service_name"]),
			Operation:    asString(h["operation_name"]),
			SpanKind:     asString(h["span_kind"]),
			StatusCode:   asString(h["status_code"]),
			StatusMsg:    asString(h["status_message"]),
			DurationMS:   asFloat(h["duration_ms"]),
			Timestamp:    asString(h["@timestamp"]),
		}
		if span.ServiceName != "" && !seenSvc[span.ServiceName] {
			seenSvc[span.ServiceName] = true
			summary.Services = append(summary.Services, span.ServiceName)
		}
		if isErrorStatus(span.StatusCode) {
			summary.ErrorSpans = append(summary.ErrorSpans, span)
		}
		summary.Spans = append(summary.Spans, span)
	}
	summary.SpanCount = len(summary.Spans)
	return summary, nil
}

func isErrorStatus(code string) bool {
	c := strings.ToUpper(strings.TrimSpace(code))
	return c == "ERROR" || c == "STATUS_CODE_ERROR" || c == "2"
}

func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

// SearchTraceSpansForService returns recent trace spans for the given service_name (for link suggestions).
func (s *OpenSearchService) SearchTraceSpansForService(serviceName, startTime, endTime string, size int) ([]map[string]interface{}, int64, error) {
	if !s.initialized {
		return []map[string]interface{}{}, 0, nil
	}
	mustClauses := []map[string]interface{}{
		{"term": map[string]string{"service_name": serviceName}},
	}
	if startTime != "" && endTime != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
			},
		})
	}
	searchBody := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"sort": []map[string]interface{}{{"@timestamp": map[string]string{"order": "desc"}}},
		"from": 0,
		"size": size,
	}
	return s.executeSearch("middle-monitor-traces", searchBody)
}

// SearchLogs searches for logs in OpenSearch.
// query = free text for body/search; service, severity = optional filters.
// extraFilters = additional field:value filters (e.g. from parsed query string).
func (s *OpenSearchService) SearchLogs(query string, service string, severity string, extraFilters map[string]string, orgID int64, from, size int, startTime, endTime string) ([]map[string]interface{}, int64, error) {
	mustClauses := []map[string]interface{}{}

	if orgID > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{"organization_id": orgID},
		})
	}

	if query != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  query,
				"fields": []string{"body", "severity_text", "service_name", "attributes.*"},
			},
		})
	}
	if service != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]string{"service_name": service},
		})
	}
	if severity != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]string{"severity_text": severity},
		})
	}
	for field, value := range extraFilters {
		if field != "" && value != "" {
			if strings.HasPrefix(field, "attributes.") {
				// OpenSearch maps object string values to text+keyword; try .keyword for exact match, fallback to field
				mustClauses = append(mustClauses, map[string]interface{}{
					"bool": map[string]interface{}{
						"should": []map[string]interface{}{
							{"term": map[string]interface{}{field + ".keyword": value}},
							{"term": map[string]interface{}{field: value}},
						},
						"minimum_should_match": 1,
					},
				})
			} else {
				mustClauses = append(mustClauses, map[string]interface{}{
					"term": map[string]interface{}{field: value},
				})
			}
		}
	}
	if startTime != "" && endTime != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
			},
		})
	}

	if len(mustClauses) == 0 {
		mustClauses = append(mustClauses, map[string]interface{}{"match_all": map[string]interface{}{}})
	}

	searchBody := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"sort": []map[string]interface{}{{"@timestamp": map[string]string{"order": "desc"}}},
		"from": from,
		"size": size,
	}

	return s.executeSearch("middle-monitor-logs", searchBody)
}

// SearchLogsDatadog searches logs with a Datadog-style query (see logquery.go:
// implicit AND, AND/&&, OR/||, NOT/-, parentheses, quoted phrases, field:value,
// wildcards). service and severity are the explicit dropdown filters, ANDed on
// top of the query.
func (s *OpenSearchService) SearchLogsDatadog(rawQuery, service, severity string, orgID int64, from, size int, startTime, endTime string) ([]map[string]interface{}, int64, error) {
	mustClauses := []map[string]interface{}{}

	if orgID > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{"organization_id": orgID},
		})
	}
	if clause := BuildLogQueryClause(rawQuery); clause != nil {
		mustClauses = append(mustClauses, clause)
	}
	if service != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]string{"service_name": service},
		})
	}
	if severity != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]string{"severity_text": severity},
		})
	}
	if startTime != "" && endTime != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
			},
		})
	}
	if len(mustClauses) == 0 {
		mustClauses = append(mustClauses, map[string]interface{}{"match_all": map[string]interface{}{}})
	}

	searchBody := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"sort": []map[string]interface{}{{"@timestamp": map[string]string{"order": "desc"}}},
		"from": from,
		"size": size,
	}

	return s.executeSearch("middle-monitor-logs", searchBody)
}

type LogServiceStats struct {
	ServiceName string
	Count       int64
	ErrorCount  int64
}

// AggregateLogsByService returns log volume grouped by service_name for a
// host/time window. ErrorCount is based on common severity labels.
func (s *OpenSearchService) AggregateLogsByService(startTime, endTime, hostName string, serviceNames []string, size int) ([]LogServiceStats, error) {
	if !s.initialized {
		return []LogServiceStats{}, nil
	}
	if size <= 0 {
		size = 10
	}
	mustClauses := []map[string]interface{}{
		{"range": map[string]interface{}{
			"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
		}},
	}
	if hostName != "" {
		mustClauses = append(mustClauses, map[string]interface{}{"term": map[string]string{"hostname": hostName}})
	}
	if len(serviceNames) > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{"terms": map[string]interface{}{"service_name": serviceNames}})
	}

	errorSeverities := []string{"ERROR", "ERR", "FATAL", "CRITICAL", "PANIC"}
	searchBody := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"aggs": map[string]interface{}{
			"services": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "service_name",
					"size":  size,
					"order": map[string]string{"_count": "desc"},
				},
				"aggs": map[string]interface{}{
					"errors": map[string]interface{}{
						"filter": map[string]interface{}{
							"bool": map[string]interface{}{
								"should": []map[string]interface{}{
									{"terms": map[string]interface{}{"severity_text": errorSeverities}},
									{"terms": map[string]interface{}{"severity": errorSeverities}},
								},
								"minimum_should_match": 1,
							},
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(searchBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLogAggregationMarshal, err)
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/%s/_search", s.baseURL, "middle-monitor-logs"), bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		slog.Error("log aggregation failed", "error", err)
		return []LogServiceStats{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Error("log aggregation refused", "status_code", resp.StatusCode)
		return []LogServiceStats{}, nil
	}

	var result struct {
		Aggregations struct {
			Services struct {
				Buckets []struct {
					Key      string `json:"key"`
					DocCount int64  `json:"doc_count"`
					Errors   struct {
						DocCount int64 `json:"doc_count"`
					} `json:"errors"`
				} `json:"buckets"`
			} `json:"services"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLogAggregationDecode, err)
	}

	stats := make([]LogServiceStats, 0, len(result.Aggregations.Services.Buckets))
	for _, b := range result.Aggregations.Services.Buckets {
		stats = append(stats, LogServiceStats{
			ServiceName: b.Key,
			Count:       b.DocCount,
			ErrorCount:  b.Errors.DocCount,
		})
	}
	return stats, nil
}

// escapeRegexPrefix escapes regex metacharacters so a user prefix matches
// literally inside a terms "include" pattern.
func escapeRegexPrefix(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '*', '?', '+', '[', ']', '(', ')', '{', '}', '|', '\\', '^', '$':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// logFieldValuesQuery builds the facet aggregation for one organization. Field
// values are service and host names from the customer's own infrastructure, so
// the org term is not optional — without it the facet lists every tenant's.
func logFieldValuesQuery(field, prefix string, orgID int64, startTime, endTime string, size int) map[string]interface{} {
	mustClauses := []map[string]interface{}{
		{"term": map[string]interface{}{"organization_id": orgID}},
	}
	if startTime != "" && endTime != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": map[string]string{"gte": startTime, "lte": endTime},
			},
		})
	}

	termsAgg := map[string]interface{}{
		"field": field,
		"size":  size,
	}
	if prefix != "" {
		termsAgg["include"] = escapeRegexPrefix(prefix) + ".*"
	}

	return map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": mustClauses},
		},
		"aggs": map[string]interface{}{
			"values": map[string]interface{}{
				"terms": termsAgg,
			},
		},
	}
}

// GetLogFieldValues returns distinct values for a field (for autocomplete).
func (s *OpenSearchService) GetLogFieldValues(field, prefix string, orgID int64, startTime, endTime string, size int) ([]string, error) {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return nil, err
		}
	}

	body, err := json.Marshal(logFieldValuesQuery(field, prefix, orgID, startTime, endTime, size))
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/middle-monitor-logs/_search", s.baseURL)
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, newOpenSearchStatusError("search", resp)
	}

	var result struct {
		Aggregations struct {
			Values struct {
				Buckets []struct {
					Key string `json:"key"`
				} `json:"buckets"`
			} `json:"values"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	values := make([]string, 0, len(result.Aggregations.Values.Buckets))
	for _, b := range result.Aggregations.Values.Buckets {
		values = append(values, b.Key)
	}
	return values, nil
}

// executeSearch runs a search query against OpenSearch
func (s *OpenSearchService) executeSearch(indexName string, searchBody map[string]interface{}) ([]map[string]interface{}, int64, error) {
	if !s.initialized {
		// Return empty results if not initialized (OpenSearch may not be running)
		return []map[string]interface{}{}, 0, nil
	}

	body, err := json.Marshal(searchBody)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrSearchBodyMarshal, err)
	}

	url := fmt.Sprintf("%s/%s/_search", s.baseURL, indexName)
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrSearchRequestCreate, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		// OpenSearch might not be running
		slog.Error("search failed", "error", err)
		return []map[string]interface{}{}, 0, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Error("search refused", "status_code", resp.StatusCode)
		return []map[string]interface{}{}, 0, nil
	}

	var result struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source map[string]interface{} `json:"_source"`
				ID     string                 `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrSearchResponseDecode, err)
	}

	docs := make([]map[string]interface{}, len(result.Hits.Hits))
	for i, hit := range result.Hits.Hits {
		doc := hit.Source
		doc["_id"] = hit.ID
		docs[i] = doc
	}

	return docs, result.Hits.Total.Value, nil
}

// retentionQuery matches every document past its own organization's cutoff.
// Documents that carry no organization_id (indexed before multi-tenancy) can't be
// attributed, so they only expire at the longest retention in use.
func retentionQuery(retentions []OrgRetention) map[string]interface{} {
	oldest := retentions[0].Cutoff
	shoulds := make([]map[string]interface{}, 0, len(retentions)+1)
	for _, r := range retentions {
		if r.Cutoff.Before(oldest) {
			oldest = r.Cutoff
		}
		shoulds = append(shoulds, map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": []map[string]interface{}{
					{"term": map[string]interface{}{"organization_id": r.OrgID}},
					{"range": map[string]interface{}{"@timestamp": map[string]string{"lt": r.Cutoff.UTC().Format(time.RFC3339)}}},
				},
			},
		})
	}
	shoulds = append(shoulds, map[string]interface{}{
		"bool": map[string]interface{}{
			"must_not": []map[string]interface{}{
				{"exists": map[string]interface{}{"field": "organization_id"}},
			},
			"filter": []map[string]interface{}{
				{"range": map[string]interface{}{"@timestamp": map[string]string{"lt": oldest.UTC().Format(time.RFC3339)}}},
			},
		},
	})
	return map[string]interface{}{
		"bool": map[string]interface{}{"should": shoulds, "minimum_should_match": 1},
	}
}

// DeleteByRetention deletes, from all middle-monitor indices, each organization's
// documents older than its own plan retention cutoff. Returns total deleted count.
func (s *OpenSearchService) DeleteByRetention(retentions []OrgRetention) (int64, error) {
	if !s.initialized || len(retentions) == 0 {
		return 0, nil
	}

	// Daily series indices wholly past the longest retention are dropped whole;
	// delete_by_query below only grinds through the remaining young indices.
	oldest := retentions[0].Cutoff
	for _, r := range retentions {
		if r.Cutoff.Before(oldest) {
			oldest = r.Cutoff
		}
	}
	if _, err := s.DeleteExpiredSeriesIndices(oldest); err != nil {
		// Best-effort, like the per-index loop below: the next run retries.
		slog.Error("series index retention failed", "error", err)
	}

	query := retentionQuery(retentions)
	indices := []string{
		"middle-monitor-traces",
		"middle-monitor-logs",
		"middle-monitor-worker-results",
		"middle-monitor-errors",
	}
	// The series store is the large one: purged in the background, throttled.
	s.startSeriesRetention(query)

	var totalDeleted int64
	for _, indexName := range indices {
		body := map[string]interface{}{"query": query}
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return totalDeleted, fmt.Errorf("%w: %w", ErrDeleteQueryMarshal, err)
		}
		url := fmt.Sprintf("%s/%s/_delete_by_query", s.baseURL, indexName)
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
		if err != nil {
			return totalDeleted, fmt.Errorf("%w: %w", ErrDeleteRequestCreate, err)
		}
		req.Header.Set("Content-Type", "application/json")
		if s.username != "" && s.password != "" {
			req.SetBasicAuth(s.username, s.password)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			slog.Error("delete_by_query failed", "index", indexName, "error", err)
			continue
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			slog.Error("delete_by_query refused", "index", indexName, "status_code", resp.StatusCode)
			continue
		}
		var result struct {
			Deleted int64 `json:"deleted"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			slog.Error("failed to decode delete response", "index", indexName, "error", err)
			continue
		}
		resp.Body.Close()
		if result.Deleted > 0 {
			slog.Info("deleted expired documents", "count", result.Deleted, "index", indexName)
		}
		totalDeleted += result.Deleted
	}
	return totalDeleted, nil
}

// BulkIndex performs bulk indexing for better performance
func (s *OpenSearchService) BulkIndex(ctx context.Context, indexName string, docs []map[string]interface{}) error {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return fmt.Errorf("%w: %w", ErrOpensearchInitialize, err)
		}
	}

	if len(docs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, doc := range docs {
		// Action line
		action := map[string]interface{}{
			"index": map[string]string{
				"_index": indexName,
			},
		}
		actionJSON, _ := json.Marshal(action)
		buf.Write(actionJSON)
		buf.WriteString("\n")

		// Document line
		docJSON, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrDocumentMarshal, err)
		}
		buf.Write(docJSON)
		buf.WriteString("\n")
	}

	url := fmt.Sprintf("%s/_bulk", s.baseURL)
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBulkRequestCreate, err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIndexBulk, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return newOpenSearchStatusError("bulk index", resp)
	}

	return nil
}
