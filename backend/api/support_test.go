package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"middle-monitor/backend/services"
)

// A 5xx carries internal detail (SQL text, paths, driver messages); the client
// must only ever see the generic status text.
func TestRespondErrorHidesServerSideDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusInternalServerError, errors.New("pq: relation \"users\" does not exist"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "Internal Server Error" {
		t.Fatalf("internal detail leaked: %q", body["error"])
	}
}

// A 4xx is an actionable validation message: the client is meant to read it.
func TestRespondErrorReturnsClientErrorText(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusBadRequest, ErrNameRequired)

	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != ErrNameRequired.Error() {
		t.Fatalf("got %q", body["error"])
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type %q", got)
	}
}

func TestRespondErrorWithoutAnError(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusForbidden, nil)
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "Forbidden" {
		t.Fatalf("got %q", body["error"])
	}
}

// A converging client (Terraform, a script) turns a 409 into a read, which it
// can only do if the conflict names the row that already holds the key.
func TestRespondConflictNamesTheExistingRow(t *testing.T) {
	rec := httptest.NewRecorder()
	respondConflict(rec, ErrEmailTaken, 42)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "already_exists" || body["id"] != float64(42) {
		t.Fatalf("got %v", body)
	}

	// Without a known id the payload must simply omit it rather than send 0.
	rec = httptest.NewRecorder()
	respondConflict(rec, ErrEmailTaken, 0)
	body = map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, ok := body["id"]; ok {
		t.Fatalf("id 0 must not be sent: %v", body)
	}
}

// The frontend shows an upgrade CTA on this code rather than a plain error, so
// the machine-readable marker is the point of this response.
func TestRespondPlanLimitCarriesItsCode(t *testing.T) {
	rec := httptest.NewRecorder()
	respondPlanLimit(rec, errors.New("host limit reached (3)"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "plan_limit" || !strings.Contains(body["error"], "host limit reached") {
		t.Fatalf("got %v", body)
	}
}

func TestRespondJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, map[string]int{"count": 3})
	if !strings.Contains(rec.Body.String(), `"count":3`) {
		t.Fatalf("got %q", rec.Body.String())
	}
}

// An absent or nonsensical limit must not disable pagination, and a client
// cannot lift the cap by asking for a bigger page.
func TestParseLimitOffsetIsAlwaysBounded(t *testing.T) {
	cases := []struct {
		query              string
		wantLimit, wantOff int
	}{
		{"", 50, 0},
		{"?limit=10&offset=20", 10, 20},
		{"?limit=99999", 200, 0},
		{"?limit=abc&offset=xyz", 50, 0},
		{"?limit=0&offset=-5", 50, 0},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x"+c.query, nil)
		limit, offset := parseLimitOffset(r, 50, 200)
		if limit != c.wantLimit || offset != c.wantOff {
			t.Fatalf("%q gave (%d,%d), want (%d,%d)", c.query, limit, offset, c.wantLimit, c.wantOff)
		}
	}
}

// A shell pipeline produces unix seconds, a client library RFC 3339; both are
// legitimate, and an unparseable value is ignored rather than failing the call.
func TestParseTimeRangeAcceptsBothFormats(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?since=2026-09-02T10:00:00Z&until=1756800000", nil)
	since, until := parseTimeRange(r)
	if since == nil || since.UTC().Hour() != 10 {
		t.Fatalf("since = %v", since)
	}
	if until == nil || until.Unix() != 1756800000 {
		t.Fatalf("until = %v", until)
	}

	none, _ := parseTimeRange(httptest.NewRequest("GET", "/x", nil))
	if none != nil {
		t.Fatalf("absent param must be nil, got %v", none)
	}
	bad, _ := parseTimeRange(httptest.NewRequest("GET", "/x?since=yesterday", nil))
	if bad != nil {
		t.Fatalf("an unparseable value is ignored, got %v", bad)
	}
}

func TestValidateName(t *testing.T) {
	var required *FieldRequiredError
	if err := validateName("name", "   ", maxNameLen); !errors.As(err, &required) {
		t.Fatalf("got %v", err)
	}
	var tooLong *FieldTooLongError
	if err := validateName("name", strings.Repeat("a", maxNameLen+1), maxNameLen); !errors.As(err, &tooLong) {
		t.Fatalf("got %v", err)
	}
	if err := validateName("name", " ok ", maxNameLen); err != nil {
		t.Fatalf("got %v", err)
	}
}

// Internal systems create services with generated type prefixes; rejecting
// those would break error-service and agent provisioning.
func TestValidateServiceTypeAllowsGeneratedPrefixes(t *testing.T) {
	for _, ok := range []string{"http", "ping", "snmp", "certificate", "sql", "error_service_go", "agent_cpu"} {
		if err := validateServiceType(ok); err != nil {
			t.Fatalf("%q was rejected: %v", ok, err)
		}
	}
	if err := validateServiceType("carrier_pigeon"); !errors.Is(err, ErrServiceTypeInvalid) {
		t.Fatalf("got %v", err)
	}
}

// The interval floor protects the checker from being turned into a load
// generator, so both ends of the range have to be enforced.
func TestValidateServiceInterval(t *testing.T) {
	for _, bad := range []int{0, 59, 3601} {
		var rangeErr *RangeError
		if err := validateServiceInterval(bad); !errors.As(err, &rangeErr) {
			t.Fatalf("%d was accepted", bad)
		}
	}
	for _, ok := range []int{60, 300, 3600} {
		if err := validateServiceInterval(ok); err != nil {
			t.Fatalf("%d was rejected: %v", ok, err)
		}
	}
}

func TestValidateMaxAttempts(t *testing.T) {
	for _, bad := range []int{0, 11} {
		if err := validateMaxAttempts(bad); err == nil {
			t.Fatalf("%d was accepted", bad)
		}
	}
	if err := validateMaxAttempts(3); err != nil {
		t.Fatalf("got %v", err)
	}
}

// Custom status codes exist in the wild, so the check is on the shape of an
// HTTP status code, not on a whitelist.
func TestValidateExpectedStatusCodeAcceptsCustomCodes(t *testing.T) {
	for _, ok := range []int{100, 200, 743, 999} {
		if err := validateExpectedStatusCode(ok); err != nil {
			t.Fatalf("%d was rejected", ok)
		}
	}
	for _, bad := range []int{0, 99, 1000} {
		if !errors.Is(validateExpectedStatusCode(bad), ErrExpectedStatusCodeInvalid) {
			t.Fatalf("%d was accepted", bad)
		}
	}
}

func TestValidateChannelAndMaintenanceTypes(t *testing.T) {
	for _, ok := range []string{"email", "slack", "jsm", "whatsapp", "webhook"} {
		if err := validateChannelType(ok); err != nil {
			t.Fatalf("%q was rejected", ok)
		}
	}
	if !errors.Is(validateChannelType("carrier_pigeon"), ErrChannelTypeInvalid) {
		t.Fatal("an unknown channel type was accepted")
	}

	for _, ok := range []string{"service", "host"} {
		if err := validateMaintenanceTargetType(ok); err != nil {
			t.Fatalf("%q was rejected", ok)
		}
	}
	if !errors.Is(validateMaintenanceTargetType("cluster"), ErrTargetTypeInvalid) {
		t.Fatal("an unknown target type was accepted")
	}
}

// Typed errors exist so a caller can match on the kind of failure; their text
// is what the client reads, so it has to name the offending value.
func TestTypedErrorMessagesNameTheirSubject(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&FieldRequiredError{Field: "name"}, "name is required"},
		{&FieldTooLongError{Field: "name", Max: 255}, "name must be 255 characters or fewer"},
		{&RangeError{What: "service interval", Min: 60, Max: 3600, Unit: "seconds"}, "service interval must be between 60 and 3600 seconds"},
		{&RangeError{What: "max attempts", Min: 1, Max: 10}, "max attempts must be between 1 and 10"},
		{&RecordNotFoundError{Kind: "host", ID: 4, OrgID: 3}, "host 4 not found in org 3"},
		{&SubjectTypeError{SubjectType: "incident"}, `unsupported subject_type "incident"`},
		{&ExplainModeError{Mode: "telepathy"}, `unknown EXPLAIN_MODE "telepathy" (expected rules or single_shot)`},
		{&LLMStatusError{Provider: "LLM", StatusCode: 429, Body: "slow down"}, "LLM returned 429: slow down"},
		{&LLMProviderError{Provider: "LLM", Message: "overloaded"}, "LLM error: overloaded"},
		{&LLMEmptyError{Provider: "LLM"}, "LLM produced an empty explanation"},
		{&LLMEmptyError{Provider: "LLM", Detail: "no choices"}, "LLM produced an empty explanation (no choices)"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Fatalf("got %q, want %q", got, c.want)
		}
	}

	// Wrapped errors must stay matchable with errors.Is through Unwrap.
	cause := errors.New("EOF")
	decode := &LLMDecodeError{Provider: "LLM", Err: cause, Body: "<html>"}
	if !errors.Is(decode, cause) || !strings.Contains(decode.Error(), "<html>") {
		t.Fatalf("got %v", decode)
	}
	dirty := &MigrationDirtyError{Version: 42, Hint: "fix it", Err: cause}
	if !errors.Is(dirty, cause) || !strings.Contains(dirty.Error(), "version 42") {
		t.Fatalf("got %v", dirty)
	}
}

// The series window defaults to the last hour so a caller can omit the range,
// and an unparseable timestamp is a client mistake worth reporting.
func TestSeriesTimeRangeDefaultsToTheLastHour(t *testing.T) {
	start, end, err := seriesTimeRange(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatalf("got %v", err)
	}
	if d := end.Sub(start); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("window is %v, want one hour", d)
	}

	explicit := httptest.NewRequest("GET", "/x?start=2026-09-01T00:00:00Z&end=2026-09-02T00:00:00Z", nil)
	start, end, err = seriesTimeRange(explicit)
	if err != nil || end.Sub(start) != 24*time.Hour {
		t.Fatalf("got %v..%v (%v)", start, end, err)
	}

	if _, _, err := seriesTimeRange(httptest.NewRequest("GET", "/x?end=yesterday", nil)); err == nil {
		t.Fatal("an unparseable end must be reported")
	}
	if _, _, err := seriesTimeRange(httptest.NewRequest("GET", "/x?start=yesterday", nil)); err == nil {
		t.Fatal("an unparseable start must be reported")
	}
}

// The terms size bounds an aggregation that runs on the search backend, so a
// caller cannot ask for an unbounded one.
func TestTermsSizeIsBounded(t *testing.T) {
	cases := map[string]int{
		"":            defaultTermsSize,
		"?size=10":    10,
		"?size=99999": maxTermsSize,
		"?size=abc":   defaultTermsSize,
		"?size=-1":    defaultTermsSize,
	}
	for query, want := range cases {
		if got := termsSize(httptest.NewRequest("GET", "/x"+query, nil)); got != want {
			t.Fatalf("%q gave %d, want %d", query, got, want)
		}
	}
}

// A bad metric name or aggregation is the caller's mistake and must surface as
// a 400; anything else is ours and stays a 500.
func TestIsSeriesClientError(t *testing.T) {
	for _, clientErr := range []error{
		services.ErrSeriesMetricRequired,
		services.ErrSeriesAggregation,
		services.ErrSeriesRange,
	} {
		if !isSeriesClientError(fmt.Errorf("query: %w", clientErr)) {
			t.Fatalf("%v must surface as a 400", clientErr)
		}
	}
	if isSeriesClientError(errors.New("opensearch unreachable")) {
		t.Fatal("a backend failure must not be reported as a client error")
	}
}
