package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"middle-monitor/backend/services"
)

// An org status page is served to anyone, so what is published is checked
// against the SQL that actually runs: a sqlmock replay would agree with
// whatever filter the query has.

func seedStatusCheck(t *testing.T, db *sql.DB, orgID int64, name, svcType string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO services (organization_id, name, display_name, type, host, service, service_interval, max_attempts)
		VALUES ($1, $2, $3, $4, 'https://internal.example', 'app', 60, 3) RETURNING id`,
		orgID, name, name+" label", svcType,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed check: %v", err)
	}
	return id
}

func publish(t *testing.T, db *sql.DB, orgID int64, ids ...int64) int {
	t.Helper()
	body, _ := json.Marshal(map[string][]int64{"service_ids": ids})
	rec := httptest.NewRecorder()
	req := orgContext(httptest.NewRequest("PUT", "/x", bytes.NewReader(body)), orgID, 1)
	handleSetStatusPageChecks(db)(rec, req)
	return rec.Code
}

func orgStatusPage(t *testing.T, db *sql.DB, slug string) (int, services.StatusPage) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := mux.SetURLVars(httptest.NewRequest("GET", "/api/v1/status/"+slug, nil), map[string]string{"org_slug": slug})
	handlePublicOrgStatus(db)(rec, req)
	var page services.StatusPage
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	return rec.Code, page
}

// Nothing is public until an admin picks it: an org that published nothing has
// no page at all, rather than an "all operational" claim.
func TestIntegrationOrgStatusPageIsOptIn(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	orgID := seedPlatformOrg(t, db, "acme", "pro")
	seedStatusCheck(t, db, orgID, "api", "http")

	if code, _ := orgStatusPage(t, db, "acme"); code != http.StatusNotFound {
		t.Fatalf("status %d before anything was published, want 404", code)
	}
}

// The page shows exactly the selection, by label, under the org's name, and a
// new selection replaces the previous one instead of adding to it.
func TestIntegrationOrgStatusPageShowsExactlyThePublishedChecks(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	orgID := seedPlatformOrg(t, db, "acme", "pro")
	api := seedStatusCheck(t, db, orgID, "api", "http")
	web := seedStatusCheck(t, db, orgID, "web", "http")

	if code := publish(t, db, orgID, api, web); code != http.StatusOK {
		t.Fatalf("publish: %d", code)
	}
	if code := publish(t, db, orgID, web); code != http.StatusOK {
		t.Fatalf("publish: %d", code)
	}

	code, page := orgStatusPage(t, db, "acme")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if page.Name != "acme" || len(page.Components) != 1 || page.Components[0].Name != "web label" {
		t.Fatalf("got name %q, components %+v", page.Name, page.Components)
	}
}

// An admin of one org must not be able to publish another org's check, and
// internal services (agent metrics, SDK services) are never publishable.
func TestIntegrationPublishingIgnoresForeignAndInternalServices(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	acme := seedPlatformOrg(t, db, "acme", "pro")
	other := seedPlatformOrg(t, db, "other", "pro")
	foreign := seedStatusCheck(t, db, other, "secret-api", "http")
	agent := seedStatusCheck(t, db, acme, "cpu", "agent_cpu")
	own := seedStatusCheck(t, db, acme, "api", "http")

	if code := publish(t, db, acme, foreign, agent, own); code != http.StatusOK {
		t.Fatalf("publish: %d", code)
	}

	if code, _ := orgStatusPage(t, db, "other"); code != http.StatusNotFound {
		t.Fatalf("another org's check was published through acme (status %d)", code)
	}
	var agentPublic bool
	if err := db.QueryRow(`SELECT public FROM services WHERE id = $1`, agent).Scan(&agentPublic); err != nil {
		t.Fatal(err)
	}
	if agentPublic {
		t.Fatal("an agent metric was published")
	}
}
