package workers

import (
	"testing"

	"middle-monitor/backend/models"
)

// A custom rule must never reach the built-in SQL branch. Passing a nil db makes
// that failure loud: any fall-through would panic instead of quietly querying
// service_results for a metric that does not live there.
func TestQueryMetricTakesTheCustomBranch(t *testing.T) {
	metric := "http_requests_total"
	rule := models.AlertRule{
		OrganizationID: 1,
		// A built-in name is deliberately left in place: the custom field wins.
		Metric:       "cpu",
		CustomMetric: &metric,
		Aggregation:  "avg",
		Duration:     300,
	}

	value, err := queryMetric(nil, nil, rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != nil {
		t.Fatalf("expected no value without an OpenSearch client, got %v", *value)
	}
}

// An empty custom metric is not a custom rule: it must still read the built-in
// signal rather than issue an empty-metric query.
func TestQueryMetricIgnoresEmptyCustomMetric(t *testing.T) {
	empty := ""
	rule := models.AlertRule{
		OrganizationID: 1,
		Metric:         "unknown_signal",
		CustomMetric:   &empty,
		Aggregation:    "avg",
		Duration:       300,
	}

	// "unknown_signal" hits the switch's default, which returns nil without
	// touching the database, so a nil db is safe here too.
	value, err := queryMetric(nil, nil, rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != nil {
		t.Fatalf("expected no value, got %v", *value)
	}
}

func TestRuleMetricLabelPrefersTheCustomMetric(t *testing.T) {
	metric := "checkout_queue_depth"
	if got := ruleMetricLabel(models.AlertRule{Metric: "cpu", CustomMetric: &metric}); got != metric {
		t.Errorf("got %q, want %q", got, metric)
	}
	if got := ruleMetricLabel(models.AlertRule{Metric: "cpu"}); got != "cpu" {
		t.Errorf("got %q, want cpu", got)
	}
}
