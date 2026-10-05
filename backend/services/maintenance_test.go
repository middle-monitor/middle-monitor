package services

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNullableID_Nil(t *testing.T) {
	if nullableID(nil) != int64(-1) {
		t.Fatal("nil pointer should return -1")
	}
}

func TestNullableID_Value(t *testing.T) {
	v := int64(42)
	if nullableID(&v) != int64(42) {
		t.Fatal("want 42")
	}
}

func TestNewMaintenanceService(t *testing.T) {
	db, _ := newDB(t)
	if NewMaintenanceService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestMaintenanceService_List_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT m.id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "name", "target_type", "target_id",
			"starts_at", "ends_at", "created_by", "created_at", "target_name",
		}))
	windows, err := svc.List(1)
	if err != nil || len(windows) != 0 {
		t.Fatalf("expected empty list, got %v / %v", windows, err)
	}
}

func TestMaintenanceService_List_WithResult(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	createdBy := int64(1)
	mock.ExpectQuery("SELECT m.id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "name", "target_type", "target_id",
			"starts_at", "ends_at", "created_by", "created_at", "target_name",
		}).AddRow(1, 1, "Deploy freeze", "service", 2, now, now.Add(time.Hour), createdBy, now, "api"))
	windows, err := svc.List(1)
	if err != nil || len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d / %v", len(windows), err)
	}
	if windows[0].CreatedBy == nil || *windows[0].CreatedBy != 1 {
		t.Fatal("expected createdBy set")
	}
}

func TestMaintenanceService_List_NullCreatedBy(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT m.id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "name", "target_type", "target_id",
			"starts_at", "ends_at", "created_by", "created_at", "target_name",
		}).AddRow(1, 1, "freeze", "host", 3, now, now.Add(time.Hour), nil, now, "srv1"))
	windows, err := svc.List(1)
	if err != nil || len(windows) != 1 || windows[0].CreatedBy != nil {
		t.Fatalf("expected nil createdBy, got %v / %v", windows, err)
	}
}

func TestMaintenanceService_List_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT m.id").WillReturnError(sql.ErrConnDone)
	_, err := svc.List(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestMaintenanceService_Create_InvalidTargetType(t *testing.T) {
	db, _ := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	_, err := svc.Create(models.MaintenanceWindow{
		TargetType: "invalid", Name: "n",
		StartsAt: now, EndsAt: now.Add(time.Hour),
	})
	if err == nil || !strings.Contains(err.Error(), "target_type") {
		t.Fatalf("expected target_type error, got %v", err)
	}
}

func TestMaintenanceService_Create_EmptyName(t *testing.T) {
	db, _ := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	_, err := svc.Create(models.MaintenanceWindow{
		TargetType: "service", Name: "  ",
		StartsAt: now, EndsAt: now.Add(time.Hour),
	})
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestMaintenanceService_Create_EndsBeforeStarts(t *testing.T) {
	db, _ := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	_, err := svc.Create(models.MaintenanceWindow{
		TargetType: "service", Name: "n",
		StartsAt: now, EndsAt: now.Add(-time.Hour),
	})
	if err == nil || !strings.Contains(err.Error(), "ends_at") {
		t.Fatalf("expected ends_at error, got %v", err)
	}
}

func TestMaintenanceService_Create_TargetNotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	_, err := svc.Create(models.MaintenanceWindow{
		TargetType: "service", Name: "n", OrganizationID: 1, TargetID: 2,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestMaintenanceService_Create_HostType_TargetFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("INSERT INTO maintenance_windows").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, now))
	w, err := svc.Create(models.MaintenanceWindow{
		TargetType: "host", Name: "n", OrganizationID: 1, TargetID: 2,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	})
	if err != nil || w.ID != 1 {
		t.Fatalf("expected created window, got %v / %v", w, err)
	}
}

func TestMaintenanceService_Create_DBCheckError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT EXISTS").WillReturnError(sql.ErrConnDone)
	_, err := svc.Create(models.MaintenanceWindow{
		TargetType: "service", Name: "n", OrganizationID: 1, TargetID: 2,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestMaintenanceService_Create_InsertError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("INSERT INTO maintenance_windows").WillReturnError(sql.ErrConnDone)
	_, err := svc.Create(models.MaintenanceWindow{
		TargetType: "service", Name: "n", OrganizationID: 1, TargetID: 2,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	})
	if err == nil {
		t.Fatal("expected insert error")
	}
}

func TestMaintenanceService_Delete_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectExec("DELETE FROM maintenance_windows").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.Delete(1, 1); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestMaintenanceService_Delete_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectExec("DELETE FROM maintenance_windows").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.Delete(1, 1); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestMaintenanceService_Delete_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectExec("DELETE FROM maintenance_windows").WillReturnError(sql.ErrConnDone)
	if err := svc.Delete(1, 1); err == nil {
		t.Fatal("expected DB error")
	}
}

func TestIsTargetUnderMaintenance_Yes(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	svcID := int64(1)
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	under, err := svc.IsTargetUnderMaintenance(1, &svcID, nil)
	if err != nil || !under {
		t.Fatalf("expected true, got %v / %v", under, err)
	}
}

func TestIsTargetUnderMaintenance_No(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	under, err := svc.IsTargetUnderMaintenance(1, nil, nil)
	if err != nil || under {
		t.Fatalf("expected false, got %v / %v", under, err)
	}
}

func TestIsTargetUnderMaintenance_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(sql.ErrConnDone)
	_, err := svc.IsTargetUnderMaintenance(1, nil, nil)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetOrgAlertSettings_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").
		WillReturnRows(sqlmock.NewRows([]string{"warning", "critical"}).AddRow(true, false))
	w, c, err := svc.GetOrgAlertSettings(1)
	if err != nil || !w || c {
		t.Fatalf("want (true,false,nil), got (%v,%v,%v)", w, c, err)
	}
}

func TestGetOrgAlertSettings_NotFound_Defaults(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").WillReturnError(sql.ErrNoRows)
	w, c, err := svc.GetOrgAlertSettings(1)
	if err != nil || !w || !c {
		t.Fatalf("expected defaults (true,true,nil), got (%v,%v,%v)", w, c, err)
	}
}

func TestIsSeverityMuted_Warning(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").
		WillReturnRows(sqlmock.NewRows([]string{"warning", "critical"}).AddRow(false, true))
	muted, err := svc.IsSeverityMuted(1, "warning")
	if err != nil || !muted {
		t.Fatalf("expected muted=true, got %v/%v", muted, err)
	}
}

func TestIsSeverityMuted_Critical_NotMuted(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").
		WillReturnRows(sqlmock.NewRows([]string{"warning", "critical"}).AddRow(true, true))
	muted, err := svc.IsSeverityMuted(1, "critical")
	if err != nil || muted {
		t.Fatalf("expected not muted, got %v/%v", muted, err)
	}
}

func TestIsSeverityMuted_UnknownSeverity(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").
		WillReturnRows(sqlmock.NewRows([]string{"warning", "critical"}).AddRow(true, true))
	muted, err := svc.IsSeverityMuted(1, "info")
	if err != nil || muted {
		t.Fatalf("expected not muted for unknown severity, got %v/%v", muted, err)
	}
}

func TestIsSeverityMuted_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").WillReturnError(sql.ErrConnDone)
	_, err := svc.IsSeverityMuted(1, "warning")
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestSuppressAlert_SeverityMuted(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").
		WillReturnRows(sqlmock.NewRows([]string{"warning", "critical"}).AddRow(false, true))
	suppress, reason := svc.SuppressAlert(1, "warning", nil, nil)
	if !suppress || reason == "" {
		t.Fatalf("expected suppressed, got %v/%q", suppress, reason)
	}
}

func TestSuppressAlert_UnderMaintenance(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	// Not muted
	mock.ExpectQuery("SELECT alert_warning_enabled").
		WillReturnRows(sqlmock.NewRows([]string{"warning", "critical"}).AddRow(true, true))
	// Under maintenance
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	svcID := int64(1)
	suppress, reason := svc.SuppressAlert(1, "critical", &svcID, nil)
	if !suppress || reason == "" {
		t.Fatalf("expected suppressed, got %v/%q", suppress, reason)
	}
}

func TestSuppressAlert_NotSuppressed(t *testing.T) {
	db, mock := newDB(t)
	svc := NewMaintenanceService(db)
	mock.ExpectQuery("SELECT alert_warning_enabled").
		WillReturnRows(sqlmock.NewRows([]string{"warning", "critical"}).AddRow(true, true))
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	suppress, _ := svc.SuppressAlert(1, "critical", nil, nil)
	if suppress {
		t.Fatal("expected not suppressed")
	}
}
