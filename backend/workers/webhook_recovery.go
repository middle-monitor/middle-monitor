package workers

import (
	"database/sql"
	"log/slog"
	"time"

	"middle-monitor/backend/services"
)

// StartWebhookRecovery re-sends the deliveries a stopped process left in flight.
// The in-process retry covers a receiver that blinks; this covers our own
// deploy landing inside the backoff window, which would otherwise drop the
// alert with no trace beyond a row that stays open forever.
func StartWebhookRecovery(db *sql.DB) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			<-ticker.C
			recovered, err := services.RecoverAbandonedDeliveries(db)
			if err != nil {
				slog.Error("webhook recovery sweep failed", "error", err)
				continue
			}
			if recovered > 0 {
				slog.Info("re-sent abandoned webhook deliveries", "count", recovered)
			}
		}
	}()
	slog.Info("webhook recovery started", "interval_minutes", 5)
}
