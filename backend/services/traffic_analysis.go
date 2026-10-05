package services

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

type TrafficAnalyzer struct {
	db         *sql.DB
	opensearch *OpenSearchService
}

func NewTrafficAnalyzer(db *sql.DB, opensearch *OpenSearchService) *TrafficAnalyzer {
	return &TrafficAnalyzer{db: db, opensearch: opensearch}
}

type TrafficAnalysis struct {
	HostName        string                 `json:"host_name,omitempty"`
	WindowMinutes   int                    `json:"window_minutes"`
	BaselineMinutes int                    `json:"baseline_minutes"`
	Signals         []TrafficAnomalySignal `json:"signals,omitempty"`
	Summary         []string               `json:"summary,omitempty"`
}

type TrafficAnomalySignal struct {
	Service           string  `json:"service"`
	TraceCount        int64   `json:"trace_count,omitempty"`
	BaselineTraceRate float64 `json:"baseline_trace_rate_per_min,omitempty"`
	CurrentTraceRate  float64 `json:"current_trace_rate_per_min,omitempty"`
	TraceMultiplier   float64 `json:"trace_multiplier,omitempty"`
	LogCount          int64   `json:"log_count,omitempty"`
	ErrorLogCount     int64   `json:"error_log_count,omitempty"`
	LogMultiplier     float64 `json:"log_multiplier,omitempty"`
	AvgLatencyMS      float64 `json:"avg_latency_ms,omitempty"`
	MaxLatencyMS      float64 `json:"max_latency_ms,omitempty"`
	Description       string  `json:"description"`
}

type incidentScope struct {
	HostID    int64
	HostName  string
	Timestamp time.Time
}

func (a *TrafficAnalyzer) AnalyzeForError(ctx context.Context, orgID, errorID int64) (*TrafficAnalysis, error) {
	if a == nil || a.opensearch == nil {
		return nil, nil
	}
	var appService string
	var ts time.Time
	if err := a.db.QueryRowContext(ctx, `
		SELECT service, timestamp
		FROM application_errors
		WHERE id = $1 AND organization_id = $2
	`, errorID, orgID).Scan(&appService, &ts); err != nil {
		return nil, err
	}
	scope := incidentScope{Timestamp: ts}
	_ = a.db.QueryRowContext(ctx, `
		SELECT id, name
		FROM hosts
		WHERE organization_id = $1 AND service = $2
		ORDER BY id ASC LIMIT 1
	`, orgID, appService).Scan(&scope.HostID, &scope.HostName)
	if scope.HostName == "" {
		_ = a.db.QueryRowContext(ctx, `
			SELECT h.id, h.name
			FROM application_links al
			JOIN hosts h ON h.id = al.target_id
			WHERE al.organization_id = $1
			  AND al.app_service_name = $2
			  AND al.target_type = 'host'
			ORDER BY al.id ASC LIMIT 1
		`, orgID, appService).Scan(&scope.HostID, &scope.HostName)
	}
	return a.analyze(ctx, scope, []string{appService})
}

func (a *TrafficAnalyzer) AnalyzeForServiceResult(ctx context.Context, orgID, resultID int64) (*TrafficAnalysis, error) {
	if a == nil || a.opensearch == nil {
		return nil, nil
	}
	var scope incidentScope
	var hostFallback, serviceName, serviceType sql.NullString
	if err := a.db.QueryRowContext(ctx, `
		SELECT COALESCE(h.id, 0), COALESCE(h.name, ''), s.host, s.service, s.type, sr.timestamp
		FROM service_results sr
		JOIN services s ON s.id = sr.service_id
		LEFT JOIN hosts h ON h.id = s.host_id
		WHERE sr.id = $1 AND s.organization_id = $2
	`, resultID, orgID).Scan(&scope.HostID, &scope.HostName, &hostFallback, &serviceName, &serviceType, &scope.Timestamp); err != nil {
		return nil, err
	}
	if scope.HostName == "" && hostFallback.Valid {
		scope.HostName = hostFallback.String
	}

	currentService := serviceName.String
	if strings.HasPrefix(serviceType.String, "agent_") {
		currentService = ""
	}
	services, err := a.servicesLinkedToScope(ctx, orgID, scope, currentService)
	if err != nil {
		return nil, err
	}
	return a.analyze(ctx, scope, services)
}

func (a *TrafficAnalyzer) servicesLinkedToScope(ctx context.Context, orgID int64, scope incidentScope, currentService string) ([]string, error) {
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
	}
	add(currentService)

	if scope.HostID > 0 {
		rows, err := a.db.QueryContext(ctx, `
			SELECT DISTINCT app_service_name
			FROM application_links
			WHERE organization_id = $1
			  AND target_type = 'host'
			  AND target_id = $2
		`, orgID, scope.HostID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				add(name)
			}
		}
		rows.Close()

		rows, err = a.db.QueryContext(ctx, `
			SELECT DISTINCT service
			FROM services
			WHERE organization_id = $1
			  AND host_id = $2
			  AND service <> ''
		`, orgID, scope.HostID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				add(name)
			}
		}
		rows.Close()
	}

	services := make([]string, 0, len(seen))
	for name := range seen {
		services = append(services, name)
	}
	return services, nil
}

func (a *TrafficAnalyzer) analyze(_ context.Context, scope incidentScope, linkedServices []string) (*TrafficAnalysis, error) {
	if scope.Timestamp.IsZero() {
		return nil, nil
	}
	currentStart := scope.Timestamp.Add(-incidentWindowBefore)
	currentEnd := scope.Timestamp.Add(incidentWindowAfter)
	baselineStart := currentStart.Add(-60 * time.Minute)
	baselineEnd := currentStart
	currentMinutes := currentEnd.Sub(currentStart).Minutes()
	baselineMinutes := baselineEnd.Sub(baselineStart).Minutes()
	hostFilter := scope.HostName
	if len(linkedServices) > 0 {
		// App links are already the explicit "this app runs on this host" signal.
		// Many SDKs do not set resource host.name, so filtering OpenSearch by
		// hostname would hide valid app logs/traces. When linked services exist,
		// prefer service_name filtering and keep host_name as context only.
		hostFilter = ""
	}

	currentTraces, err := a.opensearch.AggregateTracesByService(
		currentStart.UTC().Format(time.RFC3339),
		currentEnd.UTC().Format(time.RFC3339),
		hostFilter,
		linkedServices,
		10,
	)
	if err != nil {
		return nil, err
	}
	currentLogs, err := a.opensearch.AggregateLogsByService(
		currentStart.UTC().Format(time.RFC3339),
		currentEnd.UTC().Format(time.RFC3339),
		hostFilter,
		linkedServices,
		10,
	)
	if err != nil {
		return nil, err
	}

	serviceFilter := serviceNamesFromTraceStats(currentTraces)
	for _, name := range serviceNamesFromLogStats(currentLogs) {
		if !containsString(serviceFilter, name) {
			serviceFilter = append(serviceFilter, name)
		}
	}
	if len(serviceFilter) == 0 {
		serviceFilter = linkedServices
	}

	baselineTraces, _ := a.opensearch.AggregateTracesByService(
		baselineStart.UTC().Format(time.RFC3339),
		baselineEnd.UTC().Format(time.RFC3339),
		hostFilter,
		serviceFilter,
		10,
	)
	baselineLogs, _ := a.opensearch.AggregateLogsByService(
		baselineStart.UTC().Format(time.RFC3339),
		baselineEnd.UTC().Format(time.RFC3339),
		hostFilter,
		serviceFilter,
		10,
	)

	analysis := &TrafficAnalysis{
		HostName:        scope.HostName,
		WindowMinutes:   int(currentMinutes),
		BaselineMinutes: int(baselineMinutes),
	}
	traceBaseline := traceStatsByService(baselineTraces)
	logBaseline := logStatsByService(baselineLogs)
	logCurrent := logStatsByService(currentLogs)

	for _, trace := range currentTraces {
		currentRate := rate(trace.Count, currentMinutes)
		baseline := traceBaseline[trace.ServiceName]
		baselineRate := rate(baseline.Count, baselineMinutes)
		multiplier := rateMultiplier(currentRate, baselineRate)

		log := logCurrent[trace.ServiceName]
		baseLog := logBaseline[trace.ServiceName]
		logMultiplier := rateMultiplier(rate(log.Count, currentMinutes), rate(baseLog.Count, baselineMinutes))

		if !isSignificantTrafficSignal(trace.Count, multiplier, trace.AvgDuration, log.Count, logMultiplier, log.ErrorCount) {
			continue
		}
		signal := TrafficAnomalySignal{
			Service:           trace.ServiceName,
			TraceCount:        trace.Count,
			BaselineTraceRate: round1(baselineRate),
			CurrentTraceRate:  round1(currentRate),
			TraceMultiplier:   round1(multiplier),
			LogCount:          log.Count,
			ErrorLogCount:     log.ErrorCount,
			LogMultiplier:     round1(logMultiplier),
			AvgLatencyMS:      round1(trace.AvgDuration),
			MaxLatencyMS:      round1(trace.MaxDuration),
		}
		signal.Description = describeTrafficSignal(signal, currentMinutes)
		analysis.Signals = append(analysis.Signals, signal)
		analysis.Summary = append(analysis.Summary, signal.Description)
		if len(analysis.Signals) >= 3 {
			break
		}
	}
	for _, log := range currentLogs {
		if len(analysis.Signals) >= 3 || containsSignal(analysis.Signals, log.ServiceName) {
			continue
		}
		baseLog := logBaseline[log.ServiceName]
		logMultiplier := rateMultiplier(rate(log.Count, currentMinutes), rate(baseLog.Count, baselineMinutes))
		if !isSignificantTrafficSignal(0, 0, 0, log.Count, logMultiplier, log.ErrorCount) {
			continue
		}
		signal := TrafficAnomalySignal{
			Service:       log.ServiceName,
			LogCount:      log.Count,
			ErrorLogCount: log.ErrorCount,
			LogMultiplier: round1(logMultiplier),
		}
		signal.Description = describeTrafficSignal(signal, currentMinutes)
		analysis.Signals = append(analysis.Signals, signal)
		analysis.Summary = append(analysis.Summary, signal.Description)
	}

	if len(analysis.Signals) == 0 {
		return nil, nil
	}
	return analysis, nil
}

func isSignificantTrafficSignal(traceCount int64, traceMultiplier, avgLatency float64, logCount int64, logMultiplier float64, errorLogCount int64) bool {
	return (traceCount >= 50 && traceMultiplier >= 3) ||
		(traceCount >= 100 && avgLatency >= 1000) ||
		(logCount >= 50 && logMultiplier >= 3) ||
		errorLogCount >= 10
}

// Descriptions stay in English like every other service-layer signal; the api
// layer re-renders them per locale (see trafficSignalSummary).
func describeTrafficSignal(s TrafficAnomalySignal, windowMinutes float64) string {
	parts := []string{}
	if s.TraceCount > 0 {
		if s.TraceMultiplier > 0 {
			parts = append(parts, fmt.Sprintf("%s received %d requests/traces in %.0f min (%s)", s.Service, s.TraceCount, windowMinutes, baselineLabel(s.TraceMultiplier)))
		} else {
			parts = append(parts, fmt.Sprintf("%s received %d requests/traces in %.0f min with no recent baseline", s.Service, s.TraceCount, windowMinutes))
		}
	} else if s.LogCount > 0 {
		if s.LogMultiplier > 0 {
			parts = append(parts, fmt.Sprintf("%s produced %d logs/requests in %.0f min (%s)", s.Service, s.LogCount, windowMinutes, baselineLabel(s.LogMultiplier)))
		} else {
			parts = append(parts, fmt.Sprintf("%s produced %d logs/requests in %.0f min", s.Service, s.LogCount, windowMinutes))
		}
	}
	if s.AvgLatencyMS >= 1000 {
		parts = append(parts, fmt.Sprintf("average latency %.0f ms", s.AvgLatencyMS))
	}
	if s.LogCount > 0 && s.TraceCount > 0 {
		if s.LogMultiplier > 0 {
			parts = append(parts, fmt.Sprintf("%d logs (%s)", s.LogCount, baselineLabel(s.LogMultiplier)))
		} else {
			parts = append(parts, fmt.Sprintf("%d logs", s.LogCount))
		}
	}
	if s.ErrorLogCount > 0 {
		parts = append(parts, fmt.Sprintf("%d error logs", s.ErrorLogCount))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("abnormal traffic/logs on %s", s.Service)
	}
	return strings.Join(parts, ", ") + "."
}

func baselineLabel(multiplier float64) string {
	switch {
	case multiplier >= 100:
		return "massive spike vs near-zero usual activity"
	case multiplier >= 10:
		return fmt.Sprintf("%.0fx usual activity", multiplier)
	case multiplier >= 3:
		return fmt.Sprintf("%.1fx usual activity", multiplier)
	default:
		return "above usual activity"
	}
}

func serviceNamesFromTraceStats(stats []TraceServiceStats) []string {
	out := make([]string, 0, len(stats))
	for _, s := range stats {
		if s.ServiceName != "" {
			out = append(out, s.ServiceName)
		}
	}
	return out
}

func serviceNamesFromLogStats(stats []LogServiceStats) []string {
	out := make([]string, 0, len(stats))
	for _, s := range stats {
		if s.ServiceName != "" {
			out = append(out, s.ServiceName)
		}
	}
	return out
}

func traceStatsByService(stats []TraceServiceStats) map[string]TraceServiceStats {
	out := make(map[string]TraceServiceStats, len(stats))
	for _, s := range stats {
		out[s.ServiceName] = s
	}
	return out
}

func logStatsByService(stats []LogServiceStats) map[string]LogServiceStats {
	out := make(map[string]LogServiceStats, len(stats))
	for _, s := range stats {
		out[s.ServiceName] = s
	}
	return out
}

func rate(count int64, minutes float64) float64 {
	if count <= 0 || minutes <= 0 {
		return 0
	}
	return float64(count) / minutes
}

func rateMultiplier(currentRate, baselineRate float64) float64 {
	if currentRate <= 0 {
		return 0
	}
	if baselineRate <= 0 {
		return 0
	}
	return currentRate / baselineRate
}

func round1(v float64) float64 {
	if v == 0 {
		return 0
	}
	return math.Round(v*10) / 10
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func containsSignal(items []TrafficAnomalySignal, serviceName string) bool {
	for _, item := range items {
		if item.Service == serviceName {
			return true
		}
	}
	return false
}
