package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// The status page is Middle Monitor's own, not any org's: without the
// self-monitoring org configured there is nothing to publish.
func TestPublicStatusIsUnavailableWithoutTheSelfMonitorOrg(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("SELF_MONITOR_ORG_SLUG", "")

	rec := httptest.NewRecorder()
	handlePublicStatus(db)(rec, httptest.NewRequest("GET", "/api/v1/status", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// The page is what people hit hardest during an outage; without caching it
// amplifies the incident it reports.
func TestPublicStatusIsCachedForAMinute(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("SELF_MONITOR_ORG_SLUG", "admin")
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("FROM organizations WHERE slug").
		WillReturnRows(sqlmock.NewRows([]string{"id", "plan", "trial_ends_at", "custom_retention_days"}).
			AddRow(int64(3), "pro", nil, nil))
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{}))

	rec := httptest.NewRecorder()
	handlePublicStatus(db)(rec, httptest.NewRequest("GET", "/api/v1/status", nil))

	if rec.Code == http.StatusOK && rec.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("cache-control %q", rec.Header().Get("Cache-Control"))
	}
}

// An unknown slug must be a 404, not a 500: the org simply is not published.
func TestPublicStatusReports404ForAnUnknownOrg(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("SELF_MONITOR_ORG_SLUG", "ghost")
	mock.ExpectQuery("FROM organizations WHERE slug").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handlePublicStatus(db)(rec, httptest.NewRequest("GET", "/api/v1/status", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}
