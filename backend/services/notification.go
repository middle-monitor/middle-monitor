package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/smtp"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// brandPrimary is the Middle Monitor brand purple (matches --brand-primary /
// brand-600 in the frontend design system). Used for the primary header/CTA in
// transactional emails so they match the app. Semantic alert colors
// (critical/warning/resolved) are intentionally NOT derived from this.
const brandPrimary = "#7c3aed"

type loginAuth struct {
	username, password string
}

func LoginAuth(username, password string) smtp.Auth {
	return &loginAuth{
		username: username,
		password: password,
	}
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:":
			return []byte(a.username), nil
		case "Password:":
			return []byte(a.password), nil
		}
	}
	return nil, nil
}

func SendNotification(channel models.NotificationChannel, title, message string) error {
	// Default alias = title (back-compat for one-off notifications).
	return SendNotificationWithAlias(channel, title, message, title)
}

// SendNotificationWithAlias is like SendNotification but carries a stable alias,
// which JSM uses to de-duplicate and (later) close the right alert. The alias is
// also the dedup_key a structured webhook receiver sees.
func SendNotificationWithAlias(channel models.NotificationChannel, title, message, alias string) error {
	return SendNotificationWithAliasTags(channel, title, message, alias, nil)
}

// SendNotificationWithAliasTags also forwards extra tags (used by routing rules)
// to channels that support them (JSM).
func SendNotificationWithAliasTags(channel models.NotificationChannel, title, message, alias string, tags []string) error {
	return SendNotificationEvent(channel, stringEvent(eventTypeFromTitle(title), title, message, alias), tags)
}

// eventTypeFromTitle recovers the transition from the title for the callers that
// still send strings. It is the same reading the Slack payload already does to
// pick its colour; typed callers set the event explicitly and never come here.
func eventTypeFromTitle(title string) string {
	switch {
	case strings.HasPrefix(title, "[RESOLVED]"):
		return EventIncidentResolved
	case strings.HasPrefix(title, "[TEST]"):
		return EventTest
	default:
		return EventIncidentOpened
	}
}

// SendNotificationEvent delivers one typed event on one channel. The human
// channels render the title and message; a webhook configured with
// format: structured receives the event itself.
//
// A channel that configured grouping buffers instead of sending: delivery then
// happens after group_wait, so the call returns without an outcome.
func SendNotificationEvent(channel models.NotificationChannel, event NotificationEvent, tags []string) error {
	settings := readGroupSettings(channel)
	if suppressedByRepeat(channel, event, settings.RepeatInterval) {
		slog.Debug("notification skipped, inside repeat interval", "event", event.Event, "channel", channel.Name)
		return nil
	}
	if settings.enabled() {
		bufferEvent(channel, event, settings)
		return nil
	}
	return deliverEvent(channel, event, tags)
}

// deliverEvent is the actual send, shared by the direct and the grouped paths.
func deliverEvent(channel models.NotificationChannel, event NotificationEvent, tags []string) error {
	slog.Info("sending notification", "type", channel.Type, "channel", channel.Name, "title", event.Title)
	switch channel.Type {
	case "slack", "webhook":
		return sendWebhookEvent(channel, event)
	case "jsm":
		return sendJSM(channel, event.Title, event.Message, event.DedupKey, tags)
	case "email":
		return sendEmail(channel, event.Title, event.Message)
	case "whatsapp":
		return sendWhatsApp(channel, event.Title, event.Message)
	default:
		return &UnknownChannelTypeError{Type: channel.Type}
	}
}

// ResolveNotification signals that an incident is resolved. For JSM it closes the
// matching alert (by alias); for every other channel it sends a "[RESOLVED]"
// message (those channels have no concept of closing an alert).
func ResolveNotification(channel models.NotificationChannel, alias, subject, body string) error {
	if channel.Type == "jsm" {
		return closeJSM(channel, alias)
	}
	return SendNotification(channel, subject, body)
}

// sendJSM creates an alert via the Jira Service Management Operations REST API
// (api.atlassian.com/jsm/ops). Auth is the API-integration key sent as
// "GenieKey <key>". The endpoint is global and processes alerts asynchronously
// (HTTP 202 on success).
func sendJSM(channel models.NotificationChannel, title, message, alias string, extraTags []string) error {
	apiKey, _ := channel.Config["api_key"].(string)
	if apiKey == "" {
		return ErrJSMAPIKeyMissing
	}

	url := "https://api.atlassian.com/jsm/ops/integration/v2/alerts"

	// Opsgenie caps "message" at 130 chars; the full text goes in "description".
	alertMsg := title
	if len(alertMsg) > 130 {
		alertMsg = alertMsg[:130]
	}

	// Map our severity (carried in the title prefix "[critical]"/"[warning]") to
	// an Opsgenie priority so on-call routing/escalation behaves correctly.
	priority := "P3"
	lower := strings.ToLower(title)
	if strings.Contains(lower, "critical") {
		priority = "P1"
	} else if strings.Contains(lower, "warning") {
		priority = "P3"
	}

	if alias == "" {
		alias = title
	}
	// Tags: base + per-channel config tags + per-rule routing tags.
	tags := []string{"middle-monitor"}
	if extra, _ := channel.Config["tags"].(string); extra != "" {
		for _, tg := range strings.Split(extra, ",") {
			if tg = strings.TrimSpace(tg); tg != "" {
				tags = append(tags, tg)
			}
		}
	}
	for _, tg := range extraTags {
		if tg = strings.TrimSpace(tg); tg != "" {
			tags = append(tags, tg)
		}
	}

	payload := map[string]interface{}{
		"message":     alertMsg,
		"description": message,
		"priority":    priority,
		"alias":       alias, // stable alias → JSM de-duplicates re-fires AND lets us close it
		"source":      "Middle-Monitor",
		"tags":        tags,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "GenieKey "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// JSM Ops returns 202 Accepted on success; anything >= 300 is a failure.
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1500))
		return &JSMStatusError{Op: "create", StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return nil
}

// closeJSM closes the JSM alert matching the alias. Acknowledging instead would
// conflate "a human is looking at it" with "the condition cleared", and would leave
// the alert open so the next fire deduplicates into it rather than paging again.
// If the alert no longer exists (already manually closed), the 404 is silently ignored.
func closeJSM(channel models.NotificationChannel, alias string) error {
	apiKey, _ := channel.Config["api_key"].(string)
	if apiKey == "" {
		return ErrJSMAPIKeyMissing
	}
	if alias == "" {
		return ErrJSMAliasMissing
	}

	base := fmt.Sprintf("https://api.atlassian.com/jsm/ops/integration/v2/alerts/%s", neturl.QueryEscape(alias))

	closePayload := map[string]interface{}{
		"source": "Middle-Monitor",
		"note":   "Auto-resolved by Middle Monitor: the condition is no longer breached.",
	}
	closeBody, _ := json.Marshal(closePayload)
	req, _ := http.NewRequest("POST", base+"/close?identifierType=alias", bytes.NewBuffer(closeBody))
	req.Header.Set("Authorization", "GenieKey "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 404 means the alert was already manually closed — that's fine.
	if resp.StatusCode == 404 {
		return nil
	}
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1500))
		return &JSMStatusError{Op: "close", StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return nil
}

// sendSMTPWith sends a pre-built HTML body through the given SMTP server.
func sendSMTPWith(host, port, user, pass, from string, to []string, subject, htmlBody string) error {
	var auth smtp.Auth
	if user != "" && pass != "" {
		auth = LoginAuth(user, pass)
	}

	msgBody := fmt.Sprintf("To: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		strings.Join(to, ","), subject, htmlBody)

	if err := smtp.SendMail(fmt.Sprintf("%s:%s", host, port), auth, from, to, []byte(msgBody)); err != nil {
		return fmt.Errorf("%w: %w", ErrSMTPSend, err)
	}
	slog.Info("email sent", "to", to, "subject", subject)
	return nil
}

// SendContactEmail forwards a contact-form message to the platform contact
// address (CONTACT_EMAIL, falling back to SMTP_FROM). No-op when neither is set.
func SendContactEmail(name, email, message string) error {
	to := os.Getenv("CONTACT_EMAIL")
	if to == "" {
		to = os.Getenv("SMTP_FROM")
	}
	if to == "" {
		slog.Info("contact email not forwarded, CONTACT_EMAIL not configured")
		return nil
	}
	subject := fmt.Sprintf("[Contact] %s", name)
	body := fmt.Sprintf(
		`<p><strong>Name:</strong> %s</p><p><strong>Email:</strong> %s</p><p style="white-space:pre-wrap">%s</p>`,
		html.EscapeString(name), html.EscapeString(email), html.EscapeString(message))
	return sendSMTP([]string{to}, subject, body)
}

// sendSMTP is the platform SMTP dispatcher used for transactional emails
// (welcome, verification, password reset, invitations). It reads credentials
// from env vars and falls back to a log line when SMTP is not configured.
func sendSMTP(to []string, subject, htmlBody string) error {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	if host == "" || port == "" {
		slog.Warn("smtp not configured, simulating send", "to", to, "subject", subject)
		return nil
	}
	return sendSMTPWith(host, port, os.Getenv("SMTP_USER"), os.Getenv("SMTP_PASS"), os.Getenv("SMTP_FROM"), to, subject, htmlBody)
}

// alertSeverityMeta returns header color, severity badge label, and status badge
// label+color derived from the notification title.
func alertSeverityMeta(title string) (headerColor, severityLabel, statusLabel, statusColor string) {
	lower := strings.ToLower(title)
	switch {
	case strings.HasPrefix(title, "[RESOLVED]"):
		return "#16a34a", "RESOLVED", "RESOLVED", "#16a34a"
	case strings.Contains(lower, "critical"):
		return "#dc2626", "CRITICAL", "OPEN", "#dc2626"
	case strings.Contains(lower, "warning"):
		return "#d97706", "WARNING", "OPEN", "#d97706"
	default:
		return brandPrimary, "ALERT", "OPEN", brandPrimary
	}
}

// alertTitleDisplay strips the [SEVERITY] prefix from the notification title so
// the card heading shows only the human-readable name.
func alertTitleDisplay(title string) string {
	for _, prefix := range []string{"[RESOLVED] ", "[CRITICAL] ", "[WARNING] ", "[RESOLVED]", "[CRITICAL]", "[WARNING]"} {
		if strings.HasPrefix(title, prefix) {
			return strings.TrimSpace(title[len(prefix):])
		}
	}
	return title
}

// alertMessageToHTML turns the plain-text alert body into styled HTML and
// extracts the deep-link URL (if the message contains one) as the CTA target.
// Lines starting with "http" become the CTA; lines like "Error: X" / "Service: X"
// become a structured info table; remaining lines form the main description.
func alertMessageToHTML(message string) (htmlBody, ctaURL string) {
	var descLines, infoLines []string
	for _, raw := range strings.Split(message, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			ctaURL = line
			continue
		}
		if strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "Service:") || strings.HasPrefix(line, "Host:") {
			infoLines = append(infoLines, line)
		} else {
			descLines = append(descLines, line)
		}
	}

	var sb strings.Builder
	if len(descLines) > 0 {
		sb.WriteString(`<p style="margin:0 0 16px;font-size:14px;color:#374151;line-height:1.7;">`)
		sb.WriteString(strings.Join(descLines, "<br>"))
		sb.WriteString(`</p>`)
	}
	if len(infoLines) > 0 {
		sb.WriteString(`<table cellpadding="0" cellspacing="0" style="width:100%;border-top:1px solid #e5e7eb;margin-top:4px;">`)
		for _, info := range infoLines {
			parts := strings.SplitN(info, ":", 2)
			label := strings.TrimSpace(parts[0])
			value := ""
			if len(parts) > 1 {
				value = strings.TrimSpace(parts[1])
			}
			sb.WriteString(fmt.Sprintf(`<tr>`+
				`<td style="padding:8px 0 8px;font-size:11px;font-weight:700;color:#6b7280;text-transform:uppercase;letter-spacing:0.6px;white-space:nowrap;padding-right:24px;vertical-align:top;">%s</td>`+
				`<td style="padding:8px 0 8px;font-size:14px;color:#111827;">%s</td>`+
				`</tr>`, label, html.EscapeString(value)))
		}
		sb.WriteString(`</table>`)
	}
	return sb.String(), ctaURL
}

// alertEmailHTML builds the full JSM-style HTML email for an alert notification.
func alertEmailHTML(title, message string) string {
	headerColor, severityLabel, statusLabel, statusColor := alertSeverityMeta(title)
	displayTitle := alertTitleDisplay(title)
	bodyHTML, ctaURL := alertMessageToHTML(message)

	ctaBlock := ""
	if ctaURL != "" {
		ctaBlock = fmt.Sprintf(
			`<tr><td style="padding:0 32px 28px;">`+
				`<a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;`+
				`font-size:14px;font-weight:600;padding:12px 28px;border-radius:8px;font-family:-apple-system,sans-serif;">`+
				`View in Middle Monitor &#8594;</a>`+
				`</td></tr>`, ctaURL, brandPrimary)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f0f2f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
<tr><td align="center">
<table width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;overflow:hidden;box-shadow:0 1px 4px rgba(0,0,0,0.12);max-width:560px;width:100%%;">

  <!-- Header -->
  <tr><td style="background:%s;padding:24px 32px;">
    <table cellpadding="0" cellspacing="0">
      <tr><td>
        <span style="display:inline-block;background:rgba(255,255,255,0.25);color:#ffffff;font-size:11px;font-weight:700;padding:3px 10px;border-radius:4px;letter-spacing:0.8px;">%s</span>
      </td></tr>
      <tr><td style="padding-top:10px;">
        <span style="color:#ffffff;font-size:20px;font-weight:700;line-height:1.3;">%s</span>
      </td></tr>
    </table>
  </td></tr>

  <!-- Status -->
  <tr><td style="padding:14px 32px;background:#f9fafb;border-bottom:1px solid #e5e7eb;">
    <span style="font-size:11px;font-weight:700;color:#6b7280;letter-spacing:0.8px;text-transform:uppercase;margin-right:12px;">STATUS</span>
    <span style="background:%s;color:#ffffff;font-size:11px;font-weight:700;padding:3px 10px;border-radius:12px;letter-spacing:0.8px;">%s</span>
  </td></tr>

  <!-- Body -->
  <tr><td style="padding:28px 32px 20px;">
    <p style="margin:0 0 12px;font-size:11px;font-weight:700;color:#6b7280;letter-spacing:0.8px;text-transform:uppercase;">DETAILS</p>
    <div style="background:#f9fafb;border-radius:8px;padding:20px 24px;">%s</div>
  </td></tr>

  <!-- CTA -->
  %s

  <!-- Footer -->
  <tr><td style="padding:16px 32px 20px;border-top:1px solid #f0f2f5;">
    <table cellpadding="0" cellspacing="0" width="100%%">
      <tr>
        <td style="font-size:12px;color:#9ca3af;">Middle Monitor &mdash; %s</td>
        <td align="right" style="font-size:12px;color:#9ca3af;">middlemonitor.io</td>
      </tr>
    </table>
  </td></tr>

</table>
</td></tr>
</table>
</body>
</html>`,
		headerColor, severityLabel, displayTitle,
		statusColor, statusLabel,
		bodyHTML,
		ctaBlock,
		time.Now().UTC().Format("2006-01-02 15:04 UTC"))
}

func sendEmail(channel models.NotificationChannel, title, message string) error {
	emailsStr, ok := channel.Config["emails"].(string)
	if !ok || emailsStr == "" {
		return ErrEmailTargetsMissing
	}
	targets := strings.Split(emailsStr, ",")
	for i := range targets {
		targets[i] = strings.TrimSpace(targets[i])
	}

	// Email alerts go through the organization's own SMTP server when it has one.
	if OrgSMTPProvider == nil {
		return ErrOrgSMTPMissing
	}
	if cfg, ok := OrgSMTPProvider(channel.OrganizationID); ok && cfg.Usable() {
		return sendSMTPWith(cfg.Host, cfg.Port, cfg.User, cfg.Pass, cfg.From, targets, title, alertEmailHTML(title, message))
	}

	// Otherwise the platform server sends them, to the org's verified members
	// only: anyone can sign up, so it must never mail an address they typed.
	var members map[string]bool
	if OrgMemberEmails != nil {
		members = OrgMemberEmails(channel.OrganizationID)
	}
	allowed := memberTargets(targets, members)
	if len(allowed) == 0 {
		slog.Warn("email alert not sent, no smtp and no member recipient", "org_id", channel.OrganizationID, "title", title)
		return ErrOrgSMTPMissing
	}
	if len(allowed) < len(targets) {
		slog.Warn("email alert skipped non-member recipients", "org_id", channel.OrganizationID, "skipped", len(targets)-len(allowed))
	}
	return sendSMTP(allowed, title, alertEmailHTML(title, message))
}

// memberTargets keeps the targets that belong to the organization's members.
func memberTargets(targets []string, members map[string]bool) []string {
	var allowed []string
	for _, t := range targets {
		if members[strings.ToLower(t)] {
			allowed = append(allowed, t)
		}
	}
	return allowed
}

// SendOrgTestEmail sends a test email to `to` using the organization's own SMTP
// configuration. Used by the settings UI to validate the SMTP setup.
func SendOrgTestEmail(orgID int64, to string) error {
	if OrgSMTPProvider == nil {
		return ErrSMTPProviderMissing
	}
	cfg, ok := OrgSMTPProvider(orgID)
	if !ok || !cfg.Usable() {
		return ErrOrgSMTPUnusable
	}
	title := "Middle Monitor — SMTP test"
	body := alertEmailHTML(title, "Your SMTP configuration works. Email alerts will be delivered through this server.")
	return sendSMTPWith(cfg.Host, cfg.Port, cfg.User, cfg.Pass, cfg.From, []string{to}, title, body)
}

// SendWelcomeEmail sends a branded welcome email to a newly registered user.
// It is fire-and-forget safe (errors are logged, not fatal).
func SendWelcomeEmail(toEmail, name, orgName string) error {
	appURL := frontendBaseURL()
	if appURL == "" {
		appURL = "https://middlemonitor.io"
	}

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f0f2f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
<tr><td align="center">
<table width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;overflow:hidden;box-shadow:0 1px 4px rgba(0,0,0,0.12);max-width:560px;width:100%%;">

  <!-- Header -->
  <tr><td style="background:%s;padding:28px 32px;">
    <table cellpadding="0" cellspacing="0">
      <tr><td>
        <span style="display:inline-block;background:rgba(255,255,255,0.2);color:#ffffff;font-size:11px;font-weight:700;padding:3px 10px;border-radius:4px;letter-spacing:0.8px;">WELCOME</span>
      </td></tr>
      <tr><td style="padding-top:10px;">
        <span style="color:#ffffff;font-size:22px;font-weight:700;line-height:1.3;">Your account is ready</span>
      </td></tr>
    </table>
  </td></tr>

  <!-- Greeting -->
  <tr><td style="padding:32px 32px 0;">
    <p style="margin:0;font-size:16px;color:#111827;font-weight:600;">Hi %s,</p>
    <p style="margin:12px 0 0;font-size:14px;color:#374151;line-height:1.7;">
      Welcome to <strong>Middle Monitor</strong>! Your account and organization
      <strong>%s</strong> have been successfully created.
    </p>
  </td></tr>

  <!-- What you can do -->
  <tr><td style="padding:24px 32px 20px;">
    <p style="margin:0 0 12px;font-size:11px;font-weight:700;color:#6b7280;letter-spacing:0.8px;text-transform:uppercase;">WHAT YOU CAN DO</p>
    <div style="background:#f9fafb;border-radius:8px;padding:20px 24px;">
      <table cellpadding="0" cellspacing="0" style="width:100%%;">
        <tr>
          <td style="padding:6px 0;font-size:14px;color:#374151;">&#9679;&nbsp; Monitor services, APIs and infrastructure in real-time</td>
        </tr>
        <tr>
          <td style="padding:6px 0;font-size:14px;color:#374151;">&#9679;&nbsp; Configure alert rules and notification channels (email, Slack, JSM)</td>
        </tr>
        <tr>
          <td style="padding:6px 0;font-size:14px;color:#374151;">&#9679;&nbsp; Track incidents, latency and error rates across your stack</td>
        </tr>
        <tr>
          <td style="padding:6px 0;font-size:14px;color:#374151;">&#9679;&nbsp; Set up downtime windows and maintenance schedules</td>
        </tr>
      </table>
    </div>
  </td></tr>

  <!-- CTA -->
  <tr><td style="padding:0 32px 32px;">
    <a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:12px 28px;border-radius:8px;font-family:-apple-system,sans-serif;">Go to Dashboard &#8594;</a>
  </td></tr>

  <!-- Footer -->
  <tr><td style="padding:16px 32px 20px;border-top:1px solid #f0f2f5;">
    <table cellpadding="0" cellspacing="0" width="100%%">
      <tr>
        <td style="font-size:12px;color:#9ca3af;">Middle Monitor &mdash; %s</td>
        <td align="right" style="font-size:12px;color:#9ca3af;">middlemonitor.io</td>
      </tr>
    </table>
  </td></tr>

</table>
</td></tr>
</table>
</body>
</html>`,
		brandPrimary, name, orgName, appURL, brandPrimary, time.Now().UTC().Format("2006-01-02"))

	subject := fmt.Sprintf("Welcome to Middle Monitor, %s!", name)
	if err := sendSMTP([]string{toEmail}, subject, htmlBody); err != nil {
		slog.Error("failed to send welcome email", "to", toEmail, "error", err)
		return err
	}
	return nil
}

// SendVerificationEmail sends a branded email asking a newly registered user to
// confirm their address. The token is embedded in a link to the frontend
// /verify-email page. Fire-and-forget safe (errors are logged, not fatal).
func SendVerificationEmail(toEmail, name, token string) error {
	appURL := frontendBaseURL()
	if appURL == "" {
		appURL = "https://middlemonitor.io"
	}
	verifyURL := fmt.Sprintf("%s/verify-email?token=%s", appURL, neturl.QueryEscape(token))

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f0f2f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
<tr><td align="center">
<table width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;overflow:hidden;box-shadow:0 1px 4px rgba(0,0,0,0.12);max-width:560px;width:100%%;">

  <!-- Header -->
  <tr><td style="background:%s;padding:28px 32px;">
    <table cellpadding="0" cellspacing="0">
      <tr><td>
        <span style="display:inline-block;background:rgba(255,255,255,0.2);color:#ffffff;font-size:11px;font-weight:700;padding:3px 10px;border-radius:4px;letter-spacing:0.8px;">VERIFY YOUR EMAIL</span>
      </td></tr>
      <tr><td style="padding-top:10px;">
        <span style="color:#ffffff;font-size:22px;font-weight:700;line-height:1.3;">Confirm your email address</span>
      </td></tr>
    </table>
  </td></tr>

  <!-- Greeting -->
  <tr><td style="padding:32px 32px 0;">
    <p style="margin:0;font-size:16px;color:#111827;font-weight:600;">Hi %s,</p>
    <p style="margin:12px 0 0;font-size:14px;color:#374151;line-height:1.7;">
      Thanks for signing up to <strong>Middle Monitor</strong>. Please confirm your email
      address to activate your account and unlock your dashboard.
    </p>
  </td></tr>

  <!-- CTA -->
  <tr><td style="padding:24px 32px 8px;">
    <a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:12px 28px;border-radius:8px;font-family:-apple-system,sans-serif;">Verify my email &#8594;</a>
  </td></tr>

  <!-- Fallback link -->
  <tr><td style="padding:8px 32px 32px;">
    <p style="margin:0;font-size:12px;color:#9ca3af;line-height:1.6;">
      If the button doesn't work, copy and paste this link into your browser:<br>
      <a href="%s" style="color:%s;word-break:break-all;">%s</a>
    </p>
    <p style="margin:12px 0 0;font-size:12px;color:#9ca3af;">This link expires in 24 hours.</p>
  </td></tr>

  <!-- Footer -->
  <tr><td style="padding:16px 32px 20px;border-top:1px solid #f0f2f5;">
    <table cellpadding="0" cellspacing="0" width="100%%">
      <tr>
        <td style="font-size:12px;color:#9ca3af;">Middle Monitor &mdash; %s</td>
        <td align="right" style="font-size:12px;color:#9ca3af;">middlemonitor.io</td>
      </tr>
    </table>
  </td></tr>

</table>
</td></tr>
</table>
</body>
</html>`,
		brandPrimary, name, verifyURL, brandPrimary, verifyURL, brandPrimary, verifyURL,
		time.Now().UTC().Format("2006-01-02"))

	subject := "Confirm your email address — Middle Monitor"
	if err := sendSMTP([]string{toEmail}, subject, htmlBody); err != nil {
		slog.Error("failed to send verification email", "to", toEmail, "error", err)
		return err
	}
	return nil
}

// SendPasswordResetEmail sends a branded email with a link to reset a forgotten
// password. The token is embedded in a link to the frontend /reset-password page,
// where the user chooses a new password. Fire-and-forget safe (errors are logged,
// not fatal).
func SendPasswordResetEmail(toEmail, name, token string) error {
	appURL := frontendBaseURL()
	if appURL == "" {
		appURL = "https://middlemonitor.io"
	}
	resetURL := fmt.Sprintf("%s/reset-password?token=%s", appURL, neturl.QueryEscape(token))

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f0f2f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
<tr><td align="center">
<table width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;overflow:hidden;box-shadow:0 1px 4px rgba(0,0,0,0.12);max-width:560px;width:100%%;">

  <!-- Header -->
  <tr><td style="background:%s;padding:28px 32px;">
    <table cellpadding="0" cellspacing="0">
      <tr><td>
        <span style="display:inline-block;background:rgba(255,255,255,0.2);color:#ffffff;font-size:11px;font-weight:700;padding:3px 10px;border-radius:4px;letter-spacing:0.8px;">PASSWORD RESET</span>
      </td></tr>
      <tr><td style="padding-top:10px;">
        <span style="color:#ffffff;font-size:22px;font-weight:700;line-height:1.3;">Reset your password</span>
      </td></tr>
    </table>
  </td></tr>

  <!-- Greeting -->
  <tr><td style="padding:32px 32px 0;">
    <p style="margin:0;font-size:16px;color:#111827;font-weight:600;">Hi %s,</p>
    <p style="margin:12px 0 0;font-size:14px;color:#374151;line-height:1.7;">
      We received a request to reset the password for your <strong>Middle Monitor</strong>
      account. Click the button below to choose a new one. If you didn't request this,
      you can safely ignore this email &mdash; your password won't change.
    </p>
  </td></tr>

  <!-- CTA -->
  <tr><td style="padding:24px 32px 8px;">
    <a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:12px 28px;border-radius:8px;font-family:-apple-system,sans-serif;">Reset my password &#8594;</a>
  </td></tr>

  <!-- Fallback link -->
  <tr><td style="padding:8px 32px 32px;">
    <p style="margin:0;font-size:12px;color:#9ca3af;line-height:1.6;">
      If the button doesn't work, copy and paste this link into your browser:<br>
      <a href="%s" style="color:%s;word-break:break-all;">%s</a>
    </p>
    <p style="margin:12px 0 0;font-size:12px;color:#9ca3af;">This link expires in 1 hour.</p>
  </td></tr>

  <!-- Footer -->
  <tr><td style="padding:16px 32px 20px;border-top:1px solid #f0f2f5;">
    <table cellpadding="0" cellspacing="0" width="100%%">
      <tr>
        <td style="font-size:12px;color:#9ca3af;">Middle Monitor &mdash; %s</td>
        <td align="right" style="font-size:12px;color:#9ca3af;">middlemonitor.io</td>
      </tr>
    </table>
  </td></tr>

</table>
</td></tr>
</table>
</body>
</html>`,
		brandPrimary, name, resetURL, brandPrimary, resetURL, brandPrimary, resetURL,
		time.Now().UTC().Format("2006-01-02"))

	subject := "Reset your password — Middle Monitor"
	if err := sendSMTP([]string{toEmail}, subject, htmlBody); err != nil {
		slog.Error("failed to send password reset email", "to", toEmail, "error", err)
		return err
	}
	return nil
}

// SendInvitationEmail sends a branded email inviting a teammate to join an
// organization. The token is embedded in a link to the frontend /accept-invite
// page, where the invitee sets their password to activate the account.
// Fire-and-forget safe (errors are logged, not fatal).
func SendInvitationEmail(toEmail, name, orgName, token string) error {
	appURL := frontendBaseURL()
	if appURL == "" {
		appURL = "https://middlemonitor.io"
	}
	acceptURL := fmt.Sprintf("%s/accept-invite?token=%s", appURL, neturl.QueryEscape(token))

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f0f2f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
<tr><td align="center">
<table width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;overflow:hidden;box-shadow:0 1px 4px rgba(0,0,0,0.12);max-width:560px;width:100%%;">

  <!-- Header -->
  <tr><td style="background:%s;padding:28px 32px;">
    <table cellpadding="0" cellspacing="0">
      <tr><td>
        <span style="display:inline-block;background:rgba(255,255,255,0.2);color:#ffffff;font-size:11px;font-weight:700;padding:3px 10px;border-radius:4px;letter-spacing:0.8px;">TEAM INVITATION</span>
      </td></tr>
      <tr><td style="padding-top:10px;">
        <span style="color:#ffffff;font-size:22px;font-weight:700;line-height:1.3;">You've been invited to %s</span>
      </td></tr>
    </table>
  </td></tr>

  <!-- Greeting -->
  <tr><td style="padding:32px 32px 0;">
    <p style="margin:0;font-size:16px;color:#111827;font-weight:600;">Hi %s,</p>
    <p style="margin:12px 0 0;font-size:14px;color:#374151;line-height:1.7;">
      You've been invited to join <strong>%s</strong> on <strong>Middle Monitor</strong>.
      Set your password to activate your account and access the dashboard.
    </p>
  </td></tr>

  <!-- CTA -->
  <tr><td style="padding:24px 32px 8px;">
    <a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:12px 28px;border-radius:8px;font-family:-apple-system,sans-serif;">Accept invitation &#8594;</a>
  </td></tr>

  <!-- Fallback link -->
  <tr><td style="padding:8px 32px 32px;">
    <p style="margin:0;font-size:12px;color:#9ca3af;line-height:1.6;">
      If the button doesn't work, copy and paste this link into your browser:<br>
      <a href="%s" style="color:%s;word-break:break-all;">%s</a>
    </p>
    <p style="margin:12px 0 0;font-size:12px;color:#9ca3af;">This invitation expires in 7 days.</p>
  </td></tr>

  <!-- Footer -->
  <tr><td style="padding:16px 32px 20px;border-top:1px solid #f0f2f5;">
    <table cellpadding="0" cellspacing="0" width="100%%">
      <tr>
        <td style="font-size:12px;color:#9ca3af;">Middle Monitor &mdash; %s</td>
        <td align="right" style="font-size:12px;color:#9ca3af;">middlemonitor.io</td>
      </tr>
    </table>
  </td></tr>

</table>
</td></tr>
</table>
</body>
</html>`,
		brandPrimary, orgName, name, orgName, acceptURL, brandPrimary, acceptURL, brandPrimary, acceptURL,
		time.Now().UTC().Format("2006-01-02"))

	subject := fmt.Sprintf("You've been invited to join %s on Middle Monitor", orgName)
	if err := sendSMTP([]string{toEmail}, subject, htmlBody); err != nil {
		slog.Error("failed to send invitation email", "to", toEmail, "error", err)
		return err
	}
	return nil
}

// SendAddedToOrgEmail notifies an existing user that they have been granted access
// to another organization. No activation is needed (they already have an account),
// so the email simply points them to the dashboard. Fire-and-forget safe.
func SendAddedToOrgEmail(toEmail, name, orgName string) error {
	appURL := frontendBaseURL()
	if appURL == "" {
		appURL = "https://middlemonitor.io"
	}
	loginURL := fmt.Sprintf("%s/login", appURL)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f0f2f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
<tr><td align="center">
<table width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;overflow:hidden;box-shadow:0 1px 4px rgba(0,0,0,0.12);max-width:560px;width:100%%;">

  <!-- Header -->
  <tr><td style="background:%s;padding:28px 32px;">
    <table cellpadding="0" cellspacing="0">
      <tr><td>
        <span style="display:inline-block;background:rgba(255,255,255,0.2);color:#ffffff;font-size:11px;font-weight:700;padding:3px 10px;border-radius:4px;letter-spacing:0.8px;">NEW ACCESS</span>
      </td></tr>
      <tr><td style="padding-top:10px;">
        <span style="color:#ffffff;font-size:22px;font-weight:700;line-height:1.3;">You now have access to %s</span>
      </td></tr>
    </table>
  </td></tr>

  <!-- Greeting -->
  <tr><td style="padding:32px 32px 0;">
    <p style="margin:0;font-size:16px;color:#111827;font-weight:600;">Hi %s,</p>
    <p style="margin:12px 0 0;font-size:14px;color:#374151;line-height:1.7;">
      You've been added to <strong>%s</strong> on <strong>Middle Monitor</strong>.
      Sign in with your existing account and switch to this organization to get started.
    </p>
  </td></tr>

  <!-- CTA -->
  <tr><td style="padding:24px 32px 32px;">
    <a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:12px 28px;border-radius:8px;font-family:-apple-system,sans-serif;">Go to dashboard &#8594;</a>
  </td></tr>

  <!-- Footer -->
  <tr><td style="padding:16px 32px 20px;border-top:1px solid #f0f2f5;">
    <table cellpadding="0" cellspacing="0" width="100%%">
      <tr>
        <td style="font-size:12px;color:#9ca3af;">Middle Monitor &mdash; %s</td>
        <td align="right" style="font-size:12px;color:#9ca3af;">middlemonitor.io</td>
      </tr>
    </table>
  </td></tr>

</table>
</td></tr>
</table>
</body>
</html>`,
		brandPrimary, orgName, name, orgName, loginURL, brandPrimary,
		time.Now().UTC().Format("2006-01-02"))

	subject := fmt.Sprintf("You've been added to %s on Middle Monitor", orgName)
	if err := sendSMTP([]string{toEmail}, subject, htmlBody); err != nil {
		slog.Error("failed to send added-to-org email", "to", toEmail, "error", err)
		return err
	}
	return nil
}

func sendWhatsApp(channel models.NotificationChannel, title, message string) error {
	phoneNumber, ok := channel.Config["phone_number"].(string)
	if !ok || phoneNumber == "" {
		return ErrWhatsAppPhoneMissing
	}
	token, ok := channel.Config["token"].(string)
	if !ok || token == "" {
		return ErrWhatsAppTokenMissing
	}
	// The sender is identified by the Meta Business phone number ID, not by
	// the destination number.
	phoneNumberID, ok := channel.Config["phone_number_id"].(string)
	if !ok || phoneNumberID == "" {
		return ErrWhatsAppPhoneIDMissing
	}

	url := fmt.Sprintf("https://graph.facebook.com/v17.0/%s/messages", neturl.PathEscape(phoneNumberID))

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                phoneNumber,
		"type":              "text",
		"text": map[string]string{
			"body": fmt.Sprintf("*%s*\n%s", title, message),
		},
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &WhatsAppStatusError{StatusCode: resp.StatusCode}
	}

	slog.Info("whatsapp sent", "to", phoneNumber, "title", title)
	return nil
}
