package services

import (
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewErrorService(t *testing.T) {
	db, _ := newDB(t)
	if NewErrorService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestCreateError_InsertError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("INSERT INTO application_errors").WillReturnError(errors.New("insert failed"))
	_, err := svc.CreateError(models.ApplicationError{
		OrganizationID: 1,
		Name:           "TestError",
		Message:        "msg",
		Fingerprint:    "abc",
		Timestamp:      time.Now(),
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateError_Success_DefaultsOrgAndTimestamp(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	// INSERT expectation
	mock.ExpectQuery("INSERT INTO application_errors").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
	// Goroutine: first query returns no rows (ErrNoRows → exits early)
	mock.ExpectQuery("error_service_").WillReturnRows(sqlmock.NewRows([]string{"id", "failure_threshold"}))
	appErr := models.ApplicationError{
		// OrganizationID=0 → defaults to 1
		// Timestamp.IsZero() → true → set to Now()
		// Fingerprint="" → computed
		Name:    "NullPtr",
		Message: "null pointer dereference",
	}
	result, err := svc.CreateError(appErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != 42 {
		t.Fatalf("expected ID=42, got %d", result.ID)
	}
	if result.OrganizationID != 1 {
		t.Fatalf("expected orgID=1, got %d", result.OrganizationID)
	}
	if result.Timestamp.IsZero() {
		t.Fatal("expected timestamp set")
	}
	if result.Fingerprint == "" {
		t.Fatal("expected fingerprint computed")
	}
	// Give goroutine time to run
	time.Sleep(50 * time.Millisecond)
}

func TestCreateError_GoroutineNotifiesWhenThresholdExceeded(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	threshold := float64(5)
	// INSERT
	mock.ExpectQuery("INSERT INTO application_errors").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	// Goroutine: finds error service with threshold
	mock.ExpectQuery("error_service_").WillReturnRows(
		sqlmock.NewRows([]string{"id", "failure_threshold"}).AddRow(int64(10), threshold),
	)
	// Goroutine: count errors in last minute (>= threshold)
	mock.ExpectQuery("COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(7)))
	// Goroutine: GetChannels → no channels (no notification sent)
	mock.ExpectQuery("FROM notification_channels").WillReturnRows(sqlmock.NewRows(channelColumns))
	appErr := models.ApplicationError{
		OrganizationID: 1,
		Name:           "FreqError",
		Message:        "happens a lot",
		Service:        "api",
		Fingerprint:    "fp1",
		Timestamp:      time.Now(),
	}
	_, err := svc.CreateError(appErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
}

func TestGetErrors_DelegatesToGetErrorsForOrg(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows(errorCols()))
	errs, err := svc.GetErrors("", "100")
	if err != nil || len(errs) != 0 {
		t.Fatalf("expected empty, got %v/%v", errs, err)
	}
}

func TestGetErrorsForOrg_WithFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	method := "POST"
	url := "http://api.example.com"
	headers := `{"content-type":"application/json"}`
	body := `{"key":"val"}`
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(errorCols()).AddRow(
			int64(1), int64(2), "DBError", "connection refused", "db.go", 42, now,
			"api", method, url, headers, body,
		),
	)
	errs, err := svc.GetErrorsForOrg(2, "api", "50")
	if err != nil || len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v/%v", errs, err)
	}
	if errs[0].HTTPMethod == nil || *errs[0].HTTPMethod != "POST" {
		t.Fatalf("expected HTTPMethod=POST, got %v", errs[0].HTTPMethod)
	}
	if errs[0].HTTPBody == nil || *errs[0].HTTPBody != body {
		t.Fatalf("expected HTTPBody set, got %v", errs[0].HTTPBody)
	}
}

func TestGetErrorsForOrg_DefaultLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	// limit="" → defaults to "100"
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows(errorCols()))
	_, err := svc.GetErrorsForOrg(1, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetErrorsForOrg_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db error"))
	_, err := svc.GetErrorsForOrg(1, "", "100")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetErrorsForOrg_ScanError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	// Return row with wrong type for 'line' field (not an int)
	badCols := []string{"id", "organization_id", "name", "message", "file", "line", "timestamp", "service", "http_method", "http_url", "http_headers", "http_body"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(badCols).AddRow(int64(1), int64(1), "E", "msg", "f.go", "not-an-int", time.Now(), "api", nil, nil, nil, nil),
	)
	_, err := svc.GetErrorsForOrg(1, "", "100")
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestGetErrorByID_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	mock.ExpectQuery("FROM application_errors WHERE id").WillReturnRows(
		sqlmock.NewRows(errorCols()).AddRow(int64(5), int64(1), "E", "msg", "f.go", 10, now, "api", nil, nil, nil, nil),
	)
	e, err := svc.GetErrorByID(5, 1)
	if err != nil || e == nil || e.ID != 5 {
		t.Fatalf("expected e.ID=5, got %v/%v", e, err)
	}
}

func TestGetErrorByID_NotFound_ReturnsNil(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("FROM application_errors WHERE id").WillReturnRows(sqlmock.NewRows(errorCols()))
	e, err := svc.GetErrorByID(999, 1)
	if err != nil || e != nil {
		t.Fatalf("expected nil,nil for not found, got %v/%v", e, err)
	}
}

func TestGetErrorByID_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("FROM application_errors WHERE id").WillReturnError(errors.New("db error"))
	_, err := svc.GetErrorByID(1, 1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetErrorByID_WithHTTPFields(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	method, url, headers, body := "GET", "http://e.com", "h", "b"
	mock.ExpectQuery("FROM application_errors WHERE id").WillReturnRows(
		sqlmock.NewRows(errorCols()).AddRow(int64(2), int64(1), "E", "msg", "f.go", 1, now, "api", method, url, headers, body),
	)
	e, err := svc.GetErrorByID(2, 1)
	if err != nil || e.HTTPMethod == nil || e.HTTPURL == nil || e.HTTPHeaders == nil || e.HTTPBody == nil {
		t.Fatalf("expected HTTP fields set, got %v/%v", e, err)
	}
}

func TestGetErrorsGroupedForOrg_DefaultWindow_HourBuckets(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(groupedErrorCols()).AddRow(int64(1), int64(1), "NullPtr", "msg", "f.go", 1, now, "api", nil, nil, nil),
	)
	groups, err := svc.GetErrorsGroupedForOrg(1, "", "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].EventCount != 1 {
		t.Fatalf("expected event_count=1, got %d", groups[0].EventCount)
	}
}

func TestGetErrorsGroupedForOrg_Window14d_DayBuckets(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(groupedErrorCols()).AddRow(int64(1), int64(1), "NullPtr", "msg", "f.go", 1, now, "api", nil, nil, nil),
	)
	groups, err := svc.GetErrorsGroupedForOrg(1, "", "", "14d", "", "")
	if err != nil || len(groups) != 1 {
		t.Fatalf("expected 1 group, got %v/%v", groups, err)
	}
}

func TestGetErrorsGroupedForOrg_ExplicitDateRange_HourBuckets(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	from := now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	to := now.UTC().Format(time.RFC3339)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(groupedErrorCols()).AddRow(int64(1), int64(1), "E", "msg", "f.go", 1, now, "api", nil, nil, nil),
	)
	groups, err := svc.GetErrorsGroupedForOrg(1, "", "", "", from, to)
	if err != nil || len(groups) != 1 {
		t.Fatalf("expected 1 group, got %v/%v", groups, err)
	}
}

func TestGetErrorsGroupedForOrg_ExplicitDateRange_DayBuckets(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	from := now.AddDate(0, 0, -10).UTC().Format(time.RFC3339)
	to := now.UTC().Format(time.RFC3339)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows(errorCols()))
	groups, err := svc.GetErrorsGroupedForOrg(1, "", "", "", from, to)
	if err != nil || len(groups) != 0 {
		t.Fatalf("expected empty, got %v/%v", groups, err)
	}
}

func TestGetErrorsGroupedForOrg_StartDateAfterEndDate_Swapped(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	// since > until → they get swapped
	from := now.UTC().Format(time.RFC3339)
	to := now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows(errorCols()))
	_, err := svc.GetErrorsGroupedForOrg(1, "", "", "", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetErrorsGroupedForOrg_InvalidStartDate(t *testing.T) {
	db, _ := newDB(t)
	svc := NewErrorService(db)
	_, err := svc.GetErrorsGroupedForOrg(1, "", "", "", "not-a-date", "2025-01-01T00:00:00Z")
	if err == nil {
		t.Fatal("expected error for invalid start_date")
	}
}

func TestGetErrorsGroupedForOrg_InvalidEndDate(t *testing.T) {
	db, _ := newDB(t)
	svc := NewErrorService(db)
	_, err := svc.GetErrorsGroupedForOrg(1, "", "", "", "2025-01-01T00:00:00Z", "not-a-date")
	if err == nil {
		t.Fatal("expected error for invalid end_date")
	}
}

func TestGetErrorsGroupedForOrg_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db error"))
	_, err := svc.GetErrorsGroupedForOrg(1, "", "", "", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetErrorsGroupedForOrg_WithServiceAndEnvFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(groupedErrorCols()).
			AddRow(int64(1), int64(1), "E1", "msg1", "f.go", 1, now, "api", nil, nil, nil).
			AddRow(int64(2), int64(1), "E1", "msg1", "f.go", 1, now.Add(-time.Minute), "api", nil, nil, nil),
	)
	// 2 rows, same (name,message,file) → 1 group, count=2
	groups, err := svc.GetErrorsGroupedForOrg(1, "api", "10", "", "", "")
	if err != nil || len(groups) != 1 || groups[0].EventCount != 2 {
		t.Fatalf("expected 1 group with count=2, got %v/%v", groups, err)
	}
}

func TestGetErrorsGroupedForOrg_MultipleGroups_SortedByLastSeen(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	earlier := now.Add(-5 * time.Minute)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(groupedErrorCols()).
			AddRow(int64(1), int64(1), "ErrorA", "msgA", "a.go", 1, earlier, "api", nil, nil, nil).
			AddRow(int64(2), int64(1), "ErrorB", "msgB", "b.go", 2, now, "api", nil, nil, nil),
	)
	groups, err := svc.GetErrorsGroupedForOrg(1, "", "", "", "", "")
	if err != nil || len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %v/%v", groups, err)
	}
	// Sorted by LastSeen desc: ErrorB (now) should be first
	if groups[0].Sample.Name != "ErrorB" {
		t.Fatalf("expected ErrorB first (most recent), got %s", groups[0].Sample.Name)
	}
}

func TestGetErrorsGroupedForOrg_ZeroOrgID_DefaultsToOne(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(sqlmock.NewRows(errorCols()))
	// orgID=0 → defaults to 1; no error expected
	_, err := svc.GetErrorsGroupedForOrg(0, "", "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetErrorsGroupedForOrg_ScanError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(groupedErrorCols()).AddRow(int64(1), int64(1), "E", "msg", "f.go", "not-an-int", time.Now(), "api", nil, nil, nil),
	)
	_, err := svc.GetErrorsGroupedForOrg(1, "", "", "", "", "")
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestGetErrorsGroupedForOrg_WithHTTPFields(t *testing.T) {
	db, mock := newDB(t)
	svc := NewErrorService(db)
	now := time.Now()
	method, url := "POST", "http://e.com"
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(groupedErrorCols()).AddRow(int64(1), int64(1), "E", "msg", "f.go", 1, now, "api", method, url, "abc123trace"),
	)
	groups, err := svc.GetErrorsGroupedForOrg(1, "", "", "", "", "")
	if err != nil || len(groups) != 1 {
		t.Fatalf("expected 1 group, got %v/%v", groups, err)
	}
	if groups[0].Sample.HTTPMethod == nil {
		t.Fatal("expected HTTPMethod set")
	}
}

// Reading the request body out of the database is what makes the grouped list
// cost grow with the error volume: one blob per group, for two fields only an
// error's detail ever shows. The query must not ask for them at all.
func TestGetErrorsGroupedForOrg_DoesNotSelectTheHTTPRequestPayload(t *testing.T) {
	var executed string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(
		sqlmock.QueryMatcherFunc(func(expectedSQL, actualSQL string) error {
			executed = actualSQL
			return nil
		})))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	mock.ExpectQuery("").WillReturnRows(sqlmock.NewRows(groupedErrorCols()))

	if _, err := NewErrorService(db).GetErrorsGroupedForOrg(1, "", "", "", "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, column := range []string{"http_headers", "http_body"} {
		if strings.Contains(executed, column) {
			t.Fatalf("grouped query still selects %s: %s", column, executed)
		}
	}
}

func TestBucketKeyFor_Day(t *testing.T) {
	ts := time.Date(2025, 3, 5, 14, 30, 0, 0, time.UTC)
	key := bucketKeyFor(ts, "day")
	if key != "2025-03-05" {
		t.Fatalf("expected 2025-03-05, got %s", key)
	}
}

func TestBucketKeyFor_Hour(t *testing.T) {
	ts := time.Date(2025, 3, 5, 14, 30, 0, 0, time.UTC)
	key := bucketKeyFor(ts, "hour")
	if key != "2025-03-05T14" {
		t.Fatalf("expected 2025-03-05T14, got %s", key)
	}
}

func TestBucketCountsToTimeseries_Empty(t *testing.T) {
	result := bucketCountsToTimeseries(map[string]int64{}, "hour", time.Now().Add(-1*time.Hour), time.Now())
	if result != nil {
		t.Fatal("expected nil for empty map")
	}
}

func TestBucketCountsToTimeseries_HourBuckets(t *testing.T) {
	since := time.Date(2025, 3, 5, 12, 0, 0, 0, time.UTC)
	until := time.Date(2025, 3, 5, 14, 0, 0, 0, time.UTC)
	m := map[string]int64{"2025-03-05T13": 5}
	pts := bucketCountsToTimeseries(m, "hour", since, until)
	if len(pts) == 0 {
		t.Fatal("expected at least 1 point")
	}
	var found bool
	for _, p := range pts {
		if p.Date == "2025-03-05T13" && p.Count == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bucket with count=5, got %v", pts)
	}
}

func TestBucketCountsToTimeseries_DayBuckets(t *testing.T) {
	since := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2025, 3, 3, 0, 0, 0, 0, time.UTC)
	m := map[string]int64{"2025-03-02": 10}
	pts := bucketCountsToTimeseries(m, "day", since, until)
	if len(pts) == 0 {
		t.Fatal("expected at least 1 point")
	}
	var found bool
	for _, p := range pts {
		if p.Date == "2025-03-02" && p.Count == 10 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bucket 2025-03-02 with count=10, got %v", pts)
	}
}

func TestSortErrorGroupsByLastSeen_Descending(t *testing.T) {
	now := time.Now()
	groups := []models.ErrorGroup{
		{LastSeen: now.Add(-5 * time.Minute)},
		{LastSeen: now},
		{LastSeen: now.Add(-1 * time.Minute)},
	}
	sortErrorGroupsByLastSeen(groups)
	if !groups[0].LastSeen.Equal(now) {
		t.Fatalf("expected most recent first, got %v", groups[0].LastSeen)
	}
	if groups[1].LastSeen.Before(groups[2].LastSeen) {
		t.Fatalf("expected descending order, got %v %v", groups[1].LastSeen, groups[2].LastSeen)
	}
}

func TestSortErrorGroupsByLastSeen_Single(t *testing.T) {
	now := time.Now()
	groups := []models.ErrorGroup{{LastSeen: now}}
	sortErrorGroupsByLastSeen(groups) // should not panic
	if !groups[0].LastSeen.Equal(now) {
		t.Fatal("expected unchanged")
	}
}
