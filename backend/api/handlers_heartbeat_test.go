package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/services"
)

// A cron line calls the ping URL with no Authorization header, so the route has
// to be public and reachable through the served router, not only its handler.
func TestHeartbeatPingNeedsNoCredentials(t *testing.T) {
	router, mock := heartbeatRouter(t)
	mock.ExpectQuery("SELECT id FROM services WHERE token").
		WithArgs("tok", "heartbeat").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectExec("INSERT INTO service_results").
		WithArgs(int64(9), "success", "Ping received", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/heartbeats/tok", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The fail URL is how a job reports its own failure, with its output as the
// reason: the alert must carry it rather than a bare "failed".
func TestHeartbeatFailPingRecordsTheJobOutput(t *testing.T) {
	router, mock := heartbeatRouter(t)
	mock.ExpectQuery("SELECT id FROM services WHERE token").
		WithArgs("tok", "heartbeat").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectExec("INSERT INTO service_results").
		WithArgs(int64(9), "failure", "The job reported a failure: pg_dump: connection refused", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/heartbeats/tok/fail", strings.NewReader("pg_dump: connection refused\n")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Only heartbeat tokens open the ping URL: an unknown token, or the token of
// another kind of service, is a 404 and records nothing.
func TestHeartbeatPingWithAnUnknownTokenIsNotFound(t *testing.T) {
	router, mock := heartbeatRouter(t)
	mock.ExpectQuery("SELECT id FROM services WHERE token").
		WithArgs("nope", "heartbeat").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/heartbeats/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func heartbeatRouter(t *testing.T) (http.Handler, sqlmock.Sqlmock) {
	t.Helper()
	if os.Getenv("JWT_SECRET") == "" {
		os.Setenv("JWT_SECRET", strings.Repeat("t", 48))
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return SetupAPIRouter(db, services.NewAuthService(db)), mock
}
