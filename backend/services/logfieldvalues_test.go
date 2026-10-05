package services

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// The log facet lists service and host names taken from the customer's own
// infrastructure. It has to be scoped like every other log query, otherwise any
// authenticated user enumerates every tenant's services.
func TestGetLogFieldValuesIsScopedToOrganization(t *testing.T) {
	if os.Getenv("MM_OPENSEARCH_IT") == "" {
		t.Skip("set MM_OPENSEARCH_IT=1 to run against a live OpenSearch")
	}

	s := NewOpenSearchService()
	if err := s.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()
	marker := now.UnixNano()
	mine := fmt.Sprintf("svc_mine_%d", marker)
	theirs := fmt.Sprintf("svc_theirs_%d", marker)

	const myOrg, otherOrg = 1, 424242
	for _, entry := range []struct {
		service string
		orgID   int
	}{{mine, myOrg}, {theirs, otherOrg}} {
		doc := map[string]interface{}{
			"@timestamp":      now.Format(time.RFC3339),
			"organization_id": entry.orgID,
			"service_name":    entry.service,
			"severity_text":   "INFO",
			"body":            "scope test",
		}
		if err := s.IndexLog(ctx, doc); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := http.Post(s.baseURL+"/middle-monitor-logs/_refresh", "application/json", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	start := now.Add(-time.Hour).Format(time.RFC3339)
	end := now.Add(time.Hour).Format(time.RFC3339)

	values, err := s.GetLogFieldValues("service_name", "svc_", myOrg, start, end, 100)
	if err != nil {
		t.Fatalf("field values: %v", err)
	}

	var sawMine bool
	for _, value := range values {
		if value == theirs {
			t.Errorf("service name %q leaked from organization %d", theirs, otherOrg)
		}
		if value == mine {
			sawMine = true
		}
	}
	if !sawMine {
		t.Errorf("own service name %q missing from %v", mine, values)
	}
}
