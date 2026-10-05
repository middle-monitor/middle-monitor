package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// captureAuth serves one series and records what the agent presented.
func captureAuth(t *testing.T, seen *http.Header) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Clone()
		w.Write([]byte("up 1\n"))
	}))
	t.Cleanup(server.Close)
	return server
}

func scrapeWithTarget(t *testing.T, target ScrapeTarget) error {
	t.Helper()
	client, err := clientFor(ScrapeConfig{}, target)
	if err != nil {
		return err
	}
	_, err = fetchTarget(context.Background(), client, target)
	return err
}

// Nomad and several system exporters require a token by default: without one
// the endpoint cannot be read at all.
func TestBearerTokenIsSent(t *testing.T) {
	var seen http.Header
	server := captureAuth(t, &seen)

	if err := scrapeWithTarget(t, ScrapeTarget{URL: server.URL, BearerToken: "s3cret"}); err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if got := seen.Get("Authorization"); got != "Bearer s3cret" {
		t.Fatalf("Authorization %q, want Bearer s3cret", got)
	}
}

// A deployment drops the token in a 0600 file rather than inlining it in a
// world-readable config, and the file ends with a newline it must not send.
func TestBearerTokenFileIsReadAndTrimmed(t *testing.T) {
	var seen http.Header
	server := captureAuth(t, &seen)

	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	if err := scrapeWithTarget(t, ScrapeTarget{URL: server.URL, BearerTokenFile: path}); err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if got := seen.Get("Authorization"); got != "Bearer from-file" {
		t.Fatalf("Authorization %q, want Bearer from-file", got)
	}
}

// The file is re-read at every scrape, so a rotated token is picked up without
// restarting the agent — a restart would drop the running scrape cycles.
func TestBearerTokenFileIsRereadOnRotation(t *testing.T) {
	var seen http.Header
	server := captureAuth(t, &seen)

	path := filepath.Join(t.TempDir(), "token")
	os.WriteFile(path, []byte("first"), 0o600)
	target := ScrapeTarget{URL: server.URL, BearerTokenFile: path}

	if err := scrapeWithTarget(t, target); err != nil {
		t.Fatalf("first scrape: %v", err)
	}
	os.WriteFile(path, []byte("rotated"), 0o600)
	if err := scrapeWithTarget(t, target); err != nil {
		t.Fatalf("second scrape: %v", err)
	}

	if got := seen.Get("Authorization"); got != "Bearer rotated" {
		t.Fatalf("Authorization %q, want the rotated token", got)
	}
}

func TestBasicAuthIsSent(t *testing.T) {
	var seen http.Header
	server := captureAuth(t, &seen)

	target := ScrapeTarget{URL: server.URL, BasicAuth: &BasicAuth{Username: "ops", Password: "pw"}}
	if err := scrapeWithTarget(t, target); err != nil {
		t.Fatalf("scrape: %v", err)
	}

	request := httptest.NewRequest("GET", server.URL, nil)
	request.Header = seen
	user, password, ok := request.BasicAuth()
	if !ok || user != "ops" || password != "pw" {
		t.Fatalf("basic auth %q/%q ok=%v", user, password, ok)
	}
}

// A receiver behind a gateway needs a header the scraper does not know about.
func TestCustomHeadersAreSent(t *testing.T) {
	var seen http.Header
	server := captureAuth(t, &seen)

	target := ScrapeTarget{URL: server.URL, Headers: map[string]string{"X-Scope-OrgID": "demo"}}
	if err := scrapeWithTarget(t, target); err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if got := seen.Get("X-Scope-OrgID"); got != "demo" {
		t.Fatalf("X-Scope-OrgID %q, want demo", got)
	}
}

// Expanding ${VAR} lets a systemd unit hold the secret so it never lands on
// disk in a readable config file.
func TestCredentialsExpandEnvironmentVariables(t *testing.T) {
	var seen http.Header
	server := captureAuth(t, &seen)
	t.Setenv("NOMAD_TOKEN", "from-env")

	target := ScrapeTarget{URL: server.URL, BearerToken: "${NOMAD_TOKEN}"}
	if err := scrapeWithTarget(t, target); err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if got := seen.Get("Authorization"); got != "Bearer from-env" {
		t.Fatalf("Authorization %q, want the expanded value", got)
	}
}

// A metric value must never go through the expansion: only credential fields do.
func TestExpandEnvLeavesPlainValuesAlone(t *testing.T) {
	if got := expandEnv("$notavar and {braces}"); got != "$notavar and {braces}" {
		t.Fatalf("expandEnv changed a plain value: %q", got)
	}
}

// Internal exporters routinely sit behind a self-signed certificate; without
// this the scrape fails at the handshake and no data is ever read.
func TestInsecureSkipVerifyAllowsASelfSignedTarget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer server.Close()

	if err := scrapeWithTarget(t, ScrapeTarget{URL: server.URL}); err == nil {
		t.Fatal("expected the handshake to fail without tls_config")
	}

	target := ScrapeTarget{URL: server.URL, TLSConfig: &TLSConfig{InsecureSkipVerify: true}}
	if err := scrapeWithTarget(t, target); err != nil {
		t.Fatalf("scrape with insecure_skip_verify: %v", err)
	}
}

// Half a key pair is a config mistake that would otherwise surface as an opaque
// handshake failure every interval.
func TestClientCertificateNeedsBothHalves(t *testing.T) {
	_, err := clientFor(ScrapeConfig{}, ScrapeTarget{
		URL:       "https://example.invalid/metrics",
		TLSConfig: &TLSConfig{CertFile: "/tmp/cert.pem"},
	})
	if err != errTLSKeyPairIncomplete {
		t.Fatalf("error %v, want errTLSKeyPairIncomplete", err)
	}
}

// An unreadable CA has to be a config error, not a scrape that fails forever.
func TestUnreadableCAIsAConfigError(t *testing.T) {
	target := ScrapeTarget{URL: "https://example.invalid/metrics", TLSConfig: &TLSConfig{CAFile: "/does/not/exist"}}
	if err := validateTarget(ScrapeConfig{}, target); err == nil {
		t.Fatal("expected a config error for a missing ca_file")
	}
}

// A target that cannot build its client must not silently run a loop that fails
// every interval.
func TestTargetLoopStopsOnABrokenClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		runTargetLoop(ctx, ScrapeConfig{}, ScrapeTarget{
			Name:      "broken",
			URL:       "https://example.invalid/metrics",
			TLSConfig: &TLSConfig{CertFile: "/tmp/cert.pem"},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("the loop kept running with a broken client")
	}
}

// The auth block is written in YAML, so its shape is part of the contract.
func TestAuthFieldsParseFromYAML(t *testing.T) {
	var cfg ScrapeConfig
	err := yaml.Unmarshal([]byte(`
targets:
  - name: nomad
    url: https://127.0.0.1:4646/v1/metrics
    bearer_token_file: /etc/middle-monitor/nomad.token
    headers:
      X-Scope-OrgID: demo
    basic_auth:
      username: ops
      password: pw
    tls_config:
      insecure_skip_verify: true
      ca_file: /etc/ssl/internal.pem
`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	target := cfg.Targets[0]
	if target.BearerTokenFile == "" || target.Headers["X-Scope-OrgID"] != "demo" {
		t.Fatalf("auth fields not parsed: %+v", target)
	}
	if target.BasicAuth == nil || target.BasicAuth.Username != "ops" {
		t.Fatalf("basic_auth not parsed: %+v", target.BasicAuth)
	}
	if target.TLSConfig == nil || !target.TLSConfig.InsecureSkipVerify || !strings.HasSuffix(target.TLSConfig.CAFile, "internal.pem") {
		t.Fatalf("tls_config not parsed: %+v", target.TLSConfig)
	}
}
