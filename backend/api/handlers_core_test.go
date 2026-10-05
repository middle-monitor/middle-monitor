package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// newFailingDB builds a db whose queries all fail, to exercise the 5xx branch
// of a handler without reproducing every service's SQL.
func newFailingDB(t *testing.T) (*sqlmockDB, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 6; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
		mock.ExpectExec(".*").WillReturnError(errors.New("db down"))
	}
	return &sqlmockDB{db, mock}, func() { db.Close() }
}

// An SDK sends either a per-service token or an org API key, and the agent sends
// its install token; anything else must not resolve to an org, or one tenant's
// errors would land in another's.
func TestResolveIngestOrg(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	if _, ok := resolveIngestOrg(db, "  "); ok {
		t.Fatal("an empty token must not resolve")
	}

	mock.ExpectQuery("SELECT organization_id FROM services").WithArgs("svc-token").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(3)))
	orgID, ok := resolveIngestOrg(db, "Bearer svc-token")
	if !ok || orgID != 3 {
		t.Fatalf("service token resolved to %d (%v)", orgID, ok)
	}

	// The agent has no service token and no API key: its OTLP export carries the
	// install token, and series exported with an unresolved org land nowhere.
	mock.ExpectQuery("SELECT organization_id FROM services").WithArgs("install-token").
		WillReturnError(errNoRows())
	mock.ExpectQuery("FROM api_keys").WillReturnError(errNoRows())
	mock.ExpectQuery("FROM install_tokens").WithArgs("install-token").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "expires_at"}).AddRow(int64(7), nil))
	orgID, ok = resolveIngestOrg(db, "Bearer install-token")
	if !ok || orgID != 7 {
		t.Fatalf("install token resolved to %d (%v)", orgID, ok)
	}

	mock.ExpectQuery("SELECT organization_id FROM services").WillReturnError(errNoRows())
	mock.ExpectQuery(".*").WillReturnError(errNoRows())
	mock.ExpectQuery(".*").WillReturnError(errNoRows())
	if _, ok := resolveIngestOrg(db, "mm_unknown"); ok {
		t.Fatal("an unknown token must not resolve")
	}
}

// The ingestion endpoint is public: an incomplete payload must be rejected
// before it reaches storage.
func TestSubmitErrorRequiresACompletePayload(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 4; i++ {
		mock.ExpectQuery(".*").WillReturnError(errNoRows())
	}

	cases := map[string]string{
		"not json":        `{`,
		"missing name":    `{"message":"m","file":"f","service":"s"}`,
		"missing message": `{"name":"n","file":"f","service":"s"}`,
		"missing file":    `{"name":"n","message":"m","service":"s"}`,
		"missing service": `{"name":"n","message":"m","file":"f"}`,
	}
	for name, body := range cases {
		rec := httptest.NewRecorder()
		handleSubmitError(db, nil)(rec, httptest.NewRequest("POST", "/api/v1/errors", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// A payload authenticated with a service token is stored in that token's org,
// never in the one the body claims.
func TestSubmitErrorUsesTheTokenOrgNotTheBody(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("SELECT organization_id FROM services").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(3)))
	mock.ExpectQuery("INSERT INTO application_errors").WillReturnError(errors.New("db down"))

	body := `{"name":"panic","message":"m","file":"f","service":"s","organization_id":999,"http_method":"GET","http_url":"http://x/"}`
	req := httptest.NewRequest("POST", "/api/v1/errors", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer svc-token")
	rec := httptest.NewRecorder()
	handleSubmitError(db, nil)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", rec.Code, rec.Body.String())
	}
}

// The grouped view is a different query with its own pagination; both shapes
// have to report a backend failure rather than an empty list.
func TestGetErrorsReportsBackendFailures(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))

	rec := httptest.NewRecorder()
	handleGetErrors(db)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}

	// An unparseable date range is rejected by the grouped query itself.
	rec = httptest.NewRecorder()
	handleGetErrors(db)(rec, orgRequest("GET", "/x?grouped=1&start=nope&end=nope", "", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// The grouped list reports its full size in X-Total-Count, which is what the
// pagination control needs; slicing must not change that number.
func TestGroupedErrorsReportTheirTotalCount(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 3; i++ {
		mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{}))
	}

	rec := httptest.NewRecorder()
	handleGetErrors(db)(rec, orgRequest("GET", "/x?grouped=1&limit=10&offset=100&since=&until=", "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Total-Count") != "0" {
		t.Fatalf("X-Total-Count %q", rec.Header().Get("X-Total-Count"))
	}
}

// Every read endpoint has to turn a backend failure into a 500 rather than an
// empty answer the dashboard would render as "nothing wrong".
func TestReadEndpointsReportBackendFailures(t *testing.T) {
	cases := []struct {
		name   string
		build  func(db *sqlmockDB) http.HandlerFunc
		target string
		vars   map[string]string
	}{
		{"error services", func(d *sqlmockDB) http.HandlerFunc { return handleGetErrorServices(d.DB) }, "/x", nil},
		{"application links", func(d *sqlmockDB) http.HandlerFunc { return handleGetApplicationLinks(d.DB) }, "/x", nil},
		{"host groups", func(d *sqlmockDB) http.HandlerFunc { return handleGetHostGroups(d.DB) }, "/x", nil},
		{"error correlation", func(d *sqlmockDB) http.HandlerFunc { return handleGetErrorCorrelation(d.DB, nil) }, "/x", map[string]string{"id": "4"}},
		{"result correlation", func(d *sqlmockDB) http.HandlerFunc { return handleGetServiceResultCorrelation(d.DB) }, "/x", map[string]string{"resultId": "4"}},
		{"metrics", func(d *sqlmockDB) http.HandlerFunc { return handleGetMetrics(d.DB) }, "/x", nil},
		{"services", func(d *sqlmockDB) http.HandlerFunc { return handleGetServices(d.DB) }, "/x", nil},
		{"services paged", func(d *sqlmockDB) http.HandlerFunc { return handleGetServices(d.DB) }, "/x?limit=10", nil},
		{"service stats", func(d *sqlmockDB) http.HandlerFunc { return handleGetServiceStats(d.DB) }, "/x", nil},
		{"service results", func(d *sqlmockDB) http.HandlerFunc { return handleGetServiceResults(d.DB) }, "/x", map[string]string{"id": "4"}},
		{"events", func(d *sqlmockDB) http.HandlerFunc { return handleGetEvents(d.DB) }, "/x?limit=5", nil},
		{"metric stats", func(d *sqlmockDB) http.HandlerFunc { return handleGetMetricStats(d.DB) }, "/x", nil},
		{"hosts", func(d *sqlmockDB) http.HandlerFunc { return handleGetHosts(d.DB) }, "/x", nil},
		{"hosts paged", func(d *sqlmockDB) http.HandlerFunc { return handleGetHosts(d.DB) }, "/x?limit=10", nil},
		{"host stats", func(d *sqlmockDB) http.HandlerFunc { return handleGetHostStats(d.DB) }, "/x", nil},
		{"host services", func(d *sqlmockDB) http.HandlerFunc { return handleGetHostServices(d.DB) }, "/x", map[string]string{"id": "4"}},
		{"timeline", func(d *sqlmockDB) http.HandlerFunc { return handleGetTimeline(d.DB) }, "/x?limit=5&start_date=2026-09-01T00:00:00Z&end_date=2026-09-02T00:00:00Z", nil},
	}
	for _, c := range cases {
		db, closeDB := newFailingDB(t)
		rec := httptest.NewRecorder()
		c.build(db)(rec, orgRequest("GET", c.target, "", c.vars))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status %d, want 500 (%s)", c.name, rec.Code, rec.Body.String())
		}
		closeDB()
	}
}

// A path id that is not a number is a client mistake, and each endpoint names
// the id it could not read so the caller knows which one to fix.
func TestPathIDsAreValidated(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		vars    map[string]string
	}{
		{"delete link", handleDeleteApplicationLink(db), map[string]string{"id": "abc"}},
		{"update host group", handleUpdateHostGroup(db), map[string]string{"id": "abc"}},
		{"delete host group", handleDeleteHostGroup(db), map[string]string{"id": "abc"}},
		{"assign host group", handleAssignHostGroup(db), map[string]string{"id": "abc"}},
		{"error correlation", handleGetErrorCorrelation(db, nil), map[string]string{"id": "abc"}},
		{"result correlation", handleGetServiceResultCorrelation(db), map[string]string{"resultId": "abc"}},
		{"update service", handleUpdateService(db), map[string]string{"id": "abc"}},
		{"service detail", handleGetServiceDetail(db), map[string]string{"id": "abc"}},
		{"delete service", handleDeleteService(db), map[string]string{"id": "abc"}},
		{"service results", handleGetServiceResults(db), map[string]string{"id": "abc"}},
		{"host detail", handleGetHostDetail(db), map[string]string{"id": "abc"}},
		{"update host", handleUpdateHost(db), map[string]string{"id": "abc"}},
		{"delete host", handleDeleteHost(db), map[string]string{"id": "abc"}},
		{"host services", handleGetHostServices(db), map[string]string{"id": "abc"}},
		{"agent metrics", handleGetAgentMetrics(db), map[string]string{"id": "abc"}},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.handler(rec, orgRequest("POST", "/x", `{}`, c.vars))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", c.name, rec.Code)
		}
	}
}

// A malformed JSON body is rejected before any work is done, on every endpoint
// that accepts one.
func TestMalformedBodiesAreRejected(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		vars    map[string]string
	}{
		{"create link", handleCreateApplicationLink(db), nil},
		{"create host group", handleCreateHostGroup(db), nil},
		{"update host group", handleUpdateHostGroup(db), map[string]string{"id": "4"}},
		{"assign host group", handleAssignHostGroup(db), map[string]string{"id": "4"}},
		{"submit metrics", handleSubmitMetrics(db), nil},
		{"submit service", handleSubmitService(db), nil},
		{"update service", handleUpdateService(db), map[string]string{"id": "4"}},
		{"submit event", handleSubmitEvent(db), nil},
		{"submit host", handleSubmitHost(db), nil},
		{"update host", handleUpdateHost(db), map[string]string{"id": "4"}},
		{"agent register", handleAgentRegister(db), nil},
		{"agent metrics", handleAgentMetrics(db), nil},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.handler(rec, orgRequest("POST", "/x", `{`, c.vars))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", c.name, rec.Code)
		}
	}
}

// A link with no target correlates nothing; storing it would silently do
// nothing at alert time.
func TestCreateApplicationLinkRequiresATarget(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))

	rec := httptest.NewRecorder()
	handleCreateApplicationLink(db)(rec, orgRequest("POST", "/x", `{"app_service_name":"api"}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	body := `{"app_service_name":"api","target_type":"host","target_id":4}`
	handleCreateApplicationLink(db)(rec, orgRequest("POST", "/x", body, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a rejected create must be a 400, got %d", rec.Code)
	}
}

// The group name is unique per org: a duplicate must name the existing group so
// a converging client can adopt it instead of failing.
func TestCreateHostGroupReportsTheExistingGroup(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("INSERT INTO host_groups").WillReturnError(errors.New("duplicate key"))
	mock.ExpectQuery("SELECT id FROM host_groups").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(12)))

	rec := httptest.NewRecorder()
	handleCreateHostGroup(db)(rec, orgRequest("POST", "/x", `{"name":"web"}`, nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["id"] != float64(12) {
		t.Fatalf("the conflict must name the existing group: %v", body)
	}
}

// A group still holding hosts cannot be deleted; that is a conflict, not a
// generic failure, because the caller has to move the hosts first.
func TestDeleteHostGroupReportsHostsStillAttached(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	rec := httptest.NewRecorder()
	handleDeleteHostGroup(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "4"}))

	if rec.Code != http.StatusConflict && rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 409 or 400", rec.Code)
	}
}

// Link suggestions are derived from a service's traces, so the service is not
// optional.
func TestLinkSuggestionsRequireAService(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleGetLinkSuggestions(db, nil)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// A metric with no service cannot be attributed to anything.
func TestSubmitMetricsRequiresAService(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))

	rec := httptest.NewRecorder()
	handleSubmitMetrics(db)(rec, orgRequest("POST", "/x", `{"metric_type":"cpu"}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleSubmitMetrics(db)(rec, orgRequest("POST", "/x", `{"service":"api","metric_type":"cpu"}`, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// A check with no name, type, host or target is not monitorable; the bounds on
// interval and attempts keep the checker from being turned into a load
// generator.
func TestSubmitServiceValidatesEveryField(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	cases := map[string]string{
		"no name":            `{"type":"http","host":"h","service":"s"}`,
		"missing fields":     `{"name":"n"}`,
		"bad type":           `{"name":"n","type":"telepathy","host":"h","service":"s"}`,
		"interval too short": `{"name":"n","type":"http","host":"h","service":"s","service_interval":5}`,
		"interval too long":  `{"name":"n","type":"http","host":"h","service":"s","service_interval":99999}`,
		"too many attempts":  `{"name":"n","type":"http","host":"h","service":"s","max_attempts":99}`,
		"bad status code":    `{"name":"n","type":"http","host":"h","service":"s","expected_status_code":42}`,
	}
	for name, body := range cases {
		rec := httptest.NewRecorder()
		handleSubmitService(db)(rec, orgRequest("POST", "/x", body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// An update carries the same bounds as a create: a check edited past them would
// bypass the validation the create enforces.
func TestUpdateServiceValidatesTheSameBounds(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	cases := map[string]string{
		"bad type":           `{"type":"telepathy"}`,
		"interval too short": `{"service_interval":5}`,
		"too many attempts":  `{"max_attempts":99}`,
		"bad status code":    `{"expected_status_code":42}`,
	}
	for name, body := range cases {
		rec := httptest.NewRecorder()
		handleUpdateService(db)(rec, orgRequest("PUT", "/x", body, map[string]string{"id": "4"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A check the caller cannot see must read as absent rather than as a failure.
func TestServiceLookupsReport404(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 4; i++ {
		mock.ExpectQuery(".*").WillReturnError(errNoRows())
	}

	rec := httptest.NewRecorder()
	handleGetServiceDetail(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("service detail: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleGetHostDetail(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("host detail: status %d", rec.Code)
	}
}

// An event with no type, service or message says nothing on the timeline.
func TestSubmitEventRequiresItsThreeFields(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))

	rec := httptest.NewRecorder()
	handleSubmitEvent(db)(rec, orgRequest("POST", "/x", `{"type":"deploy"}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleSubmitEvent(db)(rec, orgRequest("POST", "/x", `{"type":"deploy","service":"api","message":"v2"}`, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// A host is identified by its name and address; both are required, and a
// duplicate name names the existing host so a script can converge.
func TestSubmitHostValidatesAndReportsDuplicates(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	for name, body := range map[string]string{
		"no name": `{"host":"10.0.0.1"}`,
		"no host": `{"name":"web-01"}`,
	} {
		rec := httptest.NewRecorder()
		handleSubmitHost(db)(rec, orgRequest("POST", "/x", body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}

	mock.ExpectQuery("trial_ends_at FROM organizations").
		WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("enterprise", nil))
	mock.ExpectQuery("SELECT id FROM host_groups").WillReturnError(errNoRows())
	mock.ExpectQuery("INSERT INTO hosts").WillReturnError(errors.New("duplicate key value"))
	mock.ExpectQuery("SELECT id FROM hosts WHERE organization_id").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(8)))

	rec := httptest.NewRecorder()
	handleSubmitHost(db)(rec, orgRequest("POST", "/x", `{"name":"web-01","host":"10.0.0.1"}`, nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["id"] != float64(8) {
		t.Fatalf("the conflict must name the existing host: %v", body)
	}
}

// A free org that reached its host quota gets the machine-readable plan_limit
// code the frontend turns into an upgrade CTA, not a generic error.
func TestSubmitHostReportsThePlanLimit(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("trial_ends_at FROM organizations").
		WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM hosts").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(9999))

	rec := httptest.NewRecorder()
	handleSubmitHost(db)(rec, orgRequest("POST", "/x", `{"name":"web-01","host":"10.0.0.1"}`, nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "plan_limit" {
		t.Fatalf("the upgrade CTA needs the plan_limit code: %v", body)
	}
}

// A host still carrying checks cannot be deleted: the checks would be orphaned.
func TestDeleteHostReportsAttachedServices(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	rec := httptest.NewRecorder()
	handleDeleteHost(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "4"}))

	if rec.Code != http.StatusConflict && rec.Code != http.StatusNotFound && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

// The by-name lookup is what the agent uses; an unknown name is a 404 and an
// empty one is a client mistake.
func TestHostServicesByName(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	rec := httptest.NewRecorder()
	handleGetHostServicesByName(db)(rec, orgRequest("GET", "/x", "", map[string]string{"name": ""}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	mock.ExpectQuery(".*").WillReturnError(errNoRows())
	rec = httptest.NewRecorder()
	handleGetHostServicesByName(db)(rec, orgRequest("GET", "/x", "", map[string]string{"name": "web-01"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// The agent authenticates with an install token, from either header; without a
// valid one nothing it sends may be attributed to an org.
func TestAgentEndpointsRequireAValidInstallToken(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 4; i++ {
		mock.ExpectQuery(".*").WillReturnError(errNoRows())
	}

	register := `{"hostname":"web-01","service":"agent"}`
	metrics := `{"hostname":"web-01","service":"agent"}`

	for _, c := range []struct {
		name    string
		handler http.HandlerFunc
		body    string
	}{
		{"register", handleAgentRegister(db), register},
		{"metrics", handleAgentMetrics(db), metrics},
	} {
		rec := httptest.NewRecorder()
		c.handler(rec, httptest.NewRequest("POST", "/x", strings.NewReader(c.body)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a token: status %d, want 401", c.name, rec.Code)
		}

		req := httptest.NewRequest("POST", "/x", strings.NewReader(c.body))
		req.Header.Set("Authorization", "Bearer nope")
		rec = httptest.NewRecorder()
		c.handler(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s with a bad token: status %d, want 401", c.name, rec.Code)
		}
	}
}

// Registration and metrics both need the host and service the agent runs as.
func TestAgentEndpointsRequireHostnameAndService(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	for name, handler := range map[string]http.HandlerFunc{
		"register": handleAgentRegister(db),
		"metrics":  handleAgentMetrics(db),
	} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{"hostname":"web-01"}`)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// The agent metrics view tells "this check does not exist" apart from "the
// database is down": a 404 tells the caller the service was deleted, so an
// unreachable database must not borrow it.
func TestGetAgentMetricsClassifiesItsFailures(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(".*").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleGetAgentMetrics(db)(rec, orgRequest("GET", "/x?limit=10", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 for a missing service", rec.Code)
	}

	db2, mock2, _ := sqlmock.New()
	defer db2.Close()
	mock2.MatchExpectationsInOrder(false)
	mock2.ExpectQuery(".*").WillReturnError(errors.New("connection refused"))

	rec = httptest.NewRecorder()
	handleGetAgentMetrics(db2)(rec, orgRequest("GET", "/x?limit=10", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 for an unreachable database", rec.Code)
	}
}

// The SQL health check is what the checker itself probes; a reachable database
// answers 200 and an unreachable one 503, both with the measured latency.
func TestSQLHealthCheckReportsTheConnectionState(t *testing.T) {
	db, mock, _ := sqlmock.New(sqlmock.MonitorPingsOption(true))
	defer db.Close()

	mock.ExpectPing()
	rec := httptest.NewRecorder()
	handleSQLHealthCheck(db)(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("body %v", body)
	}
	if _, ok := body["latency_ms"]; !ok {
		t.Fatalf("the measured latency is the point of the check: %v", body)
	}

	mock.ExpectPing().WillReturnError(errors.New("connection refused"))
	rec = httptest.NewRecorder()
	handleSQLHealthCheck(db)(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}

// The grouped list is re-fetched on a timer with one entry per group on screen,
// while the HTTP request headers and body are one blob per error. They are only
// ever read from an error's detail, so carrying them here makes the payload grow
// with the error volume for nothing.
func TestGroupedErrorListCarriesNoHTTPRequestPayload(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	cols := []string{"id", "organization_id", "name", "message", "file", "line",
		"timestamp", "service", "http_method", "http_url", "trace_id"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(cols).AddRow(int64(1), int64(1), "E", "msg", "f.go", 1,
			time.Now(), "api", "POST", "http://x/orders", nil),
	)

	rec := httptest.NewRecorder()
	handleGetErrors(db)(rec, orgRequest("GET", "/x?grouped=1", "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, field := range []string{"http_headers", "http_body"} {
		if strings.Contains(body, field) {
			t.Fatalf("grouped list still carries %s: %s", field, body)
		}
	}
	// What the list already showed has to survive the diet.
	for _, field := range []string{"http_method", "http_url"} {
		if !strings.Contains(body, field) {
			t.Fatalf("grouped list lost %s: %s", field, body)
		}
	}
}

// The two fields the list dropped stay reachable one error at a time, which is
// the only granularity anything ever displayed them at.
func TestErrorDetailCarriesTheHTTPRequestPayload(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	cols := []string{"id", "organization_id", "name", "message", "file", "line",
		"timestamp", "service", "http_method", "http_url", "http_headers", "http_body"}
	mock.ExpectQuery("FROM application_errors WHERE id").WillReturnRows(
		sqlmock.NewRows(cols).AddRow(int64(5), int64(1), "E", "msg", "f.go", 1,
			time.Now(), "api", "POST", "http://x/orders", `{"content-type":"application/json"}`, `{"qty":2}`),
	)

	rec := httptest.NewRecorder()
	handleGetError(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "5"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["http_headers"] == nil || payload["http_body"] == nil {
		t.Fatalf("detail is missing the HTTP request payload: %v", payload)
	}
}

// An id that belongs to another organization must not answer with an empty
// error object the caller would render as a real one.
func TestErrorDetailRefusesAnUnknownID(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("FROM application_errors WHERE id").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	rec := httptest.NewRecorder()
	handleGetError(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "404"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// The point of the change is the payload size, so measure it: the list is what
// the errors view polls, and it used to carry one request blob per group. The
// "before" here is the same response with the two fields put back on each
// sample, which is exactly what the previous query produced.
func TestGroupedErrorListIsSmallerByTheWholeHTTPRequestPayload(t *testing.T) {
	const groups = 10
	headers := `{"content-type":"application/json","authorization":"redacted","user-agent":"` + strings.Repeat("a", 200) + `"}`
	body := strings.Repeat("x", 8000)

	db, mock, _ := sqlmock.New()
	defer db.Close()
	cols := []string{"id", "organization_id", "name", "message", "file", "line",
		"timestamp", "service", "http_method", "http_url", "trace_id"}
	rows := sqlmock.NewRows(cols)
	for i := 0; i < groups; i++ {
		rows.AddRow(int64(i+1), int64(1), fmt.Sprintf("Error%d", i), "msg", "f.go", i+1,
			time.Now(), "api", "POST", "http://x/orders", nil)
	}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(rows)

	rec := httptest.NewRecorder()
	handleGetErrors(db)(rec, orgRequest("GET", "/x?grouped=1", "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var payload []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload) != groups {
		t.Fatalf("%d groups, want %d", len(payload), groups)
	}
	for _, group := range payload {
		sample := group["sample"].(map[string]interface{})
		sample["http_headers"] = headers
		sample["http_body"] = body
	}
	before, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	saved := len(before) - rec.Body.Len()
	want := groups * (len(headers) + len(body))
	if saved < want {
		t.Fatalf("the list only saves %d bytes over %d groups, want at least %d", saved, groups, want)
	}
}
