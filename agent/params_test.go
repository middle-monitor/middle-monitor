package main

import (
	"net/url"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func expand(t *testing.T, target ScrapeTarget) []ScrapeTarget {
	t.Helper()
	expanded, err := expandTarget(target)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	return expanded
}

func queryOf(t *testing.T, raw string) url.Values {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed.Query()
}

// The script_exporter case: a prober is asked what to probe in the URL, so
// without a query string the exporter cannot be used at all.
func TestFixedParamGoesIntoTheQueryString(t *testing.T) {
	expanded := expand(t, ScrapeTarget{
		Name:   "speedtest",
		URL:    "http://127.0.0.1:9469/probe",
		Params: ParamValues{"script": {"speedtest"}},
	})

	if len(expanded) != 1 {
		t.Fatalf("got %d targets, want 1", len(expanded))
	}
	if got := queryOf(t, expanded[0].URL).Get("script"); got != "speedtest" {
		t.Fatalf("script=%q, want speedtest", got)
	}
	// A single combination keeps the configured name: nothing to disambiguate.
	if expanded[0].Name != "speedtest" {
		t.Fatalf("name %q, want speedtest", expanded[0].Name)
	}
}

// The dns_exporter case: 4 modules crossed with 3 domains is 12 scrapes, and
// each one needs its own URL or the manager would run a single loop.
func TestMatrixExpandsIntoOneTargetPerCombination(t *testing.T) {
	expanded := expand(t, ScrapeTarget{
		Name:         "dns_query",
		URL:          "http://127.0.0.1:15353/query",
		Params:       ParamValues{"module": {"custom_dns1", "custom_dns2", "cloudflare_dns1", "quad9_dns1"}},
		ParamsMatrix: ParamValues{"query_name": {"gmail.com", "outlook.com", "google.com"}},
	})

	if len(expanded) != 12 {
		t.Fatalf("got %d targets, want 12", len(expanded))
	}

	urls := map[string]bool{}
	for _, target := range expanded {
		if urls[target.URL] {
			t.Fatalf("duplicate url %q: the scrape manager keys targets by url", target.URL)
		}
		urls[target.URL] = true
	}
}

// Twelve identical series would be indistinguishable once ingested, so the
// values that produced a target are attached to it as labels.
func TestParamValuesBecomeLabels(t *testing.T) {
	expanded := expand(t, ScrapeTarget{
		Name:   "dns_query",
		URL:    "http://127.0.0.1:15353/query",
		Params: ParamValues{"module": {"a", "b"}, "query_name": {"gmail.com"}},
	})

	seen := []string{}
	for _, target := range expanded {
		if target.Labels["query_name"] != "gmail.com" {
			t.Fatalf("fixed param missing from labels: %+v", target.Labels)
		}
		seen = append(seen, target.Labels["module"])
	}
	sort.Strings(seen)
	if strings.Join(seen, ",") != "a,b" {
		t.Fatalf("module labels %v, want a and b", seen)
	}
}

// Only the axis that varies belongs in the name: a fixed parameter would just
// make every target name longer.
func TestOnlyVaryingValuesRenameTheTarget(t *testing.T) {
	expanded := expand(t, ScrapeTarget{
		Name:   "probe",
		URL:    "http://127.0.0.1:9115/probe",
		Params: ParamValues{"module": {"http_2xx"}, "target": {"https://a.example", "https://b.example"}},
	})

	for _, target := range expanded {
		if !strings.HasPrefix(target.Name, "probe/https://") {
			t.Fatalf("name %q should carry the varying value only", target.Name)
		}
	}
}

// An operator label must not be overwritten by a parameter of the same name.
func TestConfiguredLabelsWinOverParamLabels(t *testing.T) {
	expanded := expand(t, ScrapeTarget{
		URL:    "http://127.0.0.1:9115/probe",
		Params: ParamValues{"module": {"http_2xx"}},
		Labels: map[string]string{"module": "chosen-by-hand"},
	})

	if expanded[0].Labels["module"] != "chosen-by-hand" {
		t.Fatalf("param label overrode the configured one: %+v", expanded[0].Labels)
	}
}

// A URL that already carries a query keeps it: some exporters document their
// endpoint that way.
func TestExistingQueryStringIsPreserved(t *testing.T) {
	expanded := expand(t, ScrapeTarget{
		URL:    "https://127.0.0.1:4646/v1/metrics?format=prometheus",
		Params: ParamValues{"module": {"a"}},
	})

	query := queryOf(t, expanded[0].URL)
	if query.Get("format") != "prometheus" || query.Get("module") != "a" {
		t.Fatalf("query %v lost a parameter", query)
	}
}

// A target without parameters must come out exactly as it went in.
func TestTargetWithoutParamsIsUntouched(t *testing.T) {
	target := ScrapeTarget{Name: "node", URL: "http://localhost:9100/metrics"}
	expanded := expand(t, target)

	if len(expanded) != 1 || expanded[0].URL != target.URL || expanded[0].Name != target.Name {
		t.Fatalf("target changed: %+v", expanded)
	}
}

// Both YAML shapes have to work side by side: a scalar is a fixed parameter,
// a list is an axis.
func TestParamsAcceptScalarAndList(t *testing.T) {
	var cfg ScrapeConfig
	err := yaml.Unmarshal([]byte(`
targets:
  - name: dns
    url: http://127.0.0.1:15353/query
    params:
      module: [a, b]
      fixed: value
`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	params := cfg.Targets[0].Params
	if len(params["module"]) != 2 || len(params["fixed"]) != 1 || params["fixed"][0] != "value" {
		t.Fatalf("params not parsed: %+v", params)
	}
}
