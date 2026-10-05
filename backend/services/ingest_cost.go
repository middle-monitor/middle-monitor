package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Long enough to hold several scrapes of a slow target, short enough to show
// what a config change did a few minutes later.
const ingestCostWindow = 10 * time.Minute

// TargetIngestCost is what one scrape target costs against the metric budget.
type TargetIngestCost struct {
	// Name is the agent's scrape_target label; empty for the agent's own
	// system metrics and anything not scraped.
	Name                  string             `json:"name"`
	PointsPerMinute       int                `json:"points_per_minute"`
	Series                int                `json:"series"`
	ScrapeIntervalSeconds int                `json:"scrape_interval_seconds"` // 0 when unknown
	BucketPointsPerMinute int                `json:"bucket_points_per_minute"`
	TopMetrics            []MetricIngestCost `json:"top_metrics"`
}

// MetricIngestCost is one metric name's share of a target.
type MetricIngestCost struct {
	Name            string `json:"name"`
	PointsPerMinute int    `json:"points_per_minute"`
}

// HostIngestCost breaks a host's metric points per minute down by target.
type HostIngestCost struct {
	WindowMinutes   int                `json:"window_minutes"`
	PointsPerMinute int                `json:"points_per_minute"`
	Targets         []TargetIngestCost `json:"targets"`
}

type termsBucket struct {
	Key      string `json:"key"`
	DocCount int    `json:"doc_count"`
}

type targetAgg struct {
	Key      string `json:"key"`
	DocCount int    `json:"doc_count"`
	Series   struct {
		Value int `json:"value"`
	} `json:"series"`
	Scrapes struct {
		Value int `json:"value"`
	} `json:"scrapes"`
	First struct {
		Value float64 `json:"value"`
	} `json:"first"`
	Last struct {
		Value float64 `json:"value"`
	} `json:"last"`
	Buckets struct {
		DocCount int `json:"doc_count"`
	} `json:"buckets"`
	Top struct {
		Buckets []termsBucket `json:"buckets"`
	} `json:"top"`
}

func targetSubAggs() map[string]interface{} {
	return map[string]interface{}{
		"series": map[string]interface{}{"cardinality": map[string]interface{}{"field": "series_hash"}},
		// Every sample of one scrape carries the scrape's timestamp, so distinct
		// timestamps count scrapes and give the interval.
		"scrapes": map[string]interface{}{"cardinality": map[string]interface{}{"field": "@timestamp"}},
		"first":   map[string]interface{}{"min": map[string]interface{}{"field": "@timestamp"}},
		"last":    map[string]interface{}{"max": map[string]interface{}{"field": "@timestamp"}},
		"buckets": map[string]interface{}{"filter": map[string]interface{}{
			"wildcard": map[string]interface{}{"metric_name": "*_bucket"},
		}},
		"top": map[string]interface{}{"terms": map[string]interface{}{"field": "metric_name", "size": 3}},
	}
}

// HostIngestCost reads what a host sent over the last minutes, per scrape target.
func (s *OpenSearchService) HostIngestCost(ctx context.Context, orgID int64, hostname string) (*HostIngestCost, error) {
	end := time.Now().UTC()
	filters := []interface{}{
		orgFilter(orgID),
		timeRangeFilter(end.Add(-ingestCostWindow), end),
		map[string]interface{}{"term": map[string]interface{}{"hostname": hostname}},
	}
	notScraped := map[string]interface{}{"bool": map[string]interface{}{"must_not": []interface{}{
		map[string]interface{}{"prefix": map[string]interface{}{"labels_kv": "scrape_target="}},
	}}}
	body := map[string]interface{}{
		"size":  0,
		"query": map[string]interface{}{"bool": map[string]interface{}{"filter": filters}},
		"aggs": map[string]interface{}{
			"targets": map[string]interface{}{
				"terms": map[string]interface{}{"field": "labels_kv", "include": "scrape_target=.*", "size": 100},
				"aggs":  targetSubAggs(),
			},
			"unscraped": map[string]interface{}{"filter": notScraped, "aggs": targetSubAggs()},
		},
	}

	aggs, err := s.searchSeriesAggs(ctx, body)
	if err != nil {
		return nil, err
	}
	return parseHostIngestCost(aggs)
}

// observed returns the scrape interval (0 when unknown) and the time the target
// actually covered, so a target added mid-window is not diluted over the full window.
func (t targetAgg) observed() (interval, covered time.Duration) {
	if t.Scrapes.Value < 2 {
		return 0, ingestCostWindow
	}
	spread := time.Duration(t.Last.Value-t.First.Value) * time.Millisecond
	interval = spread / time.Duration(t.Scrapes.Value-1)
	return interval, spread + interval
}

func parseHostIngestCost(aggs map[string]json.RawMessage) (*HostIngestCost, error) {
	var targets struct {
		Buckets []targetAgg `json:"buckets"`
	}
	var unscraped targetAgg
	if raw, ok := aggs["targets"]; ok {
		if err := json.Unmarshal(raw, &targets); err != nil {
			return nil, fmt.Errorf("ingest cost: %w", ErrSeriesDecode)
		}
	}
	if raw, ok := aggs["unscraped"]; ok {
		if err := json.Unmarshal(raw, &unscraped); err != nil {
			return nil, fmt.Errorf("ingest cost: %w", ErrSeriesDecode)
		}
	}

	out := &HostIngestCost{WindowMinutes: int(ingestCostWindow.Minutes()), Targets: []TargetIngestCost{}}
	add := func(name string, t targetAgg) {
		if t.DocCount == 0 {
			return
		}
		interval, covered := t.observed()
		perMinute := func(count int) int {
			return int(float64(count) * time.Minute.Seconds() / covered.Seconds())
		}
		cost := TargetIngestCost{
			Name:                  name,
			PointsPerMinute:       perMinute(t.DocCount),
			Series:                t.Series.Value,
			ScrapeIntervalSeconds: int(interval.Seconds()),
			BucketPointsPerMinute: perMinute(t.Buckets.DocCount),
			TopMetrics:            []MetricIngestCost{},
		}
		for _, m := range t.Top.Buckets {
			cost.TopMetrics = append(cost.TopMetrics, MetricIngestCost{Name: m.Key, PointsPerMinute: perMinute(m.DocCount)})
		}
		out.Targets = append(out.Targets, cost)
		out.PointsPerMinute += cost.PointsPerMinute
	}
	for _, t := range targets.Buckets {
		add(strings.TrimPrefix(t.Key, "scrape_target="), t)
	}
	add("", unscraped)

	// Heaviest first: that is where a change pays.
	sort.SliceStable(out.Targets, func(i, j int) bool {
		return out.Targets[i].PointsPerMinute > out.Targets[j].PointsPerMinute
	})
	return out, nil
}
