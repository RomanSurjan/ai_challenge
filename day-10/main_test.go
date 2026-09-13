package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

func TestSlidingWindowStrategyKeepsOnlyRecentHistory(t *testing.T) {
	strategy := NewSlidingWindowStrategy(3)
	history := []chatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
	}

	got, err := strategy.Build(ContextInput{
		System:     "system message",
		History:    history,
		UserPrompt: "current",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected context messages:\n got: %+v\nwant: %+v", got.Messages, want)
	}
	if got.Metadata.Strategy != StrategySliding || got.Metadata.WindowMessages != 3 {
		t.Fatalf("unexpected metadata: %+v", got.Metadata)
	}
	if got.Metadata.HistoryMessages != 5 || got.Metadata.ContextMessages != 5 {
		t.Fatalf("unexpected counts: %+v", got.Metadata)
	}
}

func TestContextMetadataJSONUsesStrategySpecificFields(t *testing.T) {
	sliding, err := json.Marshal(ContextMetadata{
		Strategy:        StrategySliding,
		WindowMessages:  0,
		HistoryMessages: 2,
		ContextMessages: 2,
	})
	if err != nil {
		t.Fatalf("marshal sliding metadata: %v", err)
	}
	if !strings.Contains(string(sliding), `"window_messages":0`) || strings.Contains(string(sliding), `"facts_count"`) {
		t.Fatalf("unexpected sliding metadata JSON: %s", sliding)
	}

	facts, err := json.Marshal(ContextMetadata{
		Strategy:        StrategyFacts,
		FactsCount:      0,
		HistoryMessages: 2,
		ContextMessages: 2,
	})
	if err != nil {
		t.Fatalf("marshal facts metadata: %v", err)
	}
	if strings.Contains(string(facts), `"window_messages"`) || !strings.Contains(string(facts), `"facts_count":0`) {
		t.Fatalf("unexpected facts metadata JSON: %s", facts)
	}
}

func TestAgentAskUsesSlidingWindow(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), []chatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}); err != nil {
		t.Fatalf("save history: %v", err)
	}

	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		System:   "system message",
		Timeout:  time.Second,
		Memory:   store,
		Strategy: StrategySliding,
		Window:   2,
	}, client)

	answer, err := agent.Ask(context.Background(), "current")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}

	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected request messages:\n got: %+v\nwant: %+v", got.Messages, want)
	}
	if answer.Context.Strategy != StrategySliding || answer.Context.WindowMessages != 2 {
		t.Fatalf("unexpected context metadata: %+v", answer.Context)
	}
	if answer.Context.HistoryMessages != 2 || answer.Context.ContextMessages != 4 {
		t.Fatalf("unexpected context counts: %+v", answer.Context)
	}

	history, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	wantHistory := []chatMessage{
		{Role: "user", Content: "current"},
		{Role: "assistant", Content: "ok"},
	}
	if !sameMessages(history, wantHistory) {
		t.Fatalf("unexpected saved history:\n got: %+v\nwant: %+v", history, wantHistory)
	}
}

func TestAgentAskAllowsExplicitZeroWindow(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), []chatMessage{
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: "old answer"},
	}); err != nil {
		t.Fatalf("save history: %v", err)
	}

	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		System:   "system message",
		Timeout:  time.Second,
		Memory:   store,
		Strategy: StrategySliding,
		Window:   0,
	}, client)

	answer, err := agent.Ask(context.Background(), "current")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}

	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected request messages:\n got: %+v\nwant: %+v", got.Messages, want)
	}
	if answer.Context.HistoryMessages != 0 || answer.Context.ContextMessages != 2 {
		t.Fatalf("unexpected context counts: %+v", answer.Context)
	}

	history, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history should be empty with window=0: %+v", history)
	}
}

func TestAgentAskTrimsSavedHistoryToWindowMessages(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), []chatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}); err != nil {
		t.Fatalf("save history: %v", err)
	}

	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		System:   "system message",
		Timeout:  time.Second,
		Memory:   store,
		Strategy: StrategySliding,
		Window:   3,
	}, client)

	answer, err := agent.Ask(context.Background(), "current")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}

	wantContext := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, wantContext) {
		t.Fatalf("unexpected request messages:\n got: %+v\nwant: %+v", got.Messages, wantContext)
	}

	history, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	wantHistory := []chatMessage{
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "current"},
		{Role: "assistant", Content: "ok"},
	}
	if !sameMessages(history, wantHistory) {
		t.Fatalf("unexpected saved history:\n got: %+v\nwant: %+v", history, wantHistory)
	}
	if answer.Context.HistoryMessages != len(wantHistory) || answer.Context.ContextMessages != len(wantContext) {
		t.Fatalf("unexpected context counts: %+v", answer.Context)
	}
}

func TestExtractFacts(t *testing.T) {
	got := ExtractFacts("Цель: Провести исследование и собрать MVP за неделю\nИдея: ИИ-агент для трекера задач\nТвое имя: Олег\nМое имя: Роман\nГод Крещения Руси: 988\nстрока без факта")
	want := map[string]string{
		"цель":              "Провести исследование и собрать MVP за неделю",
		"идея":              "ИИ-агент для трекера задач",
		"твое имя":          "Олег",
		"мое имя":           "Роман",
		"год крещения руси": "988",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected facts:\n got: %+v\nwant: %+v", got, want)
	}

	if got := ExtractFacts("просто текст\nцель:\nhttps://example.test/page"); len(got) != 0 {
		t.Fatalf("unexpected facts from non-explicit input: %+v", got)
	}
}

func TestMergeFactsUpdatesExistingKey(t *testing.T) {
	got := MergeFacts(
		map[string]string{"цель": "старое значение", "имя": "Роман"},
		map[string]string{"цель": "новое значение"},
	)
	want := map[string]string{"цель": "новое значение", "имя": "Роман"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected merged facts:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestFactsFromMessagesUpdatesOneKeyAndKeepsOthers(t *testing.T) {
	got := FactsFromMessages([]chatMessage{
		{Role: "user", Content: "цель: старое значение\nидея: ИИ-агент для трекера задач\nтвое имя: Олег"},
		{Role: "assistant", Content: "Запомнил"},
		{Role: "user", Content: "цель: новое значение"},
	})
	want := map[string]string{
		"цель":     "новое значение",
		"идея":     "ИИ-агент для трекера задач",
		"твое имя": "Олег",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected facts from messages:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestJSONMessageStoreSavesAndLoadsStateWithFacts(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	want := ConversationState{
		Messages: []chatMessage{
			{Role: "user", Content: "цель: сравнить стратегии\nидея: тестировать память"},
			{Role: "assistant", Content: "Запомнил"},
		},
		Facts: map[string]string{"цель": "сравнить стратегии", "идея": "тестировать память"},
	}

	if err := store.SaveState(context.Background(), want); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got, err := store.LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !sameMessages(got.Messages, want.Messages) || !reflect.DeepEqual(got.Facts, want.Facts) {
		t.Fatalf("unexpected state:\n got: %+v\nwant: %+v", got, want)
	}

	var saved historyFile
	raw, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("decode saved history: %v", err)
	}
	if saved.Version != currentHistoryVersion {
		t.Fatalf("saved history version = %d, want %d", saved.Version, currentHistoryVersion)
	}
}

func TestJSONMessageStoreRecoversMissingFactsFromHistory(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	data := `{
		"version": 3,
		"messages": [
			{"role":"user","content":"Цель: Провести исследование и собрать MVP за неделю\nИдея: ИИ-агент для трекера задач\nТвое имя: Олег\nМое имя: Роман\nГод Крещения Руси: 988"},
			{"role":"assistant","content":"Запомнил"}
		],
		"facts": {"цель":"Провести исследование и собрать MVP за неделю"},
		"updated_at": "2026-09-12T00:00:00Z"
	}`
	if err := os.WriteFile(historyPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	got, err := NewJSONMessageStore(historyPath).LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	wantFacts := map[string]string{
		"цель":              "Провести исследование и собрать MVP за неделю",
		"идея":              "ИИ-агент для трекера задач",
		"твое имя":          "Олег",
		"мое имя":           "Роман",
		"год крещения руси": "988",
	}
	if !reflect.DeepEqual(got.Facts, wantFacts) {
		t.Fatalf("unexpected recovered facts:\n got: %+v\nwant: %+v", got.Facts, wantFacts)
	}
}

func TestJSONMessageStoreLoadsVersion3WithFactsAndBranches(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	data := `{
		"version": 3,
		"messages": [{"role":"user","content":"обычная история"}],
		"facts": {"цель":"сравнить стратегии"},
		"branches": {
			"active_branch_id": "branch-1",
			"checkpoint": [{"role":"assistant","content":"base"}],
			"items": [
				{"id":"main","title":"Main","messages":[{"role":"user","content":"main question"}]},
				{"id":"branch-1","title":"Alternative","messages":[{"role":"assistant","content":"branch answer"}]}
			]
		},
		"updated_at": "2026-09-12T00:00:00Z"
	}`
	if err := os.WriteFile(historyPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	got, err := NewJSONMessageStore(historyPath).LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !sameMessages(got.Messages, []chatMessage{{Role: "user", Content: "обычная история"}}) {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
	if !reflect.DeepEqual(got.Facts, map[string]string{"цель": "сравнить стратегии"}) {
		t.Fatalf("unexpected facts: %+v", got.Facts)
	}
	if got.Branches.ActiveBranchID != "branch-1" || len(got.Branches.Items) != 2 {
		t.Fatalf("unexpected branches: %+v", got.Branches)
	}
	if !sameMessages(got.Branches.Checkpoint, []chatMessage{{Role: "assistant", Content: "base"}}) {
		t.Fatalf("unexpected checkpoint: %+v", got.Branches.Checkpoint)
	}
	if !sameMessages(got.Branches.Items[1].Messages, []chatMessage{{Role: "assistant", Content: "branch answer"}}) {
		t.Fatalf("unexpected branch messages: %+v", got.Branches.Items[1].Messages)
	}
}

func TestJSONMessageStoreLoadsVersion1WithoutFacts(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	data := `{
		"version": 1,
		"messages": [{"role":"user","content":"старый формат"}],
		"updated_at": "2026-09-12T00:00:00Z"
	}`
	if err := os.WriteFile(historyPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	got, err := NewJSONMessageStore(historyPath).LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	wantMessages := []chatMessage{{Role: "user", Content: "старый формат"}}
	if !sameMessages(got.Messages, wantMessages) {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
	if len(got.Facts) != 0 {
		t.Fatalf("facts should be empty for v1 history: %+v", got.Facts)
	}
}

func TestJSONMessageStoreRejectsUnknownVersion(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	data := `{
		"version": 99,
		"messages": [{"role":"user","content":"future format"}],
		"updated_at": "2026-09-12T00:00:00Z"
	}`
	if err := os.WriteFile(historyPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	_, err := NewJSONMessageStore(historyPath).LoadState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "неподдерживаемая версия истории: 99") || !strings.Contains(err.Error(), "1, 2, 3") {
		t.Fatalf("expected clear version error, got: %v", err)
	}
}

func TestFactsStrategyAddsFactsBlockWithoutHistoryWindow(t *testing.T) {
	strategy := NewFactsStrategy()
	got, err := strategy.Build(ContextInput{
		System: "system message",
		History: []chatMessage{
			{Role: "user", Content: "u1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "u2"},
		},
		Facts:      map[string]string{"имя": "Роман", "цель": "сравнить стратегии"},
		UserPrompt: "current",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "system", Content: "Sticky facts:\n- имя: Роман\n- цель: сравнить стратегии"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected context messages:\n got: %+v\nwant: %+v", got.Messages, want)
	}
	if got.Metadata.Strategy != StrategyFacts || got.Metadata.WindowMessages != 0 || got.Metadata.FactsCount != 2 {
		t.Fatalf("unexpected metadata: %+v", got.Metadata)
	}
	if got.Metadata.HistoryMessages != 3 || got.Metadata.ContextMessages != 3 {
		t.Fatalf("unexpected metadata: %+v", got.Metadata)
	}
}

func TestFactsStrategyOmitsEmptyFactsBlock(t *testing.T) {
	strategy := NewFactsStrategy()
	got, err := strategy.Build(ContextInput{
		System:     "system message",
		UserPrompt: "current",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected context messages:\n got: %+v\nwant: %+v", got.Messages, want)
	}
}

func TestFactsStrategyIgnoresWindowMessages(t *testing.T) {
	input := ContextInput{
		System: "system message",
		History: []chatMessage{
			{Role: "user", Content: "u1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "u2"},
			{Role: "assistant", Content: "a2"},
		},
		Facts:      map[string]string{"цель": "сравнить стратегии"},
		UserPrompt: "current",
	}

	zeroWindow, err := NewContextStrategy(StrategyFacts, 0)
	if err != nil {
		t.Fatalf("NewContextStrategy returned error: %v", err)
	}
	largeWindow, err := NewContextStrategy(StrategyFacts, 100)
	if err != nil {
		t.Fatalf("NewContextStrategy returned error: %v", err)
	}
	negativeWindow, err := NewContextStrategy(StrategyFacts, -5)
	if err != nil {
		t.Fatalf("facts strategy should ignore window validation, got: %v", err)
	}

	zeroOutput, err := zeroWindow.Build(input)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	largeOutput, err := largeWindow.Build(input)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	negativeOutput, err := negativeWindow.Build(input)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if !sameMessages(zeroOutput.Messages, largeOutput.Messages) || !sameMessages(zeroOutput.Messages, negativeOutput.Messages) {
		t.Fatalf("window should not affect facts context:\n zero: %+v\nlarge: %+v\nnegative: %+v", zeroOutput.Messages, largeOutput.Messages, negativeOutput.Messages)
	}
	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "system", Content: "Sticky facts:\n- цель: сравнить стратегии"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(zeroOutput.Messages, want) {
		t.Fatalf("unexpected facts context:\n got: %+v\nwant: %+v", zeroOutput.Messages, want)
	}
}

func TestAgentAskWithFactsStrategyUpdatesFactsAndKeepsFullHistory(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.SaveState(context.Background(), ConversationState{
		Messages: []chatMessage{
			{Role: "user", Content: "u1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "u2"},
			{Role: "assistant", Content: "a2"},
		},
		Facts: map[string]string{"имя": "Роман"},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		System:   "system message",
		Timeout:  time.Second,
		Memory:   store,
		Strategy: StrategyFacts,
		Window:   2,
	}, client)

	answer, err := agent.Ask(context.Background(), "цель: сравнить стратегии")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}

	wantContext := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "system", Content: "Sticky facts:\n- имя: Роман"},
		{Role: "user", Content: "цель: сравнить стратегии"},
	}
	if !sameMessages(got.Messages, wantContext) {
		t.Fatalf("unexpected request messages:\n got: %+v\nwant: %+v", got.Messages, wantContext)
	}
	if answer.Context.Strategy != StrategyFacts || answer.Context.WindowMessages != 0 || answer.Context.FactsCount != 2 || answer.Context.HistoryMessages != 6 || answer.Context.ContextMessages != 3 {
		t.Fatalf("unexpected context metadata: %+v", answer.Context)
	}

	state, err := store.LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(state.Messages) != 6 {
		t.Fatalf("facts strategy should keep full history, got: %+v", state.Messages)
	}
	wantFacts := map[string]string{"имя": "Роман", "цель": "сравнить стратегии"}
	if !reflect.DeepEqual(state.Facts, wantFacts) {
		t.Fatalf("unexpected facts:\n got: %+v\nwant: %+v", state.Facts, wantFacts)
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

func TestAgentAskDoesNotSaveFactsOnAPIError(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusInternalServerError, `{"error":{"message":"temporary failure"}}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		Timeout:  time.Second,
		Memory:   store,
		Strategy: StrategyFacts,
		Window:   2,
	}, client)

	_, err := agent.Ask(context.Background(), "цель: не сохранять")
	if err == nil {
		t.Fatal("expected API error")
	}
	state, loadErr := store.LoadState(context.Background())
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if len(state.Messages) != 0 || len(state.Facts) != 0 {
		t.Fatalf("state should stay empty after API error: %+v", state)
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

func TestFactsAPIHandlerReturnsSavedFacts(t *testing.T) {
	store := NewJSONMessageStore(filepath.Join(t.TempDir(), "history.json"))
	want := map[string]string{"цель": "сравнить стратегии"}
	if err := store.SaveState(context.Background(), ConversationState{Facts: want}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	agent := NewAgent(AgentConfig{Memory: store}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/facts", nil)
	rec := httptest.NewRecorder()
	factsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var got factsAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !reflect.DeepEqual(got.Facts, want) {
		t.Fatalf("unexpected facts:\n got: %+v\nwant: %+v", got.Facts, want)
	}
}

func TestFactsAPIHandlerReturnsSavedFactsAfterRestart(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	data := `{
		"version": 3,
		"messages": [
			{"role":"user","content":"Цель: Провести исследование и собрать MVP за неделю\nИдея: ИИ-агент для трекера задач\nТвое имя: Олег\nМое имя: Роман\nГод Крещения Руси: 988"},
			{"role":"assistant","content":"Запомнил"}
		],
		"facts": {"цель":"Провести исследование и собрать MVP за неделю"},
		"updated_at": "2026-09-12T00:00:00Z"
	}`
	if err := os.WriteFile(historyPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}
	restartedAgent := NewAgent(AgentConfig{Memory: NewJSONMessageStore(historyPath)}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/facts", nil)
	rec := httptest.NewRecorder()
	factsAPIHandler(restartedAgent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var got factsAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := map[string]string{
		"цель":              "Провести исследование и собрать MVP за неделю",
		"идея":              "ИИ-агент для трекера задач",
		"твое имя":          "Олег",
		"мое имя":           "Роман",
		"год крещения руси": "988",
	}
	if !reflect.DeepEqual(got.Facts, want) {
		t.Fatalf("unexpected facts after restart:\n got: %+v\nwant: %+v", got.Facts, want)
	}
}

func TestSettingsAPIHandlerUpdatesSlidingWindow(t *testing.T) {
	agent := NewAgent(AgentConfig{Strategy: StrategySliding, Window: 8}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"strategy":"sliding","window_messages":3}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	settingsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	var got RuntimeSettings
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Strategy != StrategySliding || got.WindowMessages != 3 {
		t.Fatalf("unexpected settings: %+v", got)
	}
	if agent.Settings().WindowMessages != 3 {
		t.Fatalf("agent settings were not updated: %+v", agent.Settings())
	}
}

func TestSettingsAPIHandlerUpdatesFactsStrategy(t *testing.T) {
	agent := NewAgent(AgentConfig{Strategy: StrategySliding, Window: 8}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"strategy":"facts"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	settingsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if agent.Settings().Strategy != StrategyFacts {
		t.Fatalf("agent settings were not updated: %+v", agent.Settings())
	}
}

func TestSettingsAPIHandlerAcceptsFactsWindowForCompatibility(t *testing.T) {
	agent := NewAgent(AgentConfig{Strategy: StrategySliding, Window: 8}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"strategy":"facts","window_messages":4}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	settingsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if agent.Settings().Strategy != StrategyFacts || agent.Settings().WindowMessages != 4 {
		t.Fatalf("agent settings were not updated compatibly: %+v", agent.Settings())
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

func TestReadConfigReadsContextStrategyFlags(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	cfg, err := readConfig([]string{"-strategy", "sliding", "-window", "5", "-prompt", "hello"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if cfg.Strategy != StrategySliding {
		t.Fatalf("unexpected strategy: %q", cfg.Strategy)
	}
	if cfg.Window != 5 {
		t.Fatalf("unexpected window: %d", cfg.Window)
	}
	if cfg.HistoryPath != defaultHistoryPath {
		t.Fatalf("unexpected history path: %q", cfg.HistoryPath)
	}
}

func TestReadConfigReadsFactsStrategyFlag(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	cfg, err := readConfig([]string{"-strategy", "facts", "-window", "5", "-prompt", "hello"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if cfg.Strategy != StrategyFacts {
		t.Fatalf("unexpected strategy: %q", cfg.Strategy)
	}
}

func TestReadConfigRejectsNegativeWindow(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	_, err := readConfig([]string{"-window", "-1", "-prompt", "hello"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "окна") {
		t.Fatalf("expected window error, got: %v", err)
	}
}

func TestBranchingStrategyBuildsContextFromCheckpointAndActiveBranch(t *testing.T) {
	strategy := NewBranchingStrategy()
	got, err := strategy.Build(ContextInput{
		System: "system message",
		Branches: BranchState{
			ActiveBranchID: "main",
			Checkpoint: []chatMessage{
				{Role: "user", Content: "base question"},
				{Role: "assistant", Content: "base answer"},
			},
			Items: []ConversationBranch{{
				ID:    "main",
				Title: "Main",
				Messages: []chatMessage{
					{Role: "user", Content: "branch question"},
					{Role: "assistant", Content: "branch answer"},
				},
			}},
		},
		UserPrompt: "current",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "base question"},
		{Role: "assistant", Content: "base answer"},
		{Role: "user", Content: "branch question"},
		{Role: "assistant", Content: "branch answer"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected branching context:\n got: %+v\nwant: %+v", got.Messages, want)
	}
	if got.Metadata.Strategy != StrategyBranching || got.Metadata.BranchID != "main" || got.Metadata.CheckpointMessages != 2 || got.Metadata.BranchMessages != 2 {
		t.Fatalf("unexpected branching metadata: %+v", got.Metadata)
	}
	if got.Metadata.ContextMessages != 6 || got.Metadata.HistoryMessages != 4 {
		t.Fatalf("unexpected branching counts: %+v", got.Metadata)
	}
}

func TestAgentAskWithBranchingSavesOnlyActiveBranchAndIgnoresWindow(t *testing.T) {
	store := NewJSONMessageStore(filepath.Join(t.TempDir(), "history.json"))
	topLevelMessages := []chatMessage{
		{Role: "user", Content: "ordinary history"},
		{Role: "assistant", Content: "ordinary answer"},
	}
	if err := store.SaveState(context.Background(), ConversationState{
		Messages: topLevelMessages,
		Facts:    map[string]string{"имя": "Роман"},
		Branches: BranchState{
			ActiveBranchID: "main",
			Checkpoint: []chatMessage{
				{Role: "user", Content: "base question"},
				{Role: "assistant", Content: "base answer"},
			},
			Items: []ConversationBranch{{
				ID:    "main",
				Title: "Main",
				Messages: []chatMessage{
					{Role: "user", Content: "branch question"},
					{Role: "assistant", Content: "branch answer"},
				},
			}},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"branch ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		System:   "system message",
		Timeout:  time.Second,
		Memory:   store,
		Strategy: StrategyBranching,
		Window:   1,
	}, client)

	answer, err := agent.Ask(context.Background(), "цель: не менять facts")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}

	wantContext := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "base question"},
		{Role: "assistant", Content: "base answer"},
		{Role: "user", Content: "branch question"},
		{Role: "assistant", Content: "branch answer"},
		{Role: "user", Content: "цель: не менять facts"},
	}
	if !sameMessages(got.Messages, wantContext) {
		t.Fatalf("branching should not apply sliding window:\n got: %+v\nwant: %+v", got.Messages, wantContext)
	}
	if answer.Context.Strategy != StrategyBranching || answer.Context.BranchID != "main" || answer.Context.CheckpointMessages != 2 || answer.Context.BranchMessages != 4 || answer.Context.HistoryMessages != 6 {
		t.Fatalf("unexpected context metadata: %+v", answer.Context)
	}

	state, err := store.LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !sameMessages(state.Messages, topLevelMessages) {
		t.Fatalf("branching should not change ordinary history:\n got: %+v\nwant: %+v", state.Messages, topLevelMessages)
	}
	if !reflect.DeepEqual(state.Facts, map[string]string{"имя": "Роман"}) {
		t.Fatalf("branching should not change facts: %+v", state.Facts)
	}
	wantBranchMessages := []chatMessage{
		{Role: "user", Content: "branch question"},
		{Role: "assistant", Content: "branch answer"},
		{Role: "user", Content: "цель: не менять facts"},
		{Role: "assistant", Content: "branch ok"},
	}
	if !sameMessages(state.Branches.Items[0].Messages, wantBranchMessages) {
		t.Fatalf("unexpected active branch messages:\n got: %+v\nwant: %+v", state.Branches.Items[0].Messages, wantBranchMessages)
	}
}

func TestBranchingDoesNotSaveTurnOnAPIError(t *testing.T) {
	store := NewJSONMessageStore(filepath.Join(t.TempDir(), "history.json"))
	initialBranchMessages := []chatMessage{{Role: "user", Content: "before"}}
	if err := store.SaveState(context.Background(), ConversationState{
		Branches: BranchState{
			ActiveBranchID: "main",
			Items: []ConversationBranch{{
				ID:       "main",
				Title:    "Main",
				Messages: initialBranchMessages,
			}},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusInternalServerError, `{"error":{"message":"temporary failure"}}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:   "test-key",
		BaseURL:  "https://example.test",
		Timeout:  time.Second,
		Memory:   store,
		Strategy: StrategyBranching,
	}, client)

	_, err := agent.Ask(context.Background(), "should not save")
	if err == nil {
		t.Fatal("expected API error")
	}
	state, loadErr := store.LoadState(context.Background())
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if !sameMessages(state.Branches.Items[0].Messages, initialBranchMessages) {
		t.Fatalf("branch should stay unchanged after API error: %+v", state.Branches.Items[0].Messages)
	}
}

func TestSettingsAPIHandlerAcceptsBranchingWithoutWindowDependency(t *testing.T) {
	agent := NewAgent(AgentConfig{Strategy: StrategySliding, Window: 8}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"strategy":"branching","window_messages":-4}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	settingsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if agent.Settings().Strategy != StrategyBranching || agent.Settings().WindowMessages != -4 {
		t.Fatalf("agent settings were not updated compatibly: %+v", agent.Settings())
	}
}

func TestBranchesAPIManagesCheckpointBranchesSwitchingAndDeletion(t *testing.T) {
	store := NewJSONMessageStore(filepath.Join(t.TempDir(), "history.json"))
	legacyMessages := []chatMessage{
		{Role: "user", Content: "legacy question"},
		{Role: "assistant", Content: "legacy answer"},
	}
	if err := store.SaveState(context.Background(), ConversationState{Messages: legacyMessages}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	agent := NewAgent(AgentConfig{Memory: store, Strategy: StrategyBranching}, nil)

	getReq := httptest.NewRequest(http.MethodGet, "/api/branches", nil)
	getRec := httptest.NewRecorder()
	branchesAPIHandler(agent).ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected GET status: %d", getRec.Code)
	}
	got := decodeBranchesResponse(t, getRec.Body)
	if got.ActiveBranchID != "main" || got.CheckpointMessages != 0 || len(got.Branches) != 1 || got.Branches[0].MessageCount != 2 {
		t.Fatalf("unexpected initial branches: %+v", got)
	}

	checkpointReq := httptest.NewRequest(http.MethodPost, "/api/branches/checkpoint", nil)
	checkpointRec := httptest.NewRecorder()
	branchCheckpointAPIHandler(agent).ServeHTTP(checkpointRec, checkpointReq)
	if checkpointRec.Code != http.StatusOK {
		t.Fatalf("unexpected checkpoint status: %d", checkpointRec.Code)
	}
	got = decodeBranchesResponse(t, checkpointRec.Body)
	if got.CheckpointMessages != 2 || len(got.Branches) != 1 || got.Branches[0].MessageCount != 0 {
		t.Fatalf("checkpoint should move active dialog into checkpoint: %+v", got)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/branches", strings.NewReader(`{"title":"Alternative"}`))
	createRec := httptest.NewRecorder()
	branchesAPIHandler(agent).ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("unexpected create status: %d", createRec.Code)
	}
	got = decodeBranchesResponse(t, createRec.Body)
	if got.ActiveBranchID != "branch-1" || len(got.Branches) != 2 || got.Branches[1].Title != "Alternative" {
		t.Fatalf("new branch should become active: %+v", got)
	}

	switchReq := httptest.NewRequest(http.MethodPost, "/api/branches/active", strings.NewReader(`{"id":"main"}`))
	switchRec := httptest.NewRecorder()
	branchActiveAPIHandler(agent).ServeHTTP(switchRec, switchReq)
	if switchRec.Code != http.StatusOK {
		t.Fatalf("unexpected switch status: %d", switchRec.Code)
	}
	got = decodeBranchesResponse(t, switchRec.Body)
	if got.ActiveBranchID != "main" {
		t.Fatalf("branch should switch to main: %+v", got)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/branches/branch-1", nil)
	deleteRec := httptest.NewRecorder()
	branchDeleteAPIHandler(agent).ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("unexpected delete status: %d", deleteRec.Code)
	}
	got = decodeBranchesResponse(t, deleteRec.Body)
	if len(got.Branches) != 1 || got.Branches[0].ID != "main" {
		t.Fatalf("unexpected branches after delete: %+v", got)
	}

	lastDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/branches/main", nil)
	lastDeleteRec := httptest.NewRecorder()
	branchDeleteAPIHandler(agent).ServeHTTP(lastDeleteRec, lastDeleteReq)
	if lastDeleteRec.Code != http.StatusBadRequest {
		t.Fatalf("deleting last branch should fail, status: %d", lastDeleteRec.Code)
	}
}

func TestHistoryAPIHandlerReturnsVisibleBranchingHistory(t *testing.T) {
	store := NewJSONMessageStore(filepath.Join(t.TempDir(), "history.json"))
	want := []chatMessage{
		{Role: "user", Content: "base question"},
		{Role: "assistant", Content: "base answer"},
		{Role: "user", Content: "branch question"},
		{Role: "assistant", Content: "branch answer"},
	}
	if err := store.SaveState(context.Background(), ConversationState{
		Messages: []chatMessage{{Role: "user", Content: "ordinary"}},
		Branches: BranchState{
			ActiveBranchID: "main",
			Checkpoint:     want[:2],
			Items: []ConversationBranch{
				{ID: "main", Title: "Main", Messages: want[2:]},
				{ID: "branch-1", Title: "Alternative", Messages: []chatMessage{{Role: "user", Content: "other"}}},
			},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	agent := NewAgent(AgentConfig{Memory: store, Strategy: StrategyBranching}, nil)

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
		t.Fatalf("unexpected visible branching history:\n got: %+v\nwant: %+v", got.Messages, want)
	}
}

func TestJSONMessageStoreLoadsVersion2WithoutBranches(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	data := `{
		"version": 2,
		"messages": [{"role":"user","content":"старый v2"}],
		"facts": {"цель":"сравнить стратегии"},
		"updated_at": "2026-09-12T00:00:00Z"
	}`
	if err := os.WriteFile(historyPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	got, err := NewJSONMessageStore(historyPath).LoadState(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !sameMessages(got.Messages, []chatMessage{{Role: "user", Content: "старый v2"}}) {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
	if !reflect.DeepEqual(got.Facts, map[string]string{"цель": "сравнить стратегии"}) {
		t.Fatalf("unexpected facts: %+v", got.Facts)
	}
	if len(got.Branches.Items) != 0 {
		t.Fatalf("v2 history should not require branches: %+v", got.Branches)
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

func decodeBranchesResponse(t *testing.T, body io.Reader) branchesAPIResponse {
	t.Helper()
	var got branchesAPIResponse
	if err := json.NewDecoder(body).Decode(&got); err != nil {
		t.Fatalf("decode branches response: %v", err)
	}
	return got
}
