package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AgentConfig struct {
	APIKey       string
	BaseURL      string
	Model        string
	System       string
	Timeout      time.Duration
	MaxTokens    int
	ContextLimit int
	Temperature  float64
	Thinking     bool
	Memory       MessageStore
	Pricing      TokenPricing
	Calibration  TokenCalibration
}

type Agent struct {
	cfg    AgentConfig
	client *http.Client
	mu     sync.Mutex
}

type AgentResponse struct {
	Content      string
	Usage        *tokenUsage
	TokenReport  TokenReport
	SessionUsage SessionUsage
	FinishReason string
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model       string          `json:"model"`
	Messages    []chatMessage   `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Thinking    *thinkingConfig `json:"thinking,omitempty"`
	Stream      bool            `json:"stream"`
}

type tokenUsage struct {
	PromptTokens            int                 `json:"prompt_tokens"`
	CompletionTokens        int                 `json:"completion_tokens"`
	TotalTokens             int                 `json:"total_tokens"`
	ReasoningTokens         *int                `json:"reasoning_tokens,omitempty"`
	ThinkingTokens          *int                `json:"thinking_tokens,omitempty"`
	PromptCacheHitTokens    int                 `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens   int                 `json:"prompt_cache_miss_tokens,omitempty"`
	CompletionTokensDetails *tokenUsageDetails  `json:"completion_tokens_details,omitempty"`
	CompletionTokensDetail  *tokenUsageDetails  `json:"completion_tokens_detail,omitempty"`
	PromptTokensDetails     *promptUsageDetails `json:"prompt_tokens_details,omitempty"`
}

type tokenUsageDetails struct {
	ReasoningTokens *int `json:"reasoning_tokens,omitempty"`
	ThinkingTokens  *int `json:"thinking_tokens,omitempty"`
}

type promptUsageDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *tokenUsage `json:"usage,omitempty"`
}

type apiError struct {
	Error any `json:"error"`
}

func NewAgent(cfg AgentConfig, client *http.Client) *Agent {
	if client == nil {
		client = http.DefaultClient
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	return &Agent{
		cfg:    cfg,
		client: client,
	}
}

func (a *Agent) Ask(ctx context.Context, userPrompt string) (AgentResponse, error) {
	if strings.TrimSpace(a.cfg.APIKey) == "" {
		return AgentResponse{}, errors.New("переменная окружения DEEPSEEK_API_KEY не задана")
	}

	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return AgentResponse{}, errors.New("передайте текст через -prompt или stdin")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.loadState(ctx)
	if err != nil {
		return AgentResponse{}, err
	}
	history := state.Messages

	tokenReport := EstimatePromptTokenReportWithCalibration(strings.TrimSpace(a.cfg.System), history, userPrompt, a.cfg.ContextLimit, a.cfg.Pricing, a.cfg.Calibration)
	if tokenReport.OverflowTokens > 0 {
		return AgentResponse{}, &ContextLimitError{Report: tokenReport}
	}

	decoded, err := a.complete(ctx, a.buildRequest(userPrompt, history))
	if err != nil {
		return AgentResponse{}, err
	}
	if len(decoded.Choices) == 0 {
		return AgentResponse{}, errors.New("DeepSeek API вернул ответ без choices")
	}

	answer := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if answer == "" {
		return AgentResponse{}, errors.New("DeepSeek API вернул пустой ответ")
	}
	tokenReport = AddAnswerTokensWithCalibration(tokenReport, answer, a.cfg.Pricing, a.cfg.Calibration)

	updatedHistory := append(cloneMessages(history),
		chatMessage{Role: "user", Content: userPrompt},
		chatMessage{Role: "assistant", Content: answer},
	)
	state.Messages = updatedHistory
	state.SessionUsage = AddTurnToSessionUsage(state.SessionUsage, decoded.Usage, tokenReport)
	if err := a.saveState(ctx, state); err != nil {
		return AgentResponse{}, err
	}

	return AgentResponse{
		Content:      answer,
		Usage:        decoded.Usage,
		TokenReport:  tokenReport,
		SessionUsage: state.SessionUsage,
		FinishReason: decoded.Choices[0].FinishReason,
	}, nil
}

func (a *Agent) History(ctx context.Context) ([]chatMessage, error) {
	state, err := a.loadState(ctx)
	if err != nil {
		return nil, err
	}
	return cloneMessages(state.Messages), nil
}

func (a *Agent) loadState(ctx context.Context) (ConversationState, error) {
	if a.cfg.Memory == nil {
		return ConversationState{}, nil
	}
	if store, ok := a.cfg.Memory.(StateStore); ok {
		state, err := store.LoadState(ctx)
		if err != nil {
			return ConversationState{}, err
		}
		state.Messages = cloneMessages(state.Messages)
		return state, nil
	}
	messages, err := a.cfg.Memory.Load(ctx)
	if err != nil {
		return ConversationState{}, err
	}
	return ConversationState{Messages: cloneMessages(messages)}, nil
}

func (a *Agent) saveHistory(ctx context.Context, messages []chatMessage) error {
	return a.saveState(ctx, ConversationState{Messages: messages})
}

func (a *Agent) saveState(ctx context.Context, state ConversationState) error {
	if a.cfg.Memory == nil {
		return nil
	}
	state.Messages = cloneMessages(state.Messages)
	if store, ok := a.cfg.Memory.(StateStore); ok {
		if err := store.SaveState(ctx, state); err != nil {
			return fmt.Errorf("не удалось сохранить историю: %w", err)
		}
		return nil
	}
	if err := a.cfg.Memory.Save(ctx, state.Messages); err != nil {
		return fmt.Errorf("не удалось сохранить историю: %w", err)
	}
	return nil
}

func (a *Agent) buildRequest(userPrompt string, history []chatMessage) chatRequest {
	req := chatRequest{
		Model:       a.cfg.Model,
		Messages:    a.buildMessages(userPrompt, history),
		Temperature: a.cfg.Temperature,
		MaxTokens:   a.cfg.MaxTokens,
		Stream:      false,
	}
	if !a.cfg.Thinking {
		req.Thinking = &thinkingConfig{Type: "disabled"}
	}
	return req
}

func (a *Agent) buildMessages(userPrompt string, history []chatMessage) []chatMessage {
	system := strings.TrimSpace(a.cfg.System)
	messages := make([]chatMessage, 0, len(history)+2)
	if system == "" {
		messages = append(messages, history...)
		messages = append(messages, chatMessage{Role: "user", Content: userPrompt})
		return messages
	}
	messages = append(messages, chatMessage{Role: "system", Content: system})
	messages = append(messages, history...)
	messages = append(messages, chatMessage{Role: "user", Content: userPrompt})
	return messages
}

func (a *Agent) complete(ctx context.Context, request chatRequest) (chatResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	payload, err := json.Marshal(request)
	if err != nil {
		return chatResponse{}, fmt.Errorf("не удалось собрать JSON-запрос: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return chatResponse{}, fmt.Errorf("не удалось создать HTTP-запрос: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return chatResponse{}, fmt.Errorf("DeepSeek API недоступен: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatResponse{}, fmt.Errorf("не удалось прочитать ответ DeepSeek: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return chatResponse{}, fmt.Errorf("DeepSeek API вернул HTTP %d: %s", resp.StatusCode, formatAPIError(respBody))
	}

	var decoded chatResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return chatResponse{}, fmt.Errorf("не удалось разобрать JSON-ответ DeepSeek: %w", err)
	}
	return decoded, nil
}

func formatAPIError(body []byte) string {
	var decoded apiError
	if err := json.Unmarshal(body, &decoded); err == nil && decoded.Error != nil {
		formatted, err := json.Marshal(decoded.Error)
		if err == nil {
			return string(formatted)
		}
	}

	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty response body"
	}
	return text
}
