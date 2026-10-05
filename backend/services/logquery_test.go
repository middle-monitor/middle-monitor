package services

import (
	"testing"
)

// Datadog facet names must reach OpenSearch as real index fields, otherwise a
// query like service:checkout silently matches nothing (the index has
// service_name, not service).
func TestNormalizeLogQueryRewritesDatadogAliases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"service alias", "service:checkout", "service_name:checkout"},
		{"status and severity both mean severity_text", "status:ERROR severity:WARN", "severity_text:ERROR severity_text:WARN"},
		{"host alias", "host:web-01", "hostname:web-01"},
		{"real field names pass through", "service_name:x severity_text:Y hostname:z", "service_name:x severity_text:Y hostname:z"},
		{"alias inside operators and parens", "-service:web (status:ERROR OR status:WARN)", "-service_name:web (severity_text:ERROR OR severity_text:WARN)"},
		{"lucene operators untouched", "a && service:b || NOT c", "a && service_name:b || NOT c"},
		{"word containing an alias is not a facet", "myservice:x disservice", "myservice:x disservice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeLogQuery(tc.in); got != tc.want {
				t.Errorf("NormalizeLogQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Quoted phrases are user text: rewriting a facet name inside them would
// change what the user searched for.
func TestNormalizeLogQueryLeavesQuotedTextAlone(t *testing.T) {
	in := `service:api "restart the service: now" status:ERROR`
	want := `service_name:api "restart the service: now" severity_text:ERROR`
	if got := NormalizeLogQuery(in); got != want {
		t.Errorf("NormalizeLogQuery(%q) = %q, want %q", in, got, want)
	}
}

// An empty query must produce no clause at all so the handler keeps its
// match_all path (returning recent logs) instead of an empty query_string.
func TestBuildLogQueryClauseEmptyQuery(t *testing.T) {
	if BuildLogQueryClause("") != nil {
		t.Error("BuildLogQueryClause(\"\") should be nil")
	}
	if BuildLogQueryClause("   ") != nil {
		t.Error("BuildLogQueryClause(blank) should be nil")
	}
}

// The clause must keep Datadog semantics: implicit AND between bare terms
// (default_operator) and tolerance to type mismatches (lenient), so a term
// probed against a non-text field degrades instead of failing the search.
func TestBuildLogQueryClauseShape(t *testing.T) {
	clause := BuildLogQueryClause("status:ERROR timed out")
	qs, ok := clause["query_string"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected query_string clause, got %v", clause)
	}
	if qs["query"] != "severity_text:ERROR timed out" {
		t.Errorf("query = %v, want severity_text:ERROR timed out", qs["query"])
	}
	if qs["default_operator"] != "AND" {
		t.Errorf("default_operator = %v, want AND (Datadog implicit AND)", qs["default_operator"])
	}
	if qs["lenient"] != true {
		t.Error("lenient must be true so field/type mismatches do not fail the search")
	}
	fields, _ := qs["fields"].([]string)
	hasBody := false
	for _, f := range fields {
		if f == "body" {
			hasBody = true
		}
	}
	if !hasBody {
		t.Error("bare terms must search the log body")
	}
}

func TestResolveLogFieldAlias(t *testing.T) {
	if got := ResolveLogFieldAlias("service"); got != "service_name" {
		t.Errorf("service -> %q, want service_name", got)
	}
	if got := ResolveLogFieldAlias("trace_id"); got != "trace_id" {
		t.Errorf("non-alias must pass through, got %q", got)
	}
}

// A space after the colon is what people actually type. Lucene reads
// "service_name: api" as an empty field term, so the search returned nothing
// while looking perfectly valid on screen.
func TestNormalizeLogQueryClosesSpaceAfterFacet(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"alias with space", "service: api-gateway", "service_name:api-gateway"},
		{"index field with space", "span_id: c60c2dcb4731450d", "span_id:c60c2dcb4731450d"},
		{"several spaces", "status:   ERROR", "severity_text:ERROR"},
		{"combined with operators", "status: ERROR AND service: api", "severity_text:ERROR AND service_name:api"},
		{"unknown word keeps its colon", "timeout: refused", "timeout: refused"},
		{"quoted text untouched", `"restart the service: now"`, `"restart the service: now"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeLogQuery(tc.in); got != tc.want {
				t.Errorf("NormalizeLogQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Pasting an id read off a log or a trace detail is the fastest way into a
// correlation. Free text has to reach the id fields or that paste finds nothing.
func TestBuildLogQueryClauseSearchesIDFields(t *testing.T) {
	clause := BuildLogQueryClause("c60c2dcb4731450d")
	qs := clause["query_string"].(map[string]interface{})
	fields := qs["fields"].([]string)
	for _, want := range []string{"trace_id", "span_id"} {
		found := false
		for _, f := range fields {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("log free text does not search %q, fields = %v", want, fields)
		}
	}
}
