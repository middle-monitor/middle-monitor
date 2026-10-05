package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// incidentStatusRequest is what an operator or an automated system sends to move
// an incident. `note` and `resolution_note` are the same field: the second is
// the name the dashboard has always sent.
type incidentStatusRequest struct {
	Status         string  `json:"status"`
	Note           *string `json:"note,omitempty"`
	ResolutionNote *string `json:"resolution_note,omitempty"`
	Actor          string  `json:"actor,omitempty"`
}

func (r incidentStatusRequest) note() *string {
	if r.Note != nil {
		return r.Note
	}
	return r.ResolutionNote
}

// incidentSnapshot is what the transition needs to know about the incident
// before it is changed: the event carries the status it came from.
type incidentSnapshot struct {
	incident models.Incident
	previous string
	ruleID   sql.NullInt64
}

func loadIncident(db *sql.DB, incidentID, orgID int64) (incidentSnapshot, error) {
	var snap incidentSnapshot
	var description, service sql.NullString
	var serviceID, hostID sql.NullInt64
	err := db.QueryRow(
		`SELECT i.id, i.organization_id, i.alert_rule_id, i.service_id, i.host_id, i.title,
		        i.description, i.severity, i.status, i.started_at, i.acknowledged_at, i.resolved_at,
		        s.name
		   FROM incidents i
		   LEFT JOIN services s ON s.id = i.service_id
		  WHERE i.id = $1 AND i.organization_id = $2`, incidentID, orgID).
		Scan(&snap.incident.ID, &snap.incident.OrganizationID, &snap.ruleID, &serviceID, &hostID,
			&snap.incident.Title, &description, &snap.incident.Severity, &snap.incident.Status,
			&snap.incident.StartedAt, &snap.incident.AcknowledgedAt, &snap.incident.ResolvedAt, &service)
	if err != nil {
		return snap, err
	}

	snap.previous = snap.incident.Status
	if snap.ruleID.Valid {
		id := snap.ruleID.Int64
		snap.incident.AlertRuleID = &id
	}
	if serviceID.Valid {
		id := serviceID.Int64
		snap.incident.ServiceID = &id
	}
	if hostID.Valid {
		id := hostID.Int64
		snap.incident.HostID = &id
	}
	if description.Valid {
		snap.incident.Description = &description.String
	}
	if service.Valid {
		snap.incident.Service = &service.String
	}
	return snap, nil
}

// incidentDedupKey is the stable key a webhook receiver gets and can send back,
// so it has nothing to remember between two messages.
func incidentDedupKey(snap incidentSnapshot) string {
	if snap.ruleID.Valid {
		return fmt.Sprintf("mm-rule-%d", snap.ruleID.Int64)
	}
	if snap.incident.ServiceID != nil {
		return fmt.Sprintf("mm-svc-%d", *snap.incident.ServiceID)
	}
	return ""
}

// eventForTransition maps a status change to the event a receiver switches on.
func eventForTransition(previous, status string) string {
	switch strings.ToLower(status) {
	case "acknowledged":
		return services.EventIncidentAcknowledged
	case "resolved":
		return services.EventIncidentResolved
	case "open":
		if strings.EqualFold(previous, "resolved") {
			return services.EventIncidentReopened
		}
		return services.EventIncidentOpened
	default:
		return services.EventIncidentAcknowledged
	}
}

// applyIncidentStatus performs the transition and publishes it. It is the one
// place that knows a status change is an event, whether it came from the
// dashboard or from an automated remediation.
func applyIncidentStatus(db *sql.DB, w http.ResponseWriter, r *http.Request, snap incidentSnapshot, body incidentStatusRequest) {
	orgID := snap.incident.OrganizationID
	alertService := services.NewAlertService(db)

	var userID *int64
	if id := middleware.GetUserID(r.Context()); id != 0 {
		userID = &id
	}

	if err := alertService.UpdateIncidentStatusBy(snap.incident.ID, orgID, body.Status, userID, body.note(), body.Actor); err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	updated, err := loadIncident(db, snap.incident.ID, orgID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}

	// Notifying is external work: it must not hold the HTTP response open.
	eventType := eventForTransition(snap.previous, body.Status)
	go publishIncidentTransition(db, updated, snap.previous, eventType, body)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "updated",
		"incident_id":     updated.incident.ID,
		"previous_status": snap.previous,
		"dedup_key":       incidentDedupKey(updated),
	})
}

func publishIncidentTransition(db *sql.DB, snap incidentSnapshot, previous, eventType string, body incidentStatusRequest) {
	dedupKey := incidentDedupKey(snap)
	if dedupKey == "" {
		return
	}

	event := services.IncidentEvent(eventType, snap.incident, previous, dedupKey,
		services.IncidentURL(db, snap.incident.OrganizationID, snap.incident.ID))
	event.Title = incidentEventTitle(eventType, snap.incident.Title)
	event.Message = incidentEventMessage(eventType, snap.incident, body)
	if body.Actor != "" {
		event.Labels = map[string]string{"actor": body.Actor}
	}
	services.FillEventTargets(db, &event)
	services.RouteIncidentEvent(db, snap.incident.OrganizationID, event, snap.incident.ServiceID, snap.incident.HostID)
}

func incidentEventTitle(eventType, title string) string {
	switch eventType {
	case services.EventIncidentResolved:
		return fmt.Sprintf("[RESOLVED] %s", title)
	case services.EventIncidentAcknowledged:
		return fmt.Sprintf("[ACK] %s", title)
	case services.EventIncidentReopened:
		return fmt.Sprintf("[REOPENED] %s", title)
	default:
		return title
	}
}

func incidentEventMessage(eventType string, incident models.Incident, body incidentStatusRequest) string {
	actor := body.Actor
	if actor == "" {
		actor = "a user"
	}
	message := fmt.Sprintf("Incident #%d '%s' %s by %s.", incident.ID, incident.Title, strings.TrimPrefix(eventType, "incident."), actor)
	if note := body.note(); note != nil && strings.TrimSpace(*note) != "" {
		message += " Note: " + strings.TrimSpace(*note)
	}
	return message
}

// handleUpdateIncidentStatus moves one incident by id.
func handleUpdateIncidentStatus(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		incidentID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrIncidentIDInvalid)
			return
		}

		var body incidentStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		snap, err := loadIncident(db, incidentID, orgID)
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, ErrIncidentNotFound)
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		applyIncidentStatus(db, w, r, snap, body)
	}
}

// handleUpdateIncidentStatusByDedupKey moves the incident a webhook receiver was
// told about, without it having to store the numeric id between two messages.
func handleUpdateIncidentStatusByDedupKey(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		key := mux.Vars(r)["key"]

		var body incidentStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		incidentID, err := incidentIDForDedupKey(db, orgID, key)
		if err != nil {
			respondError(w, http.StatusNotFound, ErrIncidentNotFound)
			return
		}

		snap, err := loadIncident(db, incidentID, orgID)
		if err != nil {
			respondError(w, http.StatusNotFound, ErrIncidentNotFound)
			return
		}

		applyIncidentStatus(db, w, r, snap, body)
	}
}

// incidentIDForDedupKey resolves mm-rule-<id> / mm-svc-<id> to the live incident
// it names: the most recent one that is not resolved, falling back to the most
// recent one at all so a late acknowledgement still lands somewhere sensible.
func incidentIDForDedupKey(db *sql.DB, orgID int64, key string) (int64, error) {
	var column string
	var rawID string
	switch {
	case strings.HasPrefix(key, "mm-rule-"):
		column, rawID = "alert_rule_id", strings.TrimPrefix(key, "mm-rule-")
	case strings.HasPrefix(key, "mm-svc-"):
		column, rawID = "service_id", strings.TrimPrefix(key, "mm-svc-")
	default:
		return 0, ErrDedupKeyUnknown
	}

	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return 0, ErrDedupKeyUnknown
	}

	var incidentID int64
	err = db.QueryRow(
		`SELECT id FROM incidents
		  WHERE organization_id = $1 AND `+column+` = $2
		  ORDER BY (status <> 'resolved') DESC, started_at DESC
		  LIMIT 1`, orgID, id).Scan(&incidentID)
	if err != nil {
		return 0, ErrIncidentNotFound
	}
	return incidentID, nil
}
