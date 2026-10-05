package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// The host is looked up within the caller's organization: another tenant's host
// id must answer 404, not its scrape targets.
func TestHostIngestCostIsScopedToTheOrganization(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT name FROM hosts WHERE id = \\$1 AND organization_id = \\$2").
		WithArgs(int64(42), int64(1)).WillReturnError(sql.ErrNoRows)

	rec := httptest.NewRecorder()
	handleGetHostIngestCost(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "42"}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}
