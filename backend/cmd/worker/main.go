package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"middle-monitor/backend/api"
	"middle-monitor/backend/internal/logging"
	"middle-monitor/backend/services"
	"middle-monitor/backend/workers"
)

func main() {
	// Load environment variables
	// The .env load comes first so LOG_LEVEL can be set there too.
	envErr := godotenv.Load()
	logging.Configure()
	if envErr != nil {
		slog.Debug("no .env file, using environment variables")
	}

	// Initialize database
	db, err := api.InitDB()
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	services.InitOrgSMTPProvider(db)
	// This process sends webhooks too, so its deliveries belong in the same log.
	services.SetDeliveryStore(db)

	// Initialize OTLP services for OpenSearch connection
	opensearch := api.InitializeOTLPServices(db)

	slog.Info("starting background workers")

	// Start workers
	workers.StartServiceWorker(db, opensearch)
	workers.StartAlertEvaluator(db, opensearch)
	workers.StartCleanupWorker(db, opensearch)
	workers.StartKafkaConsumers(db, opensearch)
	workers.StartWebhookRecovery(db)
	workers.StartWebhookHeartbeat(db)

	// Keep the process running until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("workers running")
	<-sigChan

	slog.Info("shutting down workers")
	// A burst accumulating in a group_wait only lives in memory: deliver it now
	// rather than losing the alerts to the restart.
	services.FlushPendingGroups()
}
