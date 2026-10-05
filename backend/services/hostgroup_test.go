package services

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// The default group is the safety net every host falls back to, so deleting it
// would orphan hosts — it must be refused.
func TestHostGroup_Delete_BlocksDefault(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostGroupService(db)
	mock.ExpectQuery("is_default FROM host_groups").
		WillReturnRows(sqlmock.NewRows([]string{"is_default"}).AddRow(true))

	err := svc.Delete(1, 1)
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("expected default-protection error, got %v", err)
	}
}

// Deleting a group that still has hosts must be refused: silently moving hosts
// to another group would change their correlation scope behind the user's back.
func TestHostGroup_Delete_RefusedWhileHostsAttached(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostGroupService(db)
	mock.ExpectQuery("is_default FROM host_groups").
		WillReturnRows(sqlmock.NewRows([]string{"is_default"}).AddRow(false))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM hosts WHERE host_group_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	if err := svc.Delete(1, 5); err != ErrGroupHasHosts {
		t.Fatalf("expected ErrGroupHasHosts, got %v", err)
	}
}

// An empty non-default group can be deleted.
func TestHostGroup_Delete_EmptyGroup(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostGroupService(db)
	mock.ExpectQuery("is_default FROM host_groups").
		WillReturnRows(sqlmock.NewRows([]string{"is_default"}).AddRow(false))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM hosts WHERE host_group_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM host_groups").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := svc.Delete(1, 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Assigning a host to a group that does not exist (or belongs to another org)
// must fail rather than silently corrupt the host's scope.
func TestHostGroup_AssignHost_RejectsUnknownGroup(t *testing.T) {
	db, mock := newDB(t)
	svc := NewHostGroupService(db)
	mock.ExpectQuery("FROM host_groups WHERE id").WillReturnError(sql.ErrNoRows)

	if err := svc.AssignHost(1, 10, 999); err == nil {
		t.Fatal("expected error for an unknown host group")
	}
}

// Without an application link there is no host/host-group scope, so correlation
// must NOT surface any neighbours — this is what prevents env-wide false
// positives (an unrelated service failing at the same time is not a signal).
func TestGetCorrelationForError_NoLink_NoNeighbours(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	corrErrCols := []string{"id", "name", "message", "file", "line", "timestamp", "service", "http_method", "http_url", "http_headers", "http_body", "fingerprint"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(corrErrCols).AddRow(int64(1), "E", "boom", "f.go", 1, now, "api", nil, nil, nil, nil, "fp1"),
	)
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectQuery("FROM application_links al").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	// No link → empty scope → neighbour and co-app queries are skipped entirely.
	mock.ExpectQuery("FROM application_links").WillReturnRows(sqlmock.NewRows([]string{"target_type", "target_id"}))
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(0), int64(0), int64(0), nil, nil),
	)

	result, err := svc.GetCorrelationForError(1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Services) != 0 || len(result.Apps) != 0 {
		t.Fatalf("expected no neighbours without a link, got services=%v apps=%v", result.Services, result.Apps)
	}
}
