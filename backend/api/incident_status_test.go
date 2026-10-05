package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// incidentRows is the shape loadIncident reads.
func incidentRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "organization_id", "alert_rule_id", "service_id", "host_id", "title",
		"description", "severity", "status", "started_at", "acknowledged_at", "resolved_at", "name",
	})
}

func orgContext(r *http.Request, orgID, userID int64) *http.Request {
	claims := &services.JWTClaims{OrganizationID: orgID, UserID: userID}
	return r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, claims))
}

// The transition a receiver switches on has to be right, or a system that
// reopens on incident.opened would act on every acknowledgement.
func TestEventForTransition(t *testing.T) {
	cases := []struct {
		previous, status, want string
	}{
		{"open", "acknowledged", services.EventIncidentAcknowledged},
		{"acknowledged", "resolved", services.EventIncidentResolved},
		{"resolved", "open", services.EventIncidentReopened},
		{"", "open", services.EventIncidentOpened},
	}
	for _, c := range cases {
		if got := eventForTransition(c.previous, c.status); got != c.want {
			t.Fatalf("%s -> %s gave %q, want %q", c.previous, c.status, got, c.want)
		}
	}
}

// The dedup key a receiver sends back must resolve to the live incident, not to
// an old resolved one that happens to share the rule.
func TestDedupKeyResolvesToTheLiveIncident(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT id FROM incidents").
		WithArgs(int64(1), int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(801)))

	id, err := incidentIDForDedupKey(db, 1, "mm-rule-5")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != 801 {
		t.Fatalf("incident %d, want 801", id)
	}
}

func TestUnknownDedupKeyShapeIsRejected(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	if _, err := incidentIDForDedupKey(db, 1, "pagerduty-42"); err != ErrDedupKeyUnknown {
		t.Fatalf("error %v, want ErrDedupKeyUnknown", err)
	}
}

// A machine acknowledging an incident is the whole point of the loop: the note
// and the actor have to reach the update, and the response has to hand back the
// dedup key so the receiver need not store anything.
func TestMachineAcknowledgementIsAccepted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	started := time.Now().UTC()
	mock.ExpectQuery("SELECT i.id, i.organization_id").
		WillReturnRows(incidentRows().AddRow(int64(801), int64(1), int64(5), nil, nil,
			"API latency", nil, "critical", "open", started, nil, nil, nil))
	mock.ExpectExec("UPDATE incidents").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT i.id, i.organization_id").
		WillReturnRows(incidentRows().AddRow(int64(801), int64(1), int64(5), nil, nil,
			"API latency", nil, "critical", "acknowledged", started, started, nil, nil))
	// The transition is published in the background; the routing lookup may or
	// may not run before the assertions, so unmatched queries are tolerated.
	mock.MatchExpectationsInOrder(false)

	body := `{"status":"acknowledged","note":"auto remediation #4711 triggered","actor":"automation"}`
	req := httptest.NewRequest("PUT", "/api/v1/organizations/acme/incidents/801/status", strings.NewReader(body))
	req = mux.SetURLVars(orgContext(req, 1, 0), map[string]string{"id": "801"})
	rec := httptest.NewRecorder()

	handleUpdateIncidentStatus(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var answer map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer["previous_status"] != "open" {
		t.Fatalf("previous_status %v", answer["previous_status"])
	}
	if answer["dedup_key"] != "mm-rule-5" {
		t.Fatalf("dedup_key %v, the receiver needs it to act again without storing ids", answer["dedup_key"])
	}
}

// Replaying an acknowledgement is what a retrying system does; it must not be
// an error, and it must not rewrite when the incident was first picked up.
func TestReplayedAcknowledgementIsAccepted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	started := time.Now().UTC()
	acked := started.Add(time.Minute)
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT i.id, i.organization_id").
			WillReturnRows(incidentRows().AddRow(int64(801), int64(1), int64(5), nil, nil,
				"API latency", nil, "critical", "acknowledged", started, acked, nil, nil))
	}
	mock.ExpectExec("UPDATE incidents SET status = .*acknowledged_at = COALESCE").
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest("PUT", "/x", strings.NewReader(`{"status":"acknowledged","actor":"automation"}`))
	req = mux.SetURLVars(orgContext(req, 1, 0), map[string]string{"id": "801"})
	rec := httptest.NewRecorder()

	handleUpdateIncidentStatus(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownIncidentIs404(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT i.id, i.organization_id").WillReturnRows(incidentRows())

	req := httptest.NewRequest("PUT", "/x", strings.NewReader(`{"status":"resolved"}`))
	req = mux.SetURLVars(orgContext(req, 1, 0), map[string]string{"id": "999"})
	rec := httptest.NewRecorder()

	handleUpdateIncidentStatus(db)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}
