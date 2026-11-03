package services

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// HeartbeatType is a passive check: a cron job or worker calls its ping URL, and
// silence past the expected period is the failure.
const HeartbeatType = "heartbeat"

const (
	DefaultHeartbeatGrace = 300
	maxHeartbeatGrace     = 86400
	// A failing job often pipes its output to the fail URL; keep the alert readable.
	maxHeartbeatMessage = 1000
)

// HeartbeatConfig is what a heartbeat stores in services.credentials.
type HeartbeatConfig struct {
	GraceSeconds int `json:"grace_seconds"`
}

// ParseHeartbeatConfig reads a heartbeat's config; an empty one gets the default grace.
func ParseHeartbeatConfig(credentials *string) (HeartbeatConfig, error) {
	cfg := HeartbeatConfig{GraceSeconds: DefaultHeartbeatGrace}
	if credentials == nil || strings.TrimSpace(*credentials) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(*credentials), &cfg); err != nil {
		return cfg, ErrHeartbeatConfigInvalid
	}
	if cfg.GraceSeconds < 0 || cfg.GraceSeconds > maxHeartbeatGrace {
		return cfg, ErrHeartbeatGraceInvalid
	}
	return cfg, nil
}

// RecordHeartbeatPing stores a ping as a check result. The worker reads the
// latest result to open or resolve the incident, so this never alerts itself.
func RecordHeartbeatPing(db *sql.DB, token string, failed bool, message string) error {
	var serviceID int64
	err := db.QueryRow(`SELECT id FROM services WHERE token = $1 AND type = $2`, token, HeartbeatType).Scan(&serviceID)
	if err == sql.ErrNoRows {
		return ErrHeartbeatNotFound
	}
	if err != nil {
		return err
	}

	status := "success"
	msg := "Ping received"
	if failed {
		status = "failure"
		msg = "The job reported a failure"
	}
	if message = strings.TrimSpace(message); message != "" {
		if len(message) > maxHeartbeatMessage {
			message = message[:maxHeartbeatMessage]
		}
		// The body is whatever the job printed; postgres refuses invalid UTF-8.
		msg += ": " + strings.ToValidUTF8(message, "")
	}
	_, err = db.Exec(
		`INSERT INTO service_results (service_id, status, message, timestamp) VALUES ($1, $2, $3, $4)`,
		serviceID, status, msg, time.Now().UTC(),
	)
	return err
}
