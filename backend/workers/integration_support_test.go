package workers

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"middle-monitor/backend/api"
)

// Integration tests run against a real PostgreSQL, because what they check is
// SQL semantics: which rows a WHERE clause selects. A mock replays the rows the
// test itself wrote, so it agrees with whatever the query says and cannot catch
// a filter that silently excludes a status.
//
// They are opt-in on MM_INTEGRATION_DB so `go test ./...` stays fast and needs
// no database. Run them with:
//
//	make test-integration
const integrationEnv = "MM_INTEGRATION_DB"

// integrationDB returns a migrated, empty database, or skips the test when no
// integration database is configured.
func integrationDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv(integrationEnv)
	if dsn == "" {
		t.Skipf("set %s to run integration tests (see make test-integration)", integrationEnv)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", integrationEnv, err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping %s: %v", integrationEnv, err)
	}
	t.Cleanup(func() { db.Close() })

	// RunMigrations opens its own connection from the DB_* vars, so the harness
	// has to agree with it on which database it is talking to.
	if err := api.RunMigrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncateAll(t, db)
	return db
}

// truncateAll empties every table the tests write to, so each test starts from
// a known state without paying for a fresh migration run.
func truncateAll(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		TRUNCATE incidents, service_results, services, hosts, alert_rules,
		         notification_channels, events, application_errors, organizations
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// seedOrg inserts one organization and returns its id.
func seedOrg(t *testing.T, db *sql.DB, slug string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id`, slug, slug,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return id
}

// seedCheck inserts one service check carrying thresholds, and returns its id.
func seedCheck(t *testing.T, db *sql.DB, orgID int64, name, svcType string, warning, critical *float64, maxAttempts int) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO services (organization_id, name, type, host, service, service_interval, max_attempts, warning_threshold, critical_threshold)
		VALUES ($1, $2, $3, 'localhost', 'app', 60, $4, $5, $6) RETURNING id`,
		orgID, name, svcType, maxAttempts, warning, critical,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed check: %v", err)
	}
	return id
}

// seedResults writes samples newest-last, spaced a minute apart so the window
// query orders them the way real samples arrive.
func seedResults(t *testing.T, db *sql.DB, serviceID int64, metricType string, values ...float64) {
	t.Helper()
	for i, v := range values {
		offset := len(values) - 1 - i
		_, err := db.Exec(`
			INSERT INTO service_results (service_id, status, metric_type, metric_value, latency, timestamp)
			VALUES ($1, 'success', $2, $3, $3, NOW() - ($4 || ' seconds')::interval)`,
			serviceID, metricType, v, offset*60)
		if err != nil {
			t.Fatalf("seed result: %v", err)
		}
	}
}

// liveIncidentCount counts the incidents of a check that are not resolved.
func liveIncidentCount(t *testing.T, db *sql.DB, serviceID int64) int {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM incidents WHERE service_id = $1 AND status <> 'resolved'`, serviceID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count incidents: %v", err)
	}
	return n
}

func incidentStatus(t *testing.T, db *sql.DB, incidentID int64) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM incidents WHERE id = $1`, incidentID).Scan(&status); err != nil {
		t.Fatalf("read incident status: %v", err)
	}
	return status
}

// seedHost inserts one host and returns its id.
func seedHost(t *testing.T, db *sql.DB, orgID int64, name string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(
		`INSERT INTO hosts (organization_id, name, host, service) VALUES ($1, $2, $2, 'app') RETURNING id`,
		orgID, name,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return id
}

// alertRule is the subset of an alert rule these tests vary.
type alertRule struct {
	metric      string
	operator    string
	aggregation string
	threshold   float64
	warning     *float64
	critical    *float64
	recovery    *float64
	severity    string
	targetType  string
	targetID    *int64
	duration    int
}

// seedRule inserts one enabled alert rule and returns its id.
func seedRule(t *testing.T, db *sql.DB, orgID int64, name string, r alertRule) int64 {
	t.Helper()
	if r.operator == "" {
		r.operator = "gt"
	}
	if r.aggregation == "" {
		r.aggregation = "avg"
	}
	if r.severity == "" {
		r.severity = "warning"
	}
	if r.targetType == "" {
		r.targetType = "any"
	}
	if r.duration == 0 {
		r.duration = 300
	}
	var id int64
	err := db.QueryRow(`
		INSERT INTO alert_rules (organization_id, name, type, target_type, target_id, metric,
		                         operator, threshold, duration, severity, enabled, aggregation,
		                         warning_threshold, critical_threshold, recovery_threshold)
		VALUES ($1, $2, 'threshold', $3, $4, $5, $6, $7, $8, $9, true, $10, $11, $12, $13)
		RETURNING id`,
		orgID, name, r.targetType, r.targetID, r.metric, r.operator, r.threshold,
		r.duration, r.severity, r.aggregation, r.warning, r.critical, r.recovery,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	return id
}

// ruleIncidents returns the id, status and severity of every incident a rule owns.
func ruleIncidents(t *testing.T, db *sql.DB, ruleID int64) []struct {
	ID       int64
	Status   string
	Severity string
} {
	t.Helper()
	rows, err := db.Query(
		`SELECT id, status, severity FROM incidents WHERE alert_rule_id = $1 ORDER BY id`, ruleID)
	if err != nil {
		t.Fatalf("read rule incidents: %v", err)
	}
	defer rows.Close()

	var out []struct {
		ID       int64
		Status   string
		Severity string
	}
	for rows.Next() {
		var row struct {
			ID       int64
			Status   string
			Severity string
		}
		if err := rows.Scan(&row.ID, &row.Status, &row.Severity); err != nil {
			t.Fatalf("scan rule incident: %v", err)
		}
		out = append(out, row)
	}
	return out
}

// seedCheckOnHost inserts a check attached to a host, for host-scoped rules.
func seedCheckOnHost(t *testing.T, db *sql.DB, orgID, hostID int64, name, svcType string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO services (organization_id, host_id, name, type, host, service, service_interval, max_attempts)
		VALUES ($1, $2, $3, $4, 'localhost', 'app', 60, 3) RETURNING id`,
		orgID, hostID, name, svcType,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed check on host: %v", err)
	}
	return id
}

// seedOldResult writes one sample deliberately outside a rule's window.
func seedOldResult(t *testing.T, db *sql.DB, serviceID int64, metricType string, value float64, age string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO service_results (service_id, status, metric_type, metric_value, latency, timestamp)
		VALUES ($1, 'success', $2, $3, $3, NOW() - $4::interval)`,
		serviceID, metricType, value, age)
	if err != nil {
		t.Fatalf("seed old result: %v", err)
	}
}

// seedCheckResults writes plain check results (no metric_type), newest last, so
// the failure-rate and latency queries see them.
func seedCheckResults(t *testing.T, db *sql.DB, serviceID int64, statuses ...string) {
	t.Helper()
	for i, status := range statuses {
		offset := len(statuses) - 1 - i
		_, err := db.Exec(`
			INSERT INTO service_results (service_id, status, latency, timestamp)
			VALUES ($1, $2, 20, NOW() - ($3 || ' seconds')::interval)`,
			serviceID, status, offset*10)
		if err != nil {
			t.Fatalf("seed check result: %v", err)
		}
	}
}

// clearResults drops every sample of a check, so a test can replace the whole
// window rather than append to it.
func clearResults(t *testing.T, db *sql.DB, serviceID int64) {
	t.Helper()
	if _, err := db.Exec(`DELETE FROM service_results WHERE service_id = $1`, serviceID); err != nil {
		t.Fatalf("clear results: %v", err)
	}
}

// checkIncidents returns the id, status and severity of every incident a check
// owns, in creation order.
func checkIncidents(t *testing.T, db *sql.DB, serviceID int64) []struct {
	ID       int64
	Status   string
	Severity string
} {
	t.Helper()
	rows, err := db.Query(
		`SELECT id, status, severity FROM incidents WHERE service_id = $1 ORDER BY id`, serviceID)
	if err != nil {
		t.Fatalf("read check incidents: %v", err)
	}
	defer rows.Close()

	var out []struct {
		ID       int64
		Status   string
		Severity string
	}
	for rows.Next() {
		var row struct {
			ID       int64
			Status   string
			Severity string
		}
		if err := rows.Scan(&row.ID, &row.Status, &row.Severity); err != nil {
			t.Fatalf("scan check incident: %v", err)
		}
		out = append(out, row)
	}
	return out
}

// seedSlowCheckResults writes plain check results carrying explicit latencies.
func seedSlowCheckResults(t *testing.T, db *sql.DB, serviceID int64, latencies ...float64) {
	t.Helper()
	for i, latency := range latencies {
		offset := len(latencies) - 1 - i
		_, err := db.Exec(`
			INSERT INTO service_results (service_id, status, latency, timestamp)
			VALUES ($1, 'success', $2, NOW() - ($3 || ' seconds')::interval)`,
			serviceID, latency, offset*10)
		if err != nil {
			t.Fatalf("seed slow result: %v", err)
		}
	}
}

// seedAppErrors writes n application errors for an organization, aged by the
// given number of minutes.
func seedAppErrors(t *testing.T, db *sql.DB, orgID int64, n, minutesAgo int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := db.Exec(`
			INSERT INTO application_errors (organization_id, name, message, file, line, environment, service, timestamp)
			VALUES ($1, 'TypeError', 'boom', 'app.go', 12, 'production', 'app', NOW() - ($2 || ' minutes')::interval)`,
			orgID, minutesAgo)
		if err != nil {
			t.Fatalf("seed app error: %v", err)
		}
	}
}
