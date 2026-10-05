package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/services"
)

// errorContextRows is the shape buildErrorContext reads.
func errorContextRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "organization_id", "name", "message", "file", "line", "timestamp", "service",
		"http_method", "http_url", "http_headers", "http_body", "trace_id",
	})
}

// serviceResultContextRows is the shape buildServiceResultContext reads.
func serviceResultContextRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "service_id", "status", "latency", "message", "timestamp",
		"service", "type", "host", "host_id",
	})
}

// An explanation is built from rows the caller owns; a subject in another org
// must read as absent, with the id named so the caller can tell which.
func TestBuildErrorContextRefusesAForeignSubject(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("FROM application_errors").WithArgs(int64(42), int64(1)).WillReturnError(errNoRows())

	var notFound *RecordNotFoundError
	_, err := buildErrorContext(context.Background(), db, services.NewCorrelationService(db), nil, 1, 42)
	if !errors.As(err, &notFound) || notFound.ID != 42 || notFound.OrgID != 1 {
		t.Fatalf("got %v", err)
	}

	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db down"))
	if _, err := buildErrorContext(context.Background(), db, services.NewCorrelationService(db), nil, 1, 42); err == nil {
		t.Fatal("a lookup failure must surface")
	}
}

// The prompt payload carries the trimmed subject plus every correlation signal
// the report can quote; a missing signal must be present as an empty slot, not
// absent, so the renderer never has to guess.
func TestBuildErrorContextAssemblesThePromptPayload(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	method, url := "GET", "https://api.example.com/health"
	mock.ExpectQuery("FROM application_errors").WillReturnRows(errorContextRows().AddRow(
		int64(42), int64(1), "http", `Get "https://api.example.com/health": dial tcp: connection refused`,
		"/srv/app/main.go", 305, at, "checkout-api", &method, &url, nil, nil, nil))
	// The correlation service and the events lookup both fail: the report is
	// still built from the subject alone.
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	got, err := buildErrorContext(context.Background(), db, services.NewCorrelationService(db), nil, 1, 42)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	slim := got["error"].(slimError)
	if slim.File != "main.go" {
		t.Fatalf("the absolute path was not trimmed for the prompt: %q", slim.File)
	}
	if slim.HTTPMethod != "GET" || slim.HTTPURL != url {
		t.Fatalf("the HTTP context was dropped: %+v", slim)
	}
	if hint, _ := got["human_hint"].(string); !strings.Contains(hint, "refuse la connexion") {
		t.Fatalf("the known cause was not precomputed: %q", hint)
	}
	for _, key := range []string{"error", "human_hint", "correlation", "recent_events", "traffic_anomalies"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s is missing from the payload", key)
		}
	}
	// No trace id on this error, so nothing to look up.
	if _, ok := got["trace_id"]; ok {
		t.Fatal("a trace was reported for an error that carries none")
	}
}

// An error carrying a distributed trace must expose it, since the failing span
// is the strongest signal the report has.
func TestBuildErrorContextSurfacesTheTraceID(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	mock.ExpectQuery("FROM application_errors").WillReturnRows(errorContextRows().AddRow(
		int64(42), int64(1), "panic", "boom", "main.go", 12, at, "api", nil, nil, nil, nil, "trace-abc"))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	got, err := buildErrorContext(context.Background(), db, services.NewCorrelationService(db), nil, 1, 42)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got["trace_id"] != "trace-abc" {
		t.Fatalf("trace_id = %v", got["trace_id"])
	}
	// OpenSearch is not wired in tests, so the span chain is simply absent.
	if _, ok := got["trace"]; ok {
		t.Fatal("a trace summary was produced without a search backend")
	}
}

func TestFetchTraceSummaryNeedsOpenSearch(t *testing.T) {
	previous := opensearch
	opensearch = nil
	t.Cleanup(func() { opensearch = previous })

	if got := fetchTraceSummary(1, "trace-abc"); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestBuildServiceResultContextRefusesAForeignSubject(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("FROM service_results sr").WithArgs(int64(9), int64(1)).WillReturnError(errNoRows())

	var notFound *RecordNotFoundError
	_, err := buildServiceResultContext(context.Background(), db, services.NewCorrelationService(db), nil, 1, 9)
	if !errors.As(err, &notFound) || notFound.Kind != "service_result" {
		t.Fatalf("got %v", err)
	}

	mock.ExpectQuery("FROM service_results sr").WillReturnError(errors.New("db down"))
	if _, err := buildServiceResultContext(context.Background(), db, services.NewCorrelationService(db), nil, 1, 9); err == nil {
		t.Fatal("a lookup failure must surface")
	}
}

// The check payload carries the target and latency the report quotes, plus the
// other checks on the host so a coverage gap can be named.
func TestBuildServiceResultContextAssemblesThePromptPayload(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	mock.ExpectQuery("FROM service_results sr").WillReturnRows(serviceResultContextRows().AddRow(
		int64(9), int64(4), "fail", 320.0, "dial tcp: connection refused", at,
		"api", "tcp", "db.internal:5432", int64(7)))
	mock.ExpectQuery("AND host_id = ").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type", "host"}).
			AddRow("api-http", "http", "db.internal").
			AddRow("api-ping", "ping", "db.internal"))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	got, err := buildServiceResultContext(context.Background(), db, services.NewCorrelationService(db), nil, 1, 9)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	slim := got["service_result"].(slimServiceResult)
	if slim.CheckTarget != "db.internal:5432" || slim.CheckType != "tcp" {
		t.Fatalf("the check identity was lost: %+v", slim)
	}
	if slim.LatencyMS == nil || *slim.LatencyMS != 320 {
		t.Fatalf("latency = %v", slim.LatencyMS)
	}
	if !strings.Contains(slim.Message, "connection refused") {
		t.Fatalf("message %q", slim.Message)
	}
	siblings := got["other_checks_on_host"].([]slimCheck)
	if len(siblings) != 2 {
		t.Fatalf("the sibling checks are what reveal a coverage gap: %v", siblings)
	}
}

// A check with no host row still has a target, and the siblings are looked up
// by that target rather than skipped.
func TestFetchSiblingChecksFallsBackToTheTarget(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	if got := fetchSiblingChecks(context.Background(), db, 1, 4, sql.NullInt64{}, ""); got != nil {
		t.Fatal("with neither a host nor a target there is nothing to look up")
	}

	mock.ExpectQuery("AND host = \\$2").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type", "host"}).AddRow("api-http", "http", "db.internal"))
	got := fetchSiblingChecks(context.Background(), db, 1, 4, sql.NullInt64{}, "db.internal")
	if len(got) != 1 || got[0].Name != "api-http" {
		t.Fatalf("got %v", got)
	}

	// A failing lookup is not worth failing the whole explanation over.
	mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	if got := fetchSiblingChecks(context.Background(), db, 1, 4, sql.NullInt64{Int64: 7, Valid: true}, ""); got != nil {
		t.Fatalf("got %v", got)
	}
}

// A deploy right before the incident is the strongest correlation the report
// has, so the window around the subject is what gets queried.
func TestFetchRecentEventsQueriesTheWindowAroundTheSubject(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Date(2026, 9, 2, 14, 30, 0, 0, time.UTC)

	mock.ExpectQuery("FROM events").
		WithArgs(int64(1), "api", at.Add(-15*time.Minute), at.Add(15*time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{"type", "message", "timestamp"}).
			AddRow("deployment", strings.Repeat("x", 200), at))

	got := fetchRecentEvents(context.Background(), db, 1, "api", at, 0)
	if len(got) != 1 || got[0].Type != "deployment" {
		t.Fatalf("got %v", got)
	}
	if len([]rune(got[0].Message)) > 121 {
		t.Fatalf("the event message was not truncated for the prompt: %d runes", len([]rune(got[0].Message)))
	}

	mock.ExpectQuery("FROM events").WillReturnError(errors.New("db down"))
	if got := fetchRecentEvents(context.Background(), db, 1, "api", at, 15); got != nil {
		t.Fatalf("a failing lookup must not fail the explanation: %v", got)
	}
}

// The traffic analyzer is optional: without it the report is built from the
// remaining signals rather than failing.
func TestTrafficAnalysisIsOptional(t *testing.T) {
	if got := analyzeErrorTraffic(context.Background(), nil, 1, 42); got != nil {
		t.Fatalf("got %v", got)
	}
	if got := analyzeServiceResultTraffic(context.Background(), nil, 1, 9); got != nil {
		t.Fatalf("got %v", got)
	}
}

// The rules engine answers without any LLM: it only needs the subject, and an
// unknown subject type must be refused rather than silently produce nothing.
func TestExplainRulesOnlyProducesAReportFromTheSubjectAlone(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	mock.ExpectQuery("FROM application_errors").WillReturnRows(errorContextRows().AddRow(
		int64(42), int64(1), "panic", "runtime error: integer divide by zero",
		"/srv/app/main.go", 295, at, "checkout-api", nil, nil, nil, nil, nil))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	content, model, _, err := explainRulesOnly(context.Background(), db, 1, "error", 42, "fr")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if model != "rules" {
		t.Fatalf("model %q", model)
	}
	if !strings.Contains(content, "Division par zéro") || !strings.Contains(content, "main.go:295") {
		t.Fatalf("content %q", content)
	}
}

func TestExplainRulesOnlyRefusesAnUnknownSubject(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	var subjectErr *SubjectTypeError
	_, _, _, err := explainRulesOnly(context.Background(), db, 1, "incident", 42, "en")
	if !errors.As(err, &subjectErr) {
		t.Fatalf("got %v", err)
	}
}

func TestExplainRulesOnlyPropagatesALookupFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("FROM service_results sr").WillReturnError(errNoRows())

	if _, _, _, err := explainRulesOnly(context.Background(), db, 1, "service_result", 9, "fr"); err == nil {
		t.Fatal("a missing subject must not produce a report")
	}
}

// The single-shot engine short-circuits the LLM when the error message already
// states its own cause: paying for a call there is pure latency.
func TestExplainSingleShotSkipsTheLLMForAKnownCause(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	mock.ExpectQuery("FROM application_errors").WillReturnRows(errorContextRows().AddRow(
		int64(42), int64(1), "panic", "runtime error: integer divide by zero",
		"main.go", 295, at, "api", nil, nil, nil, nil, nil))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	// Point the LLM at a dead endpoint: reaching it at all would be the failure.
	useOpenAIBackend(t, "http://127.0.0.1:1/v1")

	content, model, _, err := explainSingleShot(context.Background(), db, 1, "error", 42, "fr")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if model != "rules" || !strings.Contains(content, "Division par zéro") {
		t.Fatalf("model %q, content %q", model, content)
	}
}

// When the cause is not self-evident the engine does call the model, and the
// answer is scrubbed before it reaches the user.
func TestExplainSingleShotCallsTheModelForAnUnknownCause(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	mock.ExpectQuery("FROM application_errors").WillReturnRows(errorContextRows().AddRow(
		int64(42), int64(1), "OrderError", "order 4711 could not be reconciled",
		"orders.go", 88, at, "billing", nil, nil, nil, nil, nil))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	server := stubChatCompletion(t, `{"choices":[{"message":{"content":"Sortie : **La réconciliation échoue.**"}}]}`)
	useOpenAIBackend(t, server)
	t.Setenv("OPENAI_MODEL", "gpt-4o")
	t.Setenv("EXPLAIN_TIMEOUT_SECONDS", "10")

	content, model, _, err := explainSingleShot(context.Background(), db, 1, "error", 42, "fr")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if model != "gpt-4o" {
		t.Fatalf("model %q", model)
	}
	if content != "**La réconciliation échoue.**" {
		t.Fatalf("the prompt scaffolding was not scrubbed: %q", content)
	}
}

// An empty answer is not an explanation: persisting it would cache a blank
// report the user cannot regenerate.
func TestExplainSingleShotRejectsAnEmptyAnswer(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	mock.ExpectQuery("FROM application_errors").WillReturnRows(errorContextRows().AddRow(
		int64(42), int64(1), "OrderError", "order 4711 could not be reconciled",
		"orders.go", 88, at, "billing", nil, nil, nil, nil, nil))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	useOpenAIBackend(t, stubChatCompletion(t, `{"choices":[{"message":{"content":"   "}}]}`))

	var emptyErr *LLMEmptyError
	if _, _, _, err := explainSingleShot(context.Background(), db, 1, "error", 42, "fr"); !errors.As(err, &emptyErr) {
		t.Fatalf("got %v", err)
	}
}

func TestExplainSingleShotRefusesAnUnknownSubject(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	var subjectErr *SubjectTypeError
	if _, _, _, err := explainSingleShot(context.Background(), db, 1, "incident", 42, "fr"); !errors.As(err, &subjectErr) {
		t.Fatalf("got %v", err)
	}
}

// The streaming engine feeds the SSE channel and closes it, or the handler
// looping over it would hang the request forever.
func TestExplainSingleShotStreamAlwaysClosesItsChannel(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	at := time.Now().UTC()

	mock.ExpectQuery("FROM service_results sr").WillReturnRows(serviceResultContextRows().AddRow(
		int64(9), int64(4), "fail", nil, "dial tcp: connection refused", at,
		"api", "tcp", "db.internal:5432", nil))
	for i := 0; i < 8; i++ {
		mock.ExpectQuery(".*").WillReturnError(errors.New("db down"))
	}

	chunks := make(chan string, 10)
	content, model, _, err := explainSingleShotStream(context.Background(), db, 1, "service_result", 9, "fr", chunks)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if model != "deterministic" {
		t.Fatalf("a self-evident cause must not be streamed from a model: %q", model)
	}
	var streamed []string
	for chunk := range chunks {
		streamed = append(streamed, chunk)
	}
	if len(streamed) != 1 || streamed[0] != content {
		t.Fatalf("the answer did not reach the channel: %v", streamed)
	}
}

func TestExplainSingleShotStreamRefusesAnUnknownSubject(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	chunks := make(chan string, 1)
	var subjectErr *SubjectTypeError
	_, _, _, err := explainSingleShotStream(context.Background(), db, 1, "incident", 42, "fr", chunks)
	if !errors.As(err, &subjectErr) {
		t.Fatalf("got %v", err)
	}
	if _, open := <-chunks; open {
		t.Fatal("the channel must be closed even when nothing was produced")
	}
}
