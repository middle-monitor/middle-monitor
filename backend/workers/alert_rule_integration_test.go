package workers

import "testing"

// The alert-rule path is the half of the engine that reads a metric back out of
// the database before deciding anything. Which rows that read selects — the
// window, the organization, the target — is SQL, so it is checked here rather
// than against a mock that would replay whatever the test wrote.

// An organization must never see another one's data. This is the single most
// damaging failure mode the engine has: a rule that aggregates across tenants
// both leaks and pages the wrong people.
func TestAlertRuleOnlyReadsItsOwnOrganization(t *testing.T) {
	db := integrationDB(t)

	quiet := seedOrg(t, db, "quiet")
	noisy := seedOrg(t, db, "noisy")

	quietCheck := seedCheck(t, db, quiet, "cpu", "agent_cpu", nil, nil, 3)
	noisyCheck := seedCheck(t, db, noisy, "cpu", "agent_cpu", nil, nil, 3)
	seedResults(t, db, quietCheck, "cpu", 5, 5, 5)
	seedResults(t, db, noisyCheck, "cpu", 99, 99, 99)

	// max, not avg: with avg the other organization's samples would be diluted by
	// this one's and the test could pass even while leaking. On max, a single
	// foreign sample breaches immediately.
	ruleID := seedRule(t, db, quiet, "cpu high", alertRule{
		metric: "cpu", aggregation: "max", threshold: 80, critical: f64(80),
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 0 {
		t.Fatalf("got %d incidents, want 0: another organization's samples must be invisible", len(got))
	}
}

// A rule scoped to one host must ignore its neighbours, or pinning a threshold
// to a single machine would alert on the whole fleet.
func TestAlertRuleScopedToAHostIgnoresOtherHosts(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")

	quietHost := seedHost(t, db, orgID, "web-01")
	busyHost := seedHost(t, db, orgID, "web-02")

	quietCheck := seedCheckOnHost(t, db, orgID, quietHost, "cpu", "agent_cpu")
	busyCheck := seedCheckOnHost(t, db, orgID, busyHost, "cpu", "agent_cpu")
	seedResults(t, db, quietCheck, "cpu", 5, 5, 5)
	seedResults(t, db, busyCheck, "cpu", 99, 99, 99)

	// max for the same reason as the cross-organization case: averaging would
	// hide a leak behind this host's own healthy samples.
	scoped := seedRule(t, db, orgID, "web-01 cpu", alertRule{
		metric: "cpu", aggregation: "max", threshold: 80, critical: f64(80),
		targetType: "host", targetID: &quietHost,
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, scoped); len(got) != 0 {
		t.Fatalf("got %d incidents, want 0: a host-scoped rule must not read another host", len(got))
	}
}

// The aggregation the user picked has to be the one applied. avg and max
// disagree exactly when it matters: a short spike inside a calm window.
func TestAlertRuleAppliesTheChosenAggregation(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)

	// Average is 35, max is 99.
	seedResults(t, db, checkID, "cpu", 99, 3, 3)

	avgRule := seedRule(t, db, orgID, "cpu avg", alertRule{
		metric: "cpu", aggregation: "avg", threshold: 80, critical: f64(80),
	})
	maxRule := seedRule(t, db, orgID, "cpu max", alertRule{
		metric: "cpu", aggregation: "max", threshold: 80, critical: f64(80),
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, avgRule); len(got) != 0 {
		t.Fatalf("avg rule: got %d incidents, want 0 (mean is 35)", len(got))
	}
	if got := ruleIncidents(t, db, maxRule); len(got) != 1 {
		t.Fatalf("max rule: got %d incidents, want 1 (peak is 99)", len(got))
	}
}

// Samples older than the rule's window are history, not evidence. Reading them
// would keep an alert firing long after the condition cleared.
func TestAlertRuleIgnoresSamplesOlderThanItsWindow(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)

	seedOldResult(t, db, checkID, "cpu", 99, "2 hours")

	// A five-minute window cannot see a two-hour-old sample.
	ruleID := seedRule(t, db, orgID, "cpu recent", alertRule{
		metric: "cpu", threshold: 80, critical: f64(80), duration: 300,
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 0 {
		t.Fatalf("got %d incidents, want 0: a stale sample is outside the window", len(got))
	}
}

// One incident per rule while the condition holds. Without the dedup the
// evaluator would page on every tick.
func TestAlertRuleFiresOnceWhileTheConditionHolds(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)
	seedResults(t, db, checkID, "cpu", 95, 96, 97)

	ruleID := seedRule(t, db, orgID, "cpu high", alertRule{metric: "cpu", threshold: 80, critical: f64(80)})

	evaluateAlertRules(db, nil)
	evaluateAlertRules(db, nil)
	evaluateAlertRules(db, nil)

	incidents := ruleIncidents(t, db, ruleID)
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents over three cycles, want 1", len(incidents))
	}
	if incidents[0].Severity != "critical" {
		t.Fatalf("got severity %q, want critical", incidents[0].Severity)
	}
}

// Recovery closes the incident, and a later breach has to be able to open a new
// one. A rule that fires once and never again is worse than no rule.
func TestAlertRuleResolvesThenFiresAgain(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)
	ruleID := seedRule(t, db, orgID, "cpu high", alertRule{metric: "cpu", threshold: 80, critical: f64(80)})

	seedResults(t, db, checkID, "cpu", 95, 96, 97)
	evaluateAlertRules(db, nil)
	if got := ruleIncidents(t, db, ruleID); len(got) != 1 {
		t.Fatalf("first breach: got %d incidents, want 1", len(got))
	}

	clearResults(t, db, checkID)
	seedResults(t, db, checkID, "cpu", 5, 5, 5)
	evaluateAlertRules(db, nil)

	incidents := ruleIncidents(t, db, ruleID)
	if len(incidents) != 1 || incidents[0].Status != "resolved" {
		t.Fatalf("after recovery: got %+v, want one resolved incident", incidents)
	}

	clearResults(t, db, checkID)
	seedResults(t, db, checkID, "cpu", 95, 96, 97)
	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 2 {
		t.Fatalf("second breach: got %d incidents, want 2", len(got))
	}
}

// Hysteresis, end to end: a value under the trigger but still over the recovery
// threshold must keep the incident open. This is what stops an alert parked on
// its threshold from flapping every cycle.
func TestAlertRuleHoldsTheIncidentInsideTheRecoveryBand(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)
	ruleID := seedRule(t, db, orgID, "cpu high", alertRule{
		metric: "cpu", threshold: 90, critical: f64(90), recovery: f64(80),
	})

	seedResults(t, db, checkID, "cpu", 95, 95, 95)
	evaluateAlertRules(db, nil)

	// Back under the trigger, still inside the band.
	clearResults(t, db, checkID)
	seedResults(t, db, checkID, "cpu", 85, 85, 85)
	evaluateAlertRules(db, nil)

	incidents := ruleIncidents(t, db, ruleID)
	if len(incidents) != 1 || incidents[0].Status != "open" {
		t.Fatalf("inside the band: got %+v, want the incident still open", incidents)
	}

	// Past the recovery threshold.
	clearResults(t, db, checkID)
	seedResults(t, db, checkID, "cpu", 70, 70, 70)
	evaluateAlertRules(db, nil)

	incidents = ruleIncidents(t, db, ruleID)
	if len(incidents) != 1 || incidents[0].Status != "resolved" {
		t.Fatalf("past the band: got %+v, want the incident resolved", incidents)
	}
}

// A situation that gets worse has to say so. A receiver that acted on a warning
// never learns it became critical unless the open incident is escalated.
func TestAlertRuleEscalatesAWarningToCritical(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)
	ruleID := seedRule(t, db, orgID, "cpu", alertRule{
		metric: "cpu", threshold: 70, warning: f64(70), critical: f64(90),
	})

	seedResults(t, db, checkID, "cpu", 75, 75, 75)
	evaluateAlertRules(db, nil)

	incidents := ruleIncidents(t, db, ruleID)
	if len(incidents) != 1 || incidents[0].Severity != "warning" {
		t.Fatalf("first breach: got %+v, want one warning incident", incidents)
	}

	clearResults(t, db, checkID)
	seedResults(t, db, checkID, "cpu", 95, 95, 95)
	evaluateAlertRules(db, nil)

	incidents = ruleIncidents(t, db, ruleID)
	if len(incidents) != 1 {
		t.Fatalf("after escalation: got %d incidents, want the same one", len(incidents))
	}
	if incidents[0].Severity != "critical" {
		t.Fatalf("got severity %q, want critical", incidents[0].Severity)
	}
}

// A rule with no data in its window has nothing to decide on. Treating the
// absence as a zero would fire every "lt" rule the moment an agent goes quiet.
func TestAlertRuleWithNoDataDoesNotFire(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)

	ruleID := seedRule(t, db, orgID, "cpu floor", alertRule{
		metric: "cpu", operator: "lt", threshold: 10, critical: f64(10),
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 0 {
		t.Fatalf("got %d incidents with no samples, want 0", len(got))
	}
}

// A disabled rule is off. It must not be read at all, whatever the data says.
func TestDisabledAlertRuleIsNotEvaluated(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)
	seedResults(t, db, checkID, "cpu", 99, 99, 99)

	ruleID := seedRule(t, db, orgID, "cpu high", alertRule{metric: "cpu", threshold: 80, critical: f64(80)})
	if _, err := db.Exec(`UPDATE alert_rules SET enabled = false WHERE id = $1`, ruleID); err != nil {
		t.Fatalf("disable rule: %v", err)
	}

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 0 {
		t.Fatalf("got %d incidents from a disabled rule, want 0", len(got))
	}
}

// failure_rate is a percentage over the window, not a count. Two failures out
// of four is 50%, whatever the absolute numbers are.
func TestAlertRuleFailureRateIsAPercentage(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedCheckResults(t, db, checkID, "failure", "failure", "success", "success")

	under := seedRule(t, db, orgID, "under", alertRule{metric: "failure_rate", threshold: 60, critical: f64(60)})
	over := seedRule(t, db, orgID, "over", alertRule{metric: "failure_rate", threshold: 40, critical: f64(40)})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, under); len(got) != 0 {
		t.Fatalf("50%% against a 60%% threshold: got %d incidents, want 0", len(got))
	}
	if got := ruleIncidents(t, db, over); len(got) != 1 {
		t.Fatalf("50%% against a 40%% threshold: got %d incidents, want 1", len(got))
	}
}
