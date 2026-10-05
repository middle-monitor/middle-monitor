package services

import (
	"database/sql"
	"fmt"
	"time"

	"middle-monitor/backend/models"
)

// ProfileService handles pprof profile capture storage and retrieval
type ProfileService struct {
	db *sql.DB
}

// NewProfileService creates a new profile service
func NewProfileService(db *sql.DB) *ProfileService {
	return &ProfileService{db: db}
}

// Create stores a new profile capture (memoryMB optional, for RAM time-series)
func (s *ProfileService) Create(orgID int64, service, profileType string, durationSeconds *int, memoryMB *float64, data []byte) (*models.ProfileCapture, error) {
	sizeBytes := len(data)
	query := `INSERT INTO profile_captures (organization_id, service, profile_type, duration_seconds, size_bytes, memory_mb, data)
			  VALUES ($1, $2, $3, $4, $5, $6, $7)
			  RETURNING id, organization_id, service, profile_type, duration_seconds, size_bytes, memory_mb, created_at`
	var p models.ProfileCapture
	err := s.db.QueryRow(query, orgID, service, profileType, durationSeconds, sizeBytes, memoryMB, data).Scan(
		&p.ID, &p.OrganizationID, &p.Service, &p.ProfileType, &p.DurationSeconds, &p.SizeBytes, &p.MemoryMB, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns profile captures for an org with optional filters
func (s *ProfileService) List(orgID int64, service, profileType string, limit, offset int) ([]models.ProfileCapture, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, organization_id, service, profile_type, duration_seconds, size_bytes, memory_mb, created_at
			  FROM profile_captures WHERE organization_id = $1`
	args := []interface{}{orgID}
	argPos := 2
	if service != "" {
		query += fmt.Sprintf(" AND service = $%d", argPos)
		args = append(args, service)
		argPos++
	}
	if profileType != "" {
		query += fmt.Sprintf(" AND profile_type = $%d", argPos)
		args = append(args, profileType)
		argPos++
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.ProfileCapture
	for rows.Next() {
		var p models.ProfileCapture
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Service, &p.ProfileType, &p.DurationSeconds, &p.SizeBytes, &p.MemoryMB, &p.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// CountProfiles returns the total number of profile captures matching the same
// org/service/profile_type filters as List, for pagination.
func (s *ProfileService) CountProfiles(orgID int64, service, profileType string) (int, error) {
	query := `SELECT COUNT(*) FROM profile_captures WHERE organization_id = $1`
	args := []interface{}{orgID}
	argPos := 2
	if service != "" {
		query += fmt.Sprintf(" AND service = $%d", argPos)
		args = append(args, service)
		argPos++
	}
	if profileType != "" {
		query += fmt.Sprintf(" AND profile_type = $%d", argPos)
		args = append(args, profileType)
		argPos++
	}
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// GetByID returns a profile capture by ID (with data) if it belongs to the org
func (s *ProfileService) GetByID(orgID int64, id int64) (*models.ProfileCapture, error) {
	query := `SELECT id, organization_id, service, profile_type, duration_seconds, size_bytes, memory_mb, created_at, data
			  FROM profile_captures WHERE id = $1 AND organization_id = $2`
	var p models.ProfileCapture
	err := s.db.QueryRow(query, id, orgID).Scan(
		&p.ID, &p.OrganizationID, &p.Service, &p.ProfileType, &p.DurationSeconds, &p.SizeBytes, &p.MemoryMB, &p.CreatedAt, &p.Data,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// MemorySeriesPoint is one point for the RAM-over-time chart
type MemorySeriesPoint struct {
	Timestamp string  `json:"timestamp"`
	MemoryMB  float64 `json:"memory_mb"`
	Service   string  `json:"service"`
}

// Series returns memory_mb time series for charts (only rows where memory_mb IS NOT NULL)
func (s *ProfileService) Series(orgID int64, service string, from, to *time.Time, limit int) ([]MemorySeriesPoint, error) {
	if limit <= 0 {
		limit = 500
	}
	query := `SELECT created_at, memory_mb, service
			  FROM profile_captures WHERE organization_id = $1 AND memory_mb IS NOT NULL`
	args := []interface{}{orgID}
	argPos := 2
	if service != "" {
		query += fmt.Sprintf(" AND service = $%d", argPos)
		args = append(args, service)
		argPos++
	}
	if from != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argPos)
		args = append(args, *from)
		argPos++
	}
	if to != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argPos)
		args = append(args, *to)
		argPos++
	}
	query += fmt.Sprintf(" ORDER BY created_at ASC LIMIT $%d", argPos)
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemorySeriesPoint
	for rows.Next() {
		var ts time.Time
		var mb float64
		var svc string
		if err := rows.Scan(&ts, &mb, &svc); err != nil {
			return nil, err
		}
		out = append(out, MemorySeriesPoint{Timestamp: ts.Format(time.RFC3339), MemoryMB: mb, Service: svc})
	}
	return out, rows.Err()
}

// DeleteOlderThan removes profile captures older than the given duration (for cleanup)
func (s *ProfileService) DeleteOlderThan(cutoff time.Time) (int64, error) {
	result, err := s.db.Exec(`DELETE FROM profile_captures WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
