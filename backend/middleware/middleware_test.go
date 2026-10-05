package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"

	"middle-monitor/backend/services"
)

// --- Recover middleware ---

func TestRecover_NoPanic_PassesThrough(t *testing.T) {
	handler := Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestRecover_PanicReturns500(t *testing.T) {
	handler := Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
}

// --- RateLimiter ---

func TestNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(10, 5)
	if rl == nil {
		t.Fatal("expected non-nil rate limiter")
	}
}

func TestRateLimiter_AllowsFirstRequest(t *testing.T) {
	rl := NewRateLimiter(10, 5)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rl.Middleware(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestRateLimiter_Blocks_WhenBurstExhausted(t *testing.T) {
	rl := NewRateLimiter(0.001, 1) // burst=1, rate=tiny
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5678"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// First request consumes the 1 burst token
	rec1 := httptest.NewRecorder()
	rl.Middleware(next).ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("want 200 on first request, got %d", rec1.Code)
	}
	// Second request is blocked
	rec2 := httptest.NewRecorder()
	rl.Middleware(next).ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec2.Code)
	}
}

func TestRateLimiter_ExistingBucket_TokenRefill(t *testing.T) {
	rl := NewRateLimiter(100, 2) // burst=2, fast refill
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:1111"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Exhaust burst
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		rl.Middleware(next).ServeHTTP(rec, req)
	}
	// Wait briefly for refill (rate=100/s → 10ms for 1 token)
	time.Sleep(15 * time.Millisecond)
	rec := httptest.NewRecorder()
	rl.Middleware(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 after refill, got %d", rec.Code)
	}
}

func TestRateLimiter_TokensCappedAtBurst(t *testing.T) {
	// Inject a bucket with 0 tokens and an old timestamp so refill exceeds burst.
	// This covers the `if b.tokens > rl.burst` cap branch in allow().
	rl := NewRateLimiter(1000, 5)
	rl.mu.Lock()
	rl.buckets["capip"] = &bucket{tokens: 0, last: time.Now().Add(-10 * time.Second)}
	rl.mu.Unlock()

	if !rl.allow("capip") {
		t.Fatal("expected allow after token cap refill")
	}
	rl.mu.Lock()
	tokens := rl.buckets["capip"].tokens
	rl.mu.Unlock()
	if tokens != 4 { // burst(5) - 1 consumed = 4
		t.Fatalf("expected 4 tokens after capped refill, got %v", tokens)
	}
}

func TestClientIP_CFConnectingIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	req.RemoteAddr = "127.0.0.1:2001"
	if got := clientIP(req); got != "1.2.3.4" {
		t.Fatalf("want 1.2.3.4, got %q", got)
	}
}

// A caller sending its own XFF must not pick the rate-limit bucket: Cloudflare
// appends to XFF, so its first hop is attacker-controlled.
func TestClientIP_SpoofedForwardedHeadersIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	req.Header.Set("X-Real-IP", "6.6.6.6")
	req.RemoteAddr = "192.168.1.1:9999"
	if got := clientIP(req); got != "192.168.1.1" {
		t.Fatalf("spoofable header won over RemoteAddr: got %q", got)
	}
}

// Same request through the tunnel: the forged XFF must lose to CF-Connecting-IP.
func TestClientIP_CFConnectingIPBeatsSpoofedXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	req.RemoteAddr = "127.0.0.1:2001"
	if got := clientIP(req); got != "203.0.113.9" {
		t.Fatalf("want 203.0.113.9, got %q", got)
	}
}

// A malformed value must not become a bucket key of its own.
func TestClientIP_CFConnectingIPInvalidFallsBack(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("CF-Connecting-IP", "not-an-ip")
	req.RemoteAddr = "192.168.1.1:9999"
	if got := clientIP(req); got != "192.168.1.1" {
		t.Fatalf("want 192.168.1.1, got %q", got)
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:9999"
	if got := clientIP(req); got != "192.168.1.1" {
		t.Fatalf("want 192.168.1.1, got %q", got)
	}
}

func TestClientIP_RemoteAddr_InvalidFormat(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-a-valid-addr"
	got := clientIP(req)
	// net.SplitHostPort fails → returns raw RemoteAddr
	if got != "not-a-valid-addr" {
		t.Fatalf("want raw addr, got %q", got)
	}
}

func TestRateLimiter_GC_Trigger(t *testing.T) {
	rl := NewRateLimiter(10, 100)
	// Set lastSweep far in the past to trigger GC
	rl.lastSweep = time.Now().Add(-2 * time.Minute)
	// Add an old bucket
	rl.buckets["oldip"] = &bucket{tokens: 50, last: time.Now().Add(-6 * time.Minute)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "newip:1234"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	rec := httptest.NewRecorder()
	rl.Middleware(next).ServeHTTP(rec, req)
	// Old bucket should be swept
	if _, exists := rl.buckets["oldip"]; exists {
		t.Fatal("expected old bucket to be swept")
	}
}

// --- Auth middleware helpers ---

const testJWTSecret = "test-jwt-secret-that-is-at-least-32-chars-long"

func makeAuthService(t *testing.T) (*services.AuthService, sqlmock.Sqlmock) {
	t.Helper()
	t.Setenv("JWT_SECRET", testJWTSecret)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return services.NewAuthService(db), mock
}

func signTestJWT(t *testing.T, claims services.JWTClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("failed to sign JWT: %v", err)
	}
	return tokenString
}

// --- AuthMiddleware ---

func TestAuthMiddleware_NoAuthHeader_Returns401(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := AuthMiddleware(authSvc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_BadFormat_Returns401(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := AuthMiddleware(authSvc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "InvalidFormat")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ValidJWT_PassesThrough(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := AuthMiddleware(authSvc)
	var gotClaims *services.JWTClaims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = GetClaimsFromContext(r.Context())
		w.WriteHeader(200)
	})
	handler := mw(next)

	claims := services.JWTClaims{
		UserID: 42, OrganizationID: 1, OrganizationSlug: "acme", Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	tokenStr := signTestJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotClaims == nil || gotClaims.UserID != 42 {
		t.Fatal("expected claims in context")
	}
}

func TestAuthMiddleware_InvalidJWT_NotAPIKey_Returns401(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := AuthMiddleware(authSvc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ValidAPIKey_PassesThrough(t *testing.T) {
	authSvc, mock := makeAuthService(t)
	mw := AuthMiddleware(authSvc)
	var gotClaims *services.JWTClaims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = GetClaimsFromContext(r.Context())
		w.WriteHeader(200)
	})
	handler := mw(next)

	apiKey := "mm_test_api_key_12345"
	// Mock the DB query for API key validation
	rows := sqlmock.NewRows([]string{"id", "organization_id", "slug", "expires_at", "user_id", "role", "email"}).
		AddRow(1, 10, "myorg", nil, nil, nil, nil)
	mock.ExpectQuery(`SELECT k.id, k.organization_id, o.slug, k.expires_at`).
		WillReturnRows(rows)
	mock.ExpectExec(`UPDATE api_keys`).WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 with valid API key, got %d", rec.Code)
	}
	if gotClaims == nil || gotClaims.OrganizationSlug != "myorg" {
		t.Fatal("expected claims with org slug")
	}
}

// --- OptionalAuthMiddleware ---

func TestOptionalAuthMiddleware_NoHeader_PassesThrough(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := OptionalAuthMiddleware(authSvc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestOptionalAuthMiddleware_BadFormat_PassesThrough(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := OptionalAuthMiddleware(authSvc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "BadFormat")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for bad format in optional auth, got %d", rec.Code)
	}
}

func TestOptionalAuthMiddleware_InvalidToken_PassesThrough(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := OptionalAuthMiddleware(authSvc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for invalid token in optional auth, got %d", rec.Code)
	}
}

func TestOptionalAuthMiddleware_ValidJWT_AddsClaims(t *testing.T) {
	authSvc, _ := makeAuthService(t)
	mw := OptionalAuthMiddleware(authSvc)
	var gotClaims *services.JWTClaims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = GetClaimsFromContext(r.Context())
		w.WriteHeader(200)
	})
	handler := mw(next)

	claims := services.JWTClaims{
		UserID: 7, OrganizationID: 2,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	tokenStr := signTestJWT(t, claims)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotClaims == nil || gotClaims.UserID != 7 {
		t.Fatal("expected claims in context")
	}
}

// --- WriteAccess ---

// read_only users may read (GET) but not mutate; write-capable roles may do both.
func TestWriteAccess(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	cases := []struct {
		name   string
		method string
		path   string
		role   string
		hasCl  bool
		want   int
	}{
		{"readonly GET passes", http.MethodGet, "/", services.RoleReadOnly, true, 200},
		{"readonly POST blocked", http.MethodPost, "/", services.RoleReadOnly, true, http.StatusForbidden},
		{"readonly DELETE blocked", http.MethodDelete, "/", services.RoleReadOnly, true, http.StatusForbidden},
		{"readonly explain POST passes", http.MethodPost, "/errors/5/explain", services.RoleReadOnly, true, 200},
		// A read_only user must chart custom metrics like any other viewer.
		{"readonly series expression POST passes", http.MethodPost, "/organizations/acme/metrics/series/expression", services.RoleReadOnly, true, 200},
		{"readwrite POST passes", http.MethodPost, "/", services.RoleReadWrite, true, 200},
		{"admin DELETE passes", http.MethodDelete, "/", services.RoleAdmin, true, 200},
		{"no claims on write 401", http.MethodPost, "/", "", false, http.StatusUnauthorized},
		{"no claims on GET passes", http.MethodGet, "/", "", false, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.hasCl {
				req = req.WithContext(context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{Role: tc.role}))
			}
			rec := httptest.NewRecorder()
			WriteAccess(ok).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s: want %d, got %d", tc.name, tc.want, rec.Code)
			}
		})
	}
}

// --- AdminOnly ---

func TestAdminOnly_NoClaims_Returns401(t *testing.T) {
	handler := AdminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAdminOnly_NonAdmin_Returns403(t *testing.T) {
	handler := AdminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{Role: services.RoleReadWrite})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestAdminOnly_Admin_PassesThrough(t *testing.T) {
	handler := AdminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{Role: "admin"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// --- RequirePlatformAdmin ---

func TestRequirePlatformAdmin_NoClaims_Returns401(t *testing.T) {
	handler := RequirePlatformAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRequirePlatformAdmin_EmailNotListed_Returns403(t *testing.T) {
	t.Setenv("PLATFORM_ADMIN_EMAILS", "owner@example.com")
	handler := RequirePlatformAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{Email: "someone-else@example.com"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestRequirePlatformAdmin_EmailListed_PassesThrough(t *testing.T) {
	// Comma-separated, mixed case and stray whitespace, matched case-insensitively.
	t.Setenv("PLATFORM_ADMIN_EMAILS", "first@example.com, Owner@Example.com")
	handler := RequirePlatformAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{Email: "owner@example.com"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestRequirePlatformAdmin_EnvUnset_AlwaysForbidden(t *testing.T) {
	t.Setenv("PLATFORM_ADMIN_EMAILS", "")
	handler := RequirePlatformAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{Email: "owner@example.com"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

// --- GetClaimsFromContext ---

func TestGetClaimsFromContext_NotSet_ReturnsNil(t *testing.T) {
	if got := GetClaimsFromContext(context.Background()); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestGetClaimsFromContext_WrongType_ReturnsNil(t *testing.T) {
	ctx := context.WithValue(context.Background(), ClaimsContextKey, "not-claims")
	if got := GetClaimsFromContext(ctx); got != nil {
		t.Fatalf("expected nil for wrong type, got %v", got)
	}
}

func TestGetClaimsFromContext_ValidClaims(t *testing.T) {
	claims := &services.JWTClaims{UserID: 99}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)
	got := GetClaimsFromContext(ctx)
	if got == nil || got.UserID != 99 {
		t.Fatal("expected claims")
	}
}

// --- OrgAccessMiddleware ---

func TestOrgAccessMiddleware_NoClaims_Returns401(t *testing.T) {
	handler := OrgAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestOrgAccessMiddleware_NoOrgSlug_Returns400(t *testing.T) {
	handler := OrgAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{OrganizationSlug: "acme"})
	rec := httptest.NewRecorder()
	// No mux vars → orgSlug = ""
	handler.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestOrgAccessMiddleware_WrongOrg_Returns403(t *testing.T) {
	handler := OrgAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	router := mux.NewRouter()
	router.Handle("/orgs/{org_slug}/data", handler)

	req := httptest.NewRequest(http.MethodGet, "/orgs/other-org/data", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{OrganizationSlug: "acme"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestOrgAccessMiddleware_CorrectOrg_PassesThrough(t *testing.T) {
	handler := OrgAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	router := mux.NewRouter()
	router.Handle("/orgs/{org_slug}/data", handler)

	req := httptest.NewRequest(http.MethodGet, "/orgs/acme/data", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, &services.JWTClaims{OrganizationSlug: "acme"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// --- GetOrganizationID / GetUserID ---

func TestGetOrganizationID_NoClaims(t *testing.T) {
	if id := GetOrganizationID(context.Background()); id != 0 {
		t.Fatalf("want 0, got %d", id)
	}
}

func TestGetOrganizationID_WithClaims(t *testing.T) {
	ctx := context.WithValue(context.Background(), ClaimsContextKey, &services.JWTClaims{OrganizationID: 5})
	if id := GetOrganizationID(ctx); id != 5 {
		t.Fatalf("want 5, got %d", id)
	}
}

func TestGetUserID_NoClaims(t *testing.T) {
	if id := GetUserID(context.Background()); id != 0 {
		t.Fatalf("want 0, got %d", id)
	}
}

func TestGetUserID_WithClaims(t *testing.T) {
	ctx := context.WithValue(context.Background(), ClaimsContextKey, &services.JWTClaims{UserID: 42})
	if id := GetUserID(ctx); id != 42 {
		t.Fatalf("want 42, got %d", id)
	}
}
