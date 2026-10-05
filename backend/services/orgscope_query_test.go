package services

import (
	"encoding/json"
	"strings"
	"testing"
)

// The integration tests behind MM_OPENSEARCH_IT never run in CI, so tenant
// isolation would regress silently. These assert on the query the service
// SENDS, which runs everywhere: if the org term disappears from the built
// query, the data leaks, whatever the cluster then does.

func queryJSON(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func TestLogFieldValuesQueryIsScopedToOrganization(t *testing.T) {
	got := queryJSON(t, logFieldValuesQuery("service_name", "web", 42, "2026-08-01T00:00:00Z", "2026-08-02T00:00:00Z", 50))

	if !strings.Contains(got, `{"term":{"organization_id":42}}`) {
		t.Fatalf("facet query lost its organization scope:\n%s", got)
	}
	if !strings.Contains(got, `"include":"web.*"`) {
		t.Errorf("prefix filter missing:\n%s", got)
	}
}

// The scope must hold on the minimal call too — an empty time range or prefix
// must not take a different, unscoped branch.
func TestLogFieldValuesQueryStaysScopedWithoutOptionalArgs(t *testing.T) {
	got := queryJSON(t, logFieldValuesQuery("hostname", "", 7, "", "", 10))
	if !strings.Contains(got, `{"term":{"organization_id":7}}`) {
		t.Fatalf("facet query lost its organization scope:\n%s", got)
	}
}

func TestTraceByIDQueryIsScopedToOrganization(t *testing.T) {
	got := queryJSON(t, traceByIDQuery(42, "abc123", 100))

	if !strings.Contains(got, `{"term":{"organization_id":42}}`) {
		t.Fatalf("trace lookup lost its organization scope:\n%s", got)
	}
	if !strings.Contains(got, `{"term":{"trace_id":"abc123"}}`) {
		t.Errorf("trace id filter missing:\n%s", got)
	}
}
