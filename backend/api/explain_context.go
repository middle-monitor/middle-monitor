package api

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// slimError is the minimal payload we send to the LLM for an application
// error. We strip http_headers, http_body (can be huge), organization_id,
// etc. — the LLM doesn't need them to produce a TL;DR.
type slimError struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Message    string    `json:"message"`
	File       string    `json:"file,omitempty"`
	Line       int       `json:"line,omitempty"`
	Service    string    `json:"service"`
	Timestamp  time.Time `json:"timestamp"`
	HTTPMethod string    `json:"http_method,omitempty"`
	HTTPURL    string    `json:"http_url,omitempty"`
}

// slimServiceResult is the minimal payload we send to the LLM for a failing
// service check. We drop metric_type / metric_value / metadata which the LLM
// rarely needs for a TL;DR.
type slimServiceResult struct {
	ID          int64     `json:"id"`
	Service     string    `json:"service"`
	Status      string    `json:"status"`
	CheckType   string    `json:"check_type,omitempty"`   // e.g. "http", "tcp", "icmp"
	CheckTarget string    `json:"check_target,omitempty"` // host/url being probed
	Message     string    `json:"message,omitempty"`
	LatencyMS   *int      `json:"latency_ms,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// slimCorrelation collapses CorrelationService output into the one field that
// is actually useful for the LLM: the human-readable `Summary` plus a count of
// neighbour failures (for intuition) and the 3 most salient infra signals.
type slimCorrelation struct {
	Summary            string   `json:"summary,omitempty"`
	HasCorrelation     bool     `json:"has_correlation"`
	Confidence         float64  `json:"confidence,omitempty"`
	FailingNeighbours  []string `json:"failing_neighbours,omitempty"`
	UpstreamSuspects   []string `json:"upstream_suspects,omitempty"`   // neighbours that failed BEFORE the incident
	UpstreamApps       []string `json:"upstream_apps,omitempty"`       // declared dependencies that errored BEFORE the incident
	DegradedNeighbours []string `json:"degraded_neighbours,omitempty"` // same host group, slower but still passing
	InfraSignals       []string `json:"infra_signals,omitempty"`
	SimilarErrorsCount int      `json:"similar_errors_count,omitempty"`
	Recurrence         string   `json:"recurrence,omitempty"`
	RecurrenceIsNew    bool     `json:"recurrence_is_new,omitempty"`

	// Strength of each family, carried from the correlation service where it is
	// derived from the data (how far past its threshold a metric went, whether a
	// neighbour broke first). Internal: the LLM ranks on the candidates, not on
	// these. Zero means "not measured" and falls back to the family's base rate.
	infraStrength       float64
	upstreamStrength    float64
	neighbourStrength   float64
	upstreamAppStrength float64
	degradedStrength    float64
}

type slimEvent struct {
	Type      string    `json:"type"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

func trimCorrelation(c *models.CorrelationResult) *slimCorrelation {
	if c == nil {
		return nil
	}
	out := &slimCorrelation{
		Summary:            c.Summary,
		HasCorrelation:     c.HasCorrelation,
		Confidence:         c.Confidence,
		SimilarErrorsCount: len(c.SemanticMatches),
	}
	for i, s := range c.Services {
		if i >= 3 {
			break
		}
		out.FailingNeighbours = append(out.FailingNeighbours, s.ServiceName)
		out.neighbourStrength = math.Max(out.neighbourStrength, s.Confidence)
		if s.PrecededIncident {
			out.UpstreamSuspects = append(out.UpstreamSuspects, s.ServiceName)
			out.upstreamStrength = math.Max(out.upstreamStrength, s.Confidence)
		}
	}
	for i, d := range c.Degraded {
		if i >= 3 {
			break
		}
		label := d.ServiceName
		if d.HostName != "" {
			label += " (" + d.HostName + ")"
		}
		out.DegradedNeighbours = append(out.DegradedNeighbours,
			fmt.Sprintf("%s %.0fms vs %.0fms baseline (x%.1f)", label, d.LatencyMS, d.BaselineMS, d.Multiplier))
		out.degradedStrength = math.Max(out.degradedStrength, degradedStrength(d.Multiplier))
	}
	// Only declared dependencies that errored BEFORE the subject are a cause; a
	// dependent erroring after is downstream impact and would mislead the ranking.
	for _, a := range c.Apps {
		if len(out.UpstreamApps) >= 3 {
			break
		}
		if a.Relation == "dependency" && a.PrecededIncident {
			out.UpstreamApps = append(out.UpstreamApps, fmt.Sprintf("%s (%s, x%d)", a.Service, a.ErrorName, a.Count))
			out.upstreamAppStrength = math.Max(out.upstreamAppStrength, a.Confidence)
		}
	}
	for i, sig := range c.Infra {
		if i >= 3 {
			break
		}
		out.InfraSignals = append(out.InfraSignals, sig.Description)
		out.infraStrength = math.Max(out.infraStrength, sig.Confidence)
	}
	if c.Recurrence != nil {
		out.Recurrence = c.Recurrence.Description
		out.RecurrenceIsNew = c.Recurrence.IsNew
	}
	return out
}

// buildErrorContext returns the minimal JSON-serializable payload describing
// an application error for the LLM prompt.
func buildErrorContext(ctx context.Context, db *sql.DB, correlationSvc *services.CorrelationService, trafficAnalyzer *services.TrafficAnalyzer, orgID, errorID int64) (map[string]any, error) {
	var e models.ApplicationError
	var traceID sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT id, organization_id, name, message, file, line, timestamp, service,
		       http_method, http_url, http_headers, http_body, trace_id
		FROM application_errors
		WHERE id = $1 AND organization_id = $2
	`, errorID, orgID).Scan(
		&e.ID, &e.OrganizationID, &e.Name, &e.Message, &e.File, &e.Line, &e.Timestamp, &e.Service,
		&e.HTTPMethod, &e.HTTPURL, &e.HTTPHeaders, &e.HTTPBody, &traceID,
	)
	if err == sql.ErrNoRows {
		return nil, &RecordNotFoundError{Kind: "error", ID: errorID, OrgID: orgID}
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrErrorFetch, err)
	}
	if traceID.Valid {
		e.TraceID = &traceID.String
	}

	slim := slimError{
		ID:        e.ID,
		Name:      e.Name,
		Message:   truncate(e.Message, 500),
		File:      basename(e.File),
		Line:      e.Line,
		Service:   e.Service,
		Timestamp: e.Timestamp,
	}
	if e.HTTPMethod != nil {
		slim.HTTPMethod = *e.HTTPMethod
	}
	if e.HTTPURL != nil {
		slim.HTTPURL = *e.HTTPURL
	}

	correlation, cErr := correlationSvc.GetCorrelationForError(orgID, errorID)
	if cErr != nil {
		correlation = nil
	}

	events := fetchRecentEvents(ctx, db, orgID, e.Service, e.Timestamp, 15)
	traffic := analyzeErrorTraffic(ctx, trafficAnalyzer, orgID, errorID)

	out := map[string]any{
		"error":             slim,
		"human_hint":        knownErrorHint(slim, "fr"),
		"correlation":       trimCorrelation(correlation),
		"recent_events":     events,
		"traffic_anomalies": traffic,
	}
	// If the error carries a distributed trace, surface the failing span chain so
	// the LLM can point at the exact service/operation that broke.
	if e.TraceID != nil && *e.TraceID != "" {
		out["trace_id"] = *e.TraceID
		if trace := fetchTraceSummary(orgID, *e.TraceID); trace != nil {
			out["trace"] = trace
		}
	}
	return out, nil
}

// fetchTraceSummary pulls the failing span chain for a trace from OpenSearch.
// Best-effort: returns nil when OpenSearch is disabled or the trace is empty.
func fetchTraceSummary(orgID int64, traceID string) *services.TraceSummary {
	if opensearch == nil {
		return nil
	}
	trace, err := opensearch.GetTraceByID(orgID, traceID, 100)
	if err != nil {
		slog.Error("trace lookup failed", "trace_id", traceID, "error", err)
		return nil
	}
	return trace
}

// buildServiceResultContext does the same for a failing service_result.
func buildServiceResultContext(ctx context.Context, db *sql.DB, correlationSvc *services.CorrelationService, trafficAnalyzer *services.TrafficAnalyzer, orgID, resultID int64) (map[string]any, error) {
	var (
		id        int64
		serviceID int64
		status    string
		latency   sql.NullFloat64
		message   sql.NullString
		ts        time.Time
		svc       string
		svcType   string
		host      string
		hostID    sql.NullInt64
	)
	err := db.QueryRowContext(ctx, `
		SELECT sr.id, sr.service_id, sr.status, sr.latency, sr.message, sr.timestamp,
		       s.service, s.type, s.host, s.host_id
		FROM service_results sr
		JOIN services s ON s.id = sr.service_id
		WHERE sr.id = $1 AND s.organization_id = $2
	`, resultID, orgID).Scan(&id, &serviceID, &status, &latency, &message, &ts, &svc, &svcType, &host, &hostID)
	if err == sql.ErrNoRows {
		return nil, &RecordNotFoundError{Kind: "service_result", ID: resultID, OrgID: orgID}
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServiceResultFetch, err)
	}

	slim := slimServiceResult{
		ID:          id,
		Service:     svc,
		Status:      status,
		Timestamp:   ts,
		CheckType:   svcType,
		CheckTarget: host,
	}
	if message.Valid {
		slim.Message = truncate(message.String, 300)
	}
	if latency.Valid {
		n := int(latency.Float64)
		slim.LatencyMS = &n
	}

	correlation, cErr := correlationSvc.GetCorrelationForServiceResult(orgID, resultID)
	if cErr != nil {
		correlation = nil
	}

	events := fetchRecentEvents(ctx, db, orgID, svc, ts, 15)

	// Enumerate the OTHER checks configured on the same host (or, if the service
	// is not bound to a host, on the same target). This lets the LLM reason
	// about coverage gaps ("only HTTP is checked, no TCP port, no ICMP").
	otherChecks := fetchSiblingChecks(ctx, db, orgID, serviceID, hostID, host)
	traffic := analyzeServiceResultTraffic(ctx, trafficAnalyzer, orgID, resultID)

	return map[string]any{
		"service_result":       slim,
		"correlation":          trimCorrelation(correlation),
		"recent_events":        events,
		"other_checks_on_host": otherChecks,
		"traffic_anomalies":    traffic,
	}, nil
}

type slimCheck struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Host string `json:"host,omitempty"`
}

func fetchSiblingChecks(ctx context.Context, db *sql.DB, orgID, excludeServiceID int64, hostID sql.NullInt64, hostTarget string) []slimCheck {
	var (
		rows *sql.Rows
		err  error
	)
	if hostID.Valid {
		rows, err = db.QueryContext(ctx, `
			SELECT name, type, host FROM services
			WHERE organization_id = $1 AND host_id = $2 AND id <> $3
			  AND type NOT LIKE 'agent_%'
			ORDER BY name
			LIMIT 10
		`, orgID, hostID.Int64, excludeServiceID)
	} else if hostTarget != "" {
		rows, err = db.QueryContext(ctx, `
			SELECT name, type, host FROM services
			WHERE organization_id = $1 AND host = $2 AND id <> $3
			  AND type NOT LIKE 'agent_%'
			ORDER BY name
			LIMIT 10
		`, orgID, hostTarget, excludeServiceID)
	} else {
		return nil
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]slimCheck, 0, 10)
	for rows.Next() {
		var c slimCheck
		if scanErr := rows.Scan(&c.Name, &c.Type, &c.Host); scanErr != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

func fetchRecentEvents(ctx context.Context, db *sql.DB, orgID int64, service string, around time.Time, windowMin int) []slimEvent {
	if windowMin <= 0 {
		windowMin = 15
	}
	start := around.Add(-time.Duration(windowMin) * time.Minute)
	end := around.Add(time.Duration(windowMin) * time.Minute)

	rows, err := db.QueryContext(ctx, `
		SELECT type, COALESCE(message, ''), timestamp
		FROM events
		WHERE organization_id = $1 AND service = $2
		  AND timestamp BETWEEN $3 AND $4
		ORDER BY timestamp DESC
		LIMIT 5
	`, orgID, service, start, end)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]slimEvent, 0, 5)
	for rows.Next() {
		var e slimEvent
		var msg string
		if scanErr := rows.Scan(&e.Type, &msg, &e.Timestamp); scanErr != nil {
			continue
		}
		e.Message = truncate(msg, 120)
		out = append(out, e)
	}
	return out
}
