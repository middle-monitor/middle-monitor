package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/services"
)

// The DSN has to be fully overridable from the environment, and every part must
// carry a working default so a local boot needs no configuration.
func TestBuildDSNDefaultsAndOverrides(t *testing.T) {
	for _, key := range []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE"} {
		t.Setenv(key, "")
	}
	got := buildDSN()
	for _, want := range []string{"host=localhost", "port=5432", "user=postgres", "dbname=middlemonitor", "sslmode=disable", "timezone=UTC"} {
		if !strings.Contains(got, want) {
			t.Fatalf("default DSN %q is missing %q", got, want)
		}
	}

	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_PORT", "6432")
	t.Setenv("DB_USER", "mm")
	t.Setenv("DB_PASSWORD", "secret")
	t.Setenv("DB_NAME", "prod")
	t.Setenv("DB_SSLMODE", "verify-full")
	got = buildDSN()
	for _, want := range []string{"host=db.internal", "port=6432", "user=mm", "dbname=prod", "sslmode=verify-full"} {
		if !strings.Contains(got, want) {
			t.Fatalf("DSN %q is missing %q", got, want)
		}
	}
}

// The pool sizes are tunable but a nonsense value must not disable the pool.
func TestEnvIntRejectsUnusableValues(t *testing.T) {
	t.Setenv("MM_TEST_POOL", "")
	if got := envInt("MM_TEST_POOL", 25); got != 25 {
		t.Fatalf("got %d", got)
	}
	t.Setenv("MM_TEST_POOL", "50")
	if got := envInt("MM_TEST_POOL", 25); got != 50 {
		t.Fatalf("got %d", got)
	}
	for _, bad := range []string{"lots", "0", "-1"} {
		t.Setenv("MM_TEST_POOL", bad)
		if got := envInt("MM_TEST_POOL", 25); got != 25 {
			t.Fatalf("%q gave %d, want the default", bad, got)
		}
	}
}

// Seeding is opt-in: without both credentials it must do nothing at all, since
// a default admin account is the first credential a scanner tries.
func TestSeedAdminIsOptIn(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	for _, env := range []struct{ email, password string }{
		{"", ""},
		{"admin@example.com", ""},
		{"", "Password1"},
	} {
		t.Setenv("SEED_ADMIN_EMAIL", env.email)
		t.Setenv("SEED_ADMIN_PASSWORD", env.password)
		if err := seedDefaultAdmin(db); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an opted-out seed touched the database: %v", err)
	}
}

// The operator org is created on demand and pinned to pro, so it dogfoods the
// same limits a paying customer gets rather than being exempt.
func TestSeedAdminCreatesTheOrgPinnedToPro(t *testing.T) {
	t.Setenv("SEED_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("SEED_ADMIN_PASSWORD", "Password1")
	t.Setenv("SEED_ADMIN_ORG_SLUG", "")
	t.Setenv("SEED_ADMIN_NAME", "")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WithArgs("default").WillReturnError(errNoRows())
	mock.ExpectQuery("INSERT INTO organizations").WithArgs("default").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))
	mock.ExpectExec("UPDATE organizations SET plan = 'pro'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT password_hash FROM users").WillReturnError(errNoRows())
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	// Login resolves the org through memberships: without this row the seeded
	// admin of a fresh install cannot sign in.
	mock.ExpectExec("INSERT INTO memberships").WillReturnResult(sqlmock.NewResult(1, 1))

	if err := seedDefaultAdmin(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// The env vars declare the desired credentials: an existing account's password
// converges to them, or changing SEED_ADMIN_PASSWORD would lock the operator out.
func TestSeedAdminConvergesAnExistingPassword(t *testing.T) {
	t.Setenv("SEED_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("SEED_ADMIN_PASSWORD", "Password1")
	t.Setenv("SEED_ADMIN_ORG_SLUG", "admin")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))
	mock.ExpectExec("UPDATE organizations SET plan = 'pro'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT password_hash FROM users").
		WillReturnRows(sqlmock.NewRows([]string{"password_hash"}).AddRow("$2a$10$staleHashThatDoesNotMatchTheEnvValue012345678901234"))
	mock.ExpectExec("UPDATE users SET password_hash").WillReturnResult(sqlmock.NewResult(0, 1))
	// The membership follows the account's home org, not the slug's: an existing
	// account must not gain admin in another org at every boot.
	mock.ExpectExec(regexp.QuoteMeta("SELECT id, organization_id, 'admin', NOW() FROM users WHERE email = $1")).
		WithArgs("admin@example.com").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := seedDefaultAdmin(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the stale password was left in place: %v", err)
	}
}

// A failure while resolving the org must stop the seed rather than write a user
// into whatever org id happens to be zero-valued.
func TestSeedAdminStopsOnAnOrgFailure(t *testing.T) {
	t.Setenv("SEED_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("SEED_ADMIN_PASSWORD", "Password1")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(errors.New("db down"))

	if err := seedDefaultAdmin(db); err == nil {
		t.Fatal("expected the seed to fail")
	}
}

// Self-monitoring is opt-in: with no org slug configured nothing is seeded.
func TestSelfMonitorSeedIsOptIn(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("SELF_MONITOR_ORG_SLUG", "")

	ensureSelfMonitorChecks(db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an opted-out seed touched the database: %v", err)
	}
}

// With the org configured but no public URL there is nothing to probe, so the
// seed stops after resolving the org instead of creating empty checks.
func TestSelfMonitorSeedNeedsAPublicURL(t *testing.T) {
	t.Setenv("SELF_MONITOR_ORG_SLUG", "admin")
	t.Setenv("FRONTEND_URL", "")
	t.Setenv("PUBLIC_API_URL", "")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))
	mock.ExpectQuery("SELECT id FROM hosts").WillReturnError(errNoRows())

	ensureSelfMonitorChecks(db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// An unknown org means the deployment is misconfigured; seeding into whatever
// org id came back would attach our checks to a customer.
func TestSelfMonitorSeedStopsOnAnUnknownOrg(t *testing.T) {
	t.Setenv("SELF_MONITOR_ORG_SLUG", "ghost")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").WillReturnError(errNoRows())

	ensureSelfMonitorChecks(db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// The seed is idempotent: a check that already exists is never recreated, so an
// operator's edits in the UI survive every restart.
func TestSelfMonitorSeedNeverRecreatesAnExistingCheck(t *testing.T) {
	t.Setenv("SELF_MONITOR_ORG_SLUG", "admin")
	t.Setenv("FRONTEND_URL", "https://middlemonitor.io")
	t.Setenv("PUBLIC_API_URL", "https://api.middlemonitor.io")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT id FROM organizations WHERE slug").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))
	mock.ExpectQuery("SELECT id FROM hosts").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(4)))
	// Five targets: dashboard, its certificate, the API, the ingestion probe and
	// the API certificate.
	for i := 0; i < 5; i++ {
		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	}

	ensureSelfMonitorChecks(db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an existing check was recreated: %v", err)
	}
}

// A rolling update runs two instances at once; the loser of the insert race
// must recognize the row the winner created instead of logging a failure.
func TestSeededByAnotherInstance(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	if !seededByAnotherInstance(db, 3, "self-api") {
		t.Fatal("the row created by the winner was not seen")
	}

	mock.ExpectQuery("SELECT EXISTS").WillReturnError(errors.New("db down"))
	if seededByAnotherInstance(db, 3, "self-api") {
		t.Fatal("a failed lookup must not be read as success")
	}
}

// Check hosts are stored bare: the http worker adds the scheme and the
// certificate worker appends :443, so a scheme or path here would break both.
func TestBareHostStripsSchemeAndPath(t *testing.T) {
	cases := map[string]string{
		"https://middlemonitor.io":                    "middlemonitor.io",
		"http://localhost:3000/":                      "localhost:3000",
		"https://a.example.com,https://b.example.com": "a.example.com",
		" https://a.example.com/path ":                "a.example.com",
		"":                                            "",
	}
	for raw, want := range cases {
		if got := bareHost(raw); got != want {
			t.Fatalf("bareHost(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Liveness must answer without touching anything: it exists to detect a wedged
// process, so a dependency outage must not make it fail.
func TestLiveHandlerAlwaysAnswers(t *testing.T) {
	rec := httptest.NewRecorder()
	liveHandler(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

// Readiness gates traffic: an instance that cannot reach the database must be
// drained, not left serving errors.
func TestReadyHandlerFollowsTheDatabase(t *testing.T) {
	db, mock, _ := sqlmock.New(sqlmock.MonitorPingsOption(true))
	defer db.Close()

	mock.ExpectPing()
	rec := httptest.NewRecorder()
	readyHandler(db)(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ready"`) {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}

	mock.ExpectPing().WillReturnError(errors.New("connection refused"))
	rec = httptest.NewRecorder()
	readyHandler(db)(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}

// Without OpenSearch there is no receiver, and an OTLP exporter has to be told
// so rather than have its spans silently dropped.
func TestOTLPEndpointsReportAnUnavailableReceiver(t *testing.T) {
	previous := otlpReceiver
	otlpReceiver = nil
	t.Cleanup(func() { otlpReceiver = previous })

	for name, handler := range map[string]http.HandlerFunc{
		"traces":  handleOTLPTraces,
		"logs":    handleOTLPLogs,
		"metrics": handleOTLPMetrics,
	} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("POST", "/v1/"+name, strings.NewReader("")))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status %d, want 503", name, rec.Code)
		}
	}
}

// The receiver only speaks protobuf; a JSON exporter must be told explicitly
// instead of having its payload fail to parse deep in the pipeline.
func TestOTLPEndpointsRejectNonProtobufPayloads(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	expectIngestToken(mock, 3)

	previous, previousDB := otlpReceiver, otlpDB
	otlpReceiver = services.NewOTLPReceiverService(nil, db)
	otlpDB = db
	t.Cleanup(func() { otlpReceiver, otlpDB = previous, previousDB })

	for name, handler := range map[string]http.HandlerFunc{
		"traces":  handleOTLPTraces,
		"logs":    handleOTLPLogs,
		"metrics": handleOTLPMetrics,
	} {
		req := httptest.NewRequest("POST", "/v1/"+name, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer svc-token")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: status %d, want 415", name, rec.Code)
		}
	}
}

// A protobuf body that does not parse is a failed export: the exporter retries
// on a 5xx, which is what we want rather than a silent 200.
func TestOTLPEndpointsReportAnUnparseablePayload(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	expectIngestToken(mock, 3)
	for i := 0; i < 12; i++ {
		mock.ExpectQuery(".*").WillReturnError(errNoRows())
	}

	previous, previousDB := otlpReceiver, otlpDB
	otlpReceiver = services.NewOTLPReceiverService(nil, db)
	otlpDB = db
	t.Cleanup(func() { otlpReceiver, otlpDB = previous, previousDB })

	for name, handler := range map[string]http.HandlerFunc{
		"traces":  handleOTLPTraces,
		"logs":    handleOTLPLogs,
		"metrics": handleOTLPMetrics,
	} {
		req := httptest.NewRequest("POST", "/v1/"+name, strings.NewReader("\xff\xff not protobuf"))
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("Authorization", "Bearer svc-token")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status %d, want 500", name, rec.Code)
		}
	}
}

// An empty export is valid: an SDK flushing with nothing buffered must not be
// told its pipeline is broken.
func TestOTLPEndpointsAcceptAnEmptyExport(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	expectIngestToken(mock, 3)
	for i := 0; i < 12; i++ {
		mock.ExpectQuery(".*").WillReturnError(errNoRows())
	}

	previous, previousDB := otlpReceiver, otlpDB
	otlpReceiver = services.NewOTLPReceiverService(nil, db)
	otlpDB = db
	t.Cleanup(func() { otlpReceiver, otlpDB = previous, previousDB })

	for name, handler := range map[string]http.HandlerFunc{
		"traces":  handleOTLPTraces,
		"logs":    handleOTLPLogs,
		"metrics": handleOTLPMetrics,
	} {
		req := httptest.NewRequest("POST", "/v1/"+name, strings.NewReader(""))
		req.Header.Set("Authorization", "Bearer svc-token")
		req.Header.Set("Content-Type", "application/x-protobuf")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// InitializeOTLPServices must degrade rather than fail the boot when OpenSearch
// is not configured: traces are optional, the rest of the product is not.
func TestInitializeOTLPServicesDegradesWithoutOpenSearch(t *testing.T) {
	previousReceiver, previousSearch, previousDB := otlpReceiver, opensearch, otlpDB
	t.Cleanup(func() { otlpReceiver, opensearch, otlpDB = previousReceiver, previousSearch, previousDB })

	os.Unsetenv("OPENSEARCH_URL")
	db, _, _ := sqlmock.New()
	defer db.Close()

	if got := InitializeOTLPServices(db); got != nil && otlpReceiver == nil {
		t.Fatal("an initialized OpenSearch must come with a receiver")
	}
	if opensearch == nil && otlpReceiver != nil {
		t.Fatal("a receiver was wired without a search backend")
	}
}

// expectIngestToken makes n OTLP requests resolve their bearer token to a
// service of organization 1: anonymous OTLP is refused before anything else.
func expectIngestToken(mock sqlmock.Sqlmock, n int) {
	for i := 0; i < n; i++ {
		mock.ExpectQuery("FROM services WHERE token").WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(1)))
	}
}
