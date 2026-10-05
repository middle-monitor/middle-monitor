package workers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// StartAlertEvaluator runs every minute, evaluates all enabled alert rules,
// and fires incidents + notifications when thresholds are breached.
func StartAlertEvaluator(db *sql.DB, opensearch *services.OpenSearchService) {
	go func() {
		// Wait a moment so the service worker starts first
		time.Sleep(10 * time.Second)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			evaluateAlertRules(db, opensearch)
			evaluateServiceThresholds(db)
			<-ticker.C
		}
	}()
	slog.Info("alert evaluator started", "interval_seconds", 60)
}

func evaluateAlertRules(db *sql.DB, opensearch *services.OpenSearchService) {
	alertSvc := services.NewAlertService(db)
	maintSvc := services.NewMaintenanceService(db)

	rules, err := alertSvc.GetEnabledAlertRules()
	if err != nil {
		slog.Error("failed to fetch alert rules", "error", err)
		return
	}

	for _, rule := range rules {
		value, err := queryMetric(db, opensearch, rule)
		if err != nil {
			slog.Error("metric query failed", "rule_id", rule.ID, "rule", rule.Name, "error", err)
			continue
		}
		if value == nil {
			// No data in the window — nothing to evaluate
			continue
		}

		sev := ruleSeverityFor(rule, *value)
		slog.Debug("rule evaluated", "rule_id", rule.ID, "rule", rule.Name,
			"aggregation", rule.Aggregation, "metric", ruleMetricLabel(rule), "value", *value, "severity", sev)

		already, err := alertSvc.HasLiveIncidentForRule(rule.ID)
		if err != nil {
			slog.Error("live incident lookup failed", "rule_id", rule.ID, "error", err)
			continue
		}

		if already {
			// Recovery uses hysteresis: only resolve once the value crosses back past
			// the recovery threshold (or, lacking one, falls out of all severities).
			if ruleRecovered(rule, *value) {
				autoResolveIfCleared(db, alertSvc, rule)
			} else if sev == "critical" {
				escalateIfWarning(db, alertSvc, rule, *value)
			}
			continue
		}

		if sev == "" {
			// Not breaching and nothing open — nothing to do.
			continue
		}

		// Downtime windows + per-org severity opt-in: skip firing when suppressed.
		var ruleServiceID, ruleHostID *int64
		if rule.TargetID != nil {
			if rule.TargetType == "service" {
				ruleServiceID = rule.TargetID
			} else if rule.TargetType == "host" {
				ruleHostID = rule.TargetID
			}
		}
		if suppressed, reason := maintSvc.SuppressAlert(rule.OrganizationID, sev, ruleServiceID, ruleHostID); suppressed {
			slog.Info("alert suppressed", "rule_id", rule.ID, "rule", rule.Name, "severity", sev, "reason", reason)
			continue
		}

		// Fire: create incident + notify at the breached severity.
		ruleID := rule.ID
		desc := fmt.Sprintf("Alert rule '%s': %s(%s) = %.2f %s threshold (window: %ds)",
			rule.Name, rule.Aggregation, ruleMetricLabel(rule), *value, sev, rule.Duration)
		// Append the impacted service/host + a deep link when the rule is scoped.
		if ruleServiceID != nil {
			desc += services.ServiceAlertContext(db, *ruleServiceID)
		} else if ruleHostID != nil {
			desc += services.HostAlertContext(db, *ruleHostID)
		}
		incident, err := alertSvc.CreateIncident(models.Incident{
			OrganizationID: rule.OrganizationID,
			AlertRuleID:    &ruleID,
			ServiceID:      ruleServiceID,
			HostID:         ruleHostID,
			Title:          rule.Name,
			Description:    &desc,
			Severity:       sev,
		})
		if err != nil {
			slog.Error("failed to create incident", "rule_id", rule.ID, "error", err)
			continue
		}
		slog.Info("incident created", "rule_id", rule.ID, "incident_id", incident.ID, "severity", sev)

		// Send to channels configured on the rule
		channels, err := alertSvc.GetChannelsByIDs(rule.Channels, rule.OrganizationID)
		if err != nil {
			slog.Error("failed to fetch channels", "rule_id", rule.ID, "error", err)
			continue
		}
		// Fallback: all org channels if rule has none configured
		if len(channels) == 0 {
			channels, _ = alertSvc.GetChannels(rule.OrganizationID)
		}
		alias := fmt.Sprintf("mm-rule-%d", rule.ID)
		event := ruleEvent(services.EventIncidentOpened, db, rule, *incident, "", value)
		event.Title = fmt.Sprintf("[%s] %s", sev, rule.Name)
		event.Message = desc
		event.DedupKey = alias
		for _, ch := range channels {
			if !ch.Enabled {
				continue
			}
			if err := services.SendNotificationEvent(ch, event, nil); err != nil {
				slog.Error("channel send failed", "rule_id", rule.ID, "channel", ch.Name, "error", err)
			} else {
				slog.Info("channel notified", "rule_id", rule.ID, "channel", ch.Name)
			}
		}
	}
}

// ruleSeverityFor returns the breached severity ("critical" wins over "warning"),
// or "" when within limits. Uses the two-level thresholds when present, otherwise
// falls back to the legacy single threshold+severity.
func ruleSeverityFor(rule models.AlertRule, value float64) string {
	if rule.CriticalThreshold == nil && rule.WarningThreshold == nil {
		if evalOperator(rule.Operator, value, rule.Threshold) {
			if rule.Severity != "" {
				return rule.Severity
			}
			return "critical"
		}
		return ""
	}
	if rule.CriticalThreshold != nil && evalOperator(rule.Operator, value, *rule.CriticalThreshold) {
		return "critical"
	}
	if rule.WarningThreshold != nil && evalOperator(rule.Operator, value, *rule.WarningThreshold) {
		return "warning"
	}
	return ""
}

// ruleRecovered reports whether an open incident for this rule should resolve.
// With a recovery threshold it applies hysteresis (value must cross back past it);
// otherwise it resolves as soon as no severity is breached.
func ruleRecovered(rule models.AlertRule, value float64) bool {
	if rule.RecoveryThreshold != nil {
		// e.g. operator gt, trigger 90, recovery 80 → recovered when NOT (value > 80).
		return !evalOperator(rule.Operator, value, *rule.RecoveryThreshold)
	}
	return ruleSeverityFor(rule, value) == ""
}

// autoResolveIfCleared resolves the open incident for a rule when the condition is no longer breached,
// and notifies channels that the incident is resolved.
func autoResolveIfCleared(db *sql.DB, alertSvc *services.AlertService, rule models.AlertRule) {
	var incidentID int64
	var incidentTitle string
	err := db.QueryRow(
		`SELECT id, title FROM incidents WHERE alert_rule_id = $1 AND status IN `+services.LiveIncidentStatuses+` LIMIT 1`, rule.ID,
	).Scan(&incidentID, &incidentTitle)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		slog.Error("auto-resolve lookup failed", "rule_id", rule.ID, "error", err)
		return
	}

	_, err = db.Exec(`UPDATE incidents SET status='resolved', resolved_at=NOW() WHERE id=$1 AND status IN `+services.LiveIncidentStatuses, incidentID)
	if err != nil {
		slog.Error("auto-resolve failed", "rule_id", rule.ID, "error", err)
		return
	}
	slog.Info("incident auto-resolved, condition cleared", "rule_id", rule.ID, "incident_id", incidentID)

	// Notify channels that the incident is resolved
	channels, err := alertSvc.GetChannelsByIDs(rule.Channels, rule.OrganizationID)
	if err != nil || len(channels) == 0 {
		channels, _ = alertSvc.GetChannels(rule.OrganizationID)
	}
	subject := fmt.Sprintf("[RESOLVED] %s", incidentTitle)
	body := fmt.Sprintf("Incident #%d '%s' auto-resolved: the condition is no longer breached.", incidentID, incidentTitle)
	alias := fmt.Sprintf("mm-rule-%d", rule.ID)
	resolvedAt := time.Now().UTC()
	resolved := models.Incident{
		ID: incidentID, OrganizationID: rule.OrganizationID, AlertRuleID: &rule.ID,
		Title: incidentTitle, Status: "resolved", Severity: rule.Severity, ResolvedAt: &resolvedAt,
	}
	event := ruleEvent(services.EventIncidentResolved, db, rule, resolved, "open", nil)
	event.Title = subject
	event.Message = body
	event.DedupKey = alias
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		// JSM closes the alert by alias; the other channels get the typed event.
		if ch.Type == "jsm" {
			if err := services.ResolveNotification(ch, alias, subject, body); err != nil {
				slog.Error("resolve notification failed", "rule_id", rule.ID, "channel", ch.Name, "error", err)
			}
			continue
		}
		if err := services.SendNotificationEvent(ch, event, nil); err != nil {
			slog.Error("resolve notification failed", "rule_id", rule.ID, "channel", ch.Name, "error", err)
		} else {
			slog.Info("resolve notification sent", "rule_id", rule.ID, "channel", ch.Name)
		}
	}
}

// queryMetric returns the current aggregated metric value for the rule's window,
// or nil if there is no data.
func queryMetric(db *sql.DB, opensearch *services.OpenSearchService, rule models.AlertRule) (*float64, error) {
	window := fmt.Sprintf("%d seconds", rule.Duration)

	// A custom metric lives in OpenSearch, not in service_results, so it is
	// answered before the built-in switch and never falls through to it.
	if rule.CustomMetric != nil && *rule.CustomMetric != "" {
		return queryCustomMetric(opensearch, rule)
	}

	switch rule.Metric {
	case "cpu", "ram", "disk":
		return queryAgentMetric(db, rule, window)

	case "latency":
		return queryLatency(db, rule, window)

	case "error_count":
		return queryErrorCount(db, rule, window)

	case "failure_rate":
		return queryFailureRate(db, rule, window)

	default:
		// Unknown metric — skip silently
		return nil, nil
	}
}

// ruleMetricLabel names the signal a rule watches, so a custom rule does not
// log the built-in metric column it never reads.
func ruleMetricLabel(rule models.AlertRule) string {
	if rule.CustomMetric != nil && *rule.CustomMetric != "" {
		return *rule.CustomMetric
	}
	return rule.Metric
}

// queryCustomMetric aggregates a custom metric series over the rule's window.
// count is the only statistic the built-in rules do not offer; everything else
// maps to the same names on both sides.
func queryCustomMetric(opensearch *services.OpenSearchService, rule models.AlertRule) (*float64, error) {
	if opensearch == nil {
		return nil, nil
	}

	filters := make([]services.SeriesFilter, 0, len(rule.CustomLabels))
	for _, label := range rule.CustomLabels {
		filters = append(filters, services.SeriesFilter{Key: label.Key, Value: label.Value})
	}

	end := time.Now().UTC()
	aggregation := rule.Aggregation
	if aggregation == "" {
		aggregation = "avg"
	}

	// The evaluator runs on its own tick, so there is no request to inherit a
	// deadline from; the rule's window is the only bound that makes sense.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return opensearch.AggregateSeries(ctx, services.SeriesQuery{
		OrganizationID: rule.OrganizationID,
		MetricName:     *rule.CustomMetric,
		Filters:        filters,
		Aggregation:    aggregation,
		Start:          end.Add(-time.Duration(rule.Duration) * time.Second),
		End:            end,
	})
}

// aggregateExpr maps a policy aggregation (avg/min/max/sum/pXX) to a SQL expression
// over the given column. Percentiles use Postgres' percentile_cont.
func aggregateExpr(aggregation, column string) string {
	switch aggregation {
	case "min":
		return "MIN(" + column + ")"
	case "max":
		return "MAX(" + column + ")"
	case "sum":
		return "SUM(" + column + ")"
	case "p50", "p75", "p90", "p95", "p99":
		frac := map[string]string{"p50": "0.5", "p75": "0.75", "p90": "0.90", "p95": "0.95", "p99": "0.99"}[aggregation]
		return "percentile_cont(" + frac + ") WITHIN GROUP (ORDER BY " + column + ")"
	default: // avg
		return "AVG(" + column + ")"
	}
}

// queryAgentMetric aggregates cpu/ram/disk metric_value from service_results using
// the rule's chosen statistic (avg/min/max/sum/percentile).
func queryAgentMetric(db *sql.DB, rule models.AlertRule, window string) (*float64, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM service_results sr
		JOIN services s ON sr.service_id = s.id
		WHERE sr.metric_type = $1
		  AND sr.metric_value IS NOT NULL
		  AND sr.timestamp > NOW() - INTERVAL '%s'
		  AND s.organization_id = $2`, aggregateExpr(rule.Aggregation, "sr.metric_value"), window)

	args := []interface{}{rule.Metric, rule.OrganizationID}

	if rule.TargetID != nil {
		if rule.TargetType == "host" {
			query += " AND s.host_id = $3"
		} else {
			query += " AND s.id = $3"
		}
		args = append(args, *rule.TargetID)
	}

	var val sql.NullFloat64
	if err := db.QueryRow(query, args...).Scan(&val); err != nil {
		return nil, err
	}
	if !val.Valid {
		return nil, nil
	}
	return &val.Float64, nil
}

// queryLatency aggregates latency from service_results (non-agent results) using
// the rule's chosen statistic — including percentiles (p95/p99) for latency SLOs.
func queryLatency(db *sql.DB, rule models.AlertRule, window string) (*float64, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM service_results sr
		JOIN services s ON sr.service_id = s.id
		WHERE sr.latency IS NOT NULL
		  AND sr.metric_type IS NULL
		  AND sr.timestamp > NOW() - INTERVAL '%s'
		  AND s.organization_id = $1`, aggregateExpr(rule.Aggregation, "sr.latency"), window)

	args := []interface{}{rule.OrganizationID}

	if rule.TargetID != nil {
		query += " AND s.id = $2"
		args = append(args, *rule.TargetID)
	}

	var val sql.NullFloat64
	if err := db.QueryRow(query, args...).Scan(&val); err != nil {
		return nil, err
	}
	if !val.Valid {
		return nil, nil
	}
	return &val.Float64, nil
}

// queryErrorCount counts application_errors in the window.
func queryErrorCount(db *sql.DB, rule models.AlertRule, window string) (*float64, error) {
	query := fmt.Sprintf(`
		SELECT COUNT(*)::float
		FROM application_errors
		WHERE timestamp > NOW() - INTERVAL '%s'
		  AND organization_id = $1`, window)

	var val float64
	if err := db.QueryRow(query, rule.OrganizationID).Scan(&val); err != nil {
		return nil, err
	}
	return &val, nil
}

// queryFailureRate returns percentage of failures in the window for the target.
func queryFailureRate(db *sql.DB, rule models.AlertRule, window string) (*float64, error) {
	query := fmt.Sprintf(`
		SELECT COUNT(*) FILTER (WHERE sr.status = 'failure')::float * 100.0 / NULLIF(COUNT(*), 0)
		FROM service_results sr
		JOIN services s ON sr.service_id = s.id
		WHERE sr.metric_type IS NULL
		  AND sr.timestamp > NOW() - INTERVAL '%s'
		  AND s.organization_id = $1`, window)

	args := []interface{}{rule.OrganizationID}

	if rule.TargetID != nil {
		query += " AND s.id = $2"
		args = append(args, *rule.TargetID)
	}

	var val sql.NullFloat64
	if err := db.QueryRow(query, args...).Scan(&val); err != nil {
		return nil, err
	}
	if !val.Valid {
		return nil, nil
	}
	return &val.Float64, nil
}

func evalOperator(op string, value, threshold float64) bool {
	switch op {
	case "gt":
		return value > threshold
	case "gte":
		return value >= threshold
	case "lt":
		return value < threshold
	case "lte":
		return value <= threshold
	case "eq":
		return value == threshold
	default:
		return false
	}
}

// ruleEvent builds the typed event of one rule firing or resolving. The webhook
// receiver needs the rule, the value that breached it and the impacted target;
// none of that can be recovered from the rendered message.
func ruleEvent(eventType string, db *sql.DB, rule models.AlertRule, incident models.Incident, previousStatus string, observed *float64) services.NotificationEvent {
	event := services.IncidentEvent(eventType, incident, previousStatus, "", incidentURL(db, incident))
	event.AlertRule = &services.EventAlertRule{
		ID:            rule.ID,
		Name:          rule.Name,
		Metric:        ruleMetricLabel(rule),
		Aggregation:   rule.Aggregation,
		Operator:      rule.Operator,
		Threshold:     ruleThresholdFor(rule, incident.Severity),
		ObservedValue: observed,
		Duration:      rule.Duration,
	}
	if len(rule.CustomLabels) > 0 {
		labels := map[string]string{}
		for _, label := range rule.CustomLabels {
			labels[label.Key] = label.Value
		}
		event.Labels = labels
	}
	services.FillEventTargets(db, &event)
	return event
}

// ruleThresholdFor reports the threshold that was actually crossed, not the
// legacy single value, so observed_value and threshold can be compared.
func ruleThresholdFor(rule models.AlertRule, severity string) float64 {
	if severity == "critical" && rule.CriticalThreshold != nil {
		return *rule.CriticalThreshold
	}
	if severity == "warning" && rule.WarningThreshold != nil {
		return *rule.WarningThreshold
	}
	return rule.Threshold
}

func incidentURL(db *sql.DB, incident models.Incident) string {
	return services.IncidentURL(db, incident.OrganizationID, incident.ID)
}

// escalateIfWarning raises an open warning incident to critical and publishes
// the transition. Without it a receiver that acted on a warning never learns
// the situation got worse.
func escalateIfWarning(db *sql.DB, alertSvc *services.AlertService, rule models.AlertRule, value float64) {
	var incidentID int64
	var title string
	err := db.QueryRow(
		`SELECT id, title FROM incidents
		  WHERE alert_rule_id = $1 AND status IN `+services.LiveIncidentStatuses+` AND severity = 'warning'
		  LIMIT 1`, rule.ID).Scan(&incidentID, &title)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		slog.Error("escalation lookup failed", "rule_id", rule.ID, "error", err)
		return
	}

	if _, err := db.Exec(`UPDATE incidents SET severity='critical' WHERE id=$1`, incidentID); err != nil {
		slog.Error("escalation failed", "rule_id", rule.ID, "error", err)
		return
	}
	slog.Info("incident escalated to critical", "rule_id", rule.ID, "incident_id", incidentID)

	channels, err := alertSvc.GetChannelsByIDs(rule.Channels, rule.OrganizationID)
	if err != nil || len(channels) == 0 {
		channels, _ = alertSvc.GetChannels(rule.OrganizationID)
	}
	escalated := models.Incident{
		ID: incidentID, OrganizationID: rule.OrganizationID, AlertRuleID: &rule.ID,
		Title: title, Status: "open", Severity: "critical",
	}
	event := ruleEvent(services.EventIncidentEscalated, db, rule, escalated, "open", &value)
	event.Title = fmt.Sprintf("[critical] %s", rule.Name)
	event.Message = fmt.Sprintf("Incident #%d '%s' escalated from warning to critical (value: %.2f).", incidentID, title, value)
	event.DedupKey = fmt.Sprintf("mm-rule-%d", rule.ID)
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		if err := services.SendNotificationEvent(ch, event, nil); err != nil {
			slog.Error("escalation notification failed", "rule_id", rule.ID, "channel", ch.Name, "error", err)
		}
	}
}
