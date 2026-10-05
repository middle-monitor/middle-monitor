package workers

import (
	"database/sql"
	"testing"
	"time"
)

// Retention deletes paying customers' data on a schedule, so the cost of a
// wrong WHERE clause is data loss that nobody can undo. Every case here is
// about which rows the sweep matches, which is exactly what a mock cannot say.

// setOrgPlan puts an organization on a plan, optionally with a trial deadline
// and a custom retention window.
func setOrgPlan(t *testing.T, db *sql.DB, orgID int64, plan string, trialEndsAt *time.Time, customDays *int) {
	t.Helper()
	_, err := db.Exec(
		`UPDATE organizations SET plan = $2, trial_ends_at = $3, custom_retention_days = $4 WHERE id = $1`,
		orgID, plan, trialEndsAt, customDays)
	if err != nil {
		t.Fatalf("set org plan: %v", err)
	}
}

// seedResultAged writes one check sample a given number of days in the past.
func seedResultAged(t *testing.T, db *sql.DB, serviceID int64, daysAgo int) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO service_results (service_id, status, latency, timestamp)
		VALUES ($1, 'success', 10, NOW() - ($2 || ' days')::interval)`, serviceID, daysAgo)
	if err != nil {
		t.Fatalf("seed aged result: %v", err)
	}
}

func resultCount(t *testing.T, db *sql.DB, serviceID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM service_results WHERE service_id = $1`, serviceID).Scan(&n); err != nil {
		t.Fatalf("count results: %v", err)
	}
	return n
}

// The free plan keeps a week. The boundary is what matters: a sample one day
// inside the window is data the customer still expects to see.
func TestRetentionFreePlanKeepsAWeek(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	setOrgPlan(t, db, orgID, "free", nil, nil)
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedResultAged(t, db, checkID, 1)  // kept
	seedResultAged(t, db, checkID, 6)  // kept
	seedResultAged(t, db, checkID, 10) // purged
	seedResultAged(t, db, checkID, 60) // purged

	runCleanup(db, nil)

	if got := resultCount(t, db, checkID); got != 2 {
		t.Fatalf("got %d results kept, want 2 (7-day window)", got)
	}
}

// Pro keeps a month. Sweeping a Pro org on the free window would delete three
// weeks of a paying customer's history.
func TestRetentionProPlanKeepsAMonth(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	setOrgPlan(t, db, orgID, "pro", nil, nil)
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedResultAged(t, db, checkID, 10) // inside the free window, kept on Pro
	seedResultAged(t, db, checkID, 29) // kept
	seedResultAged(t, db, checkID, 40) // purged

	runCleanup(db, nil)

	if got := resultCount(t, db, checkID); got != 2 {
		t.Fatalf("got %d results kept, want 2 (30-day window)", got)
	}
}

// A trial is card-less and expires passively: the stored plan stays 'free'
// throughout. Reading that stored plan instead of the effective one would purge
// a trialling customer's data at seven days, in the middle of their evaluation.
func TestRetentionRunningTrialRetainsLikePro(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	future := time.Now().Add(7 * 24 * time.Hour)
	setOrgPlan(t, db, orgID, "free", &future, nil)
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedResultAged(t, db, checkID, 20) // outside free, inside pro
	seedResultAged(t, db, checkID, 40) // outside both

	runCleanup(db, nil)

	if got := resultCount(t, db, checkID); got != 1 {
		t.Fatalf("got %d results kept, want 1: a running trial retains like Pro", got)
	}
}

// Once the trial is over the org is back on free, with no write anywhere to say
// so. The sweep has to notice by itself.
func TestRetentionExpiredTrialFallsBackToFree(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	past := time.Now().Add(-24 * time.Hour)
	setOrgPlan(t, db, orgID, "free", &past, nil)
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedResultAged(t, db, checkID, 3)  // kept
	seedResultAged(t, db, checkID, 20) // purged, the trial is over

	runCleanup(db, nil)

	if got := resultCount(t, db, checkID); got != 1 {
		t.Fatalf("got %d results kept, want 1: an expired trial is back on the free window", got)
	}
}

// The custom plan is negotiated per customer, so its window comes from the row
// rather than a constant.
func TestRetentionCustomPlanUsesItsOwnWindow(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	ninety := 90
	setOrgPlan(t, db, orgID, "custom", nil, &ninety)
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedResultAged(t, db, checkID, 60)  // kept
	seedResultAged(t, db, checkID, 120) // purged

	runCleanup(db, nil)

	if got := resultCount(t, db, checkID); got != 1 {
		t.Fatalf("got %d results kept, want 1 (90-day window)", got)
	}
}

// A plan the sweep does not recognise keeps data forever. Defaulting to the
// free window instead would quietly delete an enterprise customer's history.
func TestRetentionUnknownPlanKeepsEverything(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	setOrgPlan(t, db, orgID, "enterprise", nil, nil)
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedResultAged(t, db, checkID, 400)

	runCleanup(db, nil)

	if got := resultCount(t, db, checkID); got != 1 {
		t.Fatalf("got %d results kept, want 1: an unrecognised plan must not be purged", got)
	}
}

// Each organization is paired with its own cutoff through unnest(). If that
// pairing slipped, one tenant's short window would delete another tenant's
// data — the worst outcome this worker can produce.
func TestRetentionAppliesEachOrganizationsOwnCutoff(t *testing.T) {
	db := integrationDB(t)

	freeOrg := seedOrg(t, db, "free-co")
	proOrg := seedOrg(t, db, "pro-co")
	setOrgPlan(t, db, freeOrg, "free", nil, nil)
	setOrgPlan(t, db, proOrg, "pro", nil, nil)

	freeCheck := seedCheck(t, db, freeOrg, "api", "http", nil, nil, 3)
	proCheck := seedCheck(t, db, proOrg, "api", "http", nil, nil, 3)

	// 20 days: outside the free window, inside the Pro one.
	seedResultAged(t, db, freeCheck, 20)
	seedResultAged(t, db, proCheck, 20)

	runCleanup(db, nil)

	if got := resultCount(t, db, freeCheck); got != 0 {
		t.Fatalf("free org: got %d results, want 0", got)
	}
	if got := resultCount(t, db, proCheck); got != 1 {
		t.Fatalf("pro org: got %d results, want 1: the free window must not reach another tenant", got)
	}
}

// The sweep runs daily and must be safe to run again. A second pass over
// already-clean data must not touch what the first pass kept.
func TestRetentionIsIdempotent(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	setOrgPlan(t, db, orgID, "free", nil, nil)
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedResultAged(t, db, checkID, 1)
	seedResultAged(t, db, checkID, 30)

	runCleanup(db, nil)
	first := resultCount(t, db, checkID)
	runCleanup(db, nil)
	second := resultCount(t, db, checkID)

	if first != 1 || second != 1 {
		t.Fatalf("got %d then %d results, want 1 both times", first, second)
	}
}
