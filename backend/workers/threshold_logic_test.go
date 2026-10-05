package workers

import "testing"

// Agent metrics are pushed, not polled, so nothing retries them the way the
// check worker retries a failed probe. The sample window is what replaces that
// retry: one spike in an otherwise healthy window must not page anyone.
func TestClassifyThresholdWindowNeedsEverySampleOverTheThreshold(t *testing.T) {
	check := thresholdCheck{warning: f64(70), critical: f64(90)}

	cases := []struct {
		name         string
		values       []float64
		wantSeverity string
		wantLevel    float64
	}{
		{"one spike in a healthy window is smoothed away", []float64{95, 10, 10}, "", 0},
		{"one healthy sample clears the window", []float64{95, 95, 10}, "", 0},
		{"every sample over critical pages", []float64{95, 92, 91}, "critical", 90},
		{"every sample over warning only warns", []float64{80, 75, 71}, "warning", 70},
		{"a sample exactly on the threshold is not over it", []float64{95, 95, 90}, "warning", 70},
		{"everything healthy", []float64{10, 20, 30}, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			severity, level := classifyThresholdWindow(check, c.values)
			if severity != c.wantSeverity || level != c.wantLevel {
				t.Fatalf("got (%q, %v), want (%q, %v)", severity, level, c.wantSeverity, c.wantLevel)
			}
		})
	}
}

// A check may carry only one of the two levels. The missing one must never be
// read as zero, which every positive value would cross.
func TestClassifyThresholdWindowIgnoresUnsetLevels(t *testing.T) {
	warningOnly := thresholdCheck{warning: f64(70)}
	if severity, _ := classifyThresholdWindow(warningOnly, []float64{999, 999}); severity != "warning" {
		t.Fatalf("warning-only check: got %q, want warning", severity)
	}

	criticalOnly := thresholdCheck{critical: f64(90)}
	if severity, _ := classifyThresholdWindow(criticalOnly, []float64{80, 80}); severity != "" {
		t.Fatalf("critical-only check under its level: got %q, want empty", severity)
	}

	none := thresholdCheck{}
	if severity, _ := classifyThresholdWindow(none, []float64{999}); severity != "" {
		t.Fatal("a check with no threshold must never breach")
	}
}

// Certificates are the one inverted comparison in the product: fewer days left
// is worse. Reusing the "over the threshold" rule here would alert on every
// healthy certificate and stay silent on the expiring ones.
func TestClassifyCertThresholdIsInverted(t *testing.T) {
	check := thresholdCheck{warning: f64(30), critical: f64(7)}

	cases := []struct {
		name         string
		days         float64
		wantSeverity string
	}{
		{"plenty of time left", 90, ""},
		{"exactly on the warning boundary is not under it", 30, ""},
		{"inside the warning window", 20, "warning"},
		{"inside the critical window", 3, "critical"},
		{"already expired", -5, "critical"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if severity, _ := classifyCertThreshold(check, c.days); severity != c.wantSeverity {
				t.Fatalf("%v days: got %q, want %q", c.days, severity, c.wantSeverity)
			}
		})
	}
}

// agentMetricType is what decides whether the evaluator reads metric_value or
// latency. agent_network must stay out: its metric_value is throughput while
// its thresholds target ping latency, so reading metric_value would compare
// megabytes per second against a millisecond threshold.
func TestAgentMetricTypeExcludesNetwork(t *testing.T) {
	cases := map[string]string{
		"agent_cpu":     "cpu",
		"agent_ram":     "ram",
		"agent_disk":    "disk",
		"agent_network": "",
		"http":          "",
		"certificate":   "",
	}
	for svcType, want := range cases {
		if got := agentMetricType(svcType); got != want {
			t.Fatalf("%q: got %q, want %q", svcType, got, want)
		}
	}
}

// The unit reaches the alert body, where a percentage rendered as milliseconds
// tells the reader the wrong story about how bad the breach is.
func TestMetricLabelUnitMatchesTheValueBeingCompared(t *testing.T) {
	cases := map[string][2]string{
		"agent_cpu":     {"CPU", "%"},
		"agent_ram":     {"RAM", "%"},
		"agent_disk":    {"Disk", "%"},
		"agent_network": {"Latency", "ms"},
		"http":          {"Latency", "ms"},
	}
	for svcType, want := range cases {
		label, unit := metricLabelUnit(svcType)
		if label != want[0] || unit != want[1] {
			t.Fatalf("%q: got (%q, %q), want (%q, %q)", svcType, label, unit, want[0], want[1])
		}
	}
}
