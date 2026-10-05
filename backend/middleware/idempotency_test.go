package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/services"
)

func withOrg(r *http.Request, orgID int64) *http.Request {
	claims := &services.JWTClaims{OrganizationID: orgID, UserID: 1}
	return r.WithContext(context.WithValue(r.Context(), ClaimsContextKey, claims))
}

// created counts how many times the handler behind the middleware ran, which is
// the whole question: a replay must not reach it.
func created(count *int, status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*count++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}

func claimRows(status interface{}, body interface{}, completed interface{}, inserted bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"status_code", "response_body", "completed_at", "inserted"}).
		AddRow(status, body, completed, inserted)
}

// A tool that retries a creation after a timeout has no way to know whether the
// first call landed. With a key, the second call gets the first answer instead
// of a second resource.
func TestReplayReturnsTheFirstAnswerWithoutRunningTheHandler(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("INSERT INTO idempotency_keys").
		WillReturnRows(claimRows(int64(201), `{"id":7}`, time.Now(), false))

	count := 0
	handler := Idempotency(db)(created(&count, http.StatusCreated, `{"id":99}`))

	req := withOrg(httptest.NewRequest("POST", "/api/v1/organizations/acme/services", strings.NewReader("{}")), 1)
	req.Header.Set("Idempotency-Key", "abc")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if count != 0 {
		t.Fatal("the handler ran again, so a duplicate was created")
	}
	if rec.Code != http.StatusCreated || rec.Body.String() != `{"id":7}` {
		t.Fatalf("replay answered %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Idempotent-Replay") != "true" {
		t.Fatal("the caller cannot tell this was a replay")
	}
}

// The first call has to go through and have its answer stored.
func TestFirstCallRunsAndIsRecorded(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("INSERT INTO idempotency_keys").WillReturnRows(claimRows(nil, nil, nil, true))
	mock.ExpectExec("UPDATE idempotency_keys SET status_code").WillReturnResult(sqlmock.NewResult(0, 1))

	count := 0
	handler := Idempotency(db)(created(&count, http.StatusCreated, `{"id":7}`))

	req := withOrg(httptest.NewRequest("POST", "/api/v1/organizations/acme/services", strings.NewReader("{}")), 1)
	req.Header.Set("Idempotency-Key", "abc")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if count != 1 {
		t.Fatalf("the handler ran %d times, want 1", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the answer was not recorded: %v", err)
	}
}

// A second caller arriving while the first is still working must be told to
// wait, not handed a duplicate.
func TestConcurrentCallIsToldTheRequestIsInProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("INSERT INTO idempotency_keys").WillReturnRows(claimRows(nil, nil, nil, false))

	count := 0
	handler := Idempotency(db)(created(&count, http.StatusCreated, `{}`))

	req := withOrg(httptest.NewRequest("POST", "/x", strings.NewReader("{}")), 1)
	req.Header.Set("Idempotency-Key", "abc")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if count != 0 {
		t.Fatal("the handler ran while another call held the key")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "idempotency_in_progress") {
		t.Fatalf("body %q gives the caller nothing to switch on", rec.Body.String())
	}
}

// A failure must release the key: the caller retries the same request rather
// than being answered forever with an error it did not cause.
func TestFailureReleasesTheKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("INSERT INTO idempotency_keys").WillReturnRows(claimRows(nil, nil, nil, true))
	mock.ExpectExec("DELETE FROM idempotency_keys").WillReturnResult(sqlmock.NewResult(0, 1))

	count := 0
	handler := Idempotency(db)(created(&count, http.StatusInternalServerError, `{"error":"boom"}`))

	req := withOrg(httptest.NewRequest("POST", "/x", strings.NewReader("{}")), 1)
	req.Header.Set("Idempotency-Key", "abc")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the key was not released: %v", err)
	}
}

// The feature is opt-in: a request without the header must not touch the table
// at all, or every dashboard click would write a row.
func TestWithoutTheHeaderNothingIsStored(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	count := 0
	handler := Idempotency(db)(created(&count, http.StatusCreated, `{}`))
	handler.ServeHTTP(httptest.NewRecorder(), withOrg(httptest.NewRequest("POST", "/x", nil), 1))

	if count != 1 {
		t.Fatal("the handler must still run")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected database work: %v", err)
	}
}

// A read is never idempotency-protected: replaying a GET from a stored body
// would serve stale data.
func TestReadsAreUntouched(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	count := 0
	handler := Idempotency(db)(created(&count, http.StatusOK, `{}`))
	req := withOrg(httptest.NewRequest("GET", "/x", nil), 1)
	req.Header.Set("Idempotency-Key", "abc")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if count != 1 {
		t.Fatal("the read did not run")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("a read touched the idempotency table: %v", err)
	}
}

// A database that cannot claim the key must not block the request: the worst
// case is the duplicate the caller already lived with.
func TestAClaimFailureLetsTheRequestThrough(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("INSERT INTO idempotency_keys").WillReturnError(context.DeadlineExceeded)

	count := 0
	handler := Idempotency(db)(created(&count, http.StatusCreated, `{}`))
	req := withOrg(httptest.NewRequest("POST", "/x", strings.NewReader("{}")), 1)
	req.Header.Set("Idempotency-Key", "abc")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if count != 1 {
		t.Fatal("the request was dropped because the key could not be claimed")
	}
}

// The series expression endpoint is a POST only because its queries do not fit
// a URL. Replaying it would pin a dashboard to the graph of the first call, so
// the key must be ignored on every request WriteAccess reads as a read. The
// stored answer below is what the middleware would serve if it did not.
func TestReadPostsIgnoreTheIdempotencyKey(t *testing.T) {
	for _, path := range []string{
		"/api/v1/organizations/acme/metrics/series/expression",
		"/api/v1/organizations/acme/errors/7/explain",
	} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}

		mock.ExpectQuery("INSERT INTO idempotency_keys").
			WillReturnRows(claimRows(int64(200), `{"series":["stale"]}`, time.Now(), false))

		count := 0
		handler := Idempotency(db)(created(&count, http.StatusOK, `{"series":[]}`))

		req := withOrg(httptest.NewRequest("POST", path, strings.NewReader("{}")), 1)
		req.Header.Set("Idempotency-Key", "abc")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if count != 1 {
			t.Errorf("%s: a read must reach the handler, it ran %d times", path, count)
		}
		if rec.Header().Get("Idempotent-Replay") != "" {
			t.Errorf("%s: served the stored answer instead of querying again", path)
		}
		if strings.Contains(rec.Body.String(), "stale") {
			t.Errorf("%s: body replayed from idempotency_keys: %s", path, rec.Body.String())
		}
		db.Close()
	}
}
