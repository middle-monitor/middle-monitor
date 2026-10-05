package services

import (
	"database/sql"
	"log/slog"
	"time"

	"middle-monitor/backend/models"
)

// deliveryDB is where webhook deliveries are recorded. It is set once at boot;
// a process that never sets it (a test, a worker built without it) still
// delivers webhooks, it simply keeps no log.
var deliveryDB *sql.DB

// SetDeliveryStore wires the delivery log to the database.
func SetDeliveryStore(db *sql.DB) {
	deliveryDB = db
}

// WebhookDelivery is one recorded delivery, as the API returns it.
type WebhookDelivery struct {
	ID             int64      `json:"id"`
	ChannelID      int64      `json:"channel_id"`
	EventID        string     `json:"event_id"`
	EventType      string     `json:"event_type"`
	DedupKey       *string    `json:"dedup_key,omitempty"`
	URL            string     `json:"url"`
	StatusCode     *int       `json:"status_code,omitempty"`
	Attempts       int        `json:"attempts"`
	ResponseBody   *string    `json:"response_body,omitempty"`
	Error          *string    `json:"error,omitempty"`
	Succeeded      bool       `json:"succeeded"`
	RequestBody    string     `json:"request_body"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	OrganizationID int64      `json:"-"`
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func nullString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

// recordDelivery writes the first attempt and returns the row id, or 0 when no
// store is wired.
func recordDelivery(channel models.NotificationChannel, event NotificationEvent, url string, body []byte, attempt webhookAttempt, attempts int) int64 {
	if deliveryDB == nil {
		return 0
	}

	var statusCode interface{}
	if attempt.Err == nil {
		statusCode = attempt.StatusCode
	}
	var errText interface{}
	if attempt.Err != nil {
		errText = truncate(attempt.Err.Error(), maxResponseBodyRecorded)
	}
	var completedAt interface{}
	if attempt.ok() || attempt.permanent() {
		completedAt = time.Now().UTC()
	}

	var id int64
	err := deliveryDB.QueryRow(
		`INSERT INTO webhook_deliveries
		   (organization_id, channel_id, event_id, event_type, dedup_key, url,
		    request_body, status_code, attempts, response_body, error, succeeded, completed_at,
		    last_attempt_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NOW()) RETURNING id`,
		channel.OrganizationID, channel.ID, event.EventID, event.Event, nullString(event.DedupKey), url,
		string(body), statusCode, attempts, nullString(truncate(attempt.Response, maxResponseBodyRecorded)),
		errText, attempt.ok(), completedAt,
	).Scan(&id)
	if err != nil {
		slog.Error("failed to record webhook delivery", "channel_id", channel.ID, "error", err)
		return 0
	}
	return id
}

// updateDelivery folds a retry into the row of the delivery it retries, so the
// log reads one line per event with an attempt count.
func updateDelivery(deliveryID int64, attempt webhookAttempt, attempts int) {
	if deliveryDB == nil || deliveryID == 0 {
		return
	}

	var statusCode interface{}
	if attempt.Err == nil {
		statusCode = attempt.StatusCode
	}
	var errText interface{}
	if attempt.Err != nil {
		errText = truncate(attempt.Err.Error(), maxResponseBodyRecorded)
	}
	var completedAt interface{}
	if attempt.ok() || attempt.permanent() {
		completedAt = time.Now().UTC()
	}

	_, err := deliveryDB.Exec(
		`UPDATE webhook_deliveries
		    SET status_code=$1, attempts=$2, response_body=$3, error=$4, succeeded=$5, completed_at=$6,
		        last_attempt_at=NOW()
		  WHERE id=$7`,
		statusCode, attempts, nullString(truncate(attempt.Response, maxResponseBodyRecorded)),
		errText, attempt.ok(), completedAt, deliveryID)
	if err != nil {
		slog.Error("failed to update webhook delivery", "delivery_id", deliveryID, "error", err)
	}
}

// ListDeliveries returns the recent deliveries of one channel, newest first.
func ListDeliveries(db *sql.DB, orgID, channelID int64, limit, offset int) ([]WebhookDelivery, int, error) {
	var total int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM webhook_deliveries WHERE organization_id=$1 AND channel_id=$2`,
		orgID, channelID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := db.Query(
		`SELECT id, channel_id, event_id, event_type, dedup_key, url, status_code, attempts,
		        response_body, error, succeeded, request_body, created_at, completed_at
		   FROM webhook_deliveries
		  WHERE organization_id=$1 AND channel_id=$2
		  ORDER BY created_at DESC, id DESC
		  LIMIT $3 OFFSET $4`, orgID, channelID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	deliveries := []WebhookDelivery{}
	for rows.Next() {
		var d WebhookDelivery
		var statusCode sql.NullInt64
		var dedupKey, responseBody, errText sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.ChannelID, &d.EventID, &d.EventType, &dedupKey, &d.URL,
			&statusCode, &d.Attempts, &responseBody, &errText, &d.Succeeded, &d.RequestBody,
			&d.CreatedAt, &completedAt); err != nil {
			return nil, 0, err
		}
		if statusCode.Valid {
			code := int(statusCode.Int64)
			d.StatusCode = &code
		}
		if dedupKey.Valid {
			d.DedupKey = &dedupKey.String
		}
		if responseBody.Valid {
			d.ResponseBody = &responseBody.String
		}
		if errText.Valid {
			d.Error = &errText.String
		}
		if completedAt.Valid {
			t := completedAt.Time
			d.CompletedAt = &t
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, total, rows.Err()
}

// ReplayDelivery re-sends the exact body that was sent the first time, and
// records the replay as a new delivery: turning an hour of integration
// debugging into two minutes is the whole point.
func ReplayDelivery(db *sql.DB, orgID, channelID, deliveryID int64) (*WebhookDelivery, error) {
	var body, eventID, eventType, url string
	var dedupKey sql.NullString
	err := db.QueryRow(
		`SELECT request_body, event_id, event_type, dedup_key, url
		   FROM webhook_deliveries
		  WHERE id=$1 AND organization_id=$2 AND channel_id=$3`,
		deliveryID, orgID, channelID).Scan(&body, &eventID, &eventType, &dedupKey, &url)
	if err == sql.ErrNoRows {
		return nil, ErrDeliveryNotFound
	}
	if err != nil {
		return nil, err
	}

	channels, err := NewAlertService(db).GetChannelsByIDs([]int64{channelID}, orgID)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, ErrChannelNotFound
	}
	channel := channels[0]
	settings, err := readWebhookSettings(channel)
	if err != nil {
		return nil, err
	}

	// The stored body is replayed byte for byte, but re-signed: the signature
	// covers a fresh timestamp, so a receiver rejecting replays still accepts it.
	attempt := postWebhook(settings, []byte(body))
	event := NotificationEvent{Event: eventType, EventID: eventID, OccurredAt: time.Now().UTC()}
	if dedupKey.Valid {
		event.DedupKey = dedupKey.String
	}
	newID := recordDelivery(channel, event, settings.URL, []byte(body), attempt, 1)

	replayed, total, err := ListDeliveries(db, orgID, channelID, 1, 0)
	if err != nil || total == 0 || len(replayed) == 0 {
		return nil, attemptError(attempt)
	}
	if newID != 0 && replayed[0].ID != newID {
		return nil, attemptError(attempt)
	}
	return &replayed[0], nil
}
