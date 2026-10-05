package services

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewHostService(t *testing.T) {
	db, _ := newDB(t)
	if NewHostService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestToNullString_Nil(t *testing.T) {
	if toNullString(nil) != nil {
		t.Fatal("expected nil for nil string pointer")
	}
}

func TestToNullString_EmptyString(t *testing.T) {
	s := ""
	if toNullString(&s) != nil {
		t.Fatal("expected nil for empty string")
	}
}

func TestToNullString_NonEmpty(t *testing.T) {
	s := "value"
	result := toNullString(&s)
	if result != s {
		t.Fatalf("expected %q, got %v", s, result)
	}
}

func TestCreateHost_PlanLimitError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	// Plan check: GetPlan → "free", COUNT hosts → already at limit
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("FROM hosts").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	_, err := svc.CreateHost(models.Host{OrganizationID: 1, Name: "web-01", Host: "192.168.1.1"})
	if err == nil {
		t.Fatal("expected plan limit error")
	}
}

func TestCreateHost_PlanLimitDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM organizations").WillReturnError(errors.New("db error"))
	_, err := svc.CreateHost(models.Host{OrganizationID: 1})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateHost_InsertError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	// Plan check passes (pro plan → no limit)
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("FROM host_groups").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery("INSERT INTO hosts").WillReturnError(errors.New("insert failed"))
	_, err := svc.CreateHost(models.Host{OrganizationID: 1, Name: "web-01", Host: "192.168.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateHost_DuplicateKeyError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("FROM host_groups").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery("INSERT INTO hosts").WillReturnError(errors.New("duplicate key value violates unique constraint"))
	_, err := svc.CreateHost(models.Host{OrganizationID: 1, Name: "web-01", Host: "192.168.1.1"})
	if err == nil || err.Error() == "insert failed" {
		t.Fatalf("expected duplicate key error, got %v", err)
	}
}

func TestCreateHost_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("FROM host_groups").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery("INSERT INTO hosts").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), now))
	h, err := svc.CreateHost(models.Host{OrganizationID: 1, Name: "web-01", Host: "192.168.1.1", Service: "web"})
	if err != nil || h.ID != 1 {
		t.Fatalf("expected h.ID=1, got %v/%v", h, err)
	}
}

func TestCreateHost_DefaultOrgID(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	// OrganizationID == 0 → defaults to 1
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("FROM host_groups").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery("INSERT INTO hosts").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(2), now))
	h, err := svc.CreateHost(models.Host{OrganizationID: 0, Name: "host-01", Host: "10.0.0.1"})
	if err != nil || h.OrganizationID != 1 {
		t.Fatalf("expected orgID=1, got %v/%v", h, err)
	}
}

func TestGetHosts_DelegatesToGetHostsForOrgZero(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	// GetHosts → GetHostsForOrg(0, ...) → orgID=0 branch (no org filter)
	mock.ExpectQuery("FROM hosts").WillReturnRows(sqlmock.NewRows(hostCols))
	hosts, err := svc.GetHosts("", false)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("expected empty, got %v/%v", hosts, err)
	}
}

func TestGetHostsForOrg_NoHosts(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts").WillReturnRows(sqlmock.NewRows(hostCols))
	hosts, err := svc.GetHostsForOrg(1, "", false)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("expected empty, got %v/%v", hosts, err)
	}
}

func TestGetHostsForOrg_WithFilters_NoHosts(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts").WillReturnRows(sqlmock.NewRows(hostCols))
	hosts, err := svc.GetHostsForOrg(1, "web", false)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("expected empty, got %v/%v", hosts, err)
	}
}

func TestGetHostsForOrg_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts").WillReturnError(errors.New("db error"))
	_, err := svc.GetHostsForOrg(1, "", false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostsForOrg_OneHost_GetHostStatusUnknown(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	mock.ExpectQuery("FROM hosts").WillReturnRows(
		sqlmock.NewRows(hostCols).AddRow(int64(1), int64(1), "web-01", "192.168.1.1", "web", nil, now),
	)
	// hostStatuses: the host has no service row → "unknown"
	mock.ExpectQuery("FROM services s").WillReturnRows(sqlmock.NewRows(hostStatusCols()))
	hosts, err := svc.GetHostsForOrg(1, "", false)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %v/%v", hosts, err)
	}
	if hosts[0].Status == nil || *hosts[0].Status != "unknown" {
		t.Fatalf("expected status=unknown, got %v", hosts[0].Status)
	}
}

func TestGetHostsForOrg_OneHost_WithDisplayName(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	displayName := "Web Server 01"
	mock.ExpectQuery("FROM hosts").WillReturnRows(
		sqlmock.NewRows(hostCols).AddRow(int64(1), int64(1), "web-01", "192.168.1.1", "web", displayName, now),
	)
	mock.ExpectQuery("FROM services s").WillReturnRows(sqlmock.NewRows(hostStatusCols()))
	hosts, err := svc.GetHostsForOrg(1, "", false)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %v/%v", hosts, err)
	}
	if hosts[0].DisplayName == nil || *hosts[0].DisplayName != displayName {
		t.Fatalf("expected display_name=%q, got %v", displayName, hosts[0].DisplayName)
	}
}

func TestGetHostsForOrg_IncludeServices_OneService(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	hostID := int64(1)
	svcID := int64(10)
	mock.ExpectQuery("FROM hosts").WillReturnRows(
		sqlmock.NewRows(hostCols).AddRow(hostID, int64(1), "web-01", "192.168.1.1", "web", nil, now),
	)
	// services for host
	svcCols := []string{"id", "host_id", "name", "display_name", "type", "host", "path", "credentials", "service", "service_interval", "max_attempts", "failure_threshold", "token", "created_at"}
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(
		sqlmock.NewRows(svcCols).AddRow(svcID, hostID, "http-check", nil, "http", "http://example.com", nil, nil, "web", 60, 3, nil, nil, now),
	)
	// hostStatuses: one query for every host of the list
	mock.ExpectQuery("FROM services s").WillReturnRows(sqlmock.NewRows(hostStatusCols()))
	hosts, err := svc.GetHostsForOrg(1, "", true)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %v/%v", hosts, err)
	}
	if len(hosts[0].Services) != 1 {
		t.Fatalf("expected 1 service, got %v", hosts[0].Services)
	}
}

func TestGetHostByID_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(1), "web-01", "192.168.1.1", "web", nil, now),
	)
	h, err := svc.GetHostByID(1)
	if err != nil || h.ID != 1 {
		t.Fatalf("expected h.ID=1, got %v/%v", h, err)
	}
}

func TestGetHostByID_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnError(errors.New("not found"))
	_, err := svc.GetHostByID(999)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostByID_WithDisplayName(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	dn := "My Host"
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(5), "web-01", "10.0.0.1", "web", dn, now),
	)
	h, err := svc.GetHostByID(5)
	if err != nil || h.DisplayName == nil || *h.DisplayName != dn {
		t.Fatalf("expected display_name=%q, got %v/%v", dn, h, err)
	}
}

func TestGetHostDetail_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(1), int64(2), "web-01", "10.0.0.1", "web", nil, now),
	)
	mock.ExpectQuery("FROM services s").WillReturnRows(sqlmock.NewRows(hostStatusCols()))
	h, err := svc.GetHostDetail(1, 2)
	if err != nil || h.ID != 1 {
		t.Fatalf("expected h.ID=1, got %v/%v", h, err)
	}
}

func TestGetHostDetail_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnError(errors.New("not found"))
	_, err := svc.GetHostDetail(999, 1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostDetail_WithDisplayName(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	dn := "Display"
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(3), int64(1), "h", "10.0.0.1", "web", dn, now),
	)
	mock.ExpectQuery("FROM services s").WillReturnRows(sqlmock.NewRows(hostStatusCols()))
	h, err := svc.GetHostDetail(3, 1)
	if err != nil || h.DisplayName == nil || *h.DisplayName != dn {
		t.Fatalf("expected display_name=%q, got %v/%v", dn, h, err)
	}
}

func TestGetHostByName_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(1), int64(2), "web-01", "10.0.0.1", "web", nil, now),
	)
	h, err := svc.GetHostByName("web-01", 2)
	if err != nil || h.Name != "web-01" {
		t.Fatalf("expected web-01, got %v/%v", h, err)
	}
}

func TestGetHostByName_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "name", "host", "service", "display_name", "created_at"}))
	_, err := svc.GetHostByName("nonexistent", 1)
	if err == nil {
		t.Fatal("expected error for not found")
	}
}

func TestGetHostByName_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnError(errors.New("connection error"))
	_, err := svc.GetHostByName("web-01", 1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostByName_WithDisplayName(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	dn := "Display Name"
	mock.ExpectQuery("FROM hosts WHERE name").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(7), int64(1), "host7", "10.0.0.7", "web", dn, now),
	)
	h, _ := svc.GetHostByName("host7", 1)
	if h.DisplayName == nil || *h.DisplayName != dn {
		t.Fatalf("expected display_name=%q, got %v", dn, h.DisplayName)
	}
}

func TestUpdateHost_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	// GetHostByID (first call inside UpdateHost)
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(1), "web-01", "10.0.0.1", "web", nil, now),
	)
	// UPDATE hosts
	mock.ExpectQuery("UPDATE hosts").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	dn := "New Display"
	h, err := svc.UpdateHost(1, models.Host{DisplayName: &dn})
	if err != nil || h == nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestUpdateHost_HostNotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnError(errors.New("not found"))
	_, err := svc.UpdateHost(999, models.Host{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateHost_UpdateDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(1), "web-01", "10.0.0.1", "web", nil, now),
	)
	mock.ExpectQuery("UPDATE hosts").WillReturnError(errors.New("update error"))
	_, err := svc.UpdateHost(1, models.Host{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateHost_UpdateNotFoundError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	mock.ExpectQuery("FROM hosts WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host", "service", "display_name", "created_at"}).
			AddRow(int64(1), "web-01", "10.0.0.1", "web", nil, now),
	)
	// UPDATE returns no rows → ErrNoRows
	mock.ExpectQuery("UPDATE hosts").WillReturnRows(sqlmock.NewRows([]string{"created_at"}))
	_, err := svc.UpdateHost(1, models.Host{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteHost_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("COUNT\\(\\*\\) FROM services WHERE host_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM hosts").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.DeleteHost(1); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// Deleting a host that still has services must be refused: its checks would be
// silently orphaned (services.host_id is ON DELETE SET NULL, not CASCADE).
func TestDeleteHost_RefusedWhileServicesAttached(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("COUNT\\(\\*\\) FROM services WHERE host_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	if err := svc.DeleteHost(1); err != ErrHostHasServices {
		t.Fatalf("expected ErrHostHasServices, got %v", err)
	}
}

func TestDeleteHost_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("COUNT\\(\\*\\) FROM services WHERE host_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM hosts").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.DeleteHost(999); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestDeleteHost_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("COUNT\\(\\*\\) FROM services WHERE host_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM hosts").WillReturnError(errors.New("db error"))
	if err := svc.DeleteHost(1); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostServices_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	svcCols := []string{"id", "host_id", "name", "display_name", "type", "host", "path", "credentials", "service", "service_interval", "max_attempts", "failure_threshold", "token", "created_at"}
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(sqlmock.NewRows(svcCols))
	svcs, err := svc.GetHostServices(1)
	if err != nil || len(svcs) != 0 {
		t.Fatalf("expected empty, got %v/%v", svcs, err)
	}
}

func TestGetHostServices_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnError(errors.New("db error"))
	_, err := svc.GetHostServices(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostServices_OneService_WithResults(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	hostID := int64(1)
	svcCols := []string{"id", "host_id", "name", "display_name", "type", "host", "path", "credentials", "service", "service_interval", "max_attempts", "failure_threshold", "token", "created_at"}
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(
		sqlmock.NewRows(svcCols).AddRow(int64(10), hostID, "http-check", nil, "http", "http://example.com", nil, nil, "web", 60, 3, nil, nil, now),
	)
	// enrichHostServicesWithResults: service_results query for service 10
	resultCols := []string{"id", "service_id", "status", "latency", "message", "timestamp", "metric_type", "metric_value", "metadata"}
	latency := float64(120.5)
	mock.ExpectQuery("FROM service_results WHERE service_id").WillReturnRows(
		sqlmock.NewRows(resultCols).AddRow(int64(1), int64(10), "success", latency, nil, now, nil, nil, nil),
	)
	svcs, err := svc.GetHostServices(1)
	if err != nil || len(svcs) != 1 {
		t.Fatalf("expected 1 service, got %v/%v", svcs, err)
	}
	if len(svcs[0].Results) != 1 {
		t.Fatalf("expected 1 result, got %v", svcs[0].Results)
	}
}

func TestGetHostServices_ServiceWithToken(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	hostID := int64(1)
	token := "mytoken"
	svcCols := []string{"id", "host_id", "name", "display_name", "type", "host", "path", "credentials", "service", "service_interval", "max_attempts", "failure_threshold", "token", "created_at"}
	mock.ExpectQuery("FROM services WHERE host_id").WillReturnRows(
		sqlmock.NewRows(svcCols).AddRow(int64(10), hostID, "err-check", nil, "error_service_go", "n/a", nil, nil, "web", 60, 3, nil, token, now),
	)
	resultCols := []string{"id", "service_id", "status", "latency", "message", "timestamp", "metric_type", "metric_value", "metadata"}
	mock.ExpectQuery("FROM service_results WHERE service_id").WillReturnRows(sqlmock.NewRows(resultCols))
	svcs, err := svc.GetHostServices(1)
	if err != nil || len(svcs) != 1 || svcs[0].Service.Token == nil || *svcs[0].Service.Token != token {
		t.Fatalf("expected token=%q, got %v/%v", token, svcs, err)
	}
}

func TestGetHostsWithStats_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM hosts h").WillReturnError(errors.New("db error"))
	_, err := svc.GetHostsWithStats(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostsWithStats_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	statsHostCols := []string{"id", "organization_id", "name", "host", "service", "display_name", "created_at", "service_count", "healthy_count", "failing_count", "warning_count", "critical_count"}
	mock.ExpectQuery("FROM hosts h").WillReturnRows(sqlmock.NewRows(statsHostCols))
	hosts, err := svc.GetHostsWithStats(1)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("expected empty, got %v/%v", hosts, err)
	}
}

func TestGetHostsWithStats_OneHost(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	statsHostCols := []string{"id", "organization_id", "name", "host", "service", "display_name", "host_group_id", "created_at", "service_count", "healthy_count", "failing_count", "warning_count", "critical_count"}
	mock.ExpectQuery("FROM hosts h").WillReturnRows(
		sqlmock.NewRows(statsHostCols).AddRow(int64(1), int64(2), "web-01", "10.0.0.1", "web", nil, nil, now, 3, 2, 1, 0, 0),
	)
	// hostStatuses
	mock.ExpectQuery("FROM services s").WillReturnRows(
		sqlmock.NewRows(hostStatusCols()).AddRow(int64(1), "failure", now.Add(-2*time.Minute), "http", nil, nil, nil),
	)
	hosts, err := svc.GetHostsWithStats(1)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %v/%v", hosts, err)
	}
	if hosts[0].Status == nil || *hosts[0].Status != "failure" {
		t.Fatalf("expected status=failure, got %v", hosts[0].Status)
	}
}

func TestGetHostsWithStats_OneHost_WithDisplayName(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	dn := "Display"
	statsHostCols := []string{"id", "organization_id", "name", "host", "service", "display_name", "host_group_id", "created_at", "service_count", "healthy_count", "failing_count", "warning_count", "critical_count"}
	mock.ExpectQuery("FROM hosts h").WillReturnRows(
		sqlmock.NewRows(statsHostCols).AddRow(int64(1), int64(2), "web-01", "10.0.0.1", "web", dn, nil, now, 0, 0, 0, 0, 0),
	)
	mock.ExpectQuery("FROM services s").WillReturnRows(sqlmock.NewRows(hostStatusCols()))
	hosts, err := svc.GetHostsWithStats(1)
	if err != nil || len(hosts) != 1 || hosts[0].DisplayName == nil || *hosts[0].DisplayName != dn {
		t.Fatalf("expected display_name=%q, got %v/%v", dn, hosts, err)
	}
}

func TestGetHostsWithStatsPaged_OneHost(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now()
	cols := []string{"id", "organization_id", "name", "host", "service", "display_name", "host_group_id", "created_at", "service_count", "healthy_count", "failing_count", "warning_count", "critical_count"}
	// Paged path derives status from counts, so no host status query at all.
	mock.ExpectQuery("FROM host_rollup").WillReturnRows(
		sqlmock.NewRows(cols).AddRow(int64(1), int64(2), "web-01", "10.0.0.1", "web", nil, nil, now, 3, 2, 1, 0, 0))
	hosts, err := svc.GetHostsWithStatsPaged(1, "web", "failing", 50, 0)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %v/%v", hosts, err)
	}
	if hosts[0].FailingCount != 1 || hosts[0].ServiceCount != 3 {
		t.Fatalf("unexpected counts: %+v", hosts[0])
	}
}

func TestCountHostsFiltered(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(12))
	total, err := svc.CountHostsFiltered(1, "", "healthy")
	if err != nil || total != 12 {
		t.Fatalf("expected 12, got %d/%v", total, err)
	}
}

func TestHostStats(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM host_rollup").WillReturnRows(
		sqlmock.NewRows([]string{"total", "healthy", "failing", "warning", "unknown", "total_services"}).AddRow(10, 6, 2, 1, 1, 25))
	stats, err := svc.HostStats(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Total != 10 || stats.Healthy != 6 || stats.Failing != 2 || stats.Warning != 1 || stats.Unknown != 1 || stats.TotalServices != 25 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// TestHostServiceStatus pins the freshness rule the host badge depends on: past
// 10 minutes (5 for an agent service) a result stops counting, so a host whose
// services went silent reads as "unknown" instead of freezing its last verdict.
// It also pins that agent metrics are judged against the CURRENT thresholds, so
// a threshold edited after the last sample takes effect immediately.
func TestHostServiceStatus(t *testing.T) {
	now := time.Now()
	fresh := sql.NullTime{Time: now.Add(-1 * time.Minute), Valid: true}

	cases := []struct {
		name        string
		status      sql.NullString
		resultTime  sql.NullTime
		serviceType string
		metricValue sql.NullFloat64
		warn, crit  sql.NullFloat64
		want        string
	}{
		{name: "service without any result does not count", serviceType: "http", want: ""},
		{name: "failure within 10 minutes counts", status: nullStr("failure"), resultTime: fresh, serviceType: "http", want: "failure"},
		{name: "failure older than 10 minutes stops counting", status: nullStr("failure"),
			resultTime: sql.NullTime{Time: now.Add(-11 * time.Minute), Valid: true}, serviceType: "http", want: ""},
		{name: "agent result older than 5 minutes stops counting", resultTime: sql.NullTime{Time: now.Add(-6 * time.Minute), Valid: true},
			serviceType: "agent_cpu", metricValue: nullFloat(90), crit: nullFloat(80), want: ""},
		{name: "agent result within 5 minutes counts", resultTime: sql.NullTime{Time: now.Add(-4 * time.Minute), Valid: true},
			serviceType: "agent_cpu", metricValue: nullFloat(90), crit: nullFloat(80), want: "failure"},
		{name: "critical is reported as failure", status: nullStr("critical"), resultTime: fresh, serviceType: "http", want: "failure"},
		{name: "warning stays warning", status: nullStr("warning"), resultTime: fresh, serviceType: "http", want: "warning"},
		{name: "success stays success", status: nullStr("success"), resultTime: fresh, serviceType: "http", want: "success"},
		{name: "agent metric above critical threshold", resultTime: fresh, serviceType: "agent_ram",
			metricValue: nullFloat(95), warn: nullFloat(70), crit: nullFloat(90), want: "failure"},
		{name: "agent metric above warning threshold only", resultTime: fresh, serviceType: "agent_ram",
			metricValue: nullFloat(75), warn: nullFloat(70), crit: nullFloat(90), want: "warning"},
		{name: "agent metric below both thresholds", resultTime: fresh, serviceType: "agent_disk",
			metricValue: nullFloat(50), warn: nullFloat(70), crit: nullFloat(90), want: "success"},
		{name: "agent without metric value falls back to the stored status", status: nullStr("failure"),
			resultTime: fresh, serviceType: "agent_cpu", crit: nullFloat(80), want: "failure"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hostServiceStatus(c.status, c.resultTime, nullStr(c.serviceType), c.metricValue, c.warn, c.crit, now)
			if got != c.want {
				t.Fatalf("hostServiceStatus = %q, want %q", got, c.want)
			}
		})
	}
}

// TestHostStatuses_WorstServiceWins pins the rollup: one failing service is
// enough to mark the host as failing, and a warning only shows when nothing is
// failing.
func TestHostStatuses_WorstServiceWins(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now().Add(-1 * time.Minute)
	mock.ExpectQuery("FROM services s").WillReturnRows(
		// The worst status comes first on purpose: with it last, a plain
		// "last row read wins" would pass and the rollup would not be pinned.
		sqlmock.NewRows(hostStatusCols()).
			AddRow(int64(1), "failure", now, "http", nil, nil, nil).
			AddRow(int64(1), "success", now, "http", nil, nil, nil).
			AddRow(int64(2), "warning", now, "http", nil, nil, nil).
			AddRow(int64(2), "success", now, "http", nil, nil, nil),
	)
	statuses := svc.hostStatuses([]int64{1, 2})
	if statuses[1] != "failure" {
		t.Fatalf("expected host 1 failure, got %q", statuses[1])
	}
	if statuses[2] != "warning" {
		t.Fatalf("expected host 2 warning, got %q", statuses[2])
	}
}

// TestHostStatuses_StaleAndServicelessHostsAreUnknown pins that a host is only
// declared healthy on evidence: no service, or nothing but stale results, both
// read as "unknown" rather than "success".
func TestHostStatuses_StaleAndServicelessHostsAreUnknown(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	stale := time.Now().Add(-15 * time.Minute)
	mock.ExpectQuery("FROM services s").WillReturnRows(
		sqlmock.NewRows(hostStatusCols()).
			AddRow(int64(1), "success", stale, "http", nil, nil, nil),
	)
	// Host 2 has no service at all, so the query returns no row for it.
	statuses := svc.hostStatuses([]int64{1, 2})
	if statuses[1] != "unknown" || statuses[2] != "unknown" {
		t.Fatalf("expected unknown for both, got %q/%q", statuses[1], statuses[2])
	}
}

func TestHostStatuses_NoHosts_SkipsQuery(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	if len(svc.hostStatuses(nil)) != 0 {
		t.Fatal("expected empty statuses")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected query: %v", err)
	}
}

func TestHostStatuses_QueryError_ReturnsUnknown(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	mock.ExpectQuery("FROM services s").WillReturnError(errors.New("db error"))
	statuses := svc.hostStatuses([]int64{7})
	if statuses[7] != "unknown" {
		t.Fatalf("expected unknown, got %q", statuses[7])
	}
}

// TestHostStatuses_ScanError_ReturnsUnknown pins the same lie towards the green
// as the interrupted iteration: the "success" row of a host is read, its
// "failure" row fails to scan. Skipping the broken row would show that host as
// healthy, so a scan error falls back to unknown too.
func TestHostStatuses_ScanError_ReturnsUnknown(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now().Add(-1 * time.Minute)
	mock.ExpectQuery("FROM services s").WillReturnRows(
		sqlmock.NewRows(hostStatusCols()).
			AddRow(int64(1), "success", now, "http", nil, nil, nil).
			AddRow(int64(1), "failure", now, "http", "not-a-float", nil, nil),
	)
	statuses := svc.hostStatuses([]int64{1})
	if statuses[1] != "unknown" {
		t.Fatalf("expected unknown after scan error, got %q", statuses[1])
	}
}

// TestHostStatuses_IterationInterrupted_ReturnsUnknown pins the one case where
// a partial read could lie towards the green: the "success" row of a host is
// read, its "failure" row never is. Reporting the partial map would show that
// host as healthy, so an interrupted iteration falls back to unknown.
func TestHostStatuses_IterationInterrupted_ReturnsUnknown(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostService(db)
	now := time.Now().Add(-1 * time.Minute)
	mock.ExpectQuery("FROM services s").WillReturnRows(
		sqlmock.NewRows(hostStatusCols()).
			AddRow(int64(1), "success", now, "http", nil, nil, nil).
			AddRow(int64(1), "failure", now, "http", nil, nil, nil).
			RowError(1, errors.New("connection lost")),
	)
	statuses := svc.hostStatuses([]int64{1})
	if statuses[1] != "unknown" {
		t.Fatalf("expected unknown after interrupted iteration, got %q", statuses[1])
	}
}

// TestGetHostsWithStats_QueryCountIsIndependentOfHostCount is the regression
// test for the reason this endpoint took two seconds: the status used to be
// recomputed host by host, so the list cost 2 queries per host on top of the
// list query. It must now cost the same two queries for 4 hosts as for 1.
// sqlmock is strict: any extra query is refused and every status collapses to
// "unknown", which the assertions below catch.
func TestGetHostsWithStats_QueryCountIsIndependentOfHostCount(t *testing.T) {
	statsHostCols := []string{"id", "organization_id", "name", "host", "service", "display_name", "host_group_id", "created_at", "service_count", "healthy_count", "failing_count", "warning_count", "critical_count"}

	for _, hostCount := range []int{1, 4} {
		t.Run(fmt.Sprintf("%d hosts", hostCount), func(t *testing.T) {
			db, mock := newDB(t)
			svc := NewHostService(db)
			now := time.Now()

			listRows := sqlmock.NewRows(statsHostCols)
			statusRows := sqlmock.NewRows(hostStatusCols())
			for i := 1; i <= hostCount; i++ {
				listRows.AddRow(int64(i), int64(2), fmt.Sprintf("web-%02d", i), "10.0.0.1", "web", nil, nil, now, 1, 1, 0, 0, 0)
				statusRows.AddRow(int64(i), "failure", now.Add(-1*time.Minute), "http", nil, nil, nil)
			}
			// Exactly two expectations, whatever the number of hosts.
			mock.ExpectQuery("FROM hosts h").WillReturnRows(listRows)
			mock.ExpectQuery("FROM services s").WillReturnRows(statusRows)

			hosts, err := svc.GetHostsWithStats(1)
			if err != nil || len(hosts) != hostCount {
				t.Fatalf("expected %d hosts, got %v/%v", hostCount, hosts, err)
			}
			for _, h := range hosts {
				if h.Status == nil || *h.Status != "failure" {
					t.Fatalf("host %d: expected status=failure, got %v", h.ID, h.Status)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unexpected queries: %v", err)
			}
		})
	}
}
