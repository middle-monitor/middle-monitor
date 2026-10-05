package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"middle-monitor/backend/services"
)

type ContactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
}

func handleContactUs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Cap the body to prevent abuse
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64KB

		var req ContactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if req.Name == "" || req.Email == "" || req.Message == "" {
			respondError(w, http.StatusBadRequest, ErrContactFieldsMissing)
			return
		}

		// Always log first so the message survives an SMTP outage, then forward it.
		slog.Info("contact message received", "name", req.Name, "email", req.Email, "message", req.Message)
		if err := services.SendContactEmail(req.Name, req.Email, req.Message); err != nil {
			slog.Error("contact email forward failed", "error", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}
