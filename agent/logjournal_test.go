package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeJournalctl stands in for journalctl: it records its arguments, prints the
// entries file, then follows forever like the real one.
func fakeJournalctl(t *testing.T) (entries, args string) {
	t.Helper()
	dir := t.TempDir()
	entries = filepath.Join(dir, "entries.json")
	args = filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "journalctl")
	body := "#!/bin/sh\necho \"$@\" >> " + args + "\ncat " + entries + "\nexec sleep 60\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entries, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	previous := journalctlPath
	journalctlPath = script
	t.Cleanup(func() { journalctlPath = previous })
	return entries, args
}

func writeEntries(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Services run by systemd log to the journal, not to a file: each entry must
// reach the Logs view under its unit, with the level syslog gave it unless the
// line itself declares one.
func TestJournaldEntriesBecomeRecords(t *testing.T) {
	entries, _ := fakeJournalctl(t)
	writeEntries(t, entries,
		`{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1760000000000000","_SYSTEMD_UNIT":"shop.service","PRIORITY":"3","MESSAGE":"payment failed"}`,
		`{"__CURSOR":"c2","_SYSTEMD_UNIT":"shop.service","PRIORITY":"6","MESSAGE":"level=warn msg=slow"}`,
		`{"__CURSOR":"c3","SYSLOG_IDENTIFIER":"kernel","PRIORITY":"6","MESSAGE":[99,97,102,233]}`,
		`{"__CURSOR":"c4","_SYSTEMD_UNIT":"ssh.service","PRIORITY":"6","MESSAGE":"noise"}`,
	)
	sink, srv := newLogSink(t)
	dir := t.TempDir()
	startCollector(t, logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceJournald, Exclude: `^ssh\.service$`}))

	sink.waitFor(t, 3)
	time.Sleep(200 * time.Millisecond)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.logs) != 3 {
		t.Fatalf("got %+v, the excluded unit must not be sent", sink.logs)
	}
	want := []receivedLog{
		{body: "payment failed", severity: "ERROR", service: "shop"},
		{body: "level=warn msg=slow", severity: "WARN", service: "shop"},
		{body: "caf�", severity: "INFO", service: "kernel"},
	}
	for i, w := range want {
		got := sink.logs[i]
		if got.body != w.body || got.severity != w.severity || got.service != w.service || got.host != "web-01" {
			t.Errorf("record %d = %+v, want %+v", i, got, w)
		}
	}
	if sink.logs[0].resource["systemd.unit"] != "shop.service" {
		t.Errorf("resource = %v, the unit is what the search filters on", sink.logs[0].resource)
	}
}

// Turning journald on must not ship the whole journal, and a restart must
// resume after the last entry the backend accepted rather than skip or resend.
func TestJournaldResumesAfterTheAcceptedCursor(t *testing.T) {
	entries, args := fakeJournalctl(t)
	sink, srv := newLogSink(t)
	dir := t.TempDir()
	cfg := logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceJournald})

	writeEntries(t, entries, `{"__CURSOR":"s=1;i=10","_SYSTEMD_UNIT":"shop.service","MESSAGE":"first"}`)
	c := startCollector(t, cfg)
	sink.waitFor(t, 1)
	c.Stop()

	writeEntries(t, entries, `{"__CURSOR":"s=1;i=11","_SYSTEMD_UNIT":"shop.service","MESSAGE":"second"}`)
	startCollector(t, cfg)
	sink.waitFor(t, 2)

	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(runs) != 2 {
		t.Fatalf("journalctl ran with %q", runs)
	}
	if !strings.Contains(runs[0], "--lines=0") {
		t.Errorf("first run %q would ship the journal's history", runs[0])
	}
	if !strings.Contains(runs[1], "--after-cursor=s=1;i=10") {
		t.Errorf("second run %q does not resume after the accepted entry", runs[1])
	}
}
