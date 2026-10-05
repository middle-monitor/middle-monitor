package services

import (
	"testing"
)

// The trace search used to be a multi_match over four fields, so a facet typed
// out of habit in the log view ("service:catalog-service") matched nothing.
// The two views advertise the same syntax and must answer the same way.
func TestNormalizeTraceQueryRewritesDatadogAliases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"service alias", "service:catalog-service", "service_name:catalog-service"},
		{"operation alias", "operation:GET", "operation_name:GET"},
		{"status alias", "status:ERROR", "status_code:ERROR"},
		{"kind alias", "kind:SPAN_KIND_SERVER", "span_kind:SPAN_KIND_SERVER"},
		{"host alias", "host:web-01", "hostname:web-01"},
		{"real field names pass through", "span_id:abc parent_span_id:def", "span_id:abc parent_span_id:def"},
		{"space after the colon", "service: catalog-service", "service_name:catalog-service"},
		{"word containing an alias is not a facet", "myservice:x", "myservice:x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeTraceQuery(tc.in); got != tc.want {
				t.Errorf("NormalizeTraceQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// An empty query must produce no clause so the handler keeps its match_all
// path and the view still lists recent spans.
func TestBuildTraceQueryClauseEmptyQuery(t *testing.T) {
	if BuildTraceQueryClause("") != nil {
		t.Error("BuildTraceQueryClause(\"\") should be nil")
	}
	if BuildTraceQueryClause("   ") != nil {
		t.Error("BuildTraceQueryClause(blank) should be nil")
	}
}

// Copying a span id out of a log detail and pasting it in the trace view is the
// whole point of correlating the two. span_id was absent from the searched
// fields, so that paste returned nothing.
func TestBuildTraceQueryClauseSearchesSpanID(t *testing.T) {
	clause := BuildTraceQueryClause("c60c2dcb4731450d")
	qs, ok := clause["query_string"].(map[string]interface{})
	if !ok {
		t.Fatalf("clause is not a query_string: %v", clause)
	}
	fields, ok := qs["fields"].([]string)
	if !ok {
		t.Fatalf("fields is not a []string: %v", qs["fields"])
	}
	for _, want := range []string{"trace_id", "span_id", "parent_span_id"} {
		found := false
		for _, f := range fields {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("trace free text does not search %q, fields = %v", want, fields)
		}
	}
}

// Datadog semantics: bare terms are ANDed, and a term probed against a
// non-text field degrades instead of failing the whole search.
func TestBuildTraceQueryClauseShape(t *testing.T) {
	clause := BuildTraceQueryClause("status:ERROR checkout")
	qs := clause["query_string"].(map[string]interface{})
	if qs["default_operator"] != "AND" {
		t.Errorf("default_operator = %v, want AND", qs["default_operator"])
	}
	if qs["lenient"] != true {
		t.Errorf("lenient = %v, want true", qs["lenient"])
	}
	if qs["query"] != "status_code:ERROR checkout" {
		t.Errorf("query = %v, want status_code:ERROR checkout", qs["query"])
	}
}
