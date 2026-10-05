package services

import (
	"database/sql"
	"fmt"
	"strings"

	"middle-monitor/backend/models"
)

// MaintenanceService manages downtime/maintenance windows and the alert
// suppression rules derived from them and from per-org severity opt-in.
type MaintenanceService struct {
	db *sql.DB
}

func NewMaintenanceService(db *sql.DB) *MaintenanceService {
	return &MaintenanceService{db: db}
}

// List returns all maintenance windows for an org (most recent first), with the
// target's display name resolved for the UI.
func (s *MaintenanceService) List(orgID int64) ([]models.MaintenanceWindow, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.organization_id, m.name, m.target_type, m.target_id,
		       m.starts_at, m.ends_at, m.created_by, m.created_at,
		       COALESCE(svc.name, h.name, '') AS target_name
		FROM maintenance_windows m
		LEFT JOIN services svc ON m.target_type = 'service' AND svc.id = m.target_id
		LEFT JOIN hosts h ON m.target_type = 'host' AND h.id = m.target_id
		WHERE m.organization_id = $1
		ORDER BY m.ends_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMaintenanceWindowsList, err)
	}
	defer rows.Close()

	windows := []models.MaintenanceWindow{}
	for rows.Next() {
		var w models.MaintenanceWindow
		var createdBy sql.NullInt64
		if err := rows.Scan(&w.ID, &w.OrganizationID, &w.Name, &w.TargetType, &w.TargetID,
			&w.StartsAt, &w.EndsAt, &createdBy, &w.CreatedAt, &w.TargetName); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMaintenanceWindowScan, err)
		}
		if createdBy.Valid {
			w.CreatedBy = &createdBy.Int64
		}
		windows = append(windows, w)
	}
	return windows, nil
}

// Create validates and inserts a maintenance window.
func (s *MaintenanceService) Create(w models.MaintenanceWindow) (*models.MaintenanceWindow, error) {
	w.TargetType = strings.ToLower(strings.TrimSpace(w.TargetType))
	if w.TargetType != "service" && w.TargetType != "host" {
		return nil, ErrMaintenanceTargetType
	}
	if strings.TrimSpace(w.Name) == "" {
		return nil, ErrNameRequired
	}
	if !w.EndsAt.After(w.StartsAt) {
		return nil, ErrMaintenanceRange
	}

	// Ensure the target belongs to the org (defense against cross-tenant targeting).
	var table string
	if w.TargetType == "service" {
		table = "services"
	} else {
		table = "hosts"
	}
	var exists bool
	if err := s.db.QueryRow(
		fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1 AND organization_id = $2)", table),
		w.TargetID, w.OrganizationID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTargetVerify, err)
	}
	if !exists {
		return nil, ErrMaintenanceTargetNotFound
	}

	err := s.db.QueryRow(`
		INSERT INTO maintenance_windows (organization_id, name, target_type, target_id, starts_at, ends_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		w.OrganizationID, w.Name, w.TargetType, w.TargetID, w.StartsAt, w.EndsAt, w.CreatedBy,
	).Scan(&w.ID, &w.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMaintenanceWindowCreate, err)
	}
	return &w, nil
}

// Delete removes a maintenance window scoped to the org.
func (s *MaintenanceService) Delete(id, orgID int64) error {
	res, err := s.db.Exec(`DELETE FROM maintenance_windows WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMaintenanceWindowDelete, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMaintenanceNotFound
	}
	return nil
}

// IsTargetUnderMaintenance reports whether an active window currently covers the
// given service and/or host. Either id may be nil. A service is also considered
// covered when a window targets its parent host.
func (s *MaintenanceService) IsTargetUnderMaintenance(orgID int64, serviceID, hostID *int64) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM maintenance_windows
		WHERE organization_id = $1
		  AND NOW() BETWEEN starts_at AND ends_at
		  AND (
		        (target_type = 'service' AND target_id = $2)
		     OR (target_type = 'host'    AND target_id = $3)
		      )`,
		orgID, nullableID(serviceID), nullableID(hostID),
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetOrgAlertSettings returns the per-org warning/critical delivery opt-in flags.
func (s *MaintenanceService) GetOrgAlertSettings(orgID int64) (warningEnabled, criticalEnabled bool, err error) {
	warningEnabled, criticalEnabled = true, true
	err = s.db.QueryRow(
		`SELECT alert_warning_enabled, alert_critical_enabled FROM organizations WHERE id = $1`, orgID,
	).Scan(&warningEnabled, &criticalEnabled)
	if err == sql.ErrNoRows {
		return true, true, nil
	}
	return warningEnabled, criticalEnabled, err
}

// IsSeverityMuted reports whether the org has opted out of receiving alerts of
// the given severity ("warning" / "critical").
func (s *MaintenanceService) IsSeverityMuted(orgID int64, severity string) (bool, error) {
	warningEnabled, criticalEnabled, err := s.GetOrgAlertSettings(orgID)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(severity) {
	case "warning":
		return !warningEnabled, nil
	case "critical":
		return !criticalEnabled, nil
	default:
		return false, nil
	}
}

// SuppressAlert combines downtime + severity opt-in: returns true (with a reason
// for logging) when a notification for this target/severity must NOT be sent.
func (s *MaintenanceService) SuppressAlert(orgID int64, severity string, serviceID, hostID *int64) (bool, string) {
	if muted, err := s.IsSeverityMuted(orgID, severity); err == nil && muted {
		return true, "severity muted in org settings"
	}
	if down, err := s.IsTargetUnderMaintenance(orgID, serviceID, hostID); err == nil && down {
		return true, "target under maintenance window"
	}
	return false, ""
}

func nullableID(v *int64) interface{} {
	if v == nil {
		return int64(-1) // never matches a real id
	}
	return *v
}
