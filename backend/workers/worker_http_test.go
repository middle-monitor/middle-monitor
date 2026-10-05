package workers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"middle-monitor/backend/models"
)

func strptr(s string) *string { return &s }

func TestCheckBodyAssertion(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		mode   *string
		body   string
		wantOK bool
	}{
		// --- json_path mode: the value is matched against the JSON value at the
		// path, so quotes/whitespace/key order in the raw body don't matter. ---
		{"json nested match", "data.foo.x.status=ok", strptr("json_path"), `{"data":{"foo":{"x":{"status":"ok"}}}}`, true},
		{"json nested mismatch", "data.foo.x.status=ok", strptr("json_path"), `{"data":{"foo":{"x":{"status":"ko"}}}}`, false},
		{"json whitespace ignored", "data.status=ok", strptr("json_path"), `{ "data": { "status": "ok" } }`, true},
		{"json array index", "items.1.name=bar", strptr("json_path"), `{"items":[{"name":"foo"},{"name":"bar"}]}`, true},
		{"json number value", "count=5", strptr("json_path"), `{"count":5}`, true},
		{"json bool value", "ready=true", strptr("json_path"), `{"ready":true}`, true},
		{"json path missing", "data.nope=ok", strptr("json_path"), `{"data":{"status":"ok"}}`, false},

		// --- json_path at any depth: users assert on a key without knowing the
		// full path from the root, so the path may resolve in any sub-object. ---
		{"json bare key in sub-sub-key", "installed=true", strptr("json_path"), `{"a":{"b":{"installed":true}}}`, true},
		{"json relative path in sub-key", "b.installed=true", strptr("json_path"), `{"a":{"b":{"installed":true}}}`, true},
		{"json bare key inside array element", "installed=true", strptr("json_path"), `{"items":[{"installed":true}]}`, true},
		{"json bare key wrong value everywhere", "installed=true", strptr("json_path"), `{"a":{"installed":false}}`, false},
		{"json any matching occurrence wins", "installed=true", strptr("json_path"), `{"installed":false,"a":{"installed":true}}`, true},
		{"json invalid body", "data.status=ok", strptr("json_path"), `not json`, false},
		{"json missing equals", "data.status", strptr("json_path"), `{"data":{"status":"ok"}}`, false},

		// --- contains mode (default): raw substring, case-sensitive. ---
		{"contains present", `"status":"ok"`, strptr("contains"), `{"data":{"status":"ok"}}`, true},
		{"contains plain text absent", "ready", strptr("contains"), `{"data":{"status":"ok"}}`, false},
		{"contains nil mode defaults", "ok", nil, `{"status":"ok"}`, true},
		{"contains value keeps comma", "a,b", strptr("contains"), `{"k":"a,b,c"}`, true},

		// --- contains mode JSON fallback: "contains" means the condition holds
		// somewhere in the body, so on JSON responses key=value / key:value must
		// match the key at any depth even though the raw text has quotes/colons. ---
		{"contains equals matches json key", "installed=true", strptr("contains"), `{"installed":true}`, true},
		{"contains equals matches nested key", "installed=true", strptr("contains"), `{"a":{"b":{"installed":true}}}`, true},
		{"contains colon matches json key", "status:ok", strptr("contains"), `{"data":{"status":"ok"}}`, true},
		{"contains quoted spaced json", `"status":"ok"`, strptr("contains"), `{ "status" : "ok" }`, true},
		{"contains json wrong value", "installed=true", strptr("contains"), `{"a":{"installed":false}}`, false},
		{"contains no fallback on non-json body", "installed=true", strptr("contains"), `installed is maybe true`, false},

		// --- multiple assertions (one per line): ALL must pass (AND). ---
		{"contains multi all present", "ok\nready", strptr("contains"), `{"status":"ok","state":"ready"}`, true},
		{"contains multi one absent", "ok\nmissing", strptr("contains"), `{"status":"ok"}`, false},
		{"json multi all match", "data.status=ok\ndata.ready=true", strptr("json_path"), `{"data":{"status":"ok","ready":true}}`, true},
		{"json multi one fails", "data.status=ok\ndata.ready=true", strptr("json_path"), `{"data":{"status":"ok","ready":false}}`, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := models.Service{ExpectedBodyContains: strptr(c.value), ExpectedBodyMode: c.mode}
			ok, msg := checkBodyAssertion(svc, []byte(c.body))
			if ok != c.wantOK {
				t.Fatalf("checkBodyAssertion(%q, mode=%v, body=%q) = %v (%q), want %v", c.value, c.mode, c.body, ok, msg, c.wantOK)
			}
		})
	}
}

func TestBuildHTTPURL(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		path     *string
		useHTTPS bool
		want     string
	}{
		// The checked URL must be byte-identical to what the user would curl:
		// a spurious trailing "/" can 404 where the exact path is 200.
		{"host already carries a path", "https://api.middlemonitor.io/healthz", nil, true, "https://api.middlemonitor.io/healthz"},
		{"host with path, empty path field", "https://api.middlemonitor.io/healthz", strptr(""), true, "https://api.middlemonitor.io/healthz"},
		{"bare host https", "example.com", nil, true, "https://example.com"},
		{"bare host http fallback", "example.com", nil, false, "http://example.com"},
		{"explicit protocol kept", "http://example.com", nil, true, "http://example.com"},
		{"path with leading slash", "example.com", strptr("/health"), true, "https://example.com/health"},
		{"path without leading slash", "example.com", strptr("health"), true, "https://example.com/health"},
		{"host path plus path field", "https://example.com/api", strptr("/health"), true, "https://example.com/api/health"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildHTTPURL(c.host, c.path, c.useHTTPS)
			if got != c.want {
				t.Fatalf("buildHTTPURL(%q, %v, %v) = %q, want %q", c.host, c.path, c.useHTTPS, got, c.want)
			}
		})
	}
}

// A redirect is invisible in the latency number alone: the check silently pays
// for a second DNS/TCP/TLS chain and users blame the monitoring. The result must
// therefore record that a hop happened and where it landed.
func TestExecuteHTTPServiceRecordsRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	result := executeHTTPService(models.Service{ID: 1, Host: ts.URL})

	if result.Status != "success" {
		t.Fatalf("status = %q, want success (message: %v)", result.Status, result.Message)
	}
	if result.Metadata == nil {
		t.Fatal("metadata is nil, want the phase breakdown")
	}
	var meta struct {
		Redirects int     `json:"http_redirects"`
		FinalURL  string  `json:"http_final_url"`
		ConnectMs float64 `json:"http_connect_ms"`
	}
	if err := json.Unmarshal([]byte(*result.Metadata), &meta); err != nil {
		t.Fatalf("metadata is not valid JSON: %v", err)
	}
	if meta.Redirects != 1 {
		t.Errorf("redirects = %d, want 1", meta.Redirects)
	}
	if meta.FinalURL != ts.URL+"/final" {
		t.Errorf("final_url = %q, want %q", meta.FinalURL, ts.URL+"/final")
	}
	// A first-time check dials, so the connect phase must be attributed rather
	// than folded into the opaque server time.
	if meta.ConnectMs <= 0 {
		t.Errorf("connect_ms = %v, want > 0 on a cold connection", meta.ConnectMs)
	}
}
