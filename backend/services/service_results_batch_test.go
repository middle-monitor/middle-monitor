package services

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

var serviceResultCols = []string{
	"id", "service_id", "status", "latency", "message", "timestamp",
	"metric_type", "metric_value", "metadata",
}

func servicesWithIDs(ids ...int64) []models.ServiceWithResults {
	out := make([]models.ServiceWithResults, 0, len(ids))
	for _, id := range ids {
		out = append(out, models.ServiceWithResults{Service: models.Service{ID: id}})
	}
	return out
}

// TestEnrichServiceResults_OneQueryWhateverTheServiceCount pins the cost model of
// the services endpoint: the number of SQL round trips must not grow with the
// number of services returned. A query per service is what made the endpoint
// cost 255 ms of median server time for 26 services while its SQL totalled 7 ms.
func TestEnrichServiceResults_OneQueryWhateverTheServiceCount(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	now := time.Now().UTC()

	rows := sqlmock.NewRows(serviceResultCols).
		AddRow(int64(10), int64(1), "success", 12.5, nil, now, nil, nil, nil).
		AddRow(int64(9), int64(1), "failure", nil, "boom", now.Add(-time.Minute), nil, nil, nil).
		AddRow(int64(8), int64(3), "success", nil, nil, now.Add(-2*time.Minute), "cpu", 42.0, `{"host":"web-01"}`)
	mock.ExpectQuery("JOIN LATERAL").
		WithArgs(int64(1), int64(2), int64(3)).
		WillReturnRows(rows)

	list := servicesWithIDs(1, 2, 3)
	svc.enrichServiceResults(list, "", "")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected query count: %v", err)
	}
	if len(list[0].Results) != 2 {
		t.Fatalf("service 1: got %d results, want 2", len(list[0].Results))
	}
	if list[0].Results[0].ID != 10 || list[0].Results[1].ID != 9 {
		t.Fatalf("service 1: results not kept in timestamp DESC order: %+v", list[0].Results)
	}
	if list[1].Results != nil {
		t.Fatalf("service 2 has no result row and must stay null, got %+v", list[1].Results)
	}
	if len(list[2].Results) != 1 || *list[2].Results[0].MetricValue != 42.0 {
		t.Fatalf("service 3: agent metric fields lost: %+v", list[2].Results)
	}
}

// TestEnrichServiceResults_LimitsMatchTheContract pins the two payload sizes the
// clients rely on: up to 10 recent results by default, up to 500 when a date
// range is passed (the compliance export reads that window).
func TestEnrichServiceResults_LimitsMatchTheContract(t *testing.T) {
	cases := []struct {
		name      string
		startDate string
		endDate   string
		wantLimit string
	}{
		{"no range", "", "", "LIMIT 10"},
		{"date range", "2026-09-01", "2026-09-16", "LIMIT 500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, mock := newDB(t)
			svc := NewServiceService(db)
			mock.ExpectQuery(regexp.QuoteMeta(c.wantLimit)).
				WillReturnRows(sqlmock.NewRows(serviceResultCols).
					AddRow(int64(1), int64(1), "success", nil, nil, time.Now().UTC(), nil, nil, nil))

			svc.enrichServiceResults(servicesWithIDs(1), c.startDate, c.endDate)

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("%s: %v", c.wantLimit, err)
			}
		})
	}
}

// TestEnrichServiceResults_DateRangeFallsBackForSilentServices keeps the status
// badge truthful: a service that produced nothing inside the requested range
// still carries its latest result overall, and that fallback is one extra query
// for all silent services, not one per service.
func TestEnrichServiceResults_DateRangeFallsBackForSilentServices(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	now := time.Now().UTC()

	mock.ExpectQuery("timestamptz").
		WithArgs(int64(1), int64(2), int64(3), "2026-09-01", "2026-09-16").
		WillReturnRows(sqlmock.NewRows(serviceResultCols).
			AddRow(int64(10), int64(1), "success", nil, nil, now, nil, nil, nil))
	mock.ExpectQuery(regexp.QuoteMeta("LIMIT 1")).
		WithArgs(int64(2), int64(3)).
		WillReturnRows(sqlmock.NewRows(serviceResultCols).
			AddRow(int64(4), int64(2), "failure", nil, "down", now.Add(-72*time.Hour), nil, nil, nil))

	list := servicesWithIDs(1, 2, 3)
	svc.enrichServiceResults(list, "2026-09-01", "2026-09-16")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected query count: %v", err)
	}
	if len(list[1].Results) != 1 || list[1].Results[0].ID != 4 {
		t.Fatalf("silent service lost its fallback result: %+v", list[1].Results)
	}
	if list[2].Results != nil {
		t.Fatalf("service without any result must stay null, got %+v", list[2].Results)
	}
}

// TestGetServicesPageForOrg_DoesNotLoopOverThePage pins the same rule on the
// paginated path: paging shrinks the loop to the page size, it does not remove
// it, so the page must be enriched by a single extra query.
func TestGetServicesPageForOrg_DoesNotLoopOverThePage(t *testing.T) {
	db, mock := newDB(t)
	svc := NewServiceService(db)
	now := time.Now().UTC()

	rows := sqlmock.NewRows(serviceRowCols)
	for _, id := range []int64{1, 2, 3} {
		rows.AddRow(id, int64(1), nil, "svc", nil, "http", "example.com", nil, nil, "api", 60, 3,
			nil, nil, nil, nil, nil, nil, nil, now, nil, "success", true)
	}
	mock.ExpectQuery("FROM services s").WillReturnRows(rows)
	mock.ExpectQuery("JOIN LATERAL").
		WithArgs(int64(1), int64(2), int64(3)).
		WillReturnRows(sqlmock.NewRows(serviceResultCols).
			AddRow(int64(7), int64(2), "success", nil, nil, now, nil, nil, nil))

	page, total, err := svc.GetServicesPageForOrg(1, "", "", "", "", "", 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected query count: %v", err)
	}
	if total != 3 || len(page) != 3 {
		t.Fatalf("got total=%d len=%d, want 3/3", total, len(page))
	}
	if len(page[1].Results) != 1 {
		t.Fatalf("page row 2 lost its result: %+v", page[1].Results)
	}
}
