package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"middle-monitor/backend/models"
)

// pagerDutyEventsURL is a variable so tests can point it at a local server.
var pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

const (
	pagerDutySummaryMax  = 1024
	pagerDutyDedupKeyMax = 255
)

// sendPagerDuty triggers or resolves a PagerDuty alert through the Events API
// v2. The dedup key is the incident alias (mm-svc-<id>, mm-rule-<id>), so the
// resolve closes the alert the trigger opened and a re-fire folds into it.
func sendPagerDuty(channel models.NotificationChannel, event NotificationEvent) error {
	key, _ := channel.Config["routing_key"].(string)
	if key == "" {
		return ErrPagerDutyRoutingKeyMissing
	}

	if event.Event == EventTest {
		// A test must not leave an open incident paging the on-call: trigger, then resolve.
		event.DedupKey = fmt.Sprintf("mm-test-%d", channel.ID)
		if err := postPagerDuty(pagerDutyBody(key, "trigger", event)); err != nil {
			return err
		}
		return postPagerDuty(pagerDutyBody(key, "resolve", event))
	}

	action := "trigger"
	switch {
	case event.Event == EventIncidentAcknowledged:
		action = "acknowledge"
	case eventTone(event) == toneResolved:
		action = "resolve"
	}
	return postPagerDuty(pagerDutyBody(key, action, event))
}

func pagerDutyBody(routingKey, action string, event NotificationEvent) map[string]interface{} {
	dedupKey := event.DedupKey
	if dedupKey == "" {
		dedupKey = event.Title
	}
	body := map[string]interface{}{
		"routing_key":  routingKey,
		"event_action": action,
		"dedup_key":    truncate(dedupKey, pagerDutyDedupKeyMax),
	}
	if action == "trigger" {
		body["payload"] = map[string]interface{}{
			"summary":        truncate(event.Title, pagerDutySummaryMax),
			"source":         "middle-monitor",
			"severity":       pagerDutySeverity(event),
			"custom_details": map[string]string{"details": event.Message},
		}
	}
	return body
}

// pagerDutySeverity maps to PagerDuty's four levels, which drive its urgency rules.
func pagerDutySeverity(event NotificationEvent) string {
	if event.Incident != nil && (event.Incident.Severity == "critical" || event.Incident.Severity == "warning") {
		return event.Incident.Severity
	}
	switch {
	case event.Event == EventTest:
		return "info"
	case event.Event == EventErrorNew:
		return "error"
	case eventTone(event) == toneWarning:
		return "warning"
	default:
		return "critical"
	}
}

func postPagerDuty(payload map[string]interface{}) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", pagerDutyEventsURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: webhookTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// The Events API answers 202 Accepted.
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1500))
		return &PagerDutyStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	return nil
}
