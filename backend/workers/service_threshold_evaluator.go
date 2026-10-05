package workers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"middle-monitor/backend/services"
)

// thresholdCheck is a check that carries warning/critical thresholds.
type thresholdCheck struct {
	id          int64
	orgID       int64
	hostID      *int64
	name        string
	svcType     string
	service     string
	warning     *float64
	critical    *float64
	maxAttempts int
}

// evaluateServiceThresholds drives alerts directly from a check's own warning /
// critical thresholds (the ones edited in the service modal). For agent checks it
// reads the latest cpu/ram/disk sample; for the rest it reads the latest latency.
// It mirrors the alert-rule evaluator: fire once, auto-resolve when cleared, and
// honor downtime windows + per-org severity opt-in.
func evaluateServiceThresholds(db *sql.DB) {
	rows, err := db.Query(`
		SELECT id, organization_id, host_id, name, type, service, warning_threshold, critical_threshold, max_attempts
		FROM services
		WHERE (warning_threshold IS NOT NULL OR critical_threshold IS NOT NULL)
		  AND type NOT LIKE 'error_service_%'`)
	if err != nil {
		slog.Error("failed to fetch checks", "error", err)
		return
	}
	defer rows.Close()

	var checks []thresholdCheck
	for rows.Next() {
		var c thresholdCheck
		var hostID sql.NullInt64
		var warn, crit sql.NullFloat64
		if err := rows.Scan(&c.id, &c.orgID, &hostID, &c.name, &c.svcType, &c.service, &warn, &crit, &c.maxAttempts); err != nil {
			slog.Error("failed to scan check", "error", err)
			continue
		}
		if hostID.Valid {
			c.hostID = &hostID.Int64
		}
		if warn.Valid {
			c.warning = &warn.Float64
		}
		if crit.Valid {
			c.critical = &crit.Float64
		}
		if c.maxAttempts <= 0 {
			c.maxAttempts = 3
		}
		checks = append(checks, c)
	}

	alertSvc := services.NewAlertService(db)
	maint := services.NewMaintenanceService(db)

	for _, c := range checks {
		severity, threshold, value := evaluateCheckThreshold(db, c)
		if value == nil {
			continue // no recent data
		}
		if severity == "" {
			autoResolveThresholdIncident(db, alertSvc, c)
			continue
		}

		// Downtime + per-org severity opt-in.
		serviceID := c.id
		if suppressed, reason := maint.SuppressAlert(c.orgID, severity, &serviceID, c.hostID); suppressed {
			slog.Info("alert suppressed", "check_id", c.id, "check", c.name, "severity", severity, "reason", reason)
			continue
		}

		// One live incident per check.
		var existingID int64
		err := db.QueryRow(
			`SELECT id FROM incidents WHERE organization_id = $1 AND service_id = $2 AND status IN `+services.LiveIncidentStatuses+` LIMIT 1`,
			c.orgID, c.id,
		).Scan(&existingID)
		if err == nil {
			continue // already alerting
		} else if err != sql.ErrNoRows {
			slog.Error("dedup lookup failed", "check_id", c.id, "error", err)
			continue
		}

		var title, desc string
		if c.svcType == "certificate" {
			title = fmt.Sprintf("Certificate expiring soon: %s", checkDisplayName(c))
			if *value < 0 {
				desc = fmt.Sprintf("Certificate has expired (%s threshold: %d days)", severity, int(threshold))
			} else {
				desc = fmt.Sprintf("Certificate expires in %d days (%s threshold: %d days)", int(*value), severity, int(threshold))
			}
		} else {
			label, unit := metricLabelUnit(c.svcType)
			title = fmt.Sprintf("%s threshold on %s", label, checkDisplayName(c))
			desc = fmt.Sprintf("%s reached %.2f%s (%s threshold: %.2f%s)", label, *value, unit, severity, threshold, unit)
		}
		desc += services.ServiceAlertContext(db, c.id)

		svc := c.service
		var incID int64
		err = db.QueryRow(
			`INSERT INTO incidents (organization_id, alert_rule_id, title, description, severity, status, service, service_id)
			 VALUES ($1, NULL, $2, $3, $4, 'open', $5, $6) RETURNING id`,
			c.orgID, title, desc, severity, svc, c.id,
		).Scan(&incID)
		if err != nil {
			slog.Error("failed to create incident", "check_id", c.id, "error", err)
			continue
		}
		slog.Info("incident created", "check_id", c.id, "check", c.name, "incident_id", incID, "severity", severity)

		alias := fmt.Sprintf("mm-svc-%d", c.id)
		svcIDForRoute := c.id
		services.RouteAlertTargeted(db, c.orgID, severity, fmt.Sprintf("[%s] %s", strings.ToUpper(severity), title), desc, alias, &svcIDForRoute, c.hostID)
	}
}

// autoResolveThresholdIncident closes and notifies an open threshold incident when
// the value falls back below both thresholds.
func autoResolveThresholdIncident(db *sql.DB, alertSvc *services.AlertService, c thresholdCheck) {
	// A failing check can still report a healthy-looking value (fast HTTP 500,
	// stale cert metadata). Its open incident belongs to the service worker,
	// which resolves it on the first successful check — never resolve it here.
	var lastStatus string
	err := db.QueryRow(
		`SELECT status FROM service_results WHERE service_id = $1 ORDER BY timestamp DESC LIMIT 1`, c.id,
	).Scan(&lastStatus)
	if err == nil && lastStatus == "failure" {
		return
	}

	var incidentID int64
	var title string
	err = db.QueryRow(
		`SELECT id, title FROM incidents WHERE organization_id = $1 AND service_id = $2 AND status IN `+services.LiveIncidentStatuses+` LIMIT 1`,
		c.orgID, c.id,
	).Scan(&incidentID, &title)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		slog.Error("resolve lookup failed", "check_id", c.id, "error", err)
		return
	}

	if _, err := db.Exec(
		`UPDATE incidents SET status='resolved', resolved_at=NOW() WHERE id=$1 AND status IN `+services.LiveIncidentStatuses, incidentID,
	); err != nil {
		slog.Error("resolve failed", "check_id", c.id, "error", err)
		return
	}
	slog.Info("incident auto-resolved", "check_id", c.id, "incident_id", incidentID)

	alias := fmt.Sprintf("mm-svc-%d", c.id)
	subject := fmt.Sprintf("[RESOLVED] %s", title)
	body := fmt.Sprintf("Incident #%d '%s' auto-resolved: the value is back within thresholds.", incidentID, title)
	svcIDForRoute := c.id
	services.RouteResolveTargeted(db, c.orgID, alias, subject, body, &svcIDForRoute, c.hostID)
}

// evaluateCheckThreshold classifies a check against its thresholds. Certificate
// checks stay instant (days-remaining does not flap); every other check only
// breaches once the last max_attempts samples ALL cross a threshold. Retrying
// cannot smooth a slow-but-successful sample the way it smooths a failure, so
// thresholds keep their own sample window.
// The returned value is the latest sample (nil when there is no recent data).
func evaluateCheckThreshold(db *sql.DB, c thresholdCheck) (string, float64, *float64) {
	if c.svcType == "certificate" {
		value := currentCertDaysRemaining(db, c)
		if value == nil {
			return "", 0, nil
		}
		severity, threshold := classifyCertThreshold(c, *value)
		return severity, threshold, value
	}

	values := recentCheckValues(db, c)
	if len(values) == 0 {
		return "", 0, nil
	}
	latest := &values[0]
	if len(values) < c.maxAttempts {
		return "", 0, latest
	}
	severity, threshold := classifyThresholdWindow(c, values)
	return severity, threshold, latest
}

// recentCheckValues returns the last max_attempts samples (newest first) for a
// non-certificate check: metric_value for agent metrics, latency otherwise.
// Returns nil when the newest sample is older than 5 minutes (stale data).
func recentCheckValues(db *sql.DB, c thresholdCheck) []float64 {
	var rows *sql.Rows
	var err error
	if metricType := agentMetricType(c.svcType); metricType != "" {
		rows, err = db.Query(`
			SELECT metric_value, timestamp FROM service_results
			WHERE service_id = $1 AND metric_type = $2 AND metric_value IS NOT NULL
			ORDER BY timestamp DESC LIMIT $3`, c.id, metricType, c.maxAttempts)
	} else {
		rows, err = db.Query(`
			SELECT latency, timestamp FROM service_results
			WHERE service_id = $1 AND latency IS NOT NULL
			ORDER BY timestamp DESC LIMIT $2`, c.id, c.maxAttempts)
	}
	if err != nil {
		slog.Error("values lookup failed", "check_id", c.id, "error", err)
		return nil
	}
	defer rows.Close()

	var values []float64
	var newest time.Time
	for rows.Next() {
		var v float64
		var ts time.Time
		if err := rows.Scan(&v, &ts); err != nil {
			return nil
		}
		if len(values) == 0 {
			newest = ts
		}
		values = append(values, v)
	}
	if len(values) == 0 || time.Since(newest) > 5*time.Minute {
		return nil
	}
	return values
}

// classifyThresholdWindow returns the breached severity ("critical" beats
// "warning") only when EVERY sample in the window crosses that threshold.
func classifyThresholdWindow(c thresholdCheck, values []float64) (string, float64) {
	allOver := func(threshold float64) bool {
		for _, v := range values {
			if v <= threshold {
				return false
			}
		}
		return true
	}
	if c.critical != nil && allOver(*c.critical) {
		return "critical", *c.critical
	}
	if c.warning != nil && allOver(*c.warning) {
		return "warning", *c.warning
	}
	return "", 0
}

// currentCertDaysRemaining reads the latest certificate check and returns the number
// of days until expiry (negative once expired). Certificate checks store no
// latency/metric_value — only an expires_at timestamp in metadata — so the value and
// the threshold comparison both differ from latency/utilization checks.
func currentCertDaysRemaining(db *sql.DB, c thresholdCheck) *float64 {
	var metadata sql.NullString
	err := db.QueryRow(`
		SELECT metadata FROM service_results
		WHERE service_id = $1 AND metadata IS NOT NULL
		  AND timestamp > NOW() - INTERVAL '24 hours'
		ORDER BY timestamp DESC LIMIT 1`, c.id).Scan(&metadata)
	if err != nil || !metadata.Valid {
		return nil
	}
	var meta struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(metadata.String), &meta); err != nil || meta.ExpiresAt == "" {
		return nil
	}
	expiresAt, err := time.Parse(time.RFC3339, meta.ExpiresAt)
	if err != nil {
		return nil
	}
	days := time.Until(expiresAt).Hours() / 24
	return &days
}

// classifyCertThreshold is inverted: fewer days remaining than the threshold is worse.
func classifyCertThreshold(c thresholdCheck, days float64) (string, float64) {
	if c.critical != nil && days < *c.critical {
		return "critical", *c.critical
	}
	if c.warning != nil && days < *c.warning {
		return "warning", *c.warning
	}
	return "", 0
}

func agentMetricType(svcType string) string {
	switch svcType {
	case "agent_cpu":
		return "cpu"
	case "agent_ram":
		return "ram"
	case "agent_disk":
		return "disk"
	default:
		return ""
	}
}

func metricLabelUnit(svcType string) (label, unit string) {
	switch svcType {
	case "agent_cpu":
		return "CPU", "%"
	case "agent_ram":
		return "RAM", "%"
	case "agent_disk":
		return "Disk", "%"
	default:
		return "Latency", "ms"
	}
}

// checkDisplayName is just the check/service name (e.g. "cpu"). The host and
// app-service are surfaced separately in the alert body via ServiceAlertContext,
// so we don't prefix with the app-service name (which produced "default/cpu").
func checkDisplayName(c thresholdCheck) string {
	return c.name
}
