package api

import (
	"database/sql"
	"net/http"
	"os"
	"strings"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"

	"github.com/gorilla/mux"
)

// SetupAPIRouter configures the router for the main Dashboard API
func SetupAPIRouter(db *sql.DB, authService *services.AuthService) *mux.Router {
	r := mux.NewRouter()
	r.Use(middleware.Recover)
	r.Use(corsMiddleware)

	// Liveness/readiness probes. Public (no auth, no rate limit) so that
	// orchestrators can probe even under load. Kept on the root path so they
	// match the conventions of every probe-aware platform.
	r.HandleFunc("/healthz", liveHandler).Methods("GET")
	r.HandleFunc("/readyz", readyHandler(db)).Methods("GET")

	api := r.PathPrefix("/api/v1").Subrouter()

	// Auth endpoints are the #1 brute-force target. Aggressive limit per IP:
	// 5 req/s sustained, burst 10. Tune via env if you front this with a CDN
	// that already filters credential-stuffing traffic.
	authLimiter := middleware.NewRateLimiter(5, 10)

	authRoutes := api.PathPrefix("/auth").Subrouter()
	authRoutes.Use(authLimiter.Middleware)
	authRoutes.HandleFunc("/register", handleRegister(authService)).Methods("POST")
	authRoutes.HandleFunc("/login", handleLogin(authService)).Methods("POST")
	// Second step of a 2FA login: exchange the challenge token + code for tokens.
	authRoutes.HandleFunc("/login/mfa", handleLoginMFA(authService)).Methods("POST")
	authRoutes.HandleFunc("/refresh", handleRefreshToken(authService)).Methods("POST")
	// Public: confirm email from the verification link (user may not be logged in here).
	authRoutes.HandleFunc("/verify-email", handleVerifyEmail(authService)).Methods("POST")
	// Public: an invited user sets their password to activate the account (not logged in yet).
	authRoutes.HandleFunc("/accept-invite", handleAcceptInvitation(authService)).Methods("POST")
	// Public: forgotten-password flow (request a reset link, then set a new password).
	authRoutes.HandleFunc("/forgot-password", handleForgotPassword(authService)).Methods("POST")
	authRoutes.HandleFunc("/reset-password", handleResetPassword(authService)).Methods("POST")

	api.HandleFunc("/webhooks/stripe", handleStripeWebhook(db)).Methods("POST")
	// Public contact form relays email — throttle per IP so it cannot be used
	// as a spam cannon. 1 req/s sustained, burst 5.
	contactLimiter := middleware.NewRateLimiter(1, 5)
	api.Handle("/contact", contactLimiter.Middleware(handleContactUs())).Methods("POST")

	// Public one-click opt-out from an outreach link. RFC 8058 POSTs come from the
	// provider (Gmail, Yahoo), so a whole campaign lands on a handful of IPs and
	// shares one bucket; a 429 loses the opt-out since one-click is never retried.
	// The HMAC already blocks mass suppression, so this only has to stop a flood.
	unsubLimiter := middleware.NewRateLimiter(20, 100)
	api.Handle("/unsubscribe", unsubLimiter.Middleware(handleUnsubscribe(db))).Methods("POST")

	// Public status page. Read-only and cached, but it is the one page people
	// hit hard during an outage, so it gets its own generous limit per IP.
	statusLimiter := middleware.NewRateLimiter(5, 20)
	api.Handle("/status", statusLimiter.Middleware(handlePublicStatus(db))).Methods("GET")

	// Public, and registered here on purpose: "protected" below is a
	// PathPrefix("") subrouter, so it matches every /api/v1 path and swallows
	// anything declared after it. A public route added lower in this function
	// answers 404 no matter how it is written.
	//
	// A machine-readable description of this API, so a client can be generated
	// rather than hand-written, and the JSON Schemas the documentation points at.
	api.HandleFunc("/openapi.json", handleOpenAPI()).Methods("GET")
	api.HandleFunc("/schemas/webhook-payload.json", handleWebhookPayloadSchema()).Methods("GET")
	api.HandleFunc("/schemas/agent-config.json", handleAgentConfigSchema()).Methods("GET")

	// Protected routes (JWT authentication required)
	protected := api.PathPrefix("").Subrouter()
	protected.Use(middleware.AuthMiddleware(authService))

	// Current user. Reachable by unverified users (the gate is only on org routes)
	// so the frontend can read email_verified and resend the verification email.
	protected.HandleFunc("/auth/me", handleGetMe(authService)).Methods("GET")
	protected.HandleFunc("/auth/resend-verification", handleResendVerification(authService)).Methods("POST")
	// Account deletion re-checks the password: a stolen session alone must not erase an account.
	protected.HandleFunc("/auth/me", handleDeleteAccount(db, authService, opensearch)).Methods("DELETE")
	// Switch to another org the user belongs to (mints a session for it). Sits
	// before the org gate so a multi-org user can always change their active org.
	protected.HandleFunc("/auth/switch-org", handleSwitchOrg(authService)).Methods("POST")
	// 2FA enrollment. Reachable while the MFA gate is active (it sits before the org
	// gate) so a user forced to enroll can actually complete setup.
	protected.HandleFunc("/auth/2fa/setup", handleSetup2FA(authService)).Methods("POST")
	protected.HandleFunc("/auth/2fa/verify", handleEnable2FA(authService)).Methods("POST")
	protected.HandleFunc("/auth/2fa/disable", handleDisable2FA(authService)).Methods("POST")

	// Personal API tokens: each user manages their own tokens (which authenticate as
	// them). Outside the org gate so they remain manageable from account settings.
	protected.HandleFunc("/auth/api-tokens", handleGetPersonalTokens(authService)).Methods("GET")
	// Minting is gated: a personal token authenticates as its owner with
	// MFAEnrollmentRequired unset, so a user still owing an authenticator could
	// otherwise mint one and reach the org data the gate is holding them back from.
	// Listing and deleting stay open — neither hands out new access.
	protected.Handle("/auth/api-tokens",
		middleware.RequireMFAEnrollment(handleCreatePersonalToken(authService))).Methods("POST")
	protected.HandleFunc("/auth/api-tokens/{id}", handleDeletePersonalToken(authService)).Methods("DELETE")

	// Cross-organization admin routes, gated to PLATFORM_ADMIN_EMAILS rather than
	// any org's own membership. Lets the instance owner see every org's plan and
	// override it by hand (comps, downgrades) without going through Stripe.
	//
	// It carries the same email and 2FA gates as the org routes, so a session
	// whose address is unverified, or whose owner has not enrolled an
	// authenticator their own org requires, is refused here too.
	//
	// Those two gates only bind session JWTs: a personal token acts as its
	// owner, so a platform admin's own mm_ token reaches these routes without
	// either check.
	platformAdmin := protected.PathPrefix("/platform-admin").Subrouter()
	platformAdmin.Use(middleware.RequirePlatformAdmin)
	platformAdmin.Use(middleware.RequireVerifiedEmail)
	platformAdmin.Use(middleware.RequireMFAEnrollment)
	platformAdmin.HandleFunc("/organizations", handleListPlatformOrganizations(db)).Methods("GET")
	platformAdmin.HandleFunc("/organizations/{id}/plan", handleSetPlatformOrganizationPlan(db)).Methods("PATCH")

	// Organization-scoped routes. Gated behind email verification AND 2FA enrollment:
	// dashboard users get 403 until they confirm their address and (if the org enforces
	// it) enroll an authenticator. API keys are exempt from both.
	org := protected.PathPrefix("/organizations/{org_slug}").Subrouter()
	org.Use(middleware.OrgAccessMiddleware)
	org.Use(middleware.RequireVerifiedEmail)
	org.Use(middleware.RequireMFAEnrollment)
	// Per-user permissions: read_only users may read (GET) but never mutate. This
	// gates every mutating method on org routes; admin-only routes layer AdminOnly
	// on top (admins always pass WriteAccess).
	org.Use(middleware.WriteAccess)
	// Opt-in per request: a caller that sends an Idempotency-Key gets the first
	// answer replayed instead of creating a second row on a retry.
	org.Use(middleware.Idempotency(db))

	// Organization management
	org.HandleFunc("", handleGetOrganization(db)).Methods("GET")
	org.HandleFunc("", handleUpdateOrganization(authService)).Methods("PUT")
	org.HandleFunc("/stats", handleGetOrganizationStats(db)).Methods("GET")

	// Admin-only org routes
	orgAdmin := org.PathPrefix("").Subrouter()
	orgAdmin.Use(middleware.AdminOnly)
	// Listing, inviting and removing users is admin-only.
	orgAdmin.HandleFunc("/users", handleGetOrganizationUsers(authService)).Methods("GET")
	orgAdmin.HandleFunc("/users", handleInviteUser(authService)).Methods("POST")
	orgAdmin.HandleFunc("/users/{id}", handleUpdateUserRole(authService)).Methods("PUT")
	orgAdmin.HandleFunc("/users/{id}", handleDeleteUser(authService)).Methods("DELETE")
	orgAdmin.HandleFunc("/checkout", handleCreateCheckoutSession()).Methods("POST")
	orgAdmin.HandleFunc("/checkout/custom", handleCreateCustomCheckoutSession()).Methods("POST")
	orgAdmin.HandleFunc("/subscription", handleGetSubscription(db)).Methods("GET")
	orgAdmin.HandleFunc("/subscription", handleUpdateSubscription(db)).Methods("PUT")
	orgAdmin.HandleFunc("/billing-portal", handleCreateBillingPortal(db)).Methods("POST")
	// Send a test email through the org's own SMTP config.
	orgAdmin.HandleFunc("/smtp/test", handleTestSMTP(db)).Methods("POST")
	orgAdmin.HandleFunc("", handleDeleteOrganization(db, authService, opensearch)).Methods("DELETE")

	// Errors
	org.HandleFunc("/errors", handleGetErrors(db)).Methods("GET")
	org.HandleFunc("/errors/{id}/correlation", handleGetErrorCorrelation(db, opensearch)).Methods("GET")
	org.HandleFunc("/errors/{id}/explain", handleExplainError(db)).Methods("POST")
	org.HandleFunc("/errors/services", handleGetErrorServices(db)).Methods("GET")
	// Registered after /errors/services so that literal path is not swallowed by {id}.
	org.HandleFunc("/errors/{id}", handleGetError(db)).Methods("GET")

	// Application links
	org.HandleFunc("/links", handleCreateApplicationLink(db)).Methods("POST")
	org.HandleFunc("/links", handleGetApplicationLinks(db)).Methods("GET")
	org.HandleFunc("/links/suggestions", handleGetLinkSuggestions(db, opensearch)).Methods("GET")
	org.HandleFunc("/links/{id}", handleDeleteApplicationLink(db)).Methods("DELETE")

	// Host groups (scope correlation to a host + its group)
	org.HandleFunc("/host-groups", handleGetHostGroups(db)).Methods("GET")
	org.HandleFunc("/host-groups", handleCreateHostGroup(db)).Methods("POST")
	org.HandleFunc("/host-groups/{id}", handleUpdateHostGroup(db)).Methods("PUT")
	org.HandleFunc("/host-groups/{id}", handleDeleteHostGroup(db)).Methods("DELETE")
	org.HandleFunc("/hosts/{id}/group", handleAssignHostGroup(db)).Methods("PUT")
	// The scrape fragment the agent on this host fetches for itself.
	org.HandleFunc("/hosts/{id}/agent-config", handleGetHostAgentConfig(db)).Methods("GET")
	org.HandleFunc("/hosts/{id}/agent-config", handleSetHostAgentConfig(db)).Methods("PUT")
	org.HandleFunc("/hosts/{id}/ingest-cost", handleGetHostIngestCost(db)).Methods("GET")

	// System metrics
	org.HandleFunc("/metrics", handleGetMetrics(db)).Methods("GET")

	// Hosts
	org.HandleFunc("/hosts", handleSubmitHost(db)).Methods("POST")
	org.HandleFunc("/hosts", handleGetHosts(db)).Methods("GET")
	// Registered before /hosts/{id} so "stats" is not captured as an id.
	org.HandleFunc("/hosts/stats", handleGetHostStats(db)).Methods("GET")
	org.HandleFunc("/hosts/{id}", handleGetHostDetail(db)).Methods("GET")
	org.HandleFunc("/hosts/{id}", handleUpdateHost(db)).Methods("PUT")
	org.HandleFunc("/hosts/{id}", handleDeleteHost(db)).Methods("DELETE")
	org.HandleFunc("/hosts/by-name/{name}/services", handleGetHostServicesByName(db)).Methods("GET")
	org.HandleFunc("/hosts/{id}/services", handleGetHostServices(db)).Methods("GET")

	// Services
	org.HandleFunc("/services", handleSubmitService(db)).Methods("POST")
	org.HandleFunc("/services", handleGetServices(db)).Methods("GET")
	// Registered before /services/{id} so "stats" is not captured as an id.
	org.HandleFunc("/services/stats", handleGetServiceStats(db)).Methods("GET")
	org.HandleFunc("/services/{id}", handleGetServiceDetail(db)).Methods("GET")
	org.HandleFunc("/services/{id}", handleUpdateService(db)).Methods("PUT")
	org.HandleFunc("/services/{id}", handleDeleteService(db)).Methods("DELETE")
	org.HandleFunc("/services/{id}/results", handleGetServiceResults(db)).Methods("GET")
	org.HandleFunc("/services/results/{resultId}/correlation", handleGetServiceResultCorrelation(db)).Methods("GET")
	org.HandleFunc("/services/results/{resultId}/explain", handleExplainServiceResult(db)).Methods("POST")
	org.HandleFunc("/services/{id}/agent-metrics", handleGetAgentMetrics(db)).Methods("GET")

	// Events
	org.HandleFunc("/events", handleSubmitEvent(db)).Methods("POST")
	org.HandleFunc("/events", handleGetEvents(db)).Methods("GET")

	// Dashboard
	org.HandleFunc("/dashboard/health", handleGetHealth(db)).Methods("GET")
	org.HandleFunc("/dashboard/errors", handleGetErrorStats(db)).Methods("GET")
	org.HandleFunc("/dashboard/metrics", handleGetMetricStats(db)).Methods("GET")
	org.HandleFunc("/dashboard/timeline", handleGetTimeline(db)).Methods("GET")

	// Custom Dashboards (user-built, persisted per organization)
	org.HandleFunc("/custom-dashboards", handleGetCustomDashboards(db)).Methods("GET")
	org.HandleFunc("/custom-dashboards", handleCreateCustomDashboard(db)).Methods("POST")
	org.HandleFunc("/custom-dashboards/{id}", handleUpdateCustomDashboard(db)).Methods("PUT")
	org.HandleFunc("/custom-dashboards/{id}", handleDeleteCustomDashboard(db)).Methods("DELETE")

	// Alert Rules
	org.HandleFunc("/alert-rules", handleGetAlertRules(db)).Methods("GET")
	org.HandleFunc("/alert-rules", handleCreateAlertRule(db)).Methods("POST")
	org.HandleFunc("/alert-rules/{id}", handleUpdateAlertRule(db)).Methods("PUT")
	org.HandleFunc("/alert-rules/{id}", handleDeleteAlertRule(db)).Methods("DELETE")
	org.HandleFunc("/alert-rules/{id}/toggle", handleToggleAlertRule(db)).Methods("PATCH")

	// Notification Channels
	org.HandleFunc("/notification-channels", handleGetNotificationChannels(db)).Methods("GET")
	org.HandleFunc("/notification-channels", handleCreateNotificationChannel(db)).Methods("POST")
	org.HandleFunc("/notification-channels/{id}", handleUpdateNotificationChannel(db)).Methods("PUT")
	org.HandleFunc("/notification-channels/{id}", handleDeleteNotificationChannel(db)).Methods("DELETE")
	org.HandleFunc("/notification-channels/{id}/test", handleTestNotificationChannel(db)).Methods("POST")
	org.HandleFunc("/notification-channels/{id}/deliveries", handleListDeliveries(db)).Methods("GET")
	org.HandleFunc("/notification-channels/{id}/deliveries/{deliveryID}/replay", handleReplayDelivery(db)).Methods("POST")

	// Incidents
	org.HandleFunc("/incidents", handleGetIncidents(db)).Methods("GET")
	org.HandleFunc("/incidents/stats", handleGetIncidentStats(db)).Methods("GET")
	org.HandleFunc("/incidents", handleCreateIncident(db)).Methods("POST")
	org.HandleFunc("/incidents/{id}/status", handleUpdateIncidentStatus(db)).Methods("PUT")
	org.HandleFunc("/incidents/dedup/{key}/status", handleUpdateIncidentStatusByDedupKey(db)).Methods("PUT")

	// Maintenance / downtime windows
	org.HandleFunc("/maintenance-windows", handleGetMaintenanceWindows(db)).Methods("GET")
	org.HandleFunc("/maintenance-windows", handleCreateMaintenanceWindow(db)).Methods("POST")
	org.HandleFunc("/maintenance-windows/{id}", handleDeleteMaintenanceWindow(db)).Methods("DELETE")

	// API Keys
	org.HandleFunc("/api-keys", handleGetAPIKeys(db)).Methods("GET")
	org.HandleFunc("/api-keys", handleCreateAPIKey(db)).Methods("POST")
	org.HandleFunc("/api-keys/{id}", handleDeleteAPIKey(db)).Methods("DELETE")

	// Install Tokens
	org.HandleFunc("/install-tokens", handleGetInstallTokens(db)).Methods("GET")
	org.HandleFunc("/install-tokens", handleCreateInstallToken(db)).Methods("POST")
	org.HandleFunc("/install-tokens/{id}", handleDeleteInstallToken(db)).Methods("DELETE")

	// Logs / Traces
	org.HandleFunc("/logs", handleSearchLogs()).Methods("GET")
	org.HandleFunc("/logs/field-values", handleLogFieldValues()).Methods("GET")
	org.HandleFunc("/traces", handleSearchTraces()).Methods("GET")

	// Network metrics
	org.HandleFunc("/network", handleGetNetworkMetrics(db)).Methods("GET")

	// Metrics explorer
	org.HandleFunc("/metrics/explorer", handleGetMetricsExplorer(db)).Methods("GET")

	// Metric series (custom metrics with labels)
	org.HandleFunc("/metrics/series/names", handleListMetricNames(db)).Methods("GET")
	org.HandleFunc("/metrics/series/label-keys", handleListMetricLabelKeys(db)).Methods("GET")
	org.HandleFunc("/metrics/series/label-values", handleListMetricLabelValues(db)).Methods("GET")
	org.HandleFunc("/metrics/series/query", handleQueryMetricSeries(db)).Methods("GET")
	org.HandleFunc("/metrics/series/expression", handleQueryMetricExpression(db)).Methods("POST")

	// Profiling (list & view)
	org.HandleFunc("/profiles", handleListProfiles(db)).Methods("GET")
	org.HandleFunc("/profiles/series", handleGetProfileSeries(db)).Methods("GET")
	org.HandleFunc("/profiles/{id}/download", handleDownloadProfile(db)).Methods("GET")
	org.HandleFunc("/profiles/{id}/flamegraph", handleGetProfileFlamegraph(db)).Methods("GET")

	return r
}

// SetupReceiverRouter configures the router for high-throughput SDK and Agent ingestion
func SetupReceiverRouter(db *sql.DB, opensearch *services.OpenSearchService) *mux.Router {
	r := mux.NewRouter()
	r.Use(middleware.Recover)
	r.Use(ingestCORSMiddleware)

	// Liveness/readiness for the receiver instance.
	r.HandleFunc("/healthz", liveHandler).Methods("GET")
	r.HandleFunc("/readyz", readyHandler(db)).Methods("GET")

	// Ingestion endpoints can receive bursts, but a single misbehaving client
	// shouldn't fill the queue. 100 req/s sustained, burst 200 per IP is
	// generous enough for a busy app and tight enough to reject loops.
	ingestLimiter := middleware.NewRateLimiter(100, 200)

	// OTLP endpoints - no auth required (token check happens deeper in)
	otlp := r.PathPrefix("/v1").Subrouter()
	otlp.Use(ingestLimiter.Middleware)
	otlp.HandleFunc("/traces", handleOTLPTraces).Methods("POST")
	otlp.HandleFunc("/logs", handleOTLPLogs).Methods("POST")
	otlp.HandleFunc("/metrics", handleOTLPMetrics).Methods("POST")

	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(ingestLimiter.Middleware)

	// Agent registration and SDK entrypoints
	api.HandleFunc("/errors", handleSubmitError(db, opensearch)).Methods("POST") // SDK
	api.HandleFunc("/metrics", handleSubmitMetrics(db)).Methods("POST")          // SDK
	api.HandleFunc("/agents/register", handleAgentRegister(db)).Methods("POST")  // Agent
	api.HandleFunc("/agents/metrics", handleAgentMetrics(db)).Methods("POST")    // Agent
	api.HandleFunc("/agents/download/install", handleAgentInstallScript(db)).Methods("GET")
	api.HandleFunc("/agents/download/update", handleAgentUpdateScript()).Methods("GET")
	api.HandleFunc("/agents/latest", handleAgentLatest()).Methods("GET")
	api.HandleFunc("/agents/config", handleAgentConfig(db)).Methods("GET")
	api.HandleFunc("/agents/download/{os}/{arch}", handleAgentDownload(db)).Methods("GET")
	api.HandleFunc("/agents/download/{os}/{arch}/sha256", handleAgentChecksum()).Methods("GET")
	api.HandleFunc("/agents/download/{version}/{os}/{arch}", handleAgentDownload(db)).Methods("GET")
	api.HandleFunc("/profiles", handleUploadProfile(db)).Methods("POST") // SDK upload

	// Health check
	api.HandleFunc("/health/sql", handleSQLHealthCheck(db)).Methods("GET")

	return r
}

// ingestCORSMiddleware allows any origin. Ingest is called from browsers on
// customer domains we cannot enumerate, so the FRONTEND_URL allowlist would
// block every report from a browser SDK. Safe here and only here: the receiver
// carries no cookie/ambient authority, so "*" grants a page nothing it could
// not already get with curl. Never reuse it on the dashboard API.
func ingestCORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		// Cache the preflight for a day: a browser SDK sends an Authorization
		// header, so every report is preflighted without this.
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// corsMiddleware echoes the request Origin back when it matches the comma-
// separated FRONTEND_URL list. It deliberately does NOT fall back to "*" since
// that combined with Authorization headers leaks bearer tokens to any site.
// Set CORS_ALLOW_ANY=true to revert to a permissive "*" (dev only).
func corsMiddleware(next http.Handler) http.Handler {
	allowAny := strings.EqualFold(os.Getenv("CORS_ALLOW_ANY"), "true")
	rawList := os.Getenv("FRONTEND_URL")
	allowed := map[string]struct{}{}
	for _, o := range strings.Split(rawList, ",") {
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		// Expose the pagination total so browser JS can read it on cross-origin reads.
		w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
