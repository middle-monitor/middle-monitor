package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// handleListDeliveries returns the recent webhook deliveries of one channel.
// Debugging an integration without this means asking the customer to reproduce
// the alert.
func handleListDeliveries(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		channelID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrChannelIDInvalid)
			return
		}

		limit, offset := parseLimitOffset(r, 50, 200)
		deliveries, total, err := services.ListDeliveries(db, orgID, channelID, limit, offset)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		json.NewEncoder(w).Encode(deliveries)
	}
}

// handleReplayDelivery re-sends a recorded delivery as it was sent the first
// time, re-signed with a fresh timestamp.
func handleReplayDelivery(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)

		channelID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrChannelIDInvalid)
			return
		}
		deliveryID, err := strconv.ParseInt(vars["deliveryID"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrDeliveryIDInvalid)
			return
		}

		delivery, err := services.ReplayDelivery(db, orgID, channelID, deliveryID)
		if err == services.ErrDeliveryNotFound {
			respondError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			// The replay itself failing is a receiver problem, not ours: the
			// caller needs the status the receiver answered with.
			respondError(w, http.StatusUnprocessableEntity, err)
			return
		}

		respondJSON(w, delivery)
	}
}
