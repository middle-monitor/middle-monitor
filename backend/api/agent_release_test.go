package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// elfBinary is the smallest thing the download handler accepts as a binary.
var elfBinary = append([]byte{0x7f, 'E', 'L', 'F'}, []byte("payload")...)

// withFakeDist puts a built agent where the handlers look for one. The lookup
// is relative to the working directory, so the test moves into a temp tree.
func withFakeDist(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	dist := filepath.Join(root, "agent", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, platform := range agentPlatforms {
		name := "middle-monitor-agent-" + platform.OS + "-" + platform.Arch
		if err := os.WriteFile(filepath.Join(dist, name), elfBinary, 0o755); err != nil {
			t.Fatalf("write binary: %v", err)
		}
	}
	if version != "" {
		if err := os.WriteFile(filepath.Join(dist, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
			t.Fatalf("write VERSION: %v", err)
		}
	}

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// The handlers look for ../agent/dist first, so run from a sibling dir.
	work := filepath.Join(root, "backend")
	os.MkdirAll(work, 0o755)
	if err := os.Chdir(work); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		os.Chdir(previous)
		checksumCache.Range(func(key, _ any) bool {
			checksumCache.Delete(key)
			return true
		})
	})
	return dist
}

func expectedDigest() string {
	sum := sha256.Sum256(elfBinary)
	return hex.EncodeToString(sum[:])
}

// The question a deployment needs answered before it downloads 14 MB per host:
// which version is on offer, and what should the file hash to?
func TestAgentLatestPublishesVersionAndChecksums(t *testing.T) {
	withFakeDist(t, "1.4.2")

	req := httptest.NewRequest("GET", "/api/v1/agents/latest", nil)
	req.Host = "api.middlemonitor.io"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	handleAgentLatest()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var release AgentRelease
	if err := json.Unmarshal(rec.Body.Bytes(), &release); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if release.Version != "1.4.2" {
		t.Fatalf("version %q, want 1.4.2", release.Version)
	}
	if got := release.Checksums["linux/amd64"]; got != expectedDigest() {
		t.Fatalf("sha256 %q, want %q", got, expectedDigest())
	}
	// The URL must be pinnable, which is the whole point of publishing it.
	want := "https://api.middlemonitor.io/api/v1/agents/download/1.4.2/linux/amd64"
	if release.URLs["linux/amd64"] != want {
		t.Fatalf("url %q, want %q", release.URLs["linux/amd64"], want)
	}
	if release.Binaries["linux/arm64"].Size != int64(len(elfBinary)) {
		t.Fatalf("size %d, want %d", release.Binaries["linux/arm64"].Size, len(elfBinary))
	}
}

// Without a VERSION file the platform must still answer, rather than reporting
// a version it cannot know.
func TestAgentLatestFallsBackToDev(t *testing.T) {
	withFakeDist(t, "")

	rec := httptest.NewRecorder()
	handleAgentLatest()(rec, httptest.NewRequest("GET", "/api/v1/agents/latest", nil))

	var release AgentRelease
	json.Unmarshal(rec.Body.Bytes(), &release)
	if release.Version != "dev" {
		t.Fatalf("version %q, want dev", release.Version)
	}
}

// Ansible's get_url checksum: reads this format directly.
func TestAgentChecksumEndpointServesShasumFormat(t *testing.T) {
	withFakeDist(t, "1.4.2")

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/agents/download/{os}/{arch}/sha256", handleAgentChecksum())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/agents/download/linux/amd64/sha256", nil))

	want := expectedDigest() + "  middle-monitor-agent-linux-amd64\n"
	if rec.Body.String() != want {
		t.Fatalf("body %q, want %q", rec.Body.String(), want)
	}
}

// Re-running a deployment on an unchanged fleet must not re-transfer the
// binary: that is the cost the ETag exists to remove.
func TestDownloadAnswers304WhenTheETagMatches(t *testing.T) {
	withFakeDist(t, "1.4.2")

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/agents/download/{os}/{arch}", handleAgentDownload(nil))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest("GET", "/api/v1/agents/download/linux/amd64", nil))

	etag := first.Header().Get("ETag")
	if etag != `"`+expectedDigest()+`"` {
		t.Fatalf("ETag %q, want the sha256", etag)
	}
	if first.Header().Get("Last-Modified") == "" {
		t.Fatal("Last-Modified is missing, so conditional requests cannot work")
	}
	if first.Header().Get("X-Agent-Version") != "1.4.2" {
		t.Fatalf("X-Agent-Version %q", first.Header().Get("X-Agent-Version"))
	}

	conditional := httptest.NewRequest("GET", "/api/v1/agents/download/linux/amd64", nil)
	conditional.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	router.ServeHTTP(second, conditional)

	if second.Code != http.StatusNotModified {
		t.Fatalf("status %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 answered with %d bytes", second.Body.Len())
	}
}

// Pinning a version is the default practice in production, so the versioned URL
// has to serve the binary.
func TestVersionedDownloadServesTheBinary(t *testing.T) {
	withFakeDist(t, "1.4.2")

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/agents/download/{version}/{os}/{arch}", handleAgentDownload(nil))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/agents/download/1.4.2/darwin/arm64", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != len(elfBinary) {
		t.Fatalf("served %d bytes, want %d", rec.Body.Len(), len(elfBinary))
	}
}

// A pin that no longer matches must fail loudly: silently serving another build
// is how a fleet drifts without anyone noticing.
func TestPinnedVersionThatIsGoneIs404(t *testing.T) {
	withFakeDist(t, "1.4.2")

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/agents/download/{version}/{os}/{arch}", handleAgentDownload(nil))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/agents/download/1.0.0/linux/amd64", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// "latest" stays usable for the install script, which does not pin.
func TestLatestKeywordServesWhateverIsOnOffer(t *testing.T) {
	withFakeDist(t, "1.4.2")

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/agents/download/{version}/{os}/{arch}", handleAgentDownload(nil))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/agents/download/latest/linux/amd64", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestUnknownPlatformIsRejected(t *testing.T) {
	withFakeDist(t, "1.4.2")

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/agents/download/{os}/{arch}", handleAgentDownload(nil))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/agents/download/plan9/sparc", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// The digest is cached by modification time; replacing the binary must change
// the published checksum or a fleet would verify against a stale value.
func TestChecksumFollowsANewBuild(t *testing.T) {
	dist := withFakeDist(t, "1.4.2")

	path := filepath.Join(dist, "middle-monitor-agent-linux-amd64")
	info, _ := os.Stat(path)
	first, err := agentFileChecksum(path, info)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}

	rebuilt := append([]byte{0x7f, 'E', 'L', 'F'}, []byte("a different build")...)
	if err := os.WriteFile(path, rebuilt, 0o755); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	info, _ = os.Stat(path)
	second, err := agentFileChecksum(path, info)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}

	if first == second {
		t.Fatal("the cached digest survived a rebuild")
	}
}

// The install script must keep offering the automation path documented for
// tools that will not pipe a remote script into a root shell.
func TestInstallScriptSupportsDownloadOnly(t *testing.T) {
	script := string(installScript)
	if !strings.Contains(script, "--download-only") {
		t.Fatal("the embedded install script lost --download-only")
	}
	if !strings.Contains(script, "/sha256") {
		t.Fatal("--download-only must verify the published digest")
	}
}

// Behind a Cloudflare tunnel, TLS is terminated at the edge and the origin only
// ever sees plain HTTP. X-Forwarded-Proto then honestly says "http", and every
// URL this API publishes comes out as http:// — including the one the install
// script bakes into each agent's configuration.
func TestPublicURLBeatsTheProxyHeaders(t *testing.T) {
	t.Setenv("PUBLIC_API_URL", "https://api.middlemonitor.io")

	req := httptest.NewRequest("GET", "/api/v1/agents/latest", nil)
	req.Host = "localhost:4001"
	req.Header.Set("X-Forwarded-Proto", "http")

	if got := requestBaseURL(req); got != "https://api.middlemonitor.io" {
		t.Fatalf("base URL %q, want the configured public URL", got)
	}
}

// A trailing slash in the configuration must not produce a double slash in
// every published URL.
func TestPublicURLIsNormalised(t *testing.T) {
	t.Setenv("PUBLIC_API_URL", " https://api.middlemonitor.io/ ")

	if got := requestBaseURL(httptest.NewRequest("GET", "/x", nil)); got != "https://api.middlemonitor.io" {
		t.Fatalf("base URL %q", got)
	}
}

// Unconfigured, a plain reverse-proxy deployment still works from the headers.
func TestHeadersRemainTheFallback(t *testing.T) {
	t.Setenv("PUBLIC_API_URL", "")

	req := httptest.NewRequest("GET", "/x", nil)
	req.Host = "api.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")

	if got := requestBaseURL(req); got != "https://api.example.com" {
		t.Fatalf("base URL %q", got)
	}
}

// A proxy chain appends to X-Forwarded-Proto; the first value is the client's.
func TestForwardedProtoChainTakesTheFirstValue(t *testing.T) {
	t.Setenv("PUBLIC_API_URL", "")

	req := httptest.NewRequest("GET", "/x", nil)
	req.Host = "ignored"
	req.Header.Set("X-Forwarded-Proto", "https, http")
	req.Header.Set("X-Forwarded-Host", "api.example.com, internal")

	if got := requestBaseURL(req); got != "https://api.example.com" {
		t.Fatalf("base URL %q", got)
	}
}

// The published download URL is what an operator copies into a deployment.
func TestPublishedDownloadURLUsesThePublicScheme(t *testing.T) {
	withFakeDist(t, "1.4.2")
	t.Setenv("PUBLIC_API_URL", "https://api.middlemonitor.io")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agents/latest", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	handleAgentLatest()(rec, req)

	var release AgentRelease
	if err := json.Unmarshal(rec.Body.Bytes(), &release); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := "https://api.middlemonitor.io/api/v1/agents/download/1.4.2/linux/amd64"
	if release.URLs["linux/amd64"] != want {
		t.Fatalf("url %q, want %q", release.URLs["linux/amd64"], want)
	}
}
