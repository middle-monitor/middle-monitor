package services

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

type AlertService struct {
	db *sql.DB
}

func NewAlertService(db *sql.DB) *AlertService {
	return &AlertService{db: db}
}

// LiveIncidentStatuses is the SQL list of statuses an incident still occupies
// while it is ongoing. Acknowledging says a human picked the incident up, not
// that the condition cleared: an acknowledged incident must still dedup new
// alerts for the same target, and is still what auto-resolution closes. Every
// dedup and auto-resolve query filters on this, so the two can never disagree.
const LiveIncidentStatuses = "('open','acknowledged')"

// ---- Notification Channels ----

func (s *AlertService) CreateChannel(channel models.NotificationChannel) (*models.NotificationChannel, error) {
	configJSON, err := json.Marshal(channel.Config)
	if err != nil {
		configJSON = []byte("{}")
	}

	query := `INSERT INTO notification_channels (organization_id, name, type, config, enabled)
			  VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at, updated_at`
	err = s.db.QueryRow(query, channel.OrganizationID, channel.Name, channel.Type, configJSON, channel.Enabled).Scan(&channel.ID, &channel.CreatedAt, &channel.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotificationChannelCreate, err)
	}
	return &channel, nil
}

func (s *AlertService) GetChannels(orgID int64) ([]models.NotificationChannel, error) {
	query := `SELECT id, organization_id, name, type, config, enabled, created_at, updated_at
			  FROM notification_channels WHERE organization_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.Query(query, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotificationChannelsFetch, err)
	}
	defer rows.Close()

	var channels []models.NotificationChannel
	for rows.Next() {
		var c models.NotificationChannel
		var configJSON []byte
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Type, &configJSON, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotificationChannelScan, err)
		}
		if len(configJSON) > 0 {
			json.Unmarshal(configJSON, &c.Config)
		}
		if c.Config == nil {
			c.Config = map[string]interface{}{}
		}
		channels = append(channels, c)
	}
	return channels, nil
}

func (s *AlertService) UpdateChannel(channelID, orgID int64, channel models.NotificationChannel) (*models.NotificationChannel, error) {
	configJSON, err := json.Marshal(channel.Config)
	if err != nil {
		configJSON = []byte("{}")
	}

	query := `UPDATE notification_channels SET name=$1, type=$2, config=$3, enabled=$4, updated_at=$5
			  WHERE id=$6 AND organization_id=$7 RETURNING id, organization_id, name, type, config, enabled, created_at, updated_at`
	var c models.NotificationChannel
	var dbConfigJSON []byte
	err = s.db.QueryRow(query, channel.Name, channel.Type, configJSON, channel.Enabled, time.Now().UTC(), channelID, orgID).Scan(
		&c.ID, &c.OrganizationID, &c.Name, &c.Type, &dbConfigJSON, &c.Enabled, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotificationChannelUpdate, err)
	}
	if len(dbConfigJSON) > 0 {
		json.Unmarshal(dbConfigJSON, &c.Config)
	}
	if c.Config == nil {
		c.Config = map[string]interface{}{}
	}
	return &c, nil
}

func (s *AlertService) DeleteChannel(channelID, orgID int64) error {
	result, err := s.db.Exec(`DELETE FROM notification_channels WHERE id = $1 AND organization_id = $2`, channelID, orgID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotificationChannelDelete, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrChannelNotFound
	}
	return nil
}

// ---- Alert Rules (CRUD) ----

// marshalMetricLabels always yields a JSON array: the column is NOT NULL, and a
// rule with no label constraint must store [] rather than null.
func marshalMetricLabels(labels []models.MetricLabel) []byte {
	if len(labels) == 0 {
		return []byte("[]")
	}
	raw, err := json.Marshal(labels)
	if err != nil {
		return []byte("[]")
	}
	return raw
}

func (s *AlertService) GetAlertRules(orgID int64) ([]models.AlertRule, error) {
	rows, err := s.db.Query(`
		SELECT id, organization_id, name, description, type, target_type, target_id,
		       metric, operator, threshold, duration, severity, enabled, channels,
		       COALESCE(aggregation, 'avg'), warning_threshold, critical_threshold, recovery_threshold,
		       COALESCE(tags, ''), COALESCE(notify_warning, true), COALESCE(notify_critical, true),
		       created_at, updated_at, custom_metric, COALESCE(custom_labels, '[]'::jsonb)
		FROM alert_rules WHERE organization_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertRules(rows)
}

func (s *AlertService) CreateAlertRule(r models.AlertRule) (*models.AlertRule, error) {
	channelsJSON, _ := json.Marshal(r.Channels)
	if r.Aggregation == "" {
		r.Aggregation = "avg"
	}
	customLabelsJSON := marshalMetricLabels(r.CustomLabels)
	isCustom := r.CustomMetric != nil && *r.CustomMetric != ""
	// Routing rules carry no metric/threshold (those live on the service). A
	// custom-metric rule has its own threshold, so it must not be coerced into
	// one just because `metric` names no built-in signal.
	if r.Metric == "" && !isCustom {
		r.Metric = "routing"
	}
	if r.Metric == "" {
		r.Metric = "custom"
	}
	if r.Type == "" {
		r.Type = "routing"
	}
	if isCustom && r.Type == "routing" {
		r.Type = "threshold"
	}
	err := s.db.QueryRow(`
		INSERT INTO alert_rules (organization_id, name, description, type, target_type, target_id,
		                         metric, operator, threshold, duration, severity, enabled, channels,
		                         aggregation, warning_threshold, critical_threshold, recovery_threshold,
		                         tags, notify_warning, notify_critical, custom_metric, custom_labels)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
		RETURNING id, created_at, updated_at`,
		r.OrganizationID, r.Name, r.Description, r.Type, r.TargetType, r.TargetID,
		r.Metric, r.Operator, r.Threshold, r.Duration, r.Severity, r.Enabled, channelsJSON,
		r.Aggregation, r.WarningThreshold, r.CriticalThreshold, r.RecoveryThreshold,
		r.Tags, r.NotifyWarning, r.NotifyCritical, r.CustomMetric, customLabelsJSON,
	).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAlertRuleCreate, err)
	}
	return &r, nil
}

func (s *AlertService) UpdateAlertRule(ruleID, orgID int64, r models.AlertRule) (*models.AlertRule, error) {
	channelsJSON, _ := json.Marshal(r.Channels)
	if r.Aggregation == "" {
		r.Aggregation = "avg"
	}
	now := time.Now().UTC()
	var warn, crit, recov sql.NullFloat64
	var updatedCustomMetric sql.NullString
	var updatedCustomLabels []byte
	err := s.db.QueryRow(`
		UPDATE alert_rules SET name=$1, description=$2, type=$3, target_type=$4, target_id=$5,
		       metric=$6, operator=$7, threshold=$8, duration=$9, severity=$10, enabled=$11,
		       channels=$12, aggregation=$13, warning_threshold=$14, critical_threshold=$15,
		       recovery_threshold=$16, tags=$17, notify_warning=$18, notify_critical=$19, updated_at=$20,
		       custom_metric=$23, custom_labels=$24
		WHERE id=$21 AND organization_id=$22
		RETURNING id, organization_id, name, description, type, target_type, target_id,
		          metric, operator, threshold, duration, severity, enabled, channels,
		          COALESCE(aggregation, 'avg'), warning_threshold, critical_threshold, recovery_threshold,
		          COALESCE(tags, ''), COALESCE(notify_warning, true), COALESCE(notify_critical, true),
		          created_at, updated_at, custom_metric, COALESCE(custom_labels, '[]'::jsonb)`,
		r.Name, r.Description, r.Type, r.TargetType, r.TargetID,
		r.Metric, r.Operator, r.Threshold, r.Duration, r.Severity, r.Enabled,
		channelsJSON, r.Aggregation, r.WarningThreshold, r.CriticalThreshold, r.RecoveryThreshold,
		r.Tags, r.NotifyWarning, r.NotifyCritical, now, ruleID, orgID,
		r.CustomMetric, marshalMetricLabels(r.CustomLabels),
	).Scan(&r.ID, &r.OrganizationID, &r.Name, &r.Description, &r.Type, &r.TargetType, &r.TargetID,
		&r.Metric, &r.Operator, &r.Threshold, &r.Duration, &r.Severity, &r.Enabled, &channelsJSON,
		&r.Aggregation, &warn, &crit, &recov, &r.Tags, &r.NotifyWarning, &r.NotifyCritical, &r.CreatedAt, &r.UpdatedAt,
		&updatedCustomMetric, &updatedCustomLabels)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAlertRuleUpdate, err)
	}
	if warn.Valid {
		r.WarningThreshold = &warn.Float64
	}
	if crit.Valid {
		r.CriticalThreshold = &crit.Float64
	}
	if recov.Valid {
		r.RecoveryThreshold = &recov.Float64
	}
	if len(channelsJSON) > 0 {
		json.Unmarshal(channelsJSON, &r.Channels)
	}
	r.CustomMetric = nil
	if updatedCustomMetric.Valid && updatedCustomMetric.String != "" {
		r.CustomMetric = &updatedCustomMetric.String
	}
	r.CustomLabels = nil
	if len(updatedCustomLabels) > 0 {
		json.Unmarshal(updatedCustomLabels, &r.CustomLabels)
	}
	return &r, nil
}

func (s *AlertService) DeleteAlertRule(ruleID, orgID int64) error {
	res, err := s.db.Exec(`DELETE FROM alert_rules WHERE id=$1 AND organization_id=$2`, ruleID, orgID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAlertRuleNotFound
	}
	return nil
}

func (s *AlertService) ToggleAlertRule(ruleID, orgID int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE alert_rules SET enabled=$1, updated_at=NOW() WHERE id=$2 AND organization_id=$3`,
		enabled, ruleID, orgID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAlertRuleNotFound
	}
	return nil
}

func scanAlertRules(rows *sql.Rows) ([]models.AlertRule, error) {
	var rules []models.AlertRule
	for rows.Next() {
		var r models.AlertRule
		var desc sql.NullString
		var targetID sql.NullInt64
		var channelsJSON []byte
		var warn, crit, recov sql.NullFloat64
		var customMetric sql.NullString
		var customLabelsJSON []byte
		if err := rows.Scan(&r.ID, &r.OrganizationID, &r.Name, &desc, &r.Type,
			&r.TargetType, &targetID, &r.Metric, &r.Operator, &r.Threshold,
			&r.Duration, &r.Severity, &r.Enabled, &channelsJSON,
			&r.Aggregation, &warn, &crit, &recov,
			&r.Tags, &r.NotifyWarning, &r.NotifyCritical, &r.CreatedAt, &r.UpdatedAt,
			&customMetric, &customLabelsJSON); err != nil {
			return nil, err
		}
		if customMetric.Valid && customMetric.String != "" {
			r.CustomMetric = &customMetric.String
		}
		if len(customLabelsJSON) > 0 {
			json.Unmarshal(customLabelsJSON, &r.CustomLabels)
		}
		if desc.Valid {
			r.Description = &desc.String
		}
		if targetID.Valid {
			r.TargetID = &targetID.Int64
		}
		if warn.Valid {
			r.WarningThreshold = &warn.Float64
		}
		if crit.Valid {
			r.CriticalThreshold = &crit.Float64
		}
		if recov.Valid {
			r.RecoveryThreshold = &recov.Float64
		}
		if len(channelsJSON) > 0 {
			json.Unmarshal(channelsJSON, &r.Channels)
		}
		if r.Channels == nil {
			r.Channels = []int64{}
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// ---- Alert Rules (evaluator helpers) ----

func (s *AlertService) GetEnabledAlertRules() ([]models.AlertRule, error) {
	rows, err := s.db.Query(`
		SELECT id, organization_id, name, description, type, target_type, target_id,
		       metric, operator, threshold, duration, severity, enabled, channels,
		       COALESCE(aggregation, 'avg'), warning_threshold, critical_threshold, recovery_threshold,
		       custom_metric, COALESCE(custom_labels, '[]'::jsonb)
		FROM alert_rules WHERE enabled = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []models.AlertRule
	for rows.Next() {
		var r models.AlertRule
		var desc, targetType sql.NullString
		var targetID sql.NullInt64
		var channelsJSON []byte
		var warn, crit, recov sql.NullFloat64
		var customMetric sql.NullString
		var customLabelsJSON []byte
		if err := rows.Scan(&r.ID, &r.OrganizationID, &r.Name, &desc, &r.Type,
			&targetType, &targetID, &r.Metric, &r.Operator, &r.Threshold,
			&r.Duration, &r.Severity, &r.Enabled, &channelsJSON,
			&r.Aggregation, &warn, &crit, &recov,
			&customMetric, &customLabelsJSON); err != nil {
			return nil, err
		}
		if customMetric.Valid && customMetric.String != "" {
			r.CustomMetric = &customMetric.String
		}
		if len(customLabelsJSON) > 0 {
			json.Unmarshal(customLabelsJSON, &r.CustomLabels)
		}
		if desc.Valid {
			r.Description = &desc.String
		}
		if targetType.Valid {
			r.TargetType = targetType.String
		}
		if targetID.Valid {
			r.TargetID = &targetID.Int64
		}
		if warn.Valid {
			r.WarningThreshold = &warn.Float64
		}
		if crit.Valid {
			r.CriticalThreshold = &crit.Float64
		}
		if recov.Valid {
			r.RecoveryThreshold = &recov.Float64
		}
		if len(channelsJSON) > 0 {
			json.Unmarshal(channelsJSON, &r.Channels)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// HasLiveIncidentForRule returns true if an ongoing incident already exists for
// this rule. Acknowledged counts: see LiveIncidentStatuses.
func (s *AlertService) HasLiveIncidentForRule(ruleID int64) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM incidents WHERE alert_rule_id = $1 AND status IN `+LiveIncidentStatuses, ruleID,
	).Scan(&count)
	return count > 0, err
}

// HasLiveIncidentForServiceCheck returns true when there is already an ongoing
// incident for the given service check (tracked by service_id, not alert_rule_id).
func (s *AlertService) HasLiveIncidentForServiceCheck(serviceID int64) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM incidents WHERE service_id = $1 AND status IN `+LiveIncidentStatuses, serviceID,
	).Scan(&count)
	return count > 0, err
}

// GetChannelsByIDs returns channels matching the given IDs, scoped to an org.
func (s *AlertService) GetChannelsByIDs(channelIDs []int64, orgID int64) ([]models.NotificationChannel, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}
	// Build $1,$2,… placeholders
	placeholders := make([]string, len(channelIDs))
	args := []interface{}{orgID}
	for i, id := range channelIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}
	query := fmt.Sprintf(`SELECT id, organization_id, name, type, config, enabled, created_at, updated_at
		FROM notification_channels WHERE organization_id = $1 AND id IN (%s) AND enabled = true`,
		strings.Join(placeholders, ","))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []models.NotificationChannel
	for rows.Next() {
		var c models.NotificationChannel
		var configJSON []byte
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Type, &configJSON, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if len(configJSON) > 0 {
			json.Unmarshal(configJSON, &c.Config)
		}
		if c.Config == nil {
			c.Config = map[string]interface{}{}
		}
		channels = append(channels, c)
	}
	return channels, nil
}

// ---- Incidents ----

func (s *AlertService) CreateIncident(inc models.Incident) (*models.Incident, error) {
	query := `INSERT INTO incidents (organization_id, alert_rule_id, service_id, host_id, title, description, severity, status, service)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, started_at`
	err := s.db.QueryRow(query, inc.OrganizationID, inc.AlertRuleID, inc.ServiceID, inc.HostID, inc.Title, inc.Description, inc.Severity, "open", inc.Service).Scan(&inc.ID, &inc.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIncidentCreate, err)
	}
	inc.Status = "open"
	return &inc, nil
}

// IncidentFilter holds the optional filters and pagination window for listing
// incidents. All string fields are optional; empty values are ignored.
type IncidentFilter struct {
	Status   string
	Severity string
	Service  string // substring match (ILIKE)
	Search   string // free text over title/service/description
	// Since and Until bound started_at. A tool mirroring the history needs a
	// time range: paging alone cannot reach past what the window holds.
	Since  *time.Time
	Until  *time.Time
	Limit  int
	Offset int
}

// IncidentStatsResult holds the global facet counts and the distinct service
// list shown on the incidents page (independent of the active filters/page).
type IncidentStatsResult struct {
	Total        int      `json:"total"`
	Open         int      `json:"open"`
	Acknowledged int      `json:"acknowledged"`
	Resolved     int      `json:"resolved"`
	Critical     int      `json:"critical"`
	Services     []string `json:"services"`
}

// incidentWhere builds the shared WHERE clause used by GetIncidents and
// CountIncidents. It returns the clause, its args and the next positional arg
// index so callers can append LIMIT/OFFSET.
func incidentWhere(orgID int64, f IncidentFilter) (string, []interface{}, int) {
	where := " WHERE organization_id = $1"
	args := []interface{}{orgID}
	argPos := 2
	if f.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", argPos)
		args = append(args, f.Status)
		argPos++
	}
	if f.Severity != "" {
		where += fmt.Sprintf(" AND severity = $%d", argPos)
		args = append(args, f.Severity)
		argPos++
	}
	if f.Service != "" {
		where += fmt.Sprintf(" AND service ILIKE $%d", argPos)
		args = append(args, "%"+f.Service+"%")
		argPos++
	}
	if f.Search != "" {
		where += fmt.Sprintf(" AND (title ILIKE $%d OR service ILIKE $%d OR description ILIKE $%d)", argPos, argPos+1, argPos+2)
		like := "%" + f.Search + "%"
		args = append(args, like, like, like)
		argPos += 3
	}
	if f.Since != nil {
		where += fmt.Sprintf(" AND started_at >= $%d", argPos)
		args = append(args, *f.Since)
		argPos++
	}
	if f.Until != nil {
		where += fmt.Sprintf(" AND started_at < $%d", argPos)
		args = append(args, *f.Until)
		argPos++
	}
	return where, args, argPos
}

func (s *AlertService) GetIncidents(orgID int64, f IncidentFilter) ([]models.Incident, error) {
	where, args, argPos := incidentWhere(orgID, f)
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, organization_id, alert_rule_id, service_id, host_id, title, description, severity, status, service, started_at, resolved_at, resolution_note, acknowledged_at, acknowledged_by
			  FROM incidents` + where +
		fmt.Sprintf(" ORDER BY started_at DESC LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIncidentsFetch, err)
	}
	defer rows.Close()

	var incidents []models.Incident
	for rows.Next() {
		var i models.Incident
		if err := rows.Scan(&i.ID, &i.OrganizationID, &i.AlertRuleID, &i.ServiceID, &i.HostID, &i.Title, &i.Description, &i.Severity, &i.Status,
			&i.Service, &i.StartedAt, &i.ResolvedAt, &i.ResolutionNote, &i.AcknowledgedAt, &i.AcknowledgedBy); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrIncidentScan, err)
		}
		incidents = append(incidents, i)
	}
	return incidents, nil
}

// CountIncidents returns the number of incidents matching the same filters as
// GetIncidents (ignoring Limit/Offset), for pagination.
func (s *AlertService) CountIncidents(orgID int64, f IncidentFilter) (int, error) {
	where, args, _ := incidentWhere(orgID, f)
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM incidents`+where, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrIncidentsCount, err)
	}
	return count, nil
}

// IncidentStats returns the global status/severity facet counts and the
// distinct service list for the org, unaffected by the active filters or page.
func (s *AlertService) IncidentStats(orgID int64) (IncidentStatsResult, error) {
	var r IncidentStatsResult
	r.Services = []string{}
	err := s.db.QueryRow(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'open'),
			COUNT(*) FILTER (WHERE status = 'acknowledged'),
			COUNT(*) FILTER (WHERE status = 'resolved'),
			COUNT(*) FILTER (WHERE severity = 'critical')
		FROM incidents WHERE organization_id = $1`, orgID).
		Scan(&r.Total, &r.Open, &r.Acknowledged, &r.Resolved, &r.Critical)
	if err != nil {
		return r, fmt.Errorf("%w: %w", ErrIncidentStatsCompute, err)
	}

	rows, err := s.db.Query(`SELECT DISTINCT service FROM incidents
		WHERE organization_id = $1 AND service IS NOT NULL AND service <> '' ORDER BY service`, orgID)
	if err != nil {
		return r, fmt.Errorf("%w: %w", ErrIncidentServicesFetch, err)
	}
	defer rows.Close()
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return r, fmt.Errorf("%w: %w", ErrIncidentServiceScan, err)
		}
		r.Services = append(r.Services, svc)
	}
	return r, rows.Err()
}

func (s *AlertService) UpdateIncidentStatus(incidentID, orgID int64, status string, userID *int64, resolutionNote *string) error {
	return s.UpdateIncidentStatusBy(incidentID, orgID, status, userID, resolutionNote, "")
}

// UpdateIncidentStatusBy is UpdateIncidentStatus with the actor that requested
// the transition. An automated remediation is not a user, so acknowledged_by
// (a user id) has nowhere to record it.
//
// Acknowledging is idempotent: acknowledged_at keeps its first value, so a
// retrying system replaying the same acknowledgement does not rewrite when the
// incident was first picked up.
func (s *AlertService) UpdateIncidentStatusBy(incidentID, orgID int64, status string, userID *int64, note *string, actor string) error {
	now := time.Now().UTC()
	var query string
	var args []interface{}
	resolutionNote := note

	switch status {
	case "acknowledged":
		query = `UPDATE incidents SET status = $1, acknowledged_at = COALESCE(acknowledged_at, $2), acknowledged_by = COALESCE($3, acknowledged_by),
		         acknowledged_actor = COALESCE($6, acknowledged_actor), acknowledgement_note = COALESCE($7, acknowledgement_note)
		         WHERE id = $4 AND organization_id = $5`
		args = []interface{}{status, now, userID, incidentID, orgID, nullIfEmpty(actor), nullIfBlank(note)}
	case "resolved":
		// COALESCE keeps a previously saved note when the caller resolves
		// without providing a new one (e.g. reopen then re-resolve).
		query = `UPDATE incidents SET status = $1, resolved_at = $2, resolution_note = COALESCE($3, resolution_note) WHERE id = $4 AND organization_id = $5`
		if resolutionNote != nil && strings.TrimSpace(*resolutionNote) == "" {
			resolutionNote = nil
		}
		args = []interface{}{status, now, resolutionNote, incidentID, orgID}
	default:
		query = `UPDATE incidents SET status = $1 WHERE id = $2 AND organization_id = $3`
		args = []interface{}{status, incidentID, orgID}
	}

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIncidentUpdate, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrIncidentNotFound
	}
	return nil
}

func nullIfEmpty(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func nullIfBlank(value *string) interface{} {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return *value
}
