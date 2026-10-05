package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func targetNames(targets []ScrapeTarget) string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.Name)
	}
	return strings.Join(names, ",")
}

// The reason fragments exist: each deployment role installs its exporter and
// drops the matching scrape file, instead of every role editing one shared file.
func TestIncludeMergesFragments(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "10-node.yaml", "- name: node\n  url: http://localhost:9100/metrics\n")
	// Both shapes are accepted: roles write one or the other and neither is wrong.
	writeFile(t, dir, "20-adguard.yaml", "targets:\n  - name: adguard\n    url: http://localhost:9618/metrics\n")

	cfg := ScrapeConfig{
		Enabled: true,
		Targets: []ScrapeTarget{{Name: "app", URL: "http://localhost:8080/metrics"}},
		Include: StringOrList{filepath.Join(dir, "*.yaml")},
	}
	if err := PrepareScrape(&cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if got := targetNames(cfg.Targets); got != "app,node,adguard" {
		t.Fatalf("targets %q, want app,node,adguard", got)
	}
}

// A fragment must not silently replace a target written by hand in the main
// file: that is exactly the collision fragments are meant to avoid.
func TestDuplicateNameKeepsTheMainConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "node.yaml", "- name: node\n  url: http://from-fragment:9100/metrics\n")

	cfg := ScrapeConfig{
		Targets: []ScrapeTarget{{Name: "node", URL: "http://from-main:9100/metrics"}},
		Include: StringOrList{filepath.Join(dir, "*.yaml")},
	}
	if err := PrepareScrape(&cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if len(cfg.Targets) != 1 || !strings.Contains(cfg.Targets[0].URL, "from-main") {
		t.Fatalf("the fragment won: %+v", cfg.Targets)
	}
}

// The agent is installed before the roles that drop their fragments, so an
// empty directory must not stop it from starting.
func TestIncludeMatchingNothingIsNotAnError(t *testing.T) {
	cfg := ScrapeConfig{Include: StringOrList{filepath.Join(t.TempDir(), "*.yaml")}}
	if err := PrepareScrape(&cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
}

// A broken fragment must name the file: the operator has to know which of the
// twenty files a role dropped is the bad one.
func TestBrokenFragmentNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.yaml", "name: node\n  url: [[[\n")

	cfg := ScrapeConfig{Include: StringOrList{filepath.Join(dir, "*.yaml")}}
	err := PrepareScrape(&cfg)
	if err == nil {
		t.Fatal("expected an error for a broken fragment")
	}
	if !strings.Contains(err.Error(), "bad.yaml") {
		t.Fatalf("error does not name the file: %v", err)
	}
}

// Global labels save repeating cluster/namespace on every target, and a target
// that sets one of them means it deliberately.
func TestGlobalLabels(t *testing.T) {
	cfg := ScrapeConfig{
		Labels: map[string]string{"cluster": "prod", "namespace": "default"},
		Targets: []ScrapeTarget{
			{Name: "node", URL: "http://localhost:9100/metrics"},
			{Name: "app", URL: "http://localhost:8080/metrics", Labels: map[string]string{"namespace": "billing"}},
		},
	}
	if err := PrepareScrape(&cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if cfg.Targets[0].Labels["cluster"] != "prod" || cfg.Targets[0].Labels["namespace"] != "default" {
		t.Fatalf("global labels missing: %+v", cfg.Targets[0].Labels)
	}
	if cfg.Targets[1].Labels["namespace"] != "billing" {
		t.Fatalf("global label overrode the target's own: %+v", cfg.Targets[1].Labels)
	}
	if cfg.Targets[1].Labels["cluster"] != "prod" {
		t.Fatalf("target lost the global label it did not set: %+v", cfg.Targets[1].Labels)
	}
}

// Discovered targets belong to the same agent, so they carry the same global
// labels as the static ones.
func TestGlobalLabelsReachDiscoveredTargets(t *testing.T) {
	cfg := ScrapeConfig{Labels: map[string]string{"cluster": "prod"}}
	decorated := decorateDiscovered(cfg, []ScrapeTarget{{Name: "svc", Labels: map[string]string{"instance": "a:1"}}})

	if decorated[0].Labels["cluster"] != "prod" || decorated[0].Labels["instance"] != "a:1" {
		t.Fatalf("labels %+v", decorated[0].Labels)
	}
}

// Parameter expansion happens at load, so the manager only ever sees real URLs.
func TestPrepareExpandsParams(t *testing.T) {
	cfg := ScrapeConfig{Targets: []ScrapeTarget{{
		Name:   "dns",
		URL:    "http://127.0.0.1:15353/query",
		Params: ParamValues{"module": {"a", "b"}},
	}}}
	if err := PrepareScrape(&cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if len(cfg.Targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(cfg.Targets))
	}
}

// What --config-check is for: a config that would not work must fail the load,
// so a deployment can refuse to restart the agent on it.
func TestValidationRejectsBrokenConfigs(t *testing.T) {
	cases := map[string]ScrapeConfig{
		"no url":         {Targets: []ScrapeTarget{{Name: "t"}}},
		"file url":       {Targets: []ScrapeTarget{{Name: "t", URL: "file:///etc/passwd"}}},
		"bad regex":      {Targets: []ScrapeTarget{{Name: "t", URL: "http://x/metrics", DropMetrics: []string{"("}}}},
		"bad nomad re":   {Nomad: NomadConfigs{{Address: "http://x", Service: "("}}},
		"http_sd no url": {HTTPSD: []HTTPSDConfig{{}}},
	}

	for name, cfg := range cases {
		copied := cfg
		if err := PrepareScrape(&copied); err == nil {
			t.Fatalf("%s: expected a config error", name)
		}
	}
}

// A valid config must pass, or --config-check is worthless as a gate.
func TestValidationAcceptsAWorkingConfig(t *testing.T) {
	cfg := ScrapeConfig{
		Enabled: true,
		Targets: []ScrapeTarget{{
			Name:        "nomad",
			URL:         "https://127.0.0.1:4646/v1/metrics",
			Params:      ParamValues{"format": {"prometheus"}},
			KeepMetrics: []string{"^nomad_.*"},
			TLSConfig:   &TLSConfig{InsecureSkipVerify: true},
		}},
	}
	if err := PrepareScrape(&cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
}

// loadConfig is what --config-check calls, so the whole file has to go through
// the same path a real start would take.
func TestLoadConfigRunsTheScrapeValidation(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "good.yaml", `
api:
  url: https://api.middlemonitor.io
scrape:
  enabled: true
  targets:
    - name: node
      url: http://localhost:9100/metrics
      keep_metrics: ['^node_.*']
`)
	if _, err := loadConfig(good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	bad := writeFile(t, dir, "bad.yaml", `
api:
  url: https://api.middlemonitor.io
scrape:
  enabled: true
  targets:
    - name: node
      url: http://localhost:9100/metrics
      keep_metrics: ['(']
`)
	if _, err := loadConfig(bad); err == nil {
		t.Fatal("expected loadConfig to reject a broken regex")
	}
}

// include accepts one path or several, because both get written in practice.
func TestIncludeAcceptsScalarAndList(t *testing.T) {
	var single ScrapeConfig
	if err := yaml.Unmarshal([]byte("include: /etc/middle-monitor/scrape.d/*.yaml\n"), &single); err != nil {
		t.Fatalf("scalar include: %v", err)
	}
	if len(single.Include) != 1 {
		t.Fatalf("scalar include parsed as %+v", single.Include)
	}

	var many ScrapeConfig
	if err := yaml.Unmarshal([]byte("include: [a.yaml, b.yaml]\n"), &many); err != nil {
		t.Fatalf("list include: %v", err)
	}
	if len(many.Include) != 2 {
		t.Fatalf("list include parsed as %+v", many.Include)
	}
}

// The example config documents every metric as on by default, so naming one
// must not silently switch the others off; only an explicit false does.
func TestMetricsDefaultOnUnlessExplicitlyDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("api:\n  url: http://localhost\nmetrics:\n  cpu: true\n  disk: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := config.Metrics
	if !m.CPU || !m.RAM || m.Disk || !m.Network {
		t.Fatalf("got cpu=%v ram=%v disk=%v network=%v, want only disk off", m.CPU, m.RAM, m.Disk, m.Network)
	}
}
