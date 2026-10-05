package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The handler rewrites the API_URL default by matching this exact line. If the
// embedded script drifts away from it, the rewrite silently no-ops and agents
// install themselves pointing at the script's hardcoded default instead of the
// host they were downloaded from.
const installScriptAPIURLSentinel = `API_URL="${MIDDLE_MONITOR_API_URL:-http://localhost:8080}"`

func TestInstallScriptKeepsTheSentinelTheHandlerRewrites(t *testing.T) {
	if !strings.Contains(string(installScript), installScriptAPIURLSentinel) {
		t.Fatalf("embedded install.sh no longer contains %s: the API_URL injection is dead code", installScriptAPIURLSentinel)
	}
}

func TestInstallScriptPointsAgentsAtTheHostItWasDownloadedFrom(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/agents/download/install", nil)
	req.Host = "api.middlemonitor.io"
	req.Header.Set("X-Forwarded-Proto", "https")

	rec := httptest.NewRecorder()
	handleAgentInstallScript(nil)(rec, req)

	body := rec.Body.String()
	want := `API_URL="${MIDDLE_MONITOR_API_URL:-https://api.middlemonitor.io}"`
	if !strings.Contains(body, want) {
		t.Errorf("served script does not point at the download host.\nwant: %s", want)
	}
	if strings.Contains(body, installScriptAPIURLSentinel) {
		t.Error("the localhost default survived: the agent would never reach the backend")
	}
}

// The install script bakes API_URL into every agent's configuration, so a
// scheme taken from the last proxy hop would configure the whole fleet to talk
// in clear.
func TestInstallScriptUsesThePublicURL(t *testing.T) {
	t.Setenv("PUBLIC_API_URL", "https://api.middlemonitor.io")

	req := httptest.NewRequest("GET", "/api/v1/agents/download/install", nil)
	req.Host = "localhost:4001"
	req.Header.Set("X-Forwarded-Proto", "http")

	rec := httptest.NewRecorder()
	handleAgentInstallScript(nil)(rec, req)

	want := `API_URL="${MIDDLE_MONITOR_API_URL:-https://api.middlemonitor.io}"`
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("the install script does not carry %s", want)
	}
}

// The update script used to be read from ../agent/update.sh at request time.
// The runtime images carry only the agent's built binaries and its schemas, so
// that lookup missed in every container and the endpoint answered 404 on every
// deployment while passing locally, where the agent tree sits next to the
// backend. Serving it from the embedded copy is what makes the endpoint exist
// in production; running from a directory with no agent tree is the case that
// used to fail.
func TestUpdateScriptIsServedWithoutTheAgentTreeOnDisk(t *testing.T) {
	t.Chdir(t.TempDir())

	req := httptest.NewRequest("GET", "/api/v1/agents/download/update", nil)
	req.Host = "api.middlemonitor.io"
	req.Header.Set("X-Forwarded-Proto", "https")

	rec := httptest.NewRecorder()
	handleAgentUpdateScript()(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d, want 200: the endpoint is unreachable wherever the agent tree is absent", rec.Code)
	}
	body := rec.Body.String()
	want := `API_URL="${MIDDLE_MONITOR_API_URL:-https://api.middlemonitor.io}"`
	if !strings.Contains(body, want) {
		t.Errorf("served script does not point at the download host.\nwant: %s", want)
	}
	if strings.Contains(body, installScriptAPIURLSentinel) {
		t.Error("the localhost default survived: the update would download from nowhere")
	}
}

// Same sentinel as the install script: if the embedded update.sh drifts away
// from this line the rewrite silently no-ops.
func TestUpdateScriptKeepsTheSentinelTheHandlerRewrites(t *testing.T) {
	if !strings.Contains(string(updateScript), installScriptAPIURLSentinel) {
		t.Fatalf("embedded update.sh no longer contains %s: the API_URL injection is dead code", installScriptAPIURLSentinel)
	}
}
