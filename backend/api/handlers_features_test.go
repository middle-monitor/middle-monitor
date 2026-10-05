package api

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// Every list endpoint has to report a backend failure rather than answer with
// an empty list the UI would render as "nothing configured".
func TestFeatureListEndpointsReportBackendFailures(t *testing.T) {
	cases := []struct {
		name   string
		build  func(*sqlmockDB) http.HandlerFunc
		target string
		vars   map[string]string
	}{
		{"channels", func(d *sqlmockDB) http.HandlerFunc { return handleGetNotificationChannels(d.DB) }, "/x", nil},
		{"incidents", func(d *sqlmockDB) http.HandlerFunc { return handleGetIncidents(d.DB) }, "/x", nil},
		{"incident stats", func(d *sqlmockDB) http.HandlerFunc { return handleGetIncidentStats(d.DB) }, "/x", nil},
		{"alert rules", func(d *sqlmockDB) http.HandlerFunc { return handleGetAlertRules(d.DB) }, "/x", nil},
		{"api keys", func(d *sqlmockDB) http.HandlerFunc { return handleGetAPIKeys(d.DB) }, "/x", nil},
		{"install tokens", func(d *sqlmockDB) http.HandlerFunc { return handleGetInstallTokens(d.DB) }, "/x", nil},
		{"network metrics", func(d *sqlmockDB) http.HandlerFunc { return handleGetNetworkMetrics(d.DB) }, "/x", nil},
		{"metrics explorer", func(d *sqlmockDB) http.HandlerFunc { return handleGetMetricsExplorer(d.DB) }, "/x", nil},
		{"profile series", func(d *sqlmockDB) http.HandlerFunc { return handleGetProfileSeries(d.DB) }, "/x?limit=10&from=2026-09-01T00:00:00Z&to=2026-09-02T00:00:00Z", nil},
		{"profiles", func(d *sqlmockDB) http.HandlerFunc { return handleListProfiles(d.DB) }, "/x", nil},
		{"test channel", func(d *sqlmockDB) http.HandlerFunc { return handleTestNotificationChannel(d.DB) }, "/x", map[string]string{"id": "4"}},
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

// Every write endpoint has to report a backend failure too, rather than answer
// as if the write had happened.
func TestFeatureWriteEndpointsReportBackendFailures(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	cases := []struct {
		name  string
		build func(*sqlmockDB) http.HandlerFunc
		body  string
		vars  map[string]string
	}{
		{"create channel", func(d *sqlmockDB) http.HandlerFunc { return handleCreateNotificationChannel(d.DB) }, `{"name":"ops","type":"slack"}`, nil},
		{"update channel", func(d *sqlmockDB) http.HandlerFunc { return handleUpdateNotificationChannel(d.DB) }, `{"name":"ops","type":"slack"}`, map[string]string{"id": "4"}},
		{"delete channel", func(d *sqlmockDB) http.HandlerFunc { return handleDeleteNotificationChannel(d.DB) }, "", map[string]string{"id": "4"}},
		{"create incident", func(d *sqlmockDB) http.HandlerFunc { return handleCreateIncident(d.DB) }, `{"title":"down","service_id":4}`, nil},
		{"create rule", func(d *sqlmockDB) http.HandlerFunc { return handleCreateAlertRule(d.DB) }, `{"name":"cpu"}`, nil},
		{"update rule", func(d *sqlmockDB) http.HandlerFunc { return handleUpdateAlertRule(d.DB) }, `{"name":"cpu"}`, map[string]string{"id": "4"}},
		{"delete rule", func(d *sqlmockDB) http.HandlerFunc { return handleDeleteAlertRule(d.DB) }, "", map[string]string{"id": "4"}},
		{"toggle rule", func(d *sqlmockDB) http.HandlerFunc { return handleToggleAlertRule(d.DB) }, `{"enabled":true}`, map[string]string{"id": "4"}},
		{"create api key", func(d *sqlmockDB) http.HandlerFunc { return handleCreateAPIKey(d.DB) }, `{"name":"ci","expires_at":"` + future + `"}`, nil},
		{"delete api key", func(d *sqlmockDB) http.HandlerFunc { return handleDeleteAPIKey(d.DB) }, "", map[string]string{"id": "4"}},
		{"create install token", func(d *sqlmockDB) http.HandlerFunc { return handleCreateInstallToken(d.DB) }, `{"name":"fleet"}`, nil},
		{"delete install token", func(d *sqlmockDB) http.HandlerFunc { return handleDeleteInstallToken(d.DB) }, "", map[string]string{"id": "4"}},
	}
	for _, c := range cases {
		db, closeDB := newFailingDB(t)
		rec := httptest.NewRecorder()
		c.build(db)(rec, orgRequest("POST", "/x", c.body, c.vars))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status %d, want 500 (%s)", c.name, rec.Code, rec.Body.String())
		}
		closeDB()
	}
}

func TestFeatureEndpointsRejectMalformedBodies(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	handlers := map[string]http.HandlerFunc{
		"create channel":  handleCreateNotificationChannel(db),
		"update channel":  handleUpdateNotificationChannel(db),
		"create incident": handleCreateIncident(db),
		"create rule":     handleCreateAlertRule(db),
		"update rule":     handleUpdateAlertRule(db),
		"toggle rule":     handleToggleAlertRule(db),
		"create api key":  handleCreateAPIKey(db),
		"create token":    handleCreateInstallToken(db),
	}
	for name, handler := range handlers {
		rec := httptest.NewRecorder()
		handler(rec, orgRequest("POST", "/x", `{`, map[string]string{"id": "4"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestFeatureEndpointsValidateTheirPathIDs(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	handlers := map[string]http.HandlerFunc{
		"update channel":       handleUpdateNotificationChannel(db),
		"delete channel":       handleDeleteNotificationChannel(db),
		"test channel":         handleTestNotificationChannel(db),
		"update rule":          handleUpdateAlertRule(db),
		"delete rule":          handleDeleteAlertRule(db),
		"toggle rule":          handleToggleAlertRule(db),
		"delete api key":       handleDeleteAPIKey(db),
		"delete install token": handleDeleteInstallToken(db),
		"download profile":     handleDownloadProfile(db),
		"flamegraph":           handleGetProfileFlamegraph(db),
	}
	for name, handler := range handlers {
		rec := httptest.NewRecorder()
		handler(rec, orgRequest("POST", "/x", `{}`, map[string]string{"id": "abc"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// A channel with no name or an unknown type would silently deliver nothing.
func TestNotificationChannelValidation(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	for name, body := range map[string]string{
		"no name":  `{"type":"slack"}`,
		"bad type": `{"name":"ops","type":"carrier_pigeon"}`,
	} {
		rec := httptest.NewRecorder()
		handleCreateNotificationChannel(db)(rec, orgRequest("POST", "/x", body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("create %s: status %d, want 400", name, rec.Code)
		}
	}

	// On update the fields are optional, but a supplied one is still checked.
	for name, body := range map[string]string{
		"blank name": `{"name":"   "}`,
		"bad type":   `{"type":"carrier_pigeon"}`,
	} {
		rec := httptest.NewRecorder()
		handleUpdateNotificationChannel(db)(rec, orgRequest("PUT", "/x", body, map[string]string{"id": "4"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("update %s: status %d, want 400", name, rec.Code)
		}
	}
}

// Testing a channel that is not the caller's own must read as absent.
func TestTestNotificationChannelReports404ForAnUnknownChannel(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "name", "type", "config", "enabled", "created_at", "updated_at",
	}))

	rec := httptest.NewRecorder()
	handleTestNotificationChannel(db)(rec, orgRequest("POST", "/x", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// An incident with neither a service nor a host is attached to nothing and
// could never be resolved by a check recovering.
func TestCreateIncidentRequiresATarget(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleCreateIncident(db)(rec, orgRequest("POST", "/x", `{"title":"down"}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// A rule created from the UI omits the fields the evaluator needs; the defaults
// are what make a minimal rule evaluable at all.
func TestCreateAlertRuleFillsTheEvaluatorDefaults(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	// Positions 4, 5, 8 and 10 are type, target_type, operator and duration.
	args := make([]driver.Value, 22)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	args[3] = "threshold"
	args[4] = "any"
	args[7] = "gt"
	args[9] = 60
	mock.ExpectQuery("INSERT INTO alert_rules").WithArgs(args...).WillReturnError(errors.New("db down"))

	rec := httptest.NewRecorder()
	handleCreateAlertRule(db)(rec, orgRequest("POST", "/x", `{"name":"cpu"}`, nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("a rule was stored without the evaluator defaults: %v", err)
	}
}

// An API key with no expiry would be a perpetual credential.
func TestCreateAPIKeyRequiresAFutureExpiry(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)

	for name, body := range map[string]string{
		"no name":        `{"expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`,
		"no expiry":      `{"name":"ci"}`,
		"expiry in past": `{"name":"ci","expires_at":"` + past + `"}`,
	} {
		rec := httptest.NewRecorder()
		handleCreateAPIKey(db)(rec, orgRequest("POST", "/x", body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// Without OpenSearch the log and trace views answer an empty result set rather
// than failing: the rest of the product still works.
func TestLogAndTraceSearchDegradeWithoutOpenSearch(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"logs":   handleSearchLogs(),
		"traces": handleSearchTraces(),
	}
	for name, handler := range cases {
		rec := httptest.NewRecorder()
		handler(rec, orgRequest("GET", "/x?q=error&service=api&from=10&size=25&start=2026-09-01T00:00:00Z&end=2026-09-02T00:00:00Z", "", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		var body map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["from"] != float64(10) || body["size"] != float64(25) {
			t.Fatalf("%s: the paging echo is what the UI scrolls on: %v", name, body)
		}
		if hits, ok := body["hits"].([]interface{}); !ok || len(hits) != 0 {
			t.Fatalf("%s: hits must be an empty array, got %v", name, body["hits"])
		}
	}

	// Defaults are applied when the caller sends no window or paging.
	rec := httptest.NewRecorder()
	handleSearchLogs()(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handleSearchTraces()(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

// The facet endpoint is meaningless without a field, and it accepts the
// Datadog-style aliases the search bar produces.
func TestLogFieldValuesRequiresAField(t *testing.T) {
	rec := httptest.NewRecorder()
	handleLogFieldValues()(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleLogFieldValues()(rec, orgRequest("GET", "/x?field=service&size=10", "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"values":[]`) {
		t.Fatalf("body %q", rec.Body.String())
	}
}

// A profile upload is authenticated by the SDK's service token, and every field
// it carries is validated before 50 MB of data is stored.
func TestUploadProfileRequiresATokenAndAValidPayload(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 10; i++ {
		mock.ExpectQuery(".*").WillReturnError(errNoRows())
	}

	rec := httptest.NewRecorder()
	handleUploadProfile(db)(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// With a valid token, the profile type and the file itself are checked: an
// unknown type or an empty upload is a client mistake, not stored data.
func TestUploadProfileValidatesTypeAndFile(t *testing.T) {
	cases := []struct {
		name        string
		profileType string
		fileName    string
		content     string
		wantStatus  int
	}{
		{"unknown type", "telepathy", "profile", "data", http.StatusBadRequest},
		{"no file", "cpu", "", "", http.StatusBadRequest},
		{"empty file", "cpu", "profile", "", http.StatusBadRequest},
	}
	for _, c := range cases {
		db, mock, _ := sqlmock.New()
		mock.MatchExpectationsInOrder(false)
		mock.ExpectQuery("SELECT organization_id FROM services").
			WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(3)))

		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("profile_type", c.profileType)
		_ = mw.WriteField("service", "api")
		_ = mw.WriteField("duration_seconds", "30")
		_ = mw.WriteField("memory_mb", "128.5")
		if c.fileName != "" {
			part, _ := mw.CreateFormFile("profile", c.fileName)
			_, _ = part.Write([]byte(c.content))
		}
		_ = mw.Close()

		req := httptest.NewRequest("POST", "/x", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer svc-token")
		rec := httptest.NewRecorder()
		handleUploadProfile(db)(rec, req)

		if rec.Code != c.wantStatus {
			t.Fatalf("%s: status %d, want %d (%s)", c.name, rec.Code, c.wantStatus, rec.Body.String())
		}
		db.Close()
	}
}

// A request body that is not a multipart form cannot carry a profile.
func TestUploadProfileRejectsANonMultipartBody(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT organization_id FROM services").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(3)))

	req := httptest.NewRequest("POST", "/x", strings.NewReader("not a form"))
	req.Header.Set("Authorization", "Bearer svc-token")
	rec := httptest.NewRecorder()
	handleUploadProfile(db)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// The profile endpoints read stored capture data, so they are session-scoped.
func TestProfileEndpointsRequireASession(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	handlers := map[string]http.HandlerFunc{
		"series":     handleGetProfileSeries(db),
		"list":       handleListProfiles(db),
		"download":   handleDownloadProfile(db),
		"flamegraph": handleGetProfileFlamegraph(db),
	}
	for name, handler := range handlers {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("GET", "/x", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", name, rec.Code)
		}
	}
}

// A capture that is not the caller's own reads as absent.
func TestProfileDownloadAndFlamegraphReport404(t *testing.T) {
	for _, name := range []string{"download", "flamegraph"} {
		db, mock, _ := sqlmock.New()
		mock.MatchExpectationsInOrder(false)
		mock.ExpectQuery(".*").WillReturnError(errNoRows())

		handler := handleDownloadProfile(db)
		if name == "flamegraph" {
			handler = handleGetProfileFlamegraph(db)
		}
		rec := httptest.NewRecorder()
		handler(rec, orgRequest("GET", "/x", "", map[string]string{"id": "9"}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", name, rec.Code)
		}
		db.Close()
	}
}

// Data that is not a pprof profile cannot be turned into a flame graph; saying
// so beats a 500 the user cannot act on.
func TestFlamegraphRejectsAnUnparseableProfile(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "service", "profile_type", "duration_seconds", "size_bytes", "memory_mb", "created_at", "data",
	}).AddRow(int64(9), int64(1), "api", "cpu", 30, 11, 128.5, time.Now().UTC(), []byte("not a pprof")))

	rec := httptest.NewRecorder()
	handleGetProfileFlamegraph(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "9"}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// The network view reads the ping results the agent stores; an empty window
// must serialize as an array the chart can map over.
func TestNetworkMetricsReturnsAnArray(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{
		"id", "service_id", "service_name", "host", "status", "metric_value", "metadata", "timestamp",
	}))

	rec := httptest.NewRecorder()
	handleGetNetworkMetrics(db)(rec, orgRequest("GET", "/x", "", nil))

	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

// The explorer maps each agent check type onto the field the chart reads; a
// CPU sample landing in the RAM series would misreport the host.
func TestMetricsExplorerMapsEachCheckTypeToItsField(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	now := time.Now().UTC()

	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{
		"id", "timestamp", "metric_type", "metric_value", "latency", "service_name", "service_type", "host_name",
	}).
		AddRow(int64(1), now, "cpu", 91.5, nil, "cpu", "agent_cpu", "web-01").
		AddRow(int64(2), now, "ram", 42.0, nil, "ram", "agent_ram", "web-01").
		AddRow(int64(3), now, nil, nil, 120.0, "api", "http", "web-01"))
	mock.ExpectQuery("SELECT DISTINCT").WillReturnRows(
		sqlmock.NewRows([]string{"name", "host_name"}).AddRow("cpu", "web-01"))

	rec := httptest.NewRecorder()
	handleGetMetricsExplorer(db)(rec, orgRequest("GET", "/x?service=cpu&host=web-01", "", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Metrics []struct {
			CPUPerc     float64  `json:"cpu_perc"`
			RAMPerc     float64  `json:"ram_perc"`
			HTTPLatency *float64 `json:"http_latency"`
			Host        string   `json:"host"`
		} `json:"metrics"`
		Filters []struct {
			Service string `json:"service"`
			Host    string `json:"host"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Metrics) != 3 {
		t.Fatalf("got %d samples", len(body.Metrics))
	}
	if body.Metrics[0].CPUPerc != 91.5 || body.Metrics[1].RAMPerc != 42 || body.Metrics[2].HTTPLatency == nil {
		t.Fatalf("a sample landed in the wrong series: %+v", body.Metrics)
	}
	if len(body.Filters) != 1 || body.Filters[0].Host != "web-01" {
		t.Fatalf("filters %+v", body.Filters)
	}
}

// A failing filter query must not take the whole view down: the samples are
// what the page is for.
func TestMetricsExplorerSurvivesAFailingFilterQuery(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{
		"id", "timestamp", "metric_type", "metric_value", "latency", "service_name", "service_type", "host_name",
	}))
	mock.ExpectQuery("SELECT DISTINCT").WillReturnError(errors.New("db down"))

	rec := httptest.NewRecorder()
	handleGetMetricsExplorer(db)(rec, orgRequest("GET", "/x", "", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"filters":[]`) {
		t.Fatalf("body %q", rec.Body.String())
	}
}
