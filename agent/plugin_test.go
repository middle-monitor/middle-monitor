package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writePlugin drops a shell script standing in for a Nagios plugin.
func writePlugin(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("plugins are shell scripts")
	}
	path := filepath.Join(t.TempDir(), "check_fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	return path
}

// The exit code is the plugin's verdict; a wrong mapping turns every warning
// into an outage page, or hides one.
func TestExitCodeGivesTheState(t *testing.T) {
	cases := map[string]string{"0": stateOK, "1": stateWarning, "2": stateCritical, "3": stateUnknown, "4": stateUnknown}
	for code, want := range cases {
		path := writePlugin(t, "echo state; exit "+code)
		got := runPlugin(context.Background(), Command{Path: path}, nil)
		if got.State != want {
			t.Errorf("exit %s: state %q, want %q", code, got.State, want)
		}
	}
}

// A hung NRPE daemon must not freeze the check forever; the platform needs an
// answer each interval to know the check is stuck.
func TestTimeoutIsUnknown(t *testing.T) {
	path := writePlugin(t, "sleep 5")
	started := time.Now()
	got := runPlugin(context.Background(), Command{Path: path, Timeout: 1}, nil)
	if got.State != stateUnknown {
		t.Fatalf("state %q, want unknown", got.State)
	}
	if !strings.Contains(got.Output, "timed out") {
		t.Fatalf("output %q does not say it timed out", got.Output)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("took %v, the timeout did not stop the plugin", time.Since(started))
	}
}

// A missing binary (plugin not yet deployed by Ansible) is a check problem to
// show, not an agent crash.
func TestMissingBinaryIsUnknown(t *testing.T) {
	got := runPlugin(context.Background(), Command{Path: "/nonexistent/check_nothing"}, nil)
	if got.State != stateUnknown || got.Output == "" {
		t.Fatalf("got %+v, want an unknown state with a reason", got)
	}
}

// Values from the platform must reach the plugin as single arguments: a
// shell would turn "; rm -rf /" into a second command.
func TestVariablesAreNotInterpretedByAShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	path := writePlugin(t, `printf '%s\n' "$1"; exit 0`)
	got := runPlugin(context.Background(), Command{Path: path, Args: []string{"$host$"}},
		map[string][]string{"host": {"x; touch " + marker}})
	if got.Output != "x; touch "+marker {
		t.Fatalf("plugin saw %q, want the value as one argument", got.Output)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the value was executed")
	}
}

// The agent's environment holds its API key; a custom script must not read it.
func TestPluginDoesNotInheritAgentEnvironment(t *testing.T) {
	t.Setenv("MIDDLE_MONITOR_API_KEY", "secret")
	path := writePlugin(t, `echo "key=$MIDDLE_MONITOR_API_KEY"`)
	got := runPlugin(context.Background(), Command{Path: path}, nil)
	if got.Output != "key=" {
		t.Fatalf("plugin read %q from the agent environment", got.Output)
	}
}

// Plugins that only write to stderr would otherwise report an empty message.
func TestStderrIsUsedWhenStdoutIsEmpty(t *testing.T) {
	path := writePlugin(t, "echo 'CRITICAL - socket refused' >&2; exit 2")
	got := runPlugin(context.Background(), Command{Path: path}, nil)
	if got.Output != "CRITICAL - socket refused" {
		t.Fatalf("output %q", got.Output)
	}
}

func TestOutputSplitsMessageLongOutputAndPerfdata(t *testing.T) {
	output := "DISK OK - free space: / 3326 MB | /=2643MB;5948;5958;0;5968\n" +
		"/ 15272 MB (77%);\n" +
		"/boot 68 MB (69%); | /boot=68MB;88;93;0;98\n" +
		"/home=69357MB;253404;253409;0;253414\n"
	message, long, perf := parsePluginOutput(output)

	if message != "DISK OK - free space: / 3326 MB" {
		t.Errorf("message %q", message)
	}
	if long != "/ 15272 MB (77%);\n/boot 68 MB (69%);" {
		t.Errorf("long output %q", long)
	}
	labels := []string{}
	for _, datum := range perf {
		labels = append(labels, datum.Label)
	}
	if strings.Join(labels, ",") != "/,/boot,/home" {
		t.Fatalf("perfdata labels %v, want the first line and every line after the second '|'", labels)
	}
}

func TestPerfdataFields(t *testing.T) {
	perf := parsePerfdata("'used space'=3.5GB;@10:20;~:30;0;100 rta=0.25ms;;; pl=U time=1.5e3ms")
	if len(perf) != 3 {
		t.Fatalf("got %d entries, want 3 (U is skipped): %+v", len(perf), perf)
	}
	used := perf[0]
	if used.Label != "used space" || used.Value != 3.5 || used.Unit != "GB" {
		t.Errorf("quoted label entry %+v", used)
	}
	if used.Warn != "@10:20" || used.Crit != "~:30" {
		t.Errorf("thresholds %q %q, want the ranges as written", used.Warn, used.Crit)
	}
	if used.Min == nil || *used.Min != 0 || used.Max == nil || *used.Max != 100 {
		t.Errorf("min/max %v %v", used.Min, used.Max)
	}
	if perf[1].Label != "rta" || perf[1].Min != nil {
		t.Errorf("empty fields must stay empty: %+v", perf[1])
	}
	if perf[2].Value != 1500 || perf[2].Unit != "ms" {
		t.Errorf("exponent value %+v", perf[2])
	}
}

// A custom script printing junk after '|' must still report its state.
func TestMalformedPerfdataIsSkipped(t *testing.T) {
	_, _, perf := parsePluginOutput("OK | garbage =oops ok=1")
	if len(perf) != 1 || perf[0].Label != "ok" {
		t.Fatalf("perfdata %+v, want only the valid entry", perf)
	}
}

func TestOutputIsCapped(t *testing.T) {
	path := writePlugin(t, "head -c 200000 /dev/zero | tr '\\0' 'a'")
	got := runPlugin(context.Background(), Command{Path: path}, nil)
	if len(got.Output) > pluginOutputLimit {
		t.Fatalf("output is %d bytes, over the %d limit", len(got.Output), pluginOutputLimit)
	}
}
