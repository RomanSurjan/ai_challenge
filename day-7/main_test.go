package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestAgentAskPersistsHistoryAndReloadsAfterRestart(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	var requests []chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, got)

		switch len(requests) {
		case 1:
			return response(http.StatusOK, `{"choices":[{"message":{"content":"Первый ответ"},"finish_reason":"stop"}]}`), nil
		case 2:
			return response(http.StatusOK, `{"choices":[{"message":{"content":"Второй ответ"},"finish_reason":"stop"}]}`), nil
		default:
			t.Fatalf("unexpected API call #%d", len(requests))
			return nil, nil
		}
	})}

	firstAgent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		System:  "system message",
		Timeout: time.Second,
		Memory:  NewJSONMessageStore(historyPath),
	}, client)
	if _, err := firstAgent.Ask(context.Background(), "первый вопрос"); err != nil {
		t.Fatalf("first Ask returned error: %v", err)
	}

	restartedAgent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		System:  "system message",
		Timeout: time.Second,
		Memory:  NewJSONMessageStore(historyPath),
	}, client)
	if _, err := restartedAgent.Ask(context.Background(), "второй вопрос"); err != nil {
		t.Fatalf("second Ask returned error: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("unexpected request count: %d", len(requests))
	}
	got := requests[1].Messages
	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "первый вопрос"},
		{Role: "assistant", Content: "Первый ответ"},
		{Role: "user", Content: "второй вопрос"},
	}
	if !sameMessages(got, want) {
		t.Fatalf("unexpected messages after restart:\n got: %+v\nwant: %+v", got, want)
	}

	history, err := NewJSONMessageStore(historyPath).Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	wantHistory := []chatMessage{
		{Role: "user", Content: "первый вопрос"},
		{Role: "assistant", Content: "Первый ответ"},
		{Role: "user", Content: "второй вопрос"},
		{Role: "assistant", Content: "Второй ответ"},
	}
	if !sameMessages(history, wantHistory) {
		t.Fatalf("unexpected saved history:\n got: %+v\nwant: %+v", history, wantHistory)
	}
}

func TestAgentAskDoesNotSaveHistoryOnAPIError(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusInternalServerError, `{"error":{"message":"temporary failure"}}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
		Memory:  NewJSONMessageStore(historyPath),
	}, client)

	_, err := agent.Ask(context.Background(), "этот ход не должен сохраниться")
	if err == nil {
		t.Fatal("expected API error")
	}

	history, loadErr := NewJSONMessageStore(historyPath).Load(context.Background())
	if loadErr != nil {
		t.Fatalf("load history: %v", loadErr)
	}
	if len(history) != 0 {
		t.Fatalf("history should stay empty after API error: %+v", history)
	}
}

func TestAgentAskReturnsHistoryLoadErrorWithoutAPICall(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(historyPath, []byte("{broken json"), 0o600); err != nil {
		t.Fatalf("write broken history: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("API should not be called when history cannot be loaded")
		return nil, nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
		Memory:  NewJSONMessageStore(historyPath),
	}, client)

	_, err := agent.Ask(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "историю") {
		t.Fatalf("expected history error, got: %v", err)
	}
}

func TestHistoryAPIHandlerReturnsSavedMessages(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	want := []chatMessage{
		{Role: "user", Content: "вопрос"},
		{Role: "assistant", Content: "ответ"},
	}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatalf("save history: %v", err)
	}
	agent := NewAgent(AgentConfig{Memory: store}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	rec := httptest.NewRecorder()
	historyAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var got historyAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected messages:\n got: %+v\nwant: %+v", got.Messages, want)
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

func sameMessages(got, want []chatMessage) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
