package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"

	"middle-monitor/backend/services"
)

// newAuthService builds an AuthService over a mock database. The secret is set
// because the constructor refuses to boot without one.
func newAuthService(t *testing.T) (*services.AuthService, sqlmock.Sqlmock, func()) {
	t.Helper()
	t.Setenv("JWT_SECRET", strings.Repeat("t", 48))
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	mock.MatchExpectationsInOrder(false)
	return services.NewAuthService(db), mock, func() { db.Close() }
}

// anyQueryFails makes every statement fail, which is how the "backend down"
// branch of an auth handler is reached.
func anyQueryFails(mock sqlmock.Sqlmock) {
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
		mock.ExpectExec(".*").WillReturnError(errors.New("db down"))
	}
}

// Public auth endpoints take a JSON body from an unauthenticated caller: a
// malformed one must be rejected before any lookup.
func TestAuthEndpointsRejectMalformedBodies(t *testing.T) {
	auth, _, closeDB := newAuthService(t)
	defer closeDB()

	handlers := map[string]http.HandlerFunc{
		"register":         handleRegister(auth),
		"login":            handleLogin(auth),
		"login mfa":        handleLoginMFA(auth),
		"refresh":          handleRefreshToken(auth),
		"verify email":     handleVerifyEmail(auth),
		"accept invite":    handleAcceptInvitation(auth),
		"forgot password":  handleForgotPassword(auth),
		"reset password":   handleResetPassword(auth),
		"enable 2fa":       handleEnable2FA(auth),
		"create token":     handleCreatePersonalToken(auth),
		"switch org":       handleSwitchOrg(auth),
		"update org":       handleUpdateOrganization(auth),
		"invite user":      handleInviteUser(auth),
		"update user role": handleUpdateUserRole(auth),
	}
	for name, handler := range handlers {
		req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", strings.NewReader("{")), 1, 7), map[string]string{"id": "9"})
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// Every account-scoped endpoint needs a session; without claims nothing can be
// resolved and the caller must get 401, never a 500 or a wrong user's data.
func TestAccountEndpointsRequireASession(t *testing.T) {
	auth, _, closeDB := newAuthService(t)
	defer closeDB()

	handlers := map[string]http.HandlerFunc{
		"setup 2fa":           handleSetup2FA(auth),
		"enable 2fa":          handleEnable2FA(auth),
		"disable 2fa":         handleDisable2FA(auth),
		"list tokens":         handleGetPersonalTokens(auth),
		"create token":        handleCreatePersonalToken(auth),
		"delete token":        handleDeletePersonalToken(auth),
		"me":                  handleGetMe(auth),
		"switch org":          handleSwitchOrg(auth),
		"resend verification": handleResendVerification(auth),
	}
	for name, handler := range handlers {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", name, rec.Code)
		}
	}
}

// Org-scoped endpoints resolve the org from the session; without one there is
// nothing to scope to.
func TestOrgEndpointsRequireAnOrganization(t *testing.T) {
	auth, _, closeDB := newAuthService(t)
	defer closeDB()
	db, _, _ := sqlmock.New()
	defer db.Close()

	handlers := map[string]http.HandlerFunc{
		"get org":     handleGetOrganization(db),
		"update org":  handleUpdateOrganization(auth),
		"test smtp":   handleTestSMTP(db),
		"list users":  handleGetOrganizationUsers(auth),
		"invite user": handleInviteUser(auth),
		"delete user": handleDeleteUser(auth),
		"update role": handleUpdateUserRole(auth),
	}
	for name, handler := range handlers {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", name, rec.Code)
		}
	}
}

// Registration failures are told apart because the client shows a different
// message for each: taken email, malformed email, weak password.
func TestRegisterClassifiesItsFailures(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	body := `{"email":"user@example.com","password":"Password1","name":"A","organization_name":"Acme"}`
	rec := httptest.NewRecorder()
	handleRegister(auth)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}

	// A malformed email never reaches the database.
	rec = httptest.NewRecorder()
	handleRegister(auth)(rec, httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"email":"not-an-email","password":"Password1","name":"A"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// Neither does a password that cannot meet the policy.
	rec = httptest.NewRecorder()
	handleRegister(auth)(rec, httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"email":"user@example.com","password":"short","name":"A","organization_name":"Acme"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// Wrong credentials must be a 401 and must not distinguish an unknown account
// from a wrong password.
func TestLoginRejectsWrongCredentialsWith401(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectQuery(".*").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleLogin(auth)(rec, httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"email":"user@example.com","password":"Password1"}`)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// The second 2FA step needs both halves; missing either is a client mistake,
// and an unknown challenge token means the login attempt has expired.
func TestLoginMFARequiresBothTokenAndCode(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	for _, body := range []string{`{"code":"123456"}`, `{"mfa_token":"t"}`, `{}`} {
		rec := httptest.NewRecorder()
		handleLoginMFA(auth)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	handleLoginMFA(auth)(rec, httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"mfa_token":"expired","code":"123456"}`)))
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

// A personal token with no expiry would be a perpetual credential; one already
// expired would be useless. Both are refused at the door.
func TestCreatePersonalTokenRequiresAFutureExpiry(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	cases := map[string]string{
		"no name":        `{"expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`,
		"no expiry":      `{"name":"ci"}`,
		"expiry in past": `{"name":"ci","expires_at":"` + past + `"}`,
	}
	for name, body := range cases {
		rec := httptest.NewRecorder()
		handleCreatePersonalToken(auth)(rec, orgRequest("POST", "/x", body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec := httptest.NewRecorder()
	handleCreatePersonalToken(auth)(rec, orgRequest("POST", "/x", `{"name":"ci","expires_at":"`+future+`"}`, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 from the failing store: %s", rec.Code, rec.Body.String())
	}
}

// Revoking someone else's token must read as "not found" rather than reveal
// that the id exists.
func TestDeletePersonalTokenIsScopedToTheOwner(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleDeletePersonalToken(auth)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleDeletePersonalToken(auth)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "9"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestListPersonalTokensReportsALookupFailure(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleGetPersonalTokens(auth)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// A session pointing at a user that no longer exists must be a 404, so the
// frontend logs out instead of rendering an empty dashboard.
func TestGetMeReportsAMissingUser(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectQuery(".*").WillReturnError(errNoRows())
	mock.ExpectQuery(".*").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleGetMe(auth)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

// A refresh token is the long-lived credential: an absent one is a client bug,
// an invalid one is a 401 and must never be a 500.
func TestRefreshTokenRejectsAnInvalidToken(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleRefreshToken(auth)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleRefreshToken(auth)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{"refresh_token":"nope"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// Switching into an org the user does not belong to is a 403, not a 500: it is
// an access decision, and the switcher shows it as such.
func TestSwitchOrgRefusesANonMember(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectQuery(".*").WillReturnError(errNoRows())
	mock.ExpectQuery(".*").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleSwitchOrg(auth)(rec, orgRequest("POST", "/x", `{"organization_id":999}`, nil))
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

// A spent or forged verification link must be rejected with the same message,
// so the link cannot be probed.
func TestVerifyEmailRejectsAnInvalidLink(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleVerifyEmail(auth)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{"token":"nope"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestResendVerificationReportsAMissingUser(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleResendVerification(auth)(rec, orgRequest("POST", "/x", `{}`, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// The forgot-password endpoint must answer the same way whether or not the
// account exists, or it becomes a user-enumeration oracle.
func TestForgotPasswordDoesNotRevealWhetherTheAccountExists(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

	rec := httptest.NewRecorder()
	handleForgotPassword(auth)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{"email":"ghost@example.com"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "If an account exists") {
		t.Fatalf("body %q", rec.Body.String())
	}
}

// A reset link that is spent, forged or paired with a weak password is a client
// error the form can act on, never a 500.
func TestResetPasswordClassifiesItsFailures(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectQuery(".*").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleResetPassword(auth)(rec, httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"token":"nope","password":"Password1"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// An invitation link that is spent or forged is a client error, and so is a
// password that cannot meet the policy.
func TestAcceptInvitationClassifiesItsFailures(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectQuery(".*").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleAcceptInvitation(auth)(rec, httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"token":"nope","password":"Password1"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// An org that no longer exists reads as absent, not as a backend failure.
func TestGetOrganizationReports404(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("FROM organizations WHERE id").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleGetOrganization(db)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// The SMTP password is write-only: it is stored encrypted and never travels
// back, only the flag saying one is set.
func TestLoadOrgNeverReturnsTheSMTPPassword(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	now := time.Now().UTC()

	mock.ExpectQuery("FROM organizations WHERE id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at",
		"alert_warning_enabled", "alert_critical_enabled", "mfa_required",
		"smtp_host", "smtp_port", "smtp_user", "smtp_from", "smtp_pass_enc",
	}).AddRow(int64(1), "Acme", "acme", "pro", nil, now, now, true, true, false,
		"smtp.example.com", "587", "user", "noreply@example.com", "encrypted-secret"))

	rec := httptest.NewRecorder()
	handleGetOrganization(db)(rec, orgRequest("GET", "/x", "", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "encrypted-secret") {
		t.Fatalf("the stored password leaked: %s", rec.Body.String())
	}
	var org map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &org)
	if org["smtp_configured"] != true {
		t.Fatalf("the caller cannot tell a password is set: %v", org)
	}
}

// Enforcing 2FA org-wide, or changing SMTP, are admin actions: a member doing
// either would change what every other member has to do to log in.
func TestOrgSettingsChangesAreAdminOnly(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	for name, body := range map[string]string{
		"mfa toggle": `{"mfa_required":true}`,
		"smtp host":  `{"smtp_host":"smtp.example.com"}`,
		"smtp pass":  `{"smtp_pass":"secret"}`,
	} {
		req := orgContext(httptest.NewRequest("PUT", "/x", strings.NewReader(body)), 1, 7)
		rec := httptest.NewRecorder()
		handleUpdateOrganization(auth)(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", name, rec.Code)
		}
	}
}

// An admin who enforces 2FA without being enrolled locks themselves out; the
// conflict tells them to enroll first.
func TestEnforcingMFARequiresTheAdminToBeEnrolled(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	mock.ExpectQuery("totp_enabled").WillReturnRows(sqlmock.NewRows([]string{"totp_enabled"}).AddRow(false))

	rec := httptest.NewRecorder()
	handleUpdateOrganization(auth)(rec, adminRequest("PUT", "/x", `{"mfa_required":true}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// Turning enforcement off never requires enrollment: it is the way out of the
// gate, not into it.
func TestDisablingMFAEnforcementSkipsTheEnrollmentCheck(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectExec("UPDATE organizations SET mfa_required").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM organizations WHERE id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at",
		"alert_warning_enabled", "alert_critical_enabled", "mfa_required",
		"smtp_host", "smtp_port", "smtp_user", "smtp_from", "smtp_pass_enc",
	}).AddRow(int64(1), "Acme", "acme", "pro", nil, now, now, true, true, false, "", "", "", "", ""))

	rec := httptest.NewRecorder()
	handleUpdateOrganization(auth)(rec, adminRequest("PUT", "/x", `{"mfa_required":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

// Each SMTP field is written independently so a partial edit does not blank the
// rest of the configuration.
func TestSMTPFieldsAreUpdatedIndependently(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	now := time.Now().UTC()

	mock.ExpectExec("SET smtp_host").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET smtp_port").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET smtp_user").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET smtp_from").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET alert_warning_enabled").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET alert_critical_enabled").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM organizations WHERE id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at",
		"alert_warning_enabled", "alert_critical_enabled", "mfa_required",
		"smtp_host", "smtp_port", "smtp_user", "smtp_from", "smtp_pass_enc",
	}).AddRow(int64(1), "Acme", "acme", "pro", nil, now, now, true, true, false,
		"smtp.example.com", "587", "u", "f@example.com", ""))

	body := `{"smtp_host":" smtp.example.com ","smtp_port":"587","smtp_user":"u","smtp_from":"f@example.com",` +
		`"alert_warning_enabled":true,"alert_critical_enabled":false}`
	rec := httptest.NewRecorder()
	handleUpdateOrganization(auth)(rec, adminRequest("PUT", "/x", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("not every field was written: %v", err)
	}
}

// A test email is an admin action, and it needs somewhere to go.
func TestTestSMTPIsAdminOnlyAndNeedsARecipient(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleTestSMTP(db)(rec, orgRequest("POST", "/x", `{}`, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleTestSMTP(db)(rec, adminRequest("POST", "/x", `{}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 with no recipient", rec.Code)
	}
}

func TestListOrganizationUsersReportsALookupFailure(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleGetOrganizationUsers(auth)(rec, orgRequest("GET", "/x", "", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// An invitation to a malformed address cannot be delivered; saying so is more
// useful than a generic failure.
func TestInviteUserRejectsAMalformedEmail(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleInviteUser(auth)(rec, orgRequest("POST", "/x", `{"email":"not-an-email","role":"member"}`, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// Deleting your own account through the members list would leave the org
// without the admin who is using it.
func TestDeleteUserRefusesSelfAndValidatesTheID(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleDeleteUser(auth)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleDeleteUser(auth)(rec, orgRequest("DELETE", "/x", "", map[string]string{"id": "7"}))
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

// Changing your own role is how an admin demotes themselves out of the org;
// the role itself must also be one the system knows.
func TestUpdateUserRoleRefusesSelfAndUnknownRoles(t *testing.T) {
	auth, mock, closeDB := newAuthService(t)
	defer closeDB()
	anyQueryFails(mock)

	rec := httptest.NewRecorder()
	handleUpdateUserRole(auth)(rec, orgRequest("PUT", "/x", `{"role":"member"}`, map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleUpdateUserRole(auth)(rec, orgRequest("PUT", "/x", `{"role":"member"}`, map[string]string{"id": "7"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self demotion: status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleUpdateUserRole(auth)(rec, orgRequest("PUT", "/x", `{"role":"overlord"}`, map[string]string{"id": "9"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown role: status %d, want 400", rec.Code)
	}
}

// The stats endpoint powers the overview: a partially failing database must
// still produce a payload rather than blanking the whole page.
func TestOrganizationStatsSurvivesPartialFailures(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 12; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	rec := httptest.NewRecorder()
	handleGetOrganizationStats(db)(rec, orgRequest("GET", "/x", "", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var stats OrganizationStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Errors.ByService == nil {
		t.Fatal("by_service must be an object the frontend can iterate")
	}
}
