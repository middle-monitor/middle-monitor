package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	_ "github.com/lib/pq"
)

// Integration tests run against a real PostgreSQL: what they check is that the
// plan override actually persists and clears a running trial, which a sqlmock
// replay of hand-written rows can't tell us. See
// workers/integration_support_test.go for the rationale.
//
// Opt-in on MM_INTEGRATION_DB, run with `make test-integration`.

func platformAdminIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("MM_INTEGRATION_DB")
	if dsn == "" {
		t.Skip("set MM_INTEGRATION_DB to run integration tests (see make test-integration)")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open MM_INTEGRATION_DB: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping MM_INTEGRATION_DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := RunMigrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`TRUNCATE organizations RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

func seedPlatformOrg(t *testing.T, db *sql.DB, slug, plan string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, plan) VALUES ($1, $1, $2) RETURNING id`, slug, plan,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return id
}

func TestIntegrationListPlatformOrganizationsReturnsSeededRows(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	seedPlatformOrg(t, db, "acme", "free")
	seedPlatformOrg(t, db, "globex", "pro")

	req := httptest.NewRequest(http.MethodGet, "/platform-admin/organizations", nil)
	rec := httptest.NewRecorder()
	handleListPlatformOrganizations(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var orgs []PlatformOrganization
	if err := json.Unmarshal(rec.Body.Bytes(), &orgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("got %d organizations, want 2", len(orgs))
	}
	plans := map[string]string{}
	for _, o := range orgs {
		plans[o.Slug] = o.Plan
	}
	if plans["acme"] != "free" || plans["globex"] != "pro" {
		t.Fatalf("unexpected plans: %+v", plans)
	}
}

func TestIntegrationSetPlatformOrganizationPlanPersistsAndClearsTrial(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	orgID := seedPlatformOrg(t, db, "acme", "free")
	if _, err := db.Exec(
		`UPDATE organizations SET trial_ends_at = NOW() + interval '7 days' WHERE id = $1`, orgID,
	); err != nil {
		t.Fatalf("seed trial: %v", err)
	}

	rec := httptest.NewRecorder()
	handleSetPlatformOrganizationPlan(db)(rec, platformRequest(strconv.FormatInt(orgID, 10), `{"plan":"pro"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var plan string
	var trialEndsAt sql.NullTime
	err := db.QueryRow(`SELECT plan, trial_ends_at FROM organizations WHERE id = $1`, orgID).Scan(&plan, &trialEndsAt)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if plan != "pro" {
		t.Fatalf("plan = %q, want pro", plan)
	}
	if trialEndsAt.Valid {
		t.Fatalf("trial_ends_at is still set after the plan override")
	}
}

// A mistyped id must answer 404 and leave every org alone, rather than report a
// success for a write that matched no row.
func TestIntegrationSetPlatformOrganizationPlanRejectsAnUnknownOrg(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	orgID := seedPlatformOrg(t, db, "acme", "free")

	rec := httptest.NewRecorder()
	handleSetPlatformOrganizationPlan(db)(rec, platformRequest(strconv.FormatInt(orgID+1000, 10), `{"plan":"pro"}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var plan string
	if err := db.QueryRow(`SELECT plan FROM organizations WHERE id = $1`, orgID).Scan(&plan); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if plan != "free" {
		t.Fatalf("the seeded org moved to %q on a write aimed at an unknown id", plan)
	}
}
