package services

import (
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestProfileService_Create_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	now := time.Now()
	dur := 30
	mb := float64(128)
	mock.ExpectQuery("INSERT INTO profile_captures").
		WillReturnRows(sqlmock.NewRows(profileCols).
			AddRow(int64(1), int64(2), "api", "cpu", &dur, 512, &mb, now))
	p, err := svc.Create(2, "api", "cpu", &dur, &mb, []byte("data"))
	if err != nil || p.ID != 1 {
		t.Fatalf("expected p.ID=1, got %v/%v", p, err)
	}
}

func TestProfileService_Create_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectQuery("INSERT INTO profile_captures").
		WillReturnError(errors.New("insert failed"))
	_, err := svc.Create(2, "api", "cpu", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProfileService_List_NoFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	now := time.Now()
	mock.ExpectQuery("FROM profile_captures").
		WillReturnRows(sqlmock.NewRows(profileCols).
			AddRow(int64(1), int64(2), "api", "cpu", nil, 512, nil, now))
	list, err := svc.List(2, "", "", 0, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 result, got %v/%v", list, err)
	}
}

func TestProfileService_List_WithFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectQuery("FROM profile_captures").
		WillReturnRows(sqlmock.NewRows(profileCols))
	list, err := svc.List(2, "api", "heap", 10, 0)
	if err != nil || len(list) != 0 {
		t.Fatalf("expected empty, got %v/%v", list, err)
	}
}

func TestProfileService_List_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectQuery("FROM profile_captures").
		WillReturnError(errors.New("query error"))
	_, err := svc.List(2, "", "", 0, 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProfileService_CountProfiles(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	total, err := svc.CountProfiles(2, "api", "heap")
	if err != nil || total != 7 {
		t.Fatalf("expected 7, got %d/%v", total, err)
	}
}

func TestProfileService_CountProfiles_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectQuery("SELECT COUNT").
		WillReturnError(errors.New("query error"))
	if _, err := svc.CountProfiles(2, "", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestProfileService_GetByID_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	now := time.Now()
	cols := append(profileCols, "data")
	mock.ExpectQuery("FROM profile_captures").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(int64(5), int64(2), "api", "cpu", nil, 256, nil, now, []byte("pprof")))
	p, err := svc.GetByID(2, 5)
	if err != nil || p.ID != 5 {
		t.Fatalf("expected p.ID=5, got %v/%v", p, err)
	}
}

func TestProfileService_GetByID_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectQuery("FROM profile_captures").
		WillReturnRows(sqlmock.NewRows(append(profileCols, "data")))
	_, err := svc.GetByID(2, 999)
	if err == nil {
		t.Fatal("expected sql.ErrNoRows")
	}
}

func TestProfileService_Series_NoTimeBounds(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	now := time.Now()
	mock.ExpectQuery("FROM profile_captures").
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "memory_mb", "service"}).
			AddRow(now, float64(64), "api"))
	pts, err := svc.Series(2, "", nil, nil, 0)
	if err != nil || len(pts) != 1 {
		t.Fatalf("expected 1 point, got %v/%v", pts, err)
	}
}

func TestProfileService_Series_WithTimeBounds(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	from := time.Now().Add(-1 * time.Hour)
	to := time.Now()
	mock.ExpectQuery("FROM profile_captures").
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "memory_mb", "service"}))
	pts, err := svc.Series(2, "api", &from, &to, 100)
	if err != nil || len(pts) != 0 {
		t.Fatalf("expected empty, got %v/%v", pts, err)
	}
}

func TestProfileService_Series_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectQuery("FROM profile_captures").
		WillReturnError(errors.New("query error"))
	_, err := svc.Series(2, "", nil, nil, 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProfileService_DeleteOlderThan_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectExec("DELETE FROM profile_captures").
		WillReturnResult(sqlmock.NewResult(0, 3))
	n, err := svc.DeleteOlderThan(time.Now())
	if err != nil || n != 3 {
		t.Fatalf("expected 3 deleted, got %d/%v", n, err)
	}
}

func TestProfileService_DeleteOlderThan_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewProfileService(db)
	mock.ExpectExec("DELETE FROM profile_captures").
		WillReturnError(errors.New("delete error"))
	_, err := svc.DeleteOlderThan(time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}
