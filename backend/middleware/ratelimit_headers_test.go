package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// A converging tool makes many read calls. Publishing the budget on every
// answer is what lets it slow down before it is refused, instead of learning
// the limit from a 429.
func TestRateLimitHeadersArePublished(t *testing.T) {
	limiter := NewRateLimiter(5, 10)
	rec := httptest.NewRecorder()

	limiter.Middleware(okHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("RateLimit-Limit"); got != "10" {
		t.Fatalf("RateLimit-Limit %q, want 10", got)
	}
	remaining, err := strconv.Atoi(rec.Header().Get("RateLimit-Remaining"))
	if err != nil || remaining < 0 || remaining > 10 {
		t.Fatalf("RateLimit-Remaining %q", rec.Header().Get("RateLimit-Remaining"))
	}
}

// When the budget is spent, the answer has to say when to come back.
func TestExhaustedBudgetAnswers429WithRetryAfter(t *testing.T) {
	limiter := NewRateLimiter(1, 1)
	handler := limiter.Middleware(okHandler())

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest("GET", "/x", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request refused: %d", first.Code)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest("GET", "/x", nil))

	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("a 429 without Retry-After leaves the caller guessing")
	}
	if got := second.Header().Get("RateLimit-Remaining"); got != "0" {
		t.Fatalf("RateLimit-Remaining %q, want 0", got)
	}
	if got := second.Header().Get("RateLimit-Reset"); got == "" || got == "0" {
		t.Fatalf("RateLimit-Reset %q must say when the budget returns", got)
	}
}
