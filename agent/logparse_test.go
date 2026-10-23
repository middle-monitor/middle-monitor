package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Docker cuts lines at 16 KB; a piece without its newline is not a record yet.
func TestDecodeDockerJSONMarksASplitLine(t *testing.T) {
	head := decodeLine(logSourceDocker, `{"log":"first half ","stream":"stdout","time":"2026-10-11T09:00:00.5Z"}`)
	tail := decodeLine(logSourceDocker, `{"log":"second half\n","stream":"stdout","time":"2026-10-11T09:00:00.6Z"}`)
	if !head.partial || tail.partial {
		t.Fatalf("partial flags: head %v tail %v, want true false", head.partial, tail.partial)
	}
	if tail.text != "second half" || tail.stream != "stdout" {
		t.Errorf("tail = %+v, want the text without its newline and the stream", tail)
	}
	if tail.time.IsZero() {
		t.Error("the runtime timestamp is the record time when the line has none")
	}
}

func TestDecodeCRIReadsStreamAndPartialFlag(t *testing.T) {
	line := decodeLine(logSourceKubernetes, "2026-10-11T09:00:00.123456789Z stderr P connection reset by peer")
	if line.stream != "stderr" || !line.partial || line.text != "connection reset by peer" {
		t.Errorf("got %+v", line)
	}
	// Nodes still on the Docker runtime write json-file under /var/log/pods.
	legacy := decodeLine(logSourceKubernetes, `{"log":"ok\n","stream":"stdout","time":"2026-10-11T09:00:00Z"}`)
	if legacy.text != "ok" {
		t.Errorf("legacy docker line under kubernetes = %+v", legacy)
	}
}

// The Logs view filters on DEBUG/INFO/WARN/ERROR/FATAL: a level spelled any
// other way must still land in one of them, or the ERROR filter misses errors.
func TestDetectSeverity(t *testing.T) {
	cases := []struct {
		name, line, want string
	}{
		{"gitlab json", `{"severity":"ERROR","time":"2026-10-11T09:00:00.000Z","message":"boom"}`, "ERROR"},
		{"pino number", `{"level":50,"msg":"db down"}`, "ERROR"},
		{"python json", `{"levelname":"WARNING","message":"slow"}`, "WARN"},
		{"logfmt", `time=2026-10-11 level=error msg="db down"`, "ERROR"},
		{"keyword", `2026/10/11 09:00:00 [FATAL] out of memory`, "FATAL"},
		{"nginx crit", `2026/10/11 09:00:00 [crit] 12#12: open() failed`, "ERROR"},
		{"error word in prose", `user signed in, no error`, "INFO"},
		{"nothing declared", `GET /health 200`, "INFO"},
	}
	for _, c := range cases {
		if got := detectSeverity(jsonFields(c.line), c.line, "INFO"); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// GitLab's JSON logs carry their own "time"; the record must keep it, not the
// moment the agent read the line.
func TestJSONTimeReadsTheLineTimestamp(t *testing.T) {
	got, ok := jsonTime(jsonFields(`{"time":"2026-10-11T09:00:00.123Z","severity":"INFO"}`))
	want := time.Date(2026, 10, 11, 9, 0, 0, 123e6, time.UTC)
	if !ok || !got.Equal(want) {
		t.Errorf("got %v %v, want %v", got, ok, want)
	}
	if got, ok := jsonTime(jsonFields(`{"ts":1760173200.5}`)); !ok || got.Unix() != 1760173200 {
		t.Errorf("epoch seconds: got %v %v", got, ok)
	}
}

func TestApplyLogRules(t *testing.T) {
	rules := []LogRule{
		{Type: logRuleExclude, Pattern: `GET /health`},
		{Type: logRuleMask, Pattern: `password=\S+`, Replace: "password=[redacted]"},
	}
	if err := prepareLogRules(rules, "test"); err != nil {
		t.Fatal(err)
	}
	if _, keep := applyLogRules(rules, "GET /health 200"); keep {
		t.Error("health checks are excluded")
	}
	got, keep := applyLogRules(rules, "login user=bob password=hunter2")
	if !keep || got != "login user=bob password=[redacted]" {
		t.Errorf("got %q %v: a secret must never leave the host", got, keep)
	}
}

func writeDockerConfig(t *testing.T, dir, name string, labels map[string]string) string {
	t.Helper()
	labelJSON := "{"
	first := true
	for k, v := range labels {
		if !first {
			labelJSON += ","
		}
		labelJSON += `"` + k + `":"` + v + `"`
		first = false
	}
	labelJSON += "}"
	cfg := `{"Name":"/` + name + `","Config":{"Image":"registry.local/api:1.4","Labels":` + labelJSON + `}}`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.v2.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, filepath.Base(dir)+"-json.log")
}

// The service name is what logs are filtered and correlated by: the compose
// service, not the generated container name, and an explicit label over both.
func TestDockerOriginService(t *testing.T) {
	root := t.TempDir()
	src := &LogSource{Type: logSourceDocker}

	compose := writeDockerConfig(t, filepath.Join(root, "aaaaaaaaaaaaaaaa"), "shop-api-1", map[string]string{"com.docker.compose.service": "api"})
	if got := resolveOrigin(src, compose, "web-01", "default"); got.service != "api" || got.attrs["container.name"] != "shop-api-1" {
		t.Errorf("compose container: service %q attrs %v", got.service, got.attrs)
	}

	labelled := writeDockerConfig(t, filepath.Join(root, "bbbbbbbbbbbbbbbb"), "x", map[string]string{"com.docker.compose.service": "api", labelService: "checkout"})
	if got := resolveOrigin(src, labelled, "web-01", "default"); got.service != "checkout" {
		t.Errorf("label must win, got %q", got.service)
	}

	optedOut := writeDockerConfig(t, filepath.Join(root, "cccccccccccccccc"), "noisy", map[string]string{labelLogs: "false"})
	if !resolveOrigin(src, optedOut, "web-01", "default").skip {
		t.Error("a container labelled io.middlemonitor.logs=false must not be collected")
	}
}

func TestKubernetesOriginFromPath(t *testing.T) {
	src := &LogSource{Type: logSourceKubernetes}
	if err := prepareLogSource(src); err != nil {
		t.Fatal(err)
	}
	got := resolveOrigin(src, "/var/log/pods/shop_api-7d9f8b6c5-x2x4z_1f2e3d4c-aaaa/api/0.log", "node-1", "default")
	want := map[string]string{
		"k8s.namespace.name": "shop",
		"k8s.pod.name":       "api-7d9f8b6c5-x2x4z",
		"k8s.pod.uid":        "1f2e3d4c-aaaa",
		"k8s.container.name": "api",
	}
	for k, v := range want {
		if got.attrs[k] != v {
			t.Errorf("%s = %q, want %q", k, got.attrs[k], v)
		}
	}
	if got.service != "api" || got.name != "shop/api-7d9f8b6c5-x2x4z/api" {
		t.Errorf("service %q name %q", got.service, got.name)
	}
}

// kube-system is noise for most teams; exclude must apply to the full
// namespace/pod/container name.
func TestKubernetesExcludeByNamespace(t *testing.T) {
	src := &LogSource{Type: logSourceKubernetes, Exclude: `^kube-system/`}
	if err := prepareLogSource(src); err != nil {
		t.Fatal(err)
	}
	if !resolveOrigin(src, "/var/log/pods/kube-system_coredns-abc_uid/coredns/0.log", "n", "d").skip {
		t.Error("kube-system pod collected despite exclude")
	}
	if resolveOrigin(src, "/var/log/pods/shop_api-abc_uid/api/0.log", "n", "d").skip {
		t.Error("application pod skipped")
	}
}

// --config-check must refuse a broken logs block before a deploy restarts the
// agent on it.
func TestPrepareLogsRejectsBrokenSources(t *testing.T) {
	cases := map[string]LogsConfig{
		"unknown type":    {Sources: []LogSource{{Type: "syslog"}}},
		"two journals":    {Sources: []LogSource{{Type: logSourceJournald, Path: StringOrList{"/a", "/b"}}}},
		"file no path":    {Sources: []LogSource{{Type: logSourceFile}}},
		"bad multiline":   {Sources: []LogSource{{Type: logSourceFile, Path: StringOrList{"/x"}, Multiline: "("}}},
		"bad start_at":    {Sources: []LogSource{{Type: logSourceDocker, StartAt: "middle"}}},
		"bad rule type":   {Rules: []LogRule{{Type: "drop", Pattern: "x"}}},
		"negative rate":   {MaxLinesPerSecond: -1},
		"bad glob":        {Sources: []LogSource{{Type: logSourceFile, Path: StringOrList{"/var/log/["}}}},
		"rule no pattern": {Sources: []LogSource{{Type: logSourceDocker, Rules: []LogRule{{Type: logRuleMask}}}}},
	}
	for name, cfg := range cases {
		cfg := cfg
		if err := PrepareLogs(&cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
