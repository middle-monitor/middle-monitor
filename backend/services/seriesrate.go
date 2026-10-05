package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const (
	TemporalityCumulative = "cumulative"
	TemporalityDelta      = "delta"

	// AggregationRate is per-second increase; only the expression endpoint serves it.
	AggregationRate = "rate"

	// rate is computed per series before summing, so the series count is the cost.
	maxRateSeries = 200

	// OpenSearch refuses a search that builds more than search.max_buckets
	// buckets; 65536 is the 2.19 default and docker-compose.prod.yml leaves it.
	openSearchMaxBuckets = 65536
	// Headroom under that limit: the aggregation tree carries more than timelines.
	rateBucketBudget = 60000
)

// rawSeriesTimeline is one stored series, bucketed but not yet aggregated across series.
type rawSeriesTimeline struct {
	temporality string
	groupValue  string
	buckets     []rawRateBucket
}

type rawRateBucket struct {
	timestamp time.Time
	last      *float64 // max in bucket: the running total for a cumulative counter
	total     *float64 // sum in bucket: the increment for a delta counter
}

// QueryRateSeries returns the per-second rate of a counter, summed per group-by value.
// Summing raw cumulative totals across hosts first would turn one process restart
// into a negative spike, hence the per-series pass.
func (s *OpenSearchService) QueryRateSeries(ctx context.Context, q SeriesQuery) ([]SeriesResult, error) {
	if q.MetricName == "" {
		return nil, ErrSeriesMetricRequired
	}
	if !q.End.After(q.Start) {
		return nil, ErrSeriesRange
	}

	// Whole seconds: the histogram interval is, and rates divide by it.
	step := time.Duration(int(resolveStep(q.Start, q.End, q.Step).Seconds())) * time.Second
	// One extra bucket before the window gives the first visible bucket a predecessor.
	fetchStart := q.Start.Add(-step)

	raw, err := s.searchSeriesAggs(ctx, rateSearchBody(q, fetchStart, step))
	if err != nil {
		return nil, err
	}
	timelines, err := parseRawTimelines(raw)
	if err != nil {
		return nil, err
	}
	return sumRatesByGroup(timelines, q.MetricName, q.GroupBy, step, q.Start), nil
}

func rateSearchBody(q SeriesQuery, fetchStart time.Time, step time.Duration) map[string]interface{} {
	filters := seriesQueryFilters(q, fetchStart)

	perSeries := map[string]interface{}{
		"temporality": map[string]interface{}{"terms": map[string]interface{}{"field": "temporality", "size": 1}},
		"timeline": map[string]interface{}{
			"date_histogram": map[string]interface{}{
				"field":          "@timestamp",
				"fixed_interval": fmt.Sprintf("%ds", int(step.Seconds())),
				"min_doc_count":  0,
				"extended_bounds": map[string]interface{}{
					"min": fetchStart.UTC().Format(time.RFC3339),
					"max": q.End.UTC().Format(time.RFC3339),
				},
			},
			"aggs": map[string]interface{}{
				"last":  map[string]interface{}{"max": map[string]interface{}{"field": "value"}},
				"total": map[string]interface{}{"sum": map[string]interface{}{"field": "value"}},
			},
		},
	}
	if q.GroupBy != "" {
		perSeries["group"] = map[string]interface{}{
			"terms": map[string]interface{}{
				"field":   "labels_kv",
				"size":    1,
				"include": escapeRegexPrefix(q.GroupBy+"=") + ".*",
			},
		}
	}

	return map[string]interface{}{
		"size":  0,
		"query": map[string]interface{}{"bool": map[string]interface{}{"filter": filters}},
		"aggs": map[string]interface{}{
			"series": map[string]interface{}{
				"terms": map[string]interface{}{"field": "series_hash", "size": rateSeriesLimit(fetchStart, q.End, step)},
				"aggs":  perSeries,
			},
		},
	}
}

// rateSeriesLimit caps the series count so series times buckets stays inside the
// bucket budget. Past it OpenSearch answers too_many_buckets_exception, which
// reaches the client as a 500 rather than as ErrSeriesTooMany.
func rateSeriesLimit(fetchStart, end time.Time, step time.Duration) int {
	// Epoch-aligned bounds can add a bucket at each end, and every series also
	// carries its temporality and group-by sub-aggregations.
	perSeries := int(end.Sub(fetchStart)/step) + 4
	limit := rateBucketBudget / perSeries
	if limit > maxRateSeries {
		return maxRateSeries
	}
	if limit < 1 {
		return 1
	}
	return limit
}

type termsKeysAgg struct {
	Buckets []struct {
		Key string `json:"key"`
	} `json:"buckets"`
}

func parseRawTimelines(aggs map[string]json.RawMessage) ([]rawSeriesTimeline, error) {
	raw, ok := aggs["series"]
	if !ok {
		return nil, nil
	}
	var series struct {
		SumOtherDocCount int64 `json:"sum_other_doc_count"`
		Buckets          []struct {
			Temporality termsKeysAgg `json:"temporality"`
			Group       termsKeysAgg `json:"group"`
			Timeline    struct {
				Buckets []struct {
					KeyMillis int64 `json:"key"`
					Last      struct {
						Value *float64 `json:"value"`
					} `json:"last"`
					Total struct {
						Value *float64 `json:"value"`
					} `json:"total"`
					DocCount int64 `json:"doc_count"`
				} `json:"buckets"`
			} `json:"timeline"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &series); err != nil {
		return nil, fmt.Errorf("rate series: %w", ErrSeriesDecode)
	}
	// A truncated series set would silently under-report the rate.
	if series.SumOtherDocCount > 0 {
		return nil, ErrSeriesTooMany
	}

	timelines := make([]rawSeriesTimeline, 0, len(series.Buckets))
	for _, sb := range series.Buckets {
		tl := rawSeriesTimeline{temporality: TemporalityCumulative}
		// Points written before temporality was stored come from SDKs that default to cumulative.
		if len(sb.Temporality.Buckets) > 0 {
			tl.temporality = sb.Temporality.Buckets[0].Key
		}
		if len(sb.Group.Buckets) > 0 {
			if _, value, err := DecodeLabel(sb.Group.Buckets[0].Key); err == nil {
				tl.groupValue = value
			}
		}
		for _, b := range sb.Timeline.Buckets {
			bucket := rawRateBucket{timestamp: time.UnixMilli(b.KeyMillis).UTC()}
			if b.DocCount > 0 {
				bucket.last = b.Last.Value
				bucket.total = b.Total.Value
			}
			tl.buckets = append(tl.buckets, bucket)
		}
		timelines = append(timelines, tl)
	}
	return timelines, nil
}

// seriesRate turns one series into per-second rates, one per bucket. A last
// bucket that end cuts in half is still divided by the nominal step, so the
// final point reads low; dropping it instead would make the chart lag the
// requested window by up to one step.
func seriesRate(tl rawSeriesTimeline, step time.Duration) []SeriesDataPoint {
	points := make([]SeriesDataPoint, 0, len(tl.buckets))
	var prev *rawRateBucket
	for i := range tl.buckets {
		b := tl.buckets[i]
		point := SeriesDataPoint{Timestamp: b.timestamp}

		if tl.temporality == TemporalityDelta {
			// An empty bucket stays a gap: a silent exporter is indistinguishable
			// from no increment, and drawing zero would invent a reading.
			if b.total != nil {
				rate := *b.total / step.Seconds()
				point.Value = &rate
			}
		} else if b.last != nil {
			if prev != nil {
				increase := *b.last - *prev.last
				// A counter that went down was reset: it restarted from zero.
				if increase < 0 {
					increase = *b.last
				}
				rate := increase / b.timestamp.Sub(prev.timestamp).Seconds()
				point.Value = &rate
			}
			prev = &tl.buckets[i]
		}
		points = append(points, point)
	}
	return points
}

// sumRatesByGroup adds the per-series rates of each group and drops the lookback bucket.
func sumRatesByGroup(timelines []rawSeriesTimeline, metricName, groupBy string, step time.Duration, visibleStart time.Time) []SeriesResult {
	type group struct {
		sums map[int64]*float64
	}
	groups := map[string]*group{}
	order := []string{}
	stamps := map[int64]time.Time{}
	// Histogram buckets align on the Unix epoch, time.Truncate on year 1.
	stepSeconds := int64(step.Seconds())
	firstVisible := time.Unix(visibleStart.Unix()-visibleStart.Unix()%stepSeconds, 0).UTC()

	for _, tl := range timelines {
		// Like QuerySeries, a series without the group-by label belongs to no group.
		if groupBy != "" && tl.groupValue == "" {
			continue
		}
		g, ok := groups[tl.groupValue]
		if !ok {
			g = &group{sums: map[int64]*float64{}}
			groups[tl.groupValue] = g
			order = append(order, tl.groupValue)
		}
		for _, p := range seriesRate(tl, step) {
			if p.Timestamp.Before(firstVisible) {
				continue
			}
			key := p.Timestamp.UnixMilli()
			stamps[key] = p.Timestamp
			if _, seen := g.sums[key]; !seen {
				g.sums[key] = nil
			}
			if p.Value == nil {
				continue
			}
			if g.sums[key] == nil {
				v := *p.Value
				g.sums[key] = &v
			} else {
				*g.sums[key] += *p.Value
			}
		}
	}

	keys := make([]int64, 0, len(stamps))
	for k := range stamps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	sort.Strings(order)

	results := make([]SeriesResult, 0, len(order))
	for _, value := range order {
		labels := map[string]string{}
		if groupBy != "" {
			labels[groupBy] = value
		}
		points := make([]SeriesDataPoint, 0, len(keys))
		for _, k := range keys {
			points = append(points, SeriesDataPoint{Timestamp: stamps[k], Value: groups[value].sums[k]})
		}
		results = append(results, SeriesResult{MetricName: metricName, Labels: labels, Points: points})
	}
	return results
}
