package workers

import (
	"testing"

	"middle-monitor/backend/services"
)

// Acknowledging an incident says a human picked it up, not that the condition
// cleared. If the dedup query only counts 'open', an acknowledged incident stops
// deduping and the evaluator opens a fresh one on its very next pass — one new
// incident per cycle, forever, for as long as somebody is working on the first.
//
// This is the regression that made the dedup and auto-resolve queries share a
// single definition of a live incident.
func TestAcknowledgedIncidentStillDedupsNewAlerts(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", f64(70), f64(90), 3)

	// Three samples all over critical: a full breaching window.
	seedResults(t, db, checkID, "cpu", 95, 96, 97)

	evaluateServiceThresholds(db)
	if got := liveIncidentCount(t, db, checkID); got != 1 {
		t.Fatalf("first pass: got %d live incidents, want 1", got)
	}

	var incidentID int64
	if err := db.QueryRow(`SELECT id FROM incidents WHERE service_id = $1`, checkID).Scan(&incidentID); err != nil {
		t.Fatalf("read incident: %v", err)
	}

	// A responder acknowledges it while the CPU is still pinned.
	if _, err := db.Exec(`UPDATE incidents SET status = 'acknowledged' WHERE id = $1`, incidentID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	// Two more evaluation cycles with the condition unchanged.
	evaluateServiceThresholds(db)
	evaluateServiceThresholds(db)

	if got := liveIncidentCount(t, db, checkID); got != 1 {
		t.Fatalf("after acknowledging, got %d live incidents, want 1: acknowledging must not un-dedup the alert", got)
	}
}

// The mirror of the dedup case: once the value comes back inside its
// thresholds, an acknowledged incident has to close like an open one. Left out
// of the auto-resolve filter it would stay live forever and keep suppressing
// every later alert for that check.
func TestAcknowledgedIncidentStillAutoResolves(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", f64(70), f64(90), 3)

	seedResults(t, db, checkID, "cpu", 95, 96, 97)
	evaluateServiceThresholds(db)

	var incidentID int64
	if err := db.QueryRow(`SELECT id FROM incidents WHERE service_id = $1`, checkID).Scan(&incidentID); err != nil {
		t.Fatalf("read incident: %v", err)
	}
	if _, err := db.Exec(`UPDATE incidents SET status = 'acknowledged' WHERE id = $1`, incidentID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	// The CPU comes back down.
	if _, err := db.Exec(`DELETE FROM service_results WHERE service_id = $1`, checkID); err != nil {
		t.Fatalf("clear results: %v", err)
	}
	seedResults(t, db, checkID, "cpu", 10, 11, 12)

	evaluateServiceThresholds(db)

	if got := incidentStatus(t, db, incidentID); got != "resolved" {
		t.Fatalf("got status %q, want resolved: an acknowledged incident must still auto-resolve", got)
	}
}

// A partial window is not evidence. Until max_attempts samples exist, the
// evaluator has nothing to smooth a spike against, so it must not fire.
func TestThresholdNeedsAFullWindowBeforeFiring(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", f64(70), f64(90), 3)

	seedResults(t, db, checkID, "cpu", 95, 96) // two samples, window is three

	evaluateServiceThresholds(db)

	if got := liveIncidentCount(t, db, checkID); got != 0 {
		t.Fatalf("got %d incidents on a partial window, want 0", got)
	}
}

// One spike inside an otherwise healthy window is what the window exists to
// absorb. This is the same rule as the unit test, checked end to end through
// the SQL that selects the samples.
func TestThresholdSmoothsASingleSpike(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "cpu", "agent_cpu", f64(70), f64(90), 3)

	seedResults(t, db, checkID, "cpu", 10, 11, 99) // newest is the spike

	evaluateServiceThresholds(db)

	if got := liveIncidentCount(t, db, checkID); got != 0 {
		t.Fatalf("got %d incidents for a single spike, want 0", got)
	}
}

// LiveIncidentStatuses is what the dedup and auto-resolve queries interpolate.
// If it ever stops being valid SQL, every one of those queries breaks at once,
// so it is worth one direct check against the real parser.
func TestLiveIncidentStatusesIsValidSQL(t *testing.T) {
	db := integrationDB(t)

	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM incidents WHERE status IN ` + services.LiveIncidentStatuses).Scan(&n)
	if err != nil {
		t.Fatalf("LiveIncidentStatuses is not usable in a WHERE clause: %v", err)
	}
}
