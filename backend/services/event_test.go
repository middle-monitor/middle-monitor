package services

import (
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewEventService(t *testing.T) {
	db, _ := newDB(t)
	if NewEventService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestCreateEvent_DefaultTimestampAndOrg(t *testing.T) {
	db, mock := newDB(t)
	svc := NewEventService(db)
	mock.ExpectQuery("INSERT INTO events").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	e, err := svc.CreateEvent(models.Event{})
	if err != nil || e.ID != 7 {
		t.Fatalf("expected created event: %v/%v", e, err)
	}
	if e.OrganizationID != 1 {
		t.Fatalf("expected default orgID 1, got %d", e.OrganizationID)
	}
}

func TestCreateEvent_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewEventService(db)
	mock.ExpectQuery("INSERT INTO events").WillReturnError(sql.ErrConnDone)
	_, err := svc.CreateEvent(models.Event{OrganizationID: 1})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetEvents_Delegates(t *testing.T) {
	db, mock := newDB(t)
	svc := NewEventService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "type", "service", "message", "metadata", "timestamp",
	}))
	events, err := svc.GetEvents("", 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("expected empty events: %v/%v", events, err)
	}
}

func TestGetEventsForOrg_AllFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewEventService(db)
	now := time.Now()
	meta := `{"key":"val"}`
	mock.ExpectQuery("SELECT id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "type", "service", "message", "metadata", "timestamp",
		}).AddRow(1, sql.NullInt64{Int64: 1, Valid: true}, "deploy", "api", "deployed", &meta, now))
	events, err := svc.GetEventsForOrg(1, "api", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("expected 1 event: %v/%v", events, err)
	}
	if events[0].Metadata == nil || *events[0].Metadata != meta {
		t.Fatal("expected metadata set")
	}
}

func TestGetEventsForOrgFiltered_WithFromTo(t *testing.T) {
	db, mock := newDB(t)
	svc := NewEventService(db)
	now := time.Now()
	from := now.Add(-time.Hour)
	to := now
	mock.ExpectQuery("SELECT id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "type", "service", "message", "metadata", "timestamp",
		}).AddRow(1, sql.NullInt64{Valid: false}, "deploy", "", "msg", nil, now))
	events, err := svc.GetEventsForOrgFiltered(0, "", 5, from, to)
	if err != nil || len(events) != 1 {
		t.Fatalf("expected 1 event: %v/%v", events, err)
	}
}

func TestGetEventsForOrgFiltered_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewEventService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetEventsForOrgFiltered(1, "", 10, time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected DB error")
	}
}
