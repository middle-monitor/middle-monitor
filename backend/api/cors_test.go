package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// A browser SDK runs on customer domains nobody can enumerate ahead of time.
// If ingest ever answers without this header, every report from every customer
// frontend is dropped by the browser before it leaves the page.
func TestIngestCORSAllowsAnUnknownCustomerOrigin(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/errors", nil)
	req.Header.Set("Origin", "https://shop.customer-we-never-heard-of.com")

	rec := httptest.NewRecorder()
	ingestCORSMiddleware(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

// The SDK sends an Authorization header, which makes every report a preflighted
// request. Rejecting the preflight blocks the POST that follows.
func TestIngestCORSAnswersThePreflightTheSDKSends(t *testing.T) {
	req := httptest.NewRequest("OPTIONS", "/api/v1/errors", nil)
	req.Header.Set("Origin", "https://shop.customer-we-never-heard-of.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	rec := httptest.NewRecorder()
	called := false
	ingestCORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 200", rec.Code)
	}
	if called {
		t.Fatal("preflight reached the handler: it must be answered by the middleware")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("preflight allows no headers: the SDK's Authorization header would be rejected")
	}
}

// The dashboard API serves user data to a logged-in session. Widening it to "*"
// would let any site read an org's data, which is why ingest has its own
// middleware instead of relaxing this one.
func TestDashboardCORSStillRefusesAnUnknownOrigin(t *testing.T) {
	os.Setenv("FRONTEND_URL", "https://app.middlemonitor.io")
	defer os.Unsetenv("FRONTEND_URL")

	req := httptest.NewRequest("GET", "/api/v1/organizations/acme", nil)
	req.Header.Set("Origin", "https://evil.example.com")

	rec := httptest.NewRecorder()
	corsMiddleware(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty for an unknown origin", got)
	}
}
