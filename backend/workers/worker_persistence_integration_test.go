package workers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"middle-monitor/backend/models"
)

// A latency rule reads the check results, not the agent metrics, so it must see
// plain samples and ignore the metric rows sharing the same table.
func TestAlertRuleLatencyReadsCheckResultsOnly(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")

	apiCheck := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)
	cpuCheck := seedCheck(t, db, orgID, "cpu", "agent_cpu", nil, nil, 3)

	// Agent samples carry a latency column too; a latency rule must not count
	// them as response times.
	seedResults(t, db, cpuCheck, "cpu", 5000, 5000, 5000)
	seedCheckResults(t, db, apiCheck, "success", "success")

	ruleID := seedRule(t, db, orgID, "slow api", alertRule{
		metric: "latency", aggregation: "max", threshold: 1000, critical: f64(1000),
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 0 {
		t.Fatalf("got %d incidents, want 0: agent samples are not response times", len(got))
	}
}

// The same rule has to fire when the check results really are slow.
func TestAlertRuleLatencyFiresOnSlowChecks(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	seedSlowCheckResults(t, db, checkID, 2000, 2500)

	ruleID := seedRule(t, db, orgID, "slow api", alertRule{
		metric: "latency", aggregation: "max", threshold: 1000, critical: f64(1000),
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 1 {
		t.Fatalf("got %d incidents, want 1", len(got))
	}
}

// An error-count rule counts rows in the window and stays inside its own
// organization, like every other read in the engine.
func TestAlertRuleErrorCountIsScopedAndWindowed(t *testing.T) {
	db := integrationDB(t)
	quiet := seedOrg(t, db, "quiet")
	noisy := seedOrg(t, db, "noisy")

	seedAppErrors(t, db, noisy, 5, 0)   // another tenant, recent
	seedAppErrors(t, db, quiet, 5, 240) // this tenant, four hours old

	ruleID := seedRule(t, db, quiet, "too many errors", alertRule{
		metric: "error_count", threshold: 3, critical: f64(3), duration: 300,
	})

	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 0 {
		t.Fatalf("got %d incidents, want 0: neither another tenant nor old rows count", len(got))
	}

	seedAppErrors(t, db, quiet, 5, 0)
	evaluateAlertRules(db, nil)

	if got := ruleIncidents(t, db, ruleID); len(got) != 1 {
		t.Fatalf("got %d incidents after recent errors, want 1", len(got))
	}
}

// A check result is the product's raw material. Metadata is optional and takes
// a different statement, so both paths are worth a round trip.
func TestSaveServiceResultPersistsBothShapes(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")
	checkID := seedCheck(t, db, orgID, "api", "http", nil, nil, 3)

	latency := 42.5
	message := "ok"
	if err := saveServiceResult(db, models.ServiceResult{
		ServiceID: checkID, Status: "success", Latency: &latency,
		Message: &message, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save without metadata: %v", err)
	}

	metadata, err := json.Marshal(map[string]any{"expires_at": "2027-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	metadataStr := string(metadata)
	if err := saveServiceResult(db, models.ServiceResult{
		ServiceID: checkID, Status: "failure", Timestamp: time.Now().UTC(),
		Metadata: &metadataStr,
	}); err != nil {
		t.Fatalf("save with metadata: %v", err)
	}

	var withMetadata int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM service_results WHERE service_id = $1 AND metadata IS NOT NULL`, checkID,
	).Scan(&withMetadata); err != nil {
		t.Fatalf("count: %v", err)
	}
	if got := resultCount(t, db, checkID); got != 2 || withMetadata != 1 {
		t.Fatalf("got %d results (%d with metadata), want 2 and 1", got, withMetadata)
	}
}

// A result whose service no longer exists must be rejected rather than stored
// as an orphan, and the caller has to hear about it.
func TestSaveServiceResultRejectsAnUnknownService(t *testing.T) {
	db := integrationDB(t)

	err := saveServiceResult(db, models.ServiceResult{
		ServiceID: 999999, Status: "success", Timestamp: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected an error for a result pointing at no service")
	}
}

// seedWebhookChannel registers an enabled webhook channel pointing at a test
// server, with the given heartbeat interval in seconds.
func seedWebhookChannel(t *testing.T, db *sql.DB, orgID int64, name, url string, heartbeatSeconds int) int64 {
	t.Helper()
	config, err := json.Marshal(map[string]any{
		"webhook_url":        url,
		"heartbeat_interval": heartbeatSeconds,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var id int64
	err = db.QueryRow(`
		INSERT INTO notification_channels (organization_id, name, type, config, enabled)
		VALUES ($1, $2, 'webhook', $3, true) RETURNING id`,
		orgID, name, config,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed webhook channel: %v", err)
	}
	return id
}

// The heartbeat proves the notification path is alive. It has to fire once on
// the first tick after a restart, then respect its interval: a receiver that
// alerts on a missing heartbeat would page on every deploy otherwise.
func TestWebhookHeartbeatFiresOnceThenRespectsItsInterval(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")

	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	seedWebhookChannel(t, db, orgID, "ops", server.URL, 300)

	lastSent := map[int64]time.Time{}
	now := time.Now()

	if sent := sendDueHeartbeats(db, lastSent, now); sent != 1 {
		t.Fatalf("first tick: got %d heartbeats, want 1", sent)
	}
	// One minute later, well inside a five-minute interval.
	if sent := sendDueHeartbeats(db, lastSent, now.Add(time.Minute)); sent != 0 {
		t.Fatalf("inside the interval: got %d heartbeats, want 0", sent)
	}
	// Past the interval.
	if sent := sendDueHeartbeats(db, lastSent, now.Add(6*time.Minute)); sent != 1 {
		t.Fatalf("past the interval: got %d heartbeats, want 1", sent)
	}

	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("receiver got %d calls, want 2", got)
	}
}

// A channel without a heartbeat configured never sends one, and a disabled
// channel is off entirely.
func TestWebhookHeartbeatSkipsChannelsThatDidNotAskForOne(t *testing.T) {
	db := integrationDB(t)
	orgID := seedOrg(t, db, "acme")

	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
	}))
	defer server.Close()

	seedWebhookChannel(t, db, orgID, "no-heartbeat", server.URL, 0)
	disabled := seedWebhookChannel(t, db, orgID, "disabled", server.URL, 300)
	if _, err := db.Exec(`UPDATE notification_channels SET enabled = false WHERE id = $1`, disabled); err != nil {
		t.Fatalf("disable channel: %v", err)
	}

	if sent := sendDueHeartbeats(db, map[int64]time.Time{}, time.Now()); sent != 0 {
		t.Fatalf("got %d heartbeats, want 0", sent)
	}
	if got := atomic.LoadInt64(&hits); got != 0 {
		t.Fatalf("receiver got %d calls, want 0", got)
	}
}
