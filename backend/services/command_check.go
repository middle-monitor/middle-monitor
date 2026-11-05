package services

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"middle-monitor/backend/models"
)

// CommandType is a check run by a zone collector with a command declared on
// the collector's machine (a Nagios plugin, check_nrpe, a custom script).
const CommandType = "command"

// MinCommandInterval is lower than other checks: Icinga users run their
// critical checks every 30 seconds.
const MinCommandInterval = 30

// CommandZoneExpr reads a check's zone without casting other types' credentials,
// and matches the expression of idx_services_command_zone.
const CommandZoneExpr = `(CASE WHEN type = 'command' THEN (credentials::jsonb)->>'zone' END)`

// CollectorRefreshSeconds is how often a collector asks for its checks again.
const CollectorRefreshSeconds = 30

// CommandCheckConfig is what a command check stores in services.credentials.
// The collector only accepts command names it declares itself, so nothing in
// here can make it run a binary its operator did not list.
type CommandCheckConfig struct {
	Zone    string              `json:"zone"`
	Command string              `json:"command"`
	Vars    map[string][]string `json:"vars,omitempty"`
}

// Same character sets as the collector, so a name the API accepts is one the
// collector can match.
var (
	commandNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,255}$`)
	commandVarPattern  = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

// ParseCommandCheckConfig reads and validates a command check's config.
func ParseCommandCheckConfig(credentials *string) (CommandCheckConfig, error) {
	var cfg CommandCheckConfig
	if credentials == nil || strings.TrimSpace(*credentials) == "" {
		return cfg, ErrCommandConfigInvalid
	}
	if err := json.Unmarshal([]byte(*credentials), &cfg); err != nil {
		return cfg, ErrCommandConfigInvalid
	}
	if !commandNamePattern.MatchString(cfg.Zone) {
		return cfg, ErrCommandZoneInvalid
	}
	if !commandNamePattern.MatchString(cfg.Command) {
		return cfg, ErrCommandNameInvalid
	}
	for name, values := range cfg.Vars {
		if !commandVarPattern.MatchString(name) {
			return cfg, ErrCommandVarInvalid
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\x00\r\n") {
				return cfg, ErrCommandVarInvalid
			}
		}
	}
	return cfg, nil
}

// CommandVars returns the variables sent to the collector. $host$ defaults to
// the check's host, which is what check_nrpe -H and most plugins need.
func CommandVars(cfg CommandCheckConfig, host string) map[string][]string {
	vars := make(map[string][]string, len(cfg.Vars)+1)
	for name, values := range cfg.Vars {
		vars[name] = values
	}
	if _, ok := vars["host"]; !ok && host != "" {
		vars["host"] = []string{host}
	}
	return vars
}

// CollectorService records collectors and serves them their checks.
type CollectorService struct {
	db *sql.DB
}

func NewCollectorService(db *sql.DB) *CollectorService {
	return &CollectorService{db: db}
}

// Sync records the collector as seen and returns the checks of its zone.
func (s *CollectorService) Sync(orgID int64, sync models.CollectorSync) (models.CollectorSyncResponse, error) {
	commands, err := json.Marshal(sync.Commands)
	if err != nil {
		return models.CollectorSyncResponse{}, fmt.Errorf("%w: %w", ErrCollectorSync, err)
	}
	_, err = s.db.Exec(`
		INSERT INTO collectors (organization_id, zone, hostname, version, commands, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (organization_id, zone, hostname)
		DO UPDATE SET version = EXCLUDED.version, commands = EXCLUDED.commands, last_seen_at = NOW()`,
		orgID, sync.Zone, sync.Hostname, sync.Version, string(commands))
	if err != nil {
		return models.CollectorSyncResponse{}, fmt.Errorf("%w: %w", ErrCollectorSync, err)
	}

	checks, err := s.zoneChecks(orgID, sync.Zone)
	if err != nil {
		return models.CollectorSyncResponse{}, err
	}
	return models.CollectorSyncResponse{Checks: checks, RefreshSeconds: CollectorRefreshSeconds}, nil
}

func (s *CollectorService) zoneChecks(orgID int64, zone string) ([]models.CollectorAssignment, error) {
	rows, err := s.db.Query(`
		SELECT id, name, host, credentials, service_interval
		  FROM services
		 WHERE organization_id = $1 AND type = 'command'
		   AND `+CommandZoneExpr+` = $2
		 ORDER BY id`, orgID, zone)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCollectorChecks, err)
	}
	defer rows.Close()

	checks := []models.CollectorAssignment{}
	for rows.Next() {
		var check models.CollectorAssignment
		var host string
		var credentials *string
		if err := rows.Scan(&check.ServiceID, &check.Name, &host, &credentials, &check.Interval); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCollectorChecks, err)
		}
		cfg, err := ParseCommandCheckConfig(credentials)
		if err != nil {
			// Written through the API, so only a manual edit gets here; the
			// other checks of the zone must still run.
			continue
		}
		check.Command = cfg.Command
		check.Vars = CommandVars(cfg, host)
		checks = append(checks, check)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCollectorChecks, err)
	}
	return checks, nil
}
