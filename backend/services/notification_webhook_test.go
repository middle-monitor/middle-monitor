package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"middle-monitor/backend/models"
)

// captured is one request as the receiver saw it.
type captured struct {
	headers http.Header
	body    map[string]interface{}
	raw     string
}

// receiver records what is posted to it and answers with the given statuses in
// order, repeating the last one.
func receiver(t *testing.T, statuses ...int) (*httptest.Server, func() []captured) {
	t.Helper()
	var mu sync.Mutex
	var seen []captured

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		json.Unmarshal(raw, &body)

		mu.Lock()
		index := len(seen)
		seen = append(seen, captured{headers: r.Header.Clone(), body: body, raw: string(raw)})
		mu.Unlock()

		status := http.StatusOK
		if len(statuses) > 0 {
			if index < len(statuses) {
				status = statuses[index]
			} else {
				status = statuses[len(statuses)-1]
			}
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)

	return server, func() []captured {
		mu.Lock()
		defer mu.Unlock()
		out := make([]captured, len(seen))
		copy(out, seen)
		return out
	}
}

func webhookChannel(url string, config map[string]interface{}) models.NotificationChannel {
	full := map[string]interface{}{"webhook_url": url}
	for key, value := range config {
		full[key] = value
	}
	return models.NotificationChannel{ID: 7, OrganizationID: 1, Name: "ops", Type: "webhook", Config: full, Enabled: true}
}

func sampleIncidentEvent() NotificationEvent {
	observed := 2310.0
	started := time.Date(2026, 6, 13, 11, 55, 0, 0, time.UTC)
	return NotificationEvent{
		Event:            EventIncidentOpened,
		EventID:          "evt_test",
		DedupKey:         "mm-rule-5",
		OccurredAt:       started,
		Title:            "[critical] API latency critical",
		Message:          "latency p95 = 2310 above threshold",
		OrganizationSlug: "acme",
		Incident: &EventIncident{
			ID: 801, Title: "API latency critical on api-health", Status: "open",
			Severity: "critical", StartedAt: started,
			URL: "https://app.middlemonitor.io/acme/incidents/801",
		},
		AlertRule: &EventAlertRule{
			ID: 5, Name: "API latency critical", Metric: "latency", Aggregation: "p95",
			Operator: "gt", Threshold: 1500, ObservedValue: &observed, Duration: 300,
		},
		Host:    &EventHost{ID: 42, Name: "web-prod-01", Group: "frontends", Service: "api"},
		Service: &EventServiceRef{ID: 1337, Name: "api-health", Type: "http"},
		Labels:  map[string]string{"project": "demo"},
	}
}

// Existing receivers parse the Slack shape. Changing the default would break
// every integration written before the structured format existed.
func TestSlackFormatStaysTheDefault(t *testing.T) {
	server, seen := receiver(t)

	if err := sendWebhookEvent(webhookChannel(server.URL, nil), sampleIncidentEvent()); err != nil {
		t.Fatalf("send: %v", err)
	}

	posts := seen()
	if len(posts) != 1 {
		t.Fatalf("got %d posts, want 1", len(posts))
	}
	if posts[0].body["text"] != "[critical] API latency critical" {
		t.Fatalf("text %v", posts[0].body["text"])
	}
	if _, structured := posts[0].body["event"]; structured {
		t.Fatal("a channel without format: structured must keep receiving the Slack shape")
	}
}

// What a machine consumer needs and the Slack shape cannot carry: the incident
// id, the transition type, the severity as an enum, and the value that breached.
func TestStructuredPayloadCarriesWhatAMachineNeeds(t *testing.T) {
	server, seen := receiver(t)
	channel := webhookChannel(server.URL, map[string]interface{}{"format": "structured"})

	if err := sendWebhookEvent(channel, sampleIncidentEvent()); err != nil {
		t.Fatalf("send: %v", err)
	}

	body := seen()[0].body
	if body["version"] != "1" || body["event"] != EventIncidentOpened {
		t.Fatalf("envelope: %v", body)
	}
	if body["dedup_key"] != "mm-rule-5" {
		t.Fatalf("dedup_key %v", body["dedup_key"])
	}

	incident, ok := body["incident"].(map[string]interface{})
	if !ok {
		t.Fatalf("no incident block: %v", body)
	}
	if incident["id"].(float64) != 801 {
		t.Fatalf("incident id %v", incident["id"])
	}
	if incident["severity"] != "critical" || incident["status"] != "open" {
		t.Fatalf("incident %v", incident)
	}
	// An unacknowledged incident must say so explicitly rather than omit the
	// field: a consumer distinguishing null from absent would guess.
	if value, present := incident["acknowledged_at"]; !present || value != nil {
		t.Fatalf("acknowledged_at %v (present=%v)", value, present)
	}

	rule := body["alert_rule"].(map[string]interface{})
	if rule["observed_value"].(float64) != 2310 || rule["threshold"].(float64) != 1500 {
		t.Fatalf("alert_rule %v", rule)
	}
	if body["labels"].(map[string]interface{})["project"] != "demo" {
		t.Fatalf("labels %v", body["labels"])
	}
	if body["host"].(map[string]interface{})["name"] != "web-prod-01" {
		t.Fatalf("host %v", body["host"])
	}
	if body["organization"].(map[string]interface{})["slug"] != "acme" {
		t.Fatalf("organization %v", body["organization"])
	}
}

// A receiver behind a gateway needs its token, or the only way in is an
// unauthenticated endpoint exposed to the internet.
func TestCustomHeadersReachTheReceiver(t *testing.T) {
	server, seen := receiver(t)
	channel := webhookChannel(server.URL, map[string]interface{}{
		"headers": map[string]interface{}{"Authorization": "Bearer gateway", "X-Tenant": "acme"},
	})

	if err := sendWebhookEvent(channel, sampleIncidentEvent()); err != nil {
		t.Fatalf("send: %v", err)
	}

	headers := seen()[0].headers
	if headers.Get("Authorization") != "Bearer gateway" || headers.Get("X-Tenant") != "acme" {
		t.Fatalf("headers %v", headers)
	}
}

// The documented verification recipe has to work, and the timestamp has to be
// inside the signed base: without it, capturing a POST and resending it later
// works forever.
func TestSignatureCoversTheTimestamp(t *testing.T) {
	server, seen := receiver(t)
	channel := webhookChannel(server.URL, map[string]interface{}{"secret": "topsecret"})

	if err := sendWebhookEvent(channel, sampleIncidentEvent()); err != nil {
		t.Fatalf("send: %v", err)
	}

	post := seen()[0]
	stamp := post.headers.Get("X-Middmonitor-Timestamp")
	if stamp == "" {
		t.Fatal("no timestamp header")
	}
	if _, err := strconv.ParseInt(stamp, 10, 64); err != nil {
		t.Fatalf("timestamp %q is not a unix time", stamp)
	}

	mac := hmac.New(sha256.New, []byte("topsecret"))
	mac.Write([]byte(stamp + "." + post.raw))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := post.headers.Get("X-Middmonitor-Signature-256"); got != want {
		t.Fatalf("signature %q, want %q", got, want)
	}

	// The original body-only signature stays, so receivers written against it
	// keep verifying after this change.
	legacy := hmac.New(sha256.New, []byte("topsecret"))
	legacy.Write([]byte(post.raw))
	if got := post.headers.Get("X-Middmonitor-Signature"); got != "sha256="+hex.EncodeToString(legacy.Sum(nil)) {
		t.Fatalf("legacy signature %q", got)
	}
}

func TestNoSecretMeansNoSignature(t *testing.T) {
	server, seen := receiver(t)

	if err := sendWebhookEvent(webhookChannel(server.URL, nil), sampleIncidentEvent()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if seen()[0].headers.Get("X-Middmonitor-Signature") != "" {
		t.Fatal("signed without a configured secret")
	}
}

// A receiver that is redeploying answers 503 for a few seconds. Losing the
// alert because of that is the failure this retry exists to prevent.
func TestFailedDeliveryIsRetried(t *testing.T) {
	previous := webhookRetryDelays
	webhookRetryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	t.Cleanup(func() { webhookRetryDelays = previous })

	server, seen := receiver(t, http.StatusServiceUnavailable, http.StatusOK)
	err := sendWebhookEvent(webhookChannel(server.URL, nil), sampleIncidentEvent())
	if err == nil {
		t.Fatal("the first attempt failed, the caller must be told")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(seen()) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(seen()) < 2 {
		t.Fatalf("got %d attempts, want a retry", len(seen()))
	}
}

// A 400 says the request itself is wrong: retrying it three times only makes
// the same mistake three more times.
func TestPermanentFailureIsNotRetried(t *testing.T) {
	previous := webhookRetryDelays
	webhookRetryDelays = []time.Duration{10 * time.Millisecond}
	t.Cleanup(func() { webhookRetryDelays = previous })

	server, seen := receiver(t, http.StatusBadRequest)
	if err := sendWebhookEvent(webhookChannel(server.URL, nil), sampleIncidentEvent()); err == nil {
		t.Fatal("expected an error")
	}

	time.Sleep(60 * time.Millisecond)
	if len(seen()) != 1 {
		t.Fatalf("got %d attempts, want 1", len(seen()))
	}
}

// A 429 is the receiver asking to slow down, not to stop.
func TestRateLimitedDeliveryIsRetried(t *testing.T) {
	attempt := webhookAttempt{StatusCode: http.StatusTooManyRequests}
	if attempt.permanent() {
		t.Fatal("429 must be retried")
	}
}

// When a host falls its services fall with it: the receiver should get one
// message, not twenty.
func TestGroupedEventsAreDeliveredOnce(t *testing.T) {
	server, seen := receiver(t)
	channel := webhookChannel(server.URL, map[string]interface{}{
		"format":     "structured",
		"group_by":   []interface{}{"host_id"},
		"group_wait": float64(1),
	})

	for i := 0; i < 3; i++ {
		event := sampleIncidentEvent()
		event.Incident.ID = int64(800 + i)
		if err := SendNotificationEvent(channel, event, nil); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	if len(seen()) != 0 {
		t.Fatal("grouping must wait before sending")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(seen()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}

	posts := seen()
	if len(posts) != 1 {
		t.Fatalf("got %d posts, want 1 for the whole burst", len(posts))
	}
	grouped, ok := posts[0].body["grouped"].(map[string]interface{})
	if !ok {
		t.Fatalf("no grouped block: %v", posts[0].body)
	}
	if grouped["count"].(float64) != 3 {
		t.Fatalf("grouped count %v, want 3", grouped["count"])
	}
	if len(grouped["events"].([]interface{})) != 2 {
		t.Fatalf("grouped events %v", grouped["events"])
	}
}

// An opening and a resolution must never be folded together, whatever the
// grouping asks for.
func TestOpeningAndResolutionNeverShareAGroup(t *testing.T) {
	opened := sampleIncidentEvent()
	resolved := sampleIncidentEvent()
	resolved.Event = EventIncidentResolved

	groupBy := []string{"host_id"}
	if groupKeyFor(opened, groupBy) == groupKeyFor(resolved, groupBy) {
		t.Fatal("an opening and a resolution landed in the same group")
	}
}

// repeat_interval stops a flapping rule from re-notifying, but a resolution
// must always get through or the receiver never closes what it opened.
func TestRepeatIntervalNeverSuppressesAResolution(t *testing.T) {
	channel := webhookChannel("http://example.invalid", nil)
	opened := sampleIncidentEvent()

	if suppressedByRepeat(channel, opened, time.Hour) {
		t.Fatal("the first send must go through")
	}
	if !suppressedByRepeat(channel, opened, time.Hour) {
		t.Fatal("the repeat inside the interval must be suppressed")
	}

	resolved := sampleIncidentEvent()
	resolved.Event = EventIncidentResolved
	if suppressedByRepeat(channel, resolved, time.Hour) {
		t.Fatal("a resolution must never be suppressed")
	}
}

// The string callers that were never migrated must still land on the right
// event type, or a resolution would be published as an opening.
func TestEventTypeIsRecoveredFromTheTitle(t *testing.T) {
	cases := map[string]string{
		"[RESOLVED] API down":    EventIncidentResolved,
		"[TEST] Middle Monitor":  EventTest,
		"[critical] API latency": EventIncidentOpened,
	}
	for title, want := range cases {
		if got := eventTypeFromTitle(title); got != want {
			t.Fatalf("%q -> %q, want %q", title, got, want)
		}
	}
}

// A channel with no webhook_url is a configuration error, reported as one.
func TestMissingURLIsAConfigError(t *testing.T) {
	channel := models.NotificationChannel{Type: "webhook", Config: map[string]interface{}{}}
	if err := sendWebhookEvent(channel, sampleIncidentEvent()); err != ErrWebhookURLMissing {
		t.Fatalf("error %v, want ErrWebhookURLMissing", err)
	}
}

// The alias a caller passes is what a receiver gets back as dedup_key, so it can
// acknowledge without storing anything between two messages.
func TestAliasBecomesTheDedupKey(t *testing.T) {
	server, seen := receiver(t)
	channel := webhookChannel(server.URL, map[string]interface{}{"format": "structured"})

	if err := SendNotificationWithAlias(channel, "[critical] Rule", "body", "mm-rule-12"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if seen()[0].body["dedup_key"] != "mm-rule-12" {
		t.Fatalf("dedup_key %v", seen()[0].body["dedup_key"])
	}
	if !strings.HasPrefix(seen()[0].body["event_id"].(string), "evt_") {
		t.Fatalf("event_id %v", seen()[0].body["event_id"])
	}
}

// Acknowledgements exist for automated receivers. Mailing one to a human is a
// notification they never used to get and never asked for, so those events stay
// on the webhook channels.
func TestAcknowledgementsStayOnWebhookChannels(t *testing.T) {
	webhook := models.NotificationChannel{Type: "webhook"}
	email := models.NotificationChannel{Type: "email"}
	slack := models.NotificationChannel{Type: "slack"}

	if !channelWantsEvent(webhook, EventIncidentAcknowledged) {
		t.Fatal("a webhook receiver must learn about an acknowledgement")
	}
	for _, channel := range []models.NotificationChannel{email, slack} {
		if channelWantsEvent(channel, EventIncidentAcknowledged) {
			t.Fatalf("%s must not be notified of an acknowledgement", channel.Type)
		}
		if channelWantsEvent(channel, EventIncidentReopened) {
			t.Fatalf("%s must not be notified of a reopening", channel.Type)
		}
	}

	// The transitions humans have always been notified of must keep reaching them.
	for _, event := range []string{EventIncidentOpened, EventIncidentResolved, EventIncidentEscalated} {
		if !channelWantsEvent(email, event) {
			t.Fatalf("email lost %s", event)
		}
	}
}
