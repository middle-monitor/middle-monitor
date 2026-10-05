package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Bucket budget for a range query. A wide range with a small step would
// otherwise ask OpenSearch for hundreds of thousands of buckets.
const (
	maxRangeBuckets   = 1000
	defaultRangeSteps = 120
	maxGroupBySeries  = 50
)

// SeriesFilter is one label equality constraint.
type SeriesFilter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SeriesQuery describes a range query over one metric.
type SeriesQuery struct {
	OrganizationID int64
	MetricName     string
	Filters        []SeriesFilter
	GroupBy        string // label key; empty aggregates everything into one series
	Aggregation    string // avg|min|max|sum|count|p50|p75|p90|p95|p99
	Start          time.Time
	End            time.Time
	Step           time.Duration
	// Hostnames scopes the query to the hosts of a host or host group. Resolved
	// from Postgres at query time rather than stamped at ingestion, so moving a
	// host between groups does not make the data already written wrong.
	Hostnames []string
}

// hostFilter narrows a query to a set of hosts.
func hostFilter(hostnames []string) map[string]interface{} {
	values := make([]interface{}, 0, len(hostnames))
	for _, name := range hostnames {
		values = append(values, name)
	}
	return map[string]interface{}{"terms": map[string]interface{}{"hostname": values}}
}

// seriesQueryFilters scopes a query to its org, window, metric, labels and hosts.
// The window start is a parameter because rate reads one bucket before it.
func seriesQueryFilters(q SeriesQuery, start time.Time) []interface{} {
	filters := []interface{}{
		orgFilter(q.OrganizationID),
		timeRangeFilter(start, q.End),
		map[string]interface{}{"term": map[string]interface{}{"metric_name": q.MetricName}},
	}
	for _, f := range q.Filters {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{"labels_kv": f.Key + "=" + f.Value},
		})
	}
	if len(q.Hostnames) > 0 {
		filters = append(filters, hostFilter(q.Hostnames))
	}
	return filters
}

// SeriesDataPoint is one aggregated bucket.
type SeriesDataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     *float64  `json:"value"`
}

// SeriesResult is one line on a chart.
type SeriesResult struct {
	MetricName string            `json:"metric_name"`
	Labels     map[string]string `json:"labels"`
	Points     []SeriesDataPoint `json:"points"`
	// Query is the expression ref ("A") that produced the series, when one did.
	Query string `json:"query,omitempty"`
}

var validAggregations = map[string]bool{
	"avg": true, "min": true, "max": true, "sum": true, "count": true,
	"p50": true, "p75": true, "p90": true, "p95": true, "p99": true,
}

var percentileOf = map[string]float64{
	"p50": 50, "p75": 75, "p90": 90, "p95": 95, "p99": 99,
}

// orgFilter scopes every query to one organization. Metrics carry label values
// from the customer's own infrastructure, so a missing scope leaks across tenants.
func orgFilter(orgID int64) map[string]interface{} {
	return map[string]interface{}{"term": map[string]interface{}{"organization_id": orgID}}
}

func timeRangeFilter(start, end time.Time) map[string]interface{} {
	return map[string]interface{}{
		"range": map[string]interface{}{
			"@timestamp": map[string]string{
				"gte": start.UTC().Format(time.RFC3339),
				"lte": end.UTC().Format(time.RFC3339),
			},
		},
	}
}

// percentileValue reads one percentile out of a percentiles response. The keys
// are the percents themselves, but their formatting is the server's choice —
// OpenSearch answers "50.0" where the percent was requested as 50 — so they are
// compared numerically instead of being rebuilt as a string.
func percentileValue(values map[string]*float64, pct float64) *float64 {
	for key, value := range values {
		if parsed, err := strconv.ParseFloat(key, 64); err == nil && parsed == pct {
			return value
		}
	}
	return nil
}

// valueAggregation renders the requested statistic over the value field.
func valueAggregation(aggregation string) map[string]interface{} {
	if pct, ok := percentileOf[aggregation]; ok {
		return map[string]interface{}{
			"percentiles": map[string]interface{}{"field": "value", "percents": []float64{pct}},
		}
	}
	if aggregation == "count" {
		return map[string]interface{}{"value_count": map[string]interface{}{"field": "value"}}
	}
	return map[string]interface{}{aggregation: map[string]interface{}{"field": "value"}}
}

// resolveStep keeps the bucket count sane for the requested range.
func resolveStep(start, end time.Time, step time.Duration) time.Duration {
	span := end.Sub(start)
	if span <= 0 {
		return time.Minute
	}
	if step <= 0 {
		step = span / defaultRangeSteps
	}
	if minStep := span / maxRangeBuckets; step < minStep {
		step = minStep
	}
	if step < time.Second {
		step = time.Second
	}
	return step
}

// ListMetricNames returns the metric names present in the range.
func (s *OpenSearchService) ListMetricNames(ctx context.Context, orgID int64, start, end time.Time, prefix string, size int, hostnames []string) ([]string, error) {
	terms := map[string]interface{}{"field": "metric_name", "size": size}
	if prefix != "" {
		terms["include"] = escapeRegexPrefix(prefix) + ".*"
	}

	filters := []interface{}{orgFilter(orgID), timeRangeFilter(start, end)}
	if len(hostnames) > 0 {
		filters = append(filters, hostFilter(hostnames))
	}

	body := map[string]interface{}{
		"size":  0,
		"query": map[string]interface{}{"bool": map[string]interface{}{"filter": filters}},
		"aggs":  map[string]interface{}{"names": map[string]interface{}{"terms": terms}},
	}

	aggs, err := s.searchSeriesAggs(ctx, body)
	if err != nil {
		return nil, err
	}
	return bucketKeys(aggs, "names"), nil
}

// ListLabelKeys returns the label keys carried by one metric.
func (s *OpenSearchService) ListLabelKeys(ctx context.Context, orgID int64, metricName string, start, end time.Time, size int, hostnames []string) ([]string, error) {
	filters := []interface{}{orgFilter(orgID), timeRangeFilter(start, end)}
	if len(hostnames) > 0 {
		filters = append(filters, hostFilter(hostnames))
	}
	if metricName != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"metric_name": metricName}})
	}

	body := map[string]interface{}{
		"size":  0,
		"query": map[string]interface{}{"bool": map[string]interface{}{"filter": filters}},
		"aggs": map[string]interface{}{
			"keys": map[string]interface{}{"terms": map[string]interface{}{"field": "label_keys", "size": size}},
		},
	}

	aggs, err := s.searchSeriesAggs(ctx, body)
	if err != nil {
		return nil, err
	}
	return bucketKeys(aggs, "keys"), nil
}

// ListLabelValues returns the values one label key takes on a metric.
func (s *OpenSearchService) ListLabelValues(ctx context.Context, orgID int64, metricName, labelKey, prefix string, start, end time.Time, size int, hostnames []string) ([]string, error) {
	filters := []interface{}{orgFilter(orgID), timeRangeFilter(start, end)}
	if len(hostnames) > 0 {
		filters = append(filters, hostFilter(hostnames))
	}
	if metricName != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"metric_name": metricName}})
	}

	body := map[string]interface{}{
		"size":  0,
		"query": map[string]interface{}{"bool": map[string]interface{}{"filter": filters}},
		"aggs": map[string]interface{}{
			"values": map[string]interface{}{
				"terms": map[string]interface{}{
					"field":   "labels_kv",
					"size":    size,
					"include": escapeRegexPrefix(labelKey+"=") + escapeRegexPrefix(prefix) + ".*",
				},
			},
		},
	}

	aggs, err := s.searchSeriesAggs(ctx, body)
	if err != nil {
		return nil, err
	}

	values := make([]string, 0, size)
	for _, kv := range bucketKeys(aggs, "values") {
		if _, value, err := DecodeLabel(kv); err == nil {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values, nil
}

// QuerySeries runs a range query and returns one result per group-by value.
func (s *OpenSearchService) QuerySeries(ctx context.Context, q SeriesQuery) ([]SeriesResult, error) {
	if q.MetricName == "" {
		return nil, ErrSeriesMetricRequired
	}
	if !validAggregations[q.Aggregation] {
		return nil, ErrSeriesAggregation
	}
	if !q.End.After(q.Start) {
		return nil, ErrSeriesRange
	}

	filters := seriesQueryFilters(q, q.Start)

	step := resolveStep(q.Start, q.End, q.Step)
	histogram := map[string]interface{}{
		"date_histogram": map[string]interface{}{
			"field":          "@timestamp",
			"fixed_interval": fmt.Sprintf("%ds", int(step.Seconds())),
			"min_doc_count":  0,
			"extended_bounds": map[string]interface{}{
				"min": q.Start.UTC().Format(time.RFC3339),
				"max": q.End.UTC().Format(time.RFC3339),
			},
		},
		"aggs": map[string]interface{}{"stat": valueAggregation(q.Aggregation)},
	}

	aggs := map[string]interface{}{"timeline": histogram}
	if q.GroupBy != "" {
		aggs = map[string]interface{}{
			"groups": map[string]interface{}{
				"terms": map[string]interface{}{
					"field":   "labels_kv",
					"size":    maxGroupBySeries,
					"include": escapeRegexPrefix(q.GroupBy+"=") + ".*",
				},
				"aggs": map[string]interface{}{"timeline": histogram},
			},
		}
	}

	body := map[string]interface{}{
		"size":  0,
		"query": map[string]interface{}{"bool": map[string]interface{}{"filter": filters}},
		"aggs":  aggs,
	}

	raw, err := s.searchSeriesAggs(ctx, body)
	if err != nil {
		return nil, err
	}
	return parseSeriesResults(raw, q), nil
}

// AggregateSeries reduces a metric to a single value over the window. Alerting
// needs one number to compare to a threshold, not a curve, so this skips the
// date histogram entirely. Returns nil when the window holds no datapoint.
func (s *OpenSearchService) AggregateSeries(ctx context.Context, q SeriesQuery) (*float64, error) {
	if q.MetricName == "" {
		return nil, ErrSeriesMetricRequired
	}
	if !validAggregations[q.Aggregation] {
		return nil, ErrSeriesAggregation
	}
	if !q.End.After(q.Start) {
		return nil, ErrSeriesRange
	}

	filters := seriesQueryFilters(q, q.Start)

	body := map[string]interface{}{
		"size":  0,
		"query": map[string]interface{}{"bool": map[string]interface{}{"filter": filters}},
		"aggs":  map[string]interface{}{"stat": valueAggregation(q.Aggregation)},
	}

	aggs, err := s.searchSeriesAggs(ctx, body)
	if err != nil {
		return nil, err
	}

	raw, ok := aggs["stat"]
	if !ok {
		return nil, nil
	}
	var stat struct {
		Value  *float64            `json:"value"`
		Values map[string]*float64 `json:"values"`
	}
	if err := json.Unmarshal(raw, &stat); err != nil {
		return nil, fmt.Errorf("aggregate series: %w", ErrSeriesDecode)
	}
	if pct, isPercentile := percentileOf[q.Aggregation]; isPercentile {
		return percentileValue(stat.Values, pct), nil
	}
	return stat.Value, nil
}

func parseSeriesResults(aggs map[string]json.RawMessage, q SeriesQuery) []SeriesResult {
	if q.GroupBy == "" {
		return []SeriesResult{{
			MetricName: q.MetricName,
			Labels:     map[string]string{},
			Points:     parseTimeline(aggs, q.Aggregation),
		}}
	}

	var groups struct {
		Buckets []struct {
			Key      string          `json:"key"`
			Timeline json.RawMessage `json:"timeline"`
		} `json:"buckets"`
	}
	if raw, ok := aggs["groups"]; ok {
		_ = json.Unmarshal(raw, &groups)
	}

	results := make([]SeriesResult, 0, len(groups.Buckets))
	for _, bucket := range groups.Buckets {
		key, value, err := DecodeLabel(bucket.Key)
		if err != nil {
			continue
		}
		results = append(results, SeriesResult{
			MetricName: q.MetricName,
			Labels:     map[string]string{key: value},
			Points:     parseTimeline(map[string]json.RawMessage{"timeline": bucket.Timeline}, q.Aggregation),
		})
	}
	return results
}

func parseTimeline(aggs map[string]json.RawMessage, aggregation string) []SeriesDataPoint {
	raw, ok := aggs["timeline"]
	if !ok {
		return []SeriesDataPoint{}
	}

	var timeline struct {
		Buckets []struct {
			KeyAsString string `json:"key_as_string"`
			KeyMillis   int64  `json:"key"`
			Stat        struct {
				Value  *float64            `json:"value"`
				Values map[string]*float64 `json:"values"`
			} `json:"stat"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &timeline); err != nil {
		return []SeriesDataPoint{}
	}

	points := make([]SeriesDataPoint, 0, len(timeline.Buckets))
	for _, bucket := range timeline.Buckets {
		point := SeriesDataPoint{Timestamp: time.UnixMilli(bucket.KeyMillis).UTC()}
		if pct, isPercentile := percentileOf[aggregation]; isPercentile {
			point.Value = percentileValue(bucket.Stat.Values, pct)
		} else {
			point.Value = bucket.Stat.Value
		}
		points = append(points, point)
	}
	return points
}

// bucketKeys pulls the string keys out of a terms aggregation.
func bucketKeys(aggs map[string]json.RawMessage, name string) []string {
	raw, ok := aggs[name]
	if !ok {
		return []string{}
	}
	var agg struct {
		Buckets []struct {
			Key string `json:"key"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &agg); err != nil {
		return []string{}
	}
	keys := make([]string, 0, len(agg.Buckets))
	for _, bucket := range agg.Buckets {
		keys = append(keys, bucket.Key)
	}
	return keys
}

// searchSeriesAggs runs a search over every series index and returns the raw
// aggregations. Unlike executeSearch it surfaces failures: an empty chart and a
// broken query must not look the same to the caller.
func (s *OpenSearchService) searchSeriesAggs(ctx context.Context, searchBody map[string]interface{}) (map[string]json.RawMessage, error) {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return nil, err
		}
	}

	body, err := json.Marshal(searchBody)
	if err != nil {
		return nil, fmt.Errorf("series search: %w", ErrSeriesMarshal)
	}

	url := fmt.Sprintf("%s/%s/_search?ignore_unavailable=true&allow_no_indices=true", s.baseURL, SeriesIndexPattern)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("series search: %w", ErrSeriesRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("series search: %w", ErrSeriesSearch)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("series search: %w", ErrSeriesSearch)
	}

	var result struct {
		Aggregations map[string]json.RawMessage `json:"aggregations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("series search: %w", ErrSeriesDecode)
	}
	if result.Aggregations == nil {
		return map[string]json.RawMessage{}, nil
	}
	return result.Aggregations, nil
}

// ParseFilters reads the repeated "key:value" filter form used by the API.
func ParseFilters(raw []string) ([]SeriesFilter, error) {
	filters := make([]SeriesFilter, 0, len(raw))
	for _, entry := range raw {
		key, value, found := strings.Cut(entry, ":")
		if !found || key == "" {
			return nil, ErrSeriesFilterMalformed
		}
		filters = append(filters, SeriesFilter{Key: key, Value: value})
	}
	return filters, nil
}
