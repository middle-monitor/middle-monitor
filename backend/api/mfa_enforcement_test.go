package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// Enforcing org-wide 2FA sends every member without an authenticator into the
// enrollment gate, and the gate has no way out but logout. An admin who has not
// enrolled would therefore lock themselves out of their own organization with a
// single checkbox, with no self-service recovery: the write must be refused.

func updateOrgRequest(t *testing.T, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organizations/acme", strings.NewReader(body))
	claims := &services.JWTClaims{UserID: 7, OrganizationID: 3, OrganizationSlug: "acme", Role: "admin"}
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, claims))
	return httptest.NewRecorder(), req
}

// Columns loadOrg scans after a settings-only update.
func orgRow() *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at",
		"alert_warning_enabled", "alert_critical_enabled", "mfa_required",
		"smtp_host", "smtp_port", "smtp_user", "smtp_from", "smtp_pass_enc",
	}).AddRow(int64(3), "Acme", "acme", "pro", nil, now, now, true, true, true, "", "", "", "", "")
}

func TestUpdateOrganization_RefusesMFAEnforcementWhenAdminNotEnrolled(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT COALESCE\\(totp_enabled, false\\) FROM users").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"totp_enabled"}).AddRow(false))

	rec, req := updateOrgRequest(t, `{"mfa_required":true}`)
	handleUpdateOrganization(services.NewAuthService(db)).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected %d, got %d (body %s)", http.StatusConflict, rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != services.ErrMFAEnrollFirst.Error() {
		t.Errorf("expected the enroll-first message, got %q", resp["error"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the UPDATE must not run: %v", err)
	}
}

func TestUpdateOrganization_AllowsMFAEnforcementWhenAdminEnrolled(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT COALESCE\\(totp_enabled, false\\) FROM users").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"totp_enabled"}).AddRow(true))
	mock.ExpectExec("UPDATE organizations SET mfa_required").
		WithArgs(true, int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM organizations WHERE id").WillReturnRows(orgRow())

	rec, req := updateOrgRequest(t, `{"mfa_required":true}`)
	handleUpdateOrganization(services.NewAuthService(db)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("an enrolled admin must be allowed to enforce 2FA, got %d (body %s)", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected queries: %v", err)
	}
}

// Turning enforcement off can never lock anyone out, so it needs no enrollment check.
func TestUpdateOrganization_DisablingMFANeedsNoEnrollment(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("UPDATE organizations SET mfa_required").
		WithArgs(false, int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM organizations WHERE id").WillReturnRows(orgRow())

	rec, req := updateOrgRequest(t, `{"mfa_required":false}`)
	handleUpdateOrganization(services.NewAuthService(db)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("disabling enforcement must not require enrollment, got %d (body %s)", rec.Code, rec.Body.String())
	}
	// No totp_enabled lookup: sqlmock reports one if the handler ran it.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected queries: %v", err)
	}
}
