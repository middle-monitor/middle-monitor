package services

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewLinkService(t *testing.T) {
	db, _ := newDB(t)
	if NewLinkService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestLinkService_Create_InvalidTargetType(t *testing.T) {
	db, _ := newDB(t)
	svc := NewLinkService(db)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "invalid", TargetID: 1})
	if err == nil || !strings.Contains(err.Error(), "target_type") {
		t.Fatalf("expected target_type error, got %v", err)
	}
}

func TestLinkService_Create_HostNotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 FROM hosts").WillReturnError(sql.ErrNoRows)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "host", TargetID: 9})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestLinkService_Create_HostDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 FROM hosts").WillReturnError(sql.ErrConnDone)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "host", TargetID: 1})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestLinkService_Create_ServiceNotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 FROM services").WillReturnError(sql.ErrNoRows)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "service", TargetID: 9})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestLinkService_Create_ServiceDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 FROM services").WillReturnError(sql.ErrConnDone)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "service", TargetID: 1})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestLinkService_Create_App_RequiresName(t *testing.T) {
	db, _ := newDB(t)
	svc := NewLinkService(db)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "app", AppServiceName: "checkout"})
	if err == nil || !strings.Contains(err.Error(), "target_app_name") {
		t.Fatalf("expected target_app_name error, got %v", err)
	}
}

func TestLinkService_Create_App_SelfLinkRejected(t *testing.T) {
	db, _ := newDB(t)
	svc := NewLinkService(db)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "app", AppServiceName: "checkout", TargetAppName: "checkout"})
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("expected self-link error, got %v", err)
	}
}

func TestLinkService_Create_App_UnknownApp(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 WHERE EXISTS").WillReturnError(sql.ErrNoRows)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "app", AppServiceName: "checkout", TargetAppName: "ghost-app"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestLinkService_Create_App_ByNameSuccess(t *testing.T) {
	// An app that has reported errors can be linked to even without an
	// error-service registration — the whole point of name-based targets.
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 WHERE EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("INSERT INTO application_links").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	link, err := svc.Create(1, models.ApplicationLink{TargetType: "app", AppServiceName: "checkout", TargetAppName: "payment", TargetID: 42})
	if err != nil || link.ID != 9 {
		t.Fatalf("expected created link: %v/%v", link, err)
	}
	if link.TargetID != 0 {
		t.Fatalf("expected target_id forced to 0 for app links, got %d", link.TargetID)
	}
}

func TestLinkService_Create_HostSuccess(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 FROM hosts").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("INSERT INTO application_links").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5))
	link, err := svc.Create(1, models.ApplicationLink{TargetType: "host", TargetID: 1, AppServiceName: "api"})
	if err != nil || link.ID != 5 {
		t.Fatalf("expected created link: %v/%v", link, err)
	}
}

func TestLinkService_Create_ServiceSuccess_InsertError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT 1 FROM services").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("INSERT INTO application_links").WillReturnError(sql.ErrConnDone)
	_, err := svc.Create(1, models.ApplicationLink{TargetType: "service", TargetID: 1})
	if err == nil {
		t.Fatal("expected insert error")
	}
}

func TestLinkService_ListForOrg_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "app_service_name", "target_type", "target_id",
	}))
	links, err := svc.ListForOrg(1)
	if err != nil || len(links) != 0 {
		t.Fatalf("expected empty: %v/%v", links, err)
	}
}

func TestLinkService_ListForOrg_WithHostAndServiceLinks(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "app_service_name", "target_type", "target_id", "target_app_name",
	}).AddRow(1, 1, "api", "host", 10, "").
		AddRow(2, 1, "api", "service", 20, ""))
	// Resolve host name
	mock.ExpectQuery("SELECT name FROM hosts").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("server1"))
	// Resolve service name
	mock.ExpectQuery("SELECT name FROM services").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("api-svc"))
	links, err := svc.ListForOrg(1)
	if err != nil || len(links) != 2 {
		t.Fatalf("expected 2 links: %v/%v", links, err)
	}
}

func TestLinkService_ListForOrg_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.ListForOrg(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestLinkService_GetLinksForCorrelation_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT target_type").WillReturnRows(sqlmock.NewRows([]string{"target_type", "target_id"}))
	hostIDs, svcIDs, err := svc.GetLinksForCorrelation(1, "api")
	if err != nil || len(hostIDs) != 0 || len(svcIDs) != 0 {
		t.Fatalf("expected empty: %v/%v/%v", hostIDs, svcIDs, err)
	}
}

func TestLinkService_GetLinksForCorrelation_WithLinks(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT target_type").WillReturnRows(sqlmock.NewRows([]string{"target_type", "target_id"}).
		AddRow("host", 10).
		AddRow("service", 20))
	hostIDs, svcIDs, err := svc.GetLinksForCorrelation(1, "api")
	if err != nil || len(hostIDs) != 1 || len(svcIDs) != 1 {
		t.Fatalf("expected 1 host + 1 service: %v/%v/%v", hostIDs, svcIDs, err)
	}
}

func TestLinkService_GetLinksForCorrelation_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectQuery("SELECT target_type").WillReturnError(sql.ErrConnDone)
	_, _, err := svc.GetLinksForCorrelation(1, "api")
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestLinkService_Delete_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectExec("DELETE FROM application_links").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.Delete(1, 1); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestLinkService_Delete_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectExec("DELETE FROM application_links").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.Delete(1, 1); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestLinkService_Delete_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewLinkService(db)
	mock.ExpectExec("DELETE FROM application_links").WillReturnError(sql.ErrConnDone)
	if err := svc.Delete(1, 1); err == nil {
		t.Fatal("expected DB error")
	}
}
