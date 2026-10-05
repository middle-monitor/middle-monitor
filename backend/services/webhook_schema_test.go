package services

import (
	"encoding/json"
	"os"
	"testing"
)

// webhookSchemaPath is the schema this API publishes at
// /api/v1/schemas/webhook-payload.json. It lives in this repository, next to the
// code that produces the payload, so this test can never be skipped away.
const webhookSchemaPath = "../api/schemas/webhook-payload.json"

// A receiver validates against the published schema. A field the platform sends
// but the schema does not declare makes a strict validator reject a payload that
// is in fact correct, and a field declared but never sent has a receiver waiting
// for something that never comes.
func TestPublishedSchemaMatchesTheStructuredPayload(t *testing.T) {
	raw, err := os.ReadFile(webhookSchemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", webhookSchemaPath, err)
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("the published schema is not valid JSON: %v", err)
	}

	event := sampleIncidentEvent()
	event.GroupedEvents = []NotificationEvent{sampleIncidentEvent()}
	payload := event.structuredPayload()

	for key := range payload {
		if _, declared := schema.Properties[key]; !declared {
			t.Fatalf("the payload carries %q, which %s does not declare", key, webhookSchemaPath)
		}
	}

	// Every documented top-level field must be reachable, or the schema promises
	// something the platform never sends.
	for key := range schema.Properties {
		if _, sent := payload[key]; !sent {
			t.Fatalf("%s declares %q, which a full event does not carry", webhookSchemaPath, key)
		}
	}
}

// The enumerated event types are the contract a receiver switches on.
func TestPublishedSchemaListsEveryEventType(t *testing.T) {
	raw, err := os.ReadFile(webhookSchemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", webhookSchemaPath, err)
	}

	var schema struct {
		Properties struct {
			Event struct {
				Enum []string `json:"enum"`
			} `json:"event"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode: %v", err)
	}

	declared := map[string]bool{}
	for _, value := range schema.Properties.Event.Enum {
		declared[value] = true
	}

	for _, eventType := range []string{
		EventIncidentOpened, EventIncidentAcknowledged, EventIncidentResolved,
		EventIncidentReopened, EventIncidentEscalated, EventErrorNew, EventTest, EventHeartbeat,
	} {
		if !declared[eventType] {
			t.Fatalf("the schema does not list the event type %q", eventType)
		}
	}
}
