package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/models"
	"middle-monitor/backend/profileflame"
	"middle-monitor/backend/services"

	"github.com/gorilla/mux"
)

// ==========================================
// Alert Rules Handlers
// ==========================================

func handleGetNotificationChannels(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		channels, err := alertService.GetChannels(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if channels == nil {
			channels = []models.NotificationChannel{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(channels)
	}
}

func handleCreateNotificationChannel(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		var channel models.NotificationChannel
		if err := json.NewDecoder(r.Body).Decode(&channel); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if err := validateName("name", channel.Name, maxNameLen); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateChannelType(channel.Type); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		channel.OrganizationID = orgID

		created, err := alertService.CreateChannel(channel)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}
}

func handleUpdateNotificationChannel(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		channelID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrChannelIDInvalid)
			return
		}

		var channel models.NotificationChannel
		if err := json.NewDecoder(r.Body).Decode(&channel); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if channel.Name != "" {
			if err := validateName("name", channel.Name, maxNameLen); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
		}
		if channel.Type != "" {
			if err := validateChannelType(channel.Type); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
		}

		updated, err := alertService.UpdateChannel(channelID, orgID, channel)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)
	}
}

func handleDeleteNotificationChannel(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		channelID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrChannelIDInvalid)
			return
		}

		if err := alertService.DeleteChannel(channelID, orgID); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleTestNotificationChannel(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		channelID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrChannelIDInvalid)
			return
		}

		// GetChannels (not GetChannelsByIDs) so a disabled channel can be
		// tested before being enabled.
		channels, err := alertService.GetChannels(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		var channel *models.NotificationChannel
		for i := range channels {
			if channels[i].ID == channelID {
				channel = &channels[i]
				break
			}
		}
		if channel == nil {
			respondError(w, http.StatusNotFound, ErrChannelNotFound)
			return
		}

		title := "[TEST] Middle Monitor test notification"
		message := fmt.Sprintf("Test notification for channel %q (%s). If you can read this, the channel is configured correctly.", channel.Name, channel.Type)
		alias := fmt.Sprintf("mm-channel-test-%d", channelID)
		if err := services.SendNotificationWithAlias(*channel, title, message, alias); err != nil {
			respondError(w, http.StatusUnprocessableEntity, fmt.Errorf("%w: %w", ErrChannelDelivery, err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"delivered": true})
	}
}

// ==========================================
// Incidents Handlers
// ==========================================

func handleGetIncidents(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		limit, offset := parseLimitOffset(r, 50, 200)
		since, until := parseTimeRange(r)
		f := services.IncidentFilter{
			Status:   r.URL.Query().Get("status"),
			Severity: r.URL.Query().Get("severity"),
			Service:  r.URL.Query().Get("service"),
			Search:   r.URL.Query().Get("search"),
			Since:    since,
			Until:    until,
			Limit:    limit,
			Offset:   offset,
		}

		total, err := alertService.CountIncidents(orgID, f)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		incidents, err := alertService.GetIncidents(orgID, f)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if incidents == nil {
			incidents = []models.Incident{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		json.NewEncoder(w).Encode(incidents)
	}
}

func handleGetIncidentStats(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		stats, err := alertService.IncidentStats(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

func handleCreateIncident(db *sql.DB) http.HandlerFunc {
	alertService := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		var incident models.Incident
		if err := json.NewDecoder(r.Body).Decode(&incident); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if incident.ServiceID == nil && incident.HostID == nil {
			respondError(w, http.StatusBadRequest, ErrTargetRequired)
			return
		}
		incident.OrganizationID = orgID

		created, err := alertService.CreateIncident(incident)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}
}

// ==========================================
// Alert Rules Handlers
// ==========================================

func handleGetAlertRules(db *sql.DB) http.HandlerFunc {
	svc := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		rules, err := svc.GetAlertRules(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if rules == nil {
			rules = []models.AlertRule{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rules)
	}
}

func handleCreateAlertRule(db *sql.DB) http.HandlerFunc {
	svc := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		var rule models.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		rule.OrganizationID = orgID
		if rule.Type == "" {
			rule.Type = "threshold"
		}
		if rule.TargetType == "" {
			rule.TargetType = "any"
		}
		if rule.Operator == "" {
			rule.Operator = "gt"
		}
		if rule.Duration == 0 {
			rule.Duration = 60
		}
		created, err := svc.CreateAlertRule(rule)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}
}

func handleUpdateAlertRule(db *sql.DB) http.HandlerFunc {
	svc := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		ruleID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrIDInvalid)
			return
		}
		var rule models.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		updated, err := svc.UpdateAlertRule(ruleID, orgID, rule)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)
	}
}

func handleDeleteAlertRule(db *sql.DB) http.HandlerFunc {
	svc := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		ruleID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrIDInvalid)
			return
		}
		if err := svc.DeleteAlertRule(ruleID, orgID); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleToggleAlertRule(db *sql.DB) http.HandlerFunc {
	svc := services.NewAlertService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		ruleID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrIDInvalid)
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if err := svc.ToggleAlertRule(ruleID, orgID, body.Enabled); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"enabled": body.Enabled})
	}
}

// ==========================================
// API Keys Handlers
// ==========================================

func handleGetAPIKeys(db *sql.DB) http.HandlerFunc {
	apiKeyService := services.NewAPIKeyService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		keys, err := apiKeyService.GetKeys(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if keys == nil {
			keys = []models.APIKey{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(keys)
	}
}

func handleCreateAPIKey(db *sql.DB) http.HandlerFunc {
	apiKeyService := services.NewAPIKeyService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		userID := middleware.GetUserID(r.Context())

		var body struct {
			Name      string     `json:"name"`
			Scopes    string     `json:"scopes"`
			ExpiresAt *time.Time `json:"expires_at,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if body.Name == "" {
			respondError(w, http.StatusBadRequest, ErrNameRequired)
			return
		}
		// Expiration is mandatory (no perpetual keys), but there is no upper bound.
		if body.ExpiresAt == nil {
			respondError(w, http.StatusBadRequest, ErrExpirationRequired)
			return
		}
		if body.ExpiresAt.Before(time.Now()) {
			respondError(w, http.StatusBadRequest, ErrExpirationInPast)
			return
		}
		if body.Scopes == "" {
			body.Scopes = "read"
		}

		key, err := apiKeyService.CreateKey(orgID, body.Name, body.Scopes, body.ExpiresAt, &userID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(key)
	}
}

func handleDeleteAPIKey(db *sql.DB) http.HandlerFunc {
	apiKeyService := services.NewAPIKeyService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		keyID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrKeyIDInvalid)
			return
		}

		if err := apiKeyService.DeleteKey(keyID, orgID); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ==========================================
// Install Tokens (agent install → org)
// ==========================================

func handleGetInstallTokens(db *sql.DB) http.HandlerFunc {
	svc := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		tokens, err := svc.ListTokens(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if tokens == nil {
			tokens = []services.InstallToken{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokens)
	}
}

func handleCreateInstallToken(db *sql.DB) http.HandlerFunc {
	svc := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		userID := middleware.GetUserID(r.Context())
		var createdBy *int64
		if userID > 0 {
			createdBy = &userID
		}

		var body struct {
			Name      string     `json:"name"`
			ExpiresAt *time.Time `json:"expires_at,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		token, err := svc.CreateToken(orgID, body.Name, body.ExpiresAt, createdBy)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(token)
	}
}

func handleDeleteInstallToken(db *sql.DB) http.HandlerFunc {
	svc := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrTokenIDInvalid)
			return
		}
		if err := svc.DeleteToken(id, orgID); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ==========================================
// Logs Handlers (OpenSearch)
// ==========================================

func handleSearchLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		service := r.URL.Query().Get("service")
		severity := r.URL.Query().Get("severity")
		startTime := r.URL.Query().Get("start")
		endTime := r.URL.Query().Get("end")

		from := 0
		size := 50
		if f, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil {
			from = f
		}
		if s, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && s <= 200 {
			size = s
		}

		if startTime == "" {
			startTime = time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
		}
		if endTime == "" {
			endTime = time.Now().UTC().Format(time.RFC3339)
		}

		var logs []map[string]interface{}
		var total int64
		if opensearch != nil {
			var err error
			orgID := middleware.GetOrganizationID(r.Context())
			// Datadog-style query syntax; the dropdown filters ride along as-is.
			logs, total, err = opensearch.SearchLogsDatadog(query, service, severity, orgID, from, size, startTime, endTime)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
		}
		if logs == nil {
			logs = []map[string]interface{}{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"hits":  logs,
			"total": total,
			"from":  from,
			"size":  size,
		})
	}
}

func handleLogFieldValues() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		field := r.URL.Query().Get("field")
		prefix := r.URL.Query().Get("prefix")
		startTime := r.URL.Query().Get("start")
		endTime := r.URL.Query().Get("end")

		if field == "" {
			respondError(w, http.StatusBadRequest, ErrFieldRequired)
			return
		}
		// Accept Datadog facet names (service:, status:, host:) here too.
		field = services.ResolveLogFieldAlias(field)

		size := 20
		if s, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && s > 0 && s <= 50 {
			size = s
		}

		if startTime == "" {
			startTime = time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
		}
		if endTime == "" {
			endTime = time.Now().UTC().Format(time.RFC3339)
		}

		var values []string
		if opensearch != nil {
			var err error
			orgID := middleware.GetOrganizationID(r.Context())
			values, err = opensearch.GetLogFieldValues(field, prefix, orgID, startTime, endTime, size)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
		}
		if values == nil {
			values = []string{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"values": values})
	}
}

// ==========================================
// Traces Handlers (OpenSearch)
// ==========================================

func handleSearchTraces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		service := r.URL.Query().Get("service")
		startTime := r.URL.Query().Get("start")
		endTime := r.URL.Query().Get("end")

		from := 0
		size := 50
		if f, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil {
			from = f
		}
		if s, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && s <= 200 {
			size = s
		}

		if startTime == "" {
			startTime = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
		}
		if endTime == "" {
			endTime = time.Now().UTC().Format(time.RFC3339)
		}

		var traces []map[string]interface{}
		var total int64
		if opensearch != nil {
			var err error
			orgID := middleware.GetOrganizationID(r.Context())
			traces, total, err = opensearch.SearchTraces(query, service, orgID, from, size, startTime, endTime)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
		}
		if traces == nil {
			traces = []map[string]interface{}{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"hits":  traces,
			"total": total,
			"from":  from,
			"size":  size,
		})
	}
}

// ==========================================
// Network Metrics Handler
// ==========================================

func handleGetNetworkMetrics(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		startTime := r.URL.Query().Get("start")
		endTime := r.URL.Query().Get("end")

		if startTime == "" {
			startTime = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
		}
		if endTime == "" {
			endTime = time.Now().UTC().Format(time.RFC3339)
		}

		// Get network metrics from service_results where metric_type = 'network'
		query := `SELECT sr.id, sr.service_id, s.name as service_name, s.host, sr.status, sr.metric_value, sr.metadata, sr.timestamp
				  FROM service_results sr
				  JOIN services s ON sr.service_id = s.id
				  WHERE s.organization_id = $1 AND s.type = 'agent_network'
				  AND sr.timestamp >= $2 AND sr.timestamp <= $3
				  ORDER BY sr.timestamp DESC LIMIT 500`

		rows, err := db.Query(query, orgID, startTime, endTime)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()

		type NetworkMetric struct {
			ID          int64    `json:"id"`
			ServiceID   int64    `json:"service_id"`
			ServiceName string   `json:"service_name"`
			Host        string   `json:"host"`
			Status      string   `json:"status"`
			Value       *float64 `json:"value"`
			Metadata    *string  `json:"metadata"`
			Timestamp   string   `json:"timestamp"`
		}

		var metrics []NetworkMetric
		for rows.Next() {
			var m NetworkMetric
			var ts time.Time
			if err := rows.Scan(&m.ID, &m.ServiceID, &m.ServiceName, &m.Host, &m.Status, &m.Value, &m.Metadata, &ts); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			m.Timestamp = ts.Format(time.RFC3339)
			metrics = append(metrics, m)
		}

		if metrics == nil {
			metrics = []NetworkMetric{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics)
	}
}

// ==========================================
// Metrics Explorer Handler (enhanced)
// ==========================================

func handleGetMetricsExplorer(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		serviceFilter := r.URL.Query().Get("service")
		hostFilter := r.URL.Query().Get("host")
		startTime := r.URL.Query().Get("start")
		endTime := r.URL.Query().Get("end")

		if startTime == "" {
			startTime = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
		}
		if endTime == "" {
			endTime = time.Now().UTC().Format(time.RFC3339)
		}

		// Build metrics from service_results (agent_cpu, agent_ram, http) - include host for context
		baseQuery := `
			SELECT sr.id, sr.timestamp, sr.metric_type, sr.metric_value, sr.latency, s.name AS service_name, s.type AS service_type,
			       COALESCE(h.name, s.host) AS host_name
			FROM service_results sr
			JOIN services s ON s.id = sr.service_id
			LEFT JOIN hosts h ON s.host_id = h.id
			WHERE s.organization_id = $1 AND sr.timestamp >= $2 AND sr.timestamp <= $3
			AND (
				(s.type = 'agent_cpu' AND sr.metric_type = 'cpu')
				OR (s.type = 'agent_ram' AND sr.metric_type = 'ram')
				OR (s.type = 'http' AND sr.latency IS NOT NULL)
			)`
		args := []interface{}{orgID, startTime, endTime}
		argPos := 4
		if serviceFilter != "" {
			baseQuery += fmt.Sprintf(" AND s.name = $%d", argPos)
			args = append(args, serviceFilter)
			argPos++
		}
		if hostFilter != "" {
			baseQuery += fmt.Sprintf(" AND COALESCE(h.name, s.host) = $%d", argPos)
			args = append(args, hostFilter)
			argPos++
		}
		baseQuery += " ORDER BY sr.timestamp ASC LIMIT 2000"

		rows, err := db.Query(baseQuery, args...)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()

		type SystemMetricWithHost struct {
			models.SystemMetric
			Host string `json:"host"`
		}
		var sysMetrics []SystemMetricWithHost
		var id int64
		var ts time.Time
		var metricType, serviceName, serviceType, hostName sql.NullString
		var metricValue sql.NullFloat64
		var latency sql.NullFloat64

		for rows.Next() {
			if err := rows.Scan(&id, &ts, &metricType, &metricValue, &latency, &serviceName, &serviceType, &hostName); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			m := SystemMetricWithHost{
				SystemMetric: models.SystemMetric{
					ID:             id,
					OrganizationID: orgID,
					Service:        serviceName.String,
					CPUPerc:        0,
					RAMPerc:        0,
					Timestamp:      ts.UTC(),
				},
				Host: hostName.String,
			}
			if serviceType.String == "agent_cpu" && metricValue.Valid {
				m.CPUPerc = metricValue.Float64
			} else if serviceType.String == "agent_ram" && metricValue.Valid {
				m.RAMPerc = metricValue.Float64
			} else if serviceType.String == "http" && latency.Valid {
				l := latency.Float64
				m.HTTPLatency = &l
			}
			sysMetrics = append(sysMetrics, m)
		}
		if sysMetrics == nil {
			sysMetrics = []SystemMetricWithHost{}
		}

		// Filters: distinct (service, host) from services that have results
		filterQuery := `
			SELECT DISTINCT s.name, COALESCE(h.name, s.host) AS host_name
			FROM service_results sr
			JOIN services s ON s.id = sr.service_id
			LEFT JOIN hosts h ON s.host_id = h.id
			WHERE s.organization_id = $1
			AND (s.type IN ('agent_cpu','agent_ram','http'))
			ORDER BY host_name, s.name`
		filterRows, err := db.Query(filterQuery, orgID)
		if err != nil {
			filterRows = nil
		}
		type FilterOption struct {
			Service string `json:"service"`
			Host    string `json:"host"`
		}
		var filters []FilterOption
		if filterRows != nil {
			defer filterRows.Close()
			for filterRows.Next() {
				var f FilterOption
				filterRows.Scan(&f.Service, &f.Host)
				filters = append(filters, f)
			}
		}
		if filters == nil {
			filters = []FilterOption{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"metrics": sysMetrics,
			"filters": filters,
		})
	}
}

// ==========================================
// Profile (pprof) Handlers
// ==========================================

// handleUploadProfile accepts pprof profile upload from SDK (Bearer token = service token)
func handleUploadProfile(db *sql.DB) http.HandlerFunc {
	profileService := services.NewProfileService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID, ok := resolveIngestOrg(db, r.Header.Get("Authorization"))
		if !ok {
			slog.Warn("profile upload rejected, missing or unknown token")
			respondError(w, http.StatusUnauthorized, ErrTokenInvalid)
			return
		}

		// Parse multipart form (max 50MB for profile data)
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			respondError(w, http.StatusBadRequest, ErrMultipartFormInvalid)
			return
		}
		profileType := strings.TrimSpace(r.FormValue("profile_type"))
		if profileType == "" {
			profileType = "cpu"
		}
		if profileType != "cpu" && profileType != "heap" && profileType != "goroutine" && profileType != "allocs" && profileType != "block" && profileType != "mutex" {
			respondError(w, http.StatusBadRequest, ErrProfileTypeInvalid)
			return
		}
		service := strings.TrimSpace(r.FormValue("service"))
		if service == "" {
			service = "unknown"
		}
		var durationSeconds *int
		if d := r.FormValue("duration_seconds"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 120 {
				durationSeconds = &n
			}
		}
		var memoryMB *float64
		if m := r.FormValue("memory_mb"); m != "" {
			if f, err := strconv.ParseFloat(m, 64); err == nil && f >= 0 {
				memoryMB = &f
			}
		}

		file, _, err := r.FormFile("profile")
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrProfileFileMissing)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrProfileRead)
			return
		}
		if len(data) == 0 {
			respondError(w, http.StatusBadRequest, ErrProfileEmpty)
			return
		}

		cap, err := profileService.Create(orgID, service, profileType, durationSeconds, memoryMB, data)
		if err != nil {
			slog.Error("profile upload failed", "org_id", orgID, "service", service, "type", profileType, "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		slog.Info("profile uploaded", "org_id", orgID, "service", service, "type", profileType, "id", cap.ID, "size", len(data))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cap)
	}
}

// handleGetProfileSeries returns memory_mb time series for RAM chart (JWT auth)
func handleGetProfileSeries(db *sql.DB) http.HandlerFunc {
	profileService := services.NewProfileService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		service := r.URL.Query().Get("service")
		fromStr := r.URL.Query().Get("from")
		toStr := r.URL.Query().Get("to")
		limitStr := r.URL.Query().Get("limit")
		limit := 500
		if limitStr != "" {
			if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 2000 {
				limit = n
			}
		}
		var from, to *time.Time
		if fromStr != "" {
			if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
				from = &t
			}
		}
		if toStr != "" {
			if t, err := time.Parse(time.RFC3339, toStr); err == nil {
				to = &t
			}
		}
		points, err := profileService.Series(orgID, service, from, to, limit)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if points == nil {
			points = []services.MemorySeriesPoint{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"points": points})
	}
}

// handleListProfiles returns profile captures for the org (JWT auth)
func handleListProfiles(db *sql.DB) http.HandlerFunc {
	profileService := services.NewProfileService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		service := r.URL.Query().Get("service")
		profileType := r.URL.Query().Get("profile_type")
		limit, offset := parseLimitOffset(r, 50, 200)

		total, err := profileService.CountProfiles(orgID, service, profileType)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		list, err := profileService.List(orgID, service, profileType, limit, offset)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if list == nil {
			list = []models.ProfileCapture{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		json.NewEncoder(w).Encode(list)
	}
}

// handleDownloadProfile returns the raw pprof file for a capture (JWT auth)
func handleDownloadProfile(db *sql.DB) http.HandlerFunc {
	profileService := services.NewProfileService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		vars := mux.Vars(r)
		idStr := vars["id"]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			respondError(w, http.StatusBadRequest, ErrProfileIDInvalid)
			return
		}
		cap, err := profileService.GetByID(orgID, id)
		if err != nil {
			if err == sql.ErrNoRows {
				respondError(w, http.StatusNotFound, ErrProfileNotFound)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=profile_%s_%s_%d.pprof", cap.ProfileType, cap.CreatedAt.Format("20060102_150405"), cap.ID))
		w.Header().Set("Content-Length", strconv.Itoa(len(cap.Data)))
		w.Write(cap.Data)
	}
}

// handleGetProfileFlamegraph returns flame graph JSON for the profile (JWT auth)
func handleGetProfileFlamegraph(db *sql.DB) http.HandlerFunc {
	profileService := services.NewProfileService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		vars := mux.Vars(r)
		idStr := vars["id"]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			respondError(w, http.StatusBadRequest, ErrProfileIDInvalid)
			return
		}
		cap, err := profileService.GetByID(orgID, id)
		if err != nil {
			if err == sql.ErrNoRows {
				respondError(w, http.StatusNotFound, ErrProfileNotFound)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		tree, err := profileflame.ToFlameTree(cap.Data)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrProfileParse)
			return
		}
		if tree == nil {
			tree = &profileflame.FlameNode{Name: "root", Value: 0, Children: []profileflame.FlameNode{}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tree)
	}
}
