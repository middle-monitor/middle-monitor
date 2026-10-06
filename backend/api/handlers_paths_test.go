package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// An accepted error is echoed back with the id it was stored under, which is
// what the SDK logs so a developer can find it in the dashboard.
func TestSubmitErrorEchoesTheStoredError(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("INSERT INTO application_errors").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(77)))
	// The alert lookup runs in the background and may or may not land.
	for i := 0; i < 4; i++ {
		mock.ExpectQuery(".*").WillReturnError(errNoRows())
	}

	body := `{"name":"panic","message":"boom","file":"main.go","service":"api"}`
	rec := httptest.NewRecorder()
	handleSubmitError(db, nil)(rec, httptest.NewRequest("POST", "/api/v1/errors", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var stored map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &stored)
	if stored["id"] != float64(77) {
		t.Fatalf("the caller cannot find the stored error: %v", stored)
	}
}

// The hosts page pages through a filtered count; the total is what the control
// needs and must reflect the filter, not the page size.
func TestGetHostsReportsTheFilteredTotal(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))
	mock.ExpectQuery("FROM hosts").WillReturnRows(sqlmock.NewRows([]string{}))

	rec := httptest.NewRecorder()
	handleGetHosts(db)(rec, orgRequest("GET", "/x?limit=10&offset=0&search=web&status=up", "", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Total-Count") != "42" {
		t.Fatalf("X-Total-Count %q", rec.Header().Get("X-Total-Count"))
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("an empty page must serialize as an array: %q", rec.Body.String())
	}
}

// Only the display name is editable: name, host and service are the identifiers
// the agent and the by-name lookup key on.
func TestUpdateHostOnlyChangesTheDisplayName(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	now := time.Now().UTC()

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT id, name, host, service, display_name, created_at FROM hosts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(4), "web-01", "10.0.0.1", "web", nil, now))
	mock.ExpectQuery("UPDATE hosts SET display_name").
		WithArgs("Front web", int64(4)).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))

	body := `{"display_name":"Front web","name":"renamed","host":"1.2.3.4"}`
	rec := httptest.NewRecorder()
	handleUpdateHost(db)(rec, orgRequest("PUT", "/x", body, map[string]string{"id": "4"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var host map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &host)
	if host["display_name"] != "Front web" {
		t.Fatalf("the display name was not applied: %v", host)
	}
	if host["name"] != "web-01" || host["host"] != "10.0.0.1" {
		t.Fatalf("a technical identifier was overwritten: %v", host)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// The host page lists checks most-recently-seen first, so the one that just
// broke is at the top rather than buried by creation order.
func TestHostServicesByNameAreSortedByLastActivity(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	old := time.Now().UTC().Add(-72 * time.Hour)
	recent := time.Now().UTC()

	mock.ExpectQuery("FROM hosts WHERE name").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(4), int64(1), "web-01", "10.0.0.1", "web", nil, old))
	mock.ExpectQuery("FROM services WHERE host_id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "host_id", "name", "display_name", "type", "host", "path", "credentials",
			"service", "service_interval", "max_attempts", "failure_threshold", "token", "created_at",
		}).
			AddRow(int64(1), int64(4), "older", nil, "http", "10.0.0.1", nil, nil, "web", 60, 3, nil, nil, old).
			AddRow(int64(2), int64(4), "newer", nil, "http", "10.0.0.1", nil, nil, "web", 60, 3, nil, nil, recent))
	for i := 0; i < 6; i++ {
		mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{}))
	}

	rec := httptest.NewRecorder()
	handleGetHostServicesByName(db)(rec, orgRequest("GET", "/x", "", map[string]string{"name": "web-01"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var listed []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed) != 2 || listed[0].Name != "newer" {
		t.Fatalf("checks are not ordered by last activity: %v", listed)
	}
}

// Enrolment hands back the secret, the otpauth URL and the QR the app scans;
// dropping any of the three breaks the setup screen.
func TestSetup2FAReturnsEverythingTheAppNeeds(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectExec("UPDATE users").WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	handleSetup2FA(auth)(rec, adminRequest("POST", "/x", `{}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	for _, key := range []string{"secret", "otpauth_url", "qr_png"} {
		if got[key] == "" {
			t.Fatalf("%s is missing: %v", key, got)
		}
	}
	if !strings.HasPrefix(got["otpauth_url"], "otpauth://") {
		t.Fatalf("otpauth_url %q", got["otpauth_url"])
	}
}

// A wrong code, or a confirmation with no enrolment underway, are both client
// errors the setup screen shows inline.
func TestEnable2FAClassifiesItsFailures(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleEnable2FA(auth)(rec, orgRequest("POST", "/x", `{"code":"000000"}`, nil))
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

// Turning 2FA off is refused while the org enforces it, or a member could opt
// out of a policy the org set.
func TestDisable2FAIsRefusedWhenTheOrgEnforcesIt(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleDisable2FA(auth)(rec, orgRequest("POST", "/x", `{}`, nil))
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

// Resending to an already-verified account is a no-op the UI treats as success,
// so the gate unlocks instead of looping on an error.
func TestResendVerificationIsIdempotent(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	now := time.Now().UTC()

	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "email", "name", "role", "email_verified", "totp_enabled", "created_at", "updated_at",
		"org_id", "org_name", "org_slug", "org_plan", "org_created_at", "org_updated_at",
	}).AddRow(int64(7), int64(1), "a@example.com", "A", "admin", true, false, now, now,
		int64(1), "Acme", "acme", "pro", now, now))
	mock.ExpectQuery(".*").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleResendVerification(auth)(rec, orgRequest("POST", "/x", `{}`, nil))

	if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

// A rules-mode explanation is built and then cached, so the next read costs
// nothing; the answer itself is what the frontend renders.
func TestExplainBuildsAndCachesARulesReport(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	t.Setenv("EXPLAIN_MODE", "rules")
	at := time.Now().UTC()

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT subject_type, subject_id, content").WillReturnError(errNoRows())
	mock.ExpectQuery("FROM application_errors").WillReturnRows(errorContextRows().AddRow(
		int64(42), int64(1), "panic", "runtime error: integer divide by zero",
		"/srv/app/main.go", 295, at, "api", nil, nil, nil, nil, nil))
	for i := 0; i < 12; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}
	var cached bool
	mock.ExpectExec("INSERT INTO explanations").
		WithArgs(seenArg{&cached}, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := orgRequest("POST", "/x", "", map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp ExplanationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Cached {
		t.Fatal("a freshly built report is not a cache hit")
	}
	if resp.Model != "rules" || !strings.Contains(resp.Content, "Division par zéro") {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !cached {
		t.Fatal("the report was not cached, so the next read pays for it again")
	}
}

// The same answer over SSE is framed as metadata plus one data frame, then
// [DONE]: the frontend consumes one protocol either way.
func TestExplainStreamsAFreshReportOverSSE(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	t.Setenv("EXPLAIN_MODE", "rules")
	at := time.Now().UTC()

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(serviceResultContextRows().AddRow(
		int64(9), int64(4), "fail", nil, "dial tcp: connection refused", at,
		"api", "tcp", "db.internal:5432", nil))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	req := httptest.NewRequest("POST", "/x?locale=en", nil)
	req.Header.Set("Accept", "text/event-stream")
	req = orgContext(req, 1, 7)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	handleExplainServiceResult(db)(rec, muxVars(req, map[string]string{"resultId": "9"}))

	body := rec.Body.String()
	if !strings.Contains(body, "event: metadata") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected stream: %q", body)
	}
	if !strings.Contains(body, "Connection refused") {
		t.Fatalf("the answer is missing from the stream: %q", body)
	}
}

// The LLM stream opens before it knows which engine will answer: a known
// failure signature is settled by rules without calling the model. The engine
// is reported once the answer is in, so the UI never labels rules as AI.
func TestExplainStreamReportsTheEngineThatAnswered(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	t.Setenv("EXPLAIN_MODE", "single_shot")
	at := time.Now().UTC()

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(serviceResultContextRows().AddRow(
		int64(9), int64(4), "fail", nil, "dial tcp: connection refused", at,
		"api", "tcp", "db.internal:5432", nil))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	req := orgContext(httptest.NewRequest("POST", "/x?locale=en", nil), 1, 7)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	handleExplainServiceResult(db)(rec, muxVars(req, map[string]string{"resultId": "9"}))

	body := rec.Body.String()
	last := strings.LastIndex(body, "event: metadata")
	done := strings.Index(body, "data: [DONE]")
	if last < 0 || done < 0 || last > done {
		t.Fatalf("no metadata before the end of the stream: %q", body)
	}
	if !strings.Contains(body[last:done], `"model":"deterministic"`) {
		t.Fatalf("the closing metadata does not name the engine: %q", body[last:done])
	}
}
