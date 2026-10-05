package services

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestDeriveServiceStatus pins the facet status to the SQL effective status: a
// service only counts as failing/warning once the last max_attempts results ALL
// breach (computed in SQL), so a single bad sample must NOT flip the facets —
// that would desync them from the badge and pre-empt the worker's alert.
func TestDeriveServiceStatus(t *testing.T) {
	cases := []struct {
		name            string
		hasResult       bool
		effectiveStatus string
		want            string
	}{
		{"no result", false, "", "unknown"},
		{"streak of failures", true, "failure", "failing"},
		{"streak of warnings", true, "warning", "warning"},
		{"incomplete streak stays healthy", true, "success", "healthy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := deriveServiceStatus(c.hasResult, c.effectiveStatus)
			if got != c.want {
				t.Fatalf("deriveServiceStatus(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

var serviceRowCols = []string{
	"id", "organization_id", "host_id", "name", "display_name", "type", "host", "path", "credentials",
	"service", "service_interval", "max_attempts", "failure_threshold", "warning_threshold", "critical_threshold",
	"expected_status_code", "expected_body_contains", "expected_body_mode", "token", "created_at", "host_name",
	"status", "has_result",
}

func TestServiceStats(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	now := time.Now()
	rows := sqlmock.NewRows(serviceRowCols).
		// http check, effective status success -> healthy
		AddRow(int64(1), int64(1), nil, "api", nil, "http", "example.com", nil, nil, "api", 60, 3,
			nil, nil, nil, nil, nil, nil, nil, now, nil, "success", true).
		// agent_cpu with max_attempts consecutive breaches -> failing
		AddRow(int64(2), int64(1), nil, "cpu", nil, "agent_cpu", "web-01", nil, nil, "web", 60, 3,
			nil, 80.0, 90.0, nil, nil, nil, nil, now, "web-01", "failure", true)
	mock.ExpectQuery("FROM services s").WillReturnRows(rows)

	stats, err := svc.ServiceStats(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Total != 2 || stats.Healthy != 1 || stats.Failing != 1 || stats.Warning != 0 || stats.Unknown != 0 {
		t.Fatalf("unexpected counts: %+v", stats)
	}
	if len(stats.Types) != 2 || stats.Types[0] != "agent_cpu" || stats.Types[1] != "http" {
		t.Fatalf("unexpected types: %v", stats.Types)
	}
}
