package api

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// handleGetHostIngestCost breaks a host's metric points down by scrape target,
// with the organization's budget beside them, so the page can say what to cut.
func handleGetHostIngestCost(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		hostID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}

		var hostname string
		err = db.QueryRow(`SELECT name FROM hosts WHERE id = $1 AND organization_id = $2`, hostID, orgID).Scan(&hostname)
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, ErrHostNotFound)
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		limits, err := services.NewPlanLimitsService(db).GetLimits(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		response := struct {
			*services.HostIngestCost
			OrgPointsPerMinuteLimit int  `json:"org_points_per_minute_limit"` // -1 = unlimited
			Enforced                bool `json:"enforced"`
		}{
			HostIngestCost:          &services.HostIngestCost{Targets: []services.TargetIngestCost{}},
			OrgPointsPerMinuteLimit: services.PointsPerMinute(limits),
			Enforced:                ingestBudgetEnforced(),
		}
		if opensearch != nil {
			cost, err := opensearch.HostIngestCost(r.Context(), orgID, hostname)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			response.HostIngestCost = cost
		}
		respondJSON(w, response)
	}
}
