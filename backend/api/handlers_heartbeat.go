package api

import (
	"database/sql"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"middle-monitor/backend/services"
)

const maxHeartbeatBody = 10 << 10

// handleHeartbeatPing records a ping from a job. GET, HEAD and POST all count,
// so a bare `curl -fsS URL` works and so does a hook that POSTs the job output.
func handleHeartbeatPing(db *sql.DB, failed bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxHeartbeatBody))
		err := services.RecordHeartbeatPing(db, mux.Vars(r)["token"], failed, string(body))
		if errors.Is(err, services.ErrHeartbeatNotFound) {
			respondError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("OK\n"))
	}
}
