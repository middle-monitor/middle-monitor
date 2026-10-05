package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// The frontend maps over the answer, so an org with no dashboard must get an
// empty array rather than null.
func TestListCustomDashboardsNeverReturnsNull(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("FROM custom_dashboards").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "name", "widgets", "created_at", "updated_at"}))

	rec := httptest.NewRecorder()
	handleGetCustomDashboards(db)(rec, orgRequest("GET", "/x", "", nil))

	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestListCustomDashboardsSurfacesALookupFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("FROM custom_dashboards").WillReturnError(errors.New("down"))

	rec := httptest.NewRecorder()
	handleGetCustomDashboards(db)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

func TestCreateCustomDashboardRequiresAName(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	for _, body := range []string{`{`, `{"name":"   "}`} {
		rec := httptest.NewRecorder()
		handleCreateCustomDashboard(db)(rec, orgRequest("POST", "/x", body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: status %d, want 400", body, rec.Code)
		}
	}
}

// The dashboard is created in the caller's org, never in the one the body asks
// for: the org comes from the session, not from user input.
func TestCreateCustomDashboardIgnoresTheOrgInTheBody(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	now := time.Now().UTC()
	mock.ExpectQuery("INSERT INTO custom_dashboards").
		WithArgs(int64(1), "Ops", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(3), now, now))

	rec := httptest.NewRecorder()
	handleCreateCustomDashboard(db)(rec, orgRequest("POST", "/x", `{"name":" Ops ","organization_id":999}`, nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the body's organization_id was trusted: %v", err)
	}
}

func TestCreateCustomDashboardSurfacesAWriteFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("INSERT INTO custom_dashboards").WillReturnError(errors.New("down"))

	rec := httptest.NewRecorder()
	handleCreateCustomDashboard(db)(rec, orgRequest("POST", "/x", `{"name":"Ops"}`, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

func TestUpdateCustomDashboard(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleUpdateCustomDashboard(db)(rec, orgRequest("PUT", "/x", `{"name":"Ops"}`, map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleUpdateCustomDashboard(db)(rec, orgRequest("PUT", "/x", `{`, map[string]string{"id": "3"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleUpdateCustomDashboard(db)(rec, orgRequest("PUT", "/x", `{"name":""}`, map[string]string{"id": "3"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no name: status %d", rec.Code)
	}

	// Another org's dashboard simply does not exist for this caller.
	mock.ExpectQuery("UPDATE custom_dashboards").WillReturnError(errNoRows())
	rec = httptest.NewRecorder()
	handleUpdateCustomDashboard(db)(rec, orgRequest("PUT", "/x", `{"name":"Ops"}`, map[string]string{"id": "3"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}

	mock.ExpectQuery("UPDATE custom_dashboards").WillReturnError(errors.New("down"))
	rec = httptest.NewRecorder()
	handleUpdateCustomDashboard(db)(rec, orgRequest("PUT", "/x", `{"name":"Ops"}`, map[string]string{"id": "3"}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}

	now := time.Now().UTC()
	mock.ExpectQuery("UPDATE custom_dashboards").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "name", "widgets", "created_at", "updated_at"}).
			AddRow(int64(3), int64(1), "Ops", []byte(`[{"type":"chart"}]`), now, now))
	rec = httptest.NewRecorder()
	handleUpdateCustomDashboard(db)(rec, orgRequest("PUT", "/x", `{"name":"Ops"}`, map[string]string{"id": "3"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteCustomDashboard(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleDeleteCustomDashboard(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	mock.ExpectExec("DELETE FROM custom_dashboards").WillReturnResult(sqlmock.NewResult(0, 0))
	rec = httptest.NewRecorder()
	handleDeleteCustomDashboard(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "3"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}

	mock.ExpectExec("DELETE FROM custom_dashboards").WillReturnError(errors.New("down"))
	rec = httptest.NewRecorder()
	handleDeleteCustomDashboard(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "3"}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}

	mock.ExpectExec("DELETE FROM custom_dashboards").WillReturnResult(sqlmock.NewResult(0, 1))
	rec = httptest.NewRecorder()
	handleDeleteCustomDashboard(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "3"}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
}
