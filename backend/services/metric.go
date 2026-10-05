package services

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"middle-monitor/backend/models"
)

// MetricService handles system metrics operations
type MetricService struct {
	db *sql.DB
}

func NewMetricService(db *sql.DB) *MetricService {
	return &MetricService{db: db}
}

func (s *MetricService) CreateMetric(metric models.SystemMetric) (*models.SystemMetric, error) {
	if metric.Timestamp.IsZero() {
		metric.Timestamp = time.Now().UTC()
	}

	// Default to organization 1 if not specified (for backward compatibility)
	if metric.OrganizationID == 0 {
		metric.OrganizationID = 1
	}

	query := `INSERT INTO system_metrics (organization_id, service, cpu_perc, ram_perc, http_latency, endpoint, timestamp)
			  VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`

	var id int64
	err := s.db.QueryRow(query, metric.OrganizationID, metric.Service, metric.CPUPerc, metric.RAMPerc, metric.HTTPLatency, metric.Endpoint, metric.Timestamp).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMetricSave, err)
	}

	metric.ID = id
	return &metric, nil
}

func (s *MetricService) GetMetrics(service string) ([]models.SystemMetric, error) {
	return s.GetMetricsForOrg(0, service)
}

func (s *MetricService) GetMetricsForOrg(orgID int64, service string) ([]models.SystemMetric, error) {
	query := `SELECT id, organization_id, service, cpu_perc, ram_perc, http_latency, endpoint, timestamp
			  FROM system_metrics WHERE 1=1`
	args := []interface{}{}
	argPos := 1

	if orgID > 0 {
		query += " AND organization_id = $" + strconv.Itoa(argPos)
		args = append(args, orgID)
		argPos++
	}
	if service != "" {
		query += " AND service = $" + strconv.Itoa(argPos)
		args = append(args, service)
		argPos++
	}

	query += " ORDER BY timestamp DESC LIMIT 50"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMetricsFetch, err)
	}
	defer rows.Close()

	metrics := make([]models.SystemMetric, 0)
	for rows.Next() {
		var m models.SystemMetric
		var orgIDNull sql.NullInt64
		if err := rows.Scan(&m.ID, &orgIDNull, &m.Service, &m.CPUPerc, &m.RAMPerc, &m.HTTPLatency, &m.Endpoint, &m.Timestamp); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMetricScan, err)
		}
		if orgIDNull.Valid {
			m.OrganizationID = orgIDNull.Int64
		}
		metrics = append(metrics, m)
	}

	return metrics, nil
}
