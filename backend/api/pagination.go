package api

import (
	"net/http"
	"strconv"
	"time"
)

// parseLimitOffset reads the limit/offset query params for paginated list
// endpoints. limit falls back to defaultLimit when absent or invalid and is
// capped at maxLimit; offset falls back to 0. Both are guaranteed >= 0.
func parseLimitOffset(r *http.Request, defaultLimit, maxLimit int) (limit, offset int) {
	limit = defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

// parseTimeRange reads the since/until query params of a list endpoint. Both
// RFC 3339 and unix seconds are accepted: a shell pipeline produces the second,
// a client library the first. An unparseable value is ignored rather than
// failing the request, matching how limit/offset behave above.
func parseTimeRange(r *http.Request) (since, until *time.Time) {
	return parseTimeParam(r, "since"), parseTimeParam(r, "until")
}

func parseTimeParam(r *http.Request, name string) *time.Time {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return &parsed
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		parsed := time.Unix(seconds, 0).UTC()
		return &parsed
	}
	return nil
}
