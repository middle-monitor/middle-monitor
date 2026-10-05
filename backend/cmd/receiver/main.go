package main

import (
	"log/slog"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"middle-monitor/backend/api"
	"middle-monitor/backend/internal/logging"
	"middle-monitor/backend/services"
)

func main() {
	// The .env load comes first so LOG_LEVEL can be set there too.
	envErr := godotenv.Load()
	logging.Configure()
	if envErr != nil {
		slog.Debug("no .env file, using environment variables")
	}

	db, err := api.InitDB()
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	services.InitOrgSMTPProvider(db)
	// This process sends webhooks too, so its deliveries belong in the same log.
	services.SetDeliveryStore(db)

	opensearch := api.InitializeOTLPServices(db)

	r := api.SetupReceiverRouter(db, opensearch)

	services.InitKafkaProducers()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	if err := api.RunServer("Receiver API", ":"+port, r); err != nil {
		slog.Error("receiver api failed", "error", err)
		os.Exit(1)
	}
}
