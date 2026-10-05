package middleware

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimiter is a tiny in-memory token-bucket keyed by client IP. It's enough
// to slow down credential stuffing on /auth/login and protect public ingestion
// endpoints from a single noisy host. For multi-instance deployments behind a
// load balancer, swap this for a Redis-backed limiter.
type RateLimiter struct {
	rate      float64
	burst     float64
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter builds a limiter that refills `ratePerSec` tokens per second
// up to `burst` tokens. Every request costs 1 token.
func NewRateLimiter(ratePerSec, burst float64) *RateLimiter {
	return &RateLimiter{
		rate:      ratePerSec,
		burst:     burst,
		buckets:   make(map[string]*bucket),
		lastSweep: time.Now(),
	}
}

// allow reports whether the request passes, and how many tokens are left. The
// remaining count is what the RateLimit headers publish, so a client can slow
// down before it is refused rather than discovering the limit as a 429.
func (rl *RateLimiter) allowRemaining(key string) (bool, int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now

	// Periodic GC: every minute, drop buckets that have been idle for >5 min.
	if now.Sub(rl.lastSweep) > time.Minute {
		for k, v := range rl.buckets {
			if now.Sub(v.last) > 5*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.lastSweep = now
	}

	if b.tokens < 1 {
		return false, 0
	}
	b.tokens--
	return true, int(b.tokens)
}

func (rl *RateLimiter) allow(key string) bool {
	allowed, _ := rl.allowRemaining(key)
	return allowed
}

// Middleware returns an http.Handler middleware that enforces the limit per
// remote IP.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		allowed, remaining := rl.allowRemaining(ip)

		// Standard headers (draft-ietf-httpapi-ratelimit-headers): a client
		// that reads them never has to discover the limit by being refused.
		w.Header().Set("RateLimit-Limit", strconv.Itoa(int(rl.burst)))
		w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("RateLimit-Reset", strconv.Itoa(resetSeconds(rl.rate, remaining)))

		if !allowed {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resetSeconds is when one more request will be allowed: immediately while
// tokens remain, otherwise the time the bucket needs to refill one.
func resetSeconds(rate float64, remaining int) int {
	if remaining > 0 || rate <= 0 {
		return 0
	}
	seconds := int(1/rate + 0.5)
	if seconds < 1 {
		return 1
	}
	return seconds
}

// X-Forwarded-For and X-Real-IP are ignored on purpose: Cloudflare appends to
// XFF instead of overwriting it, so its first hop is whatever the caller sent.
// Only CF-Connecting-IP is rewritten edge-side, and the origin is unreachable
// outside the tunnel, so no other path can forge it.
func clientIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); net.ParseIP(cf) != nil {
		return cf
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
