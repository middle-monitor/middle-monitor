package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"

	"middle-monitor/backend/api"
	"middle-monitor/backend/internal/logging"
	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

func main() {
	// The .env load comes first so LOG_LEVEL can be set there too.
	envErr := godotenv.Load()
	logging.Configure()
	if envErr != nil {
		slog.Debug("no .env file, using environment variables")
	}

	db, err := initDB()
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := runMigrations(db); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	r := mux.NewRouter()
	r.Use(corsMiddleware)

	// Liveness/readiness for orchestrator probes.
	r.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}).Methods("GET")
	r.HandleFunc("/health", handleHealth).Methods("GET") // legacy alias

	apiRouter := r.PathPrefix("/api/v1").Subrouter()

	// Auth checkout endpoint sees real money traffic. Limit per IP.
	checkoutLimiter := middleware.NewRateLimiter(2, 5)

	authService := services.NewAuthService(db)
	protected := apiRouter.PathPrefix("").Subrouter()
	protected.Use(middleware.AuthMiddleware(authService))
	protected.Use(checkoutLimiter.Middleware)

	protected.HandleFunc("/checkout", handleCreateCheckout(db)).Methods("POST")
	protected.HandleFunc("/subscriptions/{orgId}", handleGetSubscription(db)).Methods("GET")

	// Stripe webhook is public but signed; rate limit anyway.
	webhookLimiter := middleware.NewRateLimiter(20, 40)
	apiRouter.Handle("/webhook", webhookLimiter.Middleware(handleStripeWebhook(db))).Methods("POST")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}
	if err := api.RunServer("Payments service", ":"+port, r); err != nil {
		slog.Error("payments service failed", "error", err)
		os.Exit(1)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	allowAny := strings.EqualFold(os.Getenv("CORS_ALLOW_ANY"), "true")
	allowed := map[string]struct{}{}
	for _, o := range strings.Split(os.Getenv("FRONTEND_URL"), ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowed[o] = struct{}{}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowAny {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if _, ok := allowed[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
