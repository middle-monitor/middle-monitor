package workers

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

type heartbeatState struct {
	service models.Service
	last    models.ServiceResult
}

// evaluateHeartbeats turns silence into a failure. Each period that passes
// without a ping records one failure, so a job that keeps missing is alerted on
// every missed run, not once forever. A failure the job reported itself (its
// fail URL) is alerted on when it is newer than since, the previous pass.
func evaluateHeartbeats(db *sql.DB, since, now time.Time) {
	beats, err := loadHeartbeats(db)
	if err != nil {
		slog.Error("failed to load heartbeats", "error", err)
		return
	}

	alertSvc := services.NewAlertService(db)
	for _, hb := range beats {
		if missed := heartbeatMissed(hb.service, hb.last.Timestamp, now); missed != nil {
			if err := saveServiceResult(db, *missed); err != nil {
				continue
			}
			alertCheckFailure(db, alertSvc, hb.service, *missed, fmt.Sprintf("Heartbeat '%s' missed its expected ping", hb.service.Name))
			continue
		}
		switch hb.last.Status {
		case "success":
			autoResolveServiceCheckIncident(db, alertSvc, hb.service)
		case "failure":
			// A ping written between two passes can carry a timestamp just before
			// the previous pass read; the margin catches it, the live-incident
			// dedup absorbs the overlap.
			if hb.last.Timestamp.After(since.Add(-time.Minute)) {
				alertCheckFailure(db, alertSvc, hb.service, hb.last, fmt.Sprintf("Heartbeat '%s' reported a failure", hb.service.Name))
			}
		}
	}
}

// loadHeartbeats returns each heartbeat with its latest result. One that never
// pinged is left out: a job nobody wired yet must not page anyone.
func loadHeartbeats(db *sql.DB) ([]heartbeatState, error) {
	rows, err := db.Query(`
		SELECT s.id, s.organization_id, s.host_id, s.name, s.service, s.service_interval, s.credentials,
		       r.status, r.message, r.timestamp
		  FROM services s
		  JOIN LATERAL (
		        SELECT status, message, timestamp FROM service_results
		         WHERE service_id = s.id ORDER BY timestamp DESC LIMIT 1
		       ) r ON true
		 WHERE s.type = $1`, services.HeartbeatType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var beats []heartbeatState
	for rows.Next() {
		var hb heartbeatState
		s := &hb.service
		if err := rows.Scan(&s.ID, &s.OrganizationID, &s.HostID, &s.Name, &s.Service, &s.ServiceInterval, &s.Credentials,
			&hb.last.Status, &hb.last.Message, &hb.last.Timestamp); err != nil {
			return nil, err
		}
		s.Type = services.HeartbeatType
		hb.last.ServiceID = s.ID
		beats = append(beats, hb)
	}
	return beats, rows.Err()
}

// heartbeatMissed returns the failure to record once the latest result is older
// than the period plus grace, or nil while the job is on time.
func heartbeatMissed(service models.Service, last, now time.Time) *models.ServiceResult {
	// The API validates the config on write; a bad stored one falls back to the default grace.
	cfg, _ := services.ParseHeartbeatConfig(service.Credentials)
	period := time.Duration(service.ServiceInterval) * time.Second
	grace := time.Duration(cfg.GraceSeconds) * time.Second
	if !now.After(last.Add(period + grace)) {
		return nil
	}
	msg := fmt.Sprintf("No ping since %s (expected every %s, grace %s)", last.UTC().Format(time.RFC3339), period, grace)
	return &models.ServiceResult{ServiceID: service.ID, Status: "failure", Message: &msg, Timestamp: now}
}
