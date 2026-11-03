package workers

import (
	"context"
	"net"
	"testing"

	"middle-monitor/backend/models"
)

func f64ptr(v float64) *float64 { return &v }

// An open port is the whole point of the check: success, with a latency the
// threshold evaluator can work on.
func TestTCPCheckSucceedsOnAnOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	result := executeTCPService(models.Service{ID: 1, Host: ln.Addr().String()})
	if result.Status != "success" {
		t.Fatalf("status %q, message %v", result.Status, *result.Message)
	}
	if result.Latency == nil {
		t.Fatal("a successful connect must carry its latency")
	}
}

// A closed port is the outage this check exists to catch.
func TestTCPCheckFailsOnAClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	result := executeTCPService(models.Service{ID: 1, Host: addr})
	if result.Status != "failure" {
		t.Fatalf("status %q on a closed port", result.Status)
	}
}

// Slow answers alert like ping does: above critical is a failure (it opens an
// incident), above warning only a warning.
func TestApplyLatencyThresholds(t *testing.T) {
	svc := models.Service{WarningThreshold: f64ptr(300), CriticalThreshold: f64ptr(1000)}
	cases := []struct {
		latency float64
		want    string
	}{
		{50, "success"},
		{300, "success"},
		{301, "warning"},
		{1001, "failure"},
	}
	for _, c := range cases {
		var result models.ServiceResult
		applyLatencyThresholds(&result, svc, "TCP connect", c.latency, "ok")
		if result.Status != c.want {
			t.Fatalf("latency %.0f: got %q, want %q", c.latency, result.Status, c.want)
		}
	}

	legacy := models.Service{FailureThreshold: f64ptr(100)}
	var result models.ServiceResult
	applyLatencyThresholds(&result, legacy, "TCP connect", 150, "ok")
	if result.Status != "failure" {
		t.Fatalf("legacy failure_threshold ignored: got %q", result.Status)
	}
}

// Resolvers answer with a trailing dot and arbitrary case; a user types
// neither, and a mismatch on those alone would be a false outage.
func TestDNSAnswersContainIgnoresCaseAndTrailingDot(t *testing.T) {
	answers := []string{"Mail.Example.com.", "backup.example.com."}
	if !dnsAnswersContain(answers, "mail.example.com") {
		t.Fatal("fully qualified answer did not match")
	}
	if dnsAnswersContain(answers, "evil.example.net") {
		t.Fatal("an unexpected value matched")
	}
}

func TestLookupDNSResolvesLocalhost(t *testing.T) {
	answers, err := lookupDNS(context.Background(), net.DefaultResolver, "A", "localhost")
	if err != nil {
		t.Fatal(err)
	}
	if !dnsAnswersContain(answers, "127.0.0.1") {
		t.Fatalf("got %v", answers)
	}
}

// The expected value is what turns a dns check into hijack detection: a
// record that resolves somewhere else must fail.
func TestDNSCheckFailsWhenTheAnswerIsNotTheExpectedOne(t *testing.T) {
	cfg := `{"record_type":"A","expected":"10.9.8.7"}`
	result := executeDNSService(models.Service{ID: 1, Host: "localhost", Credentials: &cfg})
	if result.Status != "failure" {
		t.Fatalf("status %q, message %v", result.Status, *result.Message)
	}

	cfg = `{"record_type":"A","expected":"127.0.0.1"}`
	result = executeDNSService(models.Service{ID: 1, Host: "localhost", Credentials: &cfg})
	if result.Status != "success" {
		t.Fatalf("status %q, message %v", result.Status, *result.Message)
	}
}
