package services

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"middle-monitor/backend/models"
)

// DashboardService handles dashboard operations
type DashboardService struct {
	db *sql.DB
}

func NewDashboardService(db *sql.DB) *DashboardService {
	return &DashboardService{db: db}
}

func (s *DashboardService) GetHealth() (*models.HealthStatus, error) {
	return s.GetHealthForOrg(0)
}

func (s *DashboardService) GetHealthForOrg(orgID int64) (*models.HealthStatus, error) {
	var totalServices int
	var errors24h int64
	var failingServices int
	var warningServices int

	servicesQuery := `SELECT COUNT(*) FROM services WHERE type NOT LIKE 'error_service_%'`
	errorsQuery := `SELECT COUNT(*) FROM application_errors WHERE timestamp > NOW() - INTERVAL '24 hours'`
	// Failing AND warning counts in one pass, using the same effective status as
	// the services page, so /dashboard/health cannot disagree with /stats.
	statusQuery := `
		SELECT
		  COUNT(*) FILTER (WHERE latest.effective = 'failure'),
		  COUNT(*) FILTER (WHERE latest.effective = 'warning')
		FROM services s
		LEFT JOIN LATERAL (
		  SELECT
		    CASE
		      WHEN count(sr.status) = 0 THEN NULL
		      WHEN s.type IN ('agent_cpu','agent_ram','agent_disk') THEN
		        CASE
		          WHEN s.critical_threshold IS NOT NULL AND bool_and(sr.metric_value > s.critical_threshold) AND count(sr.metric_value) = s.max_attempts THEN 'failure'
		          WHEN s.warning_threshold IS NOT NULL AND bool_and(sr.metric_value > s.warning_threshold) AND count(sr.metric_value) = s.max_attempts THEN 'warning'
		          ELSE 'success'
		        END
		      WHEN s.type LIKE 'agent_%' THEN
		        CASE
		          WHEN bool_and(sr.status = 'failure') AND count(sr.status) = s.max_attempts THEN 'failure'
		          WHEN bool_and(sr.status = 'warning') AND count(sr.status) = s.max_attempts THEN 'warning'
		          ELSE 'success'
		        END
		      ELSE
		        CASE max(sr.status) FILTER (WHERE sr.rn = 1)
		          WHEN 'failure' THEN 'failure'
		          WHEN 'warning' THEN 'warning'
		          ELSE 'success'
		        END
		    END AS effective
		  FROM (
		    SELECT status, metric_value, row_number() OVER (ORDER BY timestamp DESC) AS rn
		    FROM service_results
		    WHERE service_id = s.id
		    ORDER BY timestamp DESC
		    LIMIT s.max_attempts
		  ) sr
		) latest ON true
		WHERE s.type NOT LIKE 'error_service_%'
	`

	if orgID > 0 {
		servicesQuery += ` AND organization_id = $1`
		errorsQuery += ` AND organization_id = $1`
		statusQuery += ` AND s.organization_id = $1`
	}

	if orgID > 0 {
		if err := s.db.QueryRow(servicesQuery, orgID).Scan(&totalServices); err != nil {
			totalServices = 0
		}
		if err := s.db.QueryRow(errorsQuery, orgID).Scan(&errors24h); err != nil {
			errors24h = 0
		}
		if err := s.db.QueryRow(statusQuery, orgID).Scan(&failingServices, &warningServices); err != nil {
			failingServices, warningServices = 0, 0
		}
	} else {
		if err := s.db.QueryRow(servicesQuery).Scan(&totalServices); err != nil {
			totalServices = 0
		}
		if err := s.db.QueryRow(errorsQuery).Scan(&errors24h); err != nil {
			errors24h = 0
		}
		if err := s.db.QueryRow(statusQuery).Scan(&failingServices, &warningServices); err != nil {
			failingServices, warningServices = 0, 0
		}
	}

	// Same escalation as the organization stats endpoint. Error volume alone is
	// not an availability signal: a busy app can log thousands of handled errors
	// while every check stays green.
	status := "healthy"
	switch {
	case failingServices > 0:
		status = "degraded"
		if failingServices > totalServices/2 {
			status = "critical"
		}
	case warningServices > 0:
		status = "degraded"
	}

	return &models.HealthStatus{
		Status:          status,
		Services:        totalServices,
		Errors24h:       errors24h,
		ServicesFailing: failingServices,
		LastUpdate:      time.Now().UTC(),
	}, nil
}

func (s *DashboardService) GetErrorStats(service string) (*models.ErrorStats, error) {
	return s.GetErrorStatsForOrg(0, service)
}

func (s *DashboardService) GetErrorStatsForOrg(orgID int64, service string) (*models.ErrorStats, error) {
	// Total and by_error are 24h-bounded to match the "Overview (24h)" card;
	// by_service stays all-time because the errors view uses it for app
	// membership regardless of date range.
	query := `SELECT COUNT(*) FROM application_errors WHERE timestamp > NOW() - INTERVAL '24 hours'`
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

	var total int64
	if err := s.db.QueryRow(query, args...).Scan(&total); err != nil {
		total = 0
	}

	byServiceQuery := `SELECT service, COUNT(*) FROM application_errors WHERE 1=1`
	byServiceArgs := []interface{}{}
	byServiceArgPos := 1
	if orgID > 0 {
		byServiceQuery += " AND organization_id = $" + strconv.Itoa(byServiceArgPos)
		byServiceArgs = append(byServiceArgs, orgID)
		byServiceArgPos++
	}
	if service != "" {
		byServiceQuery += " AND service = $" + strconv.Itoa(byServiceArgPos)
		byServiceArgs = append(byServiceArgs, service)
		byServiceArgPos++
	}
	byServiceQuery += " GROUP BY service"

	rows, err := s.db.Query(byServiceQuery, byServiceArgs...)
	byService := make(map[string]int64)
	if err == nil && rows != nil {
		for rows.Next() {
			var svc string
			var count int64
			if err := rows.Scan(&svc, &count); err == nil {
				byService[svc] = count
			}
		}
		rows.Close()
	}

	byErrorQuery := `SELECT name, COUNT(*) FROM application_errors WHERE timestamp > NOW() - INTERVAL '24 hours'`
	byErrorArgs := []interface{}{}
	byErrorArgPos := 1
	if orgID > 0 {
		byErrorQuery += " AND organization_id = $" + strconv.Itoa(byErrorArgPos)
		byErrorArgs = append(byErrorArgs, orgID)
		byErrorArgPos++
	}
	if service != "" {
		byErrorQuery += " AND service = $" + strconv.Itoa(byErrorArgPos)
		byErrorArgs = append(byErrorArgs, service)
		byErrorArgPos++
	}
	byErrorQuery += " GROUP BY name"

	rows, err = s.db.Query(byErrorQuery, byErrorArgs...)
	byError := make(map[string]int64)
	if err == nil && rows != nil {
		for rows.Next() {
			var errName string
			var count int64
			if err := rows.Scan(&errName, &count); err == nil {
				byError[errName] = count
			}
		}
		rows.Close()
	}

	recentQuery := `SELECT id, organization_id, name, message, file, line, timestamp, service, http_method, http_url, http_headers, http_body FROM application_errors WHERE 1=1`
	recentArgs := []interface{}{}
	recentArgPos := 1
	if orgID > 0 {
		recentQuery += " AND organization_id = $" + strconv.Itoa(recentArgPos)
		recentArgs = append(recentArgs, orgID)
		recentArgPos++
	}
	if service != "" {
		recentQuery += " AND service = $" + strconv.Itoa(recentArgPos)
		recentArgs = append(recentArgs, service)
		recentArgPos++
	}
	recentQuery += " ORDER BY timestamp DESC LIMIT 10"

	rows, err = s.db.Query(recentQuery, recentArgs...)
	var recent []models.ApplicationError
	if err == nil && rows != nil {
		for rows.Next() {
			var e models.ApplicationError
			var httpMethod, httpURL, httpHeaders, httpBody sql.NullString
			var orgIDNull sql.NullInt64
			if err := rows.Scan(&e.ID, &orgIDNull, &e.Name, &e.Message, &e.File, &e.Line, &e.Timestamp, &e.Service, &httpMethod, &httpURL, &httpHeaders, &httpBody); err == nil {
				if orgIDNull.Valid {
					e.OrganizationID = orgIDNull.Int64
				}
				if httpMethod.Valid {
					e.HTTPMethod = &httpMethod.String
				}
				if httpURL.Valid {
					e.HTTPURL = &httpURL.String
				}
				if httpHeaders.Valid {
					e.HTTPHeaders = &httpHeaders.String
				}
				if httpBody.Valid {
					e.HTTPBody = &httpBody.String
				}
				recent = append(recent, e)
			}
		}
		rows.Close()
	}

	return &models.ErrorStats{
		Total:     total,
		ByService: byService,
		ByError:   byError,
		Recent:    recent,
	}, nil
}

func (s *DashboardService) GetMetricStats(service string) (*models.MetricStats, error) {
	return s.GetMetricStatsForOrg(0, service)
}

func (s *DashboardService) GetMetricStatsForOrg(orgID int64, service string) (*models.MetricStats, error) {
	query := `SELECT AVG(cpu_perc), AVG(ram_perc), AVG(http_latency), MAX(timestamp)
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

	var cpuAvg, ramAvg sql.NullFloat64
	var httpLatency sql.NullFloat64
	var lastUpdate sql.NullTime
	err := s.db.QueryRow(query, args...).Scan(&cpuAvg, &ramAvg, &httpLatency, &lastUpdate)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMetricStatsFetch, err)
	}

	stats := &models.MetricStats{
		CPUAvg:      0,
		RAMAvg:      0,
		HTTPLatency: nil,
		LastUpdate:  time.Now().UTC(),
	}

	if cpuAvg.Valid {
		stats.CPUAvg = cpuAvg.Float64
	}
	if ramAvg.Valid {
		stats.RAMAvg = ramAvg.Float64
	}
	if httpLatency.Valid {
		stats.HTTPLatency = &httpLatency.Float64
	}
	if lastUpdate.Valid {
		stats.LastUpdate = lastUpdate.Time.UTC()
	}

	return stats, nil
}

func (s *DashboardService) GetTimeline(service string, limit int) ([]models.Event, error) {
	return s.GetTimelineForOrg(0, service, limit)
}

func (s *DashboardService) GetTimelineForOrg(orgID int64, service string, limit int) ([]models.Event, error) {
	return s.GetTimelineForOrgFiltered(orgID, service, limit, time.Time{}, time.Time{})
}

func (s *DashboardService) GetTimelineForOrgFiltered(orgID int64, service string, limit int, from, to time.Time) ([]models.Event, error) {
	eventService := NewEventService(s.db)
	return eventService.GetEventsForOrgFiltered(orgID, service, limit, from, to)
}
