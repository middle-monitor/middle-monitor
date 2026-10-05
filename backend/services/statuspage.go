package services

import (
	"database/sql"
	"sort"
	"strings"
	"time"
)

// Component and overall status values exposed by the page. They are derived
// from check results, not stored. Only failures count as a disruption: a
// warning is latency above a threshold, so the service answered — worth an
// alert to the operator, not a coloured bar on a public availability page.
const (
	StatusOperational = "operational"
	StatusDegraded    = "degraded"
	StatusOutage      = "outage"
	StatusUnknown     = "unknown"
)

// maxStatusWindowDays caps the uptime history. Retention can go up to a year on
// the custom plan, which is unreadable as daily bars.
const maxStatusWindowDays = 90

// Share of a day's samples that must fail before the bar reads as an outage.
// Painting a whole day red on a single failed sample out of ~1440 contradicted
// both the uptime next to it (99.94%) and the empty incident list underneath:
// a blip does not reach max_attempts, so it never opens an incident either.
const dayOutageRatio = 0.05

type StatusDay struct {
	Date   string `json:"date"`
	Status string `json:"status"`
	// Minutes the check spent failing that day, derived from the failed sample
	// count and the check's own interval. It is what a visitor hovering a bar
	// wants to know, and it is the only duration we can state honestly: results
	// are samples, not a continuous signal.
	DowntimeMinutes int `json:"downtime_minutes,omitempty"`
}

type StatusComponent struct {
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Uptime float64     `json:"uptime"`
	Days   []StatusDay `json:"days"`
}

// Kinds of disruption the page reports. The stored incident title names the
// technical check ("Check Failure: api-health") and reads as noise to a visitor,
// so the API classifies instead and the page renders its own wording — which
// also keeps the public page translatable.
const (
	IncidentUnreachable  = "unreachable"
	IncidentCertExpiring = "cert_expiring"
	IncidentDegraded     = "degraded"
	IncidentDisruption   = "disruption"
)

type StatusIncident struct {
	Component  string     `json:"component"`
	Kind       string     `json:"kind"`
	Severity   string     `json:"severity"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type StatusMaintenance struct {
	Name     string    `json:"name"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

type StatusPage struct {
	Status      string              `json:"status"`
	WindowDays  int                 `json:"window_days"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Components  []StatusComponent   `json:"components"`
	Incidents   []StatusIncident    `json:"incidents"`
	Maintenance []StatusMaintenance `json:"maintenance"`
}

type StatusPageService struct {
	db *sql.DB
}

func NewStatusPageService(db *sql.DB) *StatusPageService {
	return &StatusPageService{db: db}
}

// Get builds the public status page of one organization. Everything it returns
// is derived from services flagged public: host, path, credentials and the
// internal check names of everything else never reach the response.
func (s *StatusPageService) Get(slug string) (*StatusPage, error) {
	var orgID int64
	var plan string
	var trialEndsAt sql.NullTime
	var customRetention sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id, COALESCE(plan, 'free'), trial_ends_at, custom_retention_days
		FROM organizations WHERE slug = $1
	`, slug).Scan(&orgID, &plan, &trialEndsAt, &customRetention)
	if err == sql.ErrNoRows {
		return nil, ErrStatusPageNotFound
	}
	if err != nil {
		return nil, err
	}

	windowDays := statusWindowDays(EffectivePlan(plan, nullTimeToPtr(trialEndsAt)), int(customRetention.Int64))

	components, checks, err := s.components(orgID)
	if err != nil {
		return nil, err
	}
	// An org with no published check has no page: better a 404 than an empty
	// "all systems operational" that claims nothing is wrong.
	if len(components) == 0 {
		return nil, ErrStatusPageNotFound
	}

	history, err := s.history(orgID, windowDays)
	if err != nil {
		return nil, err
	}
	days := windowDates(windowDays)
	for i := range components {
		components[i].Days, components[i].Uptime = componentHistory(history[checks[i].id], days, checks[i].intervalSeconds)
	}

	incidents, err := s.incidents(orgID, windowDays)
	if err != nil {
		return nil, err
	}
	maintenance, err := s.maintenance(orgID)
	if err != nil {
		return nil, err
	}

	return &StatusPage{
		Status:      overallStatus(components),
		WindowDays:  windowDays,
		UpdatedAt:   time.Now().UTC(),
		Components:  components,
		Incidents:   incidents,
		Maintenance: maintenance,
	}, nil
}

// publishedCheck is a published service and the interval its results are
// sampled at, which turns a failed sample count into a duration.
type publishedCheck struct {
	id              int64
	intervalSeconds int
}

// components lists the published checks with their current status. Agent host
// metrics and SDK error services are excluded: they describe internals, not a
// user-facing endpoint. The returned checks match the components by index.
func (s *StatusPageService) components(orgID int64) ([]StatusComponent, []publishedCheck, error) {
	rows, err := s.db.Query(`
		SELECT s.id, COALESCE(NULLIF(s.display_name, ''), s.name), s.service_interval, lr.status
		FROM services s
		LEFT JOIN LATERAL (
			SELECT status FROM service_results
			WHERE service_id = s.id ORDER BY timestamp DESC LIMIT 1
		) lr ON true
		WHERE s.organization_id = $1 AND s.public
		  AND s.type NOT LIKE 'agent_%' AND s.type NOT LIKE 'error_service_%'
		ORDER BY COALESCE(NULLIF(s.display_name, ''), s.name)
	`, orgID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var components []StatusComponent
	var checks []publishedCheck
	for rows.Next() {
		var check publishedCheck
		var name string
		var status sql.NullString
		if err := rows.Scan(&check.id, &name, &check.intervalSeconds, &status); err != nil {
			return nil, nil, err
		}
		components = append(components, StatusComponent{Name: name, Status: statusFromResult(status)})
		checks = append(checks, check)
	}
	return components, checks, rows.Err()
}

type dayCounts struct {
	failures int
	total    int
}

// history buckets every published check's results per day over the window.
func (s *StatusPageService) history(orgID int64, windowDays int) (map[int64]map[string]dayCounts, error) {
	rows, err := s.db.Query(`
		SELECT sr.service_id,
		       to_char(date_trunc('day', sr.timestamp), 'YYYY-MM-DD'),
		       count(*) FILTER (WHERE sr.status = 'failure'),
		       count(*)
		FROM service_results sr
		JOIN services s ON s.id = sr.service_id
		WHERE s.organization_id = $1 AND s.public
		  AND sr.timestamp >= NOW() - make_interval(days => $2)
		GROUP BY 1, 2
	`, orgID, windowDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]map[string]dayCounts{}
	for rows.Next() {
		var id int64
		var day string
		var c dayCounts
		if err := rows.Scan(&id, &day, &c.failures, &c.total); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]dayCounts{}
		}
		out[id][day] = c
	}
	return out, rows.Err()
}

// incidents returns what happened on the published checks over the window. Only
// the component name, a kind and the timing are exposed: titles name internal
// checks and descriptions carry thresholds and internal context.
func (s *StatusPageService) incidents(orgID int64, windowDays int) ([]StatusIncident, error) {
	rows, err := s.db.Query(`
		SELECT i.service_id, COALESCE(NULLIF(s.display_name, ''), s.name),
		       i.title, s.type, i.severity, i.started_at, i.resolved_at
		FROM incidents i
		JOIN services s ON s.id = i.service_id
		WHERE i.organization_id = $1 AND s.public
		  AND (i.resolved_at IS NULL OR i.resolved_at >= NOW() - make_interval(days => $2))
		ORDER BY i.service_id, i.started_at
		LIMIT 1000
	`, orgID, windowDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var raw []rawIncident
	for rows.Next() {
		var in rawIncident
		var title, serviceType string
		var resolvedAt sql.NullTime
		if err := rows.Scan(&in.serviceID, &in.Component, &title, &serviceType, &in.Severity, &in.StartedAt, &resolvedAt); err != nil {
			return nil, err
		}
		in.Kind = incidentKind(title, serviceType)
		in.ResolvedAt = nullTimeToPtr(resolvedAt)
		raw = append(raw, in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return mergeIncidents(raw), nil
}

// incidentKind classifies a stored incident from the two titles this codebase
// produces: workers/worker.go writes "Check Failure: <check>" when a probe does
// not answer, and workers/service_threshold_evaluator.go writes "Certificate
// expiring soon: <check>" or "<metric> threshold on <check>" when a value
// crosses a threshold. Anything else degrades to a neutral disruption rather
// than guessing.
func incidentKind(title, serviceType string) string {
	switch {
	case strings.HasPrefix(title, "Check Failure:"):
		// A certificate check that fails outright is not "expiring soon": the
		// handshake itself did not complete.
		if serviceType == "certificate" {
			return IncidentDisruption
		}
		return IncidentUnreachable
	case strings.HasPrefix(title, "Certificate expiring soon:"):
		return IncidentCertExpiring
	case strings.Contains(title, " threshold on "):
		return IncidentDegraded
	default:
		return IncidentDisruption
	}
}

type rawIncident struct {
	serviceID int64
	StatusIncident
}

// A flapping check opens one incident per cycle: the evaluator resolves it as
// soon as the next sample succeeds, so a bad hour becomes forty one-minute rows.
// Consecutive incidents on the same component are merged into the outage a
// reader would recognise, rather than listing every flap.
const incidentMergeGap = 30 * time.Minute

func mergeIncidents(raw []rawIncident) []StatusIncident {
	merged := []StatusIncident{}
	for _, in := range raw {
		last := len(merged) - 1
		if last >= 0 && merged[last].Component == in.Component &&
			merged[last].ResolvedAt != nil &&
			in.StartedAt.Sub(*merged[last].ResolvedAt) <= incidentMergeGap {
			// The run ends at the latest end, not at the last row's: incidents are
			// ordered by start, and one opened earlier can resolve later, which
			// would otherwise move the end backwards ("from 20:15 to 20:14").
			if in.ResolvedAt == nil || in.ResolvedAt.After(*merged[last].ResolvedAt) {
				merged[last].ResolvedAt = in.ResolvedAt
			}
			// An unresolved flap makes the whole run ongoing, and critical wins
			// over warning: the merged line must not read calmer than reality.
			if in.Severity == "critical" {
				merged[last].Severity = in.Severity
			}
			continue
		}
		merged = append(merged, in.StatusIncident)
	}

	// Newest first, and only the recent past: a status page is not an archive.
	sort.Slice(merged, func(i, j int) bool { return merged[i].StartedAt.After(merged[j].StartedAt) })
	if len(merged) > maxStatusIncidents {
		merged = merged[:maxStatusIncidents]
	}
	return merged
}

const maxStatusIncidents = 20

// maintenance returns the windows still running or planned that target a
// published check, or the host one runs on.
func (s *StatusPageService) maintenance(orgID int64) ([]StatusMaintenance, error) {
	rows, err := s.db.Query(`
		SELECT m.name, m.starts_at, m.ends_at
		FROM maintenance_windows m
		WHERE m.organization_id = $1 AND m.ends_at >= NOW()
		  AND (
		    (m.target_type = 'service' AND EXISTS (
		       SELECT 1 FROM services s WHERE s.id = m.target_id AND s.organization_id = m.organization_id AND s.public))
		    OR (m.target_type = 'host' AND EXISTS (
		       SELECT 1 FROM services s WHERE s.host_id = m.target_id AND s.organization_id = m.organization_id AND s.public))
		  )
		ORDER BY m.starts_at
		LIMIT 20
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	windows := []StatusMaintenance{}
	for rows.Next() {
		var m StatusMaintenance
		if err := rows.Scan(&m.Name, &m.StartsAt, &m.EndsAt); err != nil {
			return nil, err
		}
		windows = append(windows, m)
	}
	return windows, rows.Err()
}

// statusWindowDays is the plan's retention, capped for readability. Showing more
// days than the data is kept for would render a wall of "unknown".
func statusWindowDays(plan string, customRetentionDays int) int {
	days := RetentionDays(plan, customRetentionDays)
	if days < 0 || days > maxStatusWindowDays {
		return maxStatusWindowDays
	}
	return days
}

func windowDates(windowDays int) []string {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	days := make([]string, 0, windowDays)
	for i := windowDays - 1; i >= 0; i-- {
		days = append(days, today.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return days
}

// componentHistory turns daily buckets into one bar per day plus the uptime
// ratio. Days without a sample stay unknown and are left out of the ratio, so a
// check created yesterday does not report 3% uptime over 90 days.
func componentHistory(buckets map[string]dayCounts, days []string, intervalSeconds int) ([]StatusDay, float64) {
	out := make([]StatusDay, 0, len(days))
	var ok, total int
	for _, day := range days {
		c, seen := buckets[day]
		if !seen || c.total == 0 {
			out = append(out, StatusDay{Date: day, Status: StatusUnknown})
			continue
		}

		bar := StatusDay{Date: day, DowntimeMinutes: downtimeMinutes(c.failures, intervalSeconds)}
		switch {
		case float64(c.failures)/float64(c.total) >= dayOutageRatio:
			bar.Status = StatusOutage
		case c.failures > 0:
			bar.Status = StatusDegraded
		default:
			bar.Status = StatusOperational
		}
		out = append(out, bar)
		ok += c.total - c.failures
		total += c.total
	}
	if total == 0 {
		return out, 0
	}
	return out, float64(ok) / float64(total) * 100
}

// downtimeMinutes turns failed samples into the time the check was down. Each
// failed sample stands for one interval; a check probed every 60s that failed
// 22 times was down about 22 minutes. Rounded up so a single failure on a slow
// interval never reads as zero.
func downtimeMinutes(failures, intervalSeconds int) int {
	if failures == 0 {
		return 0
	}
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}
	seconds := failures * intervalSeconds
	if seconds%60 == 0 {
		return seconds / 60
	}
	return seconds/60 + 1
}

func statusFromResult(status sql.NullString) string {
	if !status.Valid {
		return StatusUnknown
	}
	// A warning means slow, not down: the component stays green.
	if status.String == "failure" {
		return StatusOutage
	}
	return StatusOperational
}

// overallStatus is the worst component status: one outage makes the banner red.
func overallStatus(components []StatusComponent) string {
	worst := StatusOperational
	for _, c := range components {
		switch c.Status {
		case StatusOutage:
			return StatusOutage
		case StatusDegraded:
			worst = StatusDegraded
		}
	}
	return worst
}

func nullTimeToPtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
