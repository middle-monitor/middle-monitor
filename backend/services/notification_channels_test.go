package services

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"middle-monitor/backend/models"
)

// captureServer records every JSON body it receives and answers with status.
func captureServer(t *testing.T, status int) (*httptest.Server, *[]map[string]interface{}, *[]string) {
	t.Helper()
	var bodies []map[string]interface{}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		paths = append(paths, r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies, &paths
}

// Discord ignores a Slack payload (no "content", no "embeds") and answers 400:
// the channel must get its own shape, with the severity colour kept.
func TestDiscordChannelReceivesAnEmbed(t *testing.T) {
	srv, bodies, _ := captureServer(t, http.StatusNoContent)
	ch := models.NotificationChannel{Type: "discord", Config: map[string]interface{}{"webhook_url": srv.URL}}

	if err := SendNotification(ch, "[RESOLVED] api is back", "up again"); err != nil {
		t.Fatal(err)
	}
	embeds, _ := (*bodies)[0]["embeds"].([]interface{})
	if len(embeds) != 1 {
		t.Fatalf("got %v", (*bodies)[0])
	}
	embed := embeds[0].(map[string]interface{})
	if embed["title"] != "[RESOLVED] api is back" || embed["description"] != "up again" {
		t.Fatalf("got %v", embed)
	}
	if int(embed["color"].(float64)) != 0x38a169 {
		t.Fatalf("a resolution must be green, got %v", embed["color"])
	}
}

// An error message is attacker-influenced text (a request path, an exception);
// one containing "@everyone" must not ping a whole Discord server.
func TestDiscordPayloadDisablesMentions(t *testing.T) {
	payload := discordPayload(NotificationEvent{Title: "x", Message: "@everyone look"})
	mentions := payload["allowed_mentions"].(map[string]interface{})
	if parse := mentions["parse"].([]string); len(parse) != 0 {
		t.Fatalf("mentions are parsed: %v", parse)
	}
}

func TestTeamsChannelReceivesAnAdaptiveCard(t *testing.T) {
	srv, bodies, _ := captureServer(t, http.StatusAccepted)
	ch := models.NotificationChannel{Type: "teams", Config: map[string]interface{}{"webhook_url": srv.URL}}

	if err := SendNotification(ch, "[CRITICAL] Check failure: api", "timeout"); err != nil {
		t.Fatal(err)
	}
	attachments := (*bodies)[0]["attachments"].([]interface{})
	attachment := attachments[0].(map[string]interface{})
	if attachment["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Fatalf("got %v", attachment["contentType"])
	}
	card, _ := json.Marshal(attachment["content"])
	if !strings.Contains(string(card), "Check failure: api") || !strings.Contains(string(card), `"Attention"`) {
		t.Fatalf("got %s", card)
	}
}

func TestTelegramChannelSendsToTheBotAndChat(t *testing.T) {
	srv, bodies, paths := captureServer(t, http.StatusOK)
	defer func(prev string) { telegramAPI = prev }(telegramAPI)
	telegramAPI = srv.URL
	ch := models.NotificationChannel{Type: "telegram", Config: map[string]interface{}{"bot_token": "123:abc", "chat_id": "-100200"}}

	if err := SendNotification(ch, "[CRITICAL] Check failure: api", "timeout"); err != nil {
		t.Fatal(err)
	}
	if (*paths)[0] != "/bot123:abc/sendMessage" {
		t.Fatalf("path %q", (*paths)[0])
	}
	body := (*bodies)[0]
	if body["chat_id"] != "-100200" || body["text"] != "[CRITICAL] Check failure: api\n\ntimeout" {
		t.Fatalf("got %v", body)
	}
}

// Telegram's own reason ("chat not found") is what the channel test has to show.
func TestTelegramErrorCarriesTheAPIDescription(t *testing.T) {
	srv, _, _ := captureServer(t, http.StatusBadRequest)
	defer func(prev string) { telegramAPI = prev }(telegramAPI)
	telegramAPI = srv.URL
	ch := models.NotificationChannel{Type: "telegram", Config: map[string]interface{}{"bot_token": "123:abc", "chat_id": "1"}}

	err := SendNotification(ch, "t", "m")
	var statusErr *TelegramStatusError
	if !errors.As(err, &statusErr) || statusErr.Body != "Bad Request: chat not found" {
		t.Fatalf("got %v", err)
	}
}

// The bot token is in the request URL, and Go puts the URL in transport errors;
// the error is logged and shown in the channel test, so it must not carry it.
func TestTelegramTransportErrorDoesNotLeakTheBotToken(t *testing.T) {
	defer func(prev string) { telegramAPI = prev }(telegramAPI)
	telegramAPI = "http://127.0.0.1:1"
	ch := models.NotificationChannel{Type: "telegram", Config: map[string]interface{}{"bot_token": "123:secret-token", "chat_id": "1"}}

	err := SendNotification(ch, "t", "m")
	if !errors.Is(err, ErrTelegramUnreachable) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("the bot token leaked: %v", err)
	}
}

func TestTelegramRequiresTokenAndChat(t *testing.T) {
	if err := SendNotification(models.NotificationChannel{Type: "telegram", Config: map[string]interface{}{"chat_id": "1"}}, "t", "m"); !errors.Is(err, ErrTelegramBotTokenMissing) {
		t.Fatalf("got %v", err)
	}
	if err := SendNotification(models.NotificationChannel{Type: "telegram", Config: map[string]interface{}{"bot_token": "x"}}, "t", "m"); !errors.Is(err, ErrTelegramChatIDMissing) {
		t.Fatalf("got %v", err)
	}
}

func pagerDutyChannel(t *testing.T) (models.NotificationChannel, *[]map[string]interface{}) {
	t.Helper()
	srv, bodies, _ := captureServer(t, http.StatusAccepted)
	prev := pagerDutyEventsURL
	pagerDutyEventsURL = srv.URL
	t.Cleanup(func() { pagerDutyEventsURL = prev })
	return models.NotificationChannel{ID: 4, Type: "pagerduty", Config: map[string]interface{}{"routing_key": "R0UT1NG"}}, bodies
}

// The trigger and the resolve must share the dedup key, or PagerDuty keeps an
// alert open forever after the check recovered.
func TestPagerDutyResolvesTheAlertItTriggered(t *testing.T) {
	ch, bodies := pagerDutyChannel(t)

	if err := SendNotificationWithAlias(ch, "[CRITICAL] Check failure: api", "timeout", "mm-svc-7"); err != nil {
		t.Fatal(err)
	}
	if err := ResolveNotification(ch, "mm-svc-7", "[RESOLVED] Check failure: api", "back"); err != nil {
		t.Fatal(err)
	}

	trigger, resolve := (*bodies)[0], (*bodies)[1]
	if trigger["event_action"] != "trigger" || trigger["dedup_key"] != "mm-svc-7" || trigger["routing_key"] != "R0UT1NG" {
		t.Fatalf("trigger %v", trigger)
	}
	if severity := trigger["payload"].(map[string]interface{})["severity"]; severity != "critical" {
		t.Fatalf("severity %v", severity)
	}
	if resolve["event_action"] != "resolve" || resolve["dedup_key"] != "mm-svc-7" {
		t.Fatalf("resolve %v", resolve)
	}
}

// Acknowledging here must acknowledge there, so PagerDuty stops escalating.
func TestPagerDutyMirrorsAnAcknowledgement(t *testing.T) {
	ch, bodies := pagerDutyChannel(t)
	event := stringEvent(EventIncidentAcknowledged, "[ACKNOWLEDGED] Check failure: api", "on it", "mm-svc-7")

	if err := deliverEvent(ch, event, nil); err != nil {
		t.Fatal(err)
	}
	if got := (*bodies)[0]; got["event_action"] != "acknowledge" || got["dedup_key"] != "mm-svc-7" {
		t.Fatalf("got %v", got)
	}
}

// A test notification proves the integration works; left open it would page
// the on-call for nothing, so it is resolved right after.
func TestPagerDutyTestEventDoesNotLeaveAnOpenAlert(t *testing.T) {
	ch, bodies := pagerDutyChannel(t)

	if err := SendNotification(ch, "[TEST] Middle Monitor", "test"); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 2 || (*bodies)[0]["event_action"] != "trigger" || (*bodies)[1]["event_action"] != "resolve" {
		t.Fatalf("got %v", *bodies)
	}
	if (*bodies)[0]["dedup_key"] != (*bodies)[1]["dedup_key"] {
		t.Fatal("the resolve does not target the test alert")
	}
	if severity := (*bodies)[0]["payload"].(map[string]interface{})["severity"]; severity != "info" {
		t.Fatalf("a test must not page as critical, got %v", severity)
	}
}

func TestPagerDutyRequiresARoutingKey(t *testing.T) {
	ch := models.NotificationChannel{Type: "pagerduty", Config: map[string]interface{}{}}
	if err := SendNotification(ch, "t", "m"); !errors.Is(err, ErrPagerDutyRoutingKeyMissing) {
		t.Fatalf("got %v", err)
	}
}
