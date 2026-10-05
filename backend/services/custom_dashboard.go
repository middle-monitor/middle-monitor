package services

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"middle-monitor/backend/models"
)

// CustomDashboardService handles CRUD for user-built dashboards, scoped per org.
type CustomDashboardService struct {
	db *sql.DB
}

func NewCustomDashboardService(db *sql.DB) *CustomDashboardService {
	return &CustomDashboardService{db: db}
}

func (s *CustomDashboardService) List(orgID int64) ([]models.CustomDashboard, error) {
	rows, err := s.db.Query(`
		SELECT id, organization_id, name, widgets, created_at, updated_at
		FROM custom_dashboards WHERE organization_id = $1 ORDER BY created_at ASC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCustomDashboardsFetch, err)
	}
	defer rows.Close()

	var dashboards []models.CustomDashboard
	for rows.Next() {
		var d models.CustomDashboard
		var widgets []byte
		if err := rows.Scan(&d.ID, &d.OrganizationID, &d.Name, &widgets, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCustomDashboardScan, err)
		}
		d.Widgets = normalizeWidgets(widgets)
		dashboards = append(dashboards, d)
	}
	return dashboards, nil
}

func (s *CustomDashboardService) Create(d models.CustomDashboard) (*models.CustomDashboard, error) {
	widgets := normalizeWidgets(d.Widgets)
	err := s.db.QueryRow(`
		INSERT INTO custom_dashboards (organization_id, name, widgets)
		VALUES ($1, $2, $3) RETURNING id, created_at, updated_at`,
		d.OrganizationID, d.Name, []byte(widgets),
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCustomDashboardCreate, err)
	}
	d.Widgets = widgets
	return &d, nil
}

func (s *CustomDashboardService) Update(dashboardID, orgID int64, d models.CustomDashboard) (*models.CustomDashboard, error) {
	widgets := normalizeWidgets(d.Widgets)
	var out models.CustomDashboard
	var dbWidgets []byte
	err := s.db.QueryRow(`
		UPDATE custom_dashboards SET name = $1, widgets = $2, updated_at = $3
		WHERE id = $4 AND organization_id = $5
		RETURNING id, organization_id, name, widgets, created_at, updated_at`,
		d.Name, []byte(widgets), time.Now().UTC(), dashboardID, orgID,
	).Scan(&out.ID, &out.OrganizationID, &out.Name, &dbWidgets, &out.CreatedAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrDashboardNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCustomDashboardUpdate, err)
	}
	out.Widgets = normalizeWidgets(dbWidgets)
	return &out, nil
}

func (s *CustomDashboardService) Delete(dashboardID, orgID int64) error {
	res, err := s.db.Exec(`DELETE FROM custom_dashboards WHERE id = $1 AND organization_id = $2`, dashboardID, orgID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCustomDashboardDelete, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDashboardNotFound
	}
	return nil
}

// normalizeWidgets guarantees a valid JSON array so the column (and the API
// response) is never null or malformed, regardless of what the client sent.
func normalizeWidgets(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage("[]")
	}
	return raw
}
