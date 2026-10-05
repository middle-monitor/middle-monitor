package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The contact form is public and unauthenticated: it accepts only a complete
// message, and an SMTP outage must not lose one.
func TestContactFormRequiresACompleteMessage(t *testing.T) {
	cases := map[string]string{
		"not json":      `{`,
		"missing name":  `{"email":"a@b.c","message":"hello"}`,
		"missing email": `{"name":"A","message":"hello"}`,
		"empty message": `{"name":"A","email":"a@b.c","message":""}`,
	}
	for name, body := range cases {
		rec := httptest.NewRecorder()
		handleContactUs()(rec, httptest.NewRequest("POST", "/api/v1/contact", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// The message is logged before it is forwarded, so a failing relay still
// answers 200 rather than losing the lead.
func TestContactFormAcceptsAMessageEvenWhenTheRelayFails(t *testing.T) {
	t.Setenv("CONTACT_EMAIL", "")
	rec := httptest.NewRecorder()
	body := `{"name":"A","email":"a@b.c","message":"hello"}`
	handleContactUs()(rec, httptest.NewRequest("POST", "/api/v1/contact", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "success") {
		t.Fatalf("body %q", rec.Body.String())
	}
}
