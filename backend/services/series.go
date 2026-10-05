package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Metric series storage. Labels are stored as a "key=value" keyword array rather
// than an object: a dynamic object maps every new label key as its own field,
// which blows past the 1000-field index limit on real Prometheus data.
const (
	SeriesIndexPrefix  = "middle-monitor-series"
	SeriesIndexPattern = "middle-monitor-series-*"
	seriesTemplateName = "middle-monitor-series"
)

// SeriesPoint is one datapoint of one series.
type SeriesPoint struct {
	Timestamp      time.Time
	OrganizationID int64
	MetricName     string
	MetricType     string // gauge, sum, histogram
	Temporality    string // cumulative or delta; empty for gauges
	Unit           string
	Value          float64
	Labels         map[string]string
	ServiceName    string
	Hostname       string
}

// seriesIndexFor returns the daily index a point belongs to. Daily indices let
// retention drop whole indices instead of running delete_by_query over one
// ever-growing index.
func seriesIndexFor(t time.Time) string {
	return fmt.Sprintf("%s-%s", SeriesIndexPrefix, t.UTC().Format("2006.01.02"))
}

// encodeLabels renders labels as sorted "key=value" pairs. Prometheus label keys
// cannot contain "=", so splitting on the first one always recovers the pair.
func encodeLabels(labels map[string]string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for k, v := range labels {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// labelKeys returns the label keys alone, sorted.
func labelKeys(labels map[string]string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for k := range labels {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DecodeLabel splits a stored "key=value" pair.
func DecodeLabel(kv string) (string, string, error) {
	key, value, found := strings.Cut(kv, "=")
	if !found || key == "" {
		return "", "", ErrLabelMalformed
	}
	return key, value, nil
}

// seriesHash identifies a series across datapoints: metric name plus its label set.
func seriesHash(metricName string, encodedLabels []string) string {
	h := fnv.New64a()
	h.Write([]byte(metricName))
	for _, kv := range encodedLabels {
		h.Write([]byte{0})
		h.Write([]byte(kv))
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

func getSeriesMapping() map[string]interface{} {
	return map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]interface{}{
			// Labels are explicit; nothing else may create fields on its own.
			"dynamic": "strict",
			"properties": map[string]interface{}{
				"@timestamp":      map[string]string{"type": "date"},
				"organization_id": map[string]string{"type": "long"},
				"metric_name":     map[string]string{"type": "keyword"},
				"metric_type":     map[string]string{"type": "keyword"},
				// rate reads deltas as increments and cumulatives as running totals.
				"temporality": map[string]string{"type": "keyword"},
				"unit":        map[string]string{"type": "keyword"},
				// double, not float: a float32 counter loses precision past ~7 digits.
				"value":     map[string]string{"type": "double"},
				"labels_kv": map[string]string{"type": "keyword"},
				// Keys alone, so listing them is an aggregation over a handful of
				// terms instead of over every label value.
				"label_keys":  map[string]string{"type": "keyword"},
				"series_hash": map[string]string{"type": "keyword"},
				// Duplicated out of labels_kv: these two join metrics to hosts and
				// services across the rest of the product.
				"service_name": map[string]string{"type": "keyword"},
				"hostname":     map[string]string{"type": "keyword"},
			},
		},
	}
}

// EnsureSeriesTemplate installs the index template that gives every daily series
// index its mapping.
func (s *OpenSearchService) EnsureSeriesTemplate() error {
	template := map[string]interface{}{
		"index_patterns": []string{SeriesIndexPattern},
		"template":       getSeriesMapping(),
	}

	body, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("series template: %w", ErrSeriesMarshal)
	}

	url := fmt.Sprintf("%s/_index_template/%s", s.baseURL, seriesTemplateName)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("series template: %w", ErrSeriesRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("series template: %w", ErrSeriesTemplate)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("series template: %w", ErrSeriesTemplate)
	}

	slog.Info("series index template ready", "name", seriesTemplateName)

	// A template only applies to indices it creates. Today's index already
	// exists, and the mapping is strict, so a newly added field would be
	// rejected until tomorrow's index without this.
	return s.applySeriesMappingToExistingIndices()
}

// applySeriesMappingToExistingIndices pushes the current properties onto the
// indices already created. OpenSearch accepts added fields and refuses changes
// to existing ones.
func (s *OpenSearchService) applySeriesMappingToExistingIndices() error {
	mappings, ok := getSeriesMapping()["mappings"].(map[string]interface{})
	if !ok {
		return ErrSeriesTemplate
	}
	properties := map[string]interface{}{"properties": mappings["properties"]}

	body, err := json.Marshal(properties)
	if err != nil {
		return fmt.Errorf("series mapping: %w", ErrSeriesMarshal)
	}

	url := fmt.Sprintf("%s/%s/_mapping?ignore_unavailable=true&allow_no_indices=true", s.baseURL, SeriesIndexPattern)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("series mapping: %w", ErrSeriesRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("series mapping: %w", ErrSeriesTemplate)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("series mapping: %w", ErrSeriesTemplate)
	}
	return nil
}

// DeleteExpiredSeriesIndices drops every daily series index that ended before
// the given cutoff. This is why the indices are daily: an index wholly past the
// longest retention in use is removed in one call instead of having its
// documents ground through delete_by_query. Indices younger than the cutoff may
// still hold expired documents of shorter-retention organizations; those remain
// delete_by_query's job.
func (s *OpenSearchService) DeleteExpiredSeriesIndices(cutoff time.Time) (int, error) {
	url := fmt.Sprintf("%s/_cat/indices/%s?h=index&format=json", s.baseURL, SeriesIndexPattern)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("series retention: %w", ErrSeriesRequest)
	}
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("series retention: %w", ErrSeriesIndexList)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("series retention: %w", ErrSeriesIndexList)
	}

	var listed []struct {
		Index string `json:"index"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		return 0, fmt.Errorf("series retention: %w", ErrSeriesDecode)
	}

	deleted := 0
	for _, entry := range listed {
		if !seriesIndexExpired(entry.Index, cutoff) {
			continue
		}
		if err := s.deleteIndex(entry.Index); err != nil {
			return deleted, err
		}
		deleted++
		slog.Info("dropped expired series index", "index", entry.Index)
	}
	return deleted, nil
}

// seriesIndexExpired reports whether a daily index is wholly past the cutoff.
// The index holds points stamped within its day, so it is fully expired only
// once the day's end has passed the cutoff — dropping it on the cutoff's own
// day would take live documents with it.
func seriesIndexExpired(name string, cutoff time.Time) bool {
	day, err := time.Parse("2006.01.02", strings.TrimPrefix(name, SeriesIndexPrefix+"-"))
	if err != nil {
		// Not one of ours; _cat patterns can match more than we created.
		return false
	}
	return !day.Add(24 * time.Hour).After(cutoff)
}

func (s *OpenSearchService) deleteIndex(name string) error {
	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/%s", s.baseURL, name), nil)
	if err != nil {
		return fmt.Errorf("series retention: %w", ErrSeriesRequest)
	}
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("series retention: %w", ErrSeriesIndexDelete)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("series retention: %w", ErrSeriesIndexDelete)
	}
	return nil
}

// IndexSeriesPoint writes one datapoint to its daily index.
func (s *OpenSearchService) IndexSeriesPoint(p SeriesPoint) error {
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return err
		}
	}

	index, doc := seriesDoc(p)
	if err := s.indexDocument(index, doc); err != nil {
		return fmt.Errorf("index series point: %w", ErrSeriesIndex)
	}
	return nil
}

// seriesDoc renders a point as the document stored and the daily index it goes to.
func seriesDoc(p SeriesPoint) (string, map[string]interface{}) {
	ts := p.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	encoded := encodeLabels(p.Labels)

	doc := map[string]interface{}{
		"@timestamp":   ts.UTC().Format(time.RFC3339),
		"metric_name":  p.MetricName,
		"metric_type":  p.MetricType,
		"unit":         p.Unit,
		"value":        p.Value,
		"labels_kv":    encoded,
		"label_keys":   labelKeys(p.Labels),
		"series_hash":  seriesHash(p.MetricName, encoded),
		"service_name": p.ServiceName,
		"hostname":     p.Hostname,
	}

	if p.Temporality != "" {
		doc["temporality"] = p.Temporality
	}

	// An unattributed point must leave the field absent, not set it to 0:
	// retention collects orphans with must_not exists on organization_id, and a
	// concrete 0 belongs to no organization and would never expire.
	if p.OrganizationID > 0 {
		doc["organization_id"] = p.OrganizationID
	}
	return seriesIndexFor(ts), doc
}
