package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/models"
	"middle-monitor/backend/services"

	"github.com/gorilla/mux"
)

// handleGetMaintenanceWindows lists the org's downtime windows.
func handleGetMaintenanceWindows(db *sql.DB) http.HandlerFunc {
	maint := services.NewMaintenanceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		windows, err := maint.List(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(windows)
	}
}

// handleCreateMaintenanceWindow creates a downtime window for a service or host.
func handleCreateMaintenanceWindow(db *sql.DB) http.HandlerFunc {
	maint := services.NewMaintenanceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())

		var req struct {
			Name       string    `json:"name"`
			TargetType string    `json:"target_type"`
			TargetID   int64     `json:"target_id"`
			StartsAt   time.Time `json:"starts_at"`
			EndsAt     time.Time `json:"ends_at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if err := validateName("name", req.Name, maxNameLen); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateMaintenanceTargetType(req.TargetType); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if req.TargetID == 0 {
			respondError(w, http.StatusBadRequest, ErrTargetIDRequired)
			return
		}
		if !req.EndsAt.After(req.StartsAt) {
			respondError(w, http.StatusBadRequest, ErrWindowRangeInvalid)
			return
		}

		win := models.MaintenanceWindow{
			OrganizationID: orgID,
			Name:           req.Name,
			TargetType:     req.TargetType,
			TargetID:       req.TargetID,
			StartsAt:       req.StartsAt,
			EndsAt:         req.EndsAt,
		}
		if claims := middleware.GetClaimsFromContext(r.Context()); claims != nil {
			uid := claims.UserID
			win.CreatedBy = &uid
		}

		created, err := maint.Create(win)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}
}

// handleDeleteMaintenanceWindow removes a downtime window.
func handleDeleteMaintenanceWindow(db *sql.DB) http.HandlerFunc {
	maint := services.NewMaintenanceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrIDInvalid)
			return
		}
		if err := maint.Delete(id, orgID); err != nil {
			respondError(w, http.StatusNotFound, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
