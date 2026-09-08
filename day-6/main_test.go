package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAgentAskSendsRequestAndParsesResponse(t *testing.T) {
	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.String() != "https://example.test/chat/completions" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected content-type: %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected accept header: %q", r.Header.Get("Accept"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		body := `{
			"choices": [{
				"finish_reason": "stop",
				"message": {"content": "  Агент получил ответ.  "}
			}],
			"usage": {"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7}
		}`
		return response(http.StatusOK, body), nil
	})}

	agent := NewAgent(AgentConfig{
		APIKey:      "test-key",
		BaseURL:     "https://example.test/",
		Model:       "deepseek-v4-flash",
		System:      "system message",
		Timeout:     time.Second,
		MaxTokens:   128,
		Temperature: 0.2,
	}, client)

	answer, err := agent.Ask(context.Background(), "  Say hello  ")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if answer.Content != "Агент получил ответ." {
		t.Fatalf("unexpected answer: %q", answer.Content)
	}
	if answer.Usage == nil || answer.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected usage: %+v", answer.Usage)
	}
	if answer.FinishReason != "stop" {
		t.Fatalf("unexpected finish reason: %q", answer.FinishReason)
	}
	if got.Model != "deepseek-v4-flash" {
		t.Fatalf("unexpected model: %q", got.Model)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "system message" {
		t.Fatalf("unexpected system message: %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "Say hello" {
		t.Fatalf("unexpected user message: %+v", got.Messages[1])
	}
	if got.Thinking == nil || got.Thinking.Type != "disabled" {
		t.Fatalf("thinking should be disabled by default: %+v", got.Thinking)
	}
	if got.Stream {
		t.Fatal("stream should be false")
	}
}

func TestAgentAskOmitsThinkingWhenEnabled(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.Thinking != nil {
			t.Fatalf("thinking should be omitted when enabled: %+v", got.Thinking)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}

	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		Model:    "deepseek-v4-flash",
		Timeout:  time.Second,
		Thinking: true,
	}, client)

	if _, err := agent.Ask(context.Background(), "hello"); err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
}

func TestAgentAskRequiresPrompt(t *testing.T) {
	agent := NewAgent(AgentConfig{APIKey: "test-key"}, nil)

	_, err := agent.Ask(context.Background(), " \n ")
	if err == nil || !strings.Contains(err.Error(), "prompt") && !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("expected prompt error, got: %v", err)
	}
}

func TestAgentAskRequiresAPIKey(t *testing.T) {
	agent := NewAgent(AgentConfig{}, nil)

	_, err := agent.Ask(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Fatalf("expected api key error, got: %v", err)
	}
}

func TestAgentAskReturnsHTTPError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusUnauthorized, `{"error":{"message":"bad key"}}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
	}, client)

	_, err := agent.Ask(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("expected HTTP error, got: %v", err)
	}
}

func TestAgentAskReturnsInvalidJSONError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `not-json`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
	}, client)

	_, err := agent.Ask(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("expected JSON error, got: %v", err)
	}
}

func TestAgentAskReturnsEmptyChoicesError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
	}, client)

	_, err := agent.Ask(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "choices") {
		t.Fatalf("expected choices error, got: %v", err)
	}
}

func TestReadConfigReadsPromptFromStdin(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-pro")
	t.Setenv("DEEPSEEK_BASE_URL", "https://example.test/")

	cfg, err := readConfig([]string{}, strings.NewReader("Explain Go agents"))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if cfg.Prompt != "Explain Go agents" {
		t.Fatalf("unexpected prompt: %q", cfg.Prompt)
	}
	if cfg.Model != "deepseek-v4-pro" {
		t.Fatalf("unexpected model: %q", cfg.Model)
	}
	if cfg.BaseURL != "https://example.test" {
		t.Fatalf("unexpected base URL: %q", cfg.BaseURL)
	}
}

func response(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
