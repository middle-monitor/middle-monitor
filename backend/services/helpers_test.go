package services

import (
	"database/sql"
	"encoding/hex"
	"io"
	"middle-monitor/backend/internal/credentialenc"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// alertRuleColumns returns the 25 columns that scanAlertRules expects.
var alertRuleColumns = []string{
	"id", "organization_id", "name", "description", "type", "target_type", "target_id",
	"metric", "operator", "threshold", "duration", "severity", "enabled", "channels",
	"aggregation", "warning_threshold", "critical_threshold", "recovery_threshold",
	"tags", "notify_warning", "notify_critical", "created_at", "updated_at",
	"custom_metric", "custom_labels",
}

// channelColumns are the 8 columns GetChannels/GetChannelsByIDs scan.
var channelColumns = []string{
	"id", "organization_id", "name", "type", "config", "enabled", "created_at", "updated_at",
}

// incidentColumns are columns GetIncidents scans.
var incidentColumns = []string{
	"id", "organization_id", "alert_rule_id", "service_id", "host_id", "title", "description", "severity", "status",
	"service", "started_at", "resolved_at", "resolution_note", "acknowledged_at", "acknowledged_by",
}

var enabledRuleColumns = []string{
	"id", "organization_id", "name", "description", "type", "target_type", "target_id",
	"metric", "operator", "threshold", "duration", "severity", "enabled", "channels",
	"aggregation", "warning_threshold", "critical_threshold", "recovery_threshold",
	"custom_metric", "custom_labels",
}

func newTestAuth(t *testing.T) (*AuthService, sqlmock.Sqlmock) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	db, mock := newDB(t)
	return NewAuthService(db), mock
}

// loginIdentityCols are the columns Login scans for the identity (org-agnostic).
var loginIdentityCols = []string{
	"id", "organization_id", "email", "password_hash", "name", "email_verified", "totp_enabled", "created_at", "updated_at",
}

// userOrgCols are the columns GetUserOrganizations scans (org + per-org role).
var userOrgCols = []string{"id", "name", "slug", "plan", "trial_ends_at", "mfa_required", "created_at", "updated_at", "role"}

// getUserInOrgCols are the columns GetUserInOrg scans (identity + active org).
var getUserInOrgCols = []string{
	"id", "email", "name", "role", "email_verified", "totp_enabled", "created_at", "updated_at", "last_login_at",
	"o_id", "o_name", "o_slug", "o_plan", "o_trial_ends_at", "o_mfa_required", "o_created_at", "o_updated_at",
}

type mockRoundTripper struct {
	statusCode int
	body       string
	callCount  int
}

func (m *mockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	m.callCount++
	return &http.Response{
		StatusCode: m.statusCode,
		Body:       io.NopCloser(strings.NewReader(m.body)),
		Header:     make(http.Header),
	}, nil
}

// testKeyHex is a 64-char hex string (32 bytes) used as the AES-256 encryption key
// in tests. Must match the value set in TestMain so Global() and testKeyRing() share
// the same key (encrypt/decrypt round-trips work across both code paths).
const testKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// testKeyRing builds a *credentialenc.KeyRing directly from testKeyHex.
// Use this in tests that accept a ring parameter explicitly (avoids relying on Global()).
func testKeyRing(t *testing.T) *credentialenc.KeyRing {
	t.Helper()
	b, err := hex.DecodeString(testKeyHex)
	if err != nil {
		t.Fatalf("testKeyRing: %v", err)
	}
	return &credentialenc.KeyRing{ActiveID: "v1", Keys: map[string][]byte{"v1": b}}
}

// routingCols are the columns routingRulesForSeverity scans.
var routingCols = []string{"channels", "tags", "target_type", "target_id"}

var profileCols = []string{
	"id", "organization_id", "service",
	"profile_type", "duration_seconds", "size_bytes", "memory_mb", "created_at",
}

// statusRows mocks the failing/warning counts the health query returns.
func statusRows(failing, warning int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"failing", "warning"}).AddRow(int64(failing), int64(warning))
}

// errorCols returns columns for application_errors scan.
func errorCols() []string {
	return []string{"id", "organization_id", "name", "message", "file", "line",
		"timestamp", "service", "http_method", "http_url", "http_headers", "http_body"}
}

// groupedErrorCols returns columns for the grouped-errors scan: it leaves out
// http_headers and http_body, and selects trace_id (so the UI can deep-link an
// error to its trace).
func groupedErrorCols() []string {
	return []string{"id", "organization_id", "name", "message", "file", "line",
		"timestamp", "service", "http_method", "http_url", "trace_id"}
}

// eventCols returns columns for events scan.
func eventCols() []string {
	return []string{"id", "organization_id", "type", "service", "message", "metadata", "timestamp"}
}

// hostCols are the columns returned by GetHostsForOrg (after string replacement).
var hostCols = []string{"id", "organization_id", "name", "host", "service", "display_name", "created_at"}

// hostStatusCols returns columns for the batched hostStatuses query.
func hostStatusCols() []string {
	return []string{"host_id", "status", "timestamp", "type", "metric_value", "warning_threshold", "critical_threshold"}
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func nullFloat(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: true} }
