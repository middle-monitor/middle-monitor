package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

// receivedLog is one record as the backend sees it.
type receivedLog struct {
	body, severity, service, host string
	resource                      map[string]string
}

// logSink is a fake OTLP logs endpoint. failFirst answers that status to the
// first requests before accepting.
type logSink struct {
	mu        sync.Mutex
	logs      []receivedLog
	calls     atomic.Int32
	failFirst int32
	status    int
	auth      atomic.Value
}

func newLogSink(t *testing.T) (*logSink, *httptest.Server) {
	sink := &logSink{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		sink.auth.Store(r.Header.Get("Authorization"))
		if sink.calls.Add(1) <= sink.failFirst {
			w.WriteHeader(sink.status)
			return
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(zr)
		var req collogspb.ExportLogsServiceRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sink.mu.Lock()
		for _, rl := range req.ResourceLogs {
			res := map[string]string{}
			for _, kv := range rl.Resource.Attributes {
				res[kv.Key] = kv.Value.GetStringValue()
			}
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					sink.logs = append(sink.logs, receivedLog{
						body: lr.Body.GetStringValue(), severity: lr.SeverityText,
						service: res["service.name"], host: res["host.name"], resource: res,
					})
				}
			}
		}
		sink.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return sink, srv
}

func (s *logSink) bodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.logs))
	for _, l := range s.logs {
		out = append(out, l.body)
	}
	return out
}

func (s *logSink) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.bodies(); len(got) >= n {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("got %v, want %d records", s.bodies(), n)
	return nil
}

func logAgentConfig(srv *httptest.Server, state string, sources ...LogSource) *AgentConfig {
	cfg := &AgentConfig{}
	cfg.API.URL = srv.URL
	cfg.API.APIKey = "install-token"
	cfg.Host.Name = "web-01"
	cfg.Host.Service = "default"
	cfg.Logs = LogsConfig{Enabled: true, StateFile: state, Sources: sources}
	return cfg
}

func startCollector(t *testing.T, cfg *AgentConfig) *logCollector {
	t.Helper()
	if err := PrepareLogs(&cfg.Logs); err != nil {
		t.Fatal(err)
	}
	c := StartLogCollection(cfg)
	t.Cleanup(c.Stop)
	return c
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// Turning collection on must not flood the backend with a file's whole history,
// and a restart must neither re-send nor skip what was written meanwhile.
func TestFileTailStartsAtEndAndResumesAfterRestart(t *testing.T) {
	sink, srv := newLogSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLines(t, path, "old history line")
	cfg := logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceFile, Path: StringOrList{path}, Service: "gitlab"})

	c := startCollector(t, cfg)
	appendLines(t, path, "first", "second")
	got := sink.waitFor(t, 2)
	c.Stop()

	appendLines(t, path, "written while stopped")
	startCollector(t, cfg)
	got = sink.waitFor(t, 3)

	want := []string{"first", "second", "written while stopped"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.logs[0].service != "gitlab" || sink.logs[0].host != "web-01" {
		t.Errorf("resource = %+v: the host name is how logs line up with host metrics", sink.logs[0])
	}
	if sink.auth.Load() != "Bearer install-token" {
		t.Errorf("auth header = %v, the receiver resolves the org from it", sink.auth.Load())
	}
}

// logrotate's default renames the file and creates a new one: the lines written
// just before the rename and those in the new file must all arrive.
func TestFileRotationLosesNothing(t *testing.T) {
	sink, srv := newLogSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLines(t, path)
	startCollector(t, logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceFile, Path: StringOrList{path}}))

	appendLines(t, path, "before rotation")
	sink.waitFor(t, 1)
	appendLines(t, path, "last line of the old file")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, "first line of the new file")

	got := sink.waitFor(t, 3)
	joined := strings.Join(got, "|")
	for _, want := range []string{"last line of the old file", "first line of the new file"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q lost in rotation, got %q", want, got)
		}
	}
}

// Legacy apps write Latin-1: one such line must not make the batch unencodable
// and take the valid lines around it down with it.
func TestInvalidUTF8LineDoesNotDropItsBatch(t *testing.T) {
	sink, srv := newLogSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLines(t, path)
	startCollector(t, logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceFile, Path: StringOrList{path}}))

	appendLines(t, path, "before", "caf\xe9 latin1", "after")

	got := sink.waitFor(t, 3)
	want := []string{"before", "caf\uFFFD latin1", "after"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// copytruncate keeps the inode and empties the file: reading must start over
// instead of waiting for the file to grow past the old offset.
func TestCopyTruncateRestartsReading(t *testing.T) {
	sink, srv := newLogSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLines(t, path)
	startCollector(t, logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceFile, Path: StringOrList{path}}))

	appendLines(t, path, "a fairly long line before the truncation happens")
	sink.waitFor(t, 1)
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * logPollInterval)
	appendLines(t, path, "after")
	if got := sink.waitFor(t, 2); got[1] != "after" {
		t.Errorf("got %q", got)
	}
}

// A stack trace split over many records is unreadable and its ERROR level
// would only be on the first line.
func TestMultilineKeepsAStackTraceTogether(t *testing.T) {
	sink, srv := newLogSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLines(t, path)
	startCollector(t, logAgentConfig(srv, filepath.Join(dir, "state.json"),
		LogSource{Type: logSourceFile, Path: StringOrList{path}, Multiline: `^\d{4}-\d{2}-\d{2}`}))

	appendLines(t, path,
		"2026-10-11 09:00:00 ERROR request failed",
		"java.lang.NullPointerException: user",
		"\tat com.shop.Api.handle(Api.java:42)",
		"2026-10-11 09:00:01 INFO next request",
	)
	got := sink.waitFor(t, 2)
	if len(got) != 2 || !strings.HasSuffix(got[0], "(Api.java:42)") {
		t.Fatalf("got %q", got)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.logs[0].severity != "ERROR" || sink.logs[1].severity != "INFO" {
		t.Errorf("severities %s %s", sink.logs[0].severity, sink.logs[1].severity)
	}
}

// A container's log appears under the compose service it belongs to, with its
// own identity on the resource so it can be filtered by container.
func TestDockerContainerLogs(t *testing.T) {
	sink, srv := newLogSink(t)
	root := t.TempDir()
	logPath := writeDockerConfig(t, filepath.Join(root, "0123456789abcdef"), "shop-api-1", map[string]string{"com.docker.compose.service": "api"})
	appendLines(t, logPath)
	startCollector(t, logAgentConfig(srv, filepath.Join(root, "state.json"),
		LogSource{Type: logSourceDocker, Path: StringOrList{filepath.Join(root, "*", "*-json.log")}}))

	appendLines(t, logPath,
		`{"log":"panic: runtime ","stream":"stderr","time":"2026-10-11T09:00:00Z"}`,
		`{"log":"error: index out of range\n","stream":"stderr","time":"2026-10-11T09:00:00Z"}`,
	)
	got := sink.waitFor(t, 1)
	if got[0] != "panic: runtime error: index out of range" {
		t.Errorf("split line not joined: %q", got)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if l := sink.logs[0]; l.service != "api" || l.resource["container.name"] != "shop-api-1" || l.resource["container.image.name"] != "registry.local/api:1.4" {
		t.Errorf("got %+v", l)
	}
}

// A container started after the agent is new content: its first lines are the
// ones that explain why it crashed, they must not be skipped.
func TestContainerStartedLaterIsReadFromTheBeginning(t *testing.T) {
	sink, srv := newLogSink(t)
	root := t.TempDir()
	startCollector(t, logAgentConfig(srv, filepath.Join(root, "state.json"),
		LogSource{Type: logSourceDocker, Path: StringOrList{filepath.Join(root, "*", "*-json.log")}}))

	logPath := writeDockerConfig(t, filepath.Join(root, "fedcba9876543210"), "worker", nil)
	appendLines(t, logPath, `{"log":"starting\n","stream":"stdout","time":"2026-10-11T09:00:00Z"}`)
	if got := sink.waitFor(t, 1); got[0] != "starting" {
		t.Errorf("got %q", got)
	}
}

// A receiver restart answers 503 for a few seconds; the lines must arrive once
// it is back, not be dropped.
func TestLogExportRetriesAnUnavailableReceiver(t *testing.T) {
	sink, srv := newLogSink(t)
	sink.failFirst, sink.status = 2, http.StatusServiceUnavailable
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLines(t, path)
	startCollector(t, logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceFile, Path: StringOrList{path}}))

	appendLines(t, path, "survives the outage")
	if got := sink.waitFor(t, 1); got[0] != "survives the outage" {
		t.Errorf("got %q", got)
	}
}

// A batch the backend refuses (bad token, payload) would fail forever: it is
// dropped so the next lines still go out.
func TestLogExportDropsARefusedBatch(t *testing.T) {
	sink, srv := newLogSink(t)
	sink.failFirst, sink.status = 1, http.StatusBadRequest
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLines(t, path)
	startCollector(t, logAgentConfig(srv, filepath.Join(dir, "state.json"), LogSource{Type: logSourceFile, Path: StringOrList{path}}))

	appendLines(t, path, "refused")
	deadline := time.Now().Add(5 * time.Second)
	for sink.calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	appendLines(t, path, "next")
	if got := sink.waitFor(t, 1); got[0] != "next" {
		t.Errorf("got %q", got)
	}
}
