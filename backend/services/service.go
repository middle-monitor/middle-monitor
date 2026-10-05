package services

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// ServiceService handles service operations
type ServiceService struct {
	db *sql.DB
}

func NewServiceService(db *sql.DB) *ServiceService {
	return &ServiceService{db: db}
}

func (s *ServiceService) CreateService(service models.Service) (*models.Service, error) {
	// Default to organization 1 if not specified (for backward compatibility)
	if service.OrganizationID == 0 {
		service.OrganizationID = 1
	}

	if service.HostID != nil {
		var hostOrgID int64
		if err := s.db.QueryRow(`SELECT organization_id FROM hosts WHERE id = $1`, *service.HostID).Scan(&hostOrgID); err != nil {
			return nil, err
		}
		if hostOrgID != service.OrganizationID {
			return nil, ErrHostOrgMismatch
		}
	}

	// Check plan limits for free orgs
	planLimits := NewPlanLimitsService(s.db)
	if err := planLimits.CanCreateService(service.OrganizationID, service.Type, service.HostID); err != nil {
		return nil, err
	}

	if service.Type == "http" {
		if err := PrepareHTTPServiceCredentials(&service, nil, true); err != nil {
			return nil, err
		}
	}

	// Apply per-type default thresholds when the caller leaves them unset, so a
	// check created without explicit thresholds still alerts on its defaults.
	dw, dc := defaultThresholdsForType(service.Type)
	if service.WarningThreshold == nil {
		service.WarningThreshold = dw
	}
	if service.CriticalThreshold == nil {
		service.CriticalThreshold = dc
	}

	// Generate token for error services
	var token *string
	if strings.HasPrefix(service.Type, "error_service_") {
		generatedToken, err := generateToken()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrTokenGenerate, err)
		}
		token = &generatedToken
	}

	query := `INSERT INTO services (organization_id, host_id, name, type, host, path, credentials, service, service_interval, max_attempts, failure_threshold, warning_threshold, critical_threshold, expected_status_code, token, display_name, expected_body_contains, expected_body_mode, public)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19) RETURNING id, created_at`

	var id int64
	var createdAt time.Time
	err := s.db.QueryRow(query, service.OrganizationID, service.HostID, service.Name, service.Type, service.Host, service.Path, service.Credentials, service.Service, service.ServiceInterval, service.MaxAttempts, service.FailureThreshold, service.WarningThreshold, service.CriticalThreshold, service.ExpectedStatusCode, token, toNullString(service.DisplayName), toNullString(service.ExpectedBodyContains), toNullString(service.ExpectedBodyMode), service.Public).Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServiceCreate, err)
	}

	service.ID = id
	service.CreatedAt = createdAt
	service.Token = token
	return &service, nil
}

// defaultThresholdsForType returns the warning/critical thresholds applied when a
// check is created without explicit ones: response/round-trip latency (ms) for
// network checks, usage percentage for agent host metrics, and days-until-expiry
// for certificates. SNMP has no default (the OID value is device-specific); SQL
// defaults only critical (connection latency in ms) since its warnings come from
// internal heuristics.
func defaultThresholdsForType(serviceType string) (warning, critical *float64) {
	f := func(v float64) *float64 { return &v }
	switch serviceType {
	case "http", "https":
		return f(1000), f(3000)
	case "ping", "agent_network":
		return f(100), f(300)
	case "agent_cpu", "agent_ram", "agent_disk":
		return f(80), f(95)
	case "certificate":
		return f(30), f(7)
	case "sql":
		return nil, f(1000)
	default: // snmp and any other type: no default
		return nil, nil
	}
}

// generateToken generates a secure random token
func generateToken() (string, error) {
	bytes := make([]byte, 32) // 256 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GetAllServicesForOrg returns ALL services for an organization (with and without hosts),
// enriched with the latest result status and host name.
// Excludes error_service_* types (Error SDK); those are listed in the Errors tab only.
// When startDate and endDate are non-empty, each service's results are limited to that range (for the compliance export).
func (s *ServiceService) GetAllServicesForOrg(orgID int64, service, startDate, endDate string) ([]models.ServiceWithResults, error) {
	query := `SELECT s.id, s.organization_id, s.host_id, s.name, s.display_name, s.type, s.host, s.path, s.credentials,
	                 s.service, s.service_interval, s.max_attempts, s.failure_threshold, s.warning_threshold, s.critical_threshold, s.expected_status_code, s.expected_body_contains, s.expected_body_mode, s.token, s.created_at,
	                 h.name as host_name
	          FROM services s
	          LEFT JOIN hosts h ON h.id = s.host_id AND h.organization_id = s.organization_id
	          WHERE s.organization_id = $1 AND s.type NOT LIKE 'error_service_%'`
	args := []interface{}{orgID}
	argPos := 2

	if service != "" {
		query += " AND s.service = $" + strconv.Itoa(argPos)
		args = append(args, service)
		argPos++
	}

	query += " ORDER BY h.name NULLS LAST, s.name ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServicesFetch, err)
	}
	defer rows.Close()

	var servicesWithResults []models.ServiceWithResults
	for rows.Next() {
		var svc models.Service
		var token sql.NullString
		var orgIDNull sql.NullInt64
		var displayName sql.NullString
		var hostName sql.NullString
		var failureThreshold sql.NullFloat64
		var warningThreshold sql.NullFloat64
		var criticalThreshold sql.NullFloat64
		var expectedStatusCode sql.NullInt64
		var expectedBodyContains sql.NullString
		var expectedBodyMode sql.NullString
		if err := rows.Scan(&svc.ID, &orgIDNull, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path,
			&svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts,
			&failureThreshold, &warningThreshold, &criticalThreshold, &expectedStatusCode, &expectedBodyContains, &expectedBodyMode, &token, &svc.CreatedAt, &hostName); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrServiceScan, err)
		}
		if orgIDNull.Valid {
			svc.OrganizationID = orgIDNull.Int64
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
		if warningThreshold.Valid {
			svc.WarningThreshold = &warningThreshold.Float64
		}
		if criticalThreshold.Valid {
			svc.CriticalThreshold = &criticalThreshold.Float64
		}
		if expectedStatusCode.Valid {
			code := int(expectedStatusCode.Int64)
			svc.ExpectedStatusCode = &code
		}
		if expectedBodyContains.Valid {
			svc.ExpectedBodyContains = &expectedBodyContains.String
		}
		if expectedBodyMode.Valid {
			svc.ExpectedBodyMode = &expectedBodyMode.String
		}

		ApplyHTTPAuthAPIRedaction(&svc)

		swr := models.ServiceWithResults{Service: svc}
		if hostName.Valid {
			swr.HostName = &hostName.String
		}
		servicesWithResults = append(servicesWithResults, swr)
	}

	s.enrichServiceResults(servicesWithResults, startDate, endDate)
	return servicesWithResults, nil
}

// enrichServiceResults fills each service's Results with its recent results,
// scoped to [startDate, endDate] when both are set (falling back to the latest
// result overall so the status badge always reflects current state). Query
// errors are skipped, matching the previous inline behaviour.
func (s *ServiceService) enrichServiceResults(servicesWithResults []models.ServiceWithResults, startDate, endDate string) {
	if len(servicesWithResults) == 0 {
		return
	}
	serviceIDs := make([]int64, len(servicesWithResults))
	for i := range servicesWithResults {
		serviceIDs[i] = servicesWithResults[i].Service.ID
	}

	var byService map[int64][]models.ServiceResult
	if startDate != "" && endDate != "" {
		byService = s.latestResultsByService(serviceIDs, 500, startDate, endDate)
		var silent []int64
		for _, id := range serviceIDs {
			if len(byService[id]) == 0 {
				silent = append(silent, id)
			}
		}
		for id, results := range s.latestResultsByService(silent, 1, "", "") {
			byService[id] = results
		}
	} else {
		byService = s.latestResultsByService(serviceIDs, 10, "", "")
	}

	for i := range servicesWithResults {
		servicesWithResults[i].Results = byService[servicesWithResults[i].Service.ID]
	}
}

// latestResultsByService loads the last `limit` results of every given service
// in one round trip: a LATERAL per id lets PostgreSQL descend the index once per
// service, where a query per service made the endpoint cost grow with the number
// of services returned.
func (s *ServiceService) latestResultsByService(serviceIDs []int64, limit int, startDate, endDate string) map[int64][]models.ServiceResult {
	byService := make(map[int64][]models.ServiceResult, len(serviceIDs))
	if len(serviceIDs) == 0 {
		return byService
	}

	// Build ($1::bigint),($2),… placeholders
	placeholders := make([]string, len(serviceIDs))
	args := make([]interface{}, 0, len(serviceIDs)+2)
	for i, id := range serviceIDs {
		placeholders[i] = fmt.Sprintf("($%d::bigint)", i+1)
		args = append(args, id)
	}
	rangeFilter := ""
	if startDate != "" && endDate != "" {
		rangeFilter = fmt.Sprintf(" AND sr.timestamp >= $%d::timestamptz AND sr.timestamp <= $%d::timestamptz", len(serviceIDs)+1, len(serviceIDs)+2)
		args = append(args, startDate, endDate)
	}

	query := fmt.Sprintf(`SELECT r.id, r.service_id, r.status, r.latency, r.message, r.timestamp AT TIME ZONE 'UTC' as timestamp,
	                             r.metric_type, r.metric_value, r.metadata
	                      FROM (VALUES %s) AS t(service_id)
	                      JOIN LATERAL (
	                        SELECT sr.id, sr.service_id, sr.status, sr.latency, sr.message, sr.timestamp,
	                               sr.metric_type, sr.metric_value, sr.metadata
	                        FROM service_results sr
	                        WHERE sr.service_id = t.service_id%s
	                        ORDER BY sr.timestamp DESC
	                        LIMIT %d
	                      ) r ON true
	                      ORDER BY r.service_id, r.timestamp DESC`,
		strings.Join(placeholders, ","), rangeFilter, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		slog.Error("failed to fetch service results", "services", len(serviceIDs), "error", err)
		return byService
	}
	defer rows.Close()

	for rows.Next() {
		var r models.ServiceResult
		var latency sql.NullFloat64
		var message sql.NullString
		var metricType sql.NullString
		var metricValue sql.NullFloat64
		var metadata sql.NullString
		if err := rows.Scan(&r.ID, &r.ServiceID, &r.Status, &latency, &message, &r.Timestamp,
			&metricType, &metricValue, &metadata); err != nil {
			continue
		}
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
		byService[r.ServiceID] = append(byService[r.ServiceID], r)
	}
	return byService
}

// ServiceStatsResult holds the global service facet counts and the distinct
// type list shown on the services page (independent of filters/page).
type ServiceStatsResult struct {
	Total   int      `json:"total"`
	Healthy int      `json:"healthy"`
	Failing int      `json:"failing"`
	Warning int      `json:"warning"`
	Unknown int      `json:"unknown"`
	Types   []string `json:"types"`
}

// deriveServiceStatus maps the SQL effective status (latest result for checks,
// last max_attempts samples ALL breaching for agent metrics) to the UI facet
// value, mirroring the frontend getServiceStatus so filters/facets match the
// badge each row shows.
func deriveServiceStatus(hasResult bool, effectiveStatus string) string {
	if !hasResult {
		return "unknown"
	}
	switch effectiveStatus {
	case "failure":
		return "failing"
	case "warning":
		return "warning"
	}
	return "healthy"
}

// serviceRow is a service plus its derived status, used to filter/facet before
// the heavy per-service results enrichment is done (page only).
type serviceRow struct {
	swr    models.ServiceWithResults
	status string
}

// listServiceRowsForOrg returns one lightweight row per monitored service (its
// fields, host name and derived current status from the latest result overall),
// excluding Error SDK services. It does NOT load the results arrays, so it stays
// cheap even though it covers every service; results are enriched per page later.
func (s *ServiceService) listServiceRowsForOrg(orgID int64) ([]serviceRow, error) {
	query := `SELECT s.id, s.organization_id, s.host_id, s.name, s.display_name, s.type, s.host, s.path, s.credentials,
	                 s.service, s.service_interval, s.max_attempts, s.failure_threshold, s.warning_threshold, s.critical_threshold,
	                 s.expected_status_code, s.expected_body_contains, s.expected_body_mode, s.token, s.created_at,
	                 h.name AS host_name,
	                 lr.status, (lr.present IS NOT NULL) AS has_result
	          FROM services s
	          LEFT JOIN hosts h ON h.id = s.host_id AND h.organization_id = s.organization_id
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
	              END AS status,
	              1 AS present
	            FROM (
	              SELECT status, metric_value, row_number() OVER (ORDER BY timestamp DESC) AS rn
	              FROM service_results
	              WHERE service_id = s.id
	              ORDER BY timestamp DESC
	              LIMIT s.max_attempts
	            ) sr
	          ) lr ON true
	          WHERE s.organization_id = $1 AND s.type NOT LIKE 'error_service_%'
	          ORDER BY h.name NULLS LAST, s.name ASC`
	rows, err := s.db.Query(query, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServicesFetch, err)
	}
	defer rows.Close()

	var out []serviceRow
	for rows.Next() {
		var svc models.Service
		var token sql.NullString
		var orgIDNull sql.NullInt64
		var displayName sql.NullString
		var hostName sql.NullString
		var failureThreshold sql.NullFloat64
		var warningThreshold sql.NullFloat64
		var criticalThreshold sql.NullFloat64
		var expectedStatusCode sql.NullInt64
		var expectedBodyContains sql.NullString
		var expectedBodyMode sql.NullString
		var latestStatus sql.NullString
		var hasResult bool
		if err := rows.Scan(&svc.ID, &orgIDNull, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path,
			&svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts,
			&failureThreshold, &warningThreshold, &criticalThreshold, &expectedStatusCode, &expectedBodyContains, &expectedBodyMode, &token, &svc.CreatedAt, &hostName,
			&latestStatus, &hasResult); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrServiceScan, err)
		}
		if orgIDNull.Valid {
			svc.OrganizationID = orgIDNull.Int64
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
		if warningThreshold.Valid {
			svc.WarningThreshold = &warningThreshold.Float64
		}
		if criticalThreshold.Valid {
			svc.CriticalThreshold = &criticalThreshold.Float64
		}
		if expectedStatusCode.Valid {
			code := int(expectedStatusCode.Int64)
			svc.ExpectedStatusCode = &code
		}
		if expectedBodyContains.Valid {
			svc.ExpectedBodyContains = &expectedBodyContains.String
		}
		if expectedBodyMode.Valid {
			svc.ExpectedBodyMode = &expectedBodyMode.String
		}

		ApplyHTTPAuthAPIRedaction(&svc)

		swr := models.ServiceWithResults{Service: svc}
		if hostName.Valid {
			swr.HostName = &hostName.String
		}

		status := deriveServiceStatus(hasResult, latestStatus.String)

		out = append(out, serviceRow{swr: swr, status: status})
	}
	return out, rows.Err()
}

// serviceRowMatches applies the search/status/type filters used by the services page.
func serviceRowMatches(r serviceRow, search, status, typeFilter string) bool {
	if status != "" && status != "all" && r.status != status {
		return false
	}
	if typeFilter != "" && typeFilter != "all" && r.swr.Service.Type != typeFilter {
		return false
	}
	if search != "" {
		q := strings.ToLower(search)
		hostName := ""
		if r.swr.HostName != nil {
			hostName = strings.ToLower(*r.swr.HostName)
		}
		if !strings.Contains(strings.ToLower(r.swr.Service.Name), q) &&
			!strings.Contains(strings.ToLower(r.swr.Service.Host), q) &&
			!strings.Contains(hostName, q) &&
			!strings.Contains(strings.ToLower(r.swr.Service.Service), q) {
			return false
		}
	}
	return true
}

// GetServicesPageForOrg returns a filtered, paginated page of monitored services
// (with results enriched for that page only) plus the total matching count.
func (s *ServiceService) GetServicesPageForOrg(orgID int64, search, status, typeFilter, startDate, endDate string, limit, offset int) ([]models.ServiceWithResults, int, error) {
	all, err := s.listServiceRowsForOrg(orgID)
	if err != nil {
		return nil, 0, err
	}
	matched := make([]models.ServiceWithResults, 0, len(all))
	for _, r := range all {
		if serviceRowMatches(r, search, status, typeFilter) {
			matched = append(matched, r.swr)
		}
	}
	total := len(matched)
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := matched[start:end]
	s.enrichServiceResults(page, startDate, endDate)
	return page, total, nil
}

// ServiceStats returns the global service facet counts (per derived status) and
// the distinct type list, independent of the active filters or page.
func (s *ServiceService) ServiceStats(orgID int64) (ServiceStatsResult, error) {
	all, err := s.listServiceRowsForOrg(orgID)
	if err != nil {
		return ServiceStatsResult{Types: []string{}}, err
	}
	r := ServiceStatsResult{Total: len(all), Types: []string{}}
	seen := map[string]struct{}{}
	for _, row := range all {
		switch row.status {
		case "healthy":
			r.Healthy++
		case "failing":
			r.Failing++
		case "warning":
			r.Warning++
		default:
			r.Unknown++
		}
		if _, ok := seen[row.swr.Service.Type]; !ok {
			seen[row.swr.Service.Type] = struct{}{}
			r.Types = append(r.Types, row.swr.Service.Type)
		}
	}
	sort.Strings(r.Types)
	return r, nil
}

// GetServicesForHost returns all services for a specific host, enriched with results.
// When startDate and endDate are non-empty, each service's results are limited to that range (for the compliance export).
func (s *ServiceService) GetServicesForHost(hostID, orgID int64, startDate, endDate string) ([]models.ServiceWithResults, error) {
	query := `SELECT s.id, s.organization_id, s.host_id, s.name, s.display_name, s.type, s.host, s.path, s.credentials,
	                 s.service, s.service_interval, s.max_attempts, s.failure_threshold, s.warning_threshold, s.critical_threshold, s.expected_status_code, s.expected_body_contains, s.expected_body_mode, s.token, s.created_at,
	                 h.name as host_name
	          FROM services s
	          LEFT JOIN hosts h ON h.id = s.host_id
	          WHERE s.host_id = $1 AND s.organization_id = $2
	          ORDER BY s.name ASC`

	rows, err := s.db.Query(query, hostID, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServicesForHostFetch, err)
	}
	defer rows.Close()

	var servicesWithResults []models.ServiceWithResults
	for rows.Next() {
		var svc models.Service
		var token sql.NullString
		var orgIDNull sql.NullInt64
		var displayName sql.NullString
		var hostName sql.NullString
		var failureThreshold sql.NullFloat64
		var warningThreshold sql.NullFloat64
		var criticalThreshold sql.NullFloat64
		var expectedStatusCode sql.NullInt64
		var expectedBodyContains sql.NullString
		var expectedBodyMode sql.NullString
		if err := rows.Scan(&svc.ID, &orgIDNull, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path,
			&svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts,
			&failureThreshold, &warningThreshold, &criticalThreshold, &expectedStatusCode, &expectedBodyContains, &expectedBodyMode, &token, &svc.CreatedAt, &hostName); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrServiceScan, err)
		}
		if orgIDNull.Valid {
			svc.OrganizationID = orgIDNull.Int64
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
		if warningThreshold.Valid {
			svc.WarningThreshold = &warningThreshold.Float64
		}
		if criticalThreshold.Valid {
			svc.CriticalThreshold = &criticalThreshold.Float64
		}
		if expectedStatusCode.Valid {
			code := int(expectedStatusCode.Int64)
			svc.ExpectedStatusCode = &code
		}
		if expectedBodyContains.Valid {
			svc.ExpectedBodyContains = &expectedBodyContains.String
		}
		if expectedBodyMode.Valid {
			svc.ExpectedBodyMode = &expectedBodyMode.String
		}

		swr := models.ServiceWithResults{Service: svc}
		if hostName.Valid {
			swr.HostName = &hostName.String
		}
		servicesWithResults = append(servicesWithResults, swr)
	}

	s.enrichServiceResults(servicesWithResults, startDate, endDate)

	return servicesWithResults, nil
}

func (s *ServiceService) GetErrorServices() ([]models.Service, error) {
	return s.GetErrorServicesForOrg(0)
}

func (s *ServiceService) GetErrorServicesForOrg(orgID int64) ([]models.Service, error) {
	query := `SELECT id, organization_id, host_id, name, display_name, type, host, path, credentials, service, service_interval, max_attempts, failure_threshold, warning_threshold, critical_threshold, expected_status_code, expected_body_contains, expected_body_mode, token, created_at
			  FROM services WHERE type LIKE 'error_service_%'`
	args := []interface{}{}
	argPos := 1

	if orgID > 0 {
		query += " AND organization_id = $" + strconv.Itoa(argPos)
		args = append(args, orgID)
		argPos++
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrErrorServicesFetch, err)
	}
	defer rows.Close()

	var services []models.Service
	for rows.Next() {
		var svc models.Service
		var token sql.NullString
		var orgIDNull sql.NullInt64
		var displayName sql.NullString
		var failureThreshold sql.NullFloat64
		var warningThreshold sql.NullFloat64
		var criticalThreshold sql.NullFloat64
		var expectedStatusCode sql.NullInt64
		var expectedBodyContains sql.NullString
		var expectedBodyMode sql.NullString
		if err := rows.Scan(&svc.ID, &orgIDNull, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path, &svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts, &failureThreshold, &warningThreshold, &criticalThreshold, &expectedStatusCode, &expectedBodyContains, &expectedBodyMode, &token, &svc.CreatedAt); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrServiceScan, err)
		}
		if orgIDNull.Valid {
			svc.OrganizationID = orgIDNull.Int64
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
		if warningThreshold.Valid {
			svc.WarningThreshold = &warningThreshold.Float64
		}
		if criticalThreshold.Valid {
			svc.CriticalThreshold = &criticalThreshold.Float64
		}
		if expectedStatusCode.Valid {
			code := int(expectedStatusCode.Int64)
			svc.ExpectedStatusCode = &code
		}
		if expectedBodyContains.Valid {
			svc.ExpectedBodyContains = &expectedBodyContains.String
		}
		if expectedBodyMode.Valid {
			svc.ExpectedBodyMode = &expectedBodyMode.String
		}
		services = append(services, svc)
	}

	return services, nil
}

func (s *ServiceService) GetServiceByID(serviceID int64) (*models.Service, error) {
	query := `SELECT id, host_id, name, display_name, type, host, path, credentials, service, service_interval, max_attempts, failure_threshold, warning_threshold, critical_threshold, expected_status_code, expected_body_contains, expected_body_mode, token, created_at
			  FROM services WHERE id = $1`

	var svc models.Service
	var token sql.NullString
	var displayName sql.NullString
	var failureThreshold sql.NullFloat64
	var warningThreshold sql.NullFloat64
	var criticalThreshold sql.NullFloat64
	var expectedStatusCode sql.NullInt64
	var expectedBodyContains sql.NullString
	var expectedBodyMode sql.NullString
	err := s.db.QueryRow(query, serviceID).Scan(&svc.ID, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path, &svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts, &failureThreshold, &warningThreshold, &criticalThreshold, &expectedStatusCode, &expectedBodyContains, &expectedBodyMode, &token, &svc.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServiceFetch, err)
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
	if warningThreshold.Valid {
		svc.WarningThreshold = &warningThreshold.Float64
	}
	if criticalThreshold.Valid {
		svc.CriticalThreshold = &criticalThreshold.Float64
	}
	if expectedStatusCode.Valid {
		code := int(expectedStatusCode.Int64)
		svc.ExpectedStatusCode = &code
	}
	if expectedBodyContains.Valid {
		svc.ExpectedBodyContains = &expectedBodyContains.String
	}
	if expectedBodyMode.Valid {
		svc.ExpectedBodyMode = &expectedBodyMode.String
	}

	return &svc, nil
}

// GetServiceDetail returns a single service with host_name and latest results
func (s *ServiceService) GetServiceDetail(serviceID, orgID int64) (*models.ServiceWithResults, error) {
	query := `SELECT s.id, s.organization_id, s.host_id, s.name, s.display_name, s.type, s.host, s.path, s.credentials,
	                 s.service, s.service_interval, s.max_attempts, s.failure_threshold, s.warning_threshold, s.critical_threshold, s.expected_status_code, s.expected_body_contains, s.expected_body_mode, s.token, s.created_at,
	                 h.name as host_name
	          FROM services s
	          LEFT JOIN hosts h ON h.id = s.host_id
	          WHERE s.id = $1 AND s.organization_id = $2`

	var svc models.Service
	var token sql.NullString
	var orgIDNull sql.NullInt64
	var displayName sql.NullString
	var hostName sql.NullString
	var failureThreshold sql.NullFloat64
	var warningThreshold sql.NullFloat64
	var criticalThreshold sql.NullFloat64
	var expectedStatusCode sql.NullInt64
	var expectedBodyContains sql.NullString
	var expectedBodyMode sql.NullString
	err := s.db.QueryRow(query, serviceID, orgID).Scan(
		&svc.ID, &orgIDNull, &svc.HostID, &svc.Name, &displayName, &svc.Type, &svc.Host, &svc.Path,
		&svc.Credentials, &svc.Service, &svc.ServiceInterval, &svc.MaxAttempts,
		&failureThreshold, &warningThreshold, &criticalThreshold, &expectedStatusCode, &expectedBodyContains, &expectedBodyMode, &token, &svc.CreatedAt, &hostName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServiceNotFound, err)
	}
	if orgIDNull.Valid {
		svc.OrganizationID = orgIDNull.Int64
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
	if warningThreshold.Valid {
		svc.WarningThreshold = &warningThreshold.Float64
	}
	if criticalThreshold.Valid {
		svc.CriticalThreshold = &criticalThreshold.Float64
	}
	if expectedStatusCode.Valid {
		code := int(expectedStatusCode.Int64)
		svc.ExpectedStatusCode = &code
	}
	if expectedBodyContains.Valid {
		svc.ExpectedBodyContains = &expectedBodyContains.String
	}
	if expectedBodyMode.Valid {
		svc.ExpectedBodyMode = &expectedBodyMode.String
	}

	ApplyHTTPAuthAPIRedaction(&svc)

	swr := &models.ServiceWithResults{Service: svc}
	if hostName.Valid {
		swr.HostName = &hostName.String
	}

	// Get latest 10 results
	resultsQuery := `SELECT id, service_id, status, latency, message, timestamp AT TIME ZONE 'UTC' as timestamp,
	                        metric_type, metric_value, metadata
	                 FROM service_results WHERE service_id = $1 ORDER BY timestamp DESC LIMIT 10`
	resultsRows, err := s.db.Query(resultsQuery, serviceID)
	if err == nil {
		defer resultsRows.Close()
		for resultsRows.Next() {
			var r models.ServiceResult
			var latency sql.NullFloat64
			var message sql.NullString
			var metricType sql.NullString
			var metricValue sql.NullFloat64
			var metadata sql.NullString
			if err := resultsRows.Scan(&r.ID, &r.ServiceID, &r.Status, &latency, &message, &r.Timestamp,
				&metricType, &metricValue, &metadata); err == nil {
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
				swr.Results = append(swr.Results, r)
			}
		}
	}

	return swr, nil
}

func (s *ServiceService) UpdateService(serviceID int64, service models.Service) (*models.Service, error) {
	existing, err := s.GetServiceByID(serviceID)
	if err != nil {
		return nil, err
	}

	if service.Type == "http" {
		if err := PrepareHTTPServiceCredentials(&service, existing, false); err != nil {
			return nil, err
		}
	} else {
		// Avoid wiping SQL/SNMP credentials when the client omits the field
		if (service.Credentials == nil || (service.Credentials != nil && *service.Credentials == "")) &&
			existing != nil && existing.Type == service.Type && existing.Credentials != nil {
			service.Credentials = existing.Credentials
		}
	}

	query := `UPDATE services SET name = $1, type = $2, host = $3, path = $4, credentials = $5, service = $6, service_interval = $7, max_attempts = $8, failure_threshold = $9, warning_threshold = $10, critical_threshold = $11, expected_status_code = $12, display_name = $13, expected_body_contains = $14, expected_body_mode = $15
			  WHERE id = $16 RETURNING host_id, created_at`

	var hostID sql.NullInt64
	var createdAt time.Time
	err = s.db.QueryRow(query, service.Name, service.Type, service.Host, service.Path, service.Credentials, service.Service, service.ServiceInterval, service.MaxAttempts, service.FailureThreshold, service.WarningThreshold, service.CriticalThreshold, service.ExpectedStatusCode, toNullString(service.DisplayName), toNullString(service.ExpectedBodyContains), toNullString(service.ExpectedBodyMode), serviceID).Scan(&hostID, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrServiceNotFound
		}
		return nil, fmt.Errorf("%w: %w", ErrServiceUpdate, err)
	}

	service.ID = serviceID
	if hostID.Valid {
		service.HostID = &hostID.Int64
	}
	service.CreatedAt = createdAt
	return &service, nil
}

func (s *ServiceService) DeleteService(serviceID int64) error {
	query := `DELETE FROM services WHERE id = $1`
	result, err := s.db.Exec(query, serviceID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrServiceDelete, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrServiceNotFound
	}

	return nil
}

func (s *ServiceService) GetServiceResults(serviceID int64, startDate, endDate string) ([]models.ServiceResult, error) {
	var query string
	var args []interface{}

	if startDate != "" && endDate != "" {
		query = `SELECT id, service_id, status, latency, message, timestamp AT TIME ZONE 'UTC' as timestamp, metric_type, metric_value, metadata
				 FROM service_results 
				 WHERE service_id = $1 
				 AND timestamp >= $2::timestamptz 
				 AND timestamp <= $3::timestamptz 
				 ORDER BY timestamp DESC`
		args = []interface{}{serviceID, startDate, endDate}
	} else {
		query = `SELECT id, service_id, status, latency, message, timestamp AT TIME ZONE 'UTC' as timestamp, metric_type, metric_value, metadata
				 FROM service_results 
				 WHERE service_id = $1 
				 ORDER BY timestamp DESC 
				 LIMIT 10`
		args = []interface{}{serviceID}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServiceResultsFetch, err)
	}
	defer rows.Close()

	results := make([]models.ServiceResult, 0)
	for rows.Next() {
		var r models.ServiceResult
		var latency sql.NullFloat64
		var message sql.NullString
		var metricType sql.NullString
		var metricValue sql.NullFloat64
		var metadata sql.NullString
		if err := rows.Scan(&r.ID, &r.ServiceID, &r.Status, &latency, &message, &r.Timestamp, &metricType, &metricValue, &metadata); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrResultScan, err)
		}
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

	return results, nil
}
