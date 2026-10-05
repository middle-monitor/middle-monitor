package services

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"

	"middle-monitor/backend/models"
)

func TestValidateEmail_Valid(t *testing.T) {
	if err := validateEmail("user@example.com"); err != nil {
		t.Fatalf("expected valid email, got %v", err)
	}
}

func TestValidateEmail_Invalid_NoAt(t *testing.T) {
	if err := validateEmail("notanemail"); err == nil {
		t.Fatal("expected error for no @")
	}
}

func TestValidateEmail_Invalid_NoTLD(t *testing.T) {
	if err := validateEmail("user@domain"); err == nil {
		t.Fatal("expected error for missing TLD")
	}
}

func TestValidatePassword_Valid(t *testing.T) {
	if err := validatePassword("Password1"); err != nil {
		t.Fatalf("expected valid password, got %v", err)
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	if err := validatePassword("Abc1"); err == nil {
		t.Fatal("expected error for short password")
	}
}

func TestValidatePassword_NoUpper(t *testing.T) {
	if err := validatePassword("password1"); err == nil {
		t.Fatal("expected error for no uppercase")
	}
}

func TestValidatePassword_NoLower(t *testing.T) {
	if err := validatePassword("PASSWORD1"); err == nil {
		t.Fatal("expected error for no lowercase")
	}
}

func TestValidatePassword_NoNumber(t *testing.T) {
	if err := validatePassword("Passwordx"); err == nil {
		t.Fatal("expected error for no number")
	}
}

func TestGenerateSlug_Basic(t *testing.T) {
	slug := generateSlug("My Organization")
	if slug != "my-organization" {
		t.Fatalf("want my-organization, got %q", slug)
	}
}

func TestGenerateSlug_SpecialChars(t *testing.T) {
	slug := generateSlug("Org & Co!")
	if strings.Contains(slug, "&") || strings.Contains(slug, "!") {
		t.Fatalf("special chars not removed: %q", slug)
	}
}

func TestGenerateSlug_MultipleHyphens(t *testing.T) {
	slug := generateSlug("A--B")
	if strings.Contains(slug, "--") {
		t.Fatalf("consecutive hyphens not collapsed: %q", slug)
	}
}

func TestGenerateSlug_EmptyFallsBackToOrg(t *testing.T) {
	slug := generateSlug("!@#")
	if slug != "org" {
		t.Fatalf("want 'org' for empty result, got %q", slug)
	}
}

func TestGenerateSlug_LeadingTrailingHyphens(t *testing.T) {
	slug := generateSlug("-name-")
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		t.Fatalf("leading/trailing hyphens not trimmed: %q", slug)
	}
}

func TestGenerateRandomString_Length(t *testing.T) {
	for _, n := range []int{8, 16, 32} {
		s := generateRandomString(n)
		if len(s) != n {
			t.Fatalf("want length %d, got %d", n, len(s))
		}
	}
}

func TestGenerateRandomString_Exported(t *testing.T) {
	s := GenerateRandomString(10)
	if len(s) != 10 {
		t.Fatalf("want length 10, got %d", len(s))
	}
}

func TestValidateToken_InvalidJWT(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, _ := newDB(t)
	svc := NewAuthService(db)
	_, err := svc.ValidateToken("not.a.jwt")
	if err == nil {
		t.Fatal("expected error for invalid JWT")
	}
}

func TestValidateToken_Valid(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, _ := newDB(t)
	svc := NewAuthService(db)

	// Build a valid token using the same secret
	user := &models.User{ID: 1, OrganizationID: 2, Role: "admin"}
	tokens, err := svc.generateTokens(user, "myorg", false)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.ValidateToken(tokens.AccessToken)
	if err != nil {
		t.Fatalf("expected valid token: %v", err)
	}
	if claims.UserID != 1 {
		t.Fatalf("want UserID 1, got %d", claims.UserID)
	}
}

func TestDeleteUser_CannotDeleteSelf(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, _ := newDB(t)
	svc := NewAuthService(db)
	err := svc.DeleteUser(1, 42, 42)
	if err == nil || !strings.Contains(err.Error(), "cannot delete your own account") {
		t.Fatalf("expected self-delete error, got %v", err)
	}
}

func TestDeleteUser_UserNotFound(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock := newDB(t)
	svc := NewAuthService(db)
	mock.ExpectExec("DELETE FROM memberships").WillReturnResult(sqlmock.NewResult(0, 0))
	err := svc.DeleteUser(1, 99, 1)
	if err != ErrUserNotFound {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

func TestDeleteUser_DBError(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock := newDB(t)
	svc := NewAuthService(db)
	mock.ExpectExec("DELETE FROM memberships").WillReturnError(sql.ErrConnDone)
	err := svc.DeleteUser(1, 99, 1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestDeleteUser_Success_RemovesMembership(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock := newDB(t)
	svc := NewAuthService(db)
	// Membership removed; the identity still belongs to another org, so it is kept
	// (and its home org is repointed in case it pointed to the removed org).
	mock.ExpectExec("DELETE FROM memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec("UPDATE users SET organization_id").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := svc.DeleteUser(1, 99, 1); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestDeleteUser_Success_DeletesOrphanIdentity(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock := newDB(t)
	svc := NewAuthService(db)
	// Last membership removed → the orphaned identity is deleted too.
	mock.ExpectExec("DELETE FROM memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM users").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.DeleteUser(1, 99, 1); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestRegister_InvalidEmail(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, _, err := svc.Register(models.RegisterRequest{Email: "bad", Password: "Password1!", Name: "A", OrganizationName: "Org"})
	if err != ErrInvalidEmail {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "weak", Name: "A", OrganizationName: "Org"})
	if err != ErrWeakPassword {
		t.Fatalf("expected ErrWeakPassword, got %v", err)
	}
}

func TestRegister_EmptyName(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "  ", OrganizationName: "Org"})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestRegister_EmptyOrgName(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "John", OrganizationName: ""})
	if err == nil || !strings.Contains(err.Error(), "organization name is required") {
		t.Fatalf("expected org name error, got %v", err)
	}
}

func TestRegister_UserExists(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("SELECT id FROM users WHERE email").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5)) // user found → exists
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "John", OrganizationName: "Org"})
	if err != ErrUserExists {
		t.Fatalf("expected ErrUserExists, got %v", err)
	}
}

func TestRegister_UserCheckDBError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrConnDone)
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "John", OrganizationName: "Org"})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestRegister_OrgCheckDBError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(sql.ErrConnDone)
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "John", OrganizationName: "Org"})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestRegister_Success(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	trialEnd := now.AddDate(0, 0, TrialDays)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO organizations").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at"}).
			AddRow(1, "Org", "org", "free", trialEnd, now, now))
	mock.ExpectExec("INSERT INTO host_groups").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("INSERT INTO users").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "email", "name", "role", "email_verified", "created_at", "updated_at"}).
			AddRow(1, 1, "a@b.com", "John", "admin", false, now, now))
	mock.ExpectExec("INSERT INTO memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	result, tokens, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "John", OrganizationName: "Org"})
	if err != nil || result == nil || tokens == nil {
		t.Fatalf("expected success: %v/%v/%v", result, tokens, err)
	}
	// Signing up opens a Pro trial: the org is billed as free but must be gated as pro.
	if result.Organization.Plan != "pro" || result.Organization.TrialEndsAt == nil {
		t.Fatalf("expected a running pro trial, got plan %q / trial %v", result.Organization.Plan, result.Organization.TrialEndsAt)
	}
}

func TestRegister_SlugConflict_AddsRandomSuffix(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	trialEnd := now.AddDate(0, 0, TrialDays)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	// Slug exists → conflict path
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO organizations").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at"}).
			AddRow(2, "Org", "org-abc123", "free", trialEnd, now, now))
	mock.ExpectExec("INSERT INTO host_groups").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("INSERT INTO users").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "email", "name", "role", "email_verified", "created_at", "updated_at"}).
			AddRow(2, 2, "b@b.com", "Jane", "admin", false, now, now))
	mock.ExpectExec("INSERT INTO memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	_, _, err := svc.Register(models.RegisterRequest{Email: "b@b.com", Password: "Password1!", Name: "Jane", OrganizationName: "Org"})
	if err != nil {
		t.Fatalf("expected success with slug suffix: %v", err)
	}
}

func TestRegister_BeginError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin().WillReturnError(sql.ErrConnDone)
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "J", OrganizationName: "O"})
	if err == nil {
		t.Fatal("expected begin error")
	}
}

func TestRegister_InsertOrgError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO organizations").WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "J", OrganizationName: "O"})
	if err == nil {
		t.Fatal("expected insert org error")
	}
}

func TestRegister_InsertUserError(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	trialEnd := now.AddDate(0, 0, TrialDays)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO organizations").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at"}).
			AddRow(1, "O", "o", "free", trialEnd, now, now))
	mock.ExpectQuery("INSERT INTO users").WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "J", OrganizationName: "O"})
	if err == nil {
		t.Fatal("expected insert user error")
	}
}

func TestRegister_CommitError(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	trialEnd := now.AddDate(0, 0, TrialDays)
	mock.ExpectQuery("SELECT id FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO organizations").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "plan", "trial_ends_at", "created_at", "updated_at"}).
			AddRow(1, "O", "o", "free", trialEnd, now, now))
	mock.ExpectQuery("INSERT INTO users").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "email", "name", "role", "email_verified", "created_at", "updated_at"}).
			AddRow(1, 1, "a@b.com", "J", "admin", false, now, now))
	mock.ExpectExec("INSERT INTO memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(sql.ErrConnDone)
	_, _, err := svc.Register(models.RegisterRequest{Email: "a@b.com", Password: "Password1!", Name: "J", OrganizationName: "O"})
	if err == nil {
		t.Fatal("expected commit error")
	}
}

func TestLogin_UserNotFound(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM users").WillReturnError(sql.ErrNoRows)
	_, _, _, err := svc.Login(models.LoginRequest{Email: "a@b.com", Password: "Password1!"})
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_DBError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM users").WillReturnError(sql.ErrConnDone)
	_, _, _, err := svc.Login(models.LoginRequest{Email: "a@b.com", Password: "Password1!"})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	hash, _ := bcrypt.GenerateFromPassword([]byte("Password1!"), bcrypt.MinCost)
	mock.ExpectQuery("FROM users").WillReturnRows(sqlmock.NewRows(loginIdentityCols).
		AddRow(1, 1, "a@b.com", string(hash), "John", true, false, now, now))
	_, _, _, err := svc.Login(models.LoginRequest{Email: "a@b.com", Password: "WrongPass1!"})
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

// A single-org user logs straight into their org; the role comes from the membership.
func TestLogin_Success(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	hash, _ := bcrypt.GenerateFromPassword([]byte("Password1!"), bcrypt.MinCost)
	mock.ExpectQuery("FROM users").WillReturnRows(sqlmock.NewRows(loginIdentityCols).
		AddRow(1, 1, "a@b.com", string(hash), "John", true, false, now, now))
	mock.ExpectQuery("FROM memberships").WillReturnRows(sqlmock.NewRows(userOrgCols).
		AddRow(1, "Org", "org", "free", nil, false, now, now, "admin"))
	mock.ExpectExec("UPDATE users SET last_login_at").WillReturnResult(sqlmock.NewResult(1, 1))
	result, tokens, _, err := svc.Login(models.LoginRequest{Email: "A@B.COM", Password: "Password1!"})
	if err != nil || result == nil || tokens == nil {
		t.Fatalf("expected success: %v/%v/%v", result, tokens, err)
	}
	if result.User.Role != "admin" {
		t.Fatalf("expected membership role admin, got %q", result.User.Role)
	}
}

func TestLogin_LastLoginUpdateError_NonFatal(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	hash, _ := bcrypt.GenerateFromPassword([]byte("Password1!"), bcrypt.MinCost)
	mock.ExpectQuery("FROM users").WillReturnRows(sqlmock.NewRows(loginIdentityCols).
		AddRow(1, 1, "a@b.com", string(hash), "John", true, false, now, now))
	mock.ExpectQuery("FROM memberships").WillReturnRows(sqlmock.NewRows(userOrgCols).
		AddRow(1, "Org", "org", "free", nil, false, now, now, "admin"))
	mock.ExpectExec("UPDATE users SET last_login_at").WillReturnError(sql.ErrConnDone)
	result, tokens, _, err := svc.Login(models.LoginRequest{Email: "a@b.com", Password: "Password1!"})
	// Non-fatal → still succeeds
	if err != nil || result == nil || tokens == nil {
		t.Fatalf("expected success despite last_login error: %v/%v/%v", result, tokens, err)
	}
}

func TestGetUserByID_Found(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	mock.ExpectQuery("SELECT u.id").WillReturnRows(sqlmock.NewRows([]string{
		"u.id", "u.organization_id", "u.email", "u.name", "u.role", "u.email_verified", "u.totp_enabled", "u.created_at", "u.updated_at", "u.last_login_at",
		"o.id", "o.name", "o.slug", "o.plan", "o.trial_ends_at", "o.mfa_required", "o.created_at", "o.updated_at",
	}).AddRow(1, 1, "a@b.com", "John", "admin", true, false, now, now, nil, 1, "Org", "org", "free", nil, false, now, now))
	user, err := svc.GetUserByID(1)
	if err != nil || user.User.ID != 1 {
		t.Fatalf("expected user: %v/%v", user, err)
	}
}

func TestGetUserByID_NotFound(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("SELECT u.id").WillReturnError(sql.ErrNoRows)
	_, err := svc.GetUserByID(99)
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestGetUserByID_DBError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("SELECT u.id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetUserByID(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestRefreshTokens_InvalidToken(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, _, err := svc.RefreshTokens("invalid.token.string")
	if err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

// Refresh keeps the active org carried by the token (resolved via GetUserInOrg).
func TestRefreshTokens_Success(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	user := &models.User{ID: 1, OrganizationID: 1, Role: "admin"}
	tokens, _ := svc.generateTokens(user, "org", false)
	mock.ExpectQuery("FROM memberships").WillReturnRows(sqlmock.NewRows(getUserInOrgCols).
		AddRow(1, "a@b.com", "John", "admin", true, false, now, now, nil, 1, "Org", "org", "free", nil, false, now, now))
	result, newTokens, err := svc.RefreshTokens(tokens.RefreshToken)
	if err != nil || result == nil || newTokens == nil {
		t.Fatalf("expected success: %v/%v/%v", result, newTokens, err)
	}
}

// Membership revoked since the token was minted and no other org remains → the
// refresh is rejected so the user is forced to re-authenticate.
func TestRefreshTokens_RevokedMembership_NoOrg(t *testing.T) {
	svc, mock := newTestAuth(t)
	user := &models.User{ID: 99, OrganizationID: 1, Role: "admin"}
	tokens, _ := svc.generateTokens(user, "org", false)
	// GetUserInOrg → no membership for the active org.
	mock.ExpectQuery("FROM memberships").WillReturnError(sql.ErrNoRows)
	// defaultOrg fallback → the user belongs to no org at all.
	mock.ExpectQuery("FROM memberships").WillReturnRows(sqlmock.NewRows(userOrgCols))
	_, _, err := svc.RefreshTokens(tokens.RefreshToken)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestGetOrganizationUsers_Empty(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM memberships").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "email", "name", "role", "created_at", "updated_at", "last_login_at", "pending",
	}))
	users, err := svc.GetOrganizationUsers(1)
	if err != nil || len(users) != 0 {
		t.Fatalf("expected empty: %v/%v", users, err)
	}
}

func TestGetOrganizationUsers_DBError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM memberships").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetOrganizationUsers(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestInviteUser_InvalidEmail(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, _, err := svc.InviteUser(1, models.InviteUserRequest{Email: "bad", Name: "John", Role: "read_write"})
	if err != ErrInvalidEmail {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
}

func TestInviteUser_EmptyName(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, _, err := svc.InviteUser(1, models.InviteUserRequest{Email: "a@b.com", Name: "", Role: "read_write"})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name error, got %v", err)
	}
}

// An existing identity already a member of the target org → ErrUserExists.
func TestInviteUser_UserExists(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM users WHERE email").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name"}).AddRow(1, "a@b.com", "John"))
	mock.ExpectQuery("FROM memberships WHERE user_id").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	_, _, err := svc.InviteUser(1, models.InviteUserRequest{Email: "a@b.com", Name: "John", Role: "read_write"})
	if err != ErrUserExists {
		t.Fatalf("expected ErrUserExists, got %v", err)
	}
}

// An existing identity not yet a member → a membership is added, no token returned.
func TestInviteUser_ExistingIdentity_AddsMembership(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM users WHERE email").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name"}).AddRow(1, "a@b.com", "John"))
	mock.ExpectQuery("FROM memberships WHERE user_id").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	user, token, err := svc.InviteUser(2, models.InviteUserRequest{Email: "a@b.com", Name: "John", Role: "admin"})
	if err != nil || user == nil {
		t.Fatalf("expected success: %v/%v", user, err)
	}
	if token != "" {
		t.Fatal("expected no invite token for an existing identity")
	}
	if user.Role != "admin" {
		t.Fatalf("expected per-org role admin, got %q", user.Role)
	}
}

func TestInviteUser_DBCheckError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM users WHERE email").WillReturnError(sql.ErrConnDone)
	_, _, err := svc.InviteUser(1, models.InviteUserRequest{Email: "a@b.com", Name: "John", Role: "read_write"})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

// A new email creates a pending identity + its membership in a transaction, and
// returns a single-use token for the activation email. An unknown role falls back
// to read_write.
func TestInviteUser_Success_DefaultRole(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	mock.ExpectQuery("FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "email", "name", "role", "email_verified", "created_at", "updated_at"}).
			AddRow(1, 1, "a@b.com", "John", "read_write", false, now, now))
	mock.ExpectExec("INSERT INTO memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	user, token, err := svc.InviteUser(1, models.InviteUserRequest{Email: "a@b.com", Name: "John", Role: "badRole"})
	if err != nil || user == nil {
		t.Fatalf("expected success: %v/%v", user, err)
	}
	if token == "" {
		t.Fatal("expected a non-empty invite token")
	}
	if !user.Pending {
		t.Fatal("expected invited user to be pending")
	}
}

func TestInviteUser_Admin_Success(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	mock.ExpectQuery("FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "email", "name", "role", "email_verified", "created_at", "updated_at"}).
			AddRow(2, 1, "b@b.com", "Jane", "admin", false, now, now))
	mock.ExpectExec("INSERT INTO memberships").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	user, _, err := svc.InviteUser(1, models.InviteUserRequest{Email: "b@b.com", Name: "Jane", Role: "admin"})
	if err != nil || user == nil {
		t.Fatalf("expected success: %v/%v", user, err)
	}
}

func TestInviteUser_InsertError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("FROM users WHERE email").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	_, _, err := svc.InviteUser(1, models.InviteUserRequest{Email: "a@b.com", Name: "John", Role: "read_write"})
	if err == nil {
		t.Fatal("expected insert error")
	}
}

func TestUpdateOrganization_Success(t *testing.T) {
	svc, mock := newTestAuth(t)
	now := time.Now()
	mock.ExpectQuery("UPDATE organizations").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "created_at", "updated_at"}).
			AddRow(1, "New Name", "new-name", now, now))
	org, err := svc.UpdateOrganization(1, "New Name")
	if err != nil || org.Name != "New Name" {
		t.Fatalf("expected updated org: %v/%v", org, err)
	}
}

func TestUpdateOrganization_NotFound(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("UPDATE organizations").WillReturnError(sql.ErrNoRows)
	_, err := svc.UpdateOrganization(1, "New Name")
	if err != ErrOrgNotFound {
		t.Fatalf("expected ErrOrgNotFound, got %v", err)
	}
}

func TestUpdateOrganization_DBError(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("UPDATE organizations").WillReturnError(sql.ErrConnDone)
	_, err := svc.UpdateOrganization(1, "Name")
	if err == nil {
		t.Fatal("expected DB error")
	}
}

// IssuePasswordReset must NOT reveal whether an email is registered: an unknown
// (or pending) account is a silent no-op, not an error, so the endpoint can't be
// used to enumerate users.
func TestIssuePasswordReset_UnknownEmail_NoLeak(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("UPDATE users SET reset_token").WillReturnError(sql.ErrNoRows)
	token, name, err := svc.IssuePasswordReset("ghost@b.com")
	if err != nil {
		t.Fatalf("unknown email must not error (would leak existence): %v", err)
	}
	if token != "" || name != "" {
		t.Fatalf("expected empty token/name for unknown email, got %q/%q", token, name)
	}
}

func TestIssuePasswordReset_Success(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("UPDATE users SET reset_token").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("John"))
	token, name, err := svc.IssuePasswordReset("a@b.com")
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty reset token to email")
	}
	if name != "John" {
		t.Fatalf("expected name John, got %q", name)
	}
}

// A weak new password must be rejected before any DB write: the reset flow can't
// be a backdoor around the password policy.
func TestResetPassword_WeakPassword(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.ResetPassword("sometoken", "weak"); err != ErrWeakPassword {
		t.Fatalf("expected ErrWeakPassword, got %v", err)
	}
}

func TestResetPassword_EmptyToken(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.ResetPassword("", "Password1!"); err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

// An expired or unknown token matches no row (the 1h window is enforced in SQL):
// the caller must see ErrInvalidToken, never a successful reset.
func TestResetPassword_ExpiredOrInvalidToken(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("UPDATE users").WillReturnError(sql.ErrNoRows)
	if err := svc.ResetPassword("stale", "Password1!"); err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestResetPassword_Success(t *testing.T) {
	svc, mock := newTestAuth(t)
	mock.ExpectQuery("UPDATE users").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	if err := svc.ResetPassword("validtoken", "Password1!"); err != nil {
		t.Fatalf("expected success: %v", err)
	}
}

func TestAuthService_DB_Returns_DB(t *testing.T) {
	svc, _ := newTestAuth(t)
	if svc.DB() == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestAuthService_GetOrganizationUsers_ScanError(t *testing.T) {
	svc, mock := newTestAuth(t)
	// Return a row where last_login_at column is a non-nullable string that cannot scan
	// into *time.Time, triggering the scan error branch.
	rows := sqlmock.NewRows([]string{
		"id", "organization_id", "email", "name", "role", "created_at", "updated_at", "last_login_at", "pending",
	}).AddRow(1, 1, "a@b.com", "A", "read_write", time.Now(), time.Now(), "not-a-time", false)
	mock.ExpectQuery("FROM memberships").WillReturnRows(rows)
	_, err := svc.GetOrganizationUsers(1)
	if err == nil {
		t.Fatal("expected scan error")
	}
}
