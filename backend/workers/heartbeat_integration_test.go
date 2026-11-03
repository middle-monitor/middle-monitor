package workers

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"middle-monitor/backend/services"
)

// seedHeartbeat inserts a heartbeat expecting a ping every period, with the
// given grace, and returns its id and ping token.
func seedHeartbeat(t *testing.T, db *sql.DB, orgID int64, name string, period, grace int) (int64, string) {
	t.Helper()
	token := name + "-token"
	var id int64
	err := db.QueryRow(`
		INSERT INTO services (organization_id, name, type, host, service, service_interval, max_attempts, credentials, token)
		VALUES ($1, $2, 'heartbeat', '', 'jobs', $3, 3, $4, $5) RETURNING id`,
		orgID, name, period, `{"grace_seconds":`+strconv.Itoa(grace)+`}`, token,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed heartbeat: %v", err)
	}
	return id, token
}

// backdateResults moves every result of a check into the past, as if the last
// ping had been sent that long ago.
func backdateResults(t *testing.T, db *sql.DB, serviceID int64, ago time.Duration) {
	t.Helper()
	if _, err := db.Exec(`UPDATE service_results SET timestamp = timestamp - ($2 || ' seconds')::interval WHERE service_id = $1`,
		serviceID, int(ago.Seconds())); err != nil {
		t.Fatalf("backdate results: %v", err)
	}
}

func failureCount(t *testing.T, db *sql.DB, serviceID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM service_results WHERE service_id = $1 AND status = 'failure'`, serviceID).Scan(&n); err != nil {
		t.Fatalf("count results: %v", err)
	}
	return n
}

// The whole point of a heartbeat: a job that stops pinging past its period plus
// grace opens an incident, and the next ping closes it.
func TestHeartbeatMissedPingOpensAnIncidentAndAPingResolvesIt(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	id, token := seedHeartbeat(t, db, orgID, "backup", 3600, 300)

	if err := services.RecordHeartbeatPing(db, token, false, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	evaluateHeartbeats(db, now.Add(-time.Minute), now)
	if n := liveIncidentCount(t, db, id); n != 0 {
		t.Fatalf("a job on time opened %d incident(s)", n)
	}

	backdateResults(t, db, id, 3600*time.Second+301*time.Second)
	evaluateHeartbeats(db, now.Add(-time.Minute), now)
	if n := liveIncidentCount(t, db, id); n != 1 {
		t.Fatalf("missed ping: %d live incident(s), want 1", n)
	}

	if err := services.RecordHeartbeatPing(db, token, false, ""); err != nil {
		t.Fatal(err)
	}
	evaluateHeartbeats(db, now, time.Now().UTC())
	if n := liveIncidentCount(t, db, id); n != 0 {
		t.Fatalf("the job pinged again but %d incident(s) stay open", n)
	}
}

// Within the grace the job is late, not down: a nightly backup that runs a few
// minutes longer than usual must not page anyone.
func TestHeartbeatInsideTheGraceDoesNotAlert(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	id, token := seedHeartbeat(t, db, orgID, "backup", 3600, 600)

	if err := services.RecordHeartbeatPing(db, token, false, ""); err != nil {
		t.Fatal(err)
	}
	backdateResults(t, db, id, 3600*time.Second+300*time.Second)
	now := time.Now().UTC()
	evaluateHeartbeats(db, now.Add(-time.Minute), now)

	if n := liveIncidentCount(t, db, id); n != 0 {
		t.Fatalf("a ping inside the grace opened %d incident(s)", n)
	}
	if n := failureCount(t, db, id); n != 0 {
		t.Fatalf("a ping inside the grace recorded %d failure(s)", n)
	}
}

// A heartbeat that never pinged is one nobody wired yet. Paging on it would
// punish creating the check before deploying the cron line.
func TestHeartbeatThatNeverPingedStaysQuiet(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	id, _ := seedHeartbeat(t, db, orgID, "backup", 60, 0)

	now := time.Now().UTC().Add(24 * time.Hour)
	evaluateHeartbeats(db, now.Add(-time.Minute), now)

	if n := liveIncidentCount(t, db, id); n != 0 {
		t.Fatalf("a heartbeat without any ping opened %d incident(s)", n)
	}
}

// The job knows better than the clock: its own fail ping alerts right away,
// without waiting for the period to run out.
func TestHeartbeatFailPingOpensAnIncident(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	id, token := seedHeartbeat(t, db, orgID, "backup", 86400, 300)

	if err := services.RecordHeartbeatPing(db, token, true, "pg_dump: connection refused"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	evaluateHeartbeats(db, now.Add(-time.Minute), now)

	if n := liveIncidentCount(t, db, id); n != 1 {
		t.Fatalf("a fail ping: %d live incident(s), want 1", n)
	}
	var description string
	if err := db.QueryRow(`SELECT description FROM incidents WHERE service_id = $1`, id).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(description, "pg_dump: connection refused") {
		t.Fatalf("the job output is missing from the incident: %q", description)
	}
}

// Silence is recorded once per missed period, not once per evaluation pass:
// otherwise a daily job would log 1440 failures a day and the uptime would
// read as an outage of the whole day for one missed run.
func TestHeartbeatRecordsOneFailurePerMissedPeriod(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	id, token := seedHeartbeat(t, db, orgID, "backup", 3600, 0)

	if err := services.RecordHeartbeatPing(db, token, false, ""); err != nil {
		t.Fatal(err)
	}
	backdateResults(t, db, id, 2*time.Hour)
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		tick := now.Add(time.Duration(i) * time.Minute)
		evaluateHeartbeats(db, tick.Add(-time.Minute), tick)
	}

	if n := failureCount(t, db, id); n != 1 {
		t.Fatalf("%d failures recorded over five passes, want 1", n)
	}
}
