package services

import (
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestNewDashboardService(t *testing.T) {
	db, _ := newDB(t)
	if NewDashboardService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestGetHealth_CallsGetHealthForOrgWithZero(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	// orgID=0 branch: 3 QueryRow calls without org filter
	mock.ExpectQuery("FROM services").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(5)))
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
	mock.ExpectQuery("FROM service_results").WillReturnRows(statusRows(0, 0))
	hs, err := svc.GetHealth()
	if err != nil || hs == nil || hs.Status != "healthy" {
		t.Fatalf("expected healthy status, got %v/%v", hs, err)
	}
}

func TestGetHealthForOrg_WithOrgID_StatusHealthy(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	mock.ExpectQuery("FROM services").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("FROM service_results").WillReturnRows(statusRows(0, 0))
	hs, err := svc.GetHealthForOrg(1)
	if err != nil || hs.Status != "healthy" {
		t.Fatalf("expected healthy, got %v/%v", hs, err)
	}
	if hs.Services != 3 {
		t.Fatalf("expected 3 services, got %d", hs.Services)
	}
}

func TestGetHealthForOrg_StatusDegraded(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	// A minority of failing checks degrades the org, it does not take it down.
	mock.ExpectQuery("FROM services").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(5)))
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(10)))
	mock.ExpectQuery("FROM service_results").WillReturnRows(statusRows(2, 0))
	hs, _ := svc.GetHealthForOrg(1)
	if hs.Status != "degraded" {
		t.Fatalf("expected degraded, got %s", hs.Status)
	}
	if hs.ServicesFailing != 2 {
		t.Fatalf("expected 2 failing, got %d", hs.ServicesFailing)
	}
}

// A check that only breaches its warning threshold must not be reported as a
// healthy organization: the services page already shows it as degraded.
func TestGetHealthForOrg_WarningOnly_StatusDegraded(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	mock.ExpectQuery("FROM services").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(5)))
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("FROM service_results").WillReturnRows(statusRows(0, 2))
	hs, _ := svc.GetHealthForOrg(1)
	if hs.Status != "degraded" {
		t.Fatalf("expected degraded, got %s", hs.Status)
	}
}

func TestGetHealthForOrg_MajorityFailing_StatusCritical(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	mock.ExpectQuery("FROM services").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(5)))
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("FROM service_results").WillReturnRows(statusRows(3, 0))
	hs, _ := svc.GetHealthForOrg(1)
	if hs.Status != "critical" {
		t.Fatalf("expected critical, got %s", hs.Status)
	}
}

// Application errors are not an availability signal: a busy app can report
// thousands of handled errors while every check stays green.
func TestGetHealthForOrg_ErrorVolumeDoesNotChangeStatus(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	mock.ExpectQuery("FROM services").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(5)))
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(15000)))
	mock.ExpectQuery("FROM service_results").WillReturnRows(statusRows(0, 0))
	hs, _ := svc.GetHealthForOrg(1)
	if hs.Status != "healthy" {
		t.Fatalf("expected healthy, got %s", hs.Status)
	}
	if hs.Errors24h != 15000 {
		t.Fatalf("expected the error count to still be reported, got %d", hs.Errors24h)
	}
}

func TestGetHealthForOrg_ScanError_DefaultsZero(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	// All three QueryRows fail → scan errors are swallowed, values default to 0
	mock.ExpectQuery("FROM services").WillReturnError(errors.New("db error"))
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db error"))
	mock.ExpectQuery("FROM service_results").WillReturnError(errors.New("db error"))
	hs, err := svc.GetHealthForOrg(1)
	if err != nil || hs == nil {
		t.Fatalf("expected fallback health status, got %v/%v", hs, err)
	}
	if hs.Services != 0 || hs.Errors24h != 0 || hs.ServicesFailing != 0 {
		t.Fatalf("expected zeros on error, got %+v", hs)
	}
}

func TestGetErrorStats_CallsGetErrorStatsForOrgZero(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	// byService query
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"service", "count"}))
	// byError query
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"name", "count"}))
	// recent errors query
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows(errorCols()))
	stats, err := svc.GetErrorStats("")
	if err != nil || stats == nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetErrorStatsForOrg_WithOrgAndFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	now := time.Now()
	method := "GET"
	url := "http://example.com"
	// total count
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(42)))
	// byService
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"service", "count"}).AddRow("api", int64(42)))
	// byError
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows([]string{"name", "count"}).AddRow("NullPointerException", int64(42)))
	// recent errors with HTTP fields populated
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(errorCols()).AddRow(
			int64(1), int64(1), "NullPointerException", "msg", "file.go", 42, now,
			"api", method, url, nil, nil,
		),
	)
	stats, err := svc.GetErrorStatsForOrg(1, "api")
	if err != nil || stats.Total != 42 {
		t.Fatalf("expected total=42, got %v/%v", stats, err)
	}
	if stats.ByService["api"] != 42 {
		t.Fatalf("expected by_service[api]=42, got %v", stats.ByService)
	}
	if len(stats.Recent) != 1 || stats.Recent[0].HTTPMethod == nil || *stats.Recent[0].HTTPMethod != "GET" {
		t.Fatalf("expected 1 recent error with HTTP method, got %v", stats.Recent)
	}
}

func TestGetErrorStatsForOrg_DBError_TotalDefaultsZero(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db fail"))
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db fail"))
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db fail"))
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db fail"))
	stats, err := svc.GetErrorStatsForOrg(1, "")
	if err != nil || stats == nil || stats.Total != 0 {
		t.Fatalf("expected total=0 on error, got %v/%v", stats, err)
	}
}

func TestGetMetricStats_CallsGetMetricStatsForOrgZero(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	now := time.Now()
	mock.ExpectQuery("FROM system_metrics").WillReturnRows(
		sqlmock.NewRows([]string{"avg_cpu", "avg_ram", "http_latency", "max_ts"}).
			AddRow(float64(45.5), float64(60.2), float64(12.3), now),
	)
	stats, err := svc.GetMetricStats("")
	if err != nil || stats == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.CPUAvg != 45.5 {
		t.Fatalf("expected CPUAvg=45.5, got %v", stats.CPUAvg)
	}
}

func TestGetMetricStatsForOrg_WithFilters_NullFields(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	// NULL values → stays at zero defaults
	mock.ExpectQuery("FROM system_metrics").WillReturnRows(
		sqlmock.NewRows([]string{"avg_cpu", "avg_ram", "http_latency", "max_ts"}).
			AddRow(nil, nil, nil, nil),
	)
	stats, err := svc.GetMetricStatsForOrg(1, "api")
	if err != nil || stats == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.CPUAvg != 0 || stats.RAMAvg != 0 || stats.HTTPLatency != nil {
		t.Fatalf("expected zeros for nil fields, got %+v", stats)
	}
}

func TestGetMetricStatsForOrg_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	mock.ExpectQuery("FROM system_metrics").WillReturnError(errors.New("db error"))
	_, err := svc.GetMetricStatsForOrg(1, "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetTimeline_DelegatesToGetTimelineForOrg(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	// Delegates to GetTimelineForOrgFiltered → EventService.GetEventsForOrgFiltered
	mock.ExpectQuery("FROM events").WillReturnRows(sqlmock.NewRows(eventCols()))
	events, err := svc.GetTimeline("", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = events
}

func TestGetTimelineForOrg_WithOrgAndFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	now := time.Now()
	mock.ExpectQuery("FROM events").WillReturnRows(
		sqlmock.NewRows(eventCols()).AddRow(int64(1), int64(1), "deploy", "api", "v2 deployed", nil, now),
	)
	events, err := svc.GetTimelineForOrg(1, "api", 5)
	if err != nil || len(events) != 1 {
		t.Fatalf("expected 1 event, got %v/%v", events, err)
	}
}

func TestGetTimelineForOrgFiltered_WithTimeBounds(t *testing.T) {
	db, mock := newDB(t)
	svc := NewDashboardService(db)
	from := time.Now().Add(-1 * time.Hour)
	to := time.Now()
	mock.ExpectQuery("FROM events").WillReturnRows(sqlmock.NewRows(eventCols()))
	events, err := svc.GetTimelineForOrgFiltered(1, "", 10, from, to)
	if err != nil || len(events) != 0 {
		t.Fatalf("expected empty, got %v/%v", events, err)
	}
}
