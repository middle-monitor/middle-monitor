package workers

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

var (
	sharedHTTPTransport *http.Transport
	httpTransportOnce   sync.Once
)

// getSharedHTTPTransport returns a shared HTTP transport for connection reuse
func getSharedHTTPTransport() *http.Transport {
	httpTransportOnce.Do(func() {
		dialer := &net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}

		sharedHTTPTransport = &http.Transport{
			DialContext: dialer.DialContext,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
			TLSHandshakeTimeout:   5 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			DisableKeepAlives:     false,
			DisableCompression:    false,
			ResponseHeaderTimeout: 5 * time.Second,
			ForceAttemptHTTP2:     true,
			// Without pings, a pooled h2 connection killed silently by a NAT or a
			// tunnel stays "alive" and every reuse times out awaiting headers.
			HTTP2: &http.HTTP2Config{
				SendPingTimeout: 15 * time.Second,
				PingTimeout:     5 * time.Second,
			},
		}
	})
	return sharedHTTPTransport
}

// httpPhases accumulates connection-phase timings over a whole request chain,
// redirects included. httptrace callbacks can fire concurrently (dual-stack
// dialing), hence the mutex.
type httpPhases struct {
	mu           sync.Mutex
	dnsStart     time.Time
	dns          time.Duration
	connectStart time.Time
	connect      time.Duration
	tlsStart     time.Time
	tls          time.Duration
	redirects    int
	finalURL     string
}

func (p *httpPhases) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.dnsStart = time.Now()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if !p.dnsStart.IsZero() {
				p.dns += time.Since(p.dnsStart)
			}
		},
		ConnectStart: func(string, string) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.connectStart = time.Now()
		},
		ConnectDone: func(string, string, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if !p.connectStart.IsZero() {
				p.connect += time.Since(p.connectStart)
			}
		},
		TLSHandshakeStart: func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.tlsStart = time.Now()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if !p.tlsStart.IsZero() {
				p.tls += time.Since(p.tlsStart)
			}
		},
	}
}

// metadata renders the phase breakdown for the UI. Phases are zero when the
// connection was reused from the pool, which is the normal steady state.
// server_ms is the remainder, so the parts always add up to the shown latency.
func (p *httpPhases) metadata(elapsed time.Duration) *string {
	p.mu.Lock()
	defer p.mu.Unlock()

	round := func(d time.Duration) float64 {
		return float64(d.Microseconds()) / 1000
	}
	server := elapsed - p.dns - p.connect - p.tls
	if server < 0 {
		server = 0
	}

	meta := map[string]interface{}{
		"http_dns_ms":     round(p.dns),
		"http_connect_ms": round(p.connect),
		"http_tls_ms":     round(p.tls),
		"http_server_ms":  round(server),
		"http_redirects":  p.redirects,
	}
	if p.finalURL != "" {
		meta["http_final_url"] = p.finalURL
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	s := string(raw)
	return &s
}

func applyHTTPAuth(req *http.Request, auth *services.WorkerHTTPAuth) {
	if auth == nil || auth.Mode == "" || auth.Mode == "none" {
		return
	}
	switch auth.Mode {
	case "bearer":
		if auth.BearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+auth.BearerToken)
		}
	case "basic":
		req.SetBasicAuth(auth.BasicUser, auth.BasicPassword)
	}
}

func buildHTTPURL(host string, path *string, useHTTPS bool) string {
	var url string
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		url = host
	} else if useHTTPS {
		url = "https://" + host
	} else {
		url = "http://" + host
	}
	if path != nil && *path != "" {
		if (*path)[0] != '/' {
			url += "/"
		}
		url += *path
	}
	// No trailing "/" when path is empty: the host may already carry a path
	// (e.g. "https://x.io/healthz"), and "/healthz/" can 404 where "/healthz" is 200.
	return url
}

func hostHasExplicitProtocol(host string) bool {
	return strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://")
}

// httpStatusMatches reports whether the response status is a success. When the
// service defines an explicit expected status code, only that exact code passes;
// otherwise the default behavior applies (any 2xx is a success).
func httpStatusMatches(expected *int, statusCode int) bool {
	if expected != nil {
		return statusCode == *expected
	}
	return statusCode >= 200 && statusCode < 300
}

// checkBodyAssertion evaluates the configured response-body assertion(s) against
// the (already capped) body. The value holds one assertion per line; ALL must
// pass (AND). Returns ok=false plus a human-readable message on the first
// failure. The mode applies to every assertion.
func checkBodyAssertion(service models.Service, body []byte) (bool, string) {
	mode := "contains"
	if service.ExpectedBodyMode != nil && *service.ExpectedBodyMode != "" {
		mode = *service.ExpectedBodyMode
	}
	for _, raw := range strings.Split(*service.ExpectedBodyContains, "\n") {
		expr := strings.TrimSpace(raw)
		if expr == "" {
			continue
		}
		if ok, msg := evalBodyAssertion(mode, expr, body); !ok {
			return false, msg
		}
	}
	return true, ""
}

// evalBodyAssertion checks a single assertion. Mode "json_path" parses the body
// as JSON and compares the value at a dot-separated path (with numeric array
// indices) to the expected value as text, e.g. "data.foo.x.status=ok". The path
// may resolve at the root or inside any nested object/array, so a bare key like
// "installed=true" matches a sub-sub-key. Any other mode is a case-sensitive
// substring match; on a JSON body, a "key=value" or "key:value" expression also
// matches the key at any depth, so "installed=true" holds against
// {"installed":true} despite the raw text differing.
func evalBodyAssertion(mode, expr string, body []byte) (bool, string) {
	if mode == "json_path" {
		path, want, found := strings.Cut(expr, "=")
		path = strings.TrimSpace(path)
		want = strings.TrimSpace(want)
		if !found || path == "" {
			return false, fmt.Sprintf("invalid JSON assertion %q (expected path=value)", expr)
		}
		var root any
		if err := json.Unmarshal(body, &root); err != nil {
			return false, "HTTP body is not valid JSON"
		}
		matched, got, ok := jsonPathMatchAnywhere(root, path, want)
		if matched {
			return true, ""
		}
		if !ok {
			return false, fmt.Sprintf("JSON path %q not found in body", path)
		}
		return false, fmt.Sprintf("JSON path %s = %q (expected %q)", path, got, want)
	}

	if strings.Contains(string(body), expr) {
		return true, ""
	}
	if path, want, ok := splitBodyAssertionExpr(expr); ok {
		var root any
		if err := json.Unmarshal(body, &root); err == nil {
			if matched, _, _ := jsonPathMatchAnywhere(root, path, want); matched {
				return true, ""
			}
		}
	}
	return false, fmt.Sprintf("HTTP body does not contain %q", expr)
}

// splitBodyAssertionExpr parses "path=value" or "path:value" (spaces and
// surrounding quotes trimmed) for the JSON fallback of contains mode.
func splitBodyAssertionExpr(expr string) (path, want string, ok bool) {
	sep := "="
	if !strings.Contains(expr, sep) {
		sep = ":"
	}
	rawPath, rawWant, found := strings.Cut(expr, sep)
	path = strings.Trim(strings.TrimSpace(rawPath), `"`)
	want = strings.Trim(strings.TrimSpace(rawWant), `"`)
	if !found || path == "" {
		return "", "", false
	}
	return path, want, true
}

// jsonPathMatchAnywhere resolves path at the root and at every nested
// object/array; one occurrence equal to want is enough. found reports whether
// the path resolved at least once, got holds a resolved value for reporting.
func jsonPathMatchAnywhere(node any, path, want string) (matched bool, got string, found bool) {
	if v, ok := jsonPathValue(node, path); ok {
		if v == want {
			return true, v, true
		}
		got, found = v, true
	}
	var children []any
	switch n := node.(type) {
	case map[string]any:
		for _, c := range n {
			children = append(children, c)
		}
	case []any:
		children = n
	}
	for _, c := range children {
		m, g, f := jsonPathMatchAnywhere(c, path, want)
		if m {
			return true, g, true
		}
		if f && !found {
			got, found = g, true
		}
	}
	return false, got, found
}

// jsonPathValue walks a dot-separated path into a decoded JSON value and
// returns the leaf scalar as a string. Numeric segments index into arrays.
// Returns ok=false when the path does not resolve or the leaf is not a scalar.
func jsonPathValue(root any, path string) (string, bool) {
	cur := root
	for _, seg := range strings.Split(path, ".") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return "", false
		}
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return "", false
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return "", false
			}
			cur = node[idx]
		default:
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		// null, object or array leaf: not comparable as a scalar value.
		return "", false
	}
}

func executeHTTPService(service models.Service) models.ServiceResult {
	result := models.ServiceResult{
		ServiceID: service.ID,
		Timestamp: time.Now().UTC(),
	}

	auth, err := services.DecryptHTTPAuthForWorker(service.Credentials, nil)
	if err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("HTTP credentials: %v", err)
		result.Message = &msg
		return result
	}

	transport := getSharedHTTPTransport()
	phases := &httpPhases{}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Past the hop limit, keep the last 3xx instead of erroring out: the
			// check then reports the redirect status, which reads better than a
			// transport error.
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			phases.mu.Lock()
			defer phases.mu.Unlock()
			phases.redirects = len(via)
			phases.finalURL = req.URL.String()
			return nil
		},
	}

	doGet := func(targetURL string) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodGet, targetURL, nil)
		if err != nil {
			return nil, err
		}
		applyHTTPAuth(req, auth)
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), phases.trace()))
		return client.Do(req)
	}

	host := service.Host
	url := buildHTTPURL(host, service.Path, true)

	start := time.Now()
	resp, err := doGet(url)

	// If https fails and host had no explicit protocol, try plain http
	if err != nil && !hostHasExplicitProtocol(host) {
		httpURL := buildHTTPURL(host, service.Path, false)
		// Drop the failed attempt's timings so the breakdown describes only the
		// attempt we report on.
		*phases = httpPhases{}
		start = time.Now()
		resp, err = doGet(httpURL)
		url = httpURL
	}

	if err != nil {
		elapsed := time.Since(start)
		latency := float64(elapsed.Milliseconds())
		result.Status = "failure"
		msg := fmt.Sprintf("HTTP error: %v", err)
		result.Message = &msg
		result.Latency = &latency
		result.Metadata = phases.metadata(elapsed)
		return result
	}

	elapsed := time.Since(start)
	latency := float64(elapsed.Milliseconds())
	result.Metadata = phases.metadata(elapsed)

	// Only read the body when we need to assert on its content; otherwise drain
	// and discard it so the connection can be reused. Cap the read to bound memory.
	needBody := service.ExpectedBodyContains != nil && *service.ExpectedBodyContains != ""
	var bodyBytes []byte
	if needBody {
		const maxBodyBytes = 1 << 20 // 1 MiB
		bodyBytes, _ = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if latency > 20 {
		slog.Debug("http check completed", "latency_ms", latency, "url", url)
	}

	if httpStatusMatches(service.ExpectedStatusCode, resp.StatusCode) {
		// A failed body assertion is a hard failure, regardless of latency thresholds.
		if needBody {
			if ok, failMsg := checkBodyAssertion(service, bodyBytes); !ok {
				result.Status = "failure"
				result.Message = &failMsg
				result.Latency = &latency
				return result
			}
		}
		criticalMs := service.CriticalThreshold
		if criticalMs == nil {
			criticalMs = service.FailureThreshold // backward compat
		}
		if criticalMs != nil && latency > *criticalMs {
			result.Status = "failure"
			msg := fmt.Sprintf("HTTP latency (%.0fms) exceeded critical threshold (%.0fms)", latency, *criticalMs)
			result.Message = &msg
			result.Latency = &latency
		} else if service.WarningThreshold != nil && latency > *service.WarningThreshold {
			result.Status = "warning"
			msg := fmt.Sprintf("HTTP latency (%.0fms) exceeded warning threshold (%.0fms)", latency, *service.WarningThreshold)
			result.Message = &msg
			result.Latency = &latency
		} else {
			result.Status = "success"
			result.Latency = &latency
		}
	} else {
		result.Status = "failure"
		var msg string
		if service.ExpectedStatusCode != nil {
			msg = fmt.Sprintf("HTTP status: %d (expected %d)", resp.StatusCode, *service.ExpectedStatusCode)
		} else {
			msg = fmt.Sprintf("HTTP status: %d", resp.StatusCode)
		}
		result.Message = &msg
		result.Latency = &latency
	}

	return result
}
