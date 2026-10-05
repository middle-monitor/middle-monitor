package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// anthropicContent is a content block in the Anthropic Messages API, with
// optional cache_control for prompt caching (system prompts are cached).
type anthropicContent struct {
	Type         string              `json:"type"`
	Text         string              `json:"text"`
	CacheControl *anthropicCacheCtrl `json:"cache_control,omitempty"`
}

type anthropicCacheCtrl struct {
	Type string `json:"type"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicReq struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    []anthropicContent `json:"system"`
	Messages  []anthropicMsg     `json:"messages"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func isClaudeModel() bool {
	model := strings.ToLower(envOr("OPENAI_MODEL", ""))
	return strings.Contains(model, "claude") || os.Getenv("ANTHROPIC_API_KEY") != ""
}

func anthropicBaseURL() string {
	return strings.TrimRight(envOr("ANTHROPIC_BASE_URL", "https://api.anthropic.com"), "/")
}

func explainMaxTokens() int {
	if raw := os.Getenv("EXPLAIN_MAX_TOKENS"); raw != "" {
		var n int
		if _, perr := fmt.Sscanf(raw, "%d", &n); perr == nil && n > 0 {
			return n
		}
	}
	return 150
}

// callAnthropicCompletion calls the Anthropic Messages API with prompt caching
// on the system message. The system prompt (identical across calls for a given
// locale/subject) is cached for 5 minutes, reducing input token cost by 90%.
func callAnthropicCompletion(ctx context.Context, system, user string) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := envOr("OPENAI_MODEL", "claude-sonnet-4-6")

	reqBody, _ := json.Marshal(anthropicReq{
		Model:     model,
		MaxTokens: explainMaxTokens(),
		System: []anthropicContent{
			{Type: "text", Text: system, CacheControl: &anthropicCacheCtrl{Type: "ephemeral"}},
		},
		Messages: []anthropicMsg{{Role: "user", Content: user}},
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL()+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w: %w", ErrLLMCall, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return "", &LLMStatusError{Provider: "Anthropic", StatusCode: resp.StatusCode, Body: truncate(string(respBody), 400)}
	}

	var parsed anthropicResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", &LLMDecodeError{Provider: "Anthropic", Err: err, Body: truncate(string(respBody), 400)}
	}
	if parsed.Error != nil {
		return "", &LLMProviderError{Provider: "Anthropic", Message: parsed.Error.Message}
	}
	for _, block := range parsed.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", &LLMEmptyError{Provider: "Anthropic", Detail: "no text content, body: " + truncate(string(respBody), 400)}
}

// streamAnthropicCompletion streams from the Anthropic Messages API with
// prompt caching. Content deltas are accumulated and returned as a full string.
func streamAnthropicCompletion(ctx context.Context, system, user string, _ chan<- string) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := envOr("OPENAI_MODEL", "claude-sonnet-4-6")

	payload := map[string]any{
		"model":      model,
		"max_tokens": explainMaxTokens(),
		"stream":     true,
		"system": []map[string]any{
			{"type": "text", "text": system, "cache_control": map[string]string{"type": "ephemeral"}},
		},
		"messages": []map[string]string{{"role": "user", "content": user}},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL()+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", &LLMStatusError{Provider: "Anthropic", StatusCode: resp.StatusCode, Body: truncate(string(errBody), 300)}
	}

	reader := bufio.NewReader(resp.Body)
	var fullContent strings.Builder
	var eventType string

	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fullContent.String(), readErr
		}
		line = strings.TrimSpace(line)
		if line == "" {
			eventType = ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if eventType != "content_block_delta" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err == nil {
			if chunk.Delta.Type == "text_delta" {
				fullContent.WriteString(chunk.Delta.Text)
			}
		}
	}

	return fullContent.String(), nil
}

// callChatCompletion talks to any OpenAI-compatible endpoint in non-streaming mode.
func callChatCompletion(ctx context.Context, system, user string) (string, error) {
	if isClaudeModel() {
		return callAnthropicCompletion(ctx, system, user)
	}
	baseURL := strings.TrimRight(envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/")
	apiKey := os.Getenv("OPENAI_API_KEY")
	model := envOr("OPENAI_MODEL", "gpt-4o")

	reqBody, _ := json.Marshal(chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0,
		Stream:      false,
		MaxTokens:   explainMaxTokens(),
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrLLMCall, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return "", &LLMStatusError{Provider: "LLM", StatusCode: resp.StatusCode, Body: truncate(string(respBody), 400)}
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", &LLMDecodeError{Provider: "LLM", Err: err, Body: truncate(string(respBody), 400)}
	}
	if parsed.Error != nil {
		return "", &LLMProviderError{Provider: "LLM", Message: parsed.Error.Message}
	}
	if len(parsed.Choices) == 0 {
		return "", &LLMEmptyError{Provider: "LLM", Detail: "no choices, body: " + truncate(string(respBody), 400)}
	}

	content := parsed.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		slog.Warn("llm returned empty content", "body", truncate(string(respBody), 1500))
	}
	return content, nil
}

func streamChatCompletion(ctx context.Context, system, user string, chunkChan chan<- string) (string, error) {
	if isClaudeModel() {
		return streamAnthropicCompletion(ctx, system, user, chunkChan)
	}
	baseURL := strings.TrimRight(envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/")
	apiKey := os.Getenv("OPENAI_API_KEY")
	model := envOr("OPENAI_MODEL", "gpt-4o")

	payload := map[string]any{
		"model":       model,
		"temperature": 0.0,
		"stream":      true,
		"max_tokens":  explainMaxTokens(),
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody []byte
		errBody, _ = io.ReadAll(resp.Body)
		return "", &LLMStatusError{Provider: "LLM", StatusCode: resp.StatusCode, Body: truncate(string(errBody), 300)}
	}

	reader := bufio.NewReader(resp.Body)
	var fullContent strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fullContent.String(), err
		}

		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if len(chunk.Choices) > 0 {
				deltaContent := chunk.Choices[0].Delta.Content
				if deltaContent != "" {
					fullContent.WriteString(deltaContent)
				}
			}
		}
	}

	return fullContent.String(), nil
}
