package services

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// ErrorService handles application error operations
type ErrorService struct {
	db *sql.DB
}

func NewErrorService(db *sql.DB) *ErrorService {
	return &ErrorService{db: db}
}

func (s *ErrorService) CreateError(appErr models.ApplicationError) (*models.ApplicationError, error) {
	if appErr.Timestamp.IsZero() {
		appErr.Timestamp = time.Now().UTC()
	}

	// Default to organization 1 if not specified (for backward compatibility)
	if appErr.OrganizationID == 0 {
		appErr.OrganizationID = 1
	}

	// Compute a stable fingerprint so occurrences of the same error group
	// together (recurrence / regression detection downstream).
	if appErr.Fingerprint == "" {
		appErr.Fingerprint = ComputeErrorFingerprint(appErr.Name, appErr.Message, appErr.File)
	}

	query := `INSERT INTO application_errors (organization_id, name, message, file, line, timestamp, service, http_method, http_url, http_headers, http_body, trace_id, fingerprint)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`

	var id int64
	if err := s.db.QueryRow(query, appErr.OrganizationID, appErr.Name, appErr.Message, appErr.File, appErr.Line, appErr.Timestamp, appErr.Service, appErr.HTTPMethod, appErr.HTTPURL, appErr.HTTPHeaders, appErr.HTTPBody, appErr.TraceID, appErr.Fingerprint).Scan(&id); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrErrorSave, err)
	}

	appErr.ID = id

	// Async alert trigger for APM errors
	go func(errObj models.ApplicationError) {
		// Try to find a service configured for these APM errors
		var serviceID int64
		var failureThreshold sql.NullFloat64
		qSvc := `SELECT id, failure_threshold FROM services
                 WHERE organization_id = $1 AND service = $2 AND type LIKE 'error_service_%' LIMIT 1`
		errSvc := s.db.QueryRow(qSvc, errObj.OrganizationID, errObj.Service).Scan(&serviceID, &failureThreshold)

		if errSvc == nil && failureThreshold.Valid {
			// Threshold is defined (e.g. 10 errors per minute)
			// Let's count errors in the last minute
			var count int
			qCount := `SELECT COUNT(id) FROM application_errors
                       WHERE organization_id = $1 AND service = $2
                         AND timestamp >= NOW() - INTERVAL '1 minute'`
			s.db.QueryRow(qCount, errObj.OrganizationID, errObj.Service).Scan(&count)

			if float64(count) >= failureThreshold.Float64 {
				// Prevent spam: check if we already sent an incident recently, or just send a notification
				// For simplicity, we just fetch active channels and notify
				channels, _ := NewAlertService(s.db).GetChannels(errObj.OrganizationID)
				svcContext := ServiceAlertContext(s.db, serviceID)
				for _, ch := range channels {
					if ch.Enabled {
						title := fmt.Sprintf("APM alert: %s", errObj.Service)
						msg := fmt.Sprintf("Error rate exceeded the %.0f/min threshold (%d in the last minute).\nError: %s - %s",
							failureThreshold.Float64, count, errObj.Name, errObj.Message) + svcContext
						SendNotification(ch, title, msg)
					}
				}
			}
		}
	}(appErr)

	return &appErr, nil
}

func (s *ErrorService) GetErrors(service, limit string) ([]models.ApplicationError, error) {
	return s.GetErrorsForOrg(0, service, limit)
}

func (s *ErrorService) GetErrorsForOrg(orgID int64, service, limit string) ([]models.ApplicationError, error) {
	query := `SELECT id, organization_id, name, message, file, line, timestamp, service, http_method, http_url, http_headers, http_body
			  FROM application_errors WHERE 1=1`
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

	if limit == "" {
		limit = "100"
	}
	query += " ORDER BY timestamp DESC LIMIT $" + strconv.Itoa(argPos)
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrErrorsFetch, err)
	}
	defer rows.Close()

	var errors []models.ApplicationError
	for rows.Next() {
		var e models.ApplicationError
		var httpMethod, httpURL, httpHeaders, httpBody sql.NullString
		var orgIDNull sql.NullInt64
		if err := rows.Scan(&e.ID, &orgIDNull, &e.Name, &e.Message, &e.File, &e.Line, &e.Timestamp, &e.Service, &httpMethod, &httpURL, &httpHeaders, &httpBody); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrErrorScan, err)
		}
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
		errors = append(errors, e)
	}

	return errors, nil
}

// GetErrorByID returns an application error by ID for the given org (for the error detail endpoint)
func (s *ErrorService) GetErrorByID(id int64, orgID int64) (*models.ApplicationError, error) {
	query := `SELECT id, organization_id, name, message, file, line, timestamp, service, http_method, http_url, http_headers, http_body
			  FROM application_errors WHERE id = $1 AND organization_id = $2`
	var e models.ApplicationError
	var httpMethod, httpURL, httpHeaders, httpBody sql.NullString
	err := s.db.QueryRow(query, id, orgID).Scan(&e.ID, &e.OrganizationID, &e.Name, &e.Message, &e.File, &e.Line, &e.Timestamp, &e.Service, &httpMethod, &httpURL, &httpHeaders, &httpBody)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
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
	return &e, nil
}

// GetErrorsGroupedForOrg returns errors grouped by (name, message, file) with event count and timeseries for sparkline.
// When startDate/endDate are provided (RFC3339), they override window. Otherwise window is "24h" (hourly) or "14d" (daily).
func (s *ErrorService) GetErrorsGroupedForOrg(orgID int64, service, limit, window, startDate, endDate string) ([]models.ErrorGroup, error) {
	if orgID <= 0 {
		orgID = 1
	}
	var since, until time.Time
	var bucketFormat string
	if startDate != "" && endDate != "" {
		var err error
		since, err = time.Parse(time.RFC3339, startDate)
		if err != nil {
			return nil, fmt.Errorf("start_date: %w: %w", ErrDateInvalid, err)
		}
		until, err = time.Parse(time.RFC3339, endDate)
		if err != nil {
			return nil, fmt.Errorf("end_date: %w: %w", ErrDateInvalid, err)
		}
		since = since.UTC()
		until = until.UTC()
		if since.After(until) {
			since, until = until, since
		}
		hours := until.Sub(since).Hours()
		if hours >= 24*7 {
			bucketFormat = "day"
		} else {
			bucketFormat = "hour"
		}
	} else {
		switch strings.ToLower(window) {
		case "14d", "14days":
			since = time.Now().UTC().AddDate(0, 0, -14)
			until = time.Now().UTC()
			bucketFormat = "day"
		default:
			since = time.Now().UTC().Add(-24 * time.Hour)
			until = time.Now().UTC()
			bucketFormat = "hour"
		}
	}

	// http_headers and http_body are deliberately not selected: they are only
	// read from an error's detail, and one copy per group inflates the list.
	query := `SELECT id, organization_id, name, message, file, line, timestamp, service, http_method, http_url, trace_id
			  FROM application_errors WHERE organization_id = $1 AND timestamp >= $2::timestamptz AND timestamp <= $3::timestamptz`
	args := []interface{}{orgID, since, until}
	argPos := 4
	if service != "" {
		query += " AND service = $" + strconv.Itoa(argPos)
		args = append(args, service)
		argPos++
	}
	if limit == "" {
		limit = "2000"
	}
	query += " ORDER BY timestamp DESC LIMIT $" + strconv.Itoa(argPos)
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrErrorsFetch, err)
	}
	defer rows.Close()

	type row struct {
		e  models.ApplicationError
		ts time.Time
	}
	var all []row
	for rows.Next() {
		var e models.ApplicationError
		var httpMethod, httpURL, traceID sql.NullString
		var orgIDNull sql.NullInt64
		if err := rows.Scan(&e.ID, &orgIDNull, &e.Name, &e.Message, &e.File, &e.Line, &e.Timestamp, &e.Service, &httpMethod, &httpURL, &traceID); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrErrorScan, err)
		}
		if traceID.Valid && traceID.String != "" {
			e.TraceID = &traceID.String
		}
		if orgIDNull.Valid {
			e.OrganizationID = orgIDNull.Int64
		}
		if httpMethod.Valid {
			e.HTTPMethod = &httpMethod.String
		}
		if httpURL.Valid {
			e.HTTPURL = &httpURL.String
		}
		all = append(all, row{e: e, ts: e.Timestamp})
	}

	// Group by (name, message, file)
	type key struct {
		name, message, file string
	}
	type acc struct {
		eventCount   int64
		firstSeen    time.Time
		lastSeen     time.Time
		sampleID     int64
		sample       models.ApplicationError
		bucketCounts map[string]int64
	}
	groupAcc := make(map[key]*acc)
	for _, r := range all {
		k := key{name: r.e.Name, message: r.e.Message, file: r.e.File}
		bk := bucketKeyFor(r.ts, bucketFormat)
		if a, ok := groupAcc[k]; ok {
			a.eventCount++
			if r.ts.Before(a.firstSeen) {
				a.firstSeen = r.ts
			}
			if r.ts.After(a.lastSeen) {
				a.lastSeen = r.ts
				a.sampleID = r.e.ID
				a.sample = r.e
			}
			a.bucketCounts[bk]++
		} else {
			groupAcc[k] = &acc{
				eventCount:   1,
				firstSeen:    r.ts,
				lastSeen:     r.ts,
				sampleID:     r.e.ID,
				sample:       r.e,
				bucketCounts: map[string]int64{bk: 1},
			}
		}
	}

	out := make([]models.ErrorGroup, 0, len(groupAcc))
	for _, a := range groupAcc {
		out = append(out, models.ErrorGroup{
			EventCount: a.eventCount,
			FirstSeen:  a.firstSeen,
			LastSeen:   a.lastSeen,
			SampleID:   a.sampleID,
			Sample:     a.sample,
			Timeseries: bucketCountsToTimeseries(a.bucketCounts, bucketFormat, since, until),
		})
	}
	// Sort by last_seen desc
	sortErrorGroupsByLastSeen(out)
	return out, nil
}

// bucketKeyFor returns a key for the time bucket (e.g. "2025-03-05T14" for hour, "2025-03-05" for day).
func bucketKeyFor(t time.Time, bucketFormat string) string {
	if bucketFormat == "day" {
		return t.UTC().Format("2006-01-02")
	}
	return t.UTC().Format("2006-01-02T15")
}

// bucketCountsToTimeseries converts map[bucketKey]count to ordered []TimeSeriesPoint.
func bucketCountsToTimeseries(m map[string]int64, bucketFormat string, since, until time.Time) []models.TimeSeriesPoint {
	if len(m) == 0 {
		return nil
	}
	var points []models.TimeSeriesPoint
	const maxBuckets = 100
	if bucketFormat == "day" {
		for t := since.Truncate(24 * time.Hour); !t.After(until) && len(points) < maxBuckets; t = t.AddDate(0, 0, 1) {
			key := t.Format("2006-01-02")
			c := m[key]
			points = append(points, models.TimeSeriesPoint{Date: key, Count: c})
		}
	} else {
		for t := since.Truncate(time.Hour); !t.After(until) && len(points) < maxBuckets; t = t.Add(time.Hour) {
			key := t.Format("2006-01-02T15")
			c := m[key]
			points = append(points, models.TimeSeriesPoint{Date: key, Count: c})
		}
	}
	return points
}

func sortErrorGroupsByLastSeen(groups []models.ErrorGroup) {
	// simple sort by LastSeen desc
	for i := 0; i < len(groups); i++ {
		for j := i + 1; j < len(groups); j++ {
			if groups[j].LastSeen.After(groups[i].LastSeen) {
				groups[i], groups[j] = groups[j], groups[i]
			}
		}
	}
}
