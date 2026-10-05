package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// platformRequest builds a PATCH the way RequirePlatformAdmin hands it over:
// with the caller's claims, which the handler logs the override against.
func platformRequest(id, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
	claims := &services.JWTClaims{UserID: 7, Email: "owner@example.com"}
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, claims))
	return mux.SetURLVars(r, map[string]string{"id": id})
}

// orgExists stubs the existence lookup the handler makes before writing.
func orgExists(mock sqlmock.Sqlmock, found bool) {
	mock.ExpectQuery("SELECT EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(found))
}

func TestHandleListPlatformOrganizations_ReturnsEveryOrg(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	createdAt := time.Now()
	rows := sqlmock.NewRows([]string{"id", "name", "slug", "plan", "trial_ends_at", "created_at"}).
		AddRow(int64(1), "Acme", "acme", "free", nil, createdAt).
		AddRow(int64(2), "Globex", "globex", "pro", nil, createdAt)
	mock.ExpectQuery("SELECT id, name, slug, COALESCE.plan").WillReturnRows(rows)

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
	if len(orgs) != 2 || orgs[0].Slug != "acme" || orgs[1].Plan != "pro" {
		t.Fatalf("unexpected orgs: %+v", orgs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

func TestHandleListPlatformOrganizations_QueryError_Returns500(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT id, name, slug, COALESCE.plan").WillReturnError(errors.New("db down"))

	req := httptest.NewRequest(http.MethodGet, "/platform-admin/organizations", nil)
	rec := httptest.NewRecorder()
	handleListPlatformOrganizations(db)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleSetPlatformOrganizationPlan_InvalidID_Returns400(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleSetPlatformOrganizationPlan(db)(rec, platformRequest("abc", `{"plan":"pro"}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleSetPlatformOrganizationPlan_MalformedBody_Returns400(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleSetPlatformOrganizationPlan(db)(rec, platformRequest("7", "{"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleSetPlatformOrganizationPlan_UnknownPlan_Returns400WithoutTouchingDB(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleSetPlatformOrganizationPlan(db)(rec, platformRequest("7", `{"plan":"platinum"}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an unvalidated plan reached the database: %v", err)
	}
}

// The route exists to comp or downgrade one account by hand. setOrgPlan's WHERE
// simply matches nothing on a mistyped id and reports no error, so without this
// the operator cannot tell a success from a typo.
func TestHandleSetPlatformOrganizationPlan_UnknownOrg_Returns404AndWritesNothing(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	orgExists(mock, false)

	rec := httptest.NewRecorder()
	handleSetPlatformOrganizationPlan(db)(rec, platformRequest("4242", `{"plan":"pro"}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// No ExpectExec was registered: any write here would fail the expectations.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("a plan was written for an org that does not exist: %v", err)
	}
}

func TestHandleSetPlatformOrganizationPlan_ValidPlan_UpdatesAndClearsTrial(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	orgExists(mock, true)
	mock.ExpectExec("SET plan = .*trial_ends_at = NULL").WithArgs("pro", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	handleSetPlatformOrganizationPlan(db)(rec, platformRequest("7", `{"plan":"pro"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}
