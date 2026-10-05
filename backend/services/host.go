package services

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"middle-monitor/backend/models"
)

// HostService handles host operations
type HostService struct {
	db *sql.DB
}

func NewHostService(db *sql.DB) *HostService {
	return &HostService{db: db}
}

func (s *HostService) CreateHost(host models.Host) (*models.Host, error) {
	// Default to organization 1 if not specified (for backward compatibility)
	if host.OrganizationID == 0 {
		host.OrganizationID = 1
	}

	// Check plan limits for free orgs
	planLimits := NewPlanLimitsService(s.db)
	if err := planLimits.CanCreateHost(host.OrganizationID); err != nil {
		return nil, err
	}

	// New hosts join the org's default group unless an explicit group was provided.
	if host.HostGroupID == nil {
		var defID int64
		if err := s.db.QueryRow(`SELECT id FROM host_groups WHERE organization_id = $1 AND is_default LIMIT 1`, host.OrganizationID).Scan(&defID); err == nil {
			host.HostGroupID = &defID
		}
	}

	query := `INSERT INTO hosts (organization_id, name, host, service, display_name, host_group_id)
			  VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`

	var id int64
	var createdAt time.Time
	err := s.db.QueryRow(query, host.OrganizationID, host.Name, host.Host, host.Service, toNullString(host.DisplayName), host.HostGroupID).Scan(&id, &createdAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return nil, &NameTakenError{Kind: "host", Name: host.Name}
		}
		return nil, fmt.Errorf("%w: %w", ErrHostCreate, err)
	}

	host.ID = id
	host.CreatedAt = createdAt
	return &host, nil
}

func (s *HostService) GetHosts(service string, includeServices bool) ([]models.Host, error) {
	return s.GetHostsForOrg(0, service, includeServices)
}

func toNullString(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

// GetHostsWithStats returns hosts with service counts and health status.
// Counts are threshold-aware: for agent metrics the current metric_value is
// compared against warning_threshold / critical_threshold so that a threshold
// changed after the last result is reflected immediately.
func (s *HostService) GetHostsWithStats(orgID int64) ([]models.HostWithStats, error) {
	query := `
		SELECT h.id, h.organization_id, h.name, h.host, h.service, h.display_name, h.host_group_id, h.created_at,
		       COUNT(s.id) as service_count,
		       COUNT(CASE WHEN latest.effective_status = 'success'  THEN 1 END) as healthy_count,
		       COUNT(CASE WHEN latest.effective_status = 'failure'  THEN 1 END) as failing_count,
		       COUNT(CASE WHEN latest.effective_status = 'warning'  THEN 1 END) as warning_count,
		       COUNT(CASE WHEN latest.effective_status = 'critical' THEN 1 END) as critical_count
		FROM hosts h
		-- Error-tracking services live in the Errors view; excluded from host rollups.
		LEFT JOIN services s ON s.host_id = h.id AND s.organization_id = h.organization_id AND s.type NOT LIKE 'error_service_%'
		LEFT JOIN LATERAL (
		  SELECT
		    CASE
		      WHEN count(sr.status) = 0 THEN NULL
		      WHEN s.type IN ('agent_cpu','agent_ram','agent_disk') THEN
		        CASE
		          WHEN s.critical_threshold IS NOT NULL AND bool_and(sr.metric_value > s.critical_threshold) AND count(sr.metric_value) = s.max_attempts THEN 'failure'
		          WHEN s.warning_threshold  IS NOT NULL AND bool_and(sr.metric_value > s.warning_threshold)  AND count(sr.metric_value) = s.max_attempts THEN 'warning'
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
		    END AS effective_status
		  FROM (
		    SELECT status, metric_value, row_number() OVER (ORDER BY timestamp DESC) AS rn
		    FROM service_results
		    WHERE service_id = s.id
		    ORDER BY timestamp DESC
		    LIMIT s.max_attempts
		  ) sr
		) latest ON s.id IS NOT NULL
		WHERE h.organization_id = $1
		GROUP BY h.id
		ORDER BY h.name ASC`

	rows, err := s.db.Query(query, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHostsFetch, err)
	}
	defer rows.Close()

	var hosts []models.HostWithStats
	for rows.Next() {
		var h models.HostWithStats
		var orgIDNull sql.NullInt64
		var hostAddr string
		var displayName sql.NullString
		var hostGroupID sql.NullInt64
		if err := rows.Scan(&h.ID, &orgIDNull, &h.Name, &hostAddr, &h.Service, &displayName, &hostGroupID, &h.CreatedAt,
			&h.ServiceCount, &h.HealthyCount, &h.FailingCount, &h.WarningCount, &h.CriticalCount); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrHostScan, err)
		}
		h.Host.Host = hostAddr
		if displayName.Valid {
			h.DisplayName = &displayName.String
		}
		if hostGroupID.Valid {
			h.HostGroupID = &hostGroupID.Int64
		}
		if orgIDNull.Valid {
			h.OrganizationID = orgIDNull.Int64
		}
		hosts = append(hosts, h)
	}

	// Status reflects recent results (agent metrics within 5 min, others 10 min)
	// instead of just counts; avoids showing "Opérationnel" when no data has been
	// received yet. One query for the whole list, whatever the number of hosts.
	ids := make([]int64, len(hosts))
	for i := range hosts {
		ids[i] = hosts[i].ID
	}
	statuses := s.hostStatuses(ids)
	for i := range hosts {
		status := statuses[hosts[i].ID]
		hosts[i].Status = &status
	}

	return hosts, nil
}

// HostStatsResult holds the global host facet counts shown on the hosts page.
type HostStatsResult struct {
	Total         int `json:"total"`
	Healthy       int `json:"healthy"`
	Failing       int `json:"failing"`
	Warning       int `json:"warning"`
	Unknown       int `json:"unknown"`
	TotalServices int `json:"total_services"`
}

// hostRollupCTE builds the per-host aggregate CTE (one row per host with the
// count of services in each effective status), shared by the paginated list,
// count and stats queries. It applies the org filter ($1) and an optional
// name/host/service search, and returns the CTE text, its args and the next
// positional arg index.
//
// The per-host derived status the UI shows reduces to these counts: a host with
// zero services is "unknown", otherwise failing > warning > healthy. That mirrors
// the frontend getHostStatus exactly, so filtering/faceting stays consistent.
func hostRollupCTE(orgID int64, search string) (string, []interface{}, int) {
	cte := `WITH host_rollup AS (
		SELECT h.id, h.organization_id, h.name, h.host, h.service, h.display_name, h.host_group_id, h.created_at,
		       COUNT(s.id) AS service_count,
		       COUNT(CASE WHEN latest.effective_status = 'success'  THEN 1 END) AS healthy_count,
		       COUNT(CASE WHEN latest.effective_status = 'failure'  THEN 1 END) AS failing_count,
		       COUNT(CASE WHEN latest.effective_status = 'warning'  THEN 1 END) AS warning_count,
		       COUNT(CASE WHEN latest.effective_status = 'critical' THEN 1 END) AS critical_count
		FROM hosts h
		-- Error-tracking services live in the Errors view; excluded from host rollups.
		LEFT JOIN services s ON s.host_id = h.id AND s.organization_id = h.organization_id AND s.type NOT LIKE 'error_service_%'
		LEFT JOIN LATERAL (
		  SELECT CASE
		      WHEN count(sr.status) = 0 THEN NULL
		      WHEN s.type IN ('agent_cpu','agent_ram','agent_disk') THEN
		        CASE
		          WHEN s.critical_threshold IS NOT NULL AND bool_and(sr.metric_value > s.critical_threshold) AND count(sr.metric_value) = s.max_attempts THEN 'failure'
		          WHEN s.warning_threshold  IS NOT NULL AND bool_and(sr.metric_value > s.warning_threshold)  AND count(sr.metric_value) = s.max_attempts THEN 'warning'
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
		    END AS effective_status
		  FROM (
		    SELECT status, metric_value, row_number() OVER (ORDER BY timestamp DESC) AS rn
		    FROM service_results
		    WHERE service_id = s.id
		    ORDER BY timestamp DESC
		    LIMIT s.max_attempts
		  ) sr
		) latest ON s.id IS NOT NULL
		WHERE h.organization_id = $1`
	args := []interface{}{orgID}
	argPos := 2
	if search != "" {
		cte += fmt.Sprintf(" AND (h.name ILIKE $%d OR h.display_name ILIKE $%d OR h.host ILIKE $%d OR h.service ILIKE $%d)", argPos, argPos, argPos, argPos)
		args = append(args, "%"+search+"%")
		argPos++
	}
	cte += " GROUP BY h.id)"
	return cte, args, argPos
}

// hostStatusFilter returns the SQL predicate (over the host_rollup count columns)
// that selects hosts whose derived status matches the given value, or "" for none.
func hostStatusFilter(status string) string {
	switch status {
	case "healthy":
		return "(failing_count + critical_count) = 0 AND warning_count = 0 AND healthy_count > 0"
	case "warning":
		return "(failing_count + critical_count) = 0 AND warning_count > 0"
	case "failing":
		return "(failing_count + critical_count) > 0"
	case "unknown":
		return "service_count = 0 OR (failing_count + critical_count = 0 AND warning_count = 0 AND healthy_count = 0)"
	default:
		return ""
	}
}

// GetHostsWithStatsPaged returns a filtered, paginated page of hosts with their
// service-status counts. Status is left empty (the UI derives it from the counts);
// this avoids the per-host recency query the unpaginated path runs.
func (s *HostService) GetHostsWithStatsPaged(orgID int64, search, status string, limit, offset int) ([]models.HostWithStats, error) {
	cte, args, argPos := hostRollupCTE(orgID, search)
	query := cte + `
		SELECT id, organization_id, name, host, service, display_name, host_group_id, created_at,
		       service_count, healthy_count, failing_count, warning_count, critical_count
		FROM host_rollup`
	if sf := hostStatusFilter(status); sf != "" {
		query += " WHERE " + sf
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query += fmt.Sprintf(" ORDER BY name ASC LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHostsFetch, err)
	}
	defer rows.Close()

	var hosts []models.HostWithStats
	for rows.Next() {
		var h models.HostWithStats
		var orgIDNull sql.NullInt64
		var hostAddr string
		var displayName sql.NullString
		var hostGroupID sql.NullInt64
		if err := rows.Scan(&h.ID, &orgIDNull, &h.Name, &hostAddr, &h.Service, &displayName, &hostGroupID, &h.CreatedAt,
			&h.ServiceCount, &h.HealthyCount, &h.FailingCount, &h.WarningCount, &h.CriticalCount); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrHostScan, err)
		}
		h.Host.Host = hostAddr
		if displayName.Valid {
			h.DisplayName = &displayName.String
		}
		if hostGroupID.Valid {
			h.HostGroupID = &hostGroupID.Int64
		}
		if orgIDNull.Valid {
			h.OrganizationID = orgIDNull.Int64
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// CountHostsFiltered counts hosts matching the same search/status filters as
// GetHostsWithStatsPaged, for pagination.
func (s *HostService) CountHostsFiltered(orgID int64, search, status string) (int, error) {
	cte, args, _ := hostRollupCTE(orgID, search)
	query := cte + " SELECT COUNT(*) FROM host_rollup"
	if sf := hostStatusFilter(status); sf != "" {
		query += " WHERE " + sf
	}
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrHostsCount, err)
	}
	return count, nil
}

// HostStats returns the global host facet counts (per derived status + total
// services), independent of the active search/status filter or page.
func (s *HostService) HostStats(orgID int64) (HostStatsResult, error) {
	cte, args, _ := hostRollupCTE(orgID, "")
	query := cte + `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE (failing_count + critical_count) = 0 AND warning_count = 0 AND healthy_count > 0),
			COUNT(*) FILTER (WHERE (failing_count + critical_count) > 0),
			COUNT(*) FILTER (WHERE (failing_count + critical_count) = 0 AND warning_count > 0),
			COUNT(*) FILTER (WHERE service_count = 0 OR (failing_count + critical_count = 0 AND warning_count = 0 AND healthy_count = 0)),
			COALESCE(SUM(service_count), 0)
		FROM host_rollup`
	var r HostStatsResult
	if err := s.db.QueryRow(query, args...).Scan(&r.Total, &r.Healthy, &r.Failing, &r.Warning, &r.Unknown, &r.TotalServices); err != nil {
		return r, fmt.Errorf("%w: %w", ErrHostStatsCompute, err)
	}
	return r, nil
}

func (s *HostService) GetHostsForOrg(orgID int64, service string, includeServices bool) ([]models.Host, error) {
	query := `SELECT id, organization_id, name, host, service, created_at FROM hosts WHERE 1=1`
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

	query += " ORDER BY created_at DESC"
	query = strings.Replace(query, "SELECT id, organization_id, name, host, service, created_at", "SELECT id, organization_id, name, host, service, display_name, created_at", 1)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHostsFetch, err)
	}
	defer rows.Close()

	var hosts []models.Host
	for rows.Next() {
		var h models.Host
		var orgIDNull sql.NullInt64
		var displayName sql.NullString
		if err := rows.Scan(&h.ID, &orgIDNull, &h.Name, &h.Host, &h.Service, &displayName, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrHostScan, err)
		}
		if orgIDNull.Valid {
			h.OrganizationID = orgIDNull.Int64
		}
		if displayName.Valid {
			h.DisplayName = &displayName.String
		}
		hosts = append(hosts, h)
	}

	if includeServices {
		for i := range hosts {
			serviceRows, err := s.db.Query(`SELECT id, host_id, name, display_name, type, host, path, credentials, service, service_interval, max_attempts, failure_threshold, token, created_at 
											FROM services WHERE host_id = $1 ORDER BY created_at DESC`, hosts[i].ID)
			if err == nil {
				for serviceRows.Next() {
					var svc models.Service
					var token sql.NullString
					var displayName sql.NullString
					var failureThreshold sql.NullFloat64
					if err := serviceRows.Scan(&svc.ID, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path, &svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts, &failureThreshold, &token, &svc.CreatedAt); err == nil {
						if displayName.Valid {
							svc.DisplayName = &displayName.String
						}
						if token.Valid {
							svc.Token = &token.String
						}
						if failureThreshold.Valid {
							svc.FailureThreshold = &failureThreshold.Float64
						}
						ApplyHTTPAuthAPIRedaction(&svc)
						hosts[i].Services = append(hosts[i].Services, svc)
					}
				}
				serviceRows.Close()
			}
		}
	}

	// Calculate status for each host
	ids := make([]int64, len(hosts))
	for i := range hosts {
		ids[i] = hosts[i].ID
	}
	statuses := s.hostStatuses(ids)
	for i := range hosts {
		status := statuses[hosts[i].ID]
		hosts[i].Status = &status
	}

	return hosts, nil
}

func (s *HostService) GetHostByID(hostID int64) (*models.Host, error) {
	query := `SELECT id, name, host, service, display_name, created_at FROM hosts WHERE id = $1`

	var h models.Host
	var displayName sql.NullString
	err := s.db.QueryRow(query, hostID).Scan(&h.ID, &h.Name, &h.Host, &h.Service, &displayName, &h.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHostFetch, err)
	}
	if displayName.Valid {
		h.DisplayName = &displayName.String
	}
	return &h, nil
}

// GetHostDetail returns a host with its service count and status, scoped to org
func (s *HostService) GetHostDetail(hostID, orgID int64) (*models.Host, error) {
	query := `SELECT id, organization_id, name, host, service, display_name, created_at FROM hosts WHERE id = $1 AND organization_id = $2`

	var h models.Host
	var orgIDNull sql.NullInt64
	var displayName sql.NullString
	err := s.db.QueryRow(query, hostID, orgID).Scan(&h.ID, &orgIDNull, &h.Name, &h.Host, &h.Service, &displayName, &h.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHostNotFound, err)
	}
	if orgIDNull.Valid {
		h.OrganizationID = orgIDNull.Int64
	}
	if displayName.Valid {
		h.DisplayName = &displayName.String
	}
	status := s.hostStatuses([]int64{h.ID})[h.ID]
	h.Status = &status

	return &h, nil
}

// GetHostByName returns the host by name within the given organization (name is unique per org)
func (s *HostService) GetHostByName(hostName string, orgID int64) (*models.Host, error) {
	query := `SELECT id, organization_id, name, host, service, display_name, created_at FROM hosts WHERE name = $1 AND organization_id = $2`

	var h models.Host
	var orgIDNull sql.NullInt64
	var displayName sql.NullString
	err := s.db.QueryRow(query, hostName, orgID).Scan(&h.ID, &orgIDNull, &h.Name, &h.Host, &h.Service, &displayName, &h.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrHostNotFound
		}
		return nil, fmt.Errorf("%w: %w", ErrHostFetch, err)
	}
	if orgIDNull.Valid {
		h.OrganizationID = orgIDNull.Int64
	}
	if displayName.Valid {
		h.DisplayName = &displayName.String
	}
	return &h, nil
}

func (s *HostService) UpdateHost(hostID int64, host models.Host) (*models.Host, error) {
	// Only display_name is editable; name/host/service are technical identifiers (agent, by-name lookup)
	existing, err := s.GetHostByID(hostID)
	if err != nil {
		return nil, err
	}
	query := `UPDATE hosts SET display_name = $1 WHERE id = $2 RETURNING created_at`

	var createdAt time.Time
	if err := s.db.QueryRow(query, toNullString(host.DisplayName), hostID).Scan(&createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrHostNotFound
		}
		return nil, fmt.Errorf("%w: %w", ErrHostUpdate, err)
	}
	out := *existing
	out.DisplayName = host.DisplayName
	out.CreatedAt = createdAt
	out.ID = hostID
	return &out, nil
}

func (s *HostService) DeleteHost(hostID int64) error {
	var serviceCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM services WHERE host_id = $1`, hostID).Scan(&serviceCount); err != nil {
		return fmt.Errorf("%w: %w", ErrHostServicesCount, err)
	}
	if serviceCount > 0 {
		return ErrHostHasServices
	}

	query := `DELETE FROM hosts WHERE id = $1`
	result, err := s.db.Exec(query, hostID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHostDelete, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrHostNotFound
	}

	return nil
}

func (s *HostService) GetHostServices(hostID int64) ([]models.ServiceWithResults, error) {
	query := `SELECT id, host_id, name, display_name, type, host, path, credentials, service, service_interval, max_attempts, failure_threshold, token, created_at
			  FROM services WHERE host_id = $1 ORDER BY created_at DESC`

	rows, err := s.db.Query(query, hostID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServicesFetch, err)
	}
	defer rows.Close()

	var services []models.Service
	for rows.Next() {
		var svc models.Service
		var token sql.NullString
		var displayName sql.NullString
		var failureThreshold sql.NullFloat64
		if err := rows.Scan(&svc.ID, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path, &svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts, &failureThreshold, &token, &svc.CreatedAt); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrServiceScan, err)
		}
		if displayName.Valid {
			svc.DisplayName = &displayName.String
		}
		if token.Valid {
			svc.Token = &token.String
		}
		if failureThreshold.Valid {
			svc.FailureThreshold = &failureThreshold.Float64
		}
		services = append(services, svc)
	}

	return s.enrichHostServicesWithResults(services)
}

func (s *HostService) enrichHostServicesWithResults(services []models.Service) ([]models.ServiceWithResults, error) {
	var servicesWithResults []models.ServiceWithResults

	for _, service := range services {
		svc := service
		ApplyHTTPAuthAPIRedaction(&svc)
		var results []models.ServiceResult
		resultsQuery := `SELECT id, service_id, status, latency, message, timestamp AT TIME ZONE 'UTC' as timestamp, metric_type, metric_value, metadata
						FROM service_results WHERE service_id = $1 ORDER BY timestamp DESC LIMIT 10`
		resultsRows, err := s.db.Query(resultsQuery, svc.ID)
		if err == nil {
			for resultsRows.Next() {
				var r models.ServiceResult
				var latency sql.NullFloat64
				var message sql.NullString
				var metricType sql.NullString
				var metricValue sql.NullFloat64
				var metadata sql.NullString
				if err := resultsRows.Scan(&r.ID, &r.ServiceID, &r.Status, &latency, &message, &r.Timestamp, &metricType, &metricValue, &metadata); err == nil {
					r.Timestamp = r.Timestamp.UTC()
					if latency.Valid {
						r.Latency = &latency.Float64
					}
					if message.Valid {
						r.Message = &message.String
					}
					if metricType.Valid {
						r.MetricType = &metricType.String
					}
					if metricValue.Valid {
						r.MetricValue = &metricValue.Float64
					}
					if metadata.Valid {
						r.Metadata = &metadata.String
					}
					results = append(results, r)
				}
			}
			resultsRows.Close()
		}
		if results == nil {
			results = []models.ServiceResult{}
		}
		servicesWithResults = append(servicesWithResults, models.ServiceWithResults{
			Service: svc,
			Results: results,
		})
	}

	return servicesWithResults, nil
}

// hostStatusSeverity orders derived statuses: a host shows the worst status
// among its services, and stays unknown until one of them has a fresh result.
// The "" of a service that must not count is absent from the map, so it weighs
// 0 and can never raise a host status.
var hostStatusSeverity = map[string]int{"unknown": 0, "success": 1, "warning": 2, "failure": 3}

// hostServiceStatus derives what one service contributes to its host status, or
// "" when its last result must not count. A result older than 10 minutes (5 for
// an agent service) is ignored: a host whose agent stopped reporting falls back
// to unknown instead of freezing its last verdict on screen.
func hostServiceStatus(status sql.NullString, resultTime sql.NullTime, serviceType sql.NullString,
	metricValue, warnThreshold, critThreshold sql.NullFloat64, now time.Time) string {
	if !resultTime.Valid {
		return ""
	}

	staleThreshold := 10 * time.Minute
	if serviceType.Valid && strings.HasPrefix(serviceType.String, "agent_") {
		staleThreshold = 5 * time.Minute
	}
	if now.Sub(resultTime.Time) > staleThreshold {
		return ""
	}

	// For agent metrics evaluate current metric_value vs thresholds so
	// that thresholds set after the last result are reflected immediately.
	isAgentMetric := serviceType.Valid && (serviceType.String == "agent_cpu" ||
		serviceType.String == "agent_ram" || serviceType.String == "agent_disk")

	if isAgentMetric && metricValue.Valid {
		if critThreshold.Valid && metricValue.Float64 > critThreshold.Float64 {
			return "failure"
		}
		if warnThreshold.Valid && metricValue.Float64 > warnThreshold.Float64 {
			return "warning"
		}
		return "success"
	}

	if status.Valid {
		if status.String == "failure" || status.String == "critical" {
			return "failure"
		}
		if status.String == "warning" {
			return "warning"
		}
	}
	return "success"
}

// hostStatuses returns the derived status of each given host, in a single query
// whatever the number of hosts. Hosts with no service, or no fresh result, stay
// "unknown".
//
// The per-service lookup is a LATERAL "last result": it descends the
// service_results index instead of joining the whole table and sorting it, so
// the cost follows the number of services of the organization and not the
// volume written by the other tenants.
func (s *HostService) hostStatuses(hostIDs []int64) map[int64]string {
	statuses := unknownHostStatuses(hostIDs)
	if len(hostIDs) == 0 {
		return statuses
	}

	// Fetch the latest result per service together with thresholds so we can
	// apply the same threshold-aware logic the frontend uses.
	query := `
		SELECT s.host_id, sr.status, sr.timestamp, s.type,
		       sr.metric_value, s.warning_threshold, s.critical_threshold
		FROM services s
		LEFT JOIN LATERAL (
		  SELECT status, timestamp, metric_value
		  FROM service_results
		  WHERE service_id = s.id
		  ORDER BY timestamp DESC
		  LIMIT 1
		) sr ON true
		WHERE s.host_id = ANY($1)
	`

	rows, err := s.db.Query(query, pq.Int64Array(hostIDs))
	if err != nil {
		slog.Error("host statuses query failed", "error", err, "hosts", len(hostIDs))
		return statuses
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var hostID int64
		var status, serviceType sql.NullString
		var resultTime sql.NullTime
		var metricValue, warnThreshold, critThreshold sql.NullFloat64
		if err := rows.Scan(&hostID, &status, &resultTime, &serviceType, &metricValue, &warnThreshold, &critThreshold); err != nil {
			slog.Error("host statuses scan failed", "error", err, "hosts", len(hostIDs))
			return unknownHostStatuses(hostIDs)
		}

		derived := hostServiceStatus(status, resultTime, serviceType, metricValue, warnThreshold, critThreshold, now)
		if hostStatusSeverity[derived] > hostStatusSeverity[statuses[hostID]] {
			statuses[hostID] = derived
		}
	}

	// An interrupted iteration leaves a partial map, which would show a host as
	// healthy only because its failing row was never read. Fall back to unknown.
	if err := rows.Err(); err != nil {
		slog.Error("host statuses scan interrupted", "error", err, "hosts", len(hostIDs))
		return unknownHostStatuses(hostIDs)
	}

	return statuses
}

func unknownHostStatuses(hostIDs []int64) map[int64]string {
	statuses := make(map[int64]string, len(hostIDs))
	for _, id := range hostIDs {
		statuses[id] = "unknown"
	}
	return statuses
}
