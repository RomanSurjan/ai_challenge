package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if answer.CompressionReport == nil || answer.CompressionReport.Mode != "disabled" {
		t.Fatalf("expected disabled compression report, got: %+v", answer.CompressionReport)
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

func TestAgentAskUsesCompressedContextWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	history := []chatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", Content: "a4"},
	}
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), history); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)
	if err := summaryStore.Save(context.Background(), ConversationSummary{
		Summary:         "u1/a1/u2/a2 уже обсуждены",
		CoveredMessages: 4,
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}

	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"compressed ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		System:  "system message",
		Timeout: time.Second,
		Memory:  store,
		Summary: summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
	}, client)

	answer, err := agent.Ask(context.Background(), "current")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if answer.CompressionReport == nil || answer.CompressionReport.Mode != "enabled" {
		t.Fatalf("expected enabled compression report, got: %+v", answer.CompressionReport)
	}
	if answer.CompressionReport.SummaryCoversMessages != 4 || answer.CompressionReport.RecentMessagesKept != 4 {
		t.Fatalf("unexpected compression report: %+v", answer.CompressionReport)
	}
	want := []chatMessage{
		{Role: "system", Content: "system message"},
		{Role: "system", Content: summaryMessagePrefix + "\nu1/a1/u2/a2 уже обсуждены"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", Content: "a4"},
		{Role: "user", Content: "current"},
	}
	if !sameMessages(got.Messages, want) {
		t.Fatalf("unexpected compressed request:\n got: %+v\nwant: %+v", got.Messages, want)
	}

	savedHistory, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(savedHistory) != len(history)+2 {
		t.Fatalf("full history should be preserved and extended, got %d messages", len(savedHistory))
	}
}

func TestAgentAskUpdatesSummaryWhenChunkIsReady(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	history := buildCompressionDemoHistory(12)
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), history); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)

	var requests []chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, got)
		switch len(requests) {
		case 1:
			return response(http.StatusOK, `{"choices":[{"message":{"content":"Связная API summary из fake transport."},"finish_reason":"stop"}]}`), nil
		case 2:
			return response(http.StatusOK, `{"choices":[{"message":{"content":"summary updated"},"finish_reason":"stop"}]}`), nil
		default:
			t.Fatalf("unexpected API call #%d", len(requests))
			return nil, nil
		}
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
		Memory:  store,
		Summary: summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
	}, client)

	if _, err := agent.Ask(context.Background(), "обнови summary"); err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("expected separate summary and chat requests, got %d", len(requests))
	}
	if len(requests[0].Messages) != 2 ||
		!strings.Contains(requests[0].Messages[0].Content, "Верни только текст summary") ||
		!strings.Contains(requests[0].Messages[1].Content, "Existing summary") {
		t.Fatalf("summary request should use explicit summarization prompt: %+v", requests[0].Messages)
	}
	chatRequest := requests[1]
	if len(chatRequest.Messages) == 0 || chatRequest.Messages[0].Role != "system" || !strings.Contains(chatRequest.Messages[0].Content, summaryMessagePrefix) {
		t.Fatalf("chat request should include freshly updated summary before completion: %+v", chatRequest.Messages)
	}

	summary, err := summaryStore.Load(context.Background())
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if summary.CoveredMessages != 8 {
		t.Fatalf("unexpected covered messages: %+v", summary)
	}
	if !strings.Contains(summary.Summary, "Связная API summary") {
		t.Fatalf("unexpected summary text: %q", summary.Summary)
	}
	savedHistory, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(savedHistory) != 14 {
		t.Fatalf("full history should contain all messages, got %d", len(savedHistory))
	}
}

func TestChatWithoutPrepareUpdatesSummaryBeforeBuildingPrompt(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	history := buildCompressionDemoHistory(8)
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), history); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)

	var got chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
		Memory:  store,
		Summary: summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
		Summarizer: fixedSummarySummarizer{Text: "Связная тестовая сводка до текущего запроса."},
	}, client)

	answer, err := agent.Ask(context.Background(), "current")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if answer.CompressionReport == nil || answer.CompressionReport.SummaryUpdateStatus != "updated" {
		t.Fatalf("chat should prepare summary on its own, got: %+v", answer.CompressionReport)
	}
	if len(got.Messages) == 0 || got.Messages[0].Role != "system" || !strings.Contains(got.Messages[0].Content, "Связная тестовая сводка") {
		t.Fatalf("actual chat request should include freshly prepared summary: %+v", got.Messages)
	}
	summary, err := summaryStore.Load(context.Background())
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if summary.CoveredMessages != 4 || !strings.Contains(summary.Summary, "Связная тестовая сводка") {
		t.Fatalf("summary should be persisted before API call, got: %+v", summary)
	}
}

func TestAPISummarizerSendsSeparateRequestWithExpectedPrompt(t *testing.T) {
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
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"Итоговая связная summary из API."},"finish_reason":"stop"}]}`), nil
	})}
	summarizer := NewAPISummarizer(APISummarizerConfig{
		APIKey:      "test-key",
		BaseURL:     "https://example.test",
		Model:       "deepseek-v4-flash",
		Timeout:     time.Second,
		MaxTokens:   321,
		Temperature: 0.2,
	}, client)

	summary, err := summarizer.Summarize(context.Background(), ConversationSummary{
		Summary:         "Ранее выбрали хранить историю полностью.",
		CoveredMessages: 2,
	}, []chatMessage{
		{Role: "user", Content: "Важно не выдумывать деталей."},
		{Role: "assistant", Content: "Summary должно быть связным."},
	})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if summary != "Итоговая связная summary из API." {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if got.Model != "deepseek-v4-flash" || got.MaxTokens != 321 || got.Temperature != 0.2 {
		t.Fatalf("unexpected summary request config: %+v", got)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("expected system and user summary messages, got: %+v", got.Messages)
	}
	systemPrompt := got.Messages[0].Content
	userPrompt := got.Messages[1].Content
	for _, want := range []string{
		"связную краткую сводку",
		"ключевые факты, решения, ограничения, цели",
		"Не пересказывай каждую реплику",
		"Не добавляй выдуманные детали",
		"Учитывай existing summary и новый блок сообщений",
		"Верни только текст summary",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("summary system prompt does not contain %q:\n%s", want, systemPrompt)
		}
	}
	for _, want := range []string{
		"Existing summary:",
		"Ранее выбрали хранить историю полностью.",
		"New messages to merge into summary:",
		"user: Важно не выдумывать деталей.",
		"assistant: Summary должно быть связным.",
	} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("summary user prompt does not contain %q:\n%s", want, userPrompt)
		}
	}
}

func TestAPISummaryResultIsSavedAndUsedByCurrentChat(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	history := buildCompressionDemoHistory(8)
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), history); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)

	var requests []chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, got)
		switch len(requests) {
		case 1:
			return response(http.StatusOK, `{"choices":[{"message":{"content":"Fresh fake API summary for current prompt."},"finish_reason":"stop"}]}`), nil
		case 2:
			return response(http.StatusOK, `{"choices":[{"message":{"content":"chat ok"},"finish_reason":"stop"}]}`), nil
		default:
			t.Fatalf("unexpected API call #%d", len(requests))
			return nil, nil
		}
	})}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Timeout: time.Second,
		Memory:  store,
		Summary: summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
	}, client)

	answer, err := agent.Ask(context.Background(), "current")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if answer.Content != "chat ok" {
		t.Fatalf("unexpected answer: %q", answer.Content)
	}
	if len(requests) != 2 {
		t.Fatalf("expected summary request plus chat request, got %d", len(requests))
	}
	if !strings.Contains(requests[1].Messages[0].Content, "Fresh fake API summary") {
		t.Fatalf("actual chat request should contain fresh API summary: %+v", requests[1].Messages)
	}
	summary, err := summaryStore.Load(context.Background())
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if summary.CoveredMessages != 4 || summary.Summary != "Fresh fake API summary for current prompt." {
		t.Fatalf("summary result should be persisted, got: %+v", summary)
	}
}

func TestPrepareSummaryFailurePreservesExistingSummaryAndHistory(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	history := buildCompressionDemoHistory(8)
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), history); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)
	existing := ConversationSummary{
		Summary:         "Existing stable summary.",
		CoveredMessages: 0,
		UpdatedAt:       time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
	}
	if err := summaryStore.Save(context.Background(), existing); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	agent := NewAgent(AgentConfig{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		Memory:  store,
		Summary: summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
		Summarizer: errorSummarySummarizer{Err: errors.New("fake summary outage")},
	}, nil)

	rec := httptest.NewRecorder()
	contextPrepareAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/context/prepare", strings.NewReader(`{"message":"current"}`)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got contextPrepareAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Error == "" || !strings.Contains(got.Error, "не удалось обновить summary") {
		t.Fatalf("expected understandable summary error, got: %+v", got)
	}
	if got.CompressionReport == nil || got.CompressionReport.SummaryUpdateStatus != "failed" {
		t.Fatalf("expected failed compression report, got: %+v", got.CompressionReport)
	}
	afterSummary, err := summaryStore.Load(context.Background())
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if afterSummary.Summary != existing.Summary || afterSummary.CoveredMessages != existing.CoveredMessages {
		t.Fatalf("existing summary should not be overwritten: %+v", afterSummary)
	}
	afterHistory, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if !sameMessages(afterHistory, history) {
		t.Fatalf("history should not change after summary failure:\n got: %+v\nwant: %+v", afterHistory, history)
	}
}

func TestCompressionReportExplainsNoSummaryYet(t *testing.T) {
	history := buildCompressionDemoHistory(3)
	prompt := "следующий вопрос"
	cfg := CompressionConfig{
		Enabled:          true,
		KeepLastMessages: 4,
		ChunkSize:        4,
	}
	fullReport := EstimateChatTokenReport(buildChatMessages(defaultSystem, history, prompt), 0, TokenPricing{}, TokenCalibration{})
	actualReport := fullReport
	report := buildCompressionReport(true, fullReport, actualReport, ConversationSummary{}, cfg, len(history), SummaryUpdateReport{
		Reason: "history is still within keep-last-messages window",
	})

	if report.TotalHistoryMessages != 3 || report.SummaryCoversMessages != 0 || report.RecentMessagesKept != 3 {
		t.Fatalf("unexpected report sizing: %+v", report)
	}
	if report.EstimatedSavedInputTokens != 0 || report.EstimatedSavedPercent != 0 {
		t.Fatalf("expected zero saved input, got: %+v", report)
	}
	if report.SavingStatus != "no summary yet" {
		t.Fatalf("unexpected saving status: %q", report.SavingStatus)
	}
	if report.Reason != "history is still within keep-last-messages window" {
		t.Fatalf("unexpected reason: %q", report.Reason)
	}
}

func TestCompressionReportShowsWaitingSummaryUpdateState(t *testing.T) {
	history := buildCompressionDemoHistory(7)
	prompt := "следующий вопрос"
	cfg := CompressionConfig{
		Enabled:          true,
		KeepLastMessages: 4,
		ChunkSize:        4,
	}
	fullReport := EstimateChatTokenReport(buildChatMessages(defaultSystem, history, prompt), 0, TokenPricing{}, TokenCalibration{})
	report := buildCompressionReport(true, fullReport, fullReport, ConversationSummary{}, cfg, len(history), SummaryUpdateReport{})

	if report.PendingOldMessages != 3 {
		t.Fatalf("unexpected pending messages: %+v", report)
	}
	if report.SummaryUpdateStatus != "waiting" || report.MessagesUntilSummaryUpdate != 1 {
		t.Fatalf("expected waiting for one more message, got: %+v", report)
	}
	if report.SummaryUpdateLabel != "Waiting for 1 more messages" {
		t.Fatalf("unexpected label: %q", report.SummaryUpdateLabel)
	}
}

func TestCompressionReportShowsReadyInsteadOfNextAfterZero(t *testing.T) {
	history := buildCompressionDemoHistory(8)
	prompt := "следующий вопрос"
	cfg := CompressionConfig{
		Enabled:          true,
		KeepLastMessages: 4,
		ChunkSize:        4,
	}
	fullReport := EstimateChatTokenReport(buildChatMessages(defaultSystem, history, prompt), 0, TokenPricing{}, TokenCalibration{})
	report := buildCompressionReport(true, fullReport, fullReport, ConversationSummary{}, cfg, len(history), SummaryUpdateReport{})

	if report.PendingOldMessages != 4 {
		t.Fatalf("unexpected pending messages: %+v", report)
	}
	if report.SummaryUpdateStatus != "ready" || report.MessagesUntilSummaryUpdate != 0 {
		t.Fatalf("expected ready update state, got: %+v", report)
	}
	if report.SummaryUpdateLabel != "Ready to summarize now" {
		t.Fatalf("unexpected label: %q", report.SummaryUpdateLabel)
	}
}

func TestCompressionReportPendingOldMessagesAreWaitingForSummary(t *testing.T) {
	history := buildCompressionDemoHistory(14)
	prompt := "следующий вопрос"
	cfg := CompressionConfig{
		Enabled:          true,
		KeepLastMessages: 6,
		ChunkSize:        10,
	}
	summary := ConversationSummary{
		Summary:         "first block is summarized",
		CoveredMessages: 4,
	}
	fullReport := EstimateChatTokenReport(buildChatMessages(defaultSystem, history, prompt), 0, TokenPricing{}, TokenCalibration{})
	report := buildCompressionReport(true, fullReport, fullReport, summary, cfg, len(history), SummaryUpdateReport{})

	if report.RecentMessagesKept != 6 {
		t.Fatalf("recent window should still keep 6 messages, got: %+v", report)
	}
	if report.PendingOldMessages != 4 {
		t.Fatalf("pending old messages should be old messages waiting for summary, got: %+v", report)
	}
	if report.SummaryUpdateStatus != "waiting" || report.SummaryUpdateLabel != "Waiting for 6 more messages" {
		t.Fatalf("waiting state should come from summary_update fields, got: %+v", report)
	}
}

func TestCompressionReportSaysSummaryIsUpToDateWhenNoOldMessagesArePending(t *testing.T) {
	history := buildCompressionDemoHistory(8)
	prompt := "следующий вопрос"
	cfg := CompressionConfig{
		Enabled:          true,
		KeepLastMessages: 4,
		ChunkSize:        4,
	}
	summary := ConversationSummary{
		Summary:         "covered old block",
		CoveredMessages: 4,
	}
	fullReport := EstimateChatTokenReport(buildChatMessages(defaultSystem, history, prompt), 0, TokenPricing{}, TokenCalibration{})
	report := buildCompressionReport(true, fullReport, fullReport, summary, cfg, len(history), SummaryUpdateReport{})

	if report.PendingOldMessages != 0 {
		t.Fatalf("test setup should have no pending old messages: %+v", report)
	}
	if report.SummaryUpdateStatus != "not_needed" || report.MessagesUntilSummaryUpdate != 0 {
		t.Fatalf("summary should be up to date, got: %+v", report)
	}
	if report.SummaryUpdateLabel != "Summary is up to date" {
		t.Fatalf("unexpected summary label: %q", report.SummaryUpdateLabel)
	}
}

func TestChatPageUsesSummaryUpdateLabelsAndWaitingForSummaryCopy(t *testing.T) {
	for _, want := range []string{
		"old messages waiting for summary",
		"summary_update_label",
		"summary_update_status",
		"messages_until_summary_update",
		"Checking context",
		"Summarizing old messages",
		"Summary updated",
		"Agent is answering",
		`class="progress-status" id="progress-status"`,
		"removeProgressMessage(progress)",
		"report.summary_update_status === 'ready'",
		"prepared.compression_report.summary_update_status === 'updated'",
		"report.summary_update_label || summaryUpdateLabel(report)",
		"Input",
		"Output",
		"Saved input",
		"Compression",
		"await refreshCompressionReport().catch",
	} {
		if !strings.Contains(chatPageHTML, want) {
			t.Fatalf("chat page should contain %q", want)
		}
	}
	if strings.Contains(chatPageHTML, "next summary update after") {
		t.Fatal("chat page should not render the old next-summary-update-after label")
	}
	for _, notWant := range []string{
		"className = 'message process'",
		"context-stepper",
		"context-step",
		"progress.remove()",
	} {
		if strings.Contains(chatPageHTML, notWant) {
			t.Fatalf("chat page should not contain old bulky progress UI %q", notWant)
		}
	}
}

func TestLocalSummarySummarizerProducesCohesiveSummaryWithoutWordCuts(t *testing.T) {
	summary, err := (LocalSummarySummarizer{MaxItems: 4, MaxChars: 45}).Summarize(context.Background(), ConversationSummary{}, []chatMessage{
		{Role: "user", Content: "Запомни: проект учебный, история должна храниться полностью, а summary используется только в prompt."},
		{Role: "assistant", Content: "Принял. Нужно сохранить полный history.json и обновлять отдельный summary.json."},
		{Role: "user", Content: "Еще важно: тесты не должны ходить в реальный API и должны использовать fake transport."},
		{Role: "assistant", Content: "Зафиксировал ограничение и порядок: prepare обновляет summary до chat request."},
	})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	for _, want := range []string{
		"Сводка предыдущего диалога.",
		"Пользователь сообщил или попросил:",
		"Ассистент уже ответил или зафиксировал:",
		"старые сообщения, которые еще ждут следующего summary chunk",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary does not contain %q:\n%s", want, summary)
		}
	}
	for _, notWant := range []string{"- user:", "- assistant:", "..."} {
		if strings.Contains(summary, notWant) {
			t.Fatalf("summary still looks mechanically clipped, contains %q:\n%s", notWant, summary)
		}
	}
}

func TestMaybeUpdateSummaryDoesNotUpdateWhenBlockIsSmallerThanChunk(t *testing.T) {
	store := &demoSummaryStore{}
	cfg := CompressionConfig{
		Enabled:          true,
		KeepLastMessages: 4,
		ChunkSize:        4,
	}
	updated, updateReport, err := maybeUpdateSummary(context.Background(), buildCompressionDemoHistory(7), ConversationSummary{}, cfg, store, LocalSummarySummarizer{})
	if err != nil {
		t.Fatalf("maybeUpdateSummary returned error: %v", err)
	}
	if updateReport.Updated || updateReport.NewlyCompressedMessages != 0 {
		t.Fatalf("summary should not update yet: %+v", updateReport)
	}
	if updated.CoveredMessages != 0 || store.summary.CoveredMessages != 0 {
		t.Fatalf("summary should remain empty: updated=%+v stored=%+v", updated, store.summary)
	}
	if updateReport.Reason != "pending block is smaller than summary chunk size" {
		t.Fatalf("unexpected reason: %q", updateReport.Reason)
	}
}

func TestAgentAskCompressionCanHelpPassContextLimit(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	history := []chatMessage{
		{Role: "user", Content: strings.Repeat("длинный старый факт ", 80)},
		{Role: "assistant", Content: strings.Repeat("длинный старый ответ ", 80)},
		{Role: "user", Content: "свежий вопрос остается дословно"},
		{Role: "assistant", Content: "свежий ответ остается дословно"},
	}
	prompt := "продолжи"
	system := "system message"
	compression := CompressionConfig{
		Enabled:          true,
		KeepLastMessages: 2,
		ChunkSize:        2,
		SummaryPath:      summaryPath,
	}
	summaryStore := NewJSONSummaryStore(summaryPath)
	summary, updateReport, err := maybeUpdateSummary(context.Background(), history, ConversationSummary{}, compression, &demoSummaryStore{}, LocalSummarySummarizer{MaxItems: 1, MaxChars: 16})
	if err != nil {
		t.Fatalf("prepare summary: %v", err)
	}
	if !updateReport.Updated {
		t.Fatal("expected prepared summary to update")
	}
	fullReport := EstimateChatTokenReport(buildChatMessages(system, history, prompt), 0, TokenPricing{}, TokenCalibration{})
	compressedContext := buildCompressedContext(system, history, prompt, summary, compression, TokenPricing{}, TokenCalibration{})
	actualReport := EstimateChatTokenReport(compressedContext.Messages, 0, TokenPricing{}, TokenCalibration{})
	if fullReport.PromptTokens <= actualReport.PromptTokens {
		t.Fatalf("test setup should make compression shorter: full=%d actual=%d", fullReport.PromptTokens, actualReport.PromptTokens)
	}
	contextLimit := actualReport.PromptTokens

	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), history); err != nil {
		t.Fatalf("save history: %v", err)
	}
	apiCalled := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		apiCalled = true
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:       "test-key",
		BaseURL:      "https://example.test",
		System:       system,
		Timeout:      time.Second,
		ContextLimit: contextLimit,
		Memory:       store,
		Summary:      summaryStore,
		Compression:  compression,
		Summarizer:   LocalSummarySummarizer{MaxItems: 1, MaxChars: 16},
	}, client)

	answer, err := agent.Ask(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if !apiCalled {
		t.Fatal("API should be called because actual compressed prompt fits the local limit")
	}
	if answer.CompressionReport == nil {
		t.Fatal("expected compression report")
	}
	if answer.CompressionReport.FullPromptInputTokens <= contextLimit {
		t.Fatalf("full prompt should exceed limit in this setup: %+v", answer.CompressionReport)
	}
	if answer.CompressionReport.ActualPromptInputTokens > contextLimit {
		t.Fatalf("actual prompt should fit after compression: %+v", answer.CompressionReport)
	}
	if !answer.CompressionReport.SummaryUpdatedNow || answer.CompressionReport.NewlyCompressedMessages != 2 {
		t.Fatalf("expected summary update report, got: %+v", answer.CompressionReport)
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

func TestAgentAskReturnsContextLimitErrorWithoutAPICall(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), []chatMessage{
		{Role: "user", Content: strings.Repeat("длинная история ", 20)},
		{Role: "assistant", Content: "ответ"},
	}); err != nil {
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

	history, loadErr := store.Load(context.Background())
	if loadErr != nil {
		t.Fatalf("load history: %v", loadErr)
	}
	if len(history) != 2 {
		t.Fatalf("history should not be changed after overflow: %+v", history)
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
		return response(http.StatusOK, `{"choices":[{"message":{"content":"Ответ"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}`), nil
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
	if got.CompressionReport == nil || got.CompressionReport.Mode != "disabled" {
		t.Fatalf("expected disabled compression report, got: %+v", got.CompressionReport)
	}
	if got.CompressionReport.TotalHistoryMessages != 0 || got.CompressionReport.FullPromptInputTokens == 0 || got.CompressionReport.ActualPromptInputTokens == 0 {
		t.Fatalf("expected detailed compression report, got: %+v", got.CompressionReport)
	}
	if got.CompressionReport.SavingStatus == "" || got.CompressionReport.Reason == "" {
		t.Fatalf("expected readable compression status, got: %+v", got.CompressionReport)
	}
	if got.Usage == nil || got.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected API usage: %+v", got.Usage)
	}
}

func TestSettingsAPIHandlerReturnsCurrentSettings(t *testing.T) {
	agent := NewAgent(AgentConfig{
		ContextLimit: 2048,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 5,
			ChunkSize:        7,
		},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	settingsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got RuntimeSettings
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := RuntimeSettings{
		CompressionEnabled: true,
		KeepLastMessages:   5,
		SummaryChunkSize:   7,
		ContextLimit:       2048,
	}
	if got != want {
		t.Fatalf("unexpected settings: got %+v want %+v", got, want)
	}
}

func TestSettingsAPIHandlerUpdatesSettingsWithoutRestart(t *testing.T) {
	agent := NewAgent(AgentConfig{
		ContextLimit: 8192,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 6,
			ChunkSize:        10,
		},
	}, nil)

	body := `{"compression_enabled":false,"keep_last_messages":3,"summary_chunk_size":4,"context_limit":512}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	settingsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	got := agent.Settings()
	want := RuntimeSettings{
		CompressionEnabled: false,
		KeepLastMessages:   3,
		SummaryChunkSize:   4,
		ContextLimit:       512,
	}
	if got != want {
		t.Fatalf("settings were not updated live: got %+v want %+v", got, want)
	}
}

func TestSettingsAPIHandlerRejectsInvalidSettings(t *testing.T) {
	agent := NewAgent(AgentConfig{
		ContextLimit: 8192,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 6,
			ChunkSize:        10,
		},
	}, nil)

	body := `{"compression_enabled":true,"keep_last_messages":0,"summary_chunk_size":4,"context_limit":512}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	settingsAPIHandler(agent).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	got := agent.Settings()
	if got.KeepLastMessages != 6 || got.SummaryChunkSize != 10 || got.ContextLimit != 8192 {
		t.Fatalf("invalid settings should not change agent: %+v", got)
	}
}

func TestCompressionReportReflectsUpdatedKeepLastMessages(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), buildCompressionDemoHistory(10)); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)
	if err := summaryStore.Save(context.Background(), ConversationSummary{
		Summary:         "old facts",
		CoveredMessages: 2,
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	agent := NewAgent(AgentConfig{
		ContextLimit: 1000,
		Memory:       store,
		Summary:      summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 6,
			ChunkSize:        10,
			SummaryPath:      summaryPath,
		},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/compression-report", nil)
	rec := httptest.NewRecorder()
	compressionReportAPIHandler(agent).ServeHTTP(rec, req)
	var before CompressionReport
	if err := json.NewDecoder(rec.Body).Decode(&before); err != nil {
		t.Fatalf("decode initial report: %v", err)
	}
	if before.PendingOldMessages != 2 {
		t.Fatalf("unexpected initial pending old messages: %+v", before)
	}

	body := `{"compression_enabled":true,"keep_last_messages":3,"summary_chunk_size":10,"context_limit":1000}`
	rec = httptest.NewRecorder()
	settingsAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update settings status: %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	compressionReportAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/compression-report", nil))
	var after CompressionReport
	if err := json.NewDecoder(rec.Body).Decode(&after); err != nil {
		t.Fatalf("decode updated report: %v", err)
	}
	if after.PendingOldMessages != 5 {
		t.Fatalf("pending old messages should reflect new keep-last value: %+v", after)
	}
}

func TestCompressionReportAPIWorksWithoutSendingMessage(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), buildCompressionDemoHistory(12)); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)
	apiCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		apiCalls++
		t.Fatal("GET /api/compression-report should not call the API")
		return nil, nil
	})}
	agent := NewAgent(AgentConfig{
		APIKey:       "test-key",
		BaseURL:      "https://example.test",
		ContextLimit: 1000,
		Memory:       store,
		Summary:      summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
	}, client)

	rec := httptest.NewRecorder()
	compressionReportAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/compression-report", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got CompressionReport
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TotalHistoryMessages != 12 || got.PendingOldMessages != 8 || got.SummaryUpdatedNow {
		t.Fatalf("unexpected report without chat request: %+v", got)
	}
	summary, err := summaryStore.Load(context.Background())
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if summary.CoveredMessages != 0 || summary.Summary != "" {
		t.Fatalf("GET report should not update summary: %+v", summary)
	}
	if got.SummaryUpdateStatus != "ready" || got.SummaryUpdateLabel != "Ready to summarize now" {
		t.Fatalf("GET report should expose ready state without updating summary: %+v", got)
	}
	if apiCalls != 0 {
		t.Fatalf("GET report should not call API, got %d calls", apiCalls)
	}
}

func TestContextPrepareAPIUpdatesSummaryBeforeChat(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), buildCompressionDemoHistory(8)); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)
	agent := NewAgent(AgentConfig{
		ContextLimit: 1000,
		Memory:       store,
		Summary:      summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
		Summarizer: fixedSummarySummarizer{Text: "Summary prepared by fake summarizer."},
	}, nil)

	rec := httptest.NewRecorder()
	contextPrepareAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/context/prepare", strings.NewReader(`{"message":"current"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got contextPrepareAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.CompressionReport == nil || got.CompressionReport.SummaryUpdateStatus != "updated" {
		t.Fatalf("expected updated compression report, got: %+v", got.CompressionReport)
	}
	if got.CompressionReport.SummaryCoversMessages != 4 {
		t.Fatalf("summary coverage should increase before chat: %+v", got.CompressionReport)
	}
	summary, err := summaryStore.Load(context.Background())
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if summary.CoveredMessages != 4 {
		t.Fatalf("prepare should persist summary before chat, got: %+v", summary)
	}
}

func TestHistoryResetAPIHandlerClearsHistory(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "history.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), buildCompressionDemoHistory(4)); err != nil {
		t.Fatalf("save history: %v", err)
	}
	agent := NewAgent(AgentConfig{Memory: store}, nil)

	rec := httptest.NewRecorder()
	historyResetAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/history/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	history, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history should be empty after reset: %+v", history)
	}
}

func TestSummaryResetAPIHandlerClearsSummaryAndReportSaysNoSummaryYet(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.json")
	summaryPath := filepath.Join(dir, "summary.json")
	store := NewJSONMessageStore(historyPath)
	if err := store.Save(context.Background(), buildCompressionDemoHistory(8)); err != nil {
		t.Fatalf("save history: %v", err)
	}
	summaryStore := NewJSONSummaryStore(summaryPath)
	if err := summaryStore.Save(context.Background(), ConversationSummary{
		Summary:         "old facts",
		CoveredMessages: 4,
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	agent := NewAgent(AgentConfig{
		ContextLimit: 1000,
		Memory:       store,
		Summary:      summaryStore,
		Compression: CompressionConfig{
			Enabled:          true,
			KeepLastMessages: 4,
			ChunkSize:        4,
			SummaryPath:      summaryPath,
		},
	}, nil)

	rec := httptest.NewRecorder()
	summaryResetAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/summary/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	compressionReportAPIHandler(agent).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/compression-report", nil))
	var got CompressionReport
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.SummaryCoversMessages != 0 || got.SavingStatus != "no summary yet" {
		t.Fatalf("expected empty summary status after reset, got: %+v", got)
	}
}

func TestReadConfigReadsPromptFromStdin(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-pro")
	t.Setenv("DEEPSEEK_BASE_URL", "https://example.test/")
	t.Setenv("DAY9_HISTORY_PATH", "/tmp/day9-test-history.json")
	t.Setenv("DAY9_SUMMARY_PATH", "/tmp/day9-test-summary.json")
	t.Setenv("DAY9_CONTEXT_LIMIT", "321")
	t.Setenv("DAY9_INPUT_PRICE_PER_1M", "0.14")
	t.Setenv("DAY9_OUTPUT_PRICE_PER_1M", "0.28")
	t.Setenv("DAY9_COMPRESSION_ENABLED", "true")
	t.Setenv("DAY9_KEEP_LAST_MESSAGES", "5")
	t.Setenv("DAY9_SUMMARY_CHUNK_SIZE", "9")

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
	if cfg.HistoryPath != "/tmp/day9-test-history.json" {
		t.Fatalf("unexpected history path: %q", cfg.HistoryPath)
	}
	if cfg.Compression.SummaryPath != "/tmp/day9-test-summary.json" || !cfg.Compression.Enabled {
		t.Fatalf("unexpected compression config: %+v", cfg.Compression)
	}
	if cfg.Compression.KeepLastMessages != 5 || cfg.Compression.ChunkSize != 9 {
		t.Fatalf("unexpected compression sizing: %+v", cfg.Compression)
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
	t.Setenv("DAY9_PROMPT_TOKEN_MULTIPLIER", "1.25")
	t.Setenv("DAY9_COMPLETION_TOKEN_MULTIPLIER", "1.5")

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

func TestReadConfigEnablesCompressionByDefault(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	cfg, err := readConfig([]string{"-prompt", "hello"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if !cfg.Compression.Enabled {
		t.Fatal("expected compression to be enabled by default")
	}

	cfg, err = readConfig([]string{"-compress-history=false", "-prompt", "hello"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if cfg.Compression.Enabled {
		t.Fatal("expected compression to be disabled by explicit flag")
	}
}

func TestReadConfigAllowsCompressionDemoWithoutAPIKeyOrPrompt(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")

	cfg, err := readConfig([]string{"-demo-compression"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("readConfig returned error: %v", err)
	}
	if !cfg.DemoCompression {
		t.Fatal("expected compression demo mode")
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

func TestRunCompressionDemoShowsSavingsAndQualityComparison(t *testing.T) {
	var out bytes.Buffer
	err := runCompressionDemo(&out, CompressionDemoConfig{
		Compression: CompressionConfig{
			KeepLastMessages: 6,
			ChunkSize:        10,
		},
		ContextLimit: 1000,
	})
	if err != nil {
		t.Fatalf("runCompressionDemo returned error: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Scenario: compression",
		"small history, no summary yet",
		"chunk accumulated, summary is created",
		"history grows, summary saves input",
		"next chunk is still waiting",
		"messages full_prompt actual_prompt saved saved_%",
		"Quality comparison",
		"Without compression:",
		"With compression:",
		"Summary should preserve",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compression demo output does not contain %q:\n%s", want, text)
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

type fixedSummarySummarizer struct {
	Text string
}

func (s fixedSummarySummarizer) Summarize(ctx context.Context, existing ConversationSummary, messages []chatMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.Text, nil
}

type errorSummarySummarizer struct {
	Err error
}

func (s errorSummarySummarizer) Summarize(ctx context.Context, existing ConversationSummary, messages []chatMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", s.Err
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
