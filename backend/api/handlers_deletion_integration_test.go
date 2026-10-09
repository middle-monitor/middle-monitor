package api

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// Deletion is checked against a real PostgreSQL: what matters is what the
// cascades actually remove, which a sqlmock replay cannot tell.

func seedUser(t *testing.T, db *sql.DB, email string, homeOrg int64, password string) int64 {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (organization_id, email, password_hash, name, role, email_verified) VALUES ($1, $2, $3, $2, 'admin', true) RETURNING id`,
		homeOrg, email, string(hash),
	).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedMembership(t *testing.T, db *sql.DB, userID, orgID int64, role string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO memberships (user_id, organization_id, role) VALUES ($1, $2, $3)`, userID, orgID, role); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

func asUser(r *http.Request, userID, orgID int64) *http.Request {
	claims := &services.JWTClaims{UserID: userID, OrganizationID: orgID}
	return r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, claims))
}

func countRows(t *testing.T, db *sql.DB, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestIntegrationDeleteOrganizationKeepsMembersOfOtherOrganizations(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	acme := seedPlatformOrg(t, db, "acme", "free")
	globex := seedPlatformOrg(t, db, "globex", "free")

	// Both users have acme as home org: the users cascade would take them both.
	shared := seedUser(t, db, "shared@acme.io", acme, "Password1")
	seedMembership(t, db, shared, acme, "admin")
	seedMembership(t, db, shared, globex, "admin")
	onlyAcme := seedUser(t, db, "only@acme.io", acme, "Password1")
	seedMembership(t, db, onlyAcme, acme, "read_write")
	if _, err := db.Exec(`INSERT INTO install_tokens (organization_id, token) VALUES ($1, 'tok-acme')`, acme); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	req := asUser(httptest.NewRequest(http.MethodDelete, "/", bytes.NewBufferString(`{"confirm":"acme"}`)), shared, acme)
	rec := httptest.NewRecorder()
	handleDeleteOrganization(db, services.NewAuthService(db), nil)(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM organizations WHERE id = $1`, acme); n != 0 {
		t.Error("organization still exists")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM install_tokens WHERE organization_id = $1`, acme); n != 0 {
		t.Error("an agent install token survived: agents could keep reporting into a deleted org")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = $1 AND organization_id = $2`, shared, globex); n != 1 {
		t.Error("a member of another organization was deleted with this one")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = $1`, onlyAcme); n != 0 {
		t.Error("a member left with no organization kept an account")
	}
}

func TestIntegrationDeleteOrganizationRequiresTheSlug(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	acme := seedPlatformOrg(t, db, "acme", "free")
	admin := seedUser(t, db, "admin@acme.io", acme, "Password1")
	seedMembership(t, db, admin, acme, "admin")

	req := asUser(httptest.NewRequest(http.MethodDelete, "/", bytes.NewBufferString(`{}`)), admin, acme)
	rec := httptest.NewRecorder()
	handleDeleteOrganization(db, services.NewAuthService(db), nil)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM organizations WHERE id = $1`, acme); n != 1 {
		t.Error("an unconfirmed request deleted the organization")
	}
}

func TestIntegrationDeleteAccountTakesItsSoloOrganizationsOnly(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	solo := seedPlatformOrg(t, db, "solo", "free")
	team := seedPlatformOrg(t, db, "team", "free")
	me := seedUser(t, db, "me@solo.io", solo, "Password1")
	seedMembership(t, db, me, solo, "admin")
	seedMembership(t, db, me, team, "read_write")
	teammate := seedUser(t, db, "mate@team.io", team, "Password1")
	seedMembership(t, db, teammate, team, "admin")

	wrong := asUser(httptest.NewRequest(http.MethodDelete, "/", bytes.NewBufferString(`{"password":"nope"}`)), me, solo)
	rec := httptest.NewRecorder()
	handleDeleteAccount(db, services.NewAuthService(db), nil)(rec, wrong)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong password: status = %d, want 403", rec.Code)
	}

	req := asUser(httptest.NewRequest(http.MethodDelete, "/", bytes.NewBufferString(`{"password":"Password1"}`)), me, solo)
	rec = httptest.NewRecorder()
	handleDeleteAccount(db, services.NewAuthService(db), nil)(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = $1`, me); n != 0 {
		t.Error("account still exists")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM organizations WHERE id = $1`, solo); n != 0 {
		t.Error("an organization with no member left survived the account")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM organizations WHERE id = $1`, team); n != 1 {
		t.Error("a shared organization was deleted with one of its members")
	}
}

// Deleting the last admin would leave the other members with nobody able to
// manage the organization, invite people or pay for it.
func TestIntegrationDeleteAccountRefusesTheLastAdminOfATeam(t *testing.T) {
	db := platformAdminIntegrationDB(t)
	team := seedPlatformOrg(t, db, "team", "free")
	admin := seedUser(t, db, "admin@team.io", team, "Password1")
	seedMembership(t, db, admin, team, "admin")
	member := seedUser(t, db, "member@team.io", team, "Password1")
	seedMembership(t, db, member, team, "read_write")

	req := asUser(httptest.NewRequest(http.MethodDelete, "/", bytes.NewBufferString(`{"password":"Password1"}`)), admin, team)
	rec := httptest.NewRecorder()
	handleDeleteAccount(db, services.NewAuthService(db), nil)(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = $1`, admin); n != 1 {
		t.Error("the last admin was deleted anyway")
	}
}
