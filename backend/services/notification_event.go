package services

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"middle-monitor/backend/models"
)

// Event types a webhook receiver can switch on. A consumer that only ever
// learns about the opening is condemned to poll the API to know when to close
// what it started, so every transition is published.
const (
	EventIncidentOpened       = "incident.opened"
	EventIncidentAcknowledged = "incident.acknowledged"
	EventIncidentResolved     = "incident.resolved"
	EventIncidentReopened     = "incident.reopened"
	EventIncidentEscalated    = "incident.escalated"
	EventErrorNew             = "error.new"
	EventTest                 = "test"
	EventHeartbeat            = "heartbeat"
)

// webhookPayloadVersion is sent with every structured payload so a receiver can
// tell the shape it is parsing.
const webhookPayloadVersion = "1"

// NotificationEvent carries what happened, typed, from the place that knows it
// down to the channel that delivers it. The Slack-shaped payload encodes the
// severity in a colour and the resolution in a string prefix; a machine
// consumer cannot use either.
type NotificationEvent struct {
	Event      string
	EventID    string
	DedupKey   string
	OccurredAt time.Time

	// Title and Message are what a human channel (Slack, email, JSM) renders.
	Title   string
	Message string

	OrganizationID   int64
	OrganizationSlug string

	Incident  *EventIncident
	AlertRule *EventAlertRule
	Host      *EventHost
	Service   *EventServiceRef
	Labels    map[string]string

	// GroupedEvents are the other events of the same burst, when the channel
	// asked for grouping. They travel with the leading event so one message
	// still describes every incident it covers.
	GroupedEvents []NotificationEvent
}

// EventIncident is the incident an event is about.
type EventIncident struct {
	ID             int64
	Title          string
	Status         string
	PreviousStatus string
	Severity       string
	StartedAt      time.Time
	AcknowledgedAt *time.Time
	ResolvedAt     *time.Time
	URL            string
}

// EventAlertRule is the rule that fired, with the value that breached it.
type EventAlertRule struct {
	ID            int64
	Name          string
	Metric        string
	Aggregation   string
	Operator      string
	Threshold     float64
	ObservedValue *float64
	Duration      int
}

// EventHost is the machine an event is about.
type EventHost struct {
	ID      int64
	Name    string
	Group   string
	Service string
}

// EventServiceRef is the monitored service an event is about.
type EventServiceRef struct {
	ID   int64
	Name string
	Type string
}

// NewEventID mints the identifier a receiver uses to recognise a redelivery of
// the same event.
func NewEventID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "evt_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "evt_" + hex.EncodeToString(buf)
}

// stringEvent wraps a title/message pair for the channels that were never
// given anything richer, so one delivery path serves both.
func stringEvent(eventType, title, message, dedupKey string) NotificationEvent {
	return NotificationEvent{
		Event:      eventType,
		EventID:    NewEventID(),
		DedupKey:   dedupKey,
		OccurredAt: time.Now().UTC(),
		Title:      title,
		Message:    message,
	}
}

// IncidentEvent builds the event for one incident transition.
func IncidentEvent(eventType string, incident models.Incident, previousStatus, dedupKey, appURL string) NotificationEvent {
	event := NotificationEvent{
		Event:          eventType,
		EventID:        NewEventID(),
		DedupKey:       dedupKey,
		OccurredAt:     time.Now().UTC(),
		OrganizationID: incident.OrganizationID,
		Incident: &EventIncident{
			ID:             incident.ID,
			Title:          incident.Title,
			Status:         incident.Status,
			PreviousStatus: previousStatus,
			Severity:       incident.Severity,
			StartedAt:      incident.StartedAt,
			AcknowledgedAt: incident.AcknowledgedAt,
			ResolvedAt:     incident.ResolvedAt,
			URL:            appURL,
		},
	}
	if incident.ServiceID != nil {
		event.Service = &EventServiceRef{ID: *incident.ServiceID}
		if incident.Service != nil {
			event.Service.Name = *incident.Service
		}
	}
	if incident.HostID != nil {
		event.Host = &EventHost{ID: *incident.HostID}
	}
	return event
}

// structuredPayload renders the machine-readable body. Severity is an
// enumerated field, the transition is a type, and the incident carries its id:
// none of that survives the Slack shape.
func (e NotificationEvent) structuredPayload() map[string]interface{} {
	payload := map[string]interface{}{
		"version":     webhookPayloadVersion,
		"event":       e.Event,
		"event_id":    e.EventID,
		"occurred_at": e.OccurredAt.UTC().Format(time.RFC3339),
		"title":       e.Title,
		"message":     e.Message,
	}
	if e.DedupKey != "" {
		payload["dedup_key"] = e.DedupKey
	}
	if e.OrganizationSlug != "" {
		payload["organization"] = map[string]interface{}{"slug": e.OrganizationSlug}
	}

	if e.Incident != nil {
		incident := map[string]interface{}{
			"id":         e.Incident.ID,
			"title":      e.Incident.Title,
			"status":     e.Incident.Status,
			"severity":   e.Incident.Severity,
			"started_at": e.Incident.StartedAt.UTC().Format(time.RFC3339),
		}
		if e.Incident.PreviousStatus != "" {
			incident["previous_status"] = e.Incident.PreviousStatus
		}
		incident["acknowledged_at"] = formatTimePtr(e.Incident.AcknowledgedAt)
		incident["resolved_at"] = formatTimePtr(e.Incident.ResolvedAt)
		if e.Incident.URL != "" {
			incident["url"] = e.Incident.URL
		}
		payload["incident"] = incident
	}

	if e.AlertRule != nil {
		rule := map[string]interface{}{
			"id":        e.AlertRule.ID,
			"name":      e.AlertRule.Name,
			"metric":    e.AlertRule.Metric,
			"operator":  e.AlertRule.Operator,
			"threshold": e.AlertRule.Threshold,
			"duration":  e.AlertRule.Duration,
		}
		if e.AlertRule.Aggregation != "" {
			rule["aggregation"] = e.AlertRule.Aggregation
		}
		if e.AlertRule.ObservedValue != nil {
			rule["observed_value"] = *e.AlertRule.ObservedValue
		}
		payload["alert_rule"] = rule
	}

	if e.Host != nil {
		host := map[string]interface{}{"id": e.Host.ID}
		addIfSet(host, "name", e.Host.Name)
		addIfSet(host, "group", e.Host.Group)
		addIfSet(host, "service", e.Host.Service)
		payload["host"] = host
	}
	if e.Service != nil {
		service := map[string]interface{}{"id": e.Service.ID}
		addIfSet(service, "name", e.Service.Name)
		addIfSet(service, "type", e.Service.Type)
		payload["service"] = service
	}
	if len(e.Labels) > 0 {
		payload["labels"] = e.Labels
	}
	if len(e.GroupedEvents) > 0 {
		grouped := make([]map[string]interface{}, 0, len(e.GroupedEvents))
		for _, other := range e.GroupedEvents {
			grouped = append(grouped, other.structuredPayload())
		}
		payload["grouped"] = map[string]interface{}{
			"count":  len(e.GroupedEvents) + 1,
			"events": grouped,
		}
	}
	return payload
}

func formatTimePtr(value *time.Time) interface{} {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func addIfSet(target map[string]interface{}, key, value string) {
	if value != "" {
		target[key] = value
	}
}
