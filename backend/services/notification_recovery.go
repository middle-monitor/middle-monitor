package services

import (
	"database/sql"
	"log/slog"
	"time"

	"middle-monitor/backend/models"
)

const (
	// abandonedAfter is how long a delivery must sit untouched before the
	// sweeper treats it as orphaned. It is longer than the whole in-process
	// backoff, so a live retry is never raced by the sweeper.
	abandonedAfter = 20 * time.Minute
	// maxRecoveryAttempts stops a receiver that is simply down from being
	// retried forever.
	maxRecoveryAttempts = 8
	recoveryBatchSize   = 50
)

// pendingDelivery is one delivery nobody is retrying any more.
type pendingDelivery struct {
	id        int64
	channelID int64
	orgID     int64
	body      string
	attempts  int
}

// RecoverAbandonedDeliveries re-sends the deliveries left unfinished by a
// process that stopped mid-retry. Without it, a deploy during the backoff window
// silently drops the alert: the row stays open and nothing ever picks it up.
func RecoverAbandonedDeliveries(db *sql.DB) (int, error) {
	rows, err := db.Query(
		`SELECT id, channel_id, organization_id, request_body, attempts
		   FROM webhook_deliveries
		  WHERE completed_at IS NULL
		    AND attempts < $1
		    AND last_attempt_at < NOW() - $2::interval
		  ORDER BY last_attempt_at
		  LIMIT $3`,
		maxRecoveryAttempts, abandonedAfter.String(), recoveryBatchSize)
	if err != nil {
		return 0, err
	}

	pending := []pendingDelivery{}
	for rows.Next() {
		var d pendingDelivery
		if err := rows.Scan(&d.id, &d.channelID, &d.orgID, &d.body, &d.attempts); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	recovered := 0
	for _, delivery := range pending {
		if resendPending(db, delivery) {
			recovered++
		}
	}
	return recovered, nil
}

func resendPending(db *sql.DB, delivery pendingDelivery) bool {
	channels, err := NewAlertService(db).GetChannelsByIDs([]int64{delivery.channelID}, delivery.orgID)
	if err != nil || len(channels) == 0 {
		// The channel is gone: close the row rather than sweeping it forever.
		giveUp(db, delivery.id, delivery.attempts)
		return false
	}

	settings, err := readWebhookSettings(channels[0])
	if err != nil {
		giveUp(db, delivery.id, delivery.attempts)
		return false
	}

	attempt := postWebhook(settings, []byte(delivery.body))
	updateDelivery(delivery.id, attempt, delivery.attempts+1)
	if attempt.ok() {
		slog.Info("webhook delivery recovered", "delivery_id", delivery.id, "attempt", delivery.attempts+1)
		return true
	}
	if delivery.attempts+1 >= maxRecoveryAttempts {
		giveUp(db, delivery.id, delivery.attempts+1)
	}
	return false
}

// giveUp closes a delivery the platform will not try again, so the log shows a
// final state instead of a row that looks perpetually in flight.
func giveUp(db *sql.DB, deliveryID int64, attempts int) {
	if _, err := db.Exec(
		`UPDATE webhook_deliveries
		    SET completed_at = NOW(), attempts = $1, last_attempt_at = NOW(),
		        error = COALESCE(error, 'given up after the maximum number of attempts')
		  WHERE id = $2`, attempts, deliveryID); err != nil {
		slog.Error("failed to close webhook delivery", "delivery_id", deliveryID, "error", err)
	}
}

// FlushPendingGroups delivers every buffered burst immediately. A shutdown in
// the middle of a group_wait would otherwise throw the alerts away, since the
// buffer only lives in memory.
func FlushPendingGroups() {
	groupMu.Lock()
	pending := make(map[string]*pendingGroup, len(groupBuffer))
	channels := make(map[string]models.NotificationChannel, len(groupBuffer))
	for key, group := range groupBuffer {
		pending[key] = group
		channels[key] = groupChannels[key]
		delete(groupBuffer, key)
		delete(groupChannels, key)
	}
	groupMu.Unlock()

	for key, group := range pending {
		if len(group.events) == 0 {
			continue
		}
		if err := deliverEvent(channels[key], mergeEvents(group.events), nil); err != nil {
			logGroupError(channels[key], len(group.events), err)
		}
	}
}
