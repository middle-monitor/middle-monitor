package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// Field limits of the receiving APIs; past them the message is refused whole.
const (
	discordTitleMax       = 256
	discordDescriptionMax = 4096
	telegramTextMax       = 4096
)

var discordColors = map[string]int{toneResolved: 0x38a169, toneWarning: 0xd69e2e, toneCritical: 0xe53e3e}

// discordPayload renders one embed. Mentions are disabled: an error message
// quoting "@everyone" must not ping a whole server.
func discordPayload(event NotificationEvent) map[string]interface{} {
	return map[string]interface{}{
		"username":         "Middle Monitor",
		"allowed_mentions": map[string]interface{}{"parse": []string{}},
		"embeds": []map[string]interface{}{{
			"title":       truncate(event.Title, discordTitleMax),
			"description": truncate(event.Message, discordDescriptionMax),
			"color":       discordColors[eventTone(event)],
			"footer":      map[string]string{"text": "Middle Monitor"},
			"timestamp":   event.OccurredAt.UTC().Format(time.RFC3339),
		}},
	}
}

var teamsColors = map[string]string{toneResolved: "Good", toneWarning: "Warning", toneCritical: "Attention"}

// teamsPayload wraps an Adaptive Card in a message envelope, the shape both
// Teams Workflows webhooks and the older connectors accept.
func teamsPayload(event NotificationEvent) map[string]interface{} {
	return map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]interface{}{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body": []map[string]interface{}{
					{"type": "TextBlock", "text": event.Title, "weight": "Bolder", "size": "Medium", "color": teamsColors[eventTone(event)], "wrap": true},
					{"type": "TextBlock", "text": event.Message, "wrap": true},
					{"type": "TextBlock", "text": "Middle Monitor", "isSubtle": true, "size": "Small"},
				},
			},
		}},
	}
}

// telegramAPI is a variable so tests can point it at a local server.
var telegramAPI = "https://api.telegram.org"

// sendTelegram posts a plain-text message through a bot. Plain text on purpose:
// Markdown would need every "_" or "*" of an error message escaped.
func sendTelegram(channel models.NotificationChannel, event NotificationEvent) error {
	token, _ := channel.Config["bot_token"].(string)
	if token == "" {
		return ErrTelegramBotTokenMissing
	}
	chatID, _ := channel.Config["chat_id"].(string)
	if chatID == "" {
		return ErrTelegramChatIDMissing
	}

	body, _ := json.Marshal(map[string]interface{}{
		"chat_id":                  chatID,
		"text":                     truncate(strings.TrimSpace(event.Title+"\n\n"+event.Message), telegramTextMax),
		"disable_web_page_preview": true,
	})
	req, err := http.NewRequest("POST", telegramAPI+"/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return ErrTelegramUnreachable
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: webhookTimeout}).Do(req)
	if err != nil {
		// The request URL carries the bot token; keep only the cause.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("%w: %w", ErrTelegramUnreachable, urlErr.Err)
		}
		return ErrTelegramUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var answer struct {
			Description string `json:"description"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1500))
		if json.Unmarshal(raw, &answer) != nil || answer.Description == "" {
			answer.Description = strings.TrimSpace(string(raw))
		}
		return &TelegramStatusError{StatusCode: resp.StatusCode, Body: answer.Description}
	}
	return nil
}
