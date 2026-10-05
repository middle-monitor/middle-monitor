package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// maxAgentConfigBytes bounds what an operator can push to a machine. A scrape
// fragment is a few dozen lines; anything larger is a mistake, and this is code
// that ends up executed by an agent.
const maxAgentConfigBytes = 64 << 10

// AgentConfigDocument is the scrape fragment stored for one host.
type AgentConfigDocument struct {
	ScrapeConfig string     `json:"scrape_config"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

// handleGetHostAgentConfig returns the fragment an operator set for a host.
func handleGetHostAgentConfig(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		hostID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}

		document, err := readHostAgentConfig(db, orgID, hostID)
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, ErrHostNotFound)
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, document)
	}
}

// handleSetHostAgentConfig stores the fragment, refusing anything the agent
// would not be able to parse: a config that only fails on the machine is a
// config nobody can debug.
func handleSetHostAgentConfig(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		hostID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}

		var body AgentConfigDocument
		if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentConfigBytes)).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if len(body.ScrapeConfig) > maxAgentConfigBytes {
			respondError(w, http.StatusBadRequest, ErrAgentConfigTooLarge)
			return
		}
		if err := validateScrapeFragment(body.ScrapeConfig); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}

		result, err := db.Exec(
			`UPDATE hosts SET agent_scrape_config = $1, agent_config_updated_at = NOW()
			  WHERE id = $2 AND organization_id = $3`,
			nullIfEmptyString(body.ScrapeConfig), hostID, orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			respondError(w, http.StatusNotFound, ErrHostNotFound)
			return
		}

		document, err := readHostAgentConfig(db, orgID, hostID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, document)
	}
}

// handleAgentConfig is what the agent itself calls, authenticated with its
// install token, to fetch the fragment for its own host.
func handleAgentConfig(db *sql.DB) http.HandlerFunc {
	installTokenService := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		hostname := r.URL.Query().Get("host")
		if hostname == "" {
			respondError(w, http.StatusBadRequest, ErrHostNameRequired)
			return
		}

		token := r.Header.Get("X-Install-Token")
		if token == "" {
			if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
				token = auth[7:]
			}
		}
		if token == "" {
			respondError(w, http.StatusUnauthorized, ErrInstallTokenRequired)
			return
		}
		orgID, err := installTokenService.ValidateToken(token)
		if err != nil {
			respondError(w, http.StatusUnauthorized, ErrInstallTokenInvalid)
			return
		}

		var config sql.NullString
		var updatedAt sql.NullTime
		err = db.QueryRow(
			`SELECT agent_scrape_config, agent_config_updated_at
			   FROM hosts WHERE organization_id = $1 AND name = $2`, orgID, hostname).
			Scan(&config, &updatedAt)
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, ErrHostNotFound)
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		document := AgentConfigDocument{ScrapeConfig: config.String}
		if updatedAt.Valid {
			stamp := updatedAt.Time.UTC()
			document.UpdatedAt = &stamp
			// The agent skips the parse when nothing changed.
			w.Header().Set("Last-Modified", stamp.Format(http.TimeFormat))
		}
		respondJSON(w, document)
	}
}

func readHostAgentConfig(db *sql.DB, orgID, hostID int64) (AgentConfigDocument, error) {
	var config sql.NullString
	var updatedAt sql.NullTime
	err := db.QueryRow(
		`SELECT agent_scrape_config, agent_config_updated_at
		   FROM hosts WHERE id = $1 AND organization_id = $2`, hostID, orgID).
		Scan(&config, &updatedAt)
	if err != nil {
		return AgentConfigDocument{}, err
	}

	document := AgentConfigDocument{ScrapeConfig: config.String}
	if updatedAt.Valid {
		stamp := updatedAt.Time.UTC()
		document.UpdatedAt = &stamp
	}
	return document, nil
}

// validateScrapeFragment parses the fragment the way the agent will. An empty
// document clears the configuration.
func validateScrapeFragment(fragment string) error {
	if fragment == "" {
		return nil
	}

	// Both shapes the agent accepts: a bare list of targets, or a targets block.
	var asList []map[string]interface{}
	if err := yaml.Unmarshal([]byte(fragment), &asList); err == nil {
		return validateFragmentTargets(asList)
	}

	var asMapping struct {
		Targets []map[string]interface{} `yaml:"targets"`
	}
	if err := yaml.Unmarshal([]byte(fragment), &asMapping); err != nil {
		return ErrAgentConfigInvalid
	}
	if asMapping.Targets == nil {
		return ErrAgentConfigInvalid
	}
	return validateFragmentTargets(asMapping.Targets)
}

func validateFragmentTargets(targets []map[string]interface{}) error {
	if len(targets) == 0 {
		return ErrAgentConfigInvalid
	}
	for _, target := range targets {
		url, _ := target["url"].(string)
		if url == "" {
			return ErrAgentConfigTargetURL
		}
	}
	return nil
}

func nullIfEmptyString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
