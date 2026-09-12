package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if answer.TokenReport.CurrentRequestTokens == 0 || answer.TokenReport.SystemTokens == 0 || answer.TokenReport.PromptTokens == 0 {
		t.Fatalf("expected local token report, got: %+v", answer.TokenReport)
	}
	if answer.TokenReport.AnswerTokens == 0 || answer.TokenReport.TotalTokens != answer.TokenReport.PromptTokens+answer.TokenReport.AnswerTokens {
		t.Fatalf("unexpected answer token report: %+v", answer.TokenReport)
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

func TestJSONMessageStoreLoadsOldHistoryWithoutSessionUsage(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	oldHistory := `{
		"version": 1,
		"messages": [
			{"role": "user", "content": "старый вопрос"},
			{"role": "assistant", "content": "старый ответ"}
		],
		"updated_at": "2026-09-11T00:00:00Z"
	}`
	if err := os.WriteFile(historyPath, []byte(oldHistory), 0o600); err != nil {
		t.Fatalf("write old history: %v", err)
	}

	state, err := NewJSONMessageStore(historyPath).LoadState(context.Background())
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
	}
	if len(state.Messages) != 2 {
		t.Fatalf("unexpected messages: %+v", state.Messages)
	}
	if state.SessionUsage.API.SuccessfulTurns != 0 || state.SessionUsage.Estimate.SuccessfulTurns != 0 {
		t.Fatalf("old history should load with empty usage, got: %+v", state.SessionUsage)
	}
}

func TestAgentAskAccumulatesAPIUsageAcrossSuccessfulTurns(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	requestCount := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCount++
		switch requestCount {
		case 1:
			return response(http.StatusOK, `{
				"choices": [{"message": {"content": "Первый ответ"}, "finish_reason": "stop"}],
				"usage": {"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7}
			}`), nil
		case 2:
			return response(http.StatusOK, `{
				"choices": [{"message": {"content": "Второй ответ"}, "finish_reason": "stop"}],
				"usage": {
					"prompt_tokens": 5,
					"completion_tokens": 6,
					"total_tokens": 11,
					"completion_tokens_details": {"reasoning_tokens": 2}
				}
			}`), nil
		default:
			t.Fatalf("unexpected API call #%d", requestCount)
			return nil, nil
		}
	})}
	store := NewJSONMessageStore(historyPath)
	agent := NewAgent(AgentConfig{
		APIKey:       "test-key",
		BaseURL:      "https://example.test",
		Timeout:      time.Second,
		ContextLimit: 1000,
		Memory:       store,
	}, client)

	if _, err := agent.Ask(context.Background(), "первый вопрос"); err != nil {
		t.Fatalf("first Ask returned error: %v", err)
	}
	second, err := agent.Ask(context.Background(), "второй вопрос")
	if err != nil {
		t.Fatalf("second Ask returned error: %v", err)
	}

	state, err := store.LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	for name, usage := range map[string]UsageBucket{
		"persisted": state.SessionUsage.API,
		"response":  second.SessionUsage.API,
	} {
		if usage.PromptTokens != 8 || usage.CompletionTokens != 10 || usage.TotalTokens != 18 || usage.SuccessfulTurns != 2 {
			t.Fatalf("%s usage was not accumulated: %+v", name, usage)
		}
		if !usage.ReasoningTokensKnown || usage.ReasoningTokens != 2 {
			t.Fatalf("%s reasoning usage was not accumulated: %+v", name, usage)
		}
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

func TestAgentAskReturnsContextLimitErrorWithoutAPICall(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	initialState := ConversationState{
		Messages: []chatMessage{
			{Role: "user", Content: strings.Repeat("длинная история ", 20)},
			{Role: "assistant", Content: "ответ"},
		},
		SessionUsage: SessionUsage{
			API: UsageBucket{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
				SuccessfulTurns:  1,
			},
		},
	}
	if err := store.SaveState(context.Background(), initialState); err != nil {
		t.Fatalf("save history: %v", err)
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("API should not be called when context limit is exceeded")
		return nil, nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:       "test-key",
		BaseURL:      "https://example.test",
		System:       "system message",
		Timeout:      time.Second,
		ContextLimit: 20,
		Memory:       store,
	}, client)

	_, err := agent.Ask(context.Background(), "новый вопрос")
	if err == nil {
		t.Fatal("expected context limit error")
	}
	limitErr, ok := err.(*ContextLimitError)
	if !ok {
		t.Fatalf("expected ContextLimitError, got: %T %v", err, err)
	}
	if limitErr.Report.OverflowTokens == 0 || limitErr.Report.HistoryTokens == 0 {
		t.Fatalf("unexpected limit report: %+v", limitErr.Report)
	}

	state, loadErr := store.LoadState(context.Background())
	if loadErr != nil {
		t.Fatalf("load history: %v", loadErr)
	}
	if len(state.Messages) != 2 {
		t.Fatalf("history should not be changed after overflow: %+v", state.Messages)
	}
	if state.SessionUsage.API != initialState.SessionUsage.API || state.SessionUsage.Estimate != initialState.SessionUsage.Estimate {
		t.Fatalf("session usage should not be changed after overflow: %+v", state.SessionUsage)
	}
}

func TestAgentAskChecksContextLimitAfterCalibration(t *testing.T) {
	rawReport := EstimatePromptTokenReport("system message", nil, "короткий вопрос", 0, TokenPricing{})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("API should not be called when calibrated context limit is exceeded")
		return nil, nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:       "test-key",
		BaseURL:      "https://example.test",
		System:       "system message",
		Timeout:      time.Second,
		ContextLimit: rawReport.RawPromptTokens + 1,
		Calibration: TokenCalibration{
			PromptMultiplier:     2,
			CompletionMultiplier: 1,
		},
	}, client)

	_, err := agent.Ask(context.Background(), "короткий вопрос")
	if err == nil {
		t.Fatal("expected context limit error")
	}
	limitErr, ok := err.(*ContextLimitError)
	if !ok {
		t.Fatalf("expected ContextLimitError, got: %T %v", err, err)
	}
	if limitErr.Report.RawPromptTokens != rawReport.RawPromptTokens {
		t.Fatalf("unexpected raw prompt tokens: got %d want %d", limitErr.Report.RawPromptTokens, rawReport.RawPromptTokens)
	}
	if limitErr.Report.PromptTokens <= limitErr.Report.ContextLimit || limitErr.Report.OverflowTokens == 0 {
		t.Fatalf("expected calibrated overflow, got: %+v", limitErr.Report)
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

func TestChatAPIHandlerReturnsTokenReport(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{
			"choices": [{"message": {"content": "Ответ"}, "finish_reason": "stop"}],
			"usage": {
				"prompt_tokens": 11,
				"completion_tokens": 2,
				"total_tokens": 13,
				"reasoning_tokens": 1
			}
		}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:       "test-key",
		BaseURL:      "https://example.test",
		System:       "system message",
		Timeout:      time.Second,
		ContextLimit: 1000,
	}, client)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"Привет"}`))
	rec := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got chatAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TokenReport == nil || got.TokenReport.PromptTokens == 0 || got.TokenReport.AnswerTokens == 0 {
		t.Fatalf("expected token report, got: %+v", got.TokenReport)
	}
	if got.Usage == nil || got.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected API usage: %+v", got.Usage)
	}
	if !got.Usage.HasReasoningTokens() || got.Usage.ReasoningTokenCount() != 1 {
		t.Fatalf("unexpected reasoning usage: %+v", got.Usage)
	}
	if got.SessionUsage.API.PromptTokens != 11 || got.SessionUsage.API.CompletionTokens != 2 || got.SessionUsage.API.TotalTokens != 13 {
		t.Fatalf("unexpected session usage: %+v", got.SessionUsage)
	}
}

func TestChatAPIHandlerReturnsEstimatedSessionUsageWhenAPIUsageIsMissing(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"Ответ без usage"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:       "test-key",
		BaseURL:      "https://example.test",
		System:       "system message",
		Timeout:      time.Second,
		ContextLimit: 1000,
	}, client)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"Привет"}`))
	rec := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got chatAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Usage != nil {
		t.Fatalf("usage should be absent, got: %+v", got.Usage)
	}
	if got.SessionUsage.Estimate.SuccessfulTurns != 1 || got.SessionUsage.Estimate.TotalTokens == 0 {
		t.Fatalf("expected estimated session usage, got: %+v", got.SessionUsage)
	}
}

func TestChatPageShowsDetailedTokenMetricLabels(t *testing.T) {
	for _, want := range []string{"Turn", "Session", "оценка"} {
		if !strings.Contains(chatPageHTML, want) {
			t.Fatalf("chat page should contain %q", want)
		}
	}
	for _, removed := range []string{"['Source'", "['Question'"} {
		if strings.Contains(chatPageHTML, removed) {
			t.Fatalf("chat page should not render %q as a metric", removed)
		}
	}
}

func TestReadConfigReadsPromptFromStdin(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-pro")
	t.Setenv("DEEPSEEK_BASE_URL", "https://example.test/")
	t.Setenv("DAY8_HISTORY_PATH", "/tmp/day8-test-history.json")
	t.Setenv("DAY8_CONTEXT_LIMIT", "321")
	t.Setenv("DAY8_INPUT_PRICE_PER_1M", "0.14")
	t.Setenv("DAY8_OUTPUT_PRICE_PER_1M", "0.28")

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
	if cfg.HistoryPath != "/tmp/day8-test-history.json" {
		t.Fatalf("unexpected history path: %q", cfg.HistoryPath)
	}
	if cfg.ContextLimit != 321 {
		t.Fatalf("unexpected context limit: %d", cfg.ContextLimit)
	}
	if cfg.Pricing.InputPer1M != 0.14 || cfg.Pricing.OutputPer1M != 0.28 {
		t.Fatalf("unexpected pricing: %+v", cfg.Pricing)
	}
}

func TestReadConfigReadsTokenMultipliers(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DAY8_PROMPT_TOKEN_MULTIPLIER", "1.25")
	t.Setenv("DAY8_COMPLETION_TOKEN_MULTIPLIER", "1.5")

	cfg, err := readConfig([]string{
		"-prompt-token-multiplier", "1.75",
		"-completion-token-multiplier", "2",
	}, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if cfg.Calibration.PromptMultiplier != 1.75 || cfg.Calibration.CompletionMultiplier != 2 {
		t.Fatalf("unexpected calibration: %+v", cfg.Calibration)
	}
}

func TestReadConfigAllowsCalibrationWithoutPrompt(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	cfg, err := readConfig([]string{"-calibrate-tokens"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if !cfg.CalibrateTokens {
		t.Fatal("expected calibration mode")
	}
}

func TestReadConfigAllowsDemoWithoutAPIKeyOrPrompt(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")

	cfg, err := readConfig([]string{"-demo-tokens"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if !cfg.DemoTokens {
		t.Fatal("expected demo mode")
	}
}

func TestTokenMultiplierAppliesToEstimate(t *testing.T) {
	raw := EstimatePromptTokenReport("system", nil, "hello", 0, TokenPricing{})
	report := EstimatePromptTokenReportWithCalibration("system", nil, "hello", 0, TokenPricing{}, TokenCalibration{
		PromptMultiplier:     1.5,
		CompletionMultiplier: 2,
	})
	if report.RawPromptTokens != raw.PromptTokens {
		t.Fatalf("unexpected raw prompt tokens: got %d want %d", report.RawPromptTokens, raw.PromptTokens)
	}
	if report.PromptTokens != applyTokenMultiplier(raw.PromptTokens, 1.5) {
		t.Fatalf("prompt multiplier was not applied: %+v", report)
	}

	report = AddAnswerTokensWithCalibration(report, "short answer", TokenPricing{}, TokenCalibration{
		PromptMultiplier:     1.5,
		CompletionMultiplier: 2,
	})
	if report.AnswerTokens != applyTokenMultiplier(report.RawAnswerTokens, 2) {
		t.Fatalf("completion multiplier was not applied: %+v", report)
	}
	if report.TotalTokens != report.PromptTokens+report.AnswerTokens {
		t.Fatalf("unexpected total tokens: %+v", report)
	}
}

func TestPrintTokenReportLabelsFallbackAsEstimate(t *testing.T) {
	report := EstimatePromptTokenReportWithCalibration("system", nil, "hello", 1000, TokenPricing{}, DefaultTokenCalibration())
	report = AddAnswerTokensWithCalibration(report, "answer", TokenPricing{}, DefaultTokenCalibration())
	session := AddTurnToSessionUsage(SessionUsage{}, nil, report)

	var out bytes.Buffer
	printTokenReport(&out, report, nil, session, "")
	text := out.String()
	for _, want := range []string{
		"question (estimate)",
		"turn (estimate)",
		"session (estimate)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("token report should contain %q:\n%s", want, text)
		}
	}
}

func TestTokenUsageDetectsZeroReasoningWhenFieldIsPresent(t *testing.T) {
	zero := 0
	usage := tokenUsage{
		PromptTokens:     5,
		CompletionTokens: 2,
		TotalTokens:      7,
		CompletionTokensDetails: &tokenUsageDetails{
			ReasoningTokens: &zero,
		},
	}

	if !usage.HasReasoningTokens() {
		t.Fatal("reasoning tokens should be known when the field is present")
	}
	if usage.ReasoningTokenCount() != 0 {
		t.Fatalf("unexpected reasoning token count: %d", usage.ReasoningTokenCount())
	}
}

func TestTokenCalibrationSummaryComparesLocalAndAPIUsage(t *testing.T) {
	results := []tokenCalibrationResult{
		{LocalPrompt: 10, APIPrompt: 15, PromptDiff: 5, LocalAnswer: 5, APIAnswer: 10},
		{LocalPrompt: 30, APIPrompt: 45, PromptDiff: 15, LocalAnswer: 15, APIAnswer: 30},
	}

	summary := summarizeTokenCalibration(results)
	if summary.PromptMultiplier != 1.5 {
		t.Fatalf("unexpected prompt multiplier: %v", summary.PromptMultiplier)
	}
	if summary.CompletionMultiplier != 2 {
		t.Fatalf("unexpected completion multiplier: %v", summary.CompletionMultiplier)
	}
	if summary.MeanPromptDeviationPercent != 50 || summary.MaxPromptDeviationPercent != 50 {
		t.Fatalf("unexpected prompt deviations: %+v", summary)
	}
	if summary.MeanAnswerDeviationPercent != 100 || summary.MaxAnswerDeviationPercent != 100 {
		t.Fatalf("unexpected answer deviations: %+v", summary)
	}
}

func TestRunTokenCalibrationUsesFakeHTTPClient(t *testing.T) {
	requestCount := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCount++
		if r.URL.String() != "https://example.test/chat/completions" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}

		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.Stream {
			t.Fatal("stream should be false")
		}
		if got.MaxTokens != 128 {
			t.Fatalf("unexpected max tokens: %d", got.MaxTokens)
		}
		if got.Thinking == nil || got.Thinking.Type != "disabled" {
			t.Fatalf("thinking should be disabled by default: %+v", got.Thinking)
		}

		body := fmt.Sprintf(
			`{"choices":[{"message":{"content":"Ответ %d"},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
			requestCount,
			40+requestCount,
			5+requestCount,
			45+requestCount*2,
		)
		return response(http.StatusOK, body), nil
	})}

	var out bytes.Buffer
	err := runTokenCalibration(context.Background(), &out, AgentConfig{
		APIKey:      "test-key",
		BaseURL:     "https://example.test",
		Model:       "deepseek-v4-flash",
		System:      "system message",
		Timeout:     time.Second,
		MaxTokens:   128,
		Temperature: 0.2,
	}, client)
	if err != nil {
		t.Fatalf("runTokenCalibration returned error: %v", err)
	}
	if requestCount != len(buildTokenCalibrationScenarios("system message")) {
		t.Fatalf("unexpected request count: %d", requestCount)
	}
	text := out.String()
	for _, want := range []string{
		"scenario local_input api_input diff diff_%",
		"russian_short",
		"multi_message_history",
		"prompt_multiplier",
		"completion_multiplier",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("calibration output does not contain %q:\n%s", want, text)
		}
	}
}

func TestEstimateTextTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: " \n\t", want: 0},
		{name: "english", text: "Hello, agent!", want: 4},
		{name: "russian", text: "Привет, агент!", want: 4},
		{name: "numbers", text: "v2 costs 123.45", want: 6},
		{name: "cjk", text: "你好", want: 2},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTextTokens(tt.text); got != tt.want {
				t.Fatalf("EstimateTextTokens(%q)=%d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

func TestRunTokenDemoShowsScenariosAndCosts(t *testing.T) {
	var out bytes.Buffer
	err := runTokenDemo(&out, TokenDemoConfig{
		ContextLimit: 100,
		Pricing: TokenPricing{
			InputPer1M:  1,
			OutputPer1M: 2,
		},
	})
	if err != nil {
		t.Fatalf("runTokenDemo returned error: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Scenario: short",
		"Scenario: long",
		"Scenario: overflow",
		"turn input output overall status",
		"context limit exceeded",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("demo output does not contain %q:\n%s", want, text)
		}
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
