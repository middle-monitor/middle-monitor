package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// useOpenAIBackend points the client at a local stub and makes sure the Claude
// branch is not taken, so the OpenAI-compatible path is what gets exercised.
func useOpenAIBackend(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "gpt-4o")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", baseURL)
}

// The provider is chosen from configuration alone: an Anthropic key, or a model
// name that says Claude. Picking the wrong one sends the request to an endpoint
// that speaks another protocol.
func TestIsClaudeModelFollowsTheConfiguredProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_MODEL", "gpt-4o")
	if isClaudeModel() {
		t.Fatal("an OpenAI model with no Anthropic key is not Claude")
	}

	t.Setenv("OPENAI_MODEL", "claude-sonnet-4-6")
	if !isClaudeModel() {
		t.Fatal("a claude-* model must route to Anthropic")
	}

	t.Setenv("OPENAI_MODEL", "gpt-4o")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	if !isClaudeModel() {
		t.Fatal("an Anthropic key must route to Anthropic")
	}
}

func TestAnthropicBaseURLIsOverridableAndTrimmed(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	if got := anthropicBaseURL(); got != "https://api.anthropic.com" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("ANTHROPIC_BASE_URL", "http://localhost:9999/")
	if got := anthropicBaseURL(); got != "http://localhost:9999" {
		t.Fatalf("a trailing slash would produce a double slash in the path: %q", got)
	}
}

// The token budget bounds what the answer costs; a bad value must fall back to
// the default rather than send an unbounded or zero-token request.
func TestExplainMaxTokensRejectsUnusableValues(t *testing.T) {
	t.Setenv("EXPLAIN_MAX_TOKENS", "")
	if got := explainMaxTokens(); got != 150 {
		t.Fatalf("got %d", got)
	}
	t.Setenv("EXPLAIN_MAX_TOKENS", "512")
	if got := explainMaxTokens(); got != 512 {
		t.Fatalf("got %d", got)
	}
	for _, bad := range []string{"nope", "0", "-10"} {
		t.Setenv("EXPLAIN_MAX_TOKENS", bad)
		if got := explainMaxTokens(); got != 150 {
			t.Fatalf("%q gave %d, want the 150 default", bad, got)
		}
	}
}

// The system prompt is cached provider-side, which only works if every call
// sends it as a cacheable block. A silent regression here multiplies cost by 10.
func TestCallAnthropicSendsACacheableSystemBlock(t *testing.T) {
	var got anthropicReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-ant-test" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing auth headers: %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"content":[{"type":"thinking","text":"hmm"},{"type":"text","text":"**Cause.**"}]}`))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	t.Setenv("OPENAI_MODEL", "claude-sonnet-4-6")
	t.Setenv("EXPLAIN_MAX_TOKENS", "200")

	content, err := callAnthropicCompletion(context.Background(), "system prompt", "data")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if content != "**Cause.**" {
		t.Fatalf("the first text block is the answer, got %q", content)
	}
	if len(got.System) != 1 || got.System[0].CacheControl == nil || got.System[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("the system prompt was not sent as a cacheable block: %+v", got.System)
	}
	if got.MaxTokens != 200 || got.Model != "claude-sonnet-4-6" {
		t.Fatalf("unexpected request: %+v", got)
	}
}

// Every failure mode has to surface as a typed error: the handler logs it and
// returns 502, and "empty answer" must never be mistaken for a valid one.
func TestCallAnthropicSurfacesEveryFailureMode(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		target any
	}{
		{"http error", http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, new(*LLMStatusError)},
		{"unparseable body", http.StatusOK, `not json`, new(*LLMDecodeError)},
		{"provider error", http.StatusOK, `{"error":{"message":"overloaded"}}`, new(*LLMProviderError)},
		{"no text block", http.StatusOK, `{"content":[{"type":"thinking","text":"hmm"}]}`, new(*LLMEmptyError)},
	}
	for _, c := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
		t.Setenv("ANTHROPIC_BASE_URL", server.URL)
		_, err := callAnthropicCompletion(context.Background(), "s", "u")
		if err == nil {
			t.Fatalf("%s: expected an error", c.name)
		}
		if !errors.As(err, c.target) {
			t.Fatalf("%s: %v is not the expected typed error", c.name, err)
		}
		server.Close()
	}
}

// A dead endpoint must produce an error, not an empty explanation that would be
// persisted as if the model had answered.
func TestCallAnthropicReportsATransportFailure(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	if _, err := callAnthropicCompletion(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected a transport error")
	}
}

// Only text_delta events carry the answer; anything else in the stream (ping,
// message_start, usage) must be ignored rather than concatenated.
func TestStreamAnthropicKeepsOnlyTextDeltas(t *testing.T) {
	stream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start"}`,
		"",
		"event: content_block_delta",
		`data: {"delta":{"type":"text_delta","text":"**Cause"}}`,
		"",
		"event: content_block_delta",
		`data: {"delta":{"type":"text_delta","text":".**"}}`,
		"",
		"event: content_block_delta",
		`data: not json`,
		"",
		"event: ping",
		`data: {"delta":{"type":"text_delta","text":"IGNORED"}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(stream))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	got, err := streamAnthropicCompletion(context.Background(), "s", "u", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got != "**Cause.**" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamAnthropicReportsANonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	var statusErr *LLMStatusError
	_, err := streamAnthropicCompletion(context.Background(), "s", "u", nil)
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("got %v", err)
	}
}

// stream=false is what makes the endpoint return a plain JSON body; a
// streaming request here would come back as SSE and fail to parse.
func TestCallChatCompletionAsksForANonStreamingAnswer(t *testing.T) {
	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"**Cause.**"}}]}`))
	}))
	defer server.Close()

	useOpenAIBackend(t, server.URL+"/v1/")
	t.Setenv("OPENAI_API_KEY", "sk-test")

	content, err := callChatCompletion(context.Background(), "system", "data")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if content != "**Cause.**" {
		t.Fatalf("got %q", content)
	}
	if got.Stream {
		t.Fatal("the non-streaming path must not request a stream")
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
}

// The Claude configuration must reach Anthropic even through the generic entry
// point, otherwise a Claude key would be sent to an OpenAI-shaped endpoint.
func TestCallChatCompletionRoutesClaudeToAnthropic(t *testing.T) {
	var hit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	if _, err := callChatCompletion(context.Background(), "s", "u"); err != nil {
		t.Fatalf("call: %v", err)
	}
	if hit != "/v1/messages" {
		t.Fatalf("the request went to %q, not to the Anthropic Messages API", hit)
	}
}

func TestCallChatCompletionSurfacesEveryFailureMode(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		target any
	}{
		{"http error", http.StatusBadGateway, `boom`, new(*LLMStatusError)},
		{"unparseable body", http.StatusOK, `<html>`, new(*LLMDecodeError)},
		{"provider error", http.StatusOK, `{"error":{"message":"no model"}}`, new(*LLMProviderError)},
		{"no choices", http.StatusOK, `{"choices":[]}`, new(*LLMEmptyError)},
	}
	for _, c := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		useOpenAIBackend(t, server.URL)
		_, err := callChatCompletion(context.Background(), "s", "u")
		if err == nil || !errors.As(err, c.target) {
			t.Fatalf("%s: got %v", c.name, err)
		}
		server.Close()
	}

	// An answer with an empty content field parses but explains nothing; the
	// caller decides what to do, so it must come back without an error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"  "}}]}`))
	}))
	defer server.Close()
	useOpenAIBackend(t, server.URL)
	content, err := callChatCompletion(context.Background(), "s", "u")
	if err != nil || strings.TrimSpace(content) != "" {
		t.Fatalf("got %q, %v", content, err)
	}
}

func TestCallChatCompletionReportsATransportFailure(t *testing.T) {
	useOpenAIBackend(t, "http://127.0.0.1:1/v1")
	if _, err := callChatCompletion(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected a transport error")
	}
}

// Each delta is forwarded to the SSE channel as it arrives — that is the whole
// point of the streaming path — and [DONE] ends the stream.
func TestStreamChatCompletionAccumulatesDeltas(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"**Cause"}}]}`,
		`data: {"choices":[{"delta":{"content":".**"}}]}`,
		`data: {"choices":[{"delta":{"content":""}}]}`,
		`data: {"choices":[]}`,
		`data: not json`,
		`: a comment line`,
		`data: [DONE]`,
		`data: {"choices":[{"delta":{"content":"AFTER DONE"}}]}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(stream))
	}))
	defer server.Close()

	useOpenAIBackend(t, server.URL)

	chunks := make(chan string, 10)
	got, err := streamChatCompletion(context.Background(), "s", "u", chunks)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got != "**Cause.**" {
		t.Fatalf("got %q — content after [DONE] must be ignored", got)
	}
}

func TestStreamChatCompletionReportsANonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key"))
	}))
	defer server.Close()

	useOpenAIBackend(t, server.URL)
	var statusErr *LLMStatusError
	_, err := streamChatCompletion(context.Background(), "s", "u", nil)
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %v", err)
	}
}

func TestStreamChatCompletionRoutesClaudeToAnthropic(t *testing.T) {
	var hit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	got, err := streamChatCompletion(context.Background(), "s", "u", nil)
	if err != nil || got != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
	if hit != "/v1/messages" {
		t.Fatalf("the stream went to %q", hit)
	}
}

func TestStreamChatCompletionReportsATransportFailure(t *testing.T) {
	useOpenAIBackend(t, "http://127.0.0.1:1/v1")
	if _, err := streamChatCompletion(context.Background(), "s", "u", nil); err == nil {
		t.Fatal("expected a transport error")
	}
}
