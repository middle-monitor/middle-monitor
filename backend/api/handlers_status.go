package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// handlePublicStatus serves Middle Monitor's own status page data, unauthenticated.
//
// The org is the self-monitoring one (SELF_MONITOR_ORG_SLUG), the same org the
// seeded checks in self_monitor.go report into, so the page shows the real
// results of the checks Middle Monitor runs against its own endpoints. It is
// deliberately not slug-addressable: publishing any org's checks would be a
// different, opt-in product feature.
func handlePublicStatus(db *sql.DB) http.HandlerFunc {
	statusService := services.NewStatusPageService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(os.Getenv("SELF_MONITOR_ORG_SLUG"))
		if slug == "" {
			respondError(w, http.StatusNotFound, services.ErrStatusPageNotFound)
			return
		}
		writeStatusPage(w, statusService, slug)
	}
}

// handlePublicOrgStatus serves an organization's own status page, unauthenticated.
// Publishing is opt-in per check, so an org that published nothing answers 404.
func handlePublicOrgStatus(db *sql.DB) http.HandlerFunc {
	statusService := services.NewStatusPageService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		writeStatusPage(w, statusService, mux.Vars(r)["org_slug"])
	}
}

func writeStatusPage(w http.ResponseWriter, statusService *services.StatusPageService, slug string) {
	page, err := statusService.Get(slug)
	if err != nil {
		if errors.Is(err, services.ErrStatusPageNotFound) {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	// Checks run on a 60s interval, so a minute of caching costs no freshness
	// and keeps a status page from amplifying the incident it is reporting.
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

// handleGetStatusPageChecks lists the checks the org can publish and which are.
func handleGetStatusPageChecks(db *sql.DB) http.HandlerFunc {
	statusService := services.NewStatusPageService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		checks, err := statusService.PublishableChecks(middleware.GetOrganizationID(r.Context()))
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, checks)
	}
}

// handleSetStatusPageChecks publishes exactly the listed checks. Admin only:
// it makes monitoring data readable by anyone.
func handleSetStatusPageChecks(db *sql.DB) http.HandlerFunc {
	statusService := services.NewStatusPageService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ServiceIDs []int64 `json:"service_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		orgID := middleware.GetOrganizationID(r.Context())
		if err := statusService.SetPublishedChecks(orgID, body.ServiceIDs); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		checks, err := statusService.PublishableChecks(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, checks)
	}
}
