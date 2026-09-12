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
	Summary      SummaryStore
	Compression  CompressionConfig
	Summarizer   Summarizer
	Pricing      TokenPricing
	Calibration  TokenCalibration
}

type Agent struct {
	cfg    AgentConfig
	client *http.Client
	mu     sync.Mutex
}

type AgentResponse struct {
	Content           string
	Usage             *tokenUsage
	TokenReport       TokenReport
	CompressionReport *CompressionReport
	FinishReason      string
}

const compressionReportProbePrompt = ""

type preparedContext struct {
	History           []chatMessage
	RequestMessages   []chatMessage
	FullTokenReport   TokenReport
	TokenReport       TokenReport
	CompressionReport CompressionReport
}

type ContextPrepareError struct {
	Err               error
	CompressionReport *CompressionReport
}

func (e *ContextPrepareError) Error() string {
	return e.Err.Error()
}

func (e *ContextPrepareError) Unwrap() error {
	return e.Err
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
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens,omitempty"`
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
	cfg.Compression = normalizeCompressionConfig(cfg.Compression)
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Summary == nil && cfg.Compression.SummaryPath != "" {
		cfg.Summary = NewJSONSummaryStore(cfg.Compression.SummaryPath)
	}
	if cfg.Summarizer == nil {
		cfg.Summarizer = NewAPISummarizer(APISummarizerConfig{
			APIKey:      cfg.APIKey,
			BaseURL:     cfg.BaseURL,
			Model:       cfg.Model,
			Timeout:     cfg.Timeout,
			MaxTokens:   cfg.MaxTokens,
			Temperature: cfg.Temperature,
			Thinking:    cfg.Thinking,
		}, client)
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

	prepared, err := a.prepareContextLocked(ctx, userPrompt, true)
	if err != nil {
		return AgentResponse{}, err
	}

	if prepared.TokenReport.OverflowTokens > 0 {
		return AgentResponse{}, &ContextLimitError{Report: prepared.TokenReport, CompressionReport: &prepared.CompressionReport}
	}

	decoded, err := a.complete(ctx, a.buildRequest(prepared.RequestMessages))
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
	tokenReport := AddAnswerTokensWithCalibration(prepared.TokenReport, answer, a.cfg.Pricing, a.cfg.Calibration)

	updatedHistory := append(cloneMessages(prepared.History),
		chatMessage{Role: "user", Content: userPrompt},
		chatMessage{Role: "assistant", Content: answer},
	)
	if err := a.saveHistory(ctx, updatedHistory); err != nil {
		return AgentResponse{}, err
	}

	return AgentResponse{
		Content:           answer,
		Usage:             decoded.Usage,
		TokenReport:       tokenReport,
		CompressionReport: &prepared.CompressionReport,
		FinishReason:      decoded.Choices[0].FinishReason,
	}, nil
}

func (a *Agent) History(ctx context.Context) ([]chatMessage, error) {
	if a.cfg.Memory == nil {
		return nil, nil
	}
	messages, err := a.cfg.Memory.Load(ctx)
	if err != nil {
		return nil, err
	}
	return cloneMessages(messages), nil
}

func (a *Agent) Settings() RuntimeSettings {
	a.mu.Lock()
	defer a.mu.Unlock()

	return runtimeSettingsFromConfig(a.cfg)
}

func (a *Agent) UpdateSettings(settings RuntimeSettings) error {
	if err := validateRuntimeSettings(settings); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.cfg.ContextLimit = settings.ContextLimit
	a.cfg.Compression.Enabled = settings.CompressionEnabled
	a.cfg.Compression.KeepLastMessages = settings.KeepLastMessages
	a.cfg.Compression.ChunkSize = settings.SummaryChunkSize
	a.cfg.Compression = normalizeCompressionConfig(a.cfg.Compression)
	if a.cfg.Compression.Enabled && a.cfg.Summary == nil && a.cfg.Compression.SummaryPath != "" {
		a.cfg.Summary = NewJSONSummaryStore(a.cfg.Compression.SummaryPath)
	}
	return nil
}

func (a *Agent) CompressionState(ctx context.Context) (CompressionReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	prepared, err := a.prepareContextLocked(ctx, compressionReportProbePrompt, false)
	if err != nil {
		return CompressionReport{}, err
	}
	return prepared.CompressionReport, nil
}

func (a *Agent) PrepareContext(ctx context.Context, userPrompt string) (CompressionReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	prepared, err := a.prepareContextLocked(ctx, strings.TrimSpace(userPrompt), true)
	return prepared.CompressionReport, err
}

func (a *Agent) prepareContextLocked(ctx context.Context, userPrompt string, updateSummary bool) (preparedContext, error) {
	history, err := a.loadHistory(ctx)
	if err != nil {
		return preparedContext{}, err
	}

	system := strings.TrimSpace(a.cfg.System)
	fullMessages := a.buildMessages(userPrompt, history)
	fullReport := EstimateChatTokenReport(fullMessages, a.cfg.ContextLimit, a.cfg.Pricing, a.cfg.Calibration)
	tokenReport := fullReport
	requestMessages := fullMessages
	compressionReport := buildCompressionReport(false, fullReport, fullReport, ConversationSummary{}, a.cfg.Compression, len(history), SummaryUpdateReport{})

	if !a.cfg.Compression.Enabled {
		return preparedContext{
			History:           history,
			RequestMessages:   requestMessages,
			FullTokenReport:   fullReport,
			TokenReport:       tokenReport,
			CompressionReport: compressionReport,
		}, nil
	}

	summary, err := a.loadSummary(ctx)
	if err != nil {
		return preparedContext{}, err
	}
	updateReport := SummaryUpdateReport{}
	if updateSummary {
		summary, updateReport, err = maybeUpdateSummary(ctx, history, summary, a.cfg.Compression, a.cfg.Summary, a.cfg.Summarizer)
		if err != nil {
			compressedContext := buildCompressedContext(system, history, userPrompt, summary, a.cfg.Compression, a.cfg.Pricing, a.cfg.Calibration)
			tokenReport = EstimateChatTokenReport(compressedContext.Messages, a.cfg.ContextLimit, a.cfg.Pricing, a.cfg.Calibration)
			compressionReport = buildCompressionReport(true, fullReport, tokenReport, compressedContext.Summary, a.cfg.Compression, len(history), SummaryUpdateReport{
				Reason: fmt.Sprintf("summary update failed: %v", err),
				Status: "failed",
			})
			return preparedContext{
					History:           history,
					RequestMessages:   compressedContext.Messages,
					FullTokenReport:   fullReport,
					TokenReport:       tokenReport,
					CompressionReport: compressionReport,
				}, &ContextPrepareError{
					Err:               fmt.Errorf("не удалось обновить summary: %w", err),
					CompressionReport: &compressionReport,
				}
		}
	}

	compressedContext := buildCompressedContext(system, history, userPrompt, summary, a.cfg.Compression, a.cfg.Pricing, a.cfg.Calibration)
	requestMessages = compressedContext.Messages
	tokenReport = EstimateChatTokenReport(requestMessages, a.cfg.ContextLimit, a.cfg.Pricing, a.cfg.Calibration)
	compressionReport = buildCompressionReport(true, fullReport, tokenReport, compressedContext.Summary, a.cfg.Compression, len(history), updateReport)
	return preparedContext{
		History:           history,
		RequestMessages:   requestMessages,
		FullTokenReport:   fullReport,
		TokenReport:       tokenReport,
		CompressionReport: compressionReport,
	}, nil
}

func (a *Agent) ResetHistory(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Memory == nil {
		return nil
	}
	if resettable, ok := a.cfg.Memory.(ResettableMessageStore); ok {
		return resettable.Reset(ctx)
	}
	return a.saveHistory(ctx, nil)
}

func (a *Agent) ResetSummary(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Summary == nil {
		return nil
	}
	if resettable, ok := a.cfg.Summary.(ResettableSummaryStore); ok {
		return resettable.Reset(ctx)
	}
	return a.cfg.Summary.Save(ctx, ConversationSummary{})
}

func (a *Agent) loadHistory(ctx context.Context) ([]chatMessage, error) {
	if a.cfg.Memory == nil {
		return nil, nil
	}
	messages, err := a.cfg.Memory.Load(ctx)
	if err != nil {
		return nil, err
	}
	return cloneMessages(messages), nil
}

func (a *Agent) saveHistory(ctx context.Context, messages []chatMessage) error {
	if a.cfg.Memory == nil {
		return nil
	}
	if err := a.cfg.Memory.Save(ctx, messages); err != nil {
		return fmt.Errorf("не удалось сохранить историю: %w", err)
	}
	return nil
}

func (a *Agent) loadSummary(ctx context.Context) (ConversationSummary, error) {
	if a.cfg.Summary == nil {
		return ConversationSummary{}, nil
	}
	summary, err := a.cfg.Summary.Load(ctx)
	if err != nil {
		return ConversationSummary{}, err
	}
	return summary, nil
}

func (a *Agent) updateSummary(ctx context.Context, messages []chatMessage) (ConversationSummary, error) {
	if !a.cfg.Compression.Enabled || a.cfg.Summary == nil {
		return ConversationSummary{}, nil
	}
	summary, err := a.loadSummary(ctx)
	if err != nil {
		return ConversationSummary{}, err
	}
	updated, _, err := maybeUpdateSummary(ctx, messages, summary, a.cfg.Compression, a.cfg.Summary, a.cfg.Summarizer)
	return updated, err
}

func (a *Agent) buildRequest(messages []chatMessage) chatRequest {
	req := chatRequest{
		Model:       a.cfg.Model,
		Messages:    cloneMessages(messages),
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
	return buildChatMessages(a.cfg.System, history, userPrompt)
}

func runtimeSettingsFromConfig(cfg AgentConfig) RuntimeSettings {
	compression := normalizeCompressionConfig(cfg.Compression)
	return RuntimeSettings{
		CompressionEnabled: compression.Enabled,
		KeepLastMessages:   compression.KeepLastMessages,
		SummaryChunkSize:   compression.ChunkSize,
		ContextLimit:       cfg.ContextLimit,
	}
}

func validateRuntimeSettings(settings RuntimeSettings) error {
	if settings.KeepLastMessages <= 0 {
		return errors.New("keep_last_messages должно быть положительным")
	}
	if settings.SummaryChunkSize <= 0 {
		return errors.New("summary_chunk_size должно быть положительным")
	}
	if settings.ContextLimit < 0 {
		return errors.New("context_limit не может быть отрицательным")
	}
	return nil
}

func (a *Agent) complete(ctx context.Context, request chatRequest) (chatResponse, error) {
	return completeChat(ctx, a.client, a.cfg.BaseURL, a.cfg.APIKey, a.cfg.Timeout, request)
}

func completeChat(ctx context.Context, client *http.Client, baseURL string, apiKey string, timeout time.Duration, request chatRequest) (chatResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(request)
	if err != nil {
		return chatResponse{}, fmt.Errorf("не удалось собрать JSON-запрос: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return chatResponse{}, fmt.Errorf("не удалось создать HTTP-запрос: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
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
