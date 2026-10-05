package workers

import (
	"database/sql"
	"log/slog"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// heartbeatTick is how often channels are checked against their configured
// interval. It bounds the drift, not the interval itself.
const heartbeatTick = time.Minute

// StartWebhookHeartbeat sends a periodic heartbeat to the channels that ask for
// one. It answers the most unpleasant failure of any alerting system: silence
// that means "nothing is wrong" and silence that means "the notification path
// is dead" look exactly alike. A receiver that stops seeing heartbeats knows
// which one it is in.
func StartWebhookHeartbeat(db *sql.DB) {
	go func() {
		lastSent := map[int64]time.Time{}
		ticker := time.NewTicker(heartbeatTick)
		defer ticker.Stop()
		for {
			<-ticker.C
			sendDueHeartbeats(db, lastSent, time.Now())
		}
	}()
	slog.Info("webhook heartbeat started")
}

// sendDueHeartbeats is separated from the loop so it can be exercised without
// waiting on a ticker.
func sendDueHeartbeats(db *sql.DB, lastSent map[int64]time.Time, now time.Time) int {
	channels, err := loadHeartbeatChannels(db)
	if err != nil {
		slog.Error("failed to read heartbeat channels", "error", err)
		return 0
	}

	sent := 0
	for _, channel := range channels {
		interval := services.HeartbeatInterval(channel)
		if interval <= 0 {
			continue
		}
		// The first tick after a restart sends one: a receiver waiting on a
		// heartbeat should not have to wait a whole interval to hear from us.
		if last, seen := lastSent[channel.ID]; seen && now.Sub(last) < interval {
			continue
		}
		lastSent[channel.ID] = now

		if err := services.SendHeartbeat(db, channel); err != nil {
			slog.Error("heartbeat failed", "channel", channel.Name, "error", err)
			continue
		}
		sent++
	}
	return sent
}

// loadHeartbeatChannels reads every enabled webhook channel. Only webhooks can
// carry a heartbeat: mailing one every five minutes would be spam.
func loadHeartbeatChannels(db *sql.DB) ([]models.NotificationChannel, error) {
	rows, err := db.Query(
		`SELECT id, organization_id, name, type, config, enabled
		   FROM notification_channels
		  WHERE enabled = true AND type = 'webhook'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	channels := []models.NotificationChannel{}
	for rows.Next() {
		var channel models.NotificationChannel
		var configJSON []byte
		if err := rows.Scan(&channel.ID, &channel.OrganizationID, &channel.Name,
			&channel.Type, &configJSON, &channel.Enabled); err != nil {
			return nil, err
		}
		channel.Config = services.DecodeChannelConfig(configJSON)
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}
