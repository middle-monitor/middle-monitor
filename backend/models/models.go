package models

import (
	"encoding/json"
	"time"
)

// Organization represents an organization/tenant
type Organization struct {
	ID        int64     `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Slug      string    `json:"slug" db:"slug"` // URL-friendly identifier
	Plan      string    `json:"plan" db:"plan"` // free | pro | custom (effective: a running trial reads as pro)
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// End of the free Pro trial. Set on self-serve signup, never on a paid plan:
	// its presence next to plan=pro is what tells the UI the Pro access is a trial.
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty" db:"trial_ends_at"`

	// False on instances that sell no plans: the UI then hides billing and trials.
	BillingEnabled bool `json:"billing_enabled"`

	// Per-org alert severity opt-in (whether to deliver notifications of each level).
	AlertWarningEnabled  bool `json:"alert_warning_enabled" db:"alert_warning_enabled"`
	AlertCriticalEnabled bool `json:"alert_critical_enabled" db:"alert_critical_enabled"`

	// When true, every member must enroll TOTP before accessing the dashboard.
	MFARequired bool `json:"mfa_required" db:"mfa_required"`

	// Per-org SMTP for email alert channels. The password is stored encrypted and
	// never serialized; SMTPConfigured tells the frontend whether a password is set.
	SMTPHost       string `json:"smtp_host" db:"smtp_host"`
	SMTPPort       string `json:"smtp_port" db:"smtp_port"`
	SMTPUser       string `json:"smtp_user" db:"smtp_user"`
	SMTPFrom       string `json:"smtp_from" db:"smtp_from"`
	SMTPConfigured bool   `json:"smtp_configured" db:"-"`
}

// MaintenanceWindow suppresses alerts for a target (a service or a whole host)
// during a planned time window — e.g. a migration where the service is expected
// to be down. No notifications are sent for the target while it is active.
type MaintenanceWindow struct {
	ID             int64     `json:"id" db:"id"`
	OrganizationID int64     `json:"organization_id" db:"organization_id"`
	Name           string    `json:"name" db:"name"`
	TargetType     string    `json:"target_type" db:"target_type"` // "service" | "host"
	TargetID       int64     `json:"target_id" db:"target_id"`
	StartsAt       time.Time `json:"starts_at" db:"starts_at"`
	EndsAt         time.Time `json:"ends_at" db:"ends_at"`
	CreatedBy      *int64    `json:"created_by,omitempty" db:"created_by"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	// API-only: resolved target name for display (never stored).
	TargetName string `json:"target_name,omitempty" db:"-"`
}

// User represents a user account
type User struct {
	ID             int64      `json:"id" db:"id"`
	OrganizationID int64      `json:"organization_id" db:"organization_id"`
	Email          string     `json:"email" db:"email"`
	PasswordHash   string     `json:"-" db:"password_hash"` // Never expose in JSON
	Name           string     `json:"name" db:"name"`
	Role           string     `json:"role" db:"role"` // read_only, read_write, admin
	EmailVerified  bool       `json:"email_verified" db:"email_verified"`
	TOTPEnabled    bool       `json:"totp_enabled" db:"totp_enabled"` // user has enrolled an authenticator app
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
	LastLoginAt    *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	// API-only: true while an invited user has not yet accepted (set their password).
	Pending bool `json:"pending,omitempty" db:"-"`
}

// Membership links a user identity to an organization with a role. One user can
// hold several memberships (one per organization).
type Membership struct {
	ID             int64     `json:"id" db:"id"`
	UserID         int64     `json:"user_id" db:"user_id"`
	OrganizationID int64     `json:"organization_id" db:"organization_id"`
	Role           string    `json:"role" db:"role"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// UserOrganization is an organization a user belongs to, with the user's role in
// it. Used to list a user's orgs (login selection / org switcher).
type UserOrganization struct {
	Organization
	Role string `json:"role"`
}

// UserWithOrg represents a user with their organization info
type UserWithOrg struct {
	User
	Organization Organization `json:"organization"`
}

// AuthTokens represents JWT tokens
type AuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}

// LoginRequest represents login credentials
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RegisterRequest represents registration data
type RegisterRequest struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	Name             string `json:"name"`
	OrganizationName string `json:"organization_name"`
	// OrganizationSlug is the user-chosen URL identifier. When empty, a slug is
	// derived from OrganizationName (legacy behaviour).
	OrganizationSlug string `json:"organization_slug"`
}

// InviteUserRequest represents user invitation data
type InviteUserRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"` // admin, member
}

// ApplicationError represents a minimal application error
type ApplicationError struct {
	ID             int64     `json:"id" db:"id"`
	OrganizationID int64     `json:"organization_id" db:"organization_id"`
	Name           string    `json:"name" db:"name"`
	Message        string    `json:"message" db:"message"`
	File           string    `json:"file" db:"file"`
	Line           int       `json:"line" db:"line"`
	Timestamp      time.Time `json:"timestamp" db:"timestamp"`
	Service        string    `json:"service" db:"service"`
	// HTTP request information (optional, only for HTTP errors)
	HTTPMethod  *string `json:"http_method,omitempty" db:"http_method"`
	HTTPURL     *string `json:"http_url,omitempty" db:"http_url"`
	HTTPHeaders *string `json:"http_headers,omitempty" db:"http_headers"` // JSON encoded
	HTTPBody    *string `json:"http_body,omitempty" db:"http_body"`
	// TraceID links the error to a distributed trace (OpenTelemetry). When set,
	// the root-cause engine can pull the failing span chain from OpenSearch.
	TraceID *string `json:"trace_id,omitempty" db:"trace_id"`
	// Fingerprint is a stable hash of the normalized (name, message, file) used
	// to group occurrences of the same error and detect recurrence/regressions.
	Fingerprint string `json:"fingerprint,omitempty" db:"fingerprint"`
}

// ErrorGroup is a grouped set of identical/similar errors for Sentry-style list (events count + sparkline).
type ErrorGroup struct {
	EventCount int64             `json:"event_count"`
	FirstSeen  time.Time         `json:"first_seen"`
	LastSeen   time.Time         `json:"last_seen"`
	SampleID   int64             `json:"sample_id"`
	Sample     ApplicationError  `json:"sample"`
	Timeseries []TimeSeriesPoint `json:"timeseries"`
}

// TimeSeriesPoint is one bucket for the sparkline (date + count).
type TimeSeriesPoint struct {
	Date  string `json:"date"` // ISO date or hour
	Count int64  `json:"count"`
}

// InfraCorrelation represents an infrastructure issue correlated with an event
type InfraCorrelation struct {
	MetricName  string  `json:"metric_name"` // agent_cpu, agent_ram, agent_disk
	Value       float64 `json:"value"`
	Threshold   float64 `json:"threshold"`
	Description string  `json:"description"`
	// Confidence (0-1) reflects how strong this signal is (e.g. how far above
	// the threshold the metric went).
	Confidence float64 `json:"confidence"`
}

// ServiceCorrelation represents a service failure correlated with an event
type ServiceCorrelation struct {
	ServiceID   int64  `json:"service_id"`
	ServiceName string `json:"service_name"`
	Status      string `json:"status"` // e.g. "critical"
	Description string `json:"description"`
	// OccurredAt is when the neighbour failed. Neighbours are ordered by
	// antecedence (earliest first) so the first one is the prime suspect.
	OccurredAt *time.Time `json:"occurred_at,omitempty"`
	// PrecededIncident is true when this neighbour failed BEFORE the subject,
	// which makes it a likely upstream cause rather than a side effect.
	PrecededIncident bool    `json:"preceded_incident"`
	Confidence       float64 `json:"confidence"`
}

// DegradedNeighbour is a check in the same host group that slowed down during
// the incident window without ever failing. A noisy neighbour saturating a
// shared machine often leaves latency as its only visible trace.
type DegradedNeighbour struct {
	ServiceID   int64   `json:"service_id"`
	ServiceName string  `json:"service_name"`
	HostName    string  `json:"host_name,omitempty"`
	CheckType   string  `json:"check_type,omitempty"`
	LatencyMS   float64 `json:"latency_ms"`
	BaselineMS  float64 `json:"baseline_ms"`
	Multiplier  float64 `json:"multiplier"`
}

// AppCorrelation represents another application that also reported errors
// around the same time. Relation qualifies how it is tied to the subject:
// "dependency" (the subject links to it via an app->app correlation link — the
// strongest signal when it errored first), "dependent" (it links to the
// subject — downstream impact context), or "host_group" (mere co-occurrence in
// the same host group, context not proven cause).
type AppCorrelation struct {
	Service          string     `json:"service"`
	ErrorName        string     `json:"error_name"`
	Count            int        `json:"count"`
	OccurredAt       *time.Time `json:"occurred_at,omitempty"`
	PrecededIncident bool       `json:"preceded_incident"`
	Relation         string     `json:"relation,omitempty"` // "dependency" | "dependent" | "host_group"
	Confidence       float64    `json:"confidence,omitempty"`
}

// ErrorRecurrence describes how often the same error (by fingerprint) occurs,
// enabling "new vs known" and "regression after deploy" reasoning.
type ErrorRecurrence struct {
	Fingerprint   string     `json:"fingerprint"`
	CountLastHour int64      `json:"count_last_hour"`
	Count24h      int64      `json:"count_24h"`
	Count7d       int64      `json:"count_7d"`
	FirstSeen     *time.Time `json:"first_seen,omitempty"`
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	// IsNew: first observed very recently → likely a freshly introduced regression.
	IsNew bool `json:"is_new"`
	// IsRecurrent: seen many times over a long period → chronic/known issue.
	IsRecurrent bool   `json:"is_recurrent"`
	Description string `json:"description"`
}

// CorrelationResult represents the root cause analysis result
type CorrelationResult struct {
	HasCorrelation bool                 `json:"has_correlation"`
	HostID         *int64               `json:"host_id,omitempty"`
	HostName       *string              `json:"host_name,omitempty"`
	Infra          []InfraCorrelation   `json:"infra"`
	Services       []ServiceCorrelation `json:"services"`
	// Apps: other applications in the same host group that also errored in the window.
	Apps []AppCorrelation `json:"apps"`
	// Degraded: neighbours in the host group that slowed down without failing.
	Degraded        []DegradedNeighbour `json:"degraded,omitempty"`
	SemanticMatches []string            `json:"semantic_matches"`
	// Recurrence is populated for errors that carry a fingerprint.
	Recurrence *ErrorRecurrence `json:"recurrence,omitempty"`
	// Confidence (0-1) is the overall strength of the correlation evidence.
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

// SystemMetric represents system metrics
type SystemMetric struct {
	ID             int64     `json:"id" db:"id"`
	OrganizationID int64     `json:"organization_id" db:"organization_id"`
	Service        string    `json:"service" db:"service"`
	CPUPerc        float64   `json:"cpu_perc" db:"cpu_perc"`
	RAMPerc        float64   `json:"ram_perc" db:"ram_perc"`
	HTTPLatency    *float64  `json:"http_latency,omitempty" db:"http_latency"`
	Endpoint       *string   `json:"endpoint,omitempty" db:"endpoint"`
	Timestamp      time.Time `json:"timestamp" db:"timestamp"`
}

// Host represents a monitoring host that can have multiple services
type Host struct {
	ID             int64     `json:"id" db:"id"`
	OrganizationID int64     `json:"organization_id" db:"organization_id"`
	Name           string    `json:"name" db:"name"`                           // Technical identifier (agent, by-name lookup); not editable after creation
	DisplayName    *string   `json:"display_name,omitempty" db:"display_name"` // Optional label shown in UI; editable
	Host           string    `json:"host" db:"host"`
	Service        string    `json:"service" db:"service"`
	HostGroupID    *int64    `json:"host_group_id,omitempty" db:"host_group_id"` // Group used to scope correlation; defaults to the org's default group
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	Status         *string   `json:"status,omitempty"`   // Calculated: worst status of all services (success, failure)
	Services       []Service `json:"services,omitempty"` // Populated when fetching with services
}

// HostGroup groups hosts that share the same logical role (e.g. all the machines
// running one application). Correlation is scoped to a host and its group, never
// across the whole environment. Each organization has exactly one default group;
// new hosts join it unless explicitly reassigned.
type HostGroup struct {
	ID             int64     `json:"id" db:"id"`
	OrganizationID int64     `json:"organization_id" db:"organization_id"`
	Name           string    `json:"name" db:"name"`
	DisplayName    *string   `json:"display_name,omitempty" db:"display_name"`
	IsDefault      bool      `json:"is_default" db:"is_default"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	HostCount      int       `json:"host_count,omitempty"` // Populated when listing
}

// HostWithStats represents a host with service counts
type HostWithStats struct {
	Host
	ServiceCount  int `json:"service_count"`
	HealthyCount  int `json:"healthy_count"`
	FailingCount  int `json:"failing_count"`
	WarningCount  int `json:"warning_count"`
	CriticalCount int `json:"critical_count"`
}

// Service represents a health service configuration
type Service struct {
	ID                   int64     `json:"id" db:"id"`
	OrganizationID       int64     `json:"organization_id" db:"organization_id"`
	HostID               *int64    `json:"host_id,omitempty" db:"host_id"` // Optional: if set, service belongs to a host
	Name                 string    `json:"name" db:"name"`
	DisplayName          *string   `json:"display_name,omitempty" db:"display_name"` // Optional label shown in UI; editable, name stays the identifier
	Type                 string    `json:"type" db:"type"`                           // http, snmp, certificate, error_service_*
	Host                 string    `json:"host" db:"host"`
	Path                 *string   `json:"path,omitempty" db:"path"`
	Credentials          *string   `json:"credentials,omitempty" db:"credentials"` // JSON encoded
	Service              string    `json:"service" db:"service"`
	ServiceInterval      int       `json:"service_interval" db:"service_interval"`                       // Interval in seconds
	MaxAttempts          int       `json:"max_attempts" db:"max_attempts"`                               // Attempts a failing check gets per cycle; sample window for agent metrics
	FailureThreshold     *float64  `json:"failure_threshold,omitempty" db:"failure_threshold"`           // legacy single threshold (kept for back-compat)
	WarningThreshold     *float64  `json:"warning_threshold,omitempty" db:"warning_threshold"`           // raises a "warning" alert when crossed
	CriticalThreshold    *float64  `json:"critical_threshold,omitempty" db:"critical_threshold"`         // raises a "critical" alert when crossed
	ExpectedStatusCode   *int      `json:"expected_status_code,omitempty" db:"expected_status_code"`     // HTTP: exact status code treated as success; nil = any 2xx (default)
	ExpectedBodyContains *string   `json:"expected_body_contains,omitempty" db:"expected_body_contains"` // HTTP: assertion value — substring, or "path=value" in json_path mode; nil/empty = not checked
	ExpectedBodyMode     *string   `json:"expected_body_mode,omitempty" db:"expected_body_mode"`         // HTTP body assertion mode: "contains" (default) or "json_path"
	Token                *string   `json:"token,omitempty" db:"token"`                                   // Token for error services (SDK authentication)
	Public               bool      `json:"public" db:"public"`                                           // Opt-in: the check appears on the public status page
	CreatedAt            time.Time `json:"created_at" db:"created_at"`

	// API-only (not stored in DB): HTTP auth metadata for clients; secrets never returned.
	HttpAuthConfigured *bool   `json:"http_auth_configured,omitempty" db:"-"`
	HttpAuthMode       *string `json:"http_auth_mode,omitempty" db:"-"`
	// API-only: on update, keep existing bearer/basic secrets when new values are omitted.
	PreserveHTTPSecrets *bool `json:"preserve_http_secrets,omitempty" db:"-"`
}

// ServiceResult represents the result of a service execution
// For regular services: uses status, latency, message
// For agent services: uses status, metric_type, metric_value, metadata
type ServiceResult struct {
	ID        int64     `json:"id" db:"id"`
	ServiceID int64     `json:"service_id" db:"service_id"`
	Status    string    `json:"status" db:"status"` // success, failure, timeout
	Latency   *float64  `json:"latency,omitempty" db:"latency"`
	Message   *string   `json:"message,omitempty" db:"message"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	// Agent metrics fields (NULL for non-agent services)
	MetricType  *string  `json:"metric_type,omitempty" db:"metric_type"`   // cpu, ram, disk
	MetricValue *float64 `json:"metric_value,omitempty" db:"metric_value"` // value for agent metrics
	Metadata    *string  `json:"metadata,omitempty" db:"metadata"`         // JSON metadata for agent metrics
}

// Event represents a technical event
type Event struct {
	ID             int64     `json:"id" db:"id"`
	OrganizationID int64     `json:"organization_id" db:"organization_id"`
	Type           string    `json:"type" db:"type"` // deploy, restart, crash, error_spike
	Service        string    `json:"service" db:"service"`
	Message        string    `json:"message" db:"message"`
	Metadata       *string   `json:"metadata,omitempty" db:"metadata"` // JSON encoded
	Timestamp      time.Time `json:"timestamp" db:"timestamp"`
}

// ErrorStats represents aggregated error statistics
type ErrorStats struct {
	Total     int64              `json:"total"`
	ByService map[string]int64   `json:"by_service"`
	ByError   map[string]int64   `json:"by_error"`
	Recent    []ApplicationError `json:"recent"`
}

// MetricStats represents aggregated metric statistics
type MetricStats struct {
	CPUAvg      float64   `json:"cpu_avg"`
	RAMAvg      float64   `json:"ram_avg"`
	HTTPLatency *float64  `json:"http_latency,omitempty"`
	LastUpdate  time.Time `json:"last_update"`
}

// HealthStatus represents overall health status
type HealthStatus struct {
	Status          string    `json:"status"` // healthy, degraded, down
	Services        int       `json:"services"`
	Errors24h       int64     `json:"errors_24h"`
	ServicesFailing int       `json:"services_failing"`
	LastUpdate      time.Time `json:"last_update"`
}

// AgentRegistration represents agent registration request
type AgentRegistration struct {
	Hostname string   `json:"hostname"`
	Name     string   `json:"name"`
	Service  string   `json:"service"`
	Metrics  []string `json:"metrics"`  // ["cpu", "ram", "disk"]
	OrgSlug  string   `json:"org_slug"` // Optional: org slug for multi-tenant. If empty, uses org 1.
}

// AgentRegistrationResponse represents agent registration response
type AgentRegistrationResponse struct {
	HostID     int64   `json:"host_id"`
	ServiceIDs []int64 `json:"service_ids"`
}

// AgentMetrics represents metrics sent by agent
type AgentMetrics struct {
	Hostname  string             `json:"hostname"`
	Service   string             `json:"service"`
	Metrics   map[string]float64 `json:"metrics"`            // {"cpu": 45.2, "ram": 67.8, "disk": 23.1}
	Metadata  map[string]float64 `json:"metadata,omitempty"` // {"ram_total_gb": 16.0, "disk_total_gb": 500.0}
	Timestamp time.Time          `json:"timestamp"`
}

// AgentMetricPoint represents a single agent metric point
type AgentMetricPoint struct {
	Value     float64            `json:"value"`
	Metadata  map[string]float64 `json:"metadata,omitempty"`
	Timestamp time.Time          `json:"timestamp"`
}

// NotificationChannel represents a configured alerting channel
type NotificationChannel struct {
	ID             int64                  `json:"id"`
	OrganizationID int64                  `json:"organization_id"`
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`   // email, slack, jsm, whatsapp
	Config         map[string]interface{} `json:"config"` // {"email": "...", "webhook_url": "..."}
	Enabled        bool                   `json:"enabled"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// AlertRule defines a threshold-based alerting rule
// MetricLabel is one label equality constraint on a custom metric series.
type MetricLabel struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type AlertRule struct {
	ID             int64   `json:"id"`
	OrganizationID int64   `json:"organization_id"`
	Name           string  `json:"name"`
	Description    *string `json:"description,omitempty"`
	Type           string  `json:"type"`        // "threshold"
	TargetType     string  `json:"target_type"` // "service", "host", "any"
	TargetID       *int64  `json:"target_id,omitempty"`
	Metric         string  `json:"metric"` // "cpu","ram","disk","latency","error_count","failure_rate"
	// Set to alert on a custom metric series instead of a built-in signal; the
	// evaluator then reads OpenSearch rather than service_results.
	CustomMetric *string       `json:"custom_metric,omitempty"`
	CustomLabels []MetricLabel `json:"custom_labels,omitempty"`
	Operator     string        `json:"operator"`  // "gt","lt","gte","lte"
	Threshold    float64       `json:"threshold"` // legacy single-level threshold (kept for back-compat)
	Duration     int           `json:"duration"`  // seconds — look-back / evaluation window
	Severity     string        `json:"severity"`  // legacy single-level severity ("warning","critical")

	// Policy fields (Datadog/Sentry-style): statistic over the window + two levels + recovery.
	Aggregation       string    `json:"aggregation"` // avg|min|max|sum|p50|p75|p90|p95|p99
	WarningThreshold  *float64  `json:"warning_threshold,omitempty"`
	CriticalThreshold *float64  `json:"critical_threshold,omitempty"`
	RecoveryThreshold *float64  `json:"recovery_threshold,omitempty"` // hysteresis: value must cross back past this to resolve
	Enabled           bool      `json:"enabled"`
	Channels          []int64   `json:"channels"` // notification channel IDs
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`

	// Routing fields: a rule routes a fired severity to its channels with tags.
	// Thresholds are on the service; these decide WHO gets notified.
	Tags           string `json:"tags"`            // comma-separated, forwarded to JSM
	NotifyWarning  bool   `json:"notify_warning"`  // route warning-severity alerts
	NotifyCritical bool   `json:"notify_critical"` // route critical-severity alerts
}

// CustomDashboard is a user-built dashboard scoped to an organization. Widgets
// (with their grid layout) are an opaque JSON blob owned by the frontend, so the
// backend stores and returns them verbatim without knowing their shape.
type CustomDashboard struct {
	ID             int64           `json:"id"`
	OrganizationID int64           `json:"organization_id"`
	Name           string          `json:"name"`
	Widgets        json.RawMessage `json:"widgets"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// Incident represents a triggered alert/incident
type Incident struct {
	ID             int64      `json:"id"`
	OrganizationID int64      `json:"organization_id"`
	AlertRuleID    *int64     `json:"alert_rule_id,omitempty"`
	ServiceID      *int64     `json:"service_id,omitempty"`
	HostID         *int64     `json:"host_id,omitempty"`
	Title          string     `json:"title"`
	Description    *string    `json:"description,omitempty"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"` // open, acknowledged, resolved
	Service        *string    `json:"service,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolutionNote *string    `json:"resolution_note,omitempty"` // optional post-mortem note set when resolving
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy *int64     `json:"acknowledged_by,omitempty"`
}

// APIKey represents an API key
type APIKey struct {
	ID             int64      `json:"id"`
	OrganizationID int64      `json:"organization_id"`
	Name           string     `json:"name"`
	KeyPrefix      string     `json:"key_prefix"`
	Scopes         string     `json:"scopes"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedBy      *int64     `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// APIKeyWithSecret is returned only on creation
type APIKeyWithSecret struct {
	APIKey
	Key string `json:"key"` // Full key, only shown once
}

// ProfileCapture represents a pprof profile upload (CPU, heap, goroutine, etc.)
type ProfileCapture struct {
	ID              int64     `json:"id" db:"id"`
	OrganizationID  int64     `json:"organization_id" db:"organization_id"`
	Service         string    `json:"service" db:"service"`
	ProfileType     string    `json:"profile_type" db:"profile_type"` // cpu, heap, goroutine
	DurationSeconds *int      `json:"duration_seconds,omitempty" db:"duration_seconds"`
	SizeBytes       int       `json:"size_bytes" db:"size_bytes"`
	MemoryMB        *float64  `json:"memory_mb,omitempty" db:"memory_mb"` // RAM at capture time (for time-series chart)
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	Data            []byte    `json:"-" db:"data"` // Excluded from JSON; use download endpoint
}

// ServiceWithResults represents a service with its results
type ServiceWithResults struct {
	Service
	Results  []ServiceResult `json:"results"`
	HostName *string         `json:"host_name,omitempty"` // Name of the parent host, if any
}

// ApplicationLink links an app (by service name) to a host, service, host
// group, or another application, for alert correlation. App targets are
// name-based (like the source side): TargetAppName holds the other app's
// service tag and TargetID stays 0, so an app never needs to be registered
// as an error-service row to be linked to.
type ApplicationLink struct {
	ID             int64  `json:"id" db:"id"`
	OrganizationID int64  `json:"organization_id" db:"organization_id"`
	AppServiceName string `json:"app_service_name" db:"app_service_name"`
	TargetType     string `json:"target_type" db:"target_type"`                   // "host", "service", "host_group" or "app"
	TargetID       int64  `json:"target_id" db:"target_id"`                       // hosts.id, host_groups.id, or services.id; 0 for "app"
	TargetAppName  string `json:"target_app_name,omitempty" db:"target_app_name"` // set for "app" targets
	TargetName     string `json:"target_name,omitempty"`                          // Populated when listing (host/service/app name)
}

// LinkSuggestion is a suggested application link derived from trace data (for UI Accept/Ignore).
type LinkSuggestion struct {
	TargetType string `json:"target_type"` // "host" or "service"
	TargetID   int64  `json:"target_id"`
	TargetName string `json:"target_name"`
	Reason     string `json:"reason"` // e.g. "Appears in traces as peer/destination"
}

// LinkSuggestionsResponse wraps the suggestion list with a code explaining an
// empty result, so the UI can tell "nothing to suggest" apart from "no data
// to suggest from" instead of showing the same blank state for both.
type LinkSuggestionsResponse struct {
	Suggestions []LinkSuggestion `json:"suggestions"`
	EmptyReason string           `json:"empty_reason,omitempty"` // "tracing_not_configured" | "no_trace_data"
}
