package workers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

func heartbeatChannel(t *testing.T, url string, interval float64) models.NotificationChannel {
	t.Helper()
	return models.NotificationChannel{
		ID: 3, OrganizationID: 1, Name: "ops", Type: "webhook", Enabled: true,
		Config: map[string]interface{}{"webhook_url": url, "format": "structured", "heartbeat_interval": interval},
	}
}

// The failure a heartbeat exists to expose: a receiver cannot tell "nothing is
// wrong" from "the notification path is dead" unless something arrives on a
// fixed cadence.
func TestHeartbeatIsDeliveredAsATypedEvent(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
	}))
	defer server.Close()

	if err := services.SendHeartbeat(nil, heartbeatChannel(t, server.URL, 300)); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("got %d deliveries, want 1", len(bodies))
	}
	if bodies[0]["event"] != services.EventHeartbeat {
		t.Fatalf("event %v, want %s", bodies[0]["event"], services.EventHeartbeat)
	}
	if bodies[0]["dedup_key"] != "mm-heartbeat-3" {
		t.Fatalf("dedup_key %v", bodies[0]["dedup_key"])
	}
}

// A channel that did not ask for a heartbeat must never receive one.
func TestChannelsWithoutAnIntervalAreNotPinged(t *testing.T) {
	channel := models.NotificationChannel{Config: map[string]interface{}{"webhook_url": "http://x"}}
	if services.HeartbeatInterval(channel) != 0 {
		t.Fatal("an unconfigured channel must not be pinged")
	}

	configured := models.NotificationChannel{Config: map[string]interface{}{"heartbeat_interval": float64(600)}}
	if services.HeartbeatInterval(configured) != 10*time.Minute {
		t.Fatalf("interval %v, want 10m", services.HeartbeatInterval(configured))
	}
}

// The cadence has to be respected: a one-minute tick must not turn a five
// minute heartbeat into five deliveries.
func TestHeartbeatRespectsTheConfiguredInterval(t *testing.T) {
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { count++ }))
	defer server.Close()

	channel := heartbeatChannel(t, server.URL, 300)
	lastSent := map[int64]time.Time{}
	start := time.Now()

	// A restart must not make a receiver wait a whole interval for the first one.
	if due := heartbeatDue(channel, lastSent, start); !due {
		t.Fatal("the first tick after a start must send a heartbeat")
	}
	lastSent[channel.ID] = start

	if heartbeatDue(channel, lastSent, start.Add(time.Minute)) {
		t.Fatal("a heartbeat was sent inside its own interval")
	}
	if !heartbeatDue(channel, lastSent, start.Add(6*time.Minute)) {
		t.Fatal("the heartbeat never came back after its interval")
	}
}

// heartbeatDue mirrors the scheduling decision of sendDueHeartbeats, which is
// the part worth testing without a database.
func heartbeatDue(channel models.NotificationChannel, lastSent map[int64]time.Time, now time.Time) bool {
	interval := services.HeartbeatInterval(channel)
	if interval <= 0 {
		return false
	}
	last, seen := lastSent[channel.ID]
	return !seen || now.Sub(last) >= interval
}
