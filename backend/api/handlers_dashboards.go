package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/models"
	"middle-monitor/backend/services"

	"github.com/gorilla/mux"
)

// ==========================================
// Custom Dashboards Handlers
// ==========================================

func handleGetCustomDashboards(db *sql.DB) http.HandlerFunc {
	svc := services.NewCustomDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		dashboards, err := svc.List(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if dashboards == nil {
			dashboards = []models.CustomDashboard{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dashboards)
	}
}

func handleCreateCustomDashboard(db *sql.DB) http.HandlerFunc {
	svc := services.NewCustomDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		var d models.CustomDashboard
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			respondError(w, http.StatusBadRequest, ErrNameRequired)
			return
		}
		d.OrganizationID = orgID
		created, err := svc.Create(d)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}
}

func handleUpdateCustomDashboard(db *sql.DB) http.HandlerFunc {
	svc := services.NewCustomDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		dashboardID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrIDInvalid)
			return
		}
		var d models.CustomDashboard
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			respondError(w, http.StatusBadRequest, ErrNameRequired)
			return
		}
		updated, err := svc.Update(dashboardID, orgID, d)
		if err != nil {
			if errors.Is(err, services.ErrDashboardNotFound) {
				respondError(w, http.StatusNotFound, err)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)
	}
}

func handleDeleteCustomDashboard(db *sql.DB) http.HandlerFunc {
	svc := services.NewCustomDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		dashboardID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrIDInvalid)
			return
		}
		if err := svc.Delete(dashboardID, orgID); err != nil {
			if errors.Is(err, services.ErrDashboardNotFound) {
				respondError(w, http.StatusNotFound, err)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
