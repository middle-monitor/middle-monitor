package services

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func authService(t *testing.T) (*AuthService, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	return NewAuthService(db), mock
}

// The role string reaches an authorization decision. Anything that is not one
// of the three known roles has to be refused at the door rather than stored and
// later compared against, where an unknown value silently grants nothing or,
// worse, matches a future check.
func TestValidRole(t *testing.T) {
	for _, role := range []string{"read_only", "read_write", "admin"} {
		if !ValidRole(role) {
			t.Fatalf("%q is a supported role", role)
		}
	}
	for _, role := range []string{"", "Admin", "ADMIN", "owner", "superuser", "admin ", "read-only"} {
		if ValidRole(role) {
			t.Fatalf("%q must not be accepted as a role", role)
		}
	}
}

// An organization with no admin left is an organization nobody can administer
// again: no one could invite, change a role, or manage billing. The demotion of
// the last admin has to be refused.
func TestUpdateUserRoleRefusesToDemoteTheLastAdmin(t *testing.T) {
	svc, mock := authService(t)

	mock.ExpectQuery("SELECT role FROM memberships").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	err := svc.UpdateUserRole(1, 7, "read_only")
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("got %v, want ErrLastAdmin", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("nothing should have been written: %v", err)
	}
}

// With another admin in place the demotion is safe and must go through, on both
// the membership and the denormalized copy on the user row.
func TestUpdateUserRoleAllowsDemotionWhenAnotherAdminRemains(t *testing.T) {
	svc, mock := authService(t)

	mock.ExpectQuery("SELECT role FROM memberships").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec("UPDATE memberships SET role").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE users SET role").WillReturnResult(sqlmock.NewResult(0, 1))

	if err := svc.UpdateUserRole(1, 7, "read_only"); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("both copies of the role must be written: %v", err)
	}
}

// Promoting to admin never reduces the admin count, so it must not pay for the
// count query, and must never be blocked by the last-admin rule.
func TestUpdateUserRolePromotionSkipsTheLastAdminCheck(t *testing.T) {
	svc, mock := authService(t)

	mock.ExpectQuery("SELECT role FROM memberships").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("read_only"))
	mock.ExpectExec("UPDATE memberships SET role").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE users SET role").WillReturnResult(sqlmock.NewResult(0, 1))

	if err := svc.UpdateUserRole(1, 7, "admin"); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

// Re-submitting the role a member already has is a no-op, not a write. The form
// sends the whole member row, so this is the common case on any other edit.
func TestUpdateUserRoleIsANoOpWhenTheRoleIsUnchanged(t *testing.T) {
	svc, mock := authService(t)

	mock.ExpectQuery("SELECT role FROM memberships").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))

	if err := svc.UpdateUserRole(1, 7, "admin"); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an unchanged role must write nothing: %v", err)
	}
}

// A user id that is not a member of this organization must be reported as not
// found, not silently granted a membership by the update.
func TestUpdateUserRoleRejectsANonMember(t *testing.T) {
	svc, mock := authService(t)

	mock.ExpectQuery("SELECT role FROM memberships").WillReturnError(sql.ErrNoRows)

	err := svc.UpdateUserRole(1, 7, "admin")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("got %v, want ErrUserNotFound", err)
	}
}

// An unknown role is refused before any query runs: the check is on the input,
// not on what the database happens to contain.
func TestUpdateUserRoleRejectsAnUnknownRole(t *testing.T) {
	svc, mock := authService(t)

	err := svc.UpdateUserRole(1, 7, "superuser")
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("got %v, want ErrInvalidRole", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an invalid role must not reach the database: %v", err)
	}
}

// The slug becomes the organization's URL segment, so it has to stay inside the
// character set the router and the links assume.
func TestValidateSlug(t *testing.T) {
	valid := []string{"a", "acme", "acme-corp", "acme-corp-2", "a1", "0", strRepeat("a", 63)}
	for _, slug := range valid {
		if err := validateSlug(slug); err != nil {
			t.Fatalf("%q should be valid, got %v", slug, err)
		}
	}

	invalid := []string{
		"",                 // empty
		strRepeat("a", 64), // too long
		"Acme",             // uppercase
		"acme_corp",        // underscore
		"-acme",            // leading hyphen
		"acme-",            // trailing hyphen
		"acme--corp",       // doubled hyphen
		"acme corp",        // space
		"acme.corp",        // dot
		"acme/corp",        // path separator
		"../etc",           // traversal
	}
	for _, slug := range invalid {
		if err := validateSlug(slug); !errors.Is(err, ErrInvalidSlug) {
			t.Fatalf("%q should be rejected, got %v", slug, err)
		}
	}
}

func strRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
