package middleware

import (
	"bytes"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
)

// maxIdempotentBody caps what is stored per key. A creation answers with one
// resource, so this is generous; anything larger is not replayed rather than
// filling the table.
const maxIdempotentBody = 64 << 10

// capturingWriter records the answer so it can be stored and replayed.
type capturingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *capturingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *capturingWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < maxIdempotentBody {
		w.body.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// Idempotency replays the first answer given to a request that carries an
// Idempotency-Key. Without it, a tool that retries a creation after a timeout
// has no way to know whether the first call landed, and creates a duplicate.
//
// Only mutating methods are affected, and only when the caller opts in by
// sending the header.
func Idempotency(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			// A read has nothing to deduplicate, and the POSTs WriteAccess treats
			// as reads would otherwise replay one stored graph for ever.
			if key == "" || !mutating(r.Method) || isReadRequest(r) || db == nil {
				next.ServeHTTP(w, r)
				return
			}

			orgID := GetOrganizationID(r.Context())
			if orgID == 0 {
				next.ServeHTTP(w, r)
				return
			}

			status, body, replay, inFlight := claimKey(db, orgID, key, r)
			if replay {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Idempotent-Replay", "true")
				w.WriteHeader(status)
				w.Write([]byte(body))
				return
			}
			if inFlight {
				// The first call has not answered yet. Answering 409 is what
				// tells the caller to wait rather than to try a third time.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte(`{"error":"a request with this Idempotency-Key is still in progress","code":"idempotency_in_progress"}`))
				return
			}

			capture := &capturingWriter{ResponseWriter: w}
			next.ServeHTTP(capture, r)
			storeAnswer(db, orgID, key, r, capture)
		})
	}
}

func mutating(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
}

// claimKey reserves the key, or reports the answer already stored for it.
func claimKey(db *sql.DB, orgID int64, key string, r *http.Request) (status int, body string, replay, inFlight bool) {
	var storedStatus sql.NullInt64
	var storedBody sql.NullString
	var completed sql.NullTime
	var inserted bool

	// xmax is zero on a row this statement inserted and non-zero on one it
	// updated, which is the only way to tell a fresh claim from a replay: both
	// come back with a NULL answer until the first call finishes.
	err := db.QueryRow(
		`INSERT INTO idempotency_keys (organization_id, key, method, path)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (organization_id, key, method, path) DO UPDATE
		   SET key = idempotency_keys.key
		 RETURNING status_code, response_body, completed_at, (xmax = 0) AS inserted`,
		orgID, key, r.Method, r.URL.Path).Scan(&storedStatus, &storedBody, &completed, &inserted)
	if err != nil {
		// A key that cannot be claimed must not block the request: the worst
		// case is the duplicate the caller was already exposed to.
		slog.Error("idempotency claim failed", "path", r.URL.Path, "error", err)
		return 0, "", false, false
	}

	if inserted {
		return 0, "", false, false
	}
	if completed.Valid && storedStatus.Valid {
		return int(storedStatus.Int64), storedBody.String, true, false
	}
	return 0, "", false, true
}

// storeAnswer records a successful answer for replay. A failure releases the
// key instead, so the caller can retry the same request rather than being
// permanently answered with an error it did not cause.
func storeAnswer(db *sql.DB, orgID int64, key string, r *http.Request, capture *capturingWriter) {
	status := capture.status
	if status == 0 {
		status = http.StatusOK
	}

	if status >= 400 || capture.body.Len() >= maxIdempotentBody {
		if _, err := db.Exec(
			`DELETE FROM idempotency_keys WHERE organization_id=$1 AND key=$2 AND method=$3 AND path=$4 AND completed_at IS NULL`,
			orgID, key, r.Method, r.URL.Path); err != nil {
			slog.Error("idempotency release failed", "path", r.URL.Path, "error", err)
		}
		return
	}

	if _, err := db.Exec(
		`UPDATE idempotency_keys SET status_code=$1, response_body=$2, completed_at=NOW()
		  WHERE organization_id=$3 AND key=$4 AND method=$5 AND path=$6`,
		status, capture.body.String(), orgID, key, r.Method, r.URL.Path); err != nil {
		slog.Error("idempotency store failed", "path", r.URL.Path, "error", err)
	}
}
