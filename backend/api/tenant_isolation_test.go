package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Host and service ids are sequential integers, so any signed-in user can
// guess another tenant's. Acting on one by id must first prove it belongs to
// the caller's organization, and answer as if it did not exist otherwise.
func TestRowsOfAnotherOrganizationAreUnreachable(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		table   string
		body    string
		handler func(db *sqlmockDB) http.HandlerFunc
	}{
		{"update host", "PUT", "hosts", `{"display_name":"x"}`, func(db *sqlmockDB) http.HandlerFunc { return handleUpdateHost(db.DB) }},
		{"delete host", "DELETE", "hosts", "", func(db *sqlmockDB) http.HandlerFunc { return handleDeleteHost(db.DB) }},
		{"update service", "PUT", "services", `{"name":"x","type":"http"}`, func(db *sqlmockDB) http.HandlerFunc { return handleUpdateService(db.DB) }},
		{"delete service", "DELETE", "services", "", func(db *sqlmockDB) http.HandlerFunc { return handleDeleteService(db.DB) }},
		{"service results", "GET", "services", "", func(db *sqlmockDB) http.HandlerFunc { return handleGetServiceResults(db.DB) }},
		{"agent metrics", "GET", "services", "", func(db *sqlmockDB) http.HandlerFunc { return handleGetAgentMetrics(db.DB) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM `+c.table+` WHERE id = \$1 AND organization_id = \$2\)`).
				WithArgs(int64(42), int64(1)).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

			rec := httptest.NewRecorder()
			c.handler(&sqlmockDB{DB: raw, mock: mock})(rec, orgRequest(c.method, "/x", c.body, map[string]string{"id": "42"}))

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("the row was touched before its owner was checked: %v", err)
			}
		})
	}
}
