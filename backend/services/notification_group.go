package services

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"middle-monitor/backend/models"
)

// groupSettings is the anti-burst configuration of one channel. When a host
// falls, its twenty services fall with it: without grouping that is twenty
// POSTs and twenty triggers on the receiver's side.
type groupSettings struct {
	GroupBy        []string
	GroupWait      time.Duration
	RepeatInterval time.Duration
}

// enabled reports whether the channel asked for grouping at all. Grouping makes
// delivery asynchronous, so it stays opt-in.
func (g groupSettings) enabled() bool {
	return len(g.GroupBy) > 0 && g.GroupWait > 0
}

func readGroupSettings(channel models.NotificationChannel) groupSettings {
	settings := groupSettings{}

	if raw, ok := channel.Config["group_by"].([]interface{}); ok {
		for _, item := range raw {
			if text, ok := item.(string); ok && text != "" {
				settings.GroupBy = append(settings.GroupBy, text)
			}
		}
	}
	settings.GroupWait = configSeconds(channel.Config, "group_wait")
	settings.RepeatInterval = configSeconds(channel.Config, "repeat_interval")
	return settings
}

// configSeconds reads a duration written as a number of seconds. JSON numbers
// arrive as float64 through the config map.
func configSeconds(config map[string]interface{}, key string) time.Duration {
	switch value := config[key].(type) {
	case float64:
		return time.Duration(value) * time.Second
	case int:
		return time.Duration(value) * time.Second
	default:
		return 0
	}
}

// groupKeyFor builds the identity of a burst: the same key means the same
// underlying problem, which is what makes one message out of twenty.
func groupKeyFor(event NotificationEvent, groupBy []string) string {
	parts := make([]string, 0, len(groupBy)+1)
	for _, field := range groupBy {
		switch field {
		case "host_id":
			if event.Host != nil {
				parts = append(parts, fmt.Sprintf("host:%d", event.Host.ID))
			}
		case "service_id":
			if event.Service != nil {
				parts = append(parts, fmt.Sprintf("service:%d", event.Service.ID))
			}
		case "alert_rule_id":
			if event.AlertRule != nil {
				parts = append(parts, fmt.Sprintf("rule:%d", event.AlertRule.ID))
			}
		case "severity":
			if event.Incident != nil {
				parts = append(parts, "severity:"+event.Incident.Severity)
			}
		case "event":
			parts = append(parts, "event:"+event.Event)
		}
	}
	// The event type always takes part: an opening and a resolution must never
	// be folded into one message.
	parts = append(parts, "type:"+event.Event)
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// pendingGroup is a burst being accumulated for one channel.
type pendingGroup struct {
	events []NotificationEvent
}

var (
	groupMu sync.Mutex
	// groupBuffer holds the bursts being accumulated, groupChannels the channel
	// each one is destined for, so a shutdown can flush them without the timer.
	groupBuffer   = map[string]*pendingGroup{}
	groupChannels = map[string]models.NotificationChannel{}
	lastSentAt    = map[string]time.Time{} // channel id + dedup key + event type
)

// suppressedByRepeat reports whether the same alert was already sent inside the
// repeat interval. A resolution is never suppressed: a receiver that misses it
// never closes what it opened.
func suppressedByRepeat(channel models.NotificationChannel, event NotificationEvent, interval time.Duration) bool {
	if interval <= 0 || event.DedupKey == "" {
		return false
	}
	if event.Event != EventIncidentOpened && event.Event != EventIncidentEscalated {
		return false
	}

	key := fmt.Sprintf("%d|%s|%s", channel.ID, event.DedupKey, event.Event)
	now := time.Now()

	groupMu.Lock()
	defer groupMu.Unlock()
	if last, seen := lastSentAt[key]; seen && now.Sub(last) < interval {
		return true
	}
	lastSentAt[key] = now
	return false
}

// bufferEvent adds an event to its group and starts the wait on the first one.
// It reports whether delivery was deferred.
func bufferEvent(channel models.NotificationChannel, event NotificationEvent, settings groupSettings) bool {
	key := fmt.Sprintf("%d|%s", channel.ID, groupKeyFor(event, settings.GroupBy))

	groupMu.Lock()
	defer groupMu.Unlock()

	if group, waiting := groupBuffer[key]; waiting {
		group.events = append(group.events, event)
		return true
	}

	groupBuffer[key] = &pendingGroup{events: []NotificationEvent{event}}
	groupChannels[key] = channel
	time.AfterFunc(settings.GroupWait, func() { flushGroup(channel, key) })
	return true
}

// flushGroup sends one message for everything that accumulated during the wait.
func flushGroup(channel models.NotificationChannel, key string) {
	groupMu.Lock()
	group, waiting := groupBuffer[key]
	delete(groupBuffer, key)
	delete(groupChannels, key)
	groupMu.Unlock()

	if !waiting || len(group.events) == 0 {
		return
	}
	if err := deliverEvent(channel, mergeEvents(group.events), nil); err != nil {
		logGroupError(channel, len(group.events), err)
	}
}

// mergeEvents folds a burst into a single event. The first one leads, the rest
// travel with it so the receiver still sees every incident.
func mergeEvents(events []NotificationEvent) NotificationEvent {
	merged := events[0]
	if len(events) == 1 {
		return merged
	}

	merged.GroupedEvents = events[1:]
	merged.Title = fmt.Sprintf("%s (+%d more)", events[0].Title, len(events)-1)

	var body strings.Builder
	body.WriteString(events[0].Message)
	for _, event := range events[1:] {
		body.WriteString("\n- ")
		body.WriteString(event.Title)
	}
	merged.Message = body.String()
	return merged
}

func logGroupError(channel models.NotificationChannel, count int, err error) {
	slog.Error("grouped notification send failed", "channel", channel.Name, "events", count, "error", err)
}

// HeartbeatInterval is how often a channel asked to be pinged, or zero when it
// did not ask.
func HeartbeatInterval(channel models.NotificationChannel) time.Duration {
	return configSeconds(channel.Config, "heartbeat_interval")
}
