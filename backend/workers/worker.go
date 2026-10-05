package workers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

type serviceTimer struct {
	ticker      *time.Ticker
	interval    int
	maxAttempts int
	configHash  string
}

// serviceConfigFingerprint returns a stable string that changes whenever any
// field affecting how a service is checked changes. The worker goroutine captures
// a snapshot of the service config, so its timer must be restarted (and the
// snapshot refreshed) when this fingerprint differs.
func serviceConfigFingerprint(s models.Service) string {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	f := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'f', -1, 64)
	}
	code := ""
	if s.ExpectedStatusCode != nil {
		code = strconv.Itoa(*s.ExpectedStatusCode)
	}
	hostID := ""
	if s.HostID != nil {
		hostID = strconv.FormatInt(*s.HostID, 10)
	}
	return strings.Join([]string{
		strconv.FormatInt(s.OrganizationID, 10),
		hostID,
		s.Name,
		s.Type,
		s.Host,
		str(s.Path),
		str(s.Credentials),
		s.Service,
		strconv.Itoa(s.ServiceInterval),
		strconv.Itoa(s.MaxAttempts),
		f(s.FailureThreshold),
		f(s.WarningThreshold),
		f(s.CriticalThreshold),
		code,
		str(s.ExpectedBodyContains),
		str(s.ExpectedBodyMode),
	}, "\x1f")
}

var activeTimers = make(map[int64]*serviceTimer)
var timersMutex sync.RWMutex

// StartServiceWorker starts a background worker that executes services periodically.
// opensearch can be nil; when set, worker results are also indexed to OpenSearch for correlation.
func StartServiceWorker(db *sql.DB, opensearch *services.OpenSearchService) {
	go func() {
		for {
			startServiceTimers(db, opensearch)
			time.Sleep(30 * time.Second) // Re-check for new services every 30 seconds
		}
	}()
	slog.Info("service worker started")
}

func startServiceTimers(db *sql.DB, opensearch *services.OpenSearchService) {
	rows, err := db.Query(`SELECT id, organization_id, host_id, name, type, host, path, credentials, service, service_interval, max_attempts, failure_threshold, warning_threshold, critical_threshold, expected_status_code, expected_body_contains, expected_body_mode FROM services`)
	if err != nil {
		slog.Error("failed to fetch services", "error", err)
		return
	}
	defer rows.Close()

	activeServiceIDs := make(map[int64]bool)

	for rows.Next() {
		var s models.Service
		var failureThreshold, warningThreshold, criticalThreshold sql.NullFloat64
		var expectedStatusCode sql.NullInt64
		var expectedBodyContains sql.NullString
		var expectedBodyMode sql.NullString
		if err := rows.Scan(&s.ID, &s.OrganizationID, &s.HostID, &s.Name, &s.Type, &s.Host, &s.Path, &s.Credentials, &s.Service, &s.ServiceInterval, &s.MaxAttempts, &failureThreshold, &warningThreshold, &criticalThreshold, &expectedStatusCode, &expectedBodyContains, &expectedBodyMode); err != nil {
			slog.Error("failed to scan service row", "error", err)
			continue
		}
		if failureThreshold.Valid {
			s.FailureThreshold = &failureThreshold.Float64
		}
		if warningThreshold.Valid {
			s.WarningThreshold = &warningThreshold.Float64
		}
		if criticalThreshold.Valid {
			s.CriticalThreshold = &criticalThreshold.Float64
		}
		if expectedStatusCode.Valid {
			code := int(expectedStatusCode.Int64)
			s.ExpectedStatusCode = &code
		}
		if expectedBodyContains.Valid {
			s.ExpectedBodyContains = &expectedBodyContains.String
		}
		if expectedBodyMode.Valid {
			s.ExpectedBodyMode = &expectedBodyMode.String
		}

		// Skip agent services - they are passive (agent pushes data)
		if strings.HasPrefix(s.Type, "agent_") {
			continue
		}

		// Skip error services - they are passive (SDKs push data)
		if strings.HasPrefix(s.Type, "error_service_") {
			continue
		}

		activeServiceIDs[s.ID] = true

		// Set defaults if not set
		if s.ServiceInterval <= 0 {
			s.ServiceInterval = 60
		}
		if s.MaxAttempts <= 0 {
			s.MaxAttempts = 3
		}

		// Start or update timer for this service
		hash := serviceConfigFingerprint(s)
		timersMutex.Lock()
		existingTimer, exists := activeTimers[s.ID]

		// Restart the timer whenever any part of the service configuration changes
		// (URL/host, path, credentials, thresholds, interval, ...) so the goroutine
		// runs the check with up-to-date configuration instead of the snapshot it
		// captured when the timer was first created.
		if !exists || existingTimer.configHash != hash {
			// Stop existing timer if it exists
			if exists {
				existingTimer.ticker.Stop()
				slog.Info("stopping service timer, configuration changed", "service", s.Name, "service_id", s.ID)
			}

			// Create new timer with updated config
			interval := time.Duration(s.ServiceInterval) * time.Second
			ticker := time.NewTicker(interval)
			activeTimers[s.ID] = &serviceTimer{
				ticker:      ticker,
				interval:    s.ServiceInterval,
				maxAttempts: s.MaxAttempts,
				configHash:  hash,
			}
			timersMutex.Unlock()

			go func(service models.Service) {
				slog.Info("starting immediate check", "service", service.Name, "service_id", service.ID, "type", service.Type)
				executeService(db, service, opensearch)
				for range ticker.C {
					slog.Debug("executing scheduled check", "service", service.Name, "service_id", service.ID)
					executeService(db, service, opensearch)
				}
			}(s)

			if !exists {
				slog.Info("started service timer", "service", s.Name, "service_id", s.ID, "interval_s", s.ServiceInterval, "max_attempts", s.MaxAttempts)
			} else {
				slog.Info("restarted service timer", "service", s.Name, "service_id", s.ID, "interval_s", s.ServiceInterval, "max_attempts", s.MaxAttempts)
			}
		} else {
			timersMutex.Unlock()
		}
	}

	// Stop timers for services that no longer exist
	timersMutex.Lock()
	for serviceID, timer := range activeTimers {
		if !activeServiceIDs[serviceID] {
			timer.ticker.Stop()
			delete(activeTimers, serviceID)
			slog.Info("stopped service timer", "service_id", serviceID)
		}
	}
	timersMutex.Unlock()
}

// Delay before retry n+1; the last value applies to every further attempt.
var retryBackoff = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

// runCheckWithRetries retries a failing check up to max_attempts times within the
// same cycle and returns the first non-failure, or the last failure. A single
// result is therefore already worth max_attempts attempts: a restart between two
// attempts never surfaces as an error. Retries stop before the next scheduled run.
func runCheckWithRetries(db *sql.DB, service models.Service) models.ServiceResult {
	attempts := service.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	deadline := time.Now().Add(time.Duration(service.ServiceInterval) * time.Second)

	var result models.ServiceResult
	for i := range attempts {
		if i > 0 {
			backoff := retryBackoff[min(i-1, len(retryBackoff)-1)]
			if time.Now().Add(backoff).After(deadline) {
				break
			}
			time.Sleep(backoff)
			slog.Info("retrying check", "service", service.Name, "service_id", service.ID, "attempt", i+1, "max_attempts", attempts)
		}
		result = executeCheckOnce(db, service)
		if result.Status != "failure" {
			return result
		}
	}
	return result
}

func executeCheckOnce(db *sql.DB, service models.Service) models.ServiceResult {
	var result models.ServiceResult
	result.ServiceID = service.ID
	result.Timestamp = time.Now().UTC()

	switch service.Type {
	case "http":
		result = executeHTTPService(service)
	case "ping":
		result = executePingService(service)
	case "snmp":
		result = executeSNMPService(service)
	case "certificate":
		result = executeCertificateService(service)
	case "sql":
		result = executeSQLService(db, service)
	default:
		result.Status = "failure"
		msg := fmt.Sprintf("Unknown service type: %s", service.Type)
		result.Message = &msg
		slog.Warn("unknown service type", "type", service.Type, "service", service.Name, "service_id", service.ID)
	}
	return result
}

func executeService(db *sql.DB, service models.Service, opensearch *services.OpenSearchService) {
	slog.Debug("executing check", "service", service.Name, "service_id", service.ID, "type", service.Type)

	result := runCheckWithRetries(db, service)

	// Save result to database
	if err := saveServiceResult(db, result); err != nil {
		slog.Error("failed to save check result", "service", service.Name, "service_id", service.ID, "error", err)
	} else {
		latency := -1.0
		if result.Latency != nil {
			latency = *result.Latency
		}
		slog.Debug("saved check result", "service", service.Name, "service_id", service.ID, "status", result.Status, "latency", latency)
	}

	// Index to OpenSearch for alert correlation (fire-and-forget)
	if opensearch != nil {
		indexWorkerResultToOpenSearch(context.Background(), db, opensearch, service, result)
	}

	alertService := services.NewAlertService(db)

	if result.Status == "success" {
		// Service is healthy — auto-resolve any open incident so channels (including
		// JSM) are notified and the incident timeline is closed cleanly.
		autoResolveServiceCheckIncident(db, alertService, service)
		return
	}
	if result.Status != "failure" {
		// A warning-level threshold breach (e.g. a certificate approaching expiry):
		// the threshold evaluator owns this incident. A warning is not a full
		// recovery, so don't auto-resolve it, and don't run the critical path below.
		return
	}

	// The failure already survived max_attempts retries, so alert on it directly.
	// Check for an existing live incident so we don't fire duplicate notifications
	// on every subsequent check cycle while failing.
	already, err := alertService.HasLiveIncidentForServiceCheck(service.ID)
	if err != nil {
		slog.Error("live incident lookup failed", "service", service.Name, "error", err)
	}
	if already {
		slog.Debug("live incident already exists, skipping duplicate notification", "service", service.Name)
		return
	}

	eventMsg := fmt.Sprintf("Service '%s' failed %d consecutive attempts", service.Name, service.MaxAttempts)
	createEvent(db, models.Event{
		OrganizationID: service.OrganizationID,
		Type:           "error_spike",
		Service:        service.Service,
		Message:        eventMsg,
		Timestamp:      time.Now().UTC(),
	})

	// Skip alerting entirely if the service/host is in a downtime window.
	maintService := services.NewMaintenanceService(db)
	svcID := service.ID
	if down, _ := maintService.IsTargetUnderMaintenance(service.OrganizationID, &svcID, service.HostID); down {
		slog.Info("check failure suppressed by maintenance window", "service", service.Name)
		return
	}

	notifBody := eventMsg
	// The check's own failure reason (HTTP status, timeout, assertion) is the
	// first thing an on-call needs; without it the alert says only "it failed".
	if result.Message != nil && *result.Message != "" {
		notifBody += "\nError: " + *result.Message
	}
	notifBody += services.ServiceAlertContext(db, service.ID)

	incidentService := service.Service
	svcIDForIncident := service.ID
	_, err = alertService.CreateIncident(models.Incident{
		OrganizationID: service.OrganizationID,
		ServiceID:      &svcIDForIncident,
		Title:          fmt.Sprintf("Check Failure: %s", service.Name),
		Description:    &notifBody,
		Severity:       "critical",
		Service:        &incidentService,
	})
	if err != nil {
		slog.Error("failed to create incident", "service", service.Name, "error", err)
	}

	svcIDForRoute := service.ID
	services.RouteAlertTargeted(db, service.OrganizationID, "critical", fmt.Sprintf("[CRITICAL] Check failure: %s", service.Name), notifBody, fmt.Sprintf("mm-svc-%d", service.ID), &svcIDForRoute, service.HostID)
}

// autoResolveServiceCheckIncident closes any open incident for a service check and
// notifies channels when the check becomes healthy again.
func autoResolveServiceCheckIncident(db *sql.DB, alertSvc *services.AlertService, service models.Service) {
	var incidentID int64
	var title string
	err := db.QueryRow(
		`SELECT id, title FROM incidents WHERE service_id = $1 AND status IN `+services.LiveIncidentStatuses+` LIMIT 1`, service.ID,
	).Scan(&incidentID, &title)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		slog.Error("incident resolve lookup failed", "service", service.Name, "error", err)
		return
	}

	if _, err := db.Exec(
		`UPDATE incidents SET status='resolved', resolved_at=NOW() WHERE id=$1 AND status IN `+services.LiveIncidentStatuses, incidentID,
	); err != nil {
		slog.Error("incident resolve failed", "service", service.Name, "error", err)
		return
	}
	slog.Info("incident auto-resolved, check healthy again", "service", service.Name, "incident_id", incidentID)

	alias := fmt.Sprintf("mm-svc-%d", service.ID)
	subject := fmt.Sprintf("[RESOLVED] %s", title)
	body := fmt.Sprintf("Incident #%d '%s' auto-resolved: the check is healthy again.", incidentID, title)
	svcIDForRoute := service.ID
	services.RouteResolveTargeted(db, service.OrganizationID, alias, subject, body, &svcIDForRoute, service.HostID)
}

func createEvent(db *sql.DB, event models.Event) {
	eventService := services.NewEventService(db)
	_, err := eventService.CreateEvent(event)
	if err != nil {
		slog.Error("failed to create event", "error", err)
	}
}

func indexWorkerResultToOpenSearch(ctx context.Context, db *sql.DB, opensearch *services.OpenSearchService, service models.Service, result models.ServiceResult) {
	hostName := ""
	var hostID *int64
	if service.HostID != nil {
		hostID = service.HostID
		if err := db.QueryRowContext(ctx, `SELECT name FROM hosts WHERE id = $1`, *service.HostID).Scan(&hostName); err != nil {
			hostName = ""
		}
	}

	doc := map[string]interface{}{
		"@timestamp":      result.Timestamp.UTC().Format(time.RFC3339),
		"service_id":      service.ID,
		"service_name":    service.Name,
		"service_type":    service.Type,
		"organization_id": service.OrganizationID,
		"service":         service.Service,
		"status":          result.Status,
		"message":         nil,
		"metadata":        nil,
		"metric_type":     nil,
		"metric_value":    nil,
	}
	if hostID != nil {
		doc["host_id"] = *hostID
	}
	doc["host_name"] = hostName
	if result.Latency != nil {
		doc["latency"] = *result.Latency
	}
	if result.Message != nil {
		doc["message"] = *result.Message
	}
	if result.Metadata != nil {
		// The column stores JSON text but the index maps metadata as an object.
		// Sending the raw string makes OpenSearch reject the whole document, so
		// cert and http results never reached the index.
		decoded, err := decodeResultMetadata(*result.Metadata)
		if err != nil {
			slog.Warn("dropping unparseable metadata", "service", service.Name, "service_id", service.ID, "error", err)
		} else {
			doc["metadata"] = decoded
		}
	}
	if result.MetricType != nil {
		doc["metric_type"] = *result.MetricType
	}
	if result.MetricValue != nil {
		doc["metric_value"] = *result.MetricValue
	}

	if err := opensearch.IndexWorkerResult(ctx, doc); err != nil {
		slog.Error("failed to index result to opensearch", "service", service.Name, "service_id", service.ID, "error", err)
	} else {
		slog.Debug("indexed check result", "service_id", service.ID, "service", service.Name, "type", service.Type, "host", hostName, "status", result.Status)
	}
}

func saveServiceResult(db *sql.DB, result models.ServiceResult) error {
	// Build query based on whether metadata is present
	var query string
	var err error

	if result.Metadata != nil {
		query = `INSERT INTO service_results (service_id, status, latency, message, timestamp, metadata)
				  VALUES ($1, $2, $3, $4, $5, $6)`
		slog.Debug("saving result", "timestamp", result.Timestamp.UTC())
		_, err = db.Exec(query, result.ServiceID, result.Status, result.Latency, result.Message, result.Timestamp, result.Metadata)
	} else {
		query = `INSERT INTO service_results (service_id, status, latency, message, timestamp)
				  VALUES ($1, $2, $3, $4, $5)`
		slog.Debug("saving result", "timestamp", result.Timestamp.UTC())
		_, err = db.Exec(query, result.ServiceID, result.Status, result.Latency, result.Message, result.Timestamp)
	}

	if err != nil {
		slog.Error("failed to save service result", "service_id", result.ServiceID, "error", err)
		return err
	}
	return nil
}
