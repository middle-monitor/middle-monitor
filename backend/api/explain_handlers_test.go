package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
)

// The explicit locale query param wins over the browser header, because that is
// the switch the UI language toggle sets.
func TestExplainLocalePrefersTheExplicitParam(t *testing.T) {
	req := httptest.NewRequest("GET", "/x?locale=en", nil)
	req.Header.Set("Accept-Language", "fr-FR")
	if got := explainLocale(req); got != "en" {
		t.Fatalf("got %q", got)
	}

	header := httptest.NewRequest("GET", "/x", nil)
	header.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	if got := explainLocale(header); got != "en" {
		t.Fatalf("got %q", got)
	}

	if got := explainLocale(httptest.NewRequest("GET", "/x", nil)); got != "fr" {
		t.Fatalf("the product default is French, got %q", got)
	}
}

// The markers are how the prompt delimits the answer inside the model output. A
// truncated answer that only opened the marker is still worth showing.
func TestExtractExplanationHandlesPartialMarkers(t *testing.T) {
	full := "noise\n<!-- EXPLANATION -->\n**Cause.**\n<!-- /EXPLANATION -->\ntrailing"
	if got := extractExplanation(full); got != "**Cause.**" {
		t.Fatalf("got %q", got)
	}

	open := "noise\n<!-- EXPLANATION -->\n**Cause.**"
	if got := extractExplanation(open); got != "**Cause.**" {
		t.Fatalf("a missing closing marker must not lose the answer: %q", got)
	}

	if got := extractExplanation("  **Cause.**  "); got != "**Cause.**" {
		t.Fatalf("output with no marker at all is the answer: %q", got)
	}
}

// The renderer is minimal: without a blank line the bullets are absorbed into
// the preceding paragraph and the answer renders as one blob.
func TestNormalizeMarkdownSeparatesBulletsAndHeadings(t *testing.T) {
	got := normalizeMarkdown("**Cause.**\n- first\n- second")
	if got != "**Cause.**\n\n- first\n- second" {
		t.Fatalf("got %q", got)
	}

	afterBold := normalizeMarkdown("**Cause.**\nSome detail.")
	if afterBold != "**Cause.**\n\nSome detail." {
		t.Fatalf("got %q", afterBold)
	}

	// Already-separated content must not gain extra blank lines on each pass.
	stable := "**Cause.**\n\n- first\n- second"
	if got := normalizeMarkdown(stable); got != stable {
		t.Fatalf("normalization is not idempotent: %q", got)
	}
}

func TestEnvOrFallsBackOnAnUnsetOrEmptyValue(t *testing.T) {
	t.Setenv("MM_TEST_ENVOR", "")
	if got := envOr("MM_TEST_ENVOR", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("MM_TEST_ENVOR", "set")
	if got := envOr("MM_TEST_ENVOR", "fallback"); got != "set" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateMarksWhatWasCut(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("abcdefghij", 4); got != "abcd…" {
		t.Fatalf("got %q", got)
	}
}

func TestEscapeJSONProducesAnEmbeddableFragment(t *testing.T) {
	if got := escapeJSON(`a "quoted" line`); got != `a \"quoted\" line` {
		t.Fatalf("got %q", got)
	}
}

// Cached explanations are purged and rebuilt when they come from the old
// sectioned renderer, but an LLM answer may legitimately emit headings.
func TestStaleRulesFormatOnlyAppliesToTheRulesEngine(t *testing.T) {
	sectioned := "## Résumé\nblah"
	for _, mode := range []string{"rules", "local", "offline", ""} {
		if !isStaleRulesFormat(mode, sectioned) {
			t.Fatalf("%q: a sectioned report must be purged", mode)
		}
	}
	if isStaleRulesFormat("single_shot", sectioned) {
		t.Fatal("LLM output may legitimately contain headings")
	}
	if isStaleRulesFormat("rules", "**Cause.** — api.") {
		t.Fatal("the current minimal format is not stale")
	}
}

// A cached answer that leaked prompt scaffolding or lost half its bold markers
// is worse than regenerating: it is what the user actually sees.
func TestMalformedExplanationIsRejected(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"too short",
		"**unbalanced bold markers in this sentence",
		"**Cause.** Règles : ne jamais inventer un signal",
		"**Cause.** Rules: never invent a signal absent",
		"Sortie : **Cause.** — api / main.go.",
		"Output: **Cause.** — api / main.go.",
	}
	for _, content := range bad {
		if !isMalformedExplanation(content) {
			t.Fatalf("%q should be rejected", content)
		}
	}
	if isMalformedExplanation("**Division par zéro dans le code.** — main.go:295 dans api.") {
		t.Fatal("a well-formed report was rejected")
	}
}

// The subject has to belong to the caller's org: an explanation is built from
// rows the caller must not be able to reach otherwise.
func TestExplainErrorChecksOwnershipBeforeAnalyzing(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(42), int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestExplainErrorRejectsANonNumericID(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"id": "abc"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestExplainErrorSurfacesALookupFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT EXISTS").WillReturnError(errors.New("connection reset"))

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "connection reset") {
		t.Fatalf("a driver error must not reach the client: %s", rec.Body.String())
	}
}

func TestExplainServiceResultChecksOwnership(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(9), int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"resultId": "9"})
	rec := httptest.NewRecorder()
	handleExplainServiceResult(db)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestExplainServiceResultRejectsANonNumericID(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"resultId": "abc"})
	rec := httptest.NewRecorder()
	handleExplainServiceResult(db)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestExplainServiceResultSurfacesALookupFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT EXISTS").WillReturnError(errors.New("connection reset"))

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"resultId": "9"})
	rec := httptest.NewRecorder()
	handleExplainServiceResult(db)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// A cached French report is served as-is: regenerating it would spend an LLM
// call on an answer that has not changed.
func TestCachedExplanationIsServedWithoutReanalyzing(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("EXPLAIN_MODE", "rules")

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT subject_type, subject_id, content").
		WillReturnRows(sqlmock.NewRows([]string{"subject_type", "subject_id", "content", "model", "duration_ms", "created_at"}).
			AddRow("error", int64(42), "**Division par zéro dans le code.** — main.go:295 dans api.", "rules", int64(12), nowUTC()))

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp ExplanationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Cached || resp.Model != "rules" || resp.DurationMS != 12 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// The SSE path frames the same cached answer as metadata + one data frame, so
// the frontend consumes one protocol whether or not the answer was cached.
func TestCachedExplanationIsAlsoServedOverSSE(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("EXPLAIN_MODE", "rules")

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT subject_type, subject_id, content").
		WillReturnRows(sqlmock.NewRows([]string{"subject_type", "subject_id", "content", "model", "duration_ms", "created_at"}).
			AddRow("error", int64(42), "**Division par zéro dans le code.** — main.go:295 dans api.", nil, nil, nowUTC()))

	req := httptest.NewRequest("POST", "/x", nil)
	req.Header.Set("Accept", "text/event-stream")
	req = mux.SetURLVars(orgContext(req, 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "event: metadata") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected SSE stream: %q", body)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type %q", got)
	}
}

// A cached report in the legacy sectioned format is deleted and rebuilt, since
// regenerating with the rules engine costs nothing.
func TestStaleCachedReportIsPurgedAndRebuilt(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("EXPLAIN_MODE", "rules")

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT subject_type, subject_id, content").
		WillReturnRows(sqlmock.NewRows([]string{"subject_type", "subject_id", "content", "model", "duration_ms", "created_at"}).
			AddRow("error", int64(42), "## Résumé\nune vieille explication sectionnée", nil, nil, nowUTC()))
	mock.ExpectExec("DELETE FROM explanations").WillReturnResult(sqlmock.NewResult(0, 1))
	// Rebuilding needs the subject, which is no longer there in this test.
	mock.ExpectQuery("SELECT id, organization_id, name, message").WillReturnError(errors.New("gone"))

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x", nil), 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502 after the purge forced a rebuild", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the stale row was not purged: %v", err)
	}
}

// An EXPLAIN_MODE naming no engine must fail loudly rather than silently fall
// back to a different engine than the operator configured.
func TestUnknownExplainModeIsReported(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("EXPLAIN_MODE", "telepathy")

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT subject_type, subject_id, content").WillReturnError(errNoRows())

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x?locale=en", nil), 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
}

// The English path bypasses the cache entirely: the table is language-agnostic
// and would otherwise serve a French answer to an English reader.
func TestEnglishExplanationSkipsTheFrenchCache(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("EXPLAIN_MODE", "unknown-engine")

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	req := mux.SetURLVars(orgContext(httptest.NewRequest("POST", "/x?locale=en", nil), 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the cache was queried for an English request: %v", err)
	}
}

// An SSE consumer must receive the failure as an SSE error event, not as a
// JSON body it is not parsing.
func TestExplainFailureIsFramedAsAnSSEEvent(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	t.Setenv("EXPLAIN_MODE", "unknown-engine")

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	req := httptest.NewRequest("POST", "/x?locale=en", nil)
	req.Header.Set("Accept", "text/event-stream")
	req = mux.SetURLVars(orgContext(req, 1, 7), map[string]string{"id": "42"})
	rec := httptest.NewRecorder()
	handleExplainError(db)(rec, req)

	if !strings.Contains(rec.Body.String(), "event: error") {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}
