package services

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewAgentService(t *testing.T) {
	db, _ := newDB(t)
	if NewAgentService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestServiceTypeMapToName_AllBranches(t *testing.T) {
	cases := map[string]string{
		"agent_cpu":     "cpu",
		"agent_ram":     "ram",
		"agent_disk":    "disk",
		"agent_network": "network",
		"agent_other":   "agent_other",
		"unknown":       "unknown",
	}
	for input, expected := range cases {
		if result := serviceTypeMapToName(input); result != expected {
			t.Fatalf("serviceTypeMapToName(%q) = %q, want %q", input, result, expected)
		}
	}
}

func TestRegisterAgent_WithOrgIDFromToken_HostExists_ServiceExists(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	// host exists
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(10)))
	// service "cpu" → agent_cpu exists
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(20)))
	reg := models.AgentRegistration{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  []string{"cpu"},
	}
	resp, err := svc.RegisterAgent(reg, 1)
	if err != nil || resp.HostID != 10 || len(resp.ServiceIDs) != 1 {
		t.Fatalf("expected HostID=10, 1 service, got %v/%v", resp, err)
	}
}

func TestRegisterAgent_DefaultOrgID_OrgSlugEmpty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	// orgIDFromToken=0, OrgSlug="" → orgID=1
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(5)))
	// unknown metric type → skip
	reg := models.AgentRegistration{
		Hostname: "host1",
		Metrics:  []string{"unknown_metric"},
	}
	resp, err := svc.RegisterAgent(reg, 0)
	if err != nil || resp.HostID != 5 || len(resp.ServiceIDs) != 0 {
		t.Fatalf("expected HostID=5, 0 services, got %v/%v", resp, err)
	}
}

func TestRegisterAgent_WithOrgSlug_OrgFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM organizations WHERE slug").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	reg := models.AgentRegistration{
		Hostname: "host1",
		OrgSlug:  "my-org",
		Metrics:  []string{},
	}
	resp, err := svc.RegisterAgent(reg, 0)
	if err != nil || resp.HostID != 7 {
		t.Fatalf("expected HostID=7, got %v/%v", resp, err)
	}
}

func TestRegisterAgent_WithOrgSlug_OrgNotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM organizations WHERE slug").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	reg := models.AgentRegistration{
		Hostname: "host1",
		OrgSlug:  "nonexistent-org",
		Metrics:  []string{},
	}
	_, err := svc.RegisterAgent(reg, 0)
	if err == nil {
		t.Fatal("expected org not found error")
	}
}

func TestRegisterAgent_HostNotFound_CreatesHost(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	// Host lookup returns no rows
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// CreateHost: plan check + host count under cap
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// CreateHost: INSERT
	now := time.Now()
	mock.ExpectQuery("FROM host_groups").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery("INSERT INTO hosts").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(15), now))
	// cpu service lookup
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(25)))
	reg := models.AgentRegistration{
		Hostname: "new-host",
		Service:  "web",
		Metrics:  []string{"cpu"},
	}
	resp, err := svc.RegisterAgent(reg, 1)
	if err != nil || resp.HostID != 15 {
		t.Fatalf("expected HostID=15, got %v/%v", resp, err)
	}
}

func TestRegisterAgent_HostCheckDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnError(errors.New("db error"))
	reg := models.AgentRegistration{Hostname: "host1"}
	_, err := svc.RegisterAgent(reg, 1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegisterAgent_ServiceNotFound_CreatesService(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	now := time.Now()
	// Host exists
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(10)))
	// Service not found for "cpu"
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// CreateService: the agent's host is in the same org.
	mock.ExpectQuery("SELECT organization_id FROM hosts").WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(1)))
	// CreateService: CanCreateService (pro plan); agent services aren't counted.
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	// CreateService INSERT
	mock.ExpectQuery("INSERT INTO services").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(30), now))
	reg := models.AgentRegistration{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  []string{"cpu"},
	}
	resp, err := svc.RegisterAgent(reg, 1)
	if err != nil || len(resp.ServiceIDs) != 1 || resp.ServiceIDs[0] != 30 {
		t.Fatalf("expected serviceID=30, got %v/%v", resp, err)
	}
}

func TestRegisterAgent_ServiceCheckDBError_SkipsMetric(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	// Host exists
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(10)))
	// Service lookup fails (not ErrNoRows → log and continue)
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnError(errors.New("connection error"))
	reg := models.AgentRegistration{
		Hostname: "web-01",
		Metrics:  []string{"cpu"},
	}
	resp, err := svc.RegisterAgent(reg, 1)
	if err != nil || len(resp.ServiceIDs) != 0 {
		t.Fatalf("expected 0 services on error (metric skipped), got %v/%v", resp, err)
	}
}

func TestStoreMetrics_CPU_OverCritical(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	// Service lookup: serviceID=1, failureThreshold=90
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(1), float64(90), nil, nil),
	)
	// INSERT service_results
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"cpu": 95.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_RAM_OverWarning(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(2), nil, float64(70), float64(90)),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"ram": 75.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_Disk_BelowThresholds(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(3), nil, float64(70), float64(90)),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"disk": 50.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_Network_WithMetadata(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(4), nil, nil, nil),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"network": 100.0},
		Metadata: map[string]float64{
			"network_bytes_in_total":    1000,
			"network_bytes_out_total":   2000,
			"network_speed_in_mb_per_s": 10,
			"network_speed_out_mbps":    20,
			"network_ping_latency_ms":   5,
			"network_ping_success":      1,
		},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_Network_SpeedMetadataAlias(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(4), nil, nil, nil),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"network": 100.0},
		Metadata: map[string]float64{
			"network_speed_in_mbps":  50, // old naming → maps to network_speed_in_mb_per_s
			"network_speed_out_mbps": 60, // old naming → maps to network_speed_out_mb_per_s
		},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Network thresholds target the ping latency (ms) from metadata, never the
// metric_value (throughput MB/s): a 0.5 MB/s sample with a 250ms ping must
// breach the 100ms warning threshold, and the latency lands in the latency column.
func TestStoreMetrics_Network_PingLatencyWarning(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(4), nil, 100.0, 300.0),
	)
	mock.ExpectExec("INSERT INTO service_results").
		WithArgs(int64(4), "warning", sqlmock.AnyArg(), sqlmock.AnyArg(), "network", 0.5, sqlmock.AnyArg(), 250.0).
		WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"network": 0.5},
		Metadata: map[string]float64{
			"network_ping_latency_ms": 250,
			"network_ping_success":    1,
		},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestStoreMetrics_Network_PingLatencyCritical(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(4), nil, 100.0, 300.0),
	)
	mock.ExpectExec("INSERT INTO service_results").
		WithArgs(int64(4), "failure", sqlmock.AnyArg(), sqlmock.AnyArg(), "network", 0.5, sqlmock.AnyArg(), 500.0).
		WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"network": 0.5},
		Metadata: map[string]float64{
			"network_ping_latency_ms": 500,
			"network_ping_success":    1,
		},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// A failed ping (ICMP blocked, no ping binary) must not evaluate thresholds nor
// store a latency — the row stays success instead of false-alarming.
func TestStoreMetrics_Network_PingFailed_NoLatency(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(4), nil, 100.0, 300.0),
	)
	mock.ExpectExec("INSERT INTO service_results").
		WithArgs(int64(4), "success", nil, sqlmock.AnyArg(), "network", 0.5, sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"network": 0.5},
		Metadata: map[string]float64{
			"network_ping_latency_ms": 0,
			"network_ping_success":    0,
		},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestStoreMetrics_UnknownMetricType_Skipped(t *testing.T) {
	db, _ := newDB(t)
	svc := NewAgentService(db)
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Metrics:  map[string]float64{"unknown_metric": 42.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_WithExplicitTimestamp(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(1), nil, nil, nil),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	ts := time.Now().Add(-1 * time.Hour)
	metrics := models.AgentMetrics{
		Hostname:  "web-01",
		Service:   "web",
		Metrics:   map[string]float64{"cpu": 30.0},
		Timestamp: ts,
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_CPU_Metadata(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(1), nil, nil, nil),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"cpu": 45.0},
		Metadata: map[string]float64{
			"load_1min":  1.0,
			"load_5min":  0.8,
			"load_15min": 0.5,
		},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_RAM_Metadata(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(1), nil, nil, nil),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"ram": 50.0},
		Metadata: map[string]float64{"ram_total_gb": 16.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_Disk_Metadata(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(1), nil, nil, nil),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"disk": 60.0},
		Metadata: map[string]float64{"disk_total_gb": 500.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_ServiceNotFound_GetOrCreateAgentService(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	now := time.Now()
	// Service lookup returns ErrNoRows → getOrCreateAgentService
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}))
	// getOrCreateAgentService: host exists
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(5)))
	// service lookup by host_id + type
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(20)))
	// INSERT service_results
	mock.ExpectExec("INSERT INTO service_results").WillReturnResult(sqlmock.NewResult(1, 1))
	_ = now
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"cpu": 40.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_ServiceLookupDBError_SkipsMetric(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	// DB error (not ErrNoRows) → log + continue
	mock.ExpectQuery("FROM services WHERE host").WillReturnError(errors.New("connection error"))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"cpu": 40.0},
	}
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreMetrics_InsertError_SkipsMetric(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE host").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold", "warning_threshold", "critical_threshold"}).
			AddRow(int64(1), nil, nil, nil),
	)
	mock.ExpectExec("INSERT INTO service_results").WillReturnError(errors.New("insert failed"))
	metrics := models.AgentMetrics{
		Hostname: "web-01",
		Service:  "web",
		Metrics:  map[string]float64{"cpu": 40.0},
	}
	// error is logged, not returned
	if err := svc.StoreMetrics(metrics, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetAgentMetrics_NotAgentService(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "host", "type", "name", "service"}).
			AddRow(int64(1), "web-01", "http", "http-check", "web"),
	)
	_, err := svc.GetAgentMetrics(1, 10)
	if err == nil || err.Error() != "not an agent service" {
		t.Fatalf("expected 'not an agent service', got %v", err)
	}
}

// A missing row and an unreachable database are two different answers: the
// first one means the service is gone, the second one that we cannot tell.
func TestGetAgentMetrics_ServiceNotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE id").WillReturnError(sql.ErrNoRows)
	_, err := svc.GetAgentMetrics(999, 10)
	if !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("expected ErrServiceNotFound, got %v", err)
	}

	mock.ExpectQuery("FROM services WHERE id").WillReturnError(errors.New("connection refused"))
	_, err = svc.GetAgentMetrics(999, 10)
	if !errors.Is(err, ErrServiceFetch) {
		t.Fatalf("expected ErrServiceFetch, got %v", err)
	}
}

func TestGetAgentMetrics_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	now := time.Now()
	mock.ExpectQuery("FROM services WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "host", "type", "name", "service"}).
			AddRow(int64(1), "web-01", "agent_cpu", "cpu", "web"),
	)
	mock.ExpectQuery("FROM service_results WHERE service_id").WillReturnRows(
		sqlmock.NewRows([]string{"metric_value", "metadata", "timestamp"}).
			AddRow(float64(45.0), `{"load_1min":1.5}`, now).
			AddRow(nil, nil, now.Add(-time.Minute)), // NULL metric_value → skip
	)
	metrics, err := svc.GetAgentMetrics(1, 50)
	if err != nil || len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %v/%v", metrics, err)
	}
	if metrics[0].Value != 45.0 {
		t.Fatalf("expected value=45.0, got %v", metrics[0].Value)
	}
}

func TestGetAgentMetrics_ResultsDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM services WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "host", "type", "name", "service"}).
			AddRow(int64(1), "web-01", "agent_ram", "ram", "web"),
	)
	mock.ExpectQuery("FROM service_results WHERE service_id").WillReturnError(errors.New("db error"))
	_, err := svc.GetAgentMetrics(1, 50)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAgentMetrics_InvalidMetadataJSON_Ignored(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	now := time.Now()
	mock.ExpectQuery("FROM services WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "host", "type", "name", "service"}).
			AddRow(int64(1), "web-01", "agent_disk", "disk", "web"),
	)
	mock.ExpectQuery("FROM service_results WHERE service_id").WillReturnRows(
		sqlmock.NewRows([]string{"metric_value", "metadata", "timestamp"}).
			AddRow(float64(80.0), "not-valid-json", now),
	)
	metrics, err := svc.GetAgentMetrics(1, 50)
	if err != nil || len(metrics) != 1 || metrics[0].Metadata != nil {
		t.Fatalf("expected 1 metric with nil metadata, got %v/%v", metrics, err)
	}
}

func TestGetOrCreateAgentService_HostNotFound_Creates(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	now := time.Now()
	// Host not found
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// CreateHost: pro plan, host count under cap
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("FROM host_groups").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery("INSERT INTO hosts").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(99), now))
	// Service not found
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// CreateService: the host it just created is in the same org.
	mock.ExpectQuery("SELECT organization_id FROM hosts").WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(1)))
	// CreateService: pro plan; agent services aren't counted.
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	// INSERT service
	mock.ExpectQuery("INSERT INTO services").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(55), now))
	serviceID, err := svc.getOrCreateAgentService(1, "new-host", "web", "agent_cpu")
	if err != nil || serviceID != 55 {
		t.Fatalf("expected serviceID=55, got %v/%v", serviceID, err)
	}
}

func TestGetOrCreateAgentService_HostDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnError(errors.New("db error"))
	_, err := svc.getOrCreateAgentService(1, "host1", "web", "agent_cpu")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetOrCreateAgentService_ServiceDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAgentService(db)
	// Host exists
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(10)))
	// Service lookup fails (not ErrNoRows)
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnError(errors.New("db error"))
	_, err := svc.getOrCreateAgentService(1, "host1", "web", "agent_cpu")
	if err == nil {
		t.Fatal("expected error")
	}
}
