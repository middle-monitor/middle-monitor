package services

import (
	"errors"
	"fmt"
)

var (
	ErrSeriesTemplate = errors.New("failed to create series index template")
	ErrSeriesIndex    = errors.New("failed to index series point")
	ErrSeriesMarshal  = errors.New("failed to marshal series document")
	ErrSeriesRequest  = errors.New("failed to create series request")
	ErrLabelMalformed = errors.New("malformed label pair")

	ErrSeriesMetricRequired   = errors.New("metric name is required")
	ErrSeriesAggregation      = errors.New("unsupported aggregation")
	ErrSeriesRange            = errors.New("end must be after start")
	ErrSeriesSearch           = errors.New("series search failed")
	ErrSeriesDecode           = errors.New("failed to decode series response")
	ErrSeriesFilterMalformed  = errors.New("malformed filter, expected key:value")
	ErrSeriesLabelKeyRequired = errors.New("label key is required")
	ErrSeriesStep             = errors.New("step must be a positive number of seconds")
	ErrSeriesTooMany          = errors.New("too many series for rate, narrow the query with filters")

	ErrExpressionQueries    = errors.New("between 1 and 5 queries are required")
	ErrExpressionRef        = errors.New("query refs must be distinct single uppercase letters")
	ErrExpressionTooLong    = errors.New("expression is too long")
	ErrExpressionSyntax     = errors.New("invalid expression")
	ErrExpressionUnknownRef = errors.New("expression references an unknown query")
	ErrExpressionFunction   = errors.New("unknown expression function")
	ErrExpressionScalar     = errors.New("expression must reference at least one query")
	ErrExpressionNoMatch    = errors.New("no series share the same labels on both sides")

	ErrSeriesIndexList   = errors.New("failed to list series indices")
	ErrSeriesIndexDelete = errors.New("failed to delete series index")

	ErrEmailTargetsMissing = errors.New("missing emails in config")
	ErrOrgSMTPMissing      = errors.New("no SMTP server configured for this organization: set one in Settings then Email")

	ErrWebhookURLMissing = errors.New("missing webhook_url in config")
	ErrWebhookMarshal    = errors.New("failed to marshal webhook payload")
	ErrDeliveryNotFound  = errors.New("delivery not found")
	ErrChannelNotFound   = errors.New("notification channel not found")
)

// WebhookStatusError reports a receiver that answered with an error status.
type WebhookStatusError struct {
	StatusCode int
}

func (e *WebhookStatusError) Error() string {
	return fmt.Sprintf("webhook returned status %d", e.StatusCode)
}

// ---- Validation and lookup errors ----
//
// Callers match these with errors.Is. Before they existed, the API layer
// compared err.Error() to a literal, so rewording a message silently turned a
// 404 into a 500.
var (
	ErrNameRequired    = errors.New("name is required")
	ErrOrgNameRequired = errors.New("organization name is required")
	ErrOrgNameTooLong  = errors.New("organization name must be 255 characters or fewer")

	ErrTokenRequired = errors.New("token is required")
	ErrTokenInvalid  = errors.New("invalid token")
	ErrTokenExpired  = errors.New("token expired")
	ErrTokenNotFound = errors.New("token not found")

	ErrNotAPIKey      = errors.New("not an api key")
	ErrAPIKeyInvalid  = errors.New("invalid api key")
	ErrAPIKeyExpired  = errors.New("api key expired")
	ErrAPIKeyNotFound = errors.New("api key not found")

	ErrHostNotFound = errors.New("host not found")
	// ErrHostHasServices marks a delete refused because services are still attached to the host.
	ErrHostHasServices   = errors.New("host still has services attached; delete or detach them first")
	ErrHostGroupNotFound = errors.New("host group not found")
	// ErrGroupHasHosts marks a delete refused because hosts still belong to the group.
	ErrGroupHasHosts           = errors.New("host group still has hosts attached; reassign them first")
	ErrDefaultGroupUndeletable = errors.New("the default host group cannot be deleted")

	ErrServiceNotFound   = errors.New("service not found")
	ErrNotAgentService   = errors.New("not an agent service")
	ErrServiceFetch      = errors.New("failed to fetch service")
	ErrDashboardNotFound = errors.New("custom dashboard not found")

	ErrMaintenanceTargetType     = errors.New("target_type must be 'service' or 'host'")
	ErrMaintenanceRange          = errors.New("ends_at must be after starts_at")
	ErrMaintenanceTargetNotFound = errors.New("target not found in this organization")
	ErrMaintenanceNotFound       = errors.New("maintenance window not found")

	ErrLinkTargetType     = errors.New("target_type must be 'host', 'service', 'host_group' or 'app'")
	ErrLinkAppNameMissing = errors.New("target_app_name is required for 'app' links")
	ErrLinkSelfReference  = errors.New("an application cannot be linked to itself")
	ErrLinkNotFound       = errors.New("link not found or access denied")

	ErrAlertRuleNotFound = errors.New("alert rule not found")
	ErrIncidentNotFound  = errors.New("incident not found")

	ErrJSMAPIKeyMissing = errors.New("missing api_key in config (JSM API-integration key)")
	ErrJSMAliasMissing  = errors.New("missing alias to resolve JSM alert")

	ErrSMTPProviderMissing = errors.New("SMTP provider not initialized")
	ErrOrgSMTPUnusable     = errors.New("no usable SMTP configuration for this organization")

	ErrWhatsAppPhoneMissing   = errors.New("missing phone_number in config")
	ErrWhatsAppTokenMissing   = errors.New("missing token in config")
	ErrWhatsAppPhoneIDMissing = errors.New("missing phone_number_id in config")

	ErrKafkaWriterMissing = errors.New("kafka writer is not initialized")
	// ErrKafkaPublish is transient on the broker side; ingestion answers 503 so clients retry.
	ErrKafkaPublish = errors.New("failed to publish to kafka")

	ErrOTLPDecode       = errors.New("invalid otlp payload")
	ErrOTLPSignal       = errors.New("unknown otlp signal")
	ErrOTLPItemTooLarge = errors.New("a single otlp item exceeds the message size limit")

	ErrSelfAccountDelete = errors.New("cannot delete your own account")
	ErrInvalidRole       = errors.New("invalid role")
	ErrLastAdmin         = errors.New("cannot remove the last admin of the organization")
	ErrOrgDelete         = errors.New("failed to delete organization")
	ErrAccountDelete     = errors.New("failed to delete account")
	ErrOrgDataPurge      = errors.New("failed to purge organization data")
)

// ---- Typed errors carrying dynamic data ----

// NameTakenError reports a unique-constraint conflict on a human-facing name.
type NameTakenError struct {
	Kind string // "host", "host group", "application"
	Name string
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("a %s named %q already exists", e.Kind, e.Name)
}

// NotInOrgError reports a target that does not exist inside the caller's org.
type NotInOrgError struct {
	Kind string // "host", "host group", "service", "organization", "application"
	Ref  string
}

func (e *NotInOrgError) Error() string {
	return fmt.Sprintf("%s %s not found or not in organization", e.Kind, e.Ref)
}

// OpenSearchStatusError reports a non-2xx answer from OpenSearch.
type OpenSearchStatusError struct {
	Op         string
	StatusCode int
	// Reason is OpenSearch's own explanation, e.g. a mapping conflict; empty if none was sent.
	Reason string
}

func (e *OpenSearchStatusError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("opensearch %s: status %d: %s", e.Op, e.StatusCode, e.Reason)
	}
	return fmt.Sprintf("opensearch %s: status %d", e.Op, e.StatusCode)
}

// JSMStatusError reports a non-2xx answer from Jira Service Management.
type JSMStatusError struct {
	Op         string
	StatusCode int
	Body       string
}

func (e *JSMStatusError) Error() string {
	return fmt.Sprintf("jsm %s: status %d: %s", e.Op, e.StatusCode, e.Body)
}

// WhatsAppStatusError reports a non-2xx answer from the WhatsApp API.
type WhatsAppStatusError struct {
	StatusCode int
}

func (e *WhatsAppStatusError) Error() string {
	return fmt.Sprintf("whatsapp api returned status %d", e.StatusCode)
}

// UnknownChannelTypeError reports a notification channel this build cannot send to.
type UnknownChannelTypeError struct {
	Type string
}

func (e *UnknownChannelTypeError) Error() string {
	return fmt.Sprintf("unknown notification channel type: %s", e.Type)
}

// UnknownHTTPAuthModeError reports an http_auth mode the credential codec does not know.
type UnknownHTTPAuthModeError struct {
	Mode string
}

func (e *UnknownHTTPAuthModeError) Error() string {
	return fmt.Sprintf("http_auth: unknown mode %q", e.Mode)
}

// SigningMethodError reports a JWT signed with an unexpected algorithm.
type SigningMethodError struct {
	Alg interface{}
}

func (e *SigningMethodError) Error() string {
	return fmt.Sprintf("unexpected signing method: %v", e.Alg)
}

// ---- Authentication and account errors ----
//
// Moved here from auth.go: the rule is one errors.go per package, so a caller
// looking for a sentinel has one file to open.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserExists         = errors.New("user with this email already exists")
	ErrOrgExists          = errors.New("organization with this slug already exists")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrUserNotFound       = errors.New("user not found")
	ErrOrgNotFound        = errors.New("organization not found")
	ErrWeakPassword       = errors.New("password must be at least 8 characters with uppercase, lowercase, and number")
	ErrInvalidEmail       = errors.New("invalid email format")
	ErrInvalidSlug        = errors.New("identifier may only contain lowercase letters, digits and hyphens (e.g. my-company)")
	ErrAlreadyVerified    = errors.New("email already verified")
	ErrInvalidMFACode     = errors.New("invalid authentication code")
	ErrTOTPNotPending     = errors.New("no pending two-factor setup; start setup first")
	ErrMFARequiredByOrg   = errors.New("your organization requires two-factor authentication; it cannot be disabled")
	ErrMFAEnrollFirst     = errors.New("enable two-factor authentication on your own account before enforcing it for the organization")
)

var (
	ErrStatusPageNotFound = errors.New("no public status page for this organization")
	// A service pointing at a host owned by another organization: the host page
	// filters by org and would never list it, while the host's service count
	// would still include it.
	ErrHostOrgMismatch = errors.New("host belongs to another organization")

	ErrSQLKeyRingMissing     = errors.New("sql_credentials: encryption key ring not available")
	ErrHTTPAuthBearerMissing = errors.New("http_auth: bearer token is required (or enable preserve_http_secrets when editing)")
	ErrHTTPAuthBasicMissing  = errors.New("http_auth: basic user and password are required (or preserve with existing secrets)")
	ErrHTTPAuthBlobMissing   = errors.New("http_auth: missing encrypted secret blob")
)

// ExpressionError locates an expression mistake: a 1-based position, or the offending token.
type ExpressionError struct {
	Err      error
	Position int
	Token    string
}

func (e *ExpressionError) Error() string {
	if e.Token != "" {
		return fmt.Sprintf("%s: %s", e.Token, e.Err)
	}
	return fmt.Sprintf("at position %d: %s", e.Position, e.Err)
}

func (e *ExpressionError) Unwrap() error { return e.Err }

// Wrapped around the underlying cause, which callers reach with errors.Is/As.
var (
	ErrAPIKeyCreate                       = errors.New("failed to create api key")
	ErrAPIKeyDelete                       = errors.New("failed to delete api key")
	ErrAPIKeyScan                         = errors.New("failed to scan api key")
	ErrAPIKeysFetch                       = errors.New("failed to fetch api keys")
	ErrAccessTokenSign                    = errors.New("failed to sign access token")
	ErrAdminsCount                        = errors.New("failed to count admins")
	ErrAlertRuleCreate                    = errors.New("failed to create alert rule")
	ErrAlertRuleUpdate                    = errors.New("failed to update alert rule")
	ErrBulkRequestCreate                  = errors.New("failed to create bulk request")
	ErrCustomDashboardCreate              = errors.New("failed to create custom dashboard")
	ErrCustomDashboardDelete              = errors.New("failed to delete custom dashboard")
	ErrCustomDashboardScan                = errors.New("failed to scan custom dashboard")
	ErrCustomDashboardUpdate              = errors.New("failed to update custom dashboard")
	ErrCustomDashboardsFetch              = errors.New("failed to fetch custom dashboards")
	ErrDefaultHostGroupCreate             = errors.New("failed to create default host group")
	ErrDeleteQueryMarshal                 = errors.New("failed to marshal delete query")
	ErrDeleteRequestCreate                = errors.New("failed to create delete request")
	ErrDocumentIndex                      = errors.New("failed to index document")
	ErrDocumentMarshal                    = errors.New("failed to marshal document")
	ErrEmailVerify                        = errors.New("failed to verify email")
	ErrErrorFetch                         = errors.New("failed to fetch error")
	ErrErrorSave                          = errors.New("failed to save error")
	ErrErrorScan                          = errors.New("failed to scan error")
	ErrErrorServicesFetch                 = errors.New("failed to fetch error services")
	ErrErrorsFetch                        = errors.New("failed to fetch errors")
	ErrEventCreate                        = errors.New("failed to create event")
	ErrEventScan                          = errors.New("failed to scan event")
	ErrEventsFetch                        = errors.New("failed to fetch events")
	ErrExistingOrgCheck                   = errors.New("failed to check existing org")
	ErrExistingUserCheck                  = errors.New("failed to check existing user")
	ErrHomeOrgRepoint                     = errors.New("failed to repoint home org")
	ErrHostCheck                          = errors.New("failed to check host")
	ErrHostCreate                         = errors.New("failed to create host")
	ErrHostDelete                         = errors.New("failed to delete host")
	ErrHostFetch                          = errors.New("failed to fetch host")
	ErrHostGet                            = errors.New("failed to get host")
	ErrHostGroupCreate                    = errors.New("failed to create host group")
	ErrHostScan                           = errors.New("failed to scan host")
	ErrHostServicesCount                  = errors.New("failed to count host services")
	ErrHostStatsCompute                   = errors.New("failed to compute host stats")
	ErrHostUpdate                         = errors.New("failed to update host")
	ErrHostsCount                         = errors.New("failed to count hosts")
	ErrHostsFetch                         = errors.New("failed to fetch hosts")
	ErrIncidentCreate                     = errors.New("failed to create incident")
	ErrIncidentScan                       = errors.New("failed to scan incident")
	ErrIncidentServiceScan                = errors.New("failed to scan incident service")
	ErrIncidentServicesFetch              = errors.New("failed to fetch incident services")
	ErrIncidentStatsCompute               = errors.New("failed to compute incident stats")
	ErrIncidentUpdate                     = errors.New("failed to update incident")
	ErrIncidentsCount                     = errors.New("failed to count incidents")
	ErrIncidentsFetch                     = errors.New("failed to fetch incidents")
	ErrIndexBulk                          = errors.New("failed to bulk index")
	ErrIndexCreate                        = errors.New("failed to create index")
	ErrIndexExistenceCheck                = errors.New("failed to check index existence")
	ErrInvitationAccept                   = errors.New("failed to accept invitation")
	ErrLinkCreate                         = errors.New("failed to create link")
	ErrLogAggregationDecode               = errors.New("failed to decode log aggregation")
	ErrLogAggregationMarshal              = errors.New("failed to marshal log aggregation")
	ErrLogRequestUnmarshal                = errors.New("failed to unmarshal log request")
	ErrMFAChallengeSign                   = errors.New("failed to sign mfa challenge")
	ErrMaintenanceWindowCreate            = errors.New("failed to create maintenance window")
	ErrMaintenanceWindowDelete            = errors.New("failed to delete maintenance window")
	ErrMaintenanceWindowScan              = errors.New("failed to scan maintenance window")
	ErrMaintenanceWindowsList             = errors.New("failed to list maintenance windows")
	ErrMappingMarshal                     = errors.New("failed to marshal mapping")
	ErrMembershipAdd                      = errors.New("failed to add membership")
	ErrMembershipCheck                    = errors.New("failed to check membership")
	ErrMembershipCreate                   = errors.New("failed to create membership")
	ErrMembershipLoad                     = errors.New("failed to load membership")
	ErrMembershipRemove                   = errors.New("failed to remove membership")
	ErrMembershipRoleFetch                = errors.New("failed to fetch membership role")
	ErrMembershipRoleUpdate               = errors.New("failed to update membership role")
	ErrMembershipsCount                   = errors.New("failed to count memberships")
	ErrMetricSave                         = errors.New("failed to save metric")
	ErrMetricScan                         = errors.New("failed to scan metric")
	ErrMetricStatsFetch                   = errors.New("failed to fetch metric stats")
	ErrMetricsFetch                       = errors.New("failed to fetch metrics")
	ErrMetricsRequestUnmarshal            = errors.New("failed to unmarshal metrics request")
	ErrNotificationChannelCreate          = errors.New("failed to create notification channel")
	ErrNotificationChannelDelete          = errors.New("failed to delete notification channel")
	ErrNotificationChannelScan            = errors.New("failed to scan notification channel")
	ErrNotificationChannelUpdate          = errors.New("failed to update notification channel")
	ErrNotificationChannelsFetch          = errors.New("failed to fetch notification channels")
	ErrOpensearchInitialize               = errors.New("failed to initialize opensearch")
	ErrOrgMFAPolicyCheck                  = errors.New("failed to check org mfa policy")
	ErrOrganizationCreate                 = errors.New("failed to create organization")
	ErrOrganizationUpdate                 = errors.New("failed to update organization")
	ErrOrphanedUserDelete                 = errors.New("failed to delete orphaned user")
	ErrPasswordHash                       = errors.New("failed to hash password")
	ErrPasswordReset                      = errors.New("failed to reset password")
	ErrPasswordResetTokenIssue            = errors.New("failed to issue password reset token")
	ErrPayloadMarshal                     = errors.New("failed to marshal payload")
	ErrPersonalAPIKeyCreate               = errors.New("failed to create personal api key")
	ErrQRCodeEncode                       = errors.New("failed to encode qr code")
	ErrQRCodeRender                       = errors.New("failed to render qr code")
	ErrRecoveryCodeHash                   = errors.New("failed to hash recovery code")
	ErrRecoveryCodeStore                  = errors.New("failed to store recovery code")
	ErrRecoveryCodesClear                 = errors.New("failed to clear recovery codes")
	ErrRecoveryCodesCommit                = errors.New("failed to commit recovery codes")
	ErrRefreshTokenSign                   = errors.New("failed to sign refresh token")
	ErrRequestCreate                      = errors.New("failed to create request")
	ErrResultScan                         = errors.New("failed to scan result")
	ErrSearchBodyMarshal                  = errors.New("failed to marshal search body")
	ErrSearchRequestCreate                = errors.New("failed to create search request")
	ErrSearchResponseDecode               = errors.New("failed to decode search response")
	ErrServiceCreate                      = errors.New("failed to create service")
	ErrServiceDelete                      = errors.New("failed to delete service")
	ErrServiceGet                         = errors.New("failed to get service")
	ErrServiceResultFetch                 = errors.New("failed to fetch service result")
	ErrServiceResultsFetch                = errors.New("failed to fetch service results")
	ErrServiceResultsForAgentMetricsQuery = errors.New("failed to query service_results for agent metrics")
	ErrServiceScan                        = errors.New("failed to scan service")
	ErrServiceUpdate                      = errors.New("failed to update service")
	ErrServicesFetch                      = errors.New("failed to fetch services")
	ErrServicesForHostFetch               = errors.New("failed to fetch services for host")
	ErrTOTPDisable                        = errors.New("failed to disable totp")
	ErrTOTPDisableCommit                  = errors.New("failed to commit totp disable")
	ErrTOTPEnable                         = errors.New("failed to enable totp")
	ErrTOTPSecretGenerate                 = errors.New("failed to generate totp secret")
	ErrTOTPSecretLoad                     = errors.New("failed to load totp secret")
	ErrTOTPSecretStore                    = errors.New("failed to store totp secret")
	ErrTargetVerify                       = errors.New("failed to verify target")
	ErrTokenGenerate                      = errors.New("failed to generate token")
	ErrTraceAggregationDecode             = errors.New("failed to decode trace aggregation")
	ErrTraceAggregationMarshal            = errors.New("failed to marshal trace aggregation")
	ErrTraceRequestUnmarshal              = errors.New("failed to unmarshal trace request")
	ErrTransactionCommit                  = errors.New("failed to commit transaction")
	ErrTransactionStart                   = errors.New("failed to start transaction")
	ErrUserCreate                         = errors.New("failed to create user")
	ErrUserFind                           = errors.New("failed to find user")
	ErrUserInOrgFetch                     = errors.New("failed to fetch user in org")
	ErrUserOrganizationScan               = errors.New("failed to scan user organization")
	ErrUserOrganizationsFetch             = errors.New("failed to fetch user organizations")
	ErrUserRoleSync                       = errors.New("failed to sync user role")
	ErrUserScan                           = errors.New("failed to scan user")
	ErrUsersFetch                         = errors.New("failed to fetch users")
	ErrVerificationTokenIssue             = errors.New("failed to issue verification token")
)

// Input and delivery errors, wrapped around their cause.
var (
	ErrDateInvalid      = errors.New("invalid date, expected RFC 3339")
	ErrSMTPSend         = errors.New("smtp send failed")
	ErrHTTPAuthInvalid  = errors.New("http_auth: invalid credentials")
	ErrHTTPAuthPreserve = errors.New("http_auth: cannot preserve the existing secret")
)
