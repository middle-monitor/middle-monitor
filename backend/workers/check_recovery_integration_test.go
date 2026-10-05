package workers

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// openCheckIncident creates the incident a failing check would have opened.
func openCheckIncident(t *testing.T, db *sql.DB, orgID, serviceID int64, status string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO incidents (organization_id, title, description, severity, status, service, service_id)
		VALUES ($1, 'Check failure', 'down', 'critical', $2, 'app', $3) RETURNING id`,
		orgID, status, serviceID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("open check incident: %v", err)
	}
	return id
}

// A check that comes back healthy has to close its own incident. Left open, it
// would both mislead the dashboard and suppress the alert for the next outage,
// because one live incident per check is what the dedup counts.
func TestServiceCheckIncidentResolvesWhenTheCheckRecovers(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)
	incidentID := openCheckIncident(t, db, orgID, checkID, "open")

	autoResolveServiceCheckIncident(db, services.NewAlertService(db), models.Service{
		ID: checkID, OrganizationID: orgID, Name: "api",
	})

	if got := incidentStatus(t, db, incidentID); got != "resolved" {
		t.Fatalf("got status %q, want resolved", got)
	}
}

// Same rule as everywhere else in the engine: acknowledging means a human is on
// it, not that the check recovered. The recovery must still close it.
func TestServiceCheckIncidentResolvesEvenWhenAcknowledged(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)
	incidentID := openCheckIncident(t, db, orgID, checkID, "acknowledged")

	autoResolveServiceCheckIncident(db, services.NewAlertService(db), models.Service{
		ID: checkID, OrganizationID: orgID, Name: "api",
	})

	if got := incidentStatus(t, db, incidentID); got != "resolved" {
		t.Fatalf("got status %q, want resolved: acknowledging must not block recovery", got)
	}
}

// A recovery on one check must not close another check's incident, or one
// service coming back would silence an unrelated outage.
func TestServiceCheckRecoveryOnlyClosesItsOwnIncident(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	recovered := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)
	stillDown := seedCheck(t, db, orgID, "db", "sql", nil, nil, 3)

	recoveredIncident := openCheckIncident(t, db, orgID, recovered, "open")
	otherIncident := openCheckIncident(t, db, orgID, stillDown, "open")

	autoResolveServiceCheckIncident(db, services.NewAlertService(db), models.Service{
		ID: recovered, OrganizationID: orgID, Name: "api",
	})

	if got := incidentStatus(t, db, recoveredIncident); got != "resolved" {
		t.Fatalf("recovered check: got %q, want resolved", got)
	}
	if got := incidentStatus(t, db, otherIncident); got != "open" {
		t.Fatalf("other check: got %q, want open", got)
	}
}

// A check with nothing open is the common case, every healthy cycle. It must be
// a cheap no-op rather than an error path.
func TestServiceCheckRecoveryWithNothingOpenIsANoOp(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	autoResolveServiceCheckIncident(db, services.NewAlertService(db), models.Service{
		ID: checkID, OrganizationID: orgID, Name: "api",
	})

	if got := liveIncidentCount(t, db, checkID); got != 0 {
		t.Fatalf("got %d incidents, want 0", got)
	}
}

// seedCertResult writes a certificate check result whose metadata carries an
// expiry, which is the only place a certificate check records one.
func seedCertResult(t *testing.T, db *sql.DB, serviceID int64, expiresAt time.Time, ageHours int) {
	t.Helper()
	metadata, err := json.Marshal(map[string]string{"expires_at": expiresAt.Format(time.RFC3339)})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO service_results (service_id, status, metadata, timestamp)
		VALUES ($1, 'success', $2, NOW() - ($3 || ' hours')::interval)`,
		serviceID, string(metadata), ageHours)
	if err != nil {
		t.Fatalf("seed cert result: %v", err)
	}
}

// Certificate checks are the one inverted comparison in the engine, and their
// value is not a stored number but a date read out of metadata.
func TestCertificateThresholdAlertsAsExpiryApproaches(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	// Warn at 30 days remaining, page at 7.
	checkID := seedCheck(t, db, orgID, "tls", "certificate", f64(30), f64(7), 3)

	seedCertResult(t, db, checkID, time.Now().Add(3*24*time.Hour), 1)

	evaluateServiceThresholds(db)

	incidents := checkIncidents(t, db, checkID)
	if len(incidents) != 1 || incidents[0].Severity != "critical" {
		t.Fatalf("got %+v, want one critical incident three days before expiry", incidents)
	}
}

// A certificate with plenty of time left is the normal state and must stay quiet.
func TestCertificateThresholdStaysQuietWhenThereIsTimeLeft(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "tls", "certificate", f64(30), f64(7), 3)

	seedCertResult(t, db, checkID, time.Now().Add(200*24*time.Hour), 1)

	evaluateServiceThresholds(db)

	if got := liveIncidentCount(t, db, checkID); got != 0 {
		t.Fatalf("got %d incidents, want 0", got)
	}
}

// Metadata older than a day is not current state: the check may have stopped
// running entirely. Alerting on it would report an expiry nobody re-measured.
func TestCertificateThresholdIgnoresStaleMetadata(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "tls", "certificate", f64(30), f64(7), 3)

	seedCertResult(t, db, checkID, time.Now().Add(3*24*time.Hour), 48)

	evaluateServiceThresholds(db)

	if got := liveIncidentCount(t, db, checkID); got != 0 {
		t.Fatalf("got %d incidents, want 0: a day-old reading is not current state", got)
	}
}

// An already-expired certificate is past zero days, and the message says so
// rather than reporting a negative countdown.
func TestCertificateThresholdReportsAnExpiredCertificate(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "tls", "certificate", f64(30), f64(7), 3)

	seedCertResult(t, db, checkID, time.Now().Add(-5*24*time.Hour), 1)

	evaluateServiceThresholds(db)

	var description string
	err := db.QueryRow(
		`SELECT description FROM incidents WHERE service_id = $1`, checkID).Scan(&description)
	if err != nil {
		t.Fatalf("read incident: %v", err)
	}
	if !strings.Contains(description, "has expired") {
		t.Fatalf("got description %q, want it to say the certificate has expired", description)
	}
}
