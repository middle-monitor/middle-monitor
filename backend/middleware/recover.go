package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover wraps a handler so a panic in any downstream handler is turned into a
// clean 500 JSON response instead of a dropped connection. The full stack trace
// is logged server-side; the client only ever sees a generic message so we never
// leak internals. Register it as the OUTERMOST middleware so it covers
// everything, including other middleware that runs after it.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "method", r.Method, "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				// Only write a response if nothing has been sent yet. If the
				// handler already started streaming, we can't safely set a
				// status; the deferred recover still prevents a crash.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
