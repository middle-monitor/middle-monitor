package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"

	"middle-monitor/backend/services"
)

// muxRouter names the concrete router type these tests exercise.
type muxRouter = mux.Router

// muxNotFound is what gorilla/mux writes when no route matched at all, as
// opposed to a handler that ran and decided to answer 404.
const muxNotFound = "404 page not found\n"

func realRouter(t *testing.T) *muxRouter {
	t.Helper()
	if os.Getenv("JWT_SECRET") == "" {
		// NewAuthService refuses to start without one, and it is the router's
		// dependency rather than the subject of this test.
		os.Setenv("JWT_SECRET", strings.Repeat("t", 48))
	}
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return SetupAPIRouter(db, services.NewAuthService(db))
}

// The public routes have to be reachable through the router that is actually
// served, not just through their handler. `protected` is a PathPrefix("")
// subrouter, so it matches every /api/v1 path and swallows anything registered
// after it: openapi.json and the schemas shipped dead to production because
// every test called their handler directly.
func TestPublicRoutesAreReachableThroughTheRouter(t *testing.T) {
	router := realRouter(t)

	for _, path := range []string{
		"/healthz",
		"/readyz",
		"/api/v1/openapi.json",
		"/api/v1/schemas/webhook-payload.json",
		"/api/v1/schemas/agent-config.json",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if rec.Body.String() == muxNotFound {
			t.Errorf("%s is registered but no route matches it: check that it is declared "+
				"before the protected PathPrefix(\"\") subrouter in SetupAPIRouter", path)
		}
	}
}

// The specification is only useful if the document it describes can be fetched
// from the API it describes.
func TestTheSpecificationIsFetchableFromTheRouter(t *testing.T) {
	router := realRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"openapi"`) {
		t.Fatalf("the body served is not the specification: %.80s", rec.Body.String())
	}
}

// `/errors/{id}` matches anything `/errors/services` matches, so the literal
// path only survives while it is registered first. Nothing in the request tells
// the two apart: a caller asking for the service list would silently get the
// detail handler parsing "services" as an id, and answer 400.
func TestTheLiteralErrorRoutesWinOverTheErrorIDRoute(t *testing.T) {
	router := realRouter(t)
	const orgPrefix = "/api/v1/organizations/acme/errors"

	for path, want := range map[string]string{
		orgPrefix + "/services":      "/errors/services",
		orgPrefix + "/5/correlation": "/errors/{id}/correlation",
		orgPrefix + "/5":             "/errors/{id}",
	} {
		var match mux.RouteMatch
		if !router.Match(httptest.NewRequest("GET", path, nil), &match) {
			t.Errorf("%s matches no route at all", path)
			continue
		}
		template, err := match.Route.GetPathTemplate()
		if err != nil {
			t.Errorf("%s: path template: %v", path, err)
			continue
		}
		if !strings.HasSuffix(template, want) {
			t.Errorf("%s matched %s, want %s: check the registration order in SetupAPIRouter", path, template, want)
		}
	}
}

// The platform-admin routes are the most privileged surface of the instance:
// they read and rewrite every tenant's billing plan. They are mounted outside
// the org gate, so nothing but their own middleware stack stands in front of
// them — and the stack is one line in SetupAPIRouter that a refactor can drop
// without any handler test noticing. Exercised through the real router.
func TestPlatformAdminRoutesCarryTheEmailAndMFAGates(t *testing.T) {
	os.Setenv("JWT_SECRET", strings.Repeat("t", 48))
	t.Setenv("PLATFORM_ADMIN_EMAILS", "owner@example.com")
	router := realRouter(t)

	platformAdmin := func(email string, verified, mfaPending bool) string {
		claims := services.JWTClaims{
			UserID: 7, OrganizationID: 1, OrganizationSlug: "acme", Role: "admin",
			Email: email, EmailVerified: verified, MFAEnrollmentRequired: mfaPending,
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
			SignedString([]byte(strings.Repeat("t", 48)))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return token
	}

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"not on the list", platformAdmin("someone@example.com", true, false), http.StatusForbidden},
		{"email not verified", platformAdmin("owner@example.com", false, false), http.StatusForbidden},
		{"authenticator not enrolled", platformAdmin("owner@example.com", true, true), http.StatusForbidden},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/platform-admin/organizations", nil)
		req.Header.Set("Authorization", "Bearer "+c.token)
		router.ServeHTTP(rec, req)

		if rec.Body.String() == muxNotFound {
			t.Fatalf("%s: the platform-admin route is not reachable through the router", c.name)
		}
		if rec.Code != c.want {
			t.Errorf("%s: got %d, want %d — the gate is missing from the subrouter", c.name, rec.Code, c.want)
		}
	}
}
