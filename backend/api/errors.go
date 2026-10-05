package api

import (
	"errors"
	"fmt"
)

var (
	ErrAgentPlatformInvalid = errors.New("invalid os or architecture")
	ErrAgentBinaryMissing   = errors.New("agent binary not found")
	ErrAgentBinaryRead      = errors.New("failed to read agent binary")
	ErrAgentVersionUnknown  = errors.New("unknown agent version")
	ErrAgentReleaseEmpty    = errors.New("no agent binary is available")

	ErrIncidentIDInvalid  = errors.New("invalid incident id")
	ErrIncidentNotFound   = errors.New("incident not found")
	ErrRequestBodyInvalid = errors.New("invalid request body")
	ErrDedupKeyUnknown    = errors.New("unknown dedup key")
	ErrChannelIDInvalid   = errors.New("invalid channel id")
	ErrDeliveryIDInvalid  = errors.New("invalid delivery id")
)

// Validation and lookup errors returned by the handlers. They are sentinels so a
// caller can match on them with errors.Is instead of comparing message text.
var (
	ErrAdminRequired              = errors.New("admin access required")
	ErrAgentFieldsMissing         = errors.New("missing required fields: hostname, service")
	ErrAlreadyMember              = errors.New("this user is already a member of the organization")
	ErrAuthCodeInvalid            = errors.New("invalid authentication code")
	ErrChannelNotFound            = errors.New("notification channel not found")
	ErrChannelTypeInvalid         = errors.New("channel type must be one of: email, slack, jsm, whatsapp, webhook")
	ErrContactFieldsMissing       = errors.New("missing required fields: name, email, message")
	ErrContentTypeNotProtobuf     = errors.New("content-type must be application/x-protobuf or application/protobuf")
	ErrCredentialsInvalid         = errors.New("invalid email or password")
	ErrEmailInvalid               = errors.New("invalid email format")
	ErrEmailTaken                 = errors.New("user with this email already exists")
	ErrErrorIDInvalid             = errors.New("invalid error ID")
	ErrErrorNotFound              = errors.New("error not found")
	ErrEventFieldsMissing         = errors.New("missing required fields: type, service, message")
	ErrExpectedStatusCodeInvalid  = errors.New("expected_status_code must be a 3-digit HTTP status code between 100 and 999")
	ErrExpirationInPast           = errors.New("expiration date must be in the future")
	ErrExpirationRequired         = errors.New("an expiration date is required")
	ErrFieldRequired              = errors.New("field is required")
	ErrHostGroupIDInvalid         = errors.New("invalid host group ID")
	ErrHostIDInvalid              = errors.New("invalid host ID")
	ErrHostNameRequired           = errors.New("host name required")
	ErrHostNotFound               = errors.New("host not found")
	ErrIDInvalid                  = errors.New("invalid ID")
	ErrInstallTokenInvalid        = errors.New("invalid or expired install token")
	ErrInstallTokenRequired       = errors.New("install token required: set X-Install-Token header")
	ErrInvitationLinkInvalid      = errors.New("invalid or expired invitation link")
	ErrKeyIDInvalid               = errors.New("invalid key ID")
	ErrLinkFieldsMissing          = errors.New("missing required fields: app_service_name, target_type, target_id")
	ErrLinkIDInvalid              = errors.New("invalid link ID")
	ErrMultipartFormInvalid       = errors.New("invalid multipart form")
	ErrNameRequired               = errors.New("name is required")
	ErrNotAMember                 = errors.New("you are not a member of this organization")
	ErrOTLPReceiverUnavailable    = errors.New("OTLP receiver not initialized (OpenSearch not available)")
	ErrOrganizationIDInvalid      = errors.New("invalid organization id")
	ErrOrganizationNotFound       = errors.New("organization not found")
	ErrOwnAccountDelete           = errors.New("cannot delete your own account")
	ErrOwnRoleChange              = errors.New("cannot change your own role")
	ErrPasswordWeak               = errors.New("password must be at least 8 characters with uppercase, lowercase, and number")
	ErrPlanInvalid                = errors.New("plan must be one of: free, pro, custom")
	ErrPlanNotPurchasable         = errors.New("only the pro plan can be purchased via checkout")
	ErrProfileEmpty               = errors.New("profile data is empty")
	ErrProfileFieldsMissing       = errors.New("missing required fields: name, message, file, service")
	ErrProfileFileMissing         = errors.New("missing or invalid 'profile' file")
	ErrProfileIDInvalid           = errors.New("invalid profile ID")
	ErrProfileNotFound            = errors.New("profile not found")
	ErrProfileParse               = errors.New("failed to parse profile data")
	ErrProfileRead                = errors.New("failed to read profile data")
	ErrProfileTypeInvalid         = errors.New("profile_type must be one of: cpu, heap, goroutine, allocs, block, mutex")
	ErrRecipientMissing           = errors.New("no recipient email")
	ErrRefreshTokenInvalid        = errors.New("invalid or expired refresh token")
	ErrRefreshTokenMissing        = errors.New("missing refresh token")
	ErrRequestBodyRead            = errors.New("failed to read request body")
	ErrContentEncodingUnsupported = errors.New("unsupported content-encoding, send gzip or no encoding")
	ErrOTLPEncode                 = errors.New("failed to encode trimmed otlp payload")
	// Unauthenticated OTLP used to be stored with no organization, unmetered.
	ErrIngestTokenRequired        = errors.New("a valid token is required: send Authorization: Bearer <service token, org API key or install token>")
	ErrOTLPPayloadTooLarge        = errors.New("otlp payload too large: at most 16 MiB on the wire and 64 MiB decompressed, export smaller batches")
	ErrResetLinkInvalid           = errors.New("invalid or expired reset link")
	ErrResultIDInvalid            = errors.New("invalid result ID")
	ErrRoleInvalid                = errors.New("invalid role")
	ErrServiceFieldMissing        = errors.New("missing required field: service")
	ErrServiceFieldsMissing       = errors.New("missing required fields: type, host, service")
	ErrServiceIDInvalid           = errors.New("invalid service ID")
	ErrServiceNotFound            = errors.New("service not found")
	ErrServiceParamMissing        = errors.New("missing query param: service")
	ErrServiceResultNotFound      = errors.New("service result not found")
	ErrServiceTypeInvalid         = errors.New("service type must be one of: http, ping, snmp, certificate, sql")
	ErrSessionExpired             = errors.New("session expired, please log in again")
	ErrStripePriceMissing         = errors.New("stripe price not configured: set STRIPE_PRICE_PRO")
	ErrStripeSubscriptionMissing  = errors.New("no active Stripe subscription found for this organization")
	ErrStripeWebhookSecretMissing = errors.New("stripe webhook not configured: set STRIPE_WEBHOOK_SECRET")
	ErrSubscriptionMissing        = errors.New("no active subscription")
	ErrSubscriptionUpdateMissing  = errors.New("no active subscription to update; subscribe first")
	ErrTargetIDRequired           = errors.New("target_id is required")
	ErrTargetRequired             = errors.New("service_id or host_id is required")
	ErrTargetTypeInvalid          = errors.New("target type must be one of: service, host")
	ErrTokenIDInvalid             = errors.New("invalid token ID")
	ErrTokenInvalid               = errors.New("invalid or unknown token")
	ErrTokenNotFound              = errors.New("token not found")
	ErrTokenOrCodeMissing         = errors.New("missing token or code")
	ErrTwoFactorSetupMissing      = errors.New("no pending two-factor setup; start setup first")
	ErrUnauthorized               = errors.New("unauthorized")
	ErrUnsubscribeFieldsMissing   = errors.New("missing required fields: email, token")
	ErrUnsubscribeNotConfigured   = errors.New("unsubscribe not configured")
	ErrUnsubscribeStore           = errors.New("failed to record unsubscribe")
	ErrUnsubscribeTokenInvalid    = errors.New("invalid unsubscribe token")
	ErrUserIDInvalid              = errors.New("invalid user ID")
	ErrUserNotFound               = errors.New("user not found")
	ErrVerificationLinkInvalid    = errors.New("invalid or expired verification link")
	ErrWebhookSignatureInvalid    = errors.New("invalid webhook signature")
	ErrWindowRangeInvalid         = errors.New("ends_at must be after starts_at")
)

// Typed errors carry the dynamic part of a message. They exist so a caller can
// match with errors.As on the kind of failure rather than on the text, which is
// impossible with an inline fmt.Errorf.

// FieldRequiredError names a missing field.
type FieldRequiredError struct{ Field string }

func (e *FieldRequiredError) Error() string { return fmt.Sprintf("%s is required", e.Field) }

// FieldTooLongError names a field and the limit it exceeded.
type FieldTooLongError struct {
	Field string
	Max   int
}

func (e *FieldTooLongError) Error() string {
	return fmt.Sprintf("%s must be %d characters or fewer", e.Field, e.Max)
}

// RangeError reports a numeric value outside its accepted range.
type RangeError struct {
	What     string
	Min, Max int
	Unit     string
}

func (e *RangeError) Error() string {
	if e.Unit != "" {
		return fmt.Sprintf("%s must be between %d and %d %s", e.What, e.Min, e.Max, e.Unit)
	}
	return fmt.Sprintf("%s must be between %d and %d", e.What, e.Min, e.Max)
}

// RecordNotFoundError names the row a lookup expected to find.
type RecordNotFoundError struct {
	Kind  string
	ID    int64
	OrgID int64
}

func (e *RecordNotFoundError) Error() string {
	return fmt.Sprintf("%s %d not found in org %d", e.Kind, e.ID, e.OrgID)
}

// SubjectTypeError reports a subject the explain pipeline cannot handle.
type SubjectTypeError struct{ SubjectType string }

func (e *SubjectTypeError) Error() string {
	return fmt.Sprintf("unsupported subject_type %q", e.SubjectType)
}

// ExplainModeError reports an EXPLAIN_MODE value that names no engine.
type ExplainModeError struct{ Mode string }

func (e *ExplainModeError) Error() string {
	return fmt.Sprintf("unknown EXPLAIN_MODE %q (expected rules or single_shot)", e.Mode)
}

// LLMStatusError reports a non-OK answer from a model provider.
type LLMStatusError struct {
	Provider   string
	StatusCode int
	Body       string
}

func (e *LLMStatusError) Error() string {
	return fmt.Sprintf("%s returned %d: %s", e.Provider, e.StatusCode, e.Body)
}

// LLMProviderError reports an error the provider itself described.
type LLMProviderError struct {
	Provider string
	Message  string
}

func (e *LLMProviderError) Error() string {
	return fmt.Sprintf("%s error: %s", e.Provider, e.Message)
}

// LLMDecodeError reports an answer that did not parse.
type LLMDecodeError struct {
	Provider string
	Body     string
	Err      error
}

func (e *LLMDecodeError) Error() string {
	return fmt.Sprintf("invalid %s response: %v (body: %s)", e.Provider, e.Err, e.Body)
}

func (e *LLMDecodeError) Unwrap() error { return e.Err }

// LLMEmptyError reports an answer that parsed but carried no explanation.
type LLMEmptyError struct {
	Provider string
	Detail   string
}

func (e *LLMEmptyError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s produced an empty explanation", e.Provider)
	}
	return fmt.Sprintf("%s produced an empty explanation (%s)", e.Provider, e.Detail)
}

// MigrationDirtyError reports a migration that failed midway, leaving
// schema_migrations flagged so every later boot refuses to run.
type MigrationDirtyError struct {
	Version int
	Hint    string
	Err     error
}

func (e *MigrationDirtyError) Error() string {
	return fmt.Sprintf("migration to version %d failed and left the schema dirty; %s: %v", e.Version, e.Hint, e.Err)
}

func (e *MigrationDirtyError) Unwrap() error { return e.Err }

var (
	ErrStripeNotConfigured = errors.New("stripe not configured")
	ErrRulesReportEmpty    = errors.New("rules engine produced an empty report")
	ErrSchemaMissing       = errors.New("schema not available on this deployment")

	ErrAgentConfigTooLarge  = errors.New("agent scrape config is too large")
	ErrAgentConfigInvalid   = errors.New("agent scrape config must be a list of scrape targets, or a targets block")
	ErrAgentConfigTargetURL = errors.New("every scrape target needs a url")
	ErrSeedMembership       = errors.New("failed to add seeded admin to its organization")
)

// Wrapped around the underlying cause, which callers reach with errors.Is/As.
var (
	ErrAdminOrgPlanSeed            = errors.New("failed to seed admin org plan")
	ErrDefaultAdminSeed            = errors.New("failed to seed default admin")
	ErrEmbeddedMigrationsLoad      = errors.New("failed to load embedded migrations")
	ErrErrorFetch                  = errors.New("failed to fetch error")
	ErrLogsProcess                 = errors.New("failed to process logs")
	ErrMetricsProcess              = errors.New("failed to process metrics")
	ErrMigrationConnectionOpen     = errors.New("failed to open migration connection")
	ErrMigrationDriverInitialize   = errors.New("failed to initialize migration driver")
	ErrMigrationsApply             = errors.New("failed to apply migrations")
	ErrMigratorInitialize          = errors.New("failed to initialize migrator")
	ErrPasswordHash                = errors.New("failed to hash password")
	ErrSeededAdminCreate           = errors.New("failed to create seeded admin")
	ErrSeededAdminPasswordConverge = errors.New("failed to converge seeded admin password")
	ErrServiceResultFetch          = errors.New("failed to fetch service_result")
	ErrTracesProcess               = errors.New("failed to process traces")
)

// Failures of an outbound call, wrapped around its cause.
var (
	ErrChannelDelivery   = errors.New("delivery failed")
	ErrLLMCall           = errors.New("llm call failed")
	ErrExplanationFailed = errors.New("llm explanation failed")
)
