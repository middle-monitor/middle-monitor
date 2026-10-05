package services

import (
	"context"
	"time"
)

const maxExpressionQueries = 5

// NamedSeriesQuery is one query of an expression, addressed as $Ref.
type NamedSeriesQuery struct {
	Ref        string         `json:"ref"`
	MetricName string         `json:"metric"`
	Filters    []SeriesFilter `json:"filters"`
	GroupBy    string         `json:"group_by"`
	// Empty means avg, as on GET /metrics/series/query.
	Aggregation string `json:"aggregation"`
}

// SeriesExpressionQuery runs several queries over one window and combines them.
type SeriesExpressionQuery struct {
	OrganizationID int64
	Queries        []NamedSeriesQuery
	// Empty returns every query's series as-is.
	Expression string
	Start      time.Time
	End        time.Time
	Step       time.Duration
	Hostnames  []string
}

// QueryExpression runs every query on the same step, so their buckets line up.
func (s *OpenSearchService) QueryExpression(ctx context.Context, q SeriesExpressionQuery) ([]SeriesResult, error) {
	// Fail on a typo before spending queries on it.
	if err := ValidateSeriesExpression(q.Queries, q.Expression); err != nil {
		return nil, err
	}
	if !q.End.After(q.Start) {
		return nil, ErrSeriesRange
	}

	step := time.Duration(int(resolveStep(q.Start, q.End, q.Step).Seconds())) * time.Second
	env := make(map[string][]SeriesResult, len(q.Queries))
	all := []SeriesResult{}
	for _, nq := range q.Queries {
		sq := SeriesQuery{
			OrganizationID: q.OrganizationID,
			MetricName:     nq.MetricName,
			Filters:        nq.Filters,
			GroupBy:        nq.GroupBy,
			Aggregation:    aggregationOrDefault(nq.Aggregation),
			Start:          q.Start,
			End:            q.End,
			Step:           step,
			Hostnames:      q.Hostnames,
		}
		var results []SeriesResult
		var err error
		if nq.Aggregation == AggregationRate {
			results, err = s.QueryRateSeries(ctx, sq)
		} else {
			results, err = s.QuerySeries(ctx, sq)
		}
		if err != nil {
			return nil, err
		}
		for i := range results {
			results[i].Query = nq.Ref
		}
		env[nq.Ref] = results
		all = append(all, results...)
	}

	if q.Expression == "" {
		return all, nil
	}
	return EvaluateExpression(q.Expression, env)
}

// ValidateSeriesExpression checks queries and expression without touching OpenSearch.
func ValidateSeriesExpression(queries []NamedSeriesQuery, expression string) error {
	if err := validateExpressionQueries(queries); err != nil {
		return err
	}
	if expression == "" {
		return nil
	}
	// A dry run on empty series surfaces unknown refs and refless math too.
	env := make(map[string][]SeriesResult, len(queries))
	for _, nq := range queries {
		env[nq.Ref] = []SeriesResult{}
	}
	_, err := EvaluateExpression(expression, env)
	return err
}

func validateExpressionQueries(queries []NamedSeriesQuery) error {
	if len(queries) == 0 || len(queries) > maxExpressionQueries {
		return ErrExpressionQueries
	}
	seen := map[string]bool{}
	for _, nq := range queries {
		if len(nq.Ref) != 1 || nq.Ref[0] < 'A' || nq.Ref[0] > 'Z' || seen[nq.Ref] {
			return ErrExpressionRef
		}
		seen[nq.Ref] = true
		if nq.MetricName == "" {
			return ErrSeriesMetricRequired
		}
		if aggregation := aggregationOrDefault(nq.Aggregation); aggregation != AggregationRate && !validAggregations[aggregation] {
			return ErrSeriesAggregation
		}
	}
	return nil
}

func aggregationOrDefault(aggregation string) string {
	if aggregation == "" {
		return "avg"
	}
	return aggregation
}
