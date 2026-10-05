package services

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// SuggestionService generates link suggestions from trace data
type SuggestionService struct {
	db         *sql.DB
	opensearch *OpenSearchService
}

// NewSuggestionService creates a new suggestion service
func NewSuggestionService(db *sql.DB, opensearch *OpenSearchService) *SuggestionService {
	return &SuggestionService{db: db, opensearch: opensearch}
}

// Reason codes returned alongside an empty suggestion list, so the UI can
// explain *why* there is nothing to suggest instead of showing a flat "no
// suggestions" for every case.
const (
	ReasonTracingNotConfigured = "tracing_not_configured" // no OpenSearch / OTel pipeline
	ReasonNoTraceData          = "no_trace_data"          // tracing is up, but this app has no recent spans
)

// GetSuggestions returns suggested application links for appServiceName by analyzing
// recent trace spans and matching destination hints (peer.service, server.address, db.name, etc.) to org hosts and services.
// The second return value is a reason code explaining an empty result; it is
// only set when the emptiness is due to missing data, not a lack of matches.
func (s *SuggestionService) GetSuggestions(orgID int64, appServiceName string) ([]models.LinkSuggestion, string, error) {
	if s.opensearch == nil || !s.opensearch.initialized {
		return nil, ReasonTracingNotConfigured, nil
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -7)
	startTime := start.Format(time.RFC3339)
	endTime := end.Format(time.RFC3339)

	spans, _, err := s.opensearch.SearchTraceSpansForService(appServiceName, startTime, endTime, 300)
	if err != nil || len(spans) == 0 {
		return nil, ReasonNoTraceData, nil
	}

	hints := extractDestinationHintsFromSpans(spans)
	if len(hints) == 0 {
		return nil, ReasonNoTraceData, nil
	}

	hosts, err := s.getHostsForOrg(orgID)
	if err != nil {
		return nil, "", err
	}
	services, err := s.getServicesForOrg(orgID)
	if err != nil {
		return nil, "", err
	}

	existingLinks, _ := s.getExistingLinkTargets(orgID, appServiceName)

	suggestions := matchHintsToTargets(hints, hosts, services, existingLinks)
	return suggestions, "", nil
}

func extractDestinationHintsFromSpans(spans []map[string]interface{}) map[string]bool {
	hints := make(map[string]bool)
	for _, doc := range spans {
		attrs, _ := doc["attributes"].(map[string]interface{})
		if attrs == nil {
			continue
		}
		for _, key := range []string{"peer.service", "server.address", "db.name", "net.peer.name", "http.host", "http.url"} {
			v := attrs[key]
			if v == nil {
				continue
			}
			var s string
			switch val := v.(type) {
			case string:
				s = val
			default:
				continue
			}
			s = strings.TrimSpace(strings.ToLower(s))
			if s == "" {
				continue
			}
			if key == "http.url" {
				if u, err := url.Parse(s); err == nil && u.Host != "" {
					s = strings.ToLower(u.Hostname())
				}
			}
			if len(s) > 1 && len(s) < 256 {
				hints[s] = true
			}
		}
	}
	return hints
}

func (s *SuggestionService) getHostsForOrg(orgID int64) ([]struct {
	ID   int64
	Name string
	Host string
}, error) {
	rows, err := s.db.Query(`SELECT id, name, host FROM hosts WHERE organization_id = $1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		ID   int64
		Name string
		Host string
	}
	for rows.Next() {
		var h struct {
			ID   int64
			Name string
			Host string
		}
		if err := rows.Scan(&h.ID, &h.Name, &h.Host); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *SuggestionService) getServicesForOrg(orgID int64) ([]struct {
	ID      int64
	Name    string
	Host    string
	Service string
}, error) {
	rows, err := s.db.Query(`SELECT id, name, host, service FROM services WHERE organization_id = $1 AND type NOT LIKE 'agent_%' AND type NOT LIKE 'error_service_%'`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}
	for rows.Next() {
		var svc struct {
			ID      int64
			Name    string
			Host    string
			Service string
		}
		if err := rows.Scan(&svc.ID, &svc.Name, &svc.Host, &svc.Service); err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

func (s *SuggestionService) getExistingLinkTargets(orgID int64, appServiceName string) (map[string]bool, error) {
	rows, err := s.db.Query(
		`SELECT target_type, target_id FROM application_links WHERE organization_id = $1 AND app_service_name = $2`,
		orgID, appServiceName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var tt string
		var tid int64
		if err := rows.Scan(&tt, &tid); err != nil {
			return nil, err
		}
		seen[fmt.Sprintf("%s-%d", tt, tid)] = true
	}
	return seen, rows.Err()
}

func matchHintsToTargets(
	hints map[string]bool,
	hosts []struct {
		ID   int64
		Name string
		Host string
	},
	services []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	},
	existing map[string]bool,
) []models.LinkSuggestion {
	added := make(map[string]bool)
	var out []models.LinkSuggestion

	norm := func(s string) string { return strings.TrimSpace(strings.ToLower(s)) }
	matchesHint := func(hint, target string) bool {
		if target == "" {
			return false
		}
		t := norm(target)
		return t == hint || strings.Contains(t, hint) || strings.Contains(hint, t)
	}

	for hint := range hints {
		for _, h := range hosts {
			key := fmt.Sprintf("host-%d", h.ID)
			if existing[key] || added[key] {
				continue
			}
			if matchesHint(hint, h.Name) || matchesHint(hint, h.Host) {
				added[key] = true
				out = append(out, models.LinkSuggestion{
					TargetType: "host",
					TargetID:   h.ID,
					TargetName: h.Name,
					Reason:     "Appears in traces as peer/destination",
				})
			}
		}
		for _, svc := range services {
			key := fmt.Sprintf("service-%d", svc.ID)
			if existing[key] || added[key] {
				continue
			}
			if matchesHint(hint, svc.Name) || matchesHint(hint, svc.Host) || matchesHint(hint, svc.Service) {
				added[key] = true
				out = append(out, models.LinkSuggestion{
					TargetType: "service",
					TargetID:   svc.ID,
					TargetName: svc.Name,
					Reason:     "Appears in traces as peer/destination",
				})
			}
		}
	}
	return out
}
