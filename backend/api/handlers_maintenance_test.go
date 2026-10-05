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

// Every field of a maintenance window is load-bearing: a window with no target,
// no name or an inverted range would silence alerts it was never meant to.
func TestCreateMaintenanceWindowRejectsAnUnusableWindow(t *testing.T) {
	now := time.Now().UTC()
	cases := map[string]string{
		"not json":        `{`,
		"no name":         `{"target_type":"service","target_id":1,"starts_at":"` + now.Format(time.RFC3339) + `","ends_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `"}`,
		"bad target type": `{"name":"win","target_type":"cluster","target_id":1}`,
		"no target id":    `{"name":"win","target_type":"service","target_id":0}`,
		"inverted range":  `{"name":"win","target_type":"service","target_id":1,"starts_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `","ends_at":"` + now.Format(time.RFC3339) + `"}`,
	}
	for name, body := range cases {
		db, _, _ := sqlmock.New()
		rec := httptest.NewRecorder()
		handleCreateMaintenanceWindow(db)(rec, orgRequest("POST", "/x", body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
		db.Close()
	}
}

// The window must target a row of the caller's own org, or it would silence
// another tenant's alerts.
func TestCreateMaintenanceWindowChecksTheTargetOwnership(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	now := time.Now().UTC()
	body := `{"name":"win","target_type":"service","target_id":99,"starts_at":"` +
		now.Format(time.RFC3339) + `","ends_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `"}`
	rec := httptest.NewRecorder()
	handleCreateMaintenanceWindow(db)(rec, orgRequest("POST", "/x", body, nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// The window records who opened it, so an on-call engineer can ask.
func TestCreateMaintenanceWindowStampsTheAuthor(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	now := time.Now().UTC()

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("INSERT INTO maintenance_windows").
		WithArgs(int64(1), "win", "service", int64(5), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(11), now))

	body := `{"name":"win","target_type":"service","target_id":5,"starts_at":"` +
		now.Format(time.RFC3339) + `","ends_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `"}`
	rec := httptest.NewRecorder()
	handleCreateMaintenanceWindow(db)(rec, orgRequest("POST", "/x", body, nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the author was not recorded: %v", err)
	}
}

func TestListMaintenanceWindowsSurfacesALookupFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("FROM maintenance_windows").WillReturnError(errors.New("down"))

	rec := httptest.NewRecorder()
	handleGetMaintenanceWindows(db)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

func TestListMaintenanceWindowsReturnsTheOrgWindows(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	now := time.Now().UTC()
	mock.ExpectQuery("FROM maintenance_windows").WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "name", "target_type", "target_id",
			"starts_at", "ends_at", "created_by", "created_at", "target_name",
		}).AddRow(int64(11), int64(1), "win", "service", int64(5), now, now.Add(time.Hour), int64(7), now, "api"))

	rec := httptest.NewRecorder()
	handleGetMaintenanceWindows(db)(rec, orgRequest("GET", "/x", "", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"target_name":"api"`) {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestDeleteMaintenanceWindow(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleDeleteMaintenanceWindow(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	// A window that is not the caller's own must read as absent, not as an error.
	mock.ExpectExec("DELETE FROM maintenance_windows").WithArgs(int64(11), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	rec = httptest.NewRecorder()
	handleDeleteMaintenanceWindow(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "11"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}

	mock.ExpectExec("DELETE FROM maintenance_windows").WillReturnResult(sqlmock.NewResult(0, 1))
	rec = httptest.NewRecorder()
	handleDeleteMaintenanceWindow(db)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "11"}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
}
