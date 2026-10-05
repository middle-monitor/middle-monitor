package services

import (
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewMetricService(t *testing.T) {
	db, _ := newDB(t)
	if NewMetricService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestCreateMetric_DefaultTimestampAndOrg(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMetricService(db)
	mock.ExpectQuery("INSERT INTO system_metrics").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	m, err := svc.CreateMetric(models.SystemMetric{}) // zero timestamp, zero orgID
	if err != nil || m.ID != 1 {
		t.Fatalf("expected created metric: %v/%v", m, err)
	}
	if m.OrganizationID != 1 {
		t.Fatalf("expected default orgID 1, got %d", m.OrganizationID)
	}
}

func TestCreateMetric_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMetricService(db)
	mock.ExpectQuery("INSERT INTO system_metrics").WillReturnError(sql.ErrConnDone)
	_, err := svc.CreateMetric(models.SystemMetric{OrganizationID: 1})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetMetrics_NoFilter(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMetricService(db)
	mock.ExpectQuery("SELECT id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "service", "cpu_perc", "ram_perc",
			"http_latency", "endpoint", "timestamp",
		}))
	metrics, err := svc.GetMetrics("")
	if err != nil || len(metrics) != 0 {
		t.Fatalf("expected empty metrics: %v/%v", metrics, err)
	}
}

func TestGetMetricsForOrg_AllFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMetricService(db)
	now := time.Now()
	latency := 10.5
	endpoint := "/api"
	mock.ExpectQuery("SELECT id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "service", "cpu_perc", "ram_perc",
			"http_latency", "endpoint", "timestamp",
		}).AddRow(1, sql.NullInt64{Int64: 1, Valid: true}, "api", 55.0, 70.0, &latency, &endpoint, now))
	metrics, err := svc.GetMetricsForOrg(1, "api")
	if err != nil || len(metrics) != 1 {
		t.Fatalf("expected 1 metric: %v/%v", metrics, err)
	}
}

func TestGetMetricsForOrg_NullOrgID(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMetricService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "service", "cpu_perc", "ram_perc",
			"http_latency", "endpoint", "timestamp",
		}).AddRow(1, sql.NullInt64{Valid: false}, "api", 55.0, 70.0, nil, nil, now))
	metrics, err := svc.GetMetricsForOrg(0, "")
	if err != nil || len(metrics) != 1 {
		t.Fatalf("expected 1 metric: %v/%v", metrics, err)
	}
}

func TestGetMetricsForOrg_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMetricService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetMetricsForOrg(1, "")
	if err == nil {
		t.Fatal("expected DB error")
	}
}
