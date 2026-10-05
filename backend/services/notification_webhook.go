package services

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

const (
	// maxResponseBodyRecorded keeps the delivery log readable: a receiver
	// answering with a page of HTML must not fill the table.
	maxResponseBodyRecorded = 2048
	webhookTimeout          = 10 * time.Second
)

// webhookRetryDelays are the waits before each retry, roughly 15 minutes in
// total. A receiver that is redeploying comes back within that window; one that
// does not is down, and repeating forever would only pile up.
var webhookRetryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 8 * time.Minute}

// webhookSettings is the delivery configuration of one channel.
type webhookSettings struct {
	URL        string
	Secret     string
	Structured bool
	Headers    map[string]string
}

// readWebhookSettings reads the channel config. The payload format defaults to
// the Slack shape: existing receivers parse it and must keep working.
func readWebhookSettings(channel models.NotificationChannel) (webhookSettings, error) {
	url, _ := channel.Config["webhook_url"].(string)
	if url == "" {
		return webhookSettings{}, ErrWebhookURLMissing
	}

	settings := webhookSettings{URL: url, Headers: map[string]string{}}
	settings.Secret, _ = channel.Config["secret"].(string)

	if format, ok := channel.Config["format"].(string); ok {
		settings.Structured = strings.EqualFold(format, "structured")
	}

	// A receiver behind a gateway needs a header the platform does not know
	// about; without it the only way in is an unauthenticated endpoint.
	if raw, ok := channel.Config["headers"].(map[string]interface{}); ok {
		for key, value := range raw {
			if text, ok := value.(string); ok {
				settings.Headers[key] = text
			}
		}
	}
	return settings, nil
}

// slackPayload is the human-facing shape: a title, and an attachment whose
// colour carries the severity.
func slackPayload(event NotificationEvent) map[string]interface{} {
	title := event.Title
	color := "#e53e3e"
	switch {
	case event.Event == EventIncidentResolved || strings.HasPrefix(title, "[RESOLVED]"):
		color = "#38a169"
	case strings.Contains(strings.ToLower(title), "warning"):
		color = "#d69e2e"
	}

	return map[string]interface{}{
		"text": title,
		"attachments": []map[string]interface{}{
			{
				"color":  color,
				"text":   event.Message,
				"footer": "Middle Monitor",
				"ts":     fmt.Sprintf("%d", event.OccurredAt.Unix()),
			},
		},
	}
}

func webhookBody(settings webhookSettings, event NotificationEvent) ([]byte, error) {
	if settings.Structured {
		return json.Marshal(event.structuredPayload())
	}
	return json.Marshal(slackPayload(event))
}

// signWebhook signs the body twice on purpose. X-Middmonitor-Signature is the
// original body-only HMAC, kept so receivers written against it keep verifying.
// X-Middmonitor-Signature-256 covers "<timestamp>.<body>", which is what lets a
// receiver reject a replayed capture: without a timestamp in the signed base,
// resending a captured POST works forever.
func signWebhook(req *http.Request, secret string, body []byte, timestamp time.Time) {
	if secret == "" {
		return
	}
	stamp := strconv.FormatInt(timestamp.Unix(), 10)

	legacy := hmac.New(sha256.New, []byte(secret))
	legacy.Write(body)
	req.Header.Set("X-Middmonitor-Signature", "sha256="+hex.EncodeToString(legacy.Sum(nil)))

	current := hmac.New(sha256.New, []byte(secret))
	current.Write([]byte(stamp))
	current.Write([]byte("."))
	current.Write(body)
	req.Header.Set("X-Middmonitor-Timestamp", stamp)
	req.Header.Set("X-Middmonitor-Signature-256", "sha256="+hex.EncodeToString(current.Sum(nil)))
}

// webhookAttempt is the outcome of one POST.
type webhookAttempt struct {
	StatusCode int
	Response   string
	Err        error
}

// permanent reports whether retrying could ever help. A 4xx other than 429 is
// the receiver saying the request itself is wrong.
func (a webhookAttempt) permanent() bool {
	if a.Err != nil {
		return false
	}
	return a.StatusCode >= 400 && a.StatusCode < 500 && a.StatusCode != http.StatusTooManyRequests
}

func (a webhookAttempt) ok() bool {
	return a.Err == nil && a.StatusCode < 400
}

func postWebhook(settings webhookSettings, body []byte) webhookAttempt {
	req, err := http.NewRequest("POST", settings.URL, bytes.NewReader(body))
	if err != nil {
		return webhookAttempt{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range settings.Headers {
		req.Header.Set(key, value)
	}
	signWebhook(req, settings.Secret, body, time.Now().UTC())

	client := &http.Client{Timeout: webhookTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return webhookAttempt{Err: err}
	}
	defer resp.Body.Close()

	response, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyRecorded))
	return webhookAttempt{StatusCode: resp.StatusCode, Response: string(response)}
}

// sendWebhookEvent delivers one event. The first attempt is synchronous so the
// caller (and the channel test endpoint) learns the outcome; the retries run in
// the background with a growing backoff.
func sendWebhookEvent(channel models.NotificationChannel, event NotificationEvent) error {
	settings, err := readWebhookSettings(channel)
	if err != nil {
		return err
	}
	body, err := webhookBody(settings, event)
	if err != nil {
		return ErrWebhookMarshal
	}

	attempt := postWebhook(settings, body)
	deliveryID := recordDelivery(channel, event, settings.URL, body, attempt, 1)

	if attempt.ok() {
		return nil
	}
	if !attempt.permanent() {
		go retryWebhook(channel, settings, body, deliveryID)
	}
	return attemptError(attempt)
}

// retryWebhook keeps trying a failed delivery on a growing backoff, updating the
// same delivery row so the log shows one line per event rather than one per try.
func retryWebhook(channel models.NotificationChannel, settings webhookSettings, body []byte, deliveryID int64) {
	for i, delay := range webhookRetryDelays {
		time.Sleep(delay)
		attempt := postWebhook(settings, body)
		updateDelivery(deliveryID, attempt, i+2)
		if attempt.ok() {
			slog.Info("webhook delivery succeeded", "channel_id", channel.ID, "delivery_id", deliveryID, "attempt", i+2)
			return
		}
		if attempt.permanent() {
			return
		}
	}
	slog.Error("webhook delivery gave up", "channel_id", channel.ID, "delivery_id", deliveryID, "attempts", len(webhookRetryDelays)+1)
}

func attemptError(attempt webhookAttempt) error {
	if attempt.Err != nil {
		return attempt.Err
	}
	return &WebhookStatusError{StatusCode: attempt.StatusCode}
}

// DecodeChannelConfig reads the stored JSON config of a channel, always
// returning a usable map.
func DecodeChannelConfig(raw []byte) map[string]interface{} {
	config := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &config)
	}
	return config
}

// SendHeartbeat delivers the periodic "the notification path is alive" event.
// It bypasses grouping: folding a heartbeat into an alert burst would defeat
// the point of sending it on a fixed cadence.
func SendHeartbeat(db *sql.DB, channel models.NotificationChannel) error {
	event := NotificationEvent{
		Event:      EventHeartbeat,
		EventID:    NewEventID(),
		DedupKey:   fmt.Sprintf("mm-heartbeat-%d", channel.ID),
		OccurredAt: time.Now().UTC(),
		Title:      "Middle Monitor heartbeat",
		Message:    "The notification path for this channel is alive.",
	}
	if db != nil {
		event.OrganizationID = channel.OrganizationID
		event.OrganizationSlug = orgSlug(db, channel.OrganizationID)
	}
	return deliverEvent(channel, event, nil)
}
