package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// A bulk of 2 000 series documents is about 1 MB: large enough to cut request
// overhead, small enough to stay far below OpenSearch's http.max_content_length.
const seriesBulkSize = 2000

// SeriesBulkError reports documents a bulk request refused while the request
// itself succeeded.
type SeriesBulkError struct {
	Failed int
	Total  int
	Reason string
}

func (e *SeriesBulkError) Error() string {
	return fmt.Sprintf("series bulk: %d of %d documents refused: %s", e.Failed, e.Total, e.Reason)
}

func (e *SeriesBulkError) Unwrap() error { return ErrSeriesIndex }

// IndexSeriesPoints writes points in bulk requests. Indexing them one request
// each cost a round trip per point: at 20 000 points a minute that kept the
// worker and OpenSearch busy on HTTP overhead alone.
func (s *OpenSearchService) IndexSeriesPoints(ctx context.Context, points []SeriesPoint) error {
	if len(points) == 0 {
		return nil
	}
	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return err
		}
	}
	var firstErr error
	for start := 0; start < len(points); start += seriesBulkSize {
		end := min(start+seriesBulkSize, len(points))
		if err := s.bulkSeries(ctx, points[start:end]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *OpenSearchService) bulkSeries(ctx context.Context, points []SeriesPoint) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, p := range points {
		// Each point names its own daily index: a batch can straddle midnight.
		index, doc := seriesDoc(p)
		if err := enc.Encode(map[string]interface{}{"index": map[string]string{"_index": index}}); err != nil {
			return fmt.Errorf("series bulk: %w", ErrSeriesMarshal)
		}
		if err := enc.Encode(doc); err != nil {
			return fmt.Errorf("series bulk: %w", ErrSeriesMarshal)
		}
	}

	// filter_path keeps the answer to the failures instead of one line per document.
	url := fmt.Sprintf("%s/_bulk?filter_path=errors,items.*.error", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return fmt.Errorf("series bulk: %w", ErrSeriesRequest)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("series bulk: %w", ErrSeriesIndex)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return newOpenSearchStatusError("series bulk", resp)
	}

	var result struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Error *struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("series bulk: %w", ErrSeriesDecode)
	}
	if !result.Errors {
		return nil
	}
	failed, reason := 0, ""
	for _, item := range result.Items {
		for _, r := range item {
			if r.Error == nil {
				continue
			}
			failed++
			if reason == "" {
				// The preview quotes the rejected value, which can be customer data.
				cut, _, _ := strings.Cut(r.Error.Reason, ". Preview of field's value")
				reason = r.Error.Type + ": " + cut
			}
		}
	}
	slog.Error("series bulk refused documents", "failed", failed, "total", len(points), "reason", reason)
	return &SeriesBulkError{Failed: failed, Total: len(points), Reason: reason}
}
