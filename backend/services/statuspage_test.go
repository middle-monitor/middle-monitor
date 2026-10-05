package services

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// The status page is the one endpoint served without authentication, so what it
// must never do is describe anything an operator did not publish.

func orgRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "plan", "trial_ends_at", "custom_retention_days"}).
		AddRow(int64(3), "pro", nil, nil)
}

func TestStatusPage_QueriesOnlyPublishedChecks(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("FROM organizations WHERE slug").WillReturnRows(orgRows())
	// Each expectation is a regex against the SQL actually run: dropping the
	// public filter from any of them would publish the whole self-monitoring
	// org, internal checks included, and fail here.
	mock.ExpectQuery(`FROM services s[\s\S]*s\.public`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "service_interval", "status"}).AddRow(int64(1), "API", 60, "success"))
	mock.ExpectQuery(`FROM service_results sr[\s\S]*s\.public`).
		WillReturnRows(sqlmock.NewRows([]string{"service_id", "day", "failures", "total"}))
	mock.ExpectQuery(`FROM incidents i[\s\S]*s\.public`).
		WillReturnRows(sqlmock.NewRows([]string{"service_id", "component", "title", "type", "severity", "started_at", "resolved_at"}))
	mock.ExpectQuery(`FROM maintenance_windows m[\s\S]*s\.public`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "starts_at", "ends_at"}))

	if _, err := NewStatusPageService(db).Get("admin"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected queries: %v", err)
	}
}

// A payload that carried the check target would hand out the internal topology.
func TestStatusPage_ExposesNoInternalFields(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("FROM organizations WHERE slug").WillReturnRows(orgRows())
	mock.ExpectQuery("FROM services s").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "service_interval", "status"}).AddRow(int64(1), "API", 60, "failure"))
	mock.ExpectQuery("FROM service_results sr").
		WillReturnRows(sqlmock.NewRows([]string{"service_id", "day", "failures", "total"}))
	mock.ExpectQuery("FROM incidents i").
		WillReturnRows(sqlmock.NewRows([]string{"service_id", "component", "title", "type", "severity", "started_at", "resolved_at"}).
			AddRow(int64(1), "API", "Latency threshold on self-api", "http", "critical", time.Now(), nil))
	mock.ExpectQuery("FROM maintenance_windows m").
		WillReturnRows(sqlmock.NewRows([]string{"name", "starts_at", "ends_at"}))

	page, err := NewStatusPageService(db).Get("admin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"host", "path", "credential", "token", "description"} {
		if strings.Contains(strings.ToLower(string(body)), forbidden) {
			t.Errorf("status payload must not carry %q: %s", forbidden, body)
		}
	}
}

// An org with nothing published must not answer "all systems operational".
func TestStatusPage_NoPublishedCheckIsNotAPage(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("FROM organizations WHERE slug").WillReturnRows(orgRows())
	mock.ExpectQuery("FROM services s").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status"}))

	if _, err := NewStatusPageService(db).Get("admin"); err != ErrStatusPageNotFound {
		t.Fatalf("expected ErrStatusPageNotFound, got %v", err)
	}
}

func TestStatusPage_OverallStatusIsTheWorstComponent(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"all green", []string{StatusOperational, StatusOperational}, StatusOperational},
		{"one warning", []string{StatusOperational, StatusDegraded}, StatusDegraded},
		{"one failure wins over warnings", []string{StatusDegraded, StatusOutage}, StatusOutage},
		{"unknown does not raise the banner", []string{StatusOperational, StatusUnknown}, StatusOperational},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			components := make([]StatusComponent, 0, len(tc.statuses))
			for _, s := range tc.statuses {
				components = append(components, StatusComponent{Status: s})
			}
			if got := overallStatus(components); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A check added yesterday must not read as 1% uptime because the window is 90
// days: days without a sample are unknown and stay out of the ratio.
func TestStatusPage_UptimeIgnoresDaysWithoutSamples(t *testing.T) {
	days := []string{"2026-08-01", "2026-08-02", "2026-08-03"}
	buckets := map[string]dayCounts{
		"2026-08-03": {failures: 0, total: 100},
	}
	bars, uptime := componentHistory(buckets, days, 60)

	if uptime != 100 {
		t.Errorf("uptime = %v, want 100", uptime)
	}
	if bars[0].Status != StatusUnknown || bars[1].Status != StatusUnknown {
		t.Errorf("days without samples must be unknown, got %v", bars)
	}
	if bars[2].Status != StatusOperational {
		t.Errorf("sampled day must be operational, got %q", bars[2].Status)
	}
}

func TestStatusPage_FailedSamplesLowerUptimeAndMarkTheDay(t *testing.T) {
	days := []string{"2026-08-03"}
	bars, uptime := componentHistory(map[string]dayCounts{
		"2026-08-03": {failures: 10, total: 100},
	}, days, 60)

	if uptime != 90 {
		t.Errorf("uptime = %v, want 90", uptime)
	}
	if bars[0].Status != StatusOutage {
		t.Errorf("a day with failures must show an outage, got %q", bars[0].Status)
	}
}

// Retention bounds the history: showing more days than the data is kept for
// would render a wall of "unknown" and understate reliability.
func TestStatusPage_WindowFollowsRetention(t *testing.T) {
	cases := []struct {
		plan   string
		custom int
		want   int
	}{
		{"free", 0, FreeRetentionDays},
		{"pro", 0, ProRetentionDays},
		{"custom", 365, maxStatusWindowDays},
		{"custom", 60, 60},
	}
	for _, tc := range cases {
		if got := statusWindowDays(tc.plan, tc.custom); got != tc.want {
			t.Errorf("statusWindowDays(%q, %d) = %d, want %d", tc.plan, tc.custom, got, tc.want)
		}
	}
}

// A check that flaps opens one incident per cycle. Listing them raw turned a bad
// hour into forty identical one-minute lines on the public page, which reads as
// chaos rather than as the single outage it was.
func TestStatusPage_MergesFlappingIncidents(t *testing.T) {
	base := time.Date(2026, 7, 16, 19, 0, 0, 0, time.UTC)
	flap := func(offset time.Duration) rawIncident {
		start := base.Add(offset)
		end := start.Add(time.Minute)
		return rawIncident{serviceID: 1, StatusIncident: StatusIncident{
			Component: "API", Kind: IncidentUnreachable, Severity: "critical",
			StartedAt: start, ResolvedAt: &end,
		}}
	}

	merged := mergeIncidents([]rawIncident{flap(0), flap(2 * time.Minute), flap(4 * time.Minute)})

	if len(merged) != 1 {
		t.Fatalf("three flaps two minutes apart are one outage, got %d entries", len(merged))
	}
	if !merged[0].StartedAt.Equal(base) {
		t.Errorf("merged outage must start at the first flap, got %v", merged[0].StartedAt)
	}
	if merged[0].ResolvedAt == nil || !merged[0].ResolvedAt.Equal(base.Add(5*time.Minute)) {
		t.Errorf("merged outage must end at the last flap, got %v", merged[0].ResolvedAt)
	}
}

// Two genuinely separate outages must stay separate, or the page would claim a
// single incident spanning days.
func TestStatusPage_KeepsDistantIncidentsApart(t *testing.T) {
	base := time.Date(2026, 7, 16, 19, 0, 0, 0, time.UTC)
	end := base.Add(time.Minute)
	later := base.Add(6 * time.Hour)
	laterEnd := later.Add(time.Minute)

	merged := mergeIncidents([]rawIncident{
		{serviceID: 1, StatusIncident: StatusIncident{Component: "API", Severity: "critical", StartedAt: base, ResolvedAt: &end}},
		{serviceID: 1, StatusIncident: StatusIncident{Component: "API", Severity: "critical", StartedAt: later, ResolvedAt: &laterEnd}},
	})

	if len(merged) != 2 {
		t.Fatalf("outages six hours apart are two incidents, got %d", len(merged))
	}
	if !merged[0].StartedAt.After(merged[1].StartedAt) {
		t.Error("incidents must be listed newest first")
	}
}

// An ongoing flap inside a merged run must keep the whole run open: showing it
// resolved would tell visitors the outage is over while it is not.
func TestStatusPage_OngoingFlapKeepsTheRunOpen(t *testing.T) {
	base := time.Date(2026, 7, 16, 19, 0, 0, 0, time.UTC)
	end := base.Add(time.Minute)

	merged := mergeIncidents([]rawIncident{
		{serviceID: 1, StatusIncident: StatusIncident{Component: "API", Severity: "critical", StartedAt: base, ResolvedAt: &end}},
		{serviceID: 1, StatusIncident: StatusIncident{Component: "API", Severity: "critical", StartedAt: base.Add(2 * time.Minute)}},
	})

	if len(merged) != 1 {
		t.Fatalf("expected one merged run, got %d", len(merged))
	}
	if merged[0].ResolvedAt != nil {
		t.Errorf("run must stay ongoing, got resolved at %v", merged[0].ResolvedAt)
	}
}

// Different components never merge, even when they fail at the same second.
func TestStatusPage_DoesNotMergeAcrossComponents(t *testing.T) {
	base := time.Date(2026, 7, 16, 19, 0, 0, 0, time.UTC)
	end := base.Add(time.Minute)

	merged := mergeIncidents([]rawIncident{
		{serviceID: 1, StatusIncident: StatusIncident{Component: "API", Severity: "critical", StartedAt: base, ResolvedAt: &end}},
		{serviceID: 2, StatusIncident: StatusIncident{Component: "Dashboard", Severity: "critical", StartedAt: base, ResolvedAt: &end}},
	})

	if len(merged) != 2 {
		t.Fatalf("two components failing together are two lines, got %d", len(merged))
	}
}

// Stored titles name the internal check ("Check Failure: api-health"), which is
// noise to a visitor and untranslatable. The API classifies instead, and the
// page renders its own wording from the kind.
func TestStatusPage_ClassifiesIncidentsWithoutLeakingTheCheckName(t *testing.T) {
	cases := []struct {
		title       string
		serviceType string
		want        string
	}{
		{"Check Failure: api-health", "http", IncidentUnreachable},
		{"Check Failure: ping-nextcloud", "ping", IncidentUnreachable},
		{"Certificate expiring soon: api-cert", "certificate", IncidentCertExpiring},
		{"Latency threshold on api-health", "http", IncidentDegraded},
		{"CPU threshold on cpu", "agent_cpu", IncidentDegraded},
		// A certificate check that fails outright did not complete its handshake:
		// calling that "expiring soon" would misinform.
		{"Check Failure: api-cert", "certificate", IncidentDisruption},
		{"Something we never wrote", "http", IncidentDisruption},
	}
	for _, tc := range cases {
		if got := incidentKind(tc.title, tc.serviceType); got != tc.want {
			t.Errorf("incidentKind(%q, %q) = %q, want %q", tc.title, tc.serviceType, got, tc.want)
		}
	}
}

// Incidents are ordered by start, but one opened earlier can resolve later.
// Taking the last row's end moved the run's end backwards and rendered as
// "from 20:15 to 20:14".
func TestStatusPage_MergedRunEndsAtTheLatestResolution(t *testing.T) {
	base := time.Date(2026, 7, 8, 20, 0, 0, 0, time.UTC)
	longEnd := base.Add(2 * time.Hour)
	shortEnd := base.Add(20 * time.Minute)

	merged := mergeIncidents([]rawIncident{
		{serviceID: 1, StatusIncident: StatusIncident{Component: "API", Kind: IncidentUnreachable, StartedAt: base, ResolvedAt: &longEnd}},
		{serviceID: 1, StatusIncident: StatusIncident{Component: "API", Kind: IncidentUnreachable, StartedAt: base.Add(15 * time.Minute), ResolvedAt: &shortEnd}},
	})

	if len(merged) != 1 {
		t.Fatalf("expected one merged run, got %d", len(merged))
	}
	if !merged[0].ResolvedAt.Equal(longEnd) {
		t.Errorf("run must end at the latest resolution %v, got %v", longEnd, merged[0].ResolvedAt)
	}
}

// Production showed four solid red days next to an uptime of 99.94% and an
// empty incident list: a single failed sample out of ~1440 was painting the
// whole day as an outage. A blip never reaches max_attempts either, so it opens
// no incident — the bar has to agree with the two numbers around it.
func TestStatusPage_ABlipIsNotAnOutage(t *testing.T) {
	days := []string{"2026-07-26"}

	bars, uptime := componentHistory(map[string]dayCounts{
		"2026-07-26": {failures: 1, total: 1440},
	}, days, 60)

	if bars[0].Status != StatusDegraded {
		t.Errorf("one failure in 1440 samples must read as degraded, got %q", bars[0].Status)
	}
	if uptime < 99.9 {
		t.Errorf("uptime = %v, expected ~99.93", uptime)
	}
}

func TestStatusPage_SustainedFailureIsAnOutage(t *testing.T) {
	days := []string{"2026-07-26"}

	bars, _ := componentHistory(map[string]dayCounts{
		"2026-07-26": {failures: 200, total: 1440},
	}, days, 60)

	if bars[0].Status != StatusOutage {
		t.Errorf("14%% of the day failing must read as an outage, got %q", bars[0].Status)
	}
}

// A warning is latency above a threshold: the service answered, so the bar stays
// green. Colouring it amber told visitors something was wrong on a day when
// nothing had failed.
func TestStatusPage_WarningsStayGreen(t *testing.T) {
	bars, uptime := componentHistory(map[string]dayCounts{
		"2026-07-26": {failures: 0, total: 1440},
	}, []string{"2026-07-26"}, 60)

	if bars[0].Status != StatusOperational {
		t.Errorf("a day without failures must stay operational, got %q", bars[0].Status)
	}
	if bars[0].DowntimeMinutes != 0 {
		t.Errorf("no failure means no downtime, got %d", bars[0].DowntimeMinutes)
	}
	if uptime != 100 {
		t.Errorf("uptime = %v, want 100", uptime)
	}
}

// Only failures produce a duration, and only they colour a bar.
func TestStatusPage_OnlyFailuresColourAndTimeADay(t *testing.T) {
	bars, _ := componentHistory(map[string]dayCounts{
		"2026-07-26": {failures: 4, total: 1440},
		"2026-07-27": {total: 1440},
	}, []string{"2026-07-26", "2026-07-27"}, 60)

	if bars[0].Status != StatusDegraded || bars[0].DowntimeMinutes != 4 {
		t.Errorf("a failing day must be degraded with its downtime, got %+v", bars[0])
	}
	if bars[1].Status != StatusOperational || bars[1].DowntimeMinutes != 0 {
		t.Errorf("a clean day must be operational with no downtime, got %+v", bars[1])
	}
}
