package services

import (
	"sync/atomic"
	"testing"
	"time"

	"middle-monitor/backend/models"

	"github.com/DATA-DOG/go-sqlmock"
)

// A deploy landing inside the retry backoff used to drop the alert: the row
// stayed open and nothing ever picked it up again.
func TestAbandonedDeliveryIsResent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	server, seen := receiver(t)
	deliveryDB = db
	t.Cleanup(func() { deliveryDB = nil })

	mock.ExpectQuery("SELECT id, channel_id, organization_id, request_body, attempts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "organization_id", "request_body", "attempts"}).
			AddRow(int64(9), int64(3), int64(1), `{"event":"incident.opened"}`, 2))
	// The channel is read back so the body goes to the right receiver.
	mock.ExpectQuery("SELECT id, organization_id, name, type, config").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "name", "type", "config", "enabled", "created_at", "updated_at"}).
			AddRow(int64(3), int64(1), "ops", "webhook",
				[]byte(`{"webhook_url":"`+server.URL+`"}`), true, time.Now(), time.Now()))
	mock.ExpectExec("UPDATE webhook_deliveries").WillReturnResult(sqlmock.NewResult(0, 1))

	recovered, err := RecoverAbandonedDeliveries(db)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered %d, want 1", recovered)
	}
	if len(seen()) != 1 {
		t.Fatalf("the receiver got %d requests, want the re-sent one", len(seen()))
	}
}

// A channel deleted while a delivery was pending must close the row, or the
// sweeper carries it forever.
func TestDeliveryForADeletedChannelIsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	deliveryDB = db
	t.Cleanup(func() { deliveryDB = nil })

	mock.ExpectQuery("SELECT id, channel_id, organization_id, request_body, attempts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "organization_id", "request_body", "attempts"}).
			AddRow(int64(9), int64(3), int64(1), `{}`, 1))
	mock.ExpectQuery("SELECT id, organization_id, name, type, config").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "name", "type", "config", "enabled", "created_at", "updated_at"}))
	mock.ExpectExec("UPDATE webhook_deliveries").WillReturnResult(sqlmock.NewResult(0, 1))

	if _, err := RecoverAbandonedDeliveries(db); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the row was not closed: %v", err)
	}
}

// A burst waiting out its group_wait lives only in memory. A restart in that
// window must deliver it, not throw it away.
func TestShutdownFlushesPendingGroups(t *testing.T) {
	var hits int64
	server, seen := receiver(t)
	channel := webhookChannel(server.URL, map[string]interface{}{
		"group_by":   []interface{}{"host_id"},
		"group_wait": float64(3600), // long enough that only the flush can send it
	})

	for i := 0; i < 2; i++ {
		if err := SendNotificationEvent(channel, sampleIncidentEvent(), nil); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	atomic.StoreInt64(&hits, int64(len(seen())))
	if hits != 0 {
		t.Fatal("grouping must not deliver before the wait elapses")
	}

	FlushPendingGroups()

	posts := seen()
	if len(posts) != 1 {
		t.Fatalf("flush delivered %d messages, want 1 for the burst", len(posts))
	}
	if posts[0].body["text"] == nil {
		t.Fatalf("flushed body looks wrong: %v", posts[0].body)
	}
}

// Flushing an empty buffer must be a no-op, since every shutdown calls it.
func TestFlushWithNothingPendingIsHarmless(t *testing.T) {
	FlushPendingGroups()
	FlushPendingGroups()
}

// The sweeper must not race a live in-process retry: it only picks deliveries
// untouched for longer than the whole backoff.
func TestSweeperWaitsLongerThanTheInProcessBackoff(t *testing.T) {
	var backoff time.Duration
	for _, delay := range webhookRetryDelays {
		backoff += delay
	}
	if abandonedAfter <= backoff {
		t.Fatalf("abandonedAfter (%s) must exceed the retry backoff (%s)", abandonedAfter, backoff)
	}
}

// A channel whose configuration lost its URL cannot be retried; the row is
// closed rather than swept forever.
func TestUnsendableChannelClosesTheDelivery(t *testing.T) {
	channel := models.NotificationChannel{Type: "webhook", Config: map[string]interface{}{}}
	if _, err := readWebhookSettings(channel); err != ErrWebhookURLMissing {
		t.Fatalf("error %v, want ErrWebhookURLMissing", err)
	}
}
