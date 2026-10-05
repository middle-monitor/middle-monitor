package services

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"middle-monitor/backend/models"
)

// AgentService handles agent operations
type AgentService struct {
	db *sql.DB
}

func NewAgentService(db *sql.DB) *AgentService {
	return &AgentService{db: db}
}

// RegisterAgent registers an agent. If orgIDFromToken > 0, it overrides org_slug (used when install token is provided).
func (s *AgentService) RegisterAgent(reg models.AgentRegistration, orgIDFromToken int64) (*models.AgentRegistrationResponse, error) {
	// Resolve org_id: token takes precedence, then org_slug, else default 1
	orgID := int64(1)
	if orgIDFromToken > 0 {
		orgID = orgIDFromToken
	} else if reg.OrgSlug != "" {
		err := s.db.QueryRow(`SELECT id FROM organizations WHERE slug = $1`, reg.OrgSlug).Scan(&orgID)
		if err != nil {
			return nil, &NotInOrgError{Kind: "organization", Ref: reg.OrgSlug}
		}
	}

	// Check if host exists in this org
	var hostID int64
	err := s.db.QueryRow(`SELECT id FROM hosts WHERE name = $1 AND organization_id = $2`, reg.Hostname, orgID).Scan(&hostID)
	if err == sql.ErrNoRows {
		// Create host
		host := models.Host{
			OrganizationID: orgID,
			Name:           reg.Hostname,
			Host:           reg.Hostname,
			Service:        reg.Service,
		}
		hostService := NewHostService(s.db)
		createdHost, err := hostService.CreateHost(host)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrHostCreate, err)
		}
		hostID = createdHost.ID
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHostCheck, err)
	}

	// Create or update services for each metric
	var serviceIDs []int64
	serviceTypeMap := map[string]string{
		"cpu":     "agent_cpu",
		"ram":     "agent_ram",
		"disk":    "agent_disk",
		"network": "agent_network",
	}

	slog.Info("registering agent", "hostname", reg.Hostname, "service", reg.Service, "metrics", reg.Metrics)

	for _, metric := range reg.Metrics {
		serviceType, exists := serviceTypeMap[metric]
		if !exists {
			continue
		}

		// Check if service already exists
		var serviceID int64
		err := s.db.QueryRow(`SELECT id FROM services WHERE host_id = $1 AND type = $2`,
			hostID, serviceType).Scan(&serviceID)

		if err == sql.ErrNoRows {
			// Create service
			service := models.Service{
				OrganizationID:  orgID,
				HostID:          &hostID,
				Name:            metric,
				Type:            serviceType,
				Host:            reg.Hostname,
				Service:         reg.Service,
				ServiceInterval: 60,
				MaxAttempts:     3,
			}
			serviceService := NewServiceService(s.db)
			createdService, err := serviceService.CreateService(service)
			if err != nil {
				slog.Error("failed to create service for metric", "metric", metric, "error", err)
				continue
			}
			serviceID = createdService.ID
		} else if err != nil {
			slog.Error("failed to check service for metric", "metric", metric, "error", err)
			continue
		}

		serviceIDs = append(serviceIDs, serviceID)
	}

	return &models.AgentRegistrationResponse{
		HostID:     hostID,
		ServiceIDs: serviceIDs,
	}, nil
}

// getOrCreateAgentService returns the service ID for the given agent metric type, creating host and service if they don't exist.
func (s *AgentService) getOrCreateAgentService(orgID int64, hostname, serviceName, serviceType string) (int64, error) {
	// Get or create host
	var hostID int64
	err := s.db.QueryRow(`SELECT id FROM hosts WHERE name = $1 AND organization_id = $2`, hostname, orgID).Scan(&hostID)
	if err == sql.ErrNoRows {
		host := models.Host{
			OrganizationID: orgID,
			Name:           hostname,
			Host:           hostname,
			Service:        serviceName,
		}
		hostService := NewHostService(s.db)
		createdHost, err := hostService.CreateHost(host)
		if err != nil {
			return 0, fmt.Errorf("%w: %w", ErrHostCreate, err)
		}
		hostID = createdHost.ID
		slog.Info("created host for agent", "host_id", hostID, "hostname", hostname, "org_id", orgID)
	} else if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrHostGet, err)
	}

	// Get or create service for this metric type
	var serviceID int64
	err = s.db.QueryRow(`SELECT id FROM services WHERE host_id = $1 AND type = $2`, hostID, serviceType).Scan(&serviceID)
	if err == sql.ErrNoRows {
		service := models.Service{
			OrganizationID:  orgID,
			HostID:          &hostID,
			Name:            serviceTypeMapToName(serviceType),
			Type:            serviceType,
			Host:            hostname,
			Service:         serviceName,
			ServiceInterval: 60,
			MaxAttempts:     3,
		}
		serviceService := NewServiceService(s.db)
		createdService, err := serviceService.CreateService(service)
		if err != nil {
			return 0, fmt.Errorf("%s: %w: %w", serviceType, ErrServiceCreate, err)
		}
		serviceID = createdService.ID
		slog.Info("created service", "service_id", serviceID, "type", serviceType, "host_id", hostID)
	} else if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrServiceGet, err)
	}

	return serviceID, nil
}

func serviceTypeMapToName(serviceType string) string {
	switch serviceType {
	case "agent_cpu":
		return "cpu"
	case "agent_ram":
		return "ram"
	case "agent_disk":
		return "disk"
	case "agent_network":
		return "network"
	default:
		return serviceType
	}
}

// StoreMetrics stores agent metrics. orgID must be the organization the agent belongs to (from install token).
func (s *AgentService) StoreMetrics(metrics models.AgentMetrics, orgID int64) error {
	slog.Debug("received agent metrics", "hostname", metrics.Hostname, "service", metrics.Service, "org_id", orgID, "metrics", metrics.Metrics)
	for metricType, value := range metrics.Metrics {
		// Filter metadata
		var filteredMetadata map[string]float64
		if len(metrics.Metadata) > 0 {
			filteredMetadata = make(map[string]float64)
			switch metricType {
			case "cpu":
				if val, ok := metrics.Metadata["load_1min"]; ok {
					filteredMetadata["load_1min"] = val
				}
				if val, ok := metrics.Metadata["load_5min"]; ok {
					filteredMetadata["load_5min"] = val
				}
				if val, ok := metrics.Metadata["load_15min"]; ok {
					filteredMetadata["load_15min"] = val
				}
			case "ram":
				if val, ok := metrics.Metadata["ram_total_gb"]; ok {
					filteredMetadata["ram_total_gb"] = val
				}
			case "disk":
				if val, ok := metrics.Metadata["disk_total_gb"]; ok {
					filteredMetadata["disk_total_gb"] = val
				}
			case "network":
				if val, ok := metrics.Metadata["network_bytes_in_total"]; ok {
					filteredMetadata["network_bytes_in_total"] = val
				}
				if val, ok := metrics.Metadata["network_bytes_out_total"]; ok {
					filteredMetadata["network_bytes_out_total"] = val
				}
				// Support both old and new naming for backward compatibility
				if val, ok := metrics.Metadata["network_speed_in_mb_per_s"]; ok {
					filteredMetadata["network_speed_in_mb_per_s"] = val
				} else if val, ok := metrics.Metadata["network_speed_in_mbps"]; ok {
					filteredMetadata["network_speed_in_mb_per_s"] = val
				}
				if val, ok := metrics.Metadata["network_speed_out_mb_per_s"]; ok {
					filteredMetadata["network_speed_out_mb_per_s"] = val
				} else if val, ok := metrics.Metadata["network_speed_out_mbps"]; ok {
					filteredMetadata["network_speed_out_mb_per_s"] = val
				}
				if val, ok := metrics.Metadata["network_ping_latency_ms"]; ok {
					filteredMetadata["network_ping_latency_ms"] = val
				}
				if val, ok := metrics.Metadata["network_ping_success"]; ok {
					filteredMetadata["network_ping_success"] = val
				}
			}
		}

		var metadataJSON *string
		if len(filteredMetadata) > 0 {
			metadataBytes, err := json.Marshal(filteredMetadata)
			if err == nil {
				metadataStr := string(metadataBytes)
				metadataJSON = &metadataStr
			}
		}

		timestamp := metrics.Timestamp
		if timestamp.IsZero() {
			timestamp = time.Now().UTC()
		} else {
			timestamp = timestamp.UTC()
		}

		// Find the service
		serviceTypeMap := map[string]string{
			"cpu":     "agent_cpu",
			"ram":     "agent_ram",
			"disk":    "agent_disk",
			"network": "agent_network",
		}
		serviceType, exists := serviceTypeMap[metricType]
		if !exists {
			continue
		}

		var serviceID int64
		var failureThreshold, warningThreshold, criticalThreshold sql.NullFloat64
		err := s.db.QueryRow(`SELECT id, failure_threshold, warning_threshold, critical_threshold FROM services WHERE host = $1 AND type = $2 AND service = $3 AND organization_id = $4`,
			metrics.Hostname, serviceType, metrics.Service, orgID).Scan(&serviceID, &failureThreshold, &warningThreshold, &criticalThreshold)
		if err != nil {
			if err == sql.ErrNoRows {
				serviceID, err = s.getOrCreateAgentService(orgID, metrics.Hostname, metrics.Service, serviceType)
				if err != nil {
					slog.Error("failed to get or create service for metric", "metric", metricType, "error", err)
					continue
				}
			} else {
				slog.Error("service lookup failed for metric", "metric", metricType, "error", err)
				continue
			}
		}

		// Determine status from the two-level thresholds (warning/critical), falling
		// back to the legacy single failure_threshold, then a 90% default. This is
		// what drives the status dot in the UI for cpu/ram/disk metrics. Network
		// compares its ping latency (ms) instead of metric_value (throughput MB/s).
		status := "success"
		var message *string

		critical := 90.0
		if failureThreshold.Valid {
			critical = failureThreshold.Float64
		}
		if criticalThreshold.Valid {
			critical = criticalThreshold.Float64
		}
		hasWarning := warningThreshold.Valid

		if metricType == "cpu" || metricType == "ram" || metricType == "disk" {
			label := map[string]string{"cpu": "CPU", "ram": "RAM", "disk": "Disk"}[metricType]
			if value > critical {
				status = "failure"
				msg := fmt.Sprintf("%s usage is %.2f%% (critical threshold %.0f%%)", label, value, critical)
				message = &msg
			} else if hasWarning && value > warningThreshold.Float64 {
				status = "warning"
				msg := fmt.Sprintf("%s usage is %.2f%% (warning threshold %.0f%%)", label, value, warningThreshold.Float64)
				message = &msg
			}
		}

		// Ping latency also fills the latency column so the threshold evaluator's
		// generic latency path picks network up. A failed ping (ICMP blocked, no
		// ping binary) stores no latency and stays success rather than false-alarm.
		var pingLatency *float64
		if metricType == "network" && filteredMetadata["network_ping_success"] == 1 {
			if lat, ok := filteredMetadata["network_ping_latency_ms"]; ok {
				pingLatency = &lat
			}
		}
		if pingLatency != nil {
			if criticalThreshold.Valid && *pingLatency > criticalThreshold.Float64 {
				status = "failure"
				msg := fmt.Sprintf("Network ping latency is %.1fms (critical threshold %.0fms)", *pingLatency, criticalThreshold.Float64)
				message = &msg
			} else if warningThreshold.Valid && *pingLatency > warningThreshold.Float64 {
				status = "warning"
				msg := fmt.Sprintf("Network ping latency is %.1fms (warning threshold %.0fms)", *pingLatency, warningThreshold.Float64)
				message = &msg
			}
		}

		// Insert into service_results
		query := `INSERT INTO service_results (service_id, status, message, timestamp, metric_type, metric_value, metadata, latency)
				  VALUES ($1, $2, $3, ($4::text::timestamp AT TIME ZONE 'UTC'), $5, $6, $7, $8)`
		timestampStr := timestamp.Format("2006-01-02 15:04:05.999999")
		slog.Debug("storing agent metric", "metric", metricType, "service_id", serviceID, "value", value, "timestamp", timestampStr)
		_, err = s.db.Exec(query, serviceID, status, message, timestampStr, metricType, value, metadataJSON, pingLatency)
		if err != nil {
			slog.Error("failed to store agent metric", "metric", metricType, "error", err)
			continue
		}
	}

	return nil
}

func (s *AgentService) GetAgentMetrics(serviceID int64, limit int) ([]models.AgentMetricPoint, error) {
	// Get the service
	var service models.Service
	err := s.db.QueryRow(`SELECT id, host, type, name, service FROM services WHERE id = $1`, serviceID).Scan(
		&service.ID, &service.Host, &service.Type, &service.Name, &service.Service)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrServiceNotFound
		}
		return nil, fmt.Errorf("%w: %w", ErrServiceFetch, err)
	}

	metricTypeMap := map[string]string{
		"agent_cpu":     "cpu",
		"agent_ram":     "ram",
		"agent_disk":    "disk",
		"agent_network": "network",
	}
	metricType, exists := metricTypeMap[service.Type]
	if !exists {
		return nil, ErrNotAgentService
	}

	query := `SELECT metric_value, metadata, timestamp 
			  FROM service_results 
			  WHERE service_id = $1 AND metric_type = $2
			  ORDER BY timestamp DESC 
			  LIMIT $3`

	rows, err := s.db.Query(query, serviceID, metricType, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServiceResultsForAgentMetricsQuery, err)
	}
	defer rows.Close()

	var metrics []models.AgentMetricPoint
	for rows.Next() {
		var m models.AgentMetricPoint
		var metricValue sql.NullFloat64
		var metadataJSON sql.NullString
		if err := rows.Scan(&metricValue, &metadataJSON, &m.Timestamp); err != nil {
			slog.Error("failed to scan row", "error", err)
			continue
		}
		if !metricValue.Valid {
			continue
		}
		m.Value = metricValue.Float64
		m.Timestamp = m.Timestamp.UTC()
		if metadataJSON.Valid && metadataJSON.String != "" {
			var metadata map[string]float64
			if err := json.Unmarshal([]byte(metadataJSON.String), &metadata); err == nil {
				m.Metadata = metadata
			}
		}
		metrics = append(metrics, m)
	}

	// Reverse to get chronological order
	for i, j := 0, len(metrics)-1; i < j; i, j = i+1, j-1 {
		metrics[i], metrics[j] = metrics[j], metrics[i]
	}

	return metrics, nil
}
