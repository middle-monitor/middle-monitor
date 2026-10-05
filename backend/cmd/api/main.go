package main

import (
	"log/slog"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	middlemonitor "github.com/middle-monitor/sdk-go"

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

	// Self-monitoring: Middle-Monitor instruments itself with its own public Go
	// SDK, configured through the standard client env vars
	// (MIDDLE_MONITOR_API_URL / MIDDLE_MONITOR_SERVICE / MIDDLE_MONITOR_TOKEN).
	// Opt-in on the token so a dev boot never exports to the default endpoint.
	if os.Getenv("MIDDLE_MONITOR_TOKEN") != "" {
		if err := initSelfMonitoring(); err != nil {
			slog.Warn("self-monitoring sdk init failed, continuing without", "error", err)
		} else {
			startSelfProfiling()
		}
	}

	db, err := api.InitDB()
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := api.RunMigrations(db); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	services.InitOrgSMTPProvider(db)
	// Webhook deliveries are recorded so a failed one can be read back and
	// replayed instead of asking the customer to reproduce the alert.
	services.SetDeliveryStore(db)

	authService := services.NewAuthService(db)

	// OpenSearch is needed for Logs/Traces search, link suggestions, and
	// root-cause traffic analysis on the dashboard side.
	api.InitializeOTLPServices(db)

	r := api.SetupAPIRouter(db, authService)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// The SDK middleware traces every request and writes the request log the
	// Logs view aggregates per service. A no-op when the SDK is not initialized,
	// so the token opt-in above still governs whether anything is exported.
	if err := api.RunServer("Dashboard API", ":"+port, middlemonitor.HTTPMiddleware(r)); err != nil {
		slog.Error("dashboard api failed", "error", err)
		os.Exit(1)
	}
}

// initSelfMonitoring configures the SDK from env, then turns off the middleware's
// error reporting: respondError already submits every 5xx with the real cause,
// while the middleware would only see the sanitized response body and add a
// second, vaguer entry for the same failure.
func initSelfMonitoring() error {
	cfg, err := middlemonitor.ConfigFromEnv()
	if err != nil {
		return err
	}
	cfg.DisableHTTPErrorReporting = true
	return middlemonitor.Init(cfg)
}
