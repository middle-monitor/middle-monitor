package services

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// Every correlation leg — infra, neighbours, linked apps, traffic — observes the
// same slice of time around the incident. A merged hypothesis in the RCA dossier
// ("the host saturated under this traffic spike") is only honest if its halves
// looked at the same window; with a shorter one on the infra side, a CPU peak ten
// minutes before an error stays invisible while the spike that caused it shows.
const (
	incidentWindowBefore = 15 * time.Minute
	incidentWindowAfter  = 5 * time.Minute
)

type CorrelationService struct {
	db *sql.DB
}

func NewCorrelationService(db *sql.DB) *CorrelationService {
	return &CorrelationService{db: db}
}

func (s *CorrelationService) GetCorrelationForError(orgID, errorID int64) (*models.CorrelationResult, error) {
	// 1. Fetch the ApplicationError
	var appErr models.ApplicationError
	var fingerprint sql.NullString
	queryErr := `
		SELECT id, name, message, file, line, timestamp, service, http_method, http_url, http_headers, http_body, fingerprint
		FROM application_errors
		WHERE id = $1 AND organization_id = $2
	`
	err := s.db.QueryRow(queryErr, errorID, orgID).Scan(
		&appErr.ID, &appErr.Name, &appErr.Message, &appErr.File, &appErr.Line,
		&appErr.Timestamp, &appErr.Service,
		&appErr.HTTPMethod, &appErr.HTTPURL, &appErr.HTTPHeaders, &appErr.HTTPBody, &fingerprint,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrErrorFetch, err)
	}
	if fingerprint.Valid {
		appErr.Fingerprint = fingerprint.String
	}
	// Fall back to computing the fingerprint on the fly for legacy rows that
	// were ingested before the column existed.
	if appErr.Fingerprint == "" {
		appErr.Fingerprint = ComputeErrorFingerprint(appErr.Name, appErr.Message, appErr.File)
	}

	result := &models.CorrelationResult{
		HasCorrelation:  false,
		Infra:           make([]models.InfraCorrelation, 0),
		Services:        make([]models.ServiceCorrelation, 0),
		Apps:            make([]models.AppCorrelation, 0),
		SemanticMatches: make([]string, 0),
	}

	timeStart := appErr.Timestamp.Add(-incidentWindowBefore)
	timeEnd := appErr.Timestamp.Add(incidentWindowAfter)

	// 2. Try to find the associated Host: by naming convention first, then by the
	// explicit app -> host link, which is the only reliable signal when the app
	// tag and the host's service name differ.
	var hostID int64
	var hostName string
	queryHost := `SELECT id, name FROM hosts WHERE organization_id = $1 AND service = $2 ORDER BY id LIMIT 1`
	err = s.db.QueryRow(queryHost, orgID, appErr.Service).Scan(&hostID, &hostName)
	if err != nil {
		queryLinkedHost := `
			SELECT h.id, h.name
			FROM application_links al
			JOIN hosts h ON h.id = al.target_id
			WHERE al.organization_id = $1 AND al.app_service_name = $2 AND al.target_type = 'host'
			ORDER BY al.id ASC LIMIT 1`
		err = s.db.QueryRow(queryLinkedHost, orgID, appErr.Service).Scan(&hostID, &hostName)
	}
	if err == nil {
		result.HostID = &hostID
		result.HostName = &hostName
	}

	// 3. Neighbours: failures co-occurring within the SAME host / host group as the
	// app (never env-wide). The scope is resolved from the app's links
	// (host_group / host / service); without a link there is no neighbour scope.
	scopeHostIDs, scopeGroupIDs := s.resolveCorrelationScope(orgID, appErr.Service)

	// 4. Infrastructure saturation, over the app's whole link scope rather than
	// the single directly-named host: an app linked to a group or to a service
	// still runs on hosts whose saturation explains its errors.
	result.Infra = append(result.Infra, s.infraSaturation(mergeHostIDs(hostID, scopeHostIDs), timeStart, timeEnd, 90.0)...)
	if len(scopeHostIDs) > 0 {
		queryFailingServices := fmt.Sprintf(`
			SELECT s.id, s.name, MIN(sr.timestamp) AS first_fail, (ARRAY_AGG(sr.message ORDER BY sr.timestamp))[1]
			FROM service_results sr
			JOIN services s ON s.id = sr.service_id
			WHERE s.organization_id = $1 AND sr.status = 'failure'
			  AND sr.timestamp >= $2 AND sr.timestamp <= $3
			  AND s.type NOT LIKE 'agent_%%'
			  AND s.host_id IN (%s)
			GROUP BY s.id, s.name
			ORDER BY first_fail ASC
			LIMIT 5
		`, intInClause(scopeHostIDs))
		rows, err := s.db.Query(queryFailingServices, orgID, timeStart, timeEnd)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var sid int64
				var sname string
				var firstFail time.Time
				var serror sql.NullString
				if err := rows.Scan(&sid, &sname, &firstFail, &serror); err == nil {
					desc := "The service failed"
					if serror.Valid && serror.String != "" {
						desc = "Failure: " + serror.String
					}
					preceded := firstFail.Before(appErr.Timestamp)
					ts := firstFail
					result.Services = append(result.Services, models.ServiceCorrelation{
						ServiceID:        sid,
						ServiceName:      sname,
						Status:           "critical",
						Description:      desc,
						OccurredAt:       &ts,
						PrecededIncident: preceded,
						Confidence:       neighbourConfidence(preceded),
					})
				}
			}
		}
	}

	// 4b. Directly linked applications (app -> app correlation links), both
	// directions: dependencies this app declares, and dependents that declare
	// this app. A dependency erroring just before the subject is the strongest
	// app-level cause signal; a dependent erroring is downstream-impact context.
	linkedAppSeen := map[string]bool{}
	dependencies, dependents := s.getLinkedAppNames(orgID, appErr.Service)
	for _, la := range s.queryLinkedAppErrors(orgID, dependencies, "dependency", timeStart, timeEnd, appErr.Timestamp) {
		linkedAppSeen[la.Service] = true
		result.Apps = append(result.Apps, la)
	}
	for _, la := range s.queryLinkedAppErrors(orgID, dependents, "dependent", timeStart, timeEnd, appErr.Timestamp) {
		if linkedAppSeen[la.Service] {
			continue
		}
		linkedAppSeen[la.Service] = true
		result.Apps = append(result.Apps, la)
	}

	// 4c. Other applications linked to the same host group that also errored in
	// the same window — co-occurrence CONTEXT, not a proven cause.
	if len(scopeGroupIDs) > 0 {
		queryCoApps := fmt.Sprintf(`
			SELECT ae.service, ae.name, COUNT(*), MIN(ae.timestamp)
			FROM application_errors ae
			WHERE ae.organization_id = $1
			  AND ae.timestamp >= $2 AND ae.timestamp <= $3
			  AND ae.service <> $4
			  AND EXISTS (
				SELECT 1 FROM application_links al
				WHERE al.organization_id = $1 AND al.app_service_name = ae.service
				  AND al.target_type = 'host_group' AND al.target_id IN (%s))
			GROUP BY ae.service, ae.name
			ORDER BY MIN(ae.timestamp) ASC
			LIMIT 5
		`, intInClause(scopeGroupIDs))
		rows, err := s.db.Query(queryCoApps, orgID, timeStart, timeEnd, appErr.Service)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var a models.AppCorrelation
				var firstSeen time.Time
				if err := rows.Scan(&a.Service, &a.ErrorName, &a.Count, &firstSeen); err == nil {
					if linkedAppSeen[a.Service] {
						continue
					}
					ts := firstSeen
					a.OccurredAt = &ts
					a.PrecededIncident = firstSeen.Before(appErr.Timestamp)
					a.Relation = "host_group"
					a.Confidence = 0.4
					result.Apps = append(result.Apps, a)
				}
			}
		}
	}

	// 5. Semantic matches
	msgLower := strings.ToLower(appErr.Message)
	if strings.Contains(msgLower, "connection refused") || strings.Contains(msgLower, "dial tcp") {
		result.SemanticMatches = append(result.SemanticMatches, "Network connection problem or service unreachable (connection refused / dial tcp)")
	}
	if strings.Contains(msgLower, "timeout") || strings.Contains(msgLower, "context deadline exceeded") {
		result.SemanticMatches = append(result.SemanticMatches, "Request timed out, possibly due to overload or a network stall")
	}
	if strings.Contains(msgLower, "no space left") {
		result.SemanticMatches = append(result.SemanticMatches, "Disk full (no space left on device)")
	}
	if strings.Contains(msgLower, "out of memory") || strings.Contains(msgLower, "oom") {
		result.SemanticMatches = append(result.SemanticMatches, "Out of memory (OOM)")
	}

	// 6. Recurrence: how often this exact error (by fingerprint) occurs.
	result.Recurrence = s.computeRecurrence(orgID, appErr.Fingerprint, appErr.Timestamp)

	result.HasCorrelation = len(result.Infra) > 0 || len(result.Services) > 0 || len(result.Apps) > 0 || len(result.SemanticMatches) > 0
	result.Confidence = overallConfidence(result)

	// 7. Generate Summary
	if result.HasCorrelation || (result.Recurrence != nil && result.Recurrence.Description != "") {
		var parts []string
		if result.Recurrence != nil && result.Recurrence.Description != "" {
			parts = append(parts, result.Recurrence.Description)
		}
		if len(result.SemanticMatches) > 0 {
			parts = append(parts, "The error message points to a problem of type: "+result.SemanticMatches[0]+".")
		}
		if len(result.Services) > 0 {
			parts = append(parts, describeNeighbours(result.Services))
		}
		if d := describeCoApps(result.Apps); d != "" {
			parts = append(parts, d)
		}
		if len(result.Infra) > 0 {
			parts = append(parts, fmt.Sprintf("Infrastructure shows signs of saturation (%s).", result.Infra[0].MetricName))
		}
		result.Summary = strings.Join(parts, " ")
	} else {
		result.Summary = "No infrastructure or service correlation was found for this error."
	}

	return result, nil
}

func (s *CorrelationService) GetCorrelationForServiceResult(orgID, resultID int64) (*models.CorrelationResult, error) {
	// 1. Fetch the ServiceResult
	var sr models.ServiceResult
	var serviceName, svcType sql.NullString
	var hostID sql.NullInt64
	var hostName sql.NullString
	querySR := `
		SELECT sr.id, sr.service_id, sr.status, sr.message, sr.timestamp, s.service, s.type, s.host_id, h.name
		FROM service_results sr
		JOIN services s ON s.id = sr.service_id
		LEFT JOIN hosts h ON h.id = s.host_id
		WHERE sr.id = $1 AND s.organization_id = $2
	`
	err := s.db.QueryRow(querySR, resultID, orgID).Scan(
		&sr.ID, &sr.ServiceID, &sr.Status, &sr.Message, &sr.Timestamp, &serviceName, &svcType, &hostID, &hostName,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServiceResultFetch, err)
	}

	result := &models.CorrelationResult{
		HasCorrelation:  false,
		Infra:           make([]models.InfraCorrelation, 0),
		Services:        make([]models.ServiceCorrelation, 0),
		Apps:            make([]models.AppCorrelation, 0),
		SemanticMatches: make([]string, 0),
	}

	if hostID.Valid {
		hID := hostID.Int64
		result.HostID = &hID
		if hostName.Valid {
			result.HostName = &hostName.String
		}
	}

	timeStart := sr.Timestamp.Add(-incidentWindowBefore)
	timeEnd := sr.Timestamp.Add(incidentWindowAfter)

	// 2. Infrastructure saturation on the check's host during the failure window.
	var svcStr string
	if serviceName.Valid {
		svcStr = serviceName.String
	}
	if hostID.Valid {
		result.Infra = append(result.Infra, s.infraSaturation([]int64{hostID.Int64}, timeStart, timeEnd, 85.0)...)
	}

	// Neighbours: services in the SAME host group that also failed in the window
	// (never env-wide). Scope is the failing service's host + its group; without a
	// host there is no scope. Exclude checks probing the SAME logical service
	// (s.service), since duplicate endpoint checks are not independent neighbours.
	if hostID.Valid {
		scope := s.hostGroupScope(orgID, hostID.Int64)
		queryFailingServices := fmt.Sprintf(`
			SELECT s.id, s.name, MIN(sr.timestamp) AS first_fail, (ARRAY_AGG(sr.message ORDER BY sr.timestamp))[1]
			FROM service_results sr
			JOIN services s ON s.id = sr.service_id
			WHERE s.organization_id = $1 AND sr.status = 'failure' AND s.service != $4
			  AND sr.timestamp >= $2 AND sr.timestamp <= $3
			  AND s.type NOT LIKE 'agent_%%'
			  AND s.host_id IN (%s)
			GROUP BY s.id, s.name
			ORDER BY first_fail ASC
			LIMIT 5
		`, intInClause(scope))
		rows, err := s.db.Query(queryFailingServices, orgID, timeStart, timeEnd, svcStr)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var sid int64
				var sname string
				var firstFail time.Time
				var serror sql.NullString
				if err := rows.Scan(&sid, &sname, &firstFail, &serror); err == nil {
					desc := "The service failed"
					if serror.Valid && serror.String != "" {
						desc = "Failure: " + serror.String
					}
					preceded := firstFail.Before(sr.Timestamp)
					ts := firstFail
					result.Services = append(result.Services, models.ServiceCorrelation{
						ServiceID:        sid,
						ServiceName:      sname,
						Status:           "critical",
						Description:      desc,
						OccurredAt:       &ts,
						PrecededIncident: preceded,
						Confidence:       neighbourConfidence(preceded),
					})
				}
			}
		}

		// Neighbours that merely slowed down: a check failing outright is not the
		// only way a co-hosted service shows strain. Without this, a transfer
		// saturating one host stays invisible while its neighbours' HTTP and ping
		// latency triples.
		result.Degraded = s.degradedNeighbours(orgID, scope, sr.ServiceID, timeStart, timeEnd)
	}

	// 4. Semantic matches
	if sr.Message != nil {
		msgLower := strings.ToLower(*sr.Message)
		if strings.Contains(msgLower, "connection refused") || strings.Contains(msgLower, "dial tcp") || strings.Contains(msgLower, "no such host") {
			result.SemanticMatches = append(result.SemanticMatches, "Network connection problem or DNS unreachable")
		}
		if strings.Contains(msgLower, "timeout") || strings.Contains(msgLower, "context deadline exceeded") {
			result.SemanticMatches = append(result.SemanticMatches, "Request timed out: check network or CPU load")
		}
		if strings.Contains(msgLower, "certificate") || strings.Contains(msgLower, "x509") {
			result.SemanticMatches = append(result.SemanticMatches, "SSL/TLS certificate problem (expired or invalid)")
		}
	}

	result.HasCorrelation = len(result.Infra) > 0 || len(result.Services) > 0 || len(result.Degraded) > 0 || len(result.SemanticMatches) > 0
	result.Confidence = overallConfidence(result)

	// 5. Generate Summary
	if result.HasCorrelation {
		var parts []string
		if len(result.SemanticMatches) > 0 {
			parts = append(parts, result.SemanticMatches[0]+".")
		}
		if len(result.Services) > 0 {
			parts = append(parts, describeNeighbours(result.Services))
		}
		if len(result.Degraded) > 0 {
			parts = append(parts, describeDegraded(result.Degraded))
		}
		if len(result.Infra) > 0 {
			parts = append(parts, fmt.Sprintf("Infrastructure shows signs of saturation (%s).", result.Infra[0].MetricName))
		}
		result.Summary = strings.Join(parts, " ")
	} else {
		result.Summary = "No infrastructure or service correlation was found for this failure."
	}

	return result, nil
}

var agentMetricLabels = map[string]string{
	"agent_cpu":  "CPU",
	"agent_ram":  "RAM",
	"agent_disk": "Disk",
}

// mergeHostIDs unions a directly resolved host with a link-derived scope.
func mergeHostIDs(hostID int64, scope []int64) []int64 {
	out := make([]int64, 0, len(scope)+1)
	seen := map[int64]bool{}
	for _, id := range append([]int64{hostID}, scope...) {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// infraSaturation returns the agent metrics of the given hosts that peaked above
// their own alerting threshold during the window, falling back to
// defaultThreshold when a metric has none. It reads the MAX over the window, not
// the latest sample: a spike that already receded by the time the incident was
// recorded is still what caused it. Worst peak first.
func (s *CorrelationService) infraSaturation(hostIDs []int64, timeStart, timeEnd time.Time, defaultThreshold float64) []models.InfraCorrelation {
	out := []models.InfraCorrelation{}
	if len(hostIDs) == 0 {
		return out
	}
	query := fmt.Sprintf(`
		SELECT s.type, COALESCE(h.name, ''), COALESCE(s.critical_threshold, s.warning_threshold, $3), MAX(sr.metric_value) AS peak
		FROM service_results sr
		JOIN services s ON s.id = sr.service_id
		LEFT JOIN hosts h ON h.id = s.host_id
		WHERE s.host_id IN (%s)
		  AND s.type IN ('agent_cpu', 'agent_ram', 'agent_disk')
		  AND sr.metric_value IS NOT NULL
		  AND sr.timestamp >= $1 AND sr.timestamp <= $2
		GROUP BY s.type, h.name, s.critical_threshold, s.warning_threshold
		ORDER BY peak DESC`, intInClause(hostIDs))
	rows, err := s.db.Query(query, timeStart, timeEnd, defaultThreshold)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var svcType, host string
		var threshold, peak float64
		if err := rows.Scan(&svcType, &host, &threshold, &peak); err != nil || peak <= threshold {
			continue
		}
		if host == "" {
			host = "Host"
		}
		label := agentMetricLabels[svcType]
		out = append(out, models.InfraCorrelation{
			MetricName:  label,
			Value:       peak,
			Threshold:   threshold,
			Description: fmt.Sprintf("%s %s peaked at %.1f%% during the incident window (threshold %.0f%%)", host, label, peak, threshold),
			Confidence:  saturationConfidence(peak, threshold),
		})
	}
	return out
}

// saturationConfidence maps a metric value above a threshold to a 0.6-0.95
// confidence: the further above the threshold, the more confident we are.
func saturationConfidence(value, threshold float64) float64 {
	if value <= threshold {
		return 0.6
	}
	span := 100.0 - threshold
	if span <= 0 {
		return 0.9
	}
	ratio := (value - threshold) / span // 0..1
	conf := 0.6 + 0.35*ratio
	if conf > 0.95 {
		conf = 0.95
	}
	return round2(conf)
}

// neighbourConfidence rates a co-failing neighbour. One that broke before the
// incident is a stronger upstream-cause signal than one that broke after.
func neighbourConfidence(precededIncident bool) float64 {
	if precededIncident {
		return 0.75
	}
	return 0.45
}

// overallConfidence collapses the individual signals into a single 0-1 score,
// taking the strongest signal and nudging it up when several signals agree.
func overallConfidence(r *models.CorrelationResult) float64 {
	best := 0.0
	signals := 0
	for _, i := range r.Infra {
		signals++
		if i.Confidence > best {
			best = i.Confidence
		}
	}
	for _, s := range r.Services {
		signals++
		if s.Confidence > best {
			best = s.Confidence
		}
	}
	// Apps: a directly linked dependency can be a strong signal; host-group
	// co-occurrence stays a weak corroborating one. Zero-confidence entries
	// (legacy callers) fall back to the historical 0.4.
	for _, a := range r.Apps {
		signals++
		c := a.Confidence
		if c == 0 {
			c = 0.4
		}
		if c > best {
			best = c
		}
	}
	if len(r.SemanticMatches) > 0 {
		signals += len(r.SemanticMatches)
		if 0.6 > best {
			best = 0.6
		}
	}
	if r.Recurrence != nil && (r.Recurrence.IsNew || r.Recurrence.IsRecurrent) {
		signals++
		if 0.65 > best {
			best = 0.65
		}
	}
	if signals == 0 {
		return 0.0
	}
	// Corroboration bonus: multiple independent signals raise confidence.
	if signals >= 2 {
		best += 0.05
	}
	if signals >= 3 {
		best += 0.05
	}
	if best > 0.98 {
		best = 0.98
	}
	return round2(best)
}

// describeNeighbours summarizes services in the same host group that failed in
// the window. It notes which failed first as context (not proven causation).
func describeNeighbours(services []models.ServiceCorrelation) string {
	if len(services) == 0 {
		return ""
	}
	preceding := make([]string, 0, len(services))
	for _, s := range services {
		if s.PrecededIncident {
			preceding = append(preceding, s.ServiceName)
		}
	}
	if len(preceding) > 0 {
		return fmt.Sprintf("In the same host group, %d service(s) also failed around this time; %s failed first.",
			len(services), strings.Join(preceding, ", "))
	}
	return fmt.Sprintf("In the same host group, %d service(s) also failed at the same time.", len(services))
}

// describeCoApps summarizes other applications that also errored in the
// window: declared dependencies first (cause-oriented), then same-host-group
// co-occurrence (context only). Dependents are impact, not cause — left out.
func describeCoApps(apps []models.AppCorrelation) string {
	var deps, groupApps []string
	for _, a := range apps {
		switch a.Relation {
		case "dependency":
			if a.PrecededIncident {
				deps = append(deps, a.Service+" (errored first)")
			} else {
				deps = append(deps, a.Service)
			}
		case "dependent":
			// Downstream impact; not part of the cause-oriented summary.
		default:
			groupApps = append(groupApps, a.Service)
		}
	}
	var parts []string
	if len(deps) > 0 {
		parts = append(parts, fmt.Sprintf("A linked dependency also reported errors: %s.", strings.Join(deps, ", ")))
	}
	if len(groupApps) > 0 {
		parts = append(parts, fmt.Sprintf("At the same time, %d other app(s) in the same host group also reported errors: %s.",
			len(groupApps), strings.Join(groupApps, ", ")))
	}
	return strings.Join(parts, " ")
}

// resolveCorrelationScope resolves an app's links into the set of host IDs and
// host-group IDs that correlation may look at. Correlation never goes outside a
// host and its group: a host/service/app link is expanded to include that
// target's host's group, and a group link is expanded to all its hosts. No
// link => empty scope.
func (s *CorrelationService) resolveCorrelationScope(orgID int64, appService string) (hostIDs []int64, groupIDs []int64) {
	rows, err := s.db.Query(`
		SELECT target_type, target_id, COALESCE(target_app_name, '') FROM application_links
		WHERE organization_id = $1 AND app_service_name = $2`,
		orgID, appService)
	if err != nil {
		return nil, nil
	}
	hostSet := map[int64]bool{}
	groupSet := map[int64]bool{}
	addHostGroup := func(hostID int64) {
		var g sql.NullInt64
		if s.db.QueryRow(`SELECT host_group_id FROM hosts WHERE id = $1 AND organization_id = $2`, hostID, orgID).Scan(&g) == nil && g.Valid {
			groupSet[g.Int64] = true
		}
	}
	addServiceHost := func(query string, arg interface{}) {
		var hid sql.NullInt64
		if s.db.QueryRow(query, arg, orgID).Scan(&hid) == nil && hid.Valid {
			hostSet[hid.Int64] = true
			addHostGroup(hid.Int64)
		}
	}
	for rows.Next() {
		var tt, appName string
		var tid int64
		if rows.Scan(&tt, &tid, &appName) != nil {
			continue
		}
		switch tt {
		case "host_group":
			groupSet[tid] = true
		case "host":
			hostSet[tid] = true
			addHostGroup(tid)
		case "service":
			addServiceHost(`SELECT host_id FROM services WHERE id = $1 AND organization_id = $2`, tid)
		case "app":
			// Name-based app link: the target may or may not have a registered
			// services row; when it does, its host joins the scope. Legacy
			// id-based app links fall back to the id lookup.
			if appName != "" {
				addServiceHost(`SELECT host_id FROM services WHERE name = $1 AND organization_id = $2 AND type LIKE 'error_service_%' LIMIT 1`, appName)
			} else if tid > 0 {
				addServiceHost(`SELECT host_id FROM services WHERE id = $1 AND organization_id = $2`, tid)
			}
		}
	}
	rows.Close()

	// Expand every in-scope group to all of its hosts.
	for gid := range groupSet {
		hrows, herr := s.db.Query(`SELECT id FROM hosts WHERE host_group_id = $1 AND organization_id = $2`, gid, orgID)
		if herr != nil {
			continue
		}
		for hrows.Next() {
			var hid int64
			if hrows.Scan(&hid) == nil {
				hostSet[hid] = true
			}
		}
		hrows.Close()
	}
	for h := range hostSet {
		hostIDs = append(hostIDs, h)
	}
	for g := range groupSet {
		groupIDs = append(groupIDs, g)
	}
	return hostIDs, groupIDs
}

// getLinkedAppNames resolves this app's direct app->app correlation links in
// both directions: the app names it declares as dependencies, and the app
// names that declare it as their dependency (dependents). App targets point at
// services rows of type error_service_* whose name is the app tag.
func (s *CorrelationService) getLinkedAppNames(orgID int64, appService string) (dependencies []string, dependents []string) {
	// target_app_name is the canonical identifier; the services join only
	// resolves legacy id-based app links created before the name migration.
	rows, err := s.db.Query(`
		SELECT COALESCE(NULLIF(al.target_app_name, ''), s.name, '') FROM application_links al
		LEFT JOIN services s ON s.id = al.target_id
		WHERE al.organization_id = $1 AND al.app_service_name = $2 AND al.target_type = 'app'`,
		orgID, appService)
	if err == nil {
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil && name != "" {
				dependencies = append(dependencies, name)
			}
		}
		rows.Close()
	}
	rows, err = s.db.Query(`
		SELECT al.app_service_name FROM application_links al
		LEFT JOIN services s ON s.id = al.target_id
		WHERE al.organization_id = $1 AND al.target_type = 'app'
		  AND (al.target_app_name = $2 OR s.name = $2)`,
		orgID, appService)
	if err == nil {
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil && name != "" {
				dependents = append(dependents, name)
			}
		}
		rows.Close()
	}
	return dependencies, dependents
}

// queryLinkedAppErrors returns error groups reported by the given linked apps
// within the window, tagged with the relation and a confidence reflecting it.
func (s *CorrelationService) queryLinkedAppErrors(orgID int64, apps []string, relation string, timeStart, timeEnd, reference time.Time) []models.AppCorrelation {
	if len(apps) == 0 {
		return nil
	}
	placeholders := make([]string, len(apps))
	args := []interface{}{orgID, timeStart, timeEnd}
	for i, name := range apps {
		placeholders[i] = fmt.Sprintf("$%d", len(args)+1)
		args = append(args, name)
	}
	query := fmt.Sprintf(`
		SELECT ae.service, ae.name, COUNT(*), MIN(ae.timestamp)
		FROM application_errors ae
		WHERE ae.organization_id = $1
		  AND ae.timestamp >= $2 AND ae.timestamp <= $3
		  AND ae.service IN (%s)
		GROUP BY ae.service, ae.name
		ORDER BY MIN(ae.timestamp) ASC
		LIMIT 5
	`, strings.Join(placeholders, ","))
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []models.AppCorrelation
	for rows.Next() {
		var a models.AppCorrelation
		var firstSeen time.Time
		if rows.Scan(&a.Service, &a.ErrorName, &a.Count, &firstSeen) != nil {
			continue
		}
		ts := firstSeen
		a.OccurredAt = &ts
		a.PrecededIncident = firstSeen.Before(reference)
		a.Relation = relation
		a.Confidence = linkedAppConfidence(relation, a.PrecededIncident)
		out = append(out, a)
	}
	return out
}

// linkedAppConfidence rates a linked application's co-error: a declared
// dependency that errored before the subject is a strong upstream-cause
// signal; a dependent erroring is downstream-impact context, not a cause.
func linkedAppConfidence(relation string, preceded bool) float64 {
	if relation == "dependency" {
		if preceded {
			return 0.8
		}
		return 0.5
	}
	return 0.4
}

// hostGroupScope returns every host ID in the same group as hostID (including
// hostID itself). Used to scope a service failure's neighbours to its host group.
func (s *CorrelationService) hostGroupScope(orgID, hostID int64) []int64 {
	hostSet := map[int64]bool{hostID: true}
	var g sql.NullInt64
	if s.db.QueryRow(`SELECT host_group_id FROM hosts WHERE id = $1 AND organization_id = $2`, hostID, orgID).Scan(&g) == nil && g.Valid {
		rows, err := s.db.Query(`SELECT id FROM hosts WHERE host_group_id = $1 AND organization_id = $2`, g.Int64, orgID)
		if err == nil {
			for rows.Next() {
				var h int64
				if rows.Scan(&h) == nil {
					hostSet[h] = true
				}
			}
			rows.Close()
		}
	}
	out := make([]int64, 0, len(hostSet))
	for h := range hostSet {
		out = append(out, h)
	}
	return out
}

// degradedLatencyMultiplier: a neighbour counts as degraded once its median
// latency in the incident window reaches this multiple of its own baseline.
const degradedLatencyMultiplier = 3.0

// degradedBaselineWindow is how far back before the incident we look to learn
// what "normal" latency is for each neighbour check.
const degradedBaselineWindow = 6 * time.Hour

// degradedMinBaselineSamples guards against a single stray sample defining the
// baseline, which would turn ordinary jitter into a false correlation.
const degradedMinBaselineSamples = 5

// degradedNeighbours finds checks in the host group whose latency spiked during
// the window while still reporting success. Agent metric checks (cpu/ram/disk)
// leave latency NULL and drop out on their own; network checks store their ping
// latency there, so ICMP and HTTP degradation both surface here.
func (s *CorrelationService) degradedNeighbours(orgID int64, scope []int64, excludeServiceID int64, timeStart, timeEnd time.Time) []models.DegradedNeighbour {
	if len(scope) == 0 {
		return nil
	}
	baselineStart := timeStart.Add(-degradedBaselineWindow)
	query := fmt.Sprintf(`
		WITH win AS (
			SELECT sr.service_id,
			       percentile_cont(0.5) WITHIN GROUP (ORDER BY sr.latency::float8) AS med
			FROM service_results sr
			JOIN services s ON s.id = sr.service_id
			WHERE s.organization_id = $1 AND s.host_id IN (%s) AND s.id <> $2
			  AND sr.latency IS NOT NULL
			  AND sr.timestamp >= $3 AND sr.timestamp <= $4
			GROUP BY sr.service_id
		),
		base AS (
			SELECT sr.service_id,
			       percentile_cont(0.5) WITHIN GROUP (ORDER BY sr.latency::float8) AS med
			FROM service_results sr
			JOIN services s ON s.id = sr.service_id
			WHERE s.organization_id = $1 AND s.host_id IN (%s) AND s.id <> $2
			  AND sr.latency IS NOT NULL
			  AND sr.timestamp >= $5 AND sr.timestamp < $3
			GROUP BY sr.service_id
			HAVING COUNT(*) >= %d
		)
		SELECT s.id, s.name, COALESCE(h.name, ''), s.type, win.med, base.med
		FROM win
		JOIN base ON base.service_id = win.service_id
		JOIN services s ON s.id = win.service_id
		LEFT JOIN hosts h ON h.id = s.host_id
		WHERE base.med > 0 AND win.med >= base.med * $6
		ORDER BY win.med / base.med DESC
		LIMIT 5
	`, intInClause(scope), intInClause(scope), degradedMinBaselineSamples)

	rows, err := s.db.Query(query, orgID, excludeServiceID, timeStart, timeEnd, baselineStart, degradedLatencyMultiplier)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]models.DegradedNeighbour, 0, 5)
	for rows.Next() {
		var d models.DegradedNeighbour
		if err := rows.Scan(&d.ServiceID, &d.ServiceName, &d.HostName, &d.CheckType, &d.LatencyMS, &d.BaselineMS); err != nil {
			continue
		}
		// Divide before rounding: a sub-centisecond baseline rounds to 0 and
		// would turn the multiplier into +Inf, which JSON cannot encode.
		d.Multiplier = round2(d.LatencyMS / d.BaselineMS)
		d.LatencyMS = round2(d.LatencyMS)
		d.BaselineMS = round2(d.BaselineMS)
		out = append(out, d)
	}
	return out
}

// describeDegraded names the neighbours that slowed down, worst first.
func describeDegraded(items []models.DegradedNeighbour) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for i, d := range items {
		if i >= 3 {
			break
		}
		label := d.ServiceName
		if d.HostName != "" {
			label += " on " + d.HostName
		}
		parts = append(parts, fmt.Sprintf("%s (%.0fms vs %.0fms baseline)", label, d.LatencyMS, d.BaselineMS))
	}
	return fmt.Sprintf("Latency degraded on %s without failing.", strings.Join(parts, ", "))
}

// intInClause renders a slice of ints as a SQL IN-list body (e.g. "1,2,3").
// Inputs are DB-internal IDs (never user text), so direct interpolation is safe.
// Returns "NULL" for an empty slice so "IN (NULL)" matches nothing.
func intInClause(ids []int64) string {
	if len(ids) == 0 {
		return "NULL"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// round2 rounds to 2 decimals to keep confidence scores readable.
func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// computeRecurrence counts how often the same fingerprint occurred around the
// incident time, and classifies it as new (regression) or chronic (recurrent).
func (s *CorrelationService) computeRecurrence(orgID int64, fingerprint string, reference time.Time) *models.ErrorRecurrence {
	if fingerprint == "" {
		return nil
	}
	rec := &models.ErrorRecurrence{Fingerprint: fingerprint}
	var firstSeen, lastSeen sql.NullTime
	q := `
		SELECT
			COUNT(*) FILTER (WHERE timestamp >= $3::timestamptz - INTERVAL '1 hour' AND timestamp <= $3::timestamptz),
			COUNT(*) FILTER (WHERE timestamp >= $3::timestamptz - INTERVAL '24 hours' AND timestamp <= $3::timestamptz),
			COUNT(*) FILTER (WHERE timestamp >= $3::timestamptz - INTERVAL '7 days' AND timestamp <= $3::timestamptz),
			MIN(timestamp),
			MAX(timestamp)
		FROM application_errors
		WHERE organization_id = $1 AND fingerprint = $2
	`
	err := s.db.QueryRow(q, orgID, fingerprint, reference).Scan(
		&rec.CountLastHour, &rec.Count24h, &rec.Count7d, &firstSeen, &lastSeen,
	)
	if err != nil {
		return nil
	}
	if firstSeen.Valid {
		fs := firstSeen.Time
		rec.FirstSeen = &fs
	}
	if lastSeen.Valid {
		ls := lastSeen.Time
		rec.LastSeen = &ls
	}

	// New/regression: first time we ever saw this fingerprint is within ~1h of
	// the incident (nothing older exists).
	if rec.FirstSeen != nil && rec.FirstSeen.After(reference.Add(-1*time.Hour)) {
		rec.IsNew = true
	}
	// Recurrent/chronic: many occurrences spread over more than a day.
	if rec.Count7d >= 20 && rec.FirstSeen != nil && rec.FirstSeen.Before(reference.Add(-24*time.Hour)) {
		rec.IsRecurrent = true
	}

	switch {
	case rec.IsNew:
		rec.Description = fmt.Sprintf("New error (never seen before this hour): %d occurrence(s) in the last hour — likely a recent regression.", rec.CountLastHour)
	case rec.IsRecurrent:
		rec.Description = fmt.Sprintf("Recurring error: %d occurrences over 7 days (%d in 24h). Known chronic issue.", rec.Count7d, rec.Count24h)
	case rec.CountLastHour > 1:
		rec.Description = fmt.Sprintf("This error repeated %d times in the last hour.", rec.CountLastHour)
	}
	return rec
}
