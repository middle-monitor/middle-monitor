package services

import (
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestSaturationConfidence_BelowThreshold(t *testing.T) {
	conf := saturationConfidence(80.0, 90.0)
	if conf != 0.6 {
		t.Fatalf("expected 0.6, got %v", conf)
	}
}

func TestSaturationConfidence_AboveThreshold(t *testing.T) {
	conf := saturationConfidence(95.0, 90.0)
	// (95-90)/(100-90) = 0.5 → 0.6 + 0.35*0.5 = 0.775 → round2 = 0.78
	if conf < 0.6 || conf > 0.95 {
		t.Fatalf("expected confidence in [0.6, 0.95], got %v", conf)
	}
}

func TestSaturationConfidence_AtThreshold(t *testing.T) {
	conf := saturationConfidence(90.0, 90.0)
	// value == threshold → 0.6
	if conf != 0.6 {
		t.Fatalf("expected 0.6, got %v", conf)
	}
}

func TestSaturationConfidence_MaxCapped(t *testing.T) {
	// value far above threshold → capped at 0.95
	conf := saturationConfidence(99.9, 90.0)
	if conf > 0.95 {
		t.Fatalf("expected <= 0.95, got %v", conf)
	}
}

func TestSaturationConfidence_ZeroSpan(t *testing.T) {
	// threshold = 100, span = 0 → returns 0.9
	conf := saturationConfidence(105.0, 100.0)
	if conf != 0.9 {
		t.Fatalf("expected 0.9, got %v", conf)
	}
}

func TestNeighbourConfidence_Preceded(t *testing.T) {
	if c := neighbourConfidence(true); c != 0.75 {
		t.Fatalf("expected 0.75, got %v", c)
	}
}

func TestNeighbourConfidence_NotPreceded(t *testing.T) {
	if c := neighbourConfidence(false); c != 0.45 {
		t.Fatalf("expected 0.45, got %v", c)
	}
}

func TestRound2(t *testing.T) {
	if r := round2(0.1234); r != 0.12 {
		t.Fatalf("expected 0.12, got %v", r)
	}
	if r := round2(0.5678); r != 0.57 {
		t.Fatalf("expected 0.57, got %v", r)
	}
}

func TestOverallConfidence_NoSignals(t *testing.T) {
	r := &models.CorrelationResult{}
	if c := overallConfidence(r); c != 0.0 {
		t.Fatalf("expected 0.0, got %v", c)
	}
}

func TestOverallConfidence_OneSemanticMatch(t *testing.T) {
	r := &models.CorrelationResult{
		SemanticMatches: []string{"connection refused"},
	}
	c := overallConfidence(r)
	if c < 0.6 || c > 0.98 {
		t.Fatalf("expected confidence in [0.6, 0.98], got %v", c)
	}
}

func TestOverallConfidence_MultipleSignals_Bonus(t *testing.T) {
	r := &models.CorrelationResult{
		Infra:           []models.InfraCorrelation{{Confidence: 0.8}},
		Services:        []models.ServiceCorrelation{{Confidence: 0.75}},
		SemanticMatches: []string{"timeout"},
	}
	c := overallConfidence(r)
	// 3 signals → 2 bonuses of 0.05 → best(0.8) + 0.05 + 0.05 = 0.9
	if c < 0.85 {
		t.Fatalf("expected bonus from multiple signals, got %v", c)
	}
}

func TestOverallConfidence_Recurrence_IsNew(t *testing.T) {
	r := &models.CorrelationResult{
		Recurrence: &models.ErrorRecurrence{IsNew: true},
	}
	c := overallConfidence(r)
	if c < 0.65 {
		t.Fatalf("expected >= 0.65 for new error, got %v", c)
	}
}

func TestOverallConfidence_Recurrence_IsRecurrent(t *testing.T) {
	r := &models.CorrelationResult{
		Recurrence: &models.ErrorRecurrence{IsRecurrent: true},
	}
	c := overallConfidence(r)
	if c < 0.65 {
		t.Fatalf("expected >= 0.65 for recurrent error, got %v", c)
	}
}

func TestOverallConfidence_CappedAt098(t *testing.T) {
	infra := make([]models.InfraCorrelation, 5)
	for i := range infra {
		infra[i] = models.InfraCorrelation{Confidence: 0.95}
	}
	r := &models.CorrelationResult{Infra: infra, SemanticMatches: []string{"a", "b", "c"}}
	c := overallConfidence(r)
	if c > 0.98 {
		t.Fatalf("expected <= 0.98, got %v", c)
	}
}

func TestDescribeNeighbours_Empty(t *testing.T) {
	if s := describeNeighbours(nil); s != "" {
		t.Fatalf("expected empty string, got %q", s)
	}
}

func TestDescribeNeighbours_SomePreceding(t *testing.T) {
	svcs := []models.ServiceCorrelation{
		{ServiceName: "db", PrecededIncident: true},
		{ServiceName: "cache", PrecededIncident: false},
	}
	s := describeNeighbours(svcs)
	if s == "" || s == "Aucune" {
		t.Fatalf("expected description with preceding service, got %q", s)
	}
	// Should mention "db" as preceding
	if len(s) == 0 {
		t.Fatal("expected non-empty description")
	}
}

func TestDescribeNeighbours_NoPreceding(t *testing.T) {
	svcs := []models.ServiceCorrelation{
		{ServiceName: "db", PrecededIncident: false},
	}
	s := describeNeighbours(svcs)
	if s == "" {
		t.Fatal("expected non-empty description")
	}
}

func TestLinkedAppConfidence(t *testing.T) {
	// A dependency that errored first is the strongest app-level cause signal;
	// anything else stays a weak/contextual signal.
	if c := linkedAppConfidence("dependency", true); c != 0.8 {
		t.Fatalf("expected 0.8 for preceding dependency, got %v", c)
	}
	if c := linkedAppConfidence("dependency", false); c != 0.5 {
		t.Fatalf("expected 0.5 for non-preceding dependency, got %v", c)
	}
	if c := linkedAppConfidence("dependent", true); c != 0.4 {
		t.Fatalf("expected 0.4 for dependent, got %v", c)
	}
}

func TestOverallConfidence_DependencyAppDrives(t *testing.T) {
	// A linked dependency that errored first must outweigh the historical
	// flat 0.4 co-occurrence score.
	r := &models.CorrelationResult{
		Apps: []models.AppCorrelation{{Service: "payment", Relation: "dependency", PrecededIncident: true, Confidence: 0.8}},
	}
	if c := overallConfidence(r); c < 0.8 {
		t.Fatalf("expected >= 0.8 driven by dependency app, got %v", c)
	}
}

func TestOverallConfidence_LegacyAppFallsBackTo04(t *testing.T) {
	r := &models.CorrelationResult{
		Apps: []models.AppCorrelation{{Service: "other"}},
	}
	if c := overallConfidence(r); c != 0.4 {
		t.Fatalf("expected historical 0.4 for zero-confidence app, got %v", c)
	}
}

func TestDescribeCoApps_SplitsDependencyFromHostGroup(t *testing.T) {
	apps := []models.AppCorrelation{
		{Service: "payment", Relation: "dependency", PrecededIncident: true},
		{Service: "catalog", Relation: "host_group"},
		{Service: "frontend", Relation: "dependent"},
	}
	s := describeCoApps(apps)
	if !strings.Contains(s, "payment (errored first)") {
		t.Fatalf("expected dependency mention, got %q", s)
	}
	if !strings.Contains(s, "catalog") {
		t.Fatalf("expected host-group mention, got %q", s)
	}
	// Dependents are impact, not cause: they must not pollute the summary.
	if strings.Contains(s, "frontend") {
		t.Fatalf("expected dependent excluded from summary, got %q", s)
	}
}

func TestDescribeCoApps_OnlyDependents_Empty(t *testing.T) {
	apps := []models.AppCorrelation{{Service: "frontend", Relation: "dependent"}}
	if s := describeCoApps(apps); s != "" {
		t.Fatalf("expected empty summary for dependents only, got %q", s)
	}
}

func TestGetLinkedAppNames_BothDirections(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(
		sqlmock.NewRows([]string{"name"}).AddRow("payment"),
	)
	mock.ExpectQuery("SELECT al.app_service_name FROM application_links").WillReturnRows(
		sqlmock.NewRows([]string{"app_service_name"}).AddRow("frontend"),
	)
	deps, dependents := svc.getLinkedAppNames(1, "checkout")
	if len(deps) != 1 || deps[0] != "payment" {
		t.Fatalf("expected [payment], got %v", deps)
	}
	if len(dependents) != 1 || dependents[0] != "frontend" {
		t.Fatalf("expected [frontend], got %v", dependents)
	}
}

func TestGetLinkedAppNames_DBError_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnError(errors.New("db down"))
	mock.ExpectQuery("SELECT al.app_service_name FROM application_links").WillReturnError(errors.New("db down"))
	deps, dependents := svc.getLinkedAppNames(1, "checkout")
	if deps != nil || dependents != nil {
		t.Fatalf("expected nil slices on error, got %v/%v", deps, dependents)
	}
}

func TestQueryLinkedAppErrors_NoApps(t *testing.T) {
	db, _ := newDB(t)
	svc := NewCorrelationService(db)
	if out := svc.queryLinkedAppErrors(1, nil, "dependency", time.Now(), time.Now(), time.Now()); out != nil {
		t.Fatalf("expected nil for empty app list, got %v", out)
	}
}

func TestQueryLinkedAppErrors_TagsRelationAndConfidence(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	ref := time.Now()
	before := ref.Add(-2 * time.Minute)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"service", "name", "count", "min"}).
			AddRow("payment", "ConnectionTimeout", 3, before),
	)
	out := svc.queryLinkedAppErrors(1, []string{"payment"}, "dependency", ref.Add(-5*time.Minute), ref.Add(time.Minute), ref)
	if len(out) != 1 {
		t.Fatalf("expected 1 correlation, got %v", out)
	}
	a := out[0]
	if a.Relation != "dependency" || !a.PrecededIncident || a.Confidence != 0.8 {
		t.Fatalf("expected preceding dependency at 0.8, got %+v", a)
	}
}

func TestNewCorrelationService(t *testing.T) {
	db, _ := newDB(t)
	if NewCorrelationService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestGetCorrelationForError_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("not found"))
	_, err := svc.GetCorrelationForError(1, 999)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetCorrelationForError_MinimalSuccess(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	// Fetch error
	corrErrCols := []string{"id", "name", "message", "file", "line", "timestamp", "service", "http_method", "http_url", "http_headers", "http_body", "fingerprint"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(corrErrCols).AddRow(int64(1), "NullPtr", "connection refused", "f.go", 10, now, "api", nil, nil, nil, nil, nil),
	)
	// host lookup → not found, then linked host → not found either
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectQuery("FROM application_links al").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	// failing services → empty
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "first_fail", "message"}))
	// computeRecurrence
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(1), int64(1), int64(1), now, now),
	)
	result, err := svc.GetCorrelationForError(1, 1)
	if err != nil || result == nil {
		t.Fatalf("expected result, got %v/%v", result, err)
	}
	// "connection refused" → semantic match
	if len(result.SemanticMatches) == 0 {
		t.Fatal("expected semantic match for 'connection refused'")
	}
}

func TestGetCorrelationForError_WithHostAndMetrics(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	corrErrCols := []string{"id", "name", "message", "file", "line", "timestamp", "service", "http_method", "http_url", "http_headers", "http_body", "fingerprint"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(corrErrCols).AddRow(int64(1), "OOM", "out of memory", "f.go", 10, now, "api", nil, nil, nil, nil, "fp1"),
	)
	// host lookup → found
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(5), "web-01"),
	)
	// agent metrics on the host → CPU peaked above the threshold in the window
	mock.ExpectQuery("GROUP BY s.type").WillReturnRows(
		sqlmock.NewRows([]string{"type", "host", "threshold", "peak"}).
			AddRow("agent_cpu", "web-01", float64(90), float64(95)).
			AddRow("agent_ram", "web-01", float64(90), float64(40)),
	)
	// failing services
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "first_fail", "message"}))
	// computeRecurrence
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(0), int64(0), int64(0), nil, nil),
	)
	result, err := svc.GetCorrelationForError(1, 1)
	if err != nil || result == nil {
		t.Fatalf("expected result, got %v/%v", result, err)
	}
	if result.HostID == nil || *result.HostID != 5 {
		t.Fatalf("expected hostID=5, got %v", result.HostID)
	}
	// The whole point of infra correlation: an app error on a host whose CPU
	// saturated must carry that saturation as a signal. Only the metric above
	// the threshold counts — RAM at 40% is not evidence of anything.
	if len(result.Infra) != 1 || result.Infra[0].MetricName != "CPU" || result.Infra[0].Value != 95 {
		t.Fatalf("expected a single CPU infra signal at 95%%, got %+v", result.Infra)
	}
	// OOM → semantic match
	if len(result.SemanticMatches) == 0 {
		t.Fatal("expected OOM semantic match")
	}
}

// Saturation is relative to what the user declared as saturated for that metric,
// not to a constant: the same 82% peak is evidence on a check whose threshold is
// 70% and noise on one whose threshold is 90%.
func TestInfraSaturation_UsesPerServiceThreshold(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	mock.ExpectQuery("GROUP BY s.type").WillReturnRows(
		sqlmock.NewRows([]string{"type", "host", "threshold", "peak"}).
			AddRow("agent_cpu", "web-01", float64(70), float64(82)).
			AddRow("agent_ram", "web-01", float64(90), float64(82)),
	)
	infra := svc.infraSaturation([]int64{5}, time.Now().Add(-5*time.Minute), time.Now(), 90.0)
	if len(infra) != 1 || infra[0].MetricName != "CPU" {
		t.Fatalf("expected only the CPU signal (threshold 70), got %+v", infra)
	}
	if infra[0].Threshold != 70 {
		t.Fatalf("expected the service's own threshold 70, got %v", infra[0].Threshold)
	}
}

// An app linked to a host GROUP names no host directly, yet it still runs on the
// group's hosts: their saturation must reach the correlation.
func TestGetCorrelationForError_InfraFromGroupLinkedHost(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	corrErrCols := []string{"id", "name", "message", "file", "line", "timestamp", "service", "http_method", "http_url", "http_headers", "http_body", "fingerprint"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(corrErrCols).AddRow(int64(1), "E", "boom", "f.go", 1, now, "api", nil, nil, nil, nil, "fp1"),
	)
	// Neither the naming convention nor a direct host link resolves a host.
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectQuery("FROM application_links al").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	// Only a host_group link exists, which expands to host 5.
	mock.ExpectQuery("FROM application_links").WillReturnRows(
		sqlmock.NewRows([]string{"target_type", "target_id", "target_app_name"}).AddRow("host_group", int64(7), ""),
	)
	mock.ExpectQuery("FROM hosts WHERE host_group_id").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow(int64(5)),
	)
	mock.ExpectQuery("GROUP BY s.type").WillReturnRows(
		sqlmock.NewRows([]string{"type", "host", "threshold", "peak"}).
			AddRow("agent_cpu", "web-01", float64(90), float64(96)),
	)

	result, err := svc.GetCorrelationForError(1, 1)
	if err != nil || result == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Infra) != 1 || result.Infra[0].MetricName != "CPU" {
		t.Fatalf("expected the group's host saturation, got %+v", result.Infra)
	}
	// The host must be named: with a group scope there can be several.
	if !strings.Contains(result.Infra[0].Description, "web-01") {
		t.Fatalf("expected the host name in the description, got %q", result.Infra[0].Description)
	}
}

func TestGetCorrelationForError_FingerprintFallback(t *testing.T) {
	// When fingerprint column is empty, ComputeErrorFingerprint is called
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	corrErrCols := []string{"id", "name", "message", "file", "line", "timestamp", "service", "http_method", "http_url", "http_headers", "http_body", "fingerprint"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(corrErrCols).AddRow(int64(1), "E", "timeout msg", "f.go", 1, now, "api", nil, nil, nil, nil, ""),
	)
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectQuery("FROM application_links al").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "first_fail", "message"}))
	// computeRecurrence (empty fingerprint was computed → called with computed fp)
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(0), int64(0), int64(0), nil, nil),
	)
	result, err := svc.GetCorrelationForError(1, 1)
	if err != nil || result == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SemanticMatches) == 0 {
		t.Fatal("expected timeout semantic match")
	}
}

func TestGetCorrelationForError_WithFailingNeighbours(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	corrErrCols := []string{"id", "name", "message", "file", "line", "timestamp", "service", "http_method", "http_url", "http_headers", "http_body", "fingerprint"}
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows(corrErrCols).AddRow(int64(1), "E", "no space left on device", "f.go", 1, now, "api", nil, nil, nil, nil, "fp1"),
	)
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectQuery("FROM application_links al").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	// Scope: the app is linked to host group 7, which contains host 5.
	mock.ExpectQuery("FROM application_links").WillReturnRows(
		sqlmock.NewRows([]string{"target_type", "target_id", "target_app_name"}).AddRow("host_group", int64(7), ""),
	)
	mock.ExpectQuery("FROM hosts WHERE host_group_id").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow(int64(5)),
	)
	// Infra over the link-derived scope (host 5): nothing saturated
	mock.ExpectQuery("GROUP BY s.type").WillReturnRows(
		sqlmock.NewRows([]string{"type", "host", "threshold", "peak"}),
	)
	// A failing service in the same host group
	failCols := []string{"id", "name", "first_fail", "message"}
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(
		sqlmock.NewRows(failCols).AddRow(int64(5), "db-check", now.Add(-3*time.Minute), "connection refused"),
	)
	// Direct app links: none in either direction
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"name"}))
	mock.ExpectQuery("SELECT al.app_service_name").WillReturnRows(sqlmock.NewRows([]string{"app_service_name"}))
	// Co-occurring apps in the same host group: none
	mock.ExpectQuery("FROM application_errors ae").WillReturnRows(
		sqlmock.NewRows([]string{"service", "name", "count", "min"}),
	)
	// Recurrence
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(0), int64(0), int64(0), nil, nil),
	)
	result, err := svc.GetCorrelationForError(1, 1)
	if err != nil || result == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Services) != 1 {
		t.Fatalf("expected 1 service correlation, got %v", result.Services)
	}
}

func TestGetCorrelationForServiceResult_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	mock.ExpectQuery("FROM service_results sr").WillReturnError(errors.New("not found"))
	_, err := svc.GetCorrelationForServiceResult(1, 999)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetCorrelationForServiceResult_MinimalSuccess(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	msg := "connection refused"
	srCols := []string{"id", "service_id", "status", "message", "timestamp", "service_name", "type", "host_id", "host_name"}
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(
		sqlmock.NewRows(srCols).AddRow(int64(1), int64(10), "failure", msg, now, "api", "http", nil, nil),
	)
	// no host → no infra lookup at all
	// failing neighbours
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "first_fail", "message"}))
	result, err := svc.GetCorrelationForServiceResult(1, 1)
	if err != nil || result == nil {
		t.Fatalf("expected result, got %v/%v", result, err)
	}
	if len(result.SemanticMatches) == 0 {
		t.Fatal("expected semantic match for connection refused")
	}
}

func TestGetCorrelationForServiceResult_WithHostID(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	msg := "certificate expired x509"
	srCols := []string{"id", "service_id", "status", "message", "timestamp", "service_name", "type", "host_id", "host_name"}
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(
		sqlmock.NewRows(srCols).AddRow(int64(1), int64(10), "failure", msg, now, "api", "agent_cpu", int64(5), "web-01"),
	)
	mock.ExpectQuery("GROUP BY s.type").WillReturnRows(
		sqlmock.NewRows([]string{"type", "host", "threshold", "peak"}).AddRow("agent_cpu", "web-01", float64(85), float64(92)),
	)
	// hostFilter applied (agent_ type with host_id)
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "first_fail", "message"}))
	result, err := svc.GetCorrelationForServiceResult(1, 1)
	if err != nil || result == nil {
		t.Fatalf("expected result, got %v/%v", result, err)
	}
	if result.HostID == nil || *result.HostID != 5 {
		t.Fatalf("expected hostID=5, got %v", result.HostID)
	}
	// A check failing on a host whose CPU saturated must carry that signal.
	if len(result.Infra) != 1 || result.Infra[0].MetricName != "CPU" {
		t.Fatalf("expected a CPU infra signal, got %+v", result.Infra)
	}
	// x509 → semantic match
	if len(result.SemanticMatches) == 0 {
		t.Fatal("expected x509 semantic match")
	}
}

func TestGetCorrelationForServiceResult_NilMessage(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	srCols := []string{"id", "service_id", "status", "message", "timestamp", "service_name", "type", "host_id", "host_name"}
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(
		sqlmock.NewRows(srCols).AddRow(int64(1), int64(10), "failure", nil, now, "api", "http", nil, nil),
	)
	mock.ExpectQuery("FROM service_results sr").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "first_fail", "message"}))
	result, err := svc.GetCorrelationForServiceResult(1, 1)
	if err != nil || result == nil {
		t.Fatalf("expected result, got %v/%v", result, err)
	}
	// No message → no semantic matches
	if len(result.SemanticMatches) != 0 {
		t.Fatalf("expected 0 semantic matches for nil message, got %v", result.SemanticMatches)
	}
}

func TestComputeRecurrence_EmptyFingerprint(t *testing.T) {
	db, _ := newDB(t)
	svc := NewCorrelationService(db)
	result := svc.computeRecurrence(1, "", time.Now())
	if result != nil {
		t.Fatal("expected nil for empty fingerprint")
	}
}

func TestComputeRecurrence_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	mock.ExpectQuery("FROM application_errors").WillReturnError(errors.New("db error"))
	result := svc.computeRecurrence(1, "fp1", time.Now())
	if result != nil {
		t.Fatal("expected nil on DB error")
	}
}

func TestComputeRecurrence_IsNew(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	firstSeen := now.Add(-30 * time.Minute) // within 1h → IsNew
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(3), int64(3), int64(3), firstSeen, now),
	)
	rec := svc.computeRecurrence(1, "fp1", now)
	if rec == nil || !rec.IsNew {
		t.Fatalf("expected IsNew=true, got %v", rec)
	}
}

func TestComputeRecurrence_IsRecurrent(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	firstSeen := now.Add(-48 * time.Hour) // 2 days ago → before 24h cutoff
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(1), int64(5), int64(25), firstSeen, now),
	)
	rec := svc.computeRecurrence(1, "fp1", now)
	if rec == nil || !rec.IsRecurrent {
		t.Fatalf("expected IsRecurrent=true, got %v", rec)
	}
}

func TestComputeRecurrence_RepeatedInLastHour(t *testing.T) {
	db, mock := newDB(t)
	svc := NewCorrelationService(db)
	now := time.Now()
	firstSeen := now.Add(-2 * time.Hour) // older than 1h → not new
	mock.ExpectQuery("FROM application_errors").WillReturnRows(
		sqlmock.NewRows([]string{"count_h", "count_24h", "count_7d", "first_seen", "last_seen"}).
			AddRow(int64(5), int64(5), int64(5), firstSeen, now),
	)
	rec := svc.computeRecurrence(1, "fp1", now)
	if rec == nil || rec.IsNew || rec.IsRecurrent || rec.CountLastHour <= 1 {
		t.Fatalf("expected repeated count>1, got %v", rec)
	}
	if rec.Description == "" {
		t.Fatal("expected description for repeated error")
	}
}
