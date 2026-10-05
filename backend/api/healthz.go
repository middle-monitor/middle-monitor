package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// liveHandler always returns 200 OK. It is meant to be hit by liveness probes
// (kubelet, ECS, load balancers) to detect deadlocked or wedged processes.
// It must be cheap, must not depend on the DB, and must not require auth.
func liveHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// readyHandler reports whether this instance can serve traffic. It pings the
// DB with a tight timeout. Readiness probes use this to drain pods cleanly.
func readyHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		w.Header().Set("Content-Type", "application/json")
		if err := db.PingContext(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unavailable",
				"reason": "database ping failed",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	}
}
