package workers

import (
	"database/sql"
	"testing"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// seedCommandCheck inserts one command check of a zone and returns its id.
func seedCommandCheck(t *testing.T, db *sql.DB, orgID int64, name, zone string, maxAttempts int) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO services (organization_id, name, type, host, service, service_interval, max_attempts, credentials)
		VALUES ($1, $2, 'command', '10.0.3.12', 'app', 30, $3, $4) RETURNING id`,
		orgID, name, maxAttempts, `{"zone":"`+zone+`","command":"nrpe","vars":{"command":["check_disk"]}}`,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed command check: %v", err)
	}
	return id
}

func storedHardState(t *testing.T, db *sql.DB, serviceID int64) string {
	t.Helper()
	var state string
	err := db.QueryRow(`SELECT hard_state FROM command_check_states WHERE service_id = $1`, serviceID).Scan(&state)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	return state
}

func commandBatch(orgID int64, zone string, at time.Time, results ...models.CommandResult) models.CommandResultBatch {
	for i := range results {
		results[i].ExecutedAt = at
	}
	return models.CommandResultBatch{OrganizationID: orgID, Zone: zone, Hostname: "sat1", Results: results}
}

// A collector only ever sees its own zone's checks. An http check with plain
// text credentials sits in the same table: the zone lookup must not try to
// read it as JSON, or every sync of the organization fails.
func TestCollectorSyncServesOnlyItsZone(t *testing.T) {
	db := integrationDB(t)
	orgA, orgB := seedOrg(t, db, "a"), seedOrg(t, db, "b")
	mine := seedCommandCheck(t, db, orgA, "disk-web1", "paris", 3)
	seedCommandCheck(t, db, orgA, "disk-lyon", "lyon", 3)
	seedCommandCheck(t, db, orgB, "disk-other-org", "paris", 3)
	if _, err := db.Exec(`INSERT INTO services (organization_id, name, type, host, service, credentials) VALUES ($1, 'site', 'http', 'https://x', 'app', 'not json')`, orgA); err != nil {
		t.Fatalf("seed http check: %v", err)
	}

	response, err := services.NewCollectorService(db).Sync(orgA, models.CollectorSync{Zone: "paris", Hostname: "sat1", Commands: []string{"nrpe"}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(response.Checks) != 1 || response.Checks[0].ServiceID != mine {
		t.Fatalf("got %+v, want only check %d", response.Checks, mine)
	}
	if got := response.Checks[0].Vars["host"]; len(got) != 1 || got[0] != "10.0.3.12" {
		t.Fatalf("host var %v, want the check's host", got)
	}
}

// A collector of one zone, or of another organization, must not be able to
// write the state of checks it does not run.
func TestCommandResultsIgnoreOtherZonesAndOrganizations(t *testing.T) {
	db := integrationDB(t)
	orgA, orgB := seedOrg(t, db, "a"), seedOrg(t, db, "b")
	paris := seedCommandCheck(t, db, orgA, "disk-paris", "paris", 1)
	lyon := seedCommandCheck(t, db, orgA, "disk-lyon", "lyon", 1)
	other := seedCommandCheck(t, db, orgB, "disk-b", "paris", 1)

	batch := commandBatch(orgA, "paris", time.Now().UTC(),
		models.CommandResult{ServiceID: paris, State: "ok"},
		models.CommandResult{ServiceID: lyon, State: "critical"},
		models.CommandResult{ServiceID: other, State: "critical"})
	if err := processCommandResults(db, nil, batch); err != nil {
		t.Fatalf("process: %v", err)
	}
	if storedHardState(t, db, paris) != "ok" {
		t.Fatal("the zone's own check was not stored")
	}
	if storedHardState(t, db, lyon) != "" || storedHardState(t, db, other) != "" {
		t.Fatal("a check outside the batch's zone or organization was written")
	}
}

// The full alert cycle: no incident on a soft failure, one on the hard
// failure and none more while it stays down, resolved on recovery, and a
// replayed old result does not reopen anything.
func TestCommandResultsAlertOnHardTransitionsOnly(t *testing.T) {
	db := integrationDB(t)
	org := seedOrg(t, db, "a")
	check := seedCommandCheck(t, db, org, "disk-web1", "paris", 2)
	t0 := time.Now().UTC().Add(-time.Hour)
	step := func(minute int, state string) {
		t.Helper()
		batch := commandBatch(org, "paris", t0.Add(time.Duration(minute)*time.Minute), models.CommandResult{ServiceID: check, State: state, Output: "DISK " + state})
		if err := processCommandResults(db, nil, batch); err != nil {
			t.Fatalf("process minute %d: %v", minute, err)
		}
	}

	step(0, "critical")
	if n := liveIncidentCount(t, db, check); n != 0 {
		t.Fatalf("%d incidents after a soft failure, want 0", n)
	}
	step(1, "critical")
	if n := liveIncidentCount(t, db, check); n != 1 {
		t.Fatalf("%d incidents after the hard failure, want 1", n)
	}
	step(2, "critical")
	if n := liveIncidentCount(t, db, check); n != 1 {
		t.Fatalf("%d incidents while still failing, want still 1", n)
	}
	step(3, "ok")
	if n := liveIncidentCount(t, db, check); n != 0 {
		t.Fatalf("%d live incidents after recovery, want 0", n)
	}
	step(1, "critical")
	if storedHardState(t, db, check) != "ok" || liveIncidentCount(t, db, check) != 0 {
		t.Fatal("a replayed old result changed the state")
	}
}
