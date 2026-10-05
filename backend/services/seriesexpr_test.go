package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func exprSeries(labels map[string]string, values ...*float64) SeriesResult {
	points := make([]SeriesDataPoint, len(values))
	for i, v := range values {
		points[i] = SeriesDataPoint{Timestamp: rateT0.Add(time.Duration(i) * time.Minute), Value: v}
	}
	return SeriesResult{Labels: labels, Points: points}
}

func route(r string) map[string]string { return map[string]string{"route": r} }

// Operator precedence decides the number on the chart: "$A / $B * 100" must be
// a percentage, not A divided by 100*B.
func TestExpressionHonorsPrecedence(t *testing.T) {
	env := map[string][]SeriesResult{
		"A": {exprSeries(nil, f(5))},
		"B": {exprSeries(nil, f(50))},
	}

	got, err := EvaluateExpression("$A / $B * 100", env)
	if err != nil {
		t.Fatal(err)
	}
	if v := got[0].Points[0].Value; v == nil || *v != 10 {
		t.Errorf("got %v, want 10 (percent)", v)
	}

	got, err = EvaluateExpression("-($A - $B) / 5", env)
	if err != nil {
		t.Fatal(err)
	}
	if v := got[0].Points[0].Value; v == nil || *v != 9 {
		t.Errorf("unary minus over parentheses: got %v, want 9", v)
	}
}

// An error ratio per route must divide each route by its own total, never by
// another route's.
func TestExpressionPairsSeriesByLabels(t *testing.T) {
	env := map[string][]SeriesResult{
		"A": {exprSeries(route("/cart"), f(1)), exprSeries(route("/checkout"), f(3))},
		"B": {exprSeries(route("/checkout"), f(30)), exprSeries(route("/cart"), f(100))},
	}

	got, err := EvaluateExpression("$A / $B", env)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"/cart": 0.01, "/checkout": 0.1}
	for _, s := range got {
		if v := s.Points[0].Value; v == nil || *v != want[s.Labels["route"]] {
			t.Errorf("%s: got %v, want %v", s.Labels["route"], v, want[s.Labels["route"]])
		}
	}
}

// Each route's share of total traffic: a single series on one side applies to all.
func TestExpressionBroadcastsALoneSeries(t *testing.T) {
	env := map[string][]SeriesResult{
		"A": {exprSeries(route("/cart"), f(25)), exprSeries(route("/checkout"), f(75))},
		"B": {exprSeries(map[string]string{}, f(100))},
	}

	got, err := EvaluateExpression("$A / $B * 100", env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Labels["route"] == "" {
		t.Fatalf("broadcast must keep the per-route labels, got %+v", got)
	}
}

// A counter left with one labeled series is not a total: the errors of /checkout
// must stay divided by the traffic of /checkout, not by every route's.
func TestExpressionDoesNotBroadcastALabeledLoneSeries(t *testing.T) {
	errorsByRoute := []SeriesResult{exprSeries(route("/checkout"), f(3))}
	requestsByRoute := []SeriesResult{exprSeries(route("/checkout"), f(30)), exprSeries(route("/cart"), f(100))}

	for _, tc := range []struct {
		expression string
		env        map[string][]SeriesResult
		want       float64
	}{
		{"$A / $B", map[string][]SeriesResult{"A": errorsByRoute, "B": requestsByRoute}, 0.1},
		{"$B / $A", map[string][]SeriesResult{"A": errorsByRoute, "B": requestsByRoute}, 10},
	} {
		got, err := EvaluateExpression(tc.expression, tc.env)
		if err != nil {
			t.Fatalf("%s: %v", tc.expression, err)
		}
		if len(got) != 1 || got[0].Labels["route"] != "/checkout" {
			t.Fatalf("%s: want only /checkout, got %+v", tc.expression, got)
		}
		if v := got[0].Points[0].Value; v == nil || *v != tc.want {
			t.Errorf("%s: got %v, want %v", tc.expression, v, tc.want)
		}
	}
}

// Group-by on different labels has no sensible pairing; an empty chart would hide that.
func TestExpressionRejectsUnmatchedLabels(t *testing.T) {
	env := map[string][]SeriesResult{
		"A": {exprSeries(route("/a"), f(1)), exprSeries(route("/b"), f(1))},
		"B": {exprSeries(map[string]string{"host": "h1"}, f(1)), exprSeries(map[string]string{"host": "h2"}, f(1))},
	}
	if _, err := EvaluateExpression("$A + $B", env); !errors.Is(err, ErrExpressionNoMatch) {
		t.Errorf("got %v, want ErrExpressionNoMatch", err)
	}
}

// Division by zero and missing buckets must come out as gaps: JSON cannot carry
// NaN or Inf, and a zero would be a false reading.
func TestExpressionTurnsUndefinedIntoGaps(t *testing.T) {
	env := map[string][]SeriesResult{
		"A": {exprSeries(nil, f(1), nil)},
		"B": {exprSeries(nil, f(0), f(2))},
	}

	got, err := EvaluateExpression("$A / $B", env)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range got[0].Points {
		if p.Value != nil {
			t.Errorf("point %d: got %v, want a gap", i, *p.Value)
		}
	}
}

func TestExpressionSumAndAbs(t *testing.T) {
	env := map[string][]SeriesResult{
		"A": {exprSeries(route("/a"), f(-2), nil), exprSeries(route("/b"), f(-3), nil)},
	}

	got, err := EvaluateExpression("abs(sum($A))", env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sum must collapse to one series, got %d", len(got))
	}
	if v := got[0].Points[0].Value; v == nil || *v != 5 {
		t.Errorf("got %v, want 5", v)
	}
	if got[0].Points[1].Value != nil {
		t.Error("a bucket empty in every series must stay empty, not read 0")
	}
}

func TestExpressionRejectsBadInput(t *testing.T) {
	env := map[string][]SeriesResult{"A": {exprSeries(nil, f(1))}}
	cases := map[string]error{
		"$A +":     ErrExpressionSyntax,
		"($A":      ErrExpressionSyntax,
		"$a":       ErrExpressionSyntax,
		"$AB":      ErrExpressionSyntax,
		"1.2.3":    ErrExpressionSyntax,
		"rate($A)": ErrExpressionFunction,
		"$B * 2":   ErrExpressionUnknownRef,
		"1 + 2":    ErrExpressionScalar,
		string(make([]byte, maxExpressionLength+1)): ErrExpressionTooLong,
	}
	for expression, want := range cases {
		if _, err := EvaluateExpression(expression, env); !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", expression, err, want)
		}
	}
}

func TestValidateExpressionQueries(t *testing.T) {
	q := func(ref, agg string) NamedSeriesQuery {
		return NamedSeriesQuery{Ref: ref, MetricName: "m", Aggregation: agg}
	}
	cases := []struct {
		name    string
		queries []NamedSeriesQuery
		want    error
	}{
		{"rate accepted", []NamedSeriesQuery{q("A", "rate"), q("B", "avg")}, nil},
		// The GET route defaults to avg; a client cannot guess the field is required here.
		{"omitted aggregation defaults to avg", []NamedSeriesQuery{q("A", "")}, nil},
		{"none", nil, ErrExpressionQueries},
		{"too many", []NamedSeriesQuery{q("A", "avg"), q("B", "avg"), q("C", "avg"), q("D", "avg"), q("E", "avg"), q("F", "avg")}, ErrExpressionQueries},
		{"duplicate ref", []NamedSeriesQuery{q("A", "avg"), q("A", "avg")}, ErrExpressionRef},
		{"lowercase ref", []NamedSeriesQuery{q("a", "avg")}, ErrExpressionRef},
		{"bad aggregation", []NamedSeriesQuery{q("A", "median")}, ErrSeriesAggregation},
		{"missing metric", []NamedSeriesQuery{{Ref: "A", Aggregation: "avg"}}, ErrSeriesMetricRequired},
	}
	for _, tc := range cases {
		if err := validateExpressionQueries(tc.queries); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// The handler relies on this to refuse bad input before any search runs.
func TestValidateSeriesExpressionCatchesWhatOnlyEvaluationSees(t *testing.T) {
	queries := []NamedSeriesQuery{{Ref: "A", MetricName: "m", Aggregation: "avg"}}
	cases := map[string]error{
		"":         nil,
		"$A * 100": nil,
		"$B * 2":   ErrExpressionUnknownRef,
		"1 + 2":    ErrExpressionScalar,
		"$A +":     ErrExpressionSyntax,
	}
	for expression, want := range cases {
		if err := ValidateSeriesExpression(queries, expression); !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", expression, err, want)
		}
	}
}

// Every series query must carry the organization filter whatever else it has:
// labels come from customers' infrastructure, a missing scope leaks across tenants.
func TestSeriesQueryFiltersAlwaysScopeTheOrganization(t *testing.T) {
	for name, q := range map[string]SeriesQuery{
		"bare":         {OrganizationID: 42, MetricName: "m"},
		"with filters": {OrganizationID: 42, MetricName: "m", Filters: []SeriesFilter{{Key: "route", Value: "/a"}}, Hostnames: []string{"h1"}},
	} {
		raw, _ := json.Marshal(seriesQueryFilters(q, time.Now()))
		if !strings.Contains(string(raw), `{"term":{"organization_id":42}}`) {
			t.Errorf("%s: no organization filter in %s", name, raw)
		}
	}
}

// The api layer translates from Position and Token, so they must be filled.
func TestExpressionErrorsLocateTheMistake(t *testing.T) {
	env := map[string][]SeriesResult{"A": {exprSeries(nil, f(1))}}
	cases := map[string]ExpressionError{
		"$A +":    {Err: ErrExpressionSyntax, Position: 5},
		"$B * 2":  {Err: ErrExpressionUnknownRef, Token: "$B"},
		"foo($A)": {Err: ErrExpressionFunction, Token: "foo"},
	}
	for expression, want := range cases {
		_, err := EvaluateExpression(expression, env)
		var got *ExpressionError
		if !errors.As(err, &got) || got.Err != want.Err || got.Position != want.Position || got.Token != want.Token {
			t.Errorf("%q: got %v, want %+v", expression, err, want)
		}
	}
}
