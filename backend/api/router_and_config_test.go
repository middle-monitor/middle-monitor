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
	"github.com/gorilla/mux"
)

// The receiver is the ingestion process: every SDK, agent and OTLP entry point
// has to be reachable on it, or a whole signal silently stops arriving.
func TestReceiverRouterServesEveryIngestionEntryPoint(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	router := SetupReceiverRouter(db, nil)

	cases := []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/readyz"},
		{"POST", "/v1/traces"},
		{"POST", "/v1/logs"},
		{"POST", "/v1/metrics"},
		{"POST", "/api/v1/errors"},
		{"POST", "/api/v1/metrics"},
		{"POST", "/api/v1/agents/register"},
		{"POST", "/api/v1/agents/metrics"},
		{"GET", "/api/v1/agents/download/install"},
		{"GET", "/api/v1/agents/download/update"},
		{"GET", "/api/v1/agents/latest"},
		{"GET", "/api/v1/agents/config"},
		{"GET", "/api/v1/agents/download/linux/amd64"},
		{"GET", "/api/v1/agents/download/linux/amd64/sha256"},
		{"GET", "/api/v1/agents/download/v1.2.3/linux/amd64"},
		{"POST", "/api/v1/profiles"},
		{"GET", "/api/v1/health/sql"},
	}
	for _, c := range cases {
		var match mux.RouteMatch
		if !router.Match(httptest.NewRequest(c.method, c.path, nil), &match) || match.MatchErr != nil {
			t.Fatalf("%s %s is not served by the receiver", c.method, c.path)
		}
	}

	// And the wiring actually answers, rather than matching a nil handler.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz on the receiver: status %d", rec.Code)
	}
}

// The OpenAPI security block mirrors this: a route wrongly listed as public
// would document an unauthenticated endpoint that answers 401.
func TestPublicRouteMatchesTheUnauthenticatedSurface(t *testing.T) {
	public := []string{
		"/healthz", "/readyz",
		"/api/v1/auth/login", "/api/v1/auth/register",
		"/api/v1/webhooks/stripe",
		"/api/v1/contact", "/api/v1/status", "/api/v1/openapi.json",
		"/api/v1/schemas/webhook-payload.json",
		"/api/v1/agents/config",
	}
	for _, path := range public {
		if !PublicRoute(path) {
			t.Fatalf("%s is served without auth but not declared public", path)
		}
	}

	protected := []string{
		"/api/v1/organizations/{org_slug}/hosts",
		"/api/v1/organizations/{org_slug}",
	}
	for _, path := range protected {
		if PublicRoute(path) {
			t.Fatalf("%s requires a session but is declared public", path)
		}
	}
}

// The webhook payload schema ships in the binary: a receiver integrating with
// us must be able to read it from any deployment.
func TestWebhookPayloadSchemaIsAlwaysServed(t *testing.T) {
	rec := httptest.NewRecorder()
	handleWebhookPayloadSchema()(rec, httptest.NewRequest("GET", "/api/v1/schemas/webhook-payload.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/schema+json" {
		t.Fatalf("content-type %q", got)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &schema); err != nil {
		t.Fatalf("the embedded schema is not valid JSON: %v", err)
	}
}

// The agent config schema is owned by the agent tree; a deployment shipping the
// API alone answers 404 rather than serving an empty document.
func TestAgentConfigSchemaIsServedOnlyWhenTheAgentTreeIsPresent(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAgentConfigSchema()(rec, httptest.NewRequest("GET", "/api/v1/schemas/agent-config.json", nil))

	if rec.Code == http.StatusOK {
		var schema map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &schema); err != nil {
			t.Fatalf("the served schema is not valid JSON: %v", err)
		}
		return
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 or 200", rec.Code)
	}
}

// The fragment is per-host and org-scoped: a host that is not the caller's own
// must read as absent rather than expose another tenant's scrape targets.
func TestGetHostAgentConfigIsOrgScoped(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	rec := httptest.NewRecorder()
	handleGetHostAgentConfig(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status %d", rec.Code)
	}

	mock.ExpectQuery("agent_scrape_config").WithArgs(int64(4), int64(1)).WillReturnError(errNoRows())
	rec = httptest.NewRecorder()
	handleGetHostAgentConfig(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}

	mock.ExpectQuery("agent_scrape_config").WillReturnError(errors.New("db down"))
	rec = httptest.NewRecorder()
	handleGetHostAgentConfig(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// An operator reading back the fragment they set is how they check it landed,
// so the stored value and its timestamp both come back.
func TestGetHostAgentConfigReturnsTheStoredFragment(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	now := time.Now().UTC()

	mock.ExpectQuery("agent_scrape_config").WillReturnRows(
		sqlmock.NewRows([]string{"agent_scrape_config", "agent_config_updated_at"}).
			AddRow("- name: node\n  url: http://localhost:9100/metrics\n", now))

	rec := httptest.NewRecorder()
	handleGetHostAgentConfig(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "4"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var doc AgentConfigDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(doc.ScrapeConfig, "9100") || doc.UpdatedAt == nil {
		t.Fatalf("unexpected document: %+v", doc)
	}
}

// Writing a fragment to a host that is not the caller's own must change nothing
// and read as absent.
func TestSetHostAgentConfigIsOrgScoped(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectExec("UPDATE hosts SET agent_scrape_config").
		WithArgs(nil, int64(4), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	rec := httptest.NewRecorder()
	handleSetHostAgentConfig(db)(rec, orgRequest("PUT", "/x", `{"scrape_config":""}`, map[string]string{"id": "4"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}

	mock.ExpectExec("UPDATE hosts SET agent_scrape_config").WillReturnError(errors.New("db down"))
	rec = httptest.NewRecorder()
	handleSetHostAgentConfig(db)(rec, orgRequest("PUT", "/x", `{"scrape_config":""}`, map[string]string{"id": "4"}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// A stored fragment is read back from the row that was just written, so the
// operator sees exactly what the agent will fetch.
func TestSetHostAgentConfigEchoesWhatWasStored(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	now := time.Now().UTC()
	fragment := "- name: node\n  url: http://localhost:9100/metrics\n"

	mock.ExpectExec("UPDATE hosts SET agent_scrape_config").
		WithArgs(fragment, int64(4), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("agent_scrape_config").WillReturnRows(
		sqlmock.NewRows([]string{"agent_scrape_config", "agent_config_updated_at"}).AddRow(fragment, now))

	body, _ := json.Marshal(AgentConfigDocument{ScrapeConfig: fragment})
	rec := httptest.NewRecorder()
	handleSetHostAgentConfig(db)(rec, orgRequest("PUT", "/x", string(body), map[string]string{"id": "4"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "9100") {
		t.Fatalf("body %q", rec.Body.String())
	}
}

// The agent fetches the fragment for its own host: an unknown host name means
// there is nothing to scrape, which is an empty config, not an error.
func TestAgentConfigServesTheFragmentForItsOwnHost(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	now := time.Now().UTC()

	mock.ExpectQuery("FROM install_tokens").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "expires_at"}).AddRow(int64(1), nil))
	mock.ExpectQuery("agent_scrape_config").
		WillReturnRows(sqlmock.NewRows([]string{"agent_scrape_config", "agent_config_updated_at"}).
			AddRow("- name: node\n  url: http://localhost:9100/metrics\n", now))

	req := httptest.NewRequest("GET", "/api/v1/agents/config?host=web-01", nil)
	req.Header.Set("X-Install-Token", "mmi_token")
	rec := httptest.NewRecorder()
	handleAgentConfig(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "9100") {
		t.Fatalf("body %q", rec.Body.String())
	}
}

// The dedup key is what a receiver sends back instead of storing an id, so it
// has to be derivable from whatever the incident is attached to.
func TestIncidentDedupKeyPrefersTheRule(t *testing.T) {
	serviceID := int64(4)
	cases := []struct {
		name string
		snap incidentSnapshot
		want string
	}{
		{"rule", incidentSnapshot{ruleID: nullInt(5)}, "mm-rule-5"},
		{"service", incidentSnapshot{incident: incidentWithService(&serviceID)}, "mm-svc-4"},
		{"neither", incidentSnapshot{}, ""},
	}
	for _, c := range cases {
		if got := incidentDedupKey(c.snap); got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A receiver switches on the event type, so the title has to say which
// transition happened without parsing the body.
func TestIncidentEventTitleNamesTheTransition(t *testing.T) {
	cases := map[string]string{
		"incident.resolved":     "[RESOLVED] API down",
		"incident.acknowledged": "[ACK] API down",
		"incident.reopened":     "[REOPENED] API down",
		"incident.opened":       "API down",
	}
	for eventType, want := range cases {
		if got := incidentEventTitle(eventType, "API down"); got != want {
			t.Fatalf("%s: got %q, want %q", eventType, got, want)
		}
	}
}

// A receiver acting on a dedup key it was given must not be able to move an
// incident in another org, and an unknown key is simply not found.
func TestUpdateIncidentStatusByDedupKey(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	rec := httptest.NewRecorder()
	handleUpdateIncidentStatusByDedupKey(db)(rec, orgRequest("PUT", "/x", `{`, map[string]string{"key": "mm-rule-5"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleUpdateIncidentStatusByDedupKey(db)(rec, orgRequest("PUT", "/x", `{"status":"resolved"}`, map[string]string{"key": "pagerduty-42"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown key shape: status %d, want 404", rec.Code)
	}

	mock.ExpectQuery("SELECT id FROM incidents").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(801)))
	mock.ExpectQuery("SELECT i.id, i.organization_id").WillReturnRows(incidentRows())
	rec = httptest.NewRecorder()
	handleUpdateIncidentStatusByDedupKey(db)(rec, orgRequest("PUT", "/x", `{"status":"resolved"}`, map[string]string{"key": "mm-rule-5"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("incident gone: status %d, want 404", rec.Code)
	}
}

// A receiver resolving an incident by dedup key is the whole point of the loop:
// the transition has to land and the key has to come back with it.
func TestDedupKeyResolutionMovesTheIncident(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	started := time.Now().UTC()

	mock.ExpectQuery("SELECT id FROM incidents").WithArgs(int64(1), int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(801)))
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT i.id, i.organization_id").
			WillReturnRows(incidentRows().AddRow(int64(801), int64(1), int64(5), nil, nil,
				"API down", nil, "critical", "open", started, nil, nil, nil))
	}
	mock.ExpectExec("UPDATE incidents").WillReturnResult(sqlmock.NewResult(1, 1))

	rec := httptest.NewRecorder()
	handleUpdateIncidentStatusByDedupKey(db)(rec,
		orgRequest("PUT", "/x", `{"status":"resolved","actor":"automation"}`, map[string]string{"key": "mm-rule-5"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var answer map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	if answer["dedup_key"] != "mm-rule-5" {
		t.Fatalf("the receiver needs its key back: %v", answer)
	}
}
