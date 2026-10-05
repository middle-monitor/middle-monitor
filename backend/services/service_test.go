package services

import (
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewServiceService(t *testing.T) {
	db, _ := newDB(t)
	if NewServiceService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestCreateService_PlanLimitError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	// Free plan + error_service already at limit
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("FROM services WHERE organization_id").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	_, err := svc.CreateService(models.Service{OrganizationID: 1, Type: "error_service_go"})
	if err == nil {
		t.Fatal("expected plan limit error")
	}
}

// A host owned by another org must be refused: the host page filters by org and
// would never list the check, while the host's service count would still include
// it — the service would look attached from one page and missing from the other.
func TestCreateService_HostFromAnotherOrg_Refused(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	hostID := int64(4)
	mock.ExpectQuery("SELECT organization_id FROM hosts").WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(int64(3)))
	_, err := svc.CreateService(models.Service{OrganizationID: 1, HostID: &hostID, Type: "http", Name: "check", Host: "example.com"})
	if !errors.Is(err, ErrHostOrgMismatch) {
		t.Fatalf("expected ErrHostOrgMismatch, got %v", err)
	}
}

func TestCreateService_Success_HTTPService(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	now := time.Now()
	// Plan check passes (monitored-services count under cap)
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	// INSERT service
	mock.ExpectQuery("INSERT INTO services").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(5), now))
	created, err := svc.CreateService(models.Service{OrganizationID: 1, Type: "http", Name: "check", Host: "http://example.com"})
	if err != nil || created.ID != 5 {
		t.Fatalf("expected id=5, got %v/%v", created, err)
	}
}

func TestCreateService_Success_ErrorService_GeneratesToken(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	now := time.Now()
	// Plan check: free plan, 0 error services
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("FROM services WHERE organization_id").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("INSERT INTO services").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(7), now))
	created, err := svc.CreateService(models.Service{OrganizationID: 1, Type: "error_service_go", Name: "go-errors"})
	if err != nil || created.ID != 7 {
		t.Fatalf("expected id=7, got %v/%v", created, err)
	}
	if created.Token == nil {
		t.Fatal("expected token for error_service_go")
	}
}

func TestCreateService_DefaultOrgID(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	now := time.Now()
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("INSERT INTO services").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), now))
	created, err := svc.CreateService(models.Service{OrganizationID: 0, Type: "http", Name: "check"})
	if err != nil || created.OrganizationID != 1 {
		t.Fatalf("expected orgID=1, got %v/%v", created, err)
	}
}

func TestCreateService_InsertError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	mock.ExpectQuery("FROM organizations").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery("INSERT INTO services").WillReturnError(errors.New("insert failed"))
	_, err := svc.CreateService(models.Service{OrganizationID: 1, Type: "http"})
	if err == nil {
		t.Fatal("expected error")
	}
}
