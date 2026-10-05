package workers

import (
	"testing"

	"middle-monitor/backend/models"
)

func f64(v float64) *float64 { return &v }

// The two-level thresholds are what the alert form writes. Critical has to win
// over warning whenever both are crossed, or a page-worthy breach would notify
// at the severity a receiver ignores.
func TestRuleSeverityForPrefersCriticalOverWarning(t *testing.T) {
	rule := models.AlertRule{Operator: "gt", WarningThreshold: f64(70), CriticalThreshold: f64(90)}

	cases := []struct {
		name  string
		value float64
		want  string
	}{
		{"below both", 50, ""},
		{"exactly on the warning threshold is not over it", 70, ""},
		{"over warning only", 80, "warning"},
		{"exactly on the critical threshold still only breaches warning", 90, "warning"},
		{"over both", 95, "critical"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ruleSeverityFor(rule, c.value); got != c.want {
				t.Fatalf("value %.0f: got %q, want %q", c.value, got, c.want)
			}
		})
	}
}

// Rules written before the two-level thresholds existed carry a single
// threshold plus a severity, and they must keep firing at that severity.
func TestRuleSeverityForFallsBackToTheLegacySingleThreshold(t *testing.T) {
	legacy := models.AlertRule{Operator: "gt", Threshold: 80, Severity: "warning"}
	if got := ruleSeverityFor(legacy, 90); got != "warning" {
		t.Fatalf("legacy rule: got %q, want warning", got)
	}
	if got := ruleSeverityFor(legacy, 10); got != "" {
		t.Fatalf("legacy rule within limits: got %q, want empty", got)
	}

	// A legacy rule with no severity recorded is treated as page-worthy rather
	// than silently dropped.
	noSeverity := models.AlertRule{Operator: "gt", Threshold: 80}
	if got := ruleSeverityFor(noSeverity, 90); got != "critical" {
		t.Fatalf("legacy rule without severity: got %q, want critical", got)
	}
}

// A "lt" rule alerts when a value falls, so its critical threshold sits below
// its warning one. The severity order must still hold.
func TestRuleSeverityForHandlesFallingMetrics(t *testing.T) {
	rule := models.AlertRule{Operator: "lt", WarningThreshold: f64(20), CriticalThreshold: f64(5)}

	if got := ruleSeverityFor(rule, 50); got != "" {
		t.Fatalf("healthy value: got %q, want empty", got)
	}
	if got := ruleSeverityFor(rule, 10); got != "warning" {
		t.Fatalf("below warning: got %q, want warning", got)
	}
	if got := ruleSeverityFor(rule, 1); got != "critical" {
		t.Fatalf("below critical: got %q, want critical", got)
	}
}

// Hysteresis is the whole point of a recovery threshold: a value that drops
// just under the trigger must NOT resolve the incident, or an alert sitting on
// the threshold flaps open and closed every cycle.
func TestRuleRecoveredAppliesHysteresis(t *testing.T) {
	rule := models.AlertRule{Operator: "gt", CriticalThreshold: f64(90), RecoveryThreshold: f64(80)}

	if ruleRecovered(rule, 85) {
		t.Fatal("85 is under the trigger but still over the recovery threshold: must not resolve")
	}
	if !ruleRecovered(rule, 79) {
		t.Fatal("79 crossed back past the recovery threshold: must resolve")
	}
}

// Without a recovery threshold there is nothing to be hysteretic about, so the
// incident closes as soon as no severity is breached.
func TestRuleRecoveredWithoutRecoveryThreshold(t *testing.T) {
	rule := models.AlertRule{Operator: "gt", WarningThreshold: f64(70), CriticalThreshold: f64(90)}

	if ruleRecovered(rule, 75) {
		t.Fatal("75 still breaches warning: must not resolve")
	}
	if !ruleRecovered(rule, 10) {
		t.Fatal("10 breaches nothing: must resolve")
	}
}

// The notification reports the threshold that was actually crossed. Reporting
// the legacy single value instead would make observed_value and threshold
// incomparable in the webhook payload.
func TestRuleThresholdForReportsTheCrossedLevel(t *testing.T) {
	rule := models.AlertRule{Threshold: 1, WarningThreshold: f64(70), CriticalThreshold: f64(90)}

	if got := ruleThresholdFor(rule, "critical"); got != 90 {
		t.Fatalf("critical: got %v, want 90", got)
	}
	if got := ruleThresholdFor(rule, "warning"); got != 70 {
		t.Fatalf("warning: got %v, want 70", got)
	}
	// A legacy rule has neither level, so the single threshold is all there is.
	legacy := models.AlertRule{Threshold: 42}
	if got := ruleThresholdFor(legacy, "critical"); got != 42 {
		t.Fatalf("legacy: got %v, want 42", got)
	}
}

// An unknown operator must not silently behave like "gt": a typo in a stored
// rule would then fire alerts nobody configured.
func TestEvalOperator(t *testing.T) {
	cases := []struct {
		op    string
		value float64
		want  bool
	}{
		{"gt", 11, true}, {"gt", 10, false},
		{"gte", 10, true}, {"gte", 9, false},
		{"lt", 9, true}, {"lt", 10, false},
		{"lte", 10, true}, {"lte", 11, false},
		{"eq", 10, true}, {"eq", 11, false},
		{"", 999, false},
		{"contains", 999, false},
	}
	for _, c := range cases {
		if got := evalOperator(c.op, c.value, 10); got != c.want {
			t.Fatalf("%q(%v, 10): got %v, want %v", c.op, c.value, got, c.want)
		}
	}
}

// The aggregation comes from the rule, so it reaches SQL. Anything unrecognised
// has to fall back to a known-safe expression rather than reach the database.
func TestAggregateExpr(t *testing.T) {
	cases := map[string]string{
		"min":                              "MIN(latency)",
		"max":                              "MAX(latency)",
		"sum":                              "SUM(latency)",
		"avg":                              "AVG(latency)",
		"":                                 "AVG(latency)",
		"latency); DROP TABLE services --": "AVG(latency)",
		"p95":                              "percentile_cont(0.95) WITHIN GROUP (ORDER BY latency)",
		"p50":                              "percentile_cont(0.5) WITHIN GROUP (ORDER BY latency)",
	}
	for aggregation, want := range cases {
		if got := aggregateExpr(aggregation, "latency"); got != want {
			t.Fatalf("%q: got %q, want %q", aggregation, got, want)
		}
	}
}
