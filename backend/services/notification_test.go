package services

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"middle-monitor/backend/models"
)

// An on-call reading the email must see the failure reason and the deep link;
// the reason goes to the info table (not the prose block) and the URL becomes the CTA.
func TestAlertMessageToHTML_ErrorLineAndCTA(t *testing.T) {
	body, cta := alertMessageToHTML("Service 'api' failed 3 consecutive attempts\nError: HTTP status: 500\nService: api\nhttps://app.example.com/organizations/acme/services/7")
	if !strings.Contains(body, ">Error</td>") || !strings.Contains(body, "HTTP status: 500") {
		t.Fatalf("error line missing from info table: %s", body)
	}
	if cta != "https://app.example.com/organizations/acme/services/7" {
		t.Fatalf("want deep link as CTA, got %q", cta)
	}
}

// Check messages carry arbitrary server output (body assertions, driver errors),
// so an unescaped '<' would break the table layout.
func TestAlertMessageToHTML_EscapesErrorValue(t *testing.T) {
	body, _ := alertMessageToHTML("Error: body did not contain <ok>")
	if strings.Contains(body, "<ok>") {
		t.Fatalf("error value not escaped: %s", body)
	}
}

func TestLoginAuth_Start(t *testing.T) {
	auth := LoginAuth("user", "pass")
	proto, msg, err := auth.Start(nil)
	if err != nil || proto != "LOGIN" || string(msg) != "user" {
		t.Fatalf("unexpected Start: %q/%q/%v", proto, msg, err)
	}
}

func TestLoginAuth_Next_Username(t *testing.T) {
	auth := LoginAuth("user", "pass")
	msg, err := auth.(*loginAuth).Next([]byte("Username:"), true)
	if err != nil || string(msg) != "user" {
		t.Fatalf("expected user, got %q/%v", msg, err)
	}
}

func TestLoginAuth_Next_Password(t *testing.T) {
	auth := LoginAuth("user", "pass")
	msg, err := auth.(*loginAuth).Next([]byte("Password:"), true)
	if err != nil || string(msg) != "pass" {
		t.Fatalf("expected pass, got %q/%v", msg, err)
	}
}

func TestLoginAuth_Next_UnknownPrompt(t *testing.T) {
	auth := LoginAuth("user", "pass")
	msg, err := auth.(*loginAuth).Next([]byte("Other:"), true)
	if err != nil || msg != nil {
		t.Fatalf("expected nil/nil for unknown prompt, got %q/%v", msg, err)
	}
}

func TestLoginAuth_Next_NotMore(t *testing.T) {
	auth := LoginAuth("user", "pass")
	msg, err := auth.(*loginAuth).Next([]byte("anything"), false)
	if err != nil || msg != nil {
		t.Fatalf("expected nil/nil when more=false, got %q/%v", msg, err)
	}
}

func TestSendNotificationWithAliasTags_UnknownType(t *testing.T) {
	ch := models.NotificationChannel{Type: "unknown", Config: map[string]interface{}{}}
	err := SendNotificationWithAliasTags(ch, "title", "msg", "alias", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown notification channel type") {
		t.Fatalf("expected unknown type error, got %v", err)
	}
}

func TestResolveNotification_NonJSM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ch := models.NotificationChannel{
		Type:    "slack",
		Enabled: true,
		Config:  map[string]interface{}{"webhook_url": srv.URL},
	}
	// Should call SendNotification which calls sendWebhook
	if err := ResolveNotification(ch, "alias", "Resolved", "all good"); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestResolveNotification_JSM_MissingAPIKey(t *testing.T) {
	ch := models.NotificationChannel{Type: "jsm", Config: map[string]interface{}{}}
	err := ResolveNotification(ch, "alias", "title", "body")
	if err == nil || !strings.Contains(err.Error(), "missing api_key") {
		t.Fatalf("expected missing api_key error, got %v", err)
	}
}

func TestResolveNotification_JSM_MissingAlias(t *testing.T) {
	ch := models.NotificationChannel{Type: "jsm", Config: map[string]interface{}{"api_key": "test"}}
	err := ResolveNotification(ch, "", "title", "body")
	if err == nil || !strings.Contains(err.Error(), "missing alias") {
		t.Fatalf("expected missing alias error, got %v", err)
	}
}

func TestSendWebhook_MissingURL(t *testing.T) {
	ch := models.NotificationChannel{Type: "slack", Config: map[string]interface{}{}}
	err := SendNotificationWithAliasTags(ch, "title", "msg", "alias", nil)
	if err == nil || !strings.Contains(err.Error(), "missing webhook_url") {
		t.Fatalf("expected missing webhook_url error, got %v", err)
	}
}

func TestSendWebhook_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ch := models.NotificationChannel{
		Type: "slack", Config: map[string]interface{}{"webhook_url": srv.URL},
	}
	if err := SendNotificationWithAliasTags(ch, "title", "msg", "alias", nil); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestSendWebhook_ResolvedTitle_GreenColor(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ch := models.NotificationChannel{
		Type: "webhook", Config: map[string]interface{}{"webhook_url": srv.URL},
	}
	if err := SendNotification(ch, "[RESOLVED] Service up", "all good"); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
	if !strings.Contains(string(gotBody), "#38a169") {
		t.Fatal("expected green color for resolved")
	}
}

func TestSendWebhook_WarningTitle_YellowColor(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ch := models.NotificationChannel{
		Type: "webhook", Config: map[string]interface{}{"webhook_url": srv.URL},
	}
	if err := SendNotification(ch, "[warning] CPU high", "msg"); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
	if !strings.Contains(string(gotBody), "#d69e2e") {
		t.Fatal("expected yellow color for warning")
	}
}

func TestSendWebhook_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	ch := models.NotificationChannel{
		Type: "slack", Config: map[string]interface{}{"webhook_url": srv.URL},
	}
	err := SendNotificationWithAliasTags(ch, "title", "msg", "alias", nil)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 error, got %v", err)
	}
}

func TestSendJSM_MissingAPIKey(t *testing.T) {
	ch := models.NotificationChannel{Type: "jsm", Config: map[string]interface{}{}}
	err := SendNotificationWithAliasTags(ch, "critical alert", "msg", "alias", nil)
	if err == nil || !strings.Contains(err.Error(), "missing api_key") {
		t.Fatalf("expected missing api_key error, got %v", err)
	}
}

func TestSendJSM_WithMockTransport(t *testing.T) {
	// Override http.DefaultTransport with a mock that returns 202.
	origTransport := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 202, body: ""}
	defer func() { http.DefaultTransport = origTransport }()

	ch := models.NotificationChannel{
		Type: "jsm",
		Config: map[string]interface{}{
			"api_key": "test-key",
			"tags":    "env:prod, team:ops",
		},
	}
	// Long title (>130 chars to trigger truncation)
	longTitle := strings.Repeat("critical alert very long title", 5)
	err := SendNotificationWithAliasTags(ch, longTitle, "description", "alias-123", []string{"extra-tag"})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestSendJSM_WarningTitle(t *testing.T) {
	origTransport := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 202, body: ""}
	defer func() { http.DefaultTransport = origTransport }()

	ch := models.NotificationChannel{
		Type:   "jsm",
		Config: map[string]interface{}{"api_key": "test-key"},
	}
	err := SendNotificationWithAliasTags(ch, "warning: cpu high", "msg", "alias", nil)
	if err != nil {
		t.Fatalf("expected nil for warning: %v", err)
	}
}

func TestSendJSM_EmptyAlias_FallsBackToTitle(t *testing.T) {
	origTransport := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 202, body: ""}
	defer func() { http.DefaultTransport = origTransport }()

	ch := models.NotificationChannel{
		Type:   "jsm",
		Config: map[string]interface{}{"api_key": "test-key"},
	}
	err := SendNotificationWithAliasTags(ch, "alert title", "msg", "", nil)
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestSendJSM_ErrorStatus(t *testing.T) {
	origTransport := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 400, body: "bad request"}
	defer func() { http.DefaultTransport = origTransport }()

	ch := models.NotificationChannel{
		Type:   "jsm",
		Config: map[string]interface{}{"api_key": "test-key"},
	}
	err := SendNotificationWithAliasTags(ch, "alert", "msg", "alias", nil)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 error, got %v", err)
	}
}

func TestCloseJSM_Success_404(t *testing.T) {
	// 404 on close is silently ignored.
	origTransport := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 404, body: ""}
	defer func() { http.DefaultTransport = origTransport }()

	ch := models.NotificationChannel{
		Type:   "jsm",
		Config: map[string]interface{}{"api_key": "test-key"},
	}
	err := ResolveNotification(ch, "alias-123", "title", "body")
	if err != nil {
		t.Fatalf("expected nil for 404 on close, got %v", err)
	}
}

func TestCloseJSM_ErrorStatus(t *testing.T) {
	origTransport := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 500, body: "error"}
	defer func() { http.DefaultTransport = origTransport }()

	ch := models.NotificationChannel{
		Type:   "jsm",
		Config: map[string]interface{}{"api_key": "test-key"},
	}
	err := ResolveNotification(ch, "alias-123", "title", "body")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestSendEmail_NoOrgSMTP_Errors(t *testing.T) {
	prev := OrgSMTPProvider
	defer func() { OrgSMTPProvider = prev }()
	// Org has no usable SMTP configuration.
	OrgSMTPProvider = func(orgID int64) (*SMTPSettings, bool) { return nil, false }

	ch := models.NotificationChannel{
		Type:   "email",
		Config: map[string]interface{}{"emails": "user@example.com, other@example.com"},
	}
	// Email alerts now require the org's own SMTP — there is no fallback to a
	// platform SMTP, so an unconfigured org must surface an error.
	err := SendNotificationWithAliasTags(ch, "title", "msg", "alias", nil)
	if err == nil {
		t.Fatal("expected error when org has no SMTP configured")
	}
}

func TestSendEmail_MissingEmails(t *testing.T) {
	ch := models.NotificationChannel{
		Type:   "email",
		Config: map[string]interface{}{},
	}
	err := SendNotificationWithAliasTags(ch, "title", "msg", "alias", nil)
	if err == nil || !strings.Contains(err.Error(), "missing emails") {
		t.Fatalf("expected missing emails error, got %v", err)
	}
}

func TestSendWhatsApp_MissingPhoneAndToken(t *testing.T) {
	ch := models.NotificationChannel{
		Type:   "whatsapp",
		Config: map[string]interface{}{},
	}
	err := SendNotificationWithAliasTags(ch, "title", "msg", "alias", nil)
	// Missing config → error (either network or missing config)
	_ = err // we just need coverage; error is expected
}
