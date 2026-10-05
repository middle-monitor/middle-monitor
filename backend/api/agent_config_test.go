package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
)

// A fragment that only fails once it reaches the machine is a fragment nobody
// can debug, so it is parsed the way the agent will before it is stored.
func TestAgentConfigIsValidatedBeforeItIsStored(t *testing.T) {
	cases := map[string]bool{
		"- name: node\n  url: http://localhost:9100/metrics\n":       true,
		"targets:\n  - name: node\n    url: http://x:9100/metrics\n": true,
		"":                             true,  // clears the configuration
		"- name: node\n":               false, // no url
		"targets:\n  - name: node\n":   false,
		"this: is: not: yaml: at: all": false,
		"[]":                           false, // an empty list says nothing
	}

	for fragment, valid := range cases {
		err := validateScrapeFragment(fragment)
		if valid && err != nil {
			t.Fatalf("%q was rejected: %v", fragment, err)
		}
		if !valid && err == nil {
			t.Fatalf("%q was accepted", fragment)
		}
	}
}

// The agent authenticates with its install token, the only credential it has.
func TestAgentConfigRequiresAnInstallToken(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	rec := httptest.NewRecorder()
	handleAgentConfig(db)(rec, httptest.NewRequest("GET", "/api/v1/agents/config?host=web-01", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestAgentConfigNeedsAHostName(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	rec := httptest.NewRecorder()
	handleAgentConfig(db)(rec, httptest.NewRequest("GET", "/api/v1/agents/config", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// Reading back what an operator set is what makes the feature usable from a
// tool: write, then converge on the same document.
func TestHostAgentConfigRoundTrip(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	updated := time.Now().UTC()
	fragment := "- name: node\n  url: http://localhost:9100/metrics\n"

	mock.ExpectExec("UPDATE hosts SET agent_scrape_config").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT agent_scrape_config").
		WillReturnRows(sqlmock.NewRows([]string{"agent_scrape_config", "agent_config_updated_at"}).
			AddRow(fragment, updated))

	body, _ := json.Marshal(AgentConfigDocument{ScrapeConfig: fragment})
	req := httptest.NewRequest("PUT", "/x", strings.NewReader(string(body)))
	req = mux.SetURLVars(orgContext(req, 1, 1), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()

	handleSetHostAgentConfig(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var answer AgentConfigDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.ScrapeConfig != fragment || answer.UpdatedAt == nil {
		t.Fatalf("answer %+v", answer)
	}
}

// A broken fragment must be refused at the API, not discovered on the machine.
func TestBrokenAgentConfigIsRefused(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	body := `{"scrape_config":"- name: node\n"}`
	req := httptest.NewRequest("PUT", "/x", strings.NewReader(body))
	req = mux.SetURLVars(orgContext(req, 1, 1), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()

	handleSetHostAgentConfig(db)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "url") {
		t.Fatalf("the error does not say what is wrong: %s", rec.Body.String())
	}
}

// This is configuration an agent will execute: it has to be bounded.
func TestOversizedAgentConfigIsRefused(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	huge := strings.Repeat("x", maxAgentConfigBytes+10)
	body, _ := json.Marshal(AgentConfigDocument{ScrapeConfig: huge})
	req := httptest.NewRequest("PUT", "/x", strings.NewReader(string(body)))
	req = mux.SetURLVars(orgContext(req, 1, 1), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()

	handleSetHostAgentConfig(db)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}
