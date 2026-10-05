package workers

import (
	"testing"

	"middle-monitor/backend/models"
)

func sp(s string) *string { return &s }

// The ping check reports the ICMP round-trip, not how long the subprocess took
// to fork and exec. Falling back to wall-clock timing would add milliseconds of
// process overhead to every sample and drift a latency threshold.
func TestParsePingRTTReadsTheICMPRoundTrip(t *testing.T) {
	linux := `PING example.com (93.184.216.34) 56(84) bytes of data.
64 bytes from 93.184.216.34: icmp_seq=1 ttl=56 time=8.12 ms

--- example.com ping statistics ---`
	rtt, ok := parsePingRTT(linux)
	if !ok || rtt != 8.12 {
		t.Fatalf("linux ping: got (%v, %v), want (8.12, true)", rtt, ok)
	}

	// Sub-millisecond replies print "time<1 ms" rather than "time=".
	if rtt, ok := parsePingRTT("64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time<1 ms"); !ok || rtt != 1 {
		t.Fatalf("sub-millisecond reply: got (%v, %v), want (1, true)", rtt, ok)
	}

	// macOS spaces the unit differently.
	if rtt, ok := parsePingRTT("64 bytes from 1.1.1.1: icmp_seq=0 ttl=57 time=14.204 ms"); !ok || rtt != 14.204 {
		t.Fatalf("macos ping: got (%v, %v), want (14.204, true)", rtt, ok)
	}
}

// An unreachable host produces output with no timing at all. Returning ok=false
// is what lets the caller record "no latency" instead of a zero that reads as an
// instant reply.
func TestParsePingRTTReportsWhenThereIsNoTiming(t *testing.T) {
	for _, output := range []string{
		"",
		"ping: cannot resolve nope.invalid: Unknown host",
		"PING x (1.2.3.4): 56 data bytes\nRequest timeout for icmp_seq 0",
	} {
		if rtt, ok := parsePingRTT(output); ok {
			t.Fatalf("%q: got (%v, true), want ok=false", output, rtt)
		}
	}
}

// SNMP credentials are optional and stored as free-form JSON. A missing or
// broken blob has to fall back to the defaults rather than query an empty OID,
// which would make every SNMP check fail after a bad edit.
func TestParseSNMPCredentialsFallsBackToDefaults(t *testing.T) {
	cases := []struct {
		name          string
		credentials   *string
		wantCommunity string
		wantOID       string
	}{
		{"no credentials", nil, defaultSNMPCommunity, sysUptimeOID},
		{"empty credentials", sp(""), defaultSNMPCommunity, sysUptimeOID},
		{"malformed json", sp("{not json"), defaultSNMPCommunity, sysUptimeOID},
		{"empty object", sp("{}"), defaultSNMPCommunity, sysUptimeOID},
		{"blank values are not overrides", sp(`{"community":"","oid":""}`), defaultSNMPCommunity, sysUptimeOID},
		{"community only", sp(`{"community":"secret"}`), "secret", sysUptimeOID},
		{"oid only", sp(`{"oid":"1.3.6.1.2.1.1.5.0"}`), defaultSNMPCommunity, "1.3.6.1.2.1.1.5.0"},
		{"both", sp(`{"community":"secret","oid":"1.3.6.1.4.1.9.2.1.58.0"}`), "secret", "1.3.6.1.4.1.9.2.1.58.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			community, oid := parseSNMPCredentials(c.credentials)
			if community != c.wantCommunity || oid != c.wantOID {
				t.Fatalf("got (%q, %q), want (%q, %q)", community, oid, c.wantCommunity, c.wantOID)
			}
		})
	}
}

// A host stored with its own scheme must keep it: prefixing https:// onto
// "http://legacy.internal" would produce a URL that never resolves.
func TestHostHasExplicitProtocol(t *testing.T) {
	cases := map[string]bool{
		"http://example.com":  true,
		"https://example.com": true,
		"example.com":         false,
		"10.0.0.1":            false,
		"ftp://example.com":   false,
		"":                    false,
	}
	for host, want := range cases {
		if got := hostHasExplicitProtocol(host); got != want {
			t.Fatalf("%q: got %v, want %v", host, got, want)
		}
	}
}

// The fingerprint is what tells the worker a check's schedule must be rebuilt.
// Any field that changes how the check runs has to move it, or an edit saved in
// the UI keeps running with the old configuration until the process restarts.
func TestServiceConfigFingerprintMovesWithEveryRunAffectingField(t *testing.T) {
	base := models.Service{
		OrganizationID:  1,
		Name:            "api",
		Type:            "http",
		Host:            "example.com",
		Service:         "app",
		ServiceInterval: 60,
		MaxAttempts:     3,
	}
	reference := serviceConfigFingerprint(base)

	if serviceConfigFingerprint(base) != reference {
		t.Fatal("the same configuration must produce the same fingerprint")
	}

	mutations := map[string]func(*models.Service){
		"interval":           func(s *models.Service) { s.ServiceInterval = 30 },
		"max attempts":       func(s *models.Service) { s.MaxAttempts = 5 },
		"host":               func(s *models.Service) { s.Host = "other.example.com" },
		"path":               func(s *models.Service) { s.Path = sp("/healthz") },
		"type":               func(s *models.Service) { s.Type = "ping" },
		"credentials":        func(s *models.Service) { s.Credentials = sp(`{"community":"x"}`) },
		"warning threshold":  func(s *models.Service) { s.WarningThreshold = f64(70) },
		"critical threshold": func(s *models.Service) { s.CriticalThreshold = f64(90) },
		"expected status":    func(s *models.Service) { code := 204; s.ExpectedStatusCode = &code },
		"expected body":      func(s *models.Service) { s.ExpectedBodyContains = sp("ok") },
		"body mode":          func(s *models.Service) { s.ExpectedBodyMode = sp("regex") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if serviceConfigFingerprint(changed) == reference {
				t.Fatalf("changing the %s must change the fingerprint", name)
			}
		})
	}
}

// The parts are joined on a unit separator precisely so that moving a character
// from one field to the next cannot produce the same fingerprint.
func TestServiceConfigFingerprintDoesNotConfuseAdjacentFields(t *testing.T) {
	a := models.Service{OrganizationID: 1, Name: "ab", Type: "http", Host: "h", Service: "s"}
	b := models.Service{OrganizationID: 1, Name: "a", Type: "bhttp", Host: "h", Service: "s"}
	if serviceConfigFingerprint(a) == serviceConfigFingerprint(b) {
		t.Fatal("fields must not run into each other")
	}
}

// The engine name comes from a user-edited field. Anything unrecognised has to
// land on a real driver rather than an empty string, which would fail every SQL
// check with a driver error instead of a connection result.
func TestNormalizeSQLEngine(t *testing.T) {
	cases := map[string]string{
		"mysql":      "mysql",
		"MySQL":      "mysql",
		"  mariadb ": "mysql",
		"MARIADB":    "mysql",
		"postgres":   "postgres",
		"postgresql": "postgres",
		"":           "postgres",
		"sqlite":     "postgres",
	}
	for engine, want := range cases {
		if got := normalizeSQLEngine(engine); got != want {
			t.Fatalf("%q: got %q, want %q", engine, got, want)
		}
	}
}
