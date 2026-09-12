package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const summaryMessagePrefix = "Краткое содержание предыдущей части диалога:"

type CompressionConfig struct {
	Enabled          bool
	KeepLastMessages int
	ChunkSize        int
	SummaryPath      string
}

type RuntimeSettings struct {
	CompressionEnabled bool `json:"compression_enabled"`
	KeepLastMessages   int  `json:"keep_last_messages"`
	SummaryChunkSize   int  `json:"summary_chunk_size"`
	ContextLimit       int  `json:"context_limit"`
}

type ConversationSummary struct {
	Version         int       `json:"version"`
	Summary         string    `json:"summary"`
	CoveredMessages int       `json:"covered_messages"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SummaryStore interface {
	Load(ctx context.Context) (ConversationSummary, error)
	Save(ctx context.Context, summary ConversationSummary) error
}

type ResettableSummaryStore interface {
	SummaryStore
	Reset(ctx context.Context) error
}

type JSONSummaryStore struct {
	path string
	mu   sync.Mutex
}

type Summarizer interface {
	Summarize(ctx context.Context, existing ConversationSummary, messages []chatMessage) (string, error)
}

type LocalSummarySummarizer struct {
	MaxItems int
	MaxChars int
}

type APISummarizerConfig struct {
	APIKey      string
	BaseURL     string
	Model       string
	Timeout     time.Duration
	MaxTokens   int
	Temperature float64
	Thinking    bool
}

type APISummarizer struct {
	cfg    APISummarizerConfig
	client *http.Client
}

type CompressedContext struct {
	Messages                []chatMessage       `json:"messages"`
	Summary                 ConversationSummary `json:"summary"`
	OriginalHistoryTokens   int                 `json:"original_history_tokens"`
	CompressedHistoryTokens int                 `json:"compressed_history_tokens"`
	SavedTokens             int                 `json:"saved_tokens"`
}

type CompressionReport struct {
	Enabled                    bool    `json:"enabled"`
	Mode                       string  `json:"mode"`
	TotalHistoryMessages       int     `json:"total_history_messages"`
	KeepLastMessages           int     `json:"keep_last_messages"`
	ChunkSize                  int     `json:"chunk_size"`
	FullPromptInputTokens      int     `json:"full_prompt_input_tokens"`
	ActualPromptInputTokens    int     `json:"actual_prompt_input_tokens"`
	EstimatedSavedInputTokens  int     `json:"estimated_saved_input_tokens"`
	EstimatedSavedPercent      float64 `json:"estimated_saved_percent"`
	SummaryCoversMessages      int     `json:"summary_covers_messages"`
	RecentMessagesKept         int     `json:"recent_messages_kept"`
	PendingOldMessages         int     `json:"pending_old_messages"`
	NextSummaryUpdateAfter     int     `json:"next_summary_update_after"`
	SummaryUpdateStatus        string  `json:"summary_update_status"`
	MessagesUntilSummaryUpdate int     `json:"messages_until_summary_update"`
	SummaryUpdateLabel         string  `json:"summary_update_label"`
	SummaryUpdatedNow          bool    `json:"summary_updated_now"`
	NewlyCompressedMessages    int     `json:"newly_compressed_messages"`
	Reason                     string  `json:"reason"`
	SavingStatus               string  `json:"saving_status"`
	ContextLimit               int     `json:"context_limit"`
	ContextLimitStatus         string  `json:"context_limit_status"`

	FullInputTokens       int     `json:"full_input_tokens"`
	CompressedInputTokens int     `json:"compressed_input_tokens"`
	SavedTokens           int     `json:"saved_tokens"`
	SavedPercent          float64 `json:"saved_percent"`
	PendingMessages       int     `json:"pending_messages"`
}

type SummaryUpdateReport struct {
	Updated                 bool
	NewlyCompressedMessages int
	Reason                  string
	Status                  string
}

func NewJSONSummaryStore(path string) *JSONSummaryStore {
	return &JSONSummaryStore{path: path}
}

func (s *JSONSummaryStore) Load(ctx context.Context) (ConversationSummary, error) {
	if s == nil || s.path == "" {
		return ConversationSummary{}, nil
	}
	if err := ctx.Err(); err != nil {
		return ConversationSummary{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ConversationSummary{}, nil
		}
		return ConversationSummary{}, fmt.Errorf("не удалось прочитать summary: %w", err)
	}
	if len(data) == 0 {
		return ConversationSummary{}, nil
	}

	var summary ConversationSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return ConversationSummary{}, fmt.Errorf("не удалось разобрать summary: %w", err)
	}
	if summary.Version != 1 {
		return ConversationSummary{}, fmt.Errorf("неподдерживаемая версия summary: %d", summary.Version)
	}
	if summary.CoveredMessages < 0 {
		return ConversationSummary{}, fmt.Errorf("summary содержит некорректное количество сообщений: %d", summary.CoveredMessages)
	}
	return summary, nil
}

func (s *JSONSummaryStore) Save(ctx context.Context, summary ConversationSummary) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if summary.CoveredMessages < 0 {
		return fmt.Errorf("summary содержит некорректное количество сообщений: %d", summary.CoveredMessages)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("не удалось создать папку summary: %w", err)
		}
	}

	summary.Version = 1
	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось собрать summary: %w", err)
	}
	data = append(data, '\n')

	tmpPath := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("не удалось записать временный файл summary: %w", err)
	}
	defer os.Remove(tmpPath)

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("не удалось заменить файл summary: %w", err)
	}
	return nil
}

func (s *JSONSummaryStore) Reset(ctx context.Context) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("не удалось очистить summary: %w", err)
	}
	return nil
}

func DefaultCompressionConfig() CompressionConfig {
	return CompressionConfig{
		KeepLastMessages: 6,
		ChunkSize:        10,
		SummaryPath:      defaultSummaryPath,
	}
}

func normalizeCompressionConfig(cfg CompressionConfig) CompressionConfig {
	defaults := DefaultCompressionConfig()
	if cfg.KeepLastMessages <= 0 {
		cfg.KeepLastMessages = defaults.KeepLastMessages
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = defaults.ChunkSize
	}
	if strings.TrimSpace(cfg.SummaryPath) == "" {
		cfg.SummaryPath = defaults.SummaryPath
	}
	return cfg
}

func NewAPISummarizer(cfg APISummarizerConfig, client *http.Client) *APISummarizer {
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
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	return &APISummarizer{cfg: cfg, client: client}
}

func (s *APISummarizer) Summarize(ctx context.Context, existing ConversationSummary, messages []chatMessage) (string, error) {
	if s == nil {
		return "", errors.New("API summarizer is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(s.cfg.APIKey) == "" {
		return "", errors.New("переменная окружения DEEPSEEK_API_KEY не задана для обновления summary")
	}
	request := chatRequest{
		Model: s.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: summarySystemPrompt()},
			{Role: "user", Content: summaryUserPrompt(existing, messages)},
		},
		Temperature: s.cfg.Temperature,
		MaxTokens:   s.cfg.MaxTokens,
		Stream:      false,
	}
	if !s.cfg.Thinking {
		request.Thinking = &thinkingConfig{Type: "disabled"}
	}

	decoded, err := completeChat(ctx, s.client, s.cfg.BaseURL, s.cfg.APIKey, s.cfg.Timeout, request)
	if err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 {
		return "", errors.New("DeepSeek API вернул summary-ответ без choices")
	}
	summary := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if summary == "" {
		return "", errors.New("DeepSeek API вернул пустой summary")
	}
	return summary, nil
}

func summarySystemPrompt() string {
	return strings.TrimSpace(`Ты обновляешь summary предыдущей части диалога для будущего prompt.

Напиши связную краткую сводку, которая сохраняет ключевые факты, решения, ограничения, цели, пользовательские предпочтения, текущий статус и важные незавершенные задачи.
Не пересказывай каждую реплику и не делай протокол по сообщениям.
Не добавляй выдуманные детали.
Учитывай existing summary и новый блок сообщений: обнови общую сводку так, чтобы она заменила старую часть истории.
Верни только текст summary, без markdown-заголовков и служебных пояснений.`)
}

func summaryUserPrompt(existing ConversationSummary, messages []chatMessage) string {
	var b strings.Builder
	b.WriteString("Existing summary:\n")
	if text := strings.TrimSpace(existing.Summary); text != "" {
		b.WriteString(text)
	} else {
		b.WriteString("(empty)")
	}
	b.WriteString("\n\nNew messages to merge into summary:\n")
	for i, message := range messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "message"
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			content = "(empty)"
		}
		fmt.Fprintf(&b, "%d. %s: %s\n", i+1, role, content)
	}
	return strings.TrimSpace(b.String())
}

func (s LocalSummarySummarizer) Summarize(ctx context.Context, existing ConversationSummary, messages []chatMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	maxItems := s.MaxItems
	if maxItems <= 0 {
		maxItems = 12
	}
	maxChars := s.MaxChars
	if maxChars <= 0 {
		maxChars = 180
	}
	maxChars = minInt(maxChars, 64)

	userItems := make([]string, 0, maxItems)
	assistantItems := make([]string, 0, maxItems)
	contextItems := make([]string, 0, maxItems)
	skipped := maxInt(0, len(messages)-maxItems)
	selected := selectSummaryMessages(messages, maxItems)
	maxPerRole := maxInt(1, maxItems/4)

	for _, message := range selected {
		item := summarizeMessageContent(message.Content, maxChars)
		if item == "" {
			continue
		}
		switch message.Role {
		case "user":
			if len(userItems) < maxPerRole {
				userItems = append(userItems, item)
			}
		case "assistant":
			if len(assistantItems) < maxPerRole {
				assistantItems = append(assistantItems, item)
			}
		default:
			if len(contextItems) < maxPerRole {
				contextItems = append(contextItems, item)
			}
		}
	}

	paragraphs := make([]string, 0, 5)
	if previous := strings.TrimSpace(existing.Summary); previous != "" {
		paragraphs = append(paragraphs, previous)
	}

	intro := "Сводка предыдущего диалога."
	if skipped > 0 {
		intro += fmt.Sprintf(" В новом блоке было больше сообщений, поэтому локальная эвристика сохранила %d репрезентативных.", maxItems)
	}
	paragraphs = append(paragraphs, intro)

	if len(userItems) > 0 {
		paragraphs = append(paragraphs, "Пользователь сообщил или попросил: "+joinSummaryItems(userItems)+".")
	}
	if len(assistantItems) > 0 {
		paragraphs = append(paragraphs, "Ассистент уже ответил или зафиксировал: "+joinSummaryItems(assistantItems)+".")
	}
	if len(contextItems) > 0 {
		paragraphs = append(paragraphs, "Дополнительный контекст: "+joinSummaryItems(contextItems)+".")
	}
	paragraphs = append(paragraphs, "Важно: prompt получает summary, старые сообщения, которые еще ждут следующего summary chunk, последние сообщения и текущий запрос; полная история остается отдельно.")

	return strings.TrimSpace(strings.Join(paragraphs, "\n\n")), nil
}

func selectSummaryMessages(messages []chatMessage, maxItems int) []chatMessage {
	if maxItems <= 0 || len(messages) <= maxItems {
		return cloneMessages(messages)
	}
	head := maxItems / 2
	tail := maxItems - head
	selected := make([]chatMessage, 0, maxItems)
	selected = append(selected, messages[:head]...)
	selected = append(selected, messages[len(messages)-tail:]...)
	return cloneMessages(selected)
}

func summarizeMessageContent(content string, maxChars int) string {
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return ""
	}
	content = trimAtSentenceOrWord(content, maxChars)
	content = strings.TrimRight(content, ".,;:!?")
	return content
}

func trimAtSentenceOrWord(text string, maxChars int) string {
	if maxChars <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	cut := string(runes[:maxChars])
	lastSentence := strings.LastIndexAny(cut, ".!?")
	if lastSentence >= maxChars/2 {
		return strings.TrimSpace(cut[:lastSentence+1])
	}
	lastSpace := strings.LastIndex(cut, " ")
	if lastSpace >= maxChars/2 {
		return strings.TrimSpace(cut[:lastSpace])
	}
	return strings.TrimSpace(cut)
}

func joinSummaryItems(items []string) string {
	cleaned := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			cleaned = append(cleaned, item)
		}
	}
	return strings.Join(cleaned, "; ")
}

func buildCompressedContext(system string, history []chatMessage, userPrompt string, summary ConversationSummary, cfg CompressionConfig, pricing TokenPricing, calibration TokenCalibration) CompressedContext {
	cfg = normalizeCompressionConfig(cfg)
	summary = normalizeSummaryForHistory(summary, len(history))

	fullMessages := buildChatMessages(system, history, userPrompt)
	compressedHistory := buildCompressedHistory(history, summary)
	compressedMessages := buildChatMessages(system, compressedHistory, userPrompt)

	fullReport := EstimateChatTokenReport(fullMessages, 0, pricing, calibration)
	compressedReport := EstimateChatTokenReport(compressedMessages, 0, pricing, calibration)
	saved := fullReport.PromptTokens - compressedReport.PromptTokens
	if saved < 0 {
		saved = 0
	}

	return CompressedContext{
		Messages:                compressedMessages,
		Summary:                 summary,
		OriginalHistoryTokens:   fullReport.HistoryTokens,
		CompressedHistoryTokens: compressedReport.HistoryTokens,
		SavedTokens:             saved,
	}
}

func buildCompressedHistory(history []chatMessage, summary ConversationSummary) []chatMessage {
	summary = normalizeSummaryForHistory(summary, len(history))
	messages := make([]chatMessage, 0, len(history)-summary.CoveredMessages+1)
	if strings.TrimSpace(summary.Summary) != "" && summary.CoveredMessages > 0 {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: summaryMessagePrefix + "\n" + strings.TrimSpace(summary.Summary),
		})
	}
	messages = append(messages, history[summary.CoveredMessages:]...)
	return messages
}

func buildChatMessages(system string, history []chatMessage, userPrompt string) []chatMessage {
	system = strings.TrimSpace(system)
	messages := make([]chatMessage, 0, len(history)+2)
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	messages = append(messages, history...)
	messages = append(messages, chatMessage{Role: "user", Content: strings.TrimSpace(userPrompt)})
	return messages
}

func buildCompressionReport(enabled bool, fullReport TokenReport, actualReport TokenReport, summary ConversationSummary, cfg CompressionConfig, historyLen int, update SummaryUpdateReport) CompressionReport {
	cfg = normalizeCompressionConfig(cfg)
	summary = normalizeSummaryForHistory(summary, historyLen)

	actualInput := fullReport.PromptTokens
	if enabled {
		actualInput = actualReport.PromptTokens
	}
	saved := fullReport.PromptTokens - actualInput
	if saved < 0 {
		saved = 0
	}
	percent := 0.0
	if fullReport.PromptTokens > 0 {
		percent = float64(saved) * 100 / float64(fullReport.PromptTokens)
	}

	mode := "disabled"
	if enabled {
		mode = "enabled"
	}
	targetCovered := targetCoveredMessages(historyLen, cfg)
	pendingOldMessages := maxInt(0, targetCovered-summary.CoveredMessages)
	recentMessagesKept := minInt(cfg.KeepLastMessages, maxInt(0, historyLen-summary.CoveredMessages-pendingOldMessages))
	summaryUpdateStatus, messagesUntilSummaryUpdate, summaryUpdateLabel := summaryUpdateState(enabled, summary, cfg, historyLen, pendingOldMessages, update)
	reason := strings.TrimSpace(update.Reason)
	if reason == "" {
		reason = compressionReason(enabled, summary, cfg, historyLen, pendingOldMessages)
	}
	savingStatus := compressionSavingStatus(enabled, summary, saved)
	contextLimitStatus := compressionContextLimitStatus(actualReport)

	return CompressionReport{
		Enabled:                    enabled,
		Mode:                       mode,
		TotalHistoryMessages:       historyLen,
		KeepLastMessages:           cfg.KeepLastMessages,
		ChunkSize:                  cfg.ChunkSize,
		FullPromptInputTokens:      fullReport.PromptTokens,
		ActualPromptInputTokens:    actualInput,
		EstimatedSavedInputTokens:  saved,
		EstimatedSavedPercent:      percent,
		SummaryCoversMessages:      summary.CoveredMessages,
		RecentMessagesKept:         recentMessagesKept,
		PendingOldMessages:         pendingOldMessages,
		NextSummaryUpdateAfter:     messagesUntilSummaryUpdate,
		SummaryUpdateStatus:        summaryUpdateStatus,
		MessagesUntilSummaryUpdate: messagesUntilSummaryUpdate,
		SummaryUpdateLabel:         summaryUpdateLabel,
		SummaryUpdatedNow:          update.Updated,
		NewlyCompressedMessages:    update.NewlyCompressedMessages,
		Reason:                     reason,
		SavingStatus:               savingStatus,
		ContextLimit:               actualReport.ContextLimit,
		ContextLimitStatus:         contextLimitStatus,

		FullInputTokens:       fullReport.PromptTokens,
		CompressedInputTokens: actualInput,
		SavedTokens:           saved,
		SavedPercent:          percent,
		PendingMessages:       pendingOldMessages,
	}
}

func maybeUpdateSummary(ctx context.Context, history []chatMessage, summary ConversationSummary, cfg CompressionConfig, store SummaryStore, summarizer Summarizer) (ConversationSummary, SummaryUpdateReport, error) {
	if !cfg.Enabled || store == nil {
		return summary, SummaryUpdateReport{Reason: "compression is disabled", Status: "disabled"}, nil
	}
	cfg = normalizeCompressionConfig(cfg)
	summary = normalizeSummaryForHistory(summary, len(history))

	targetCovered := targetCoveredMessages(len(history), cfg)
	if targetCovered <= summary.CoveredMessages {
		return summary, SummaryUpdateReport{Reason: compressionReason(true, summary, cfg, len(history), 0), Status: "not_needed"}, nil
	}
	if targetCovered-summary.CoveredMessages < cfg.ChunkSize {
		return summary, SummaryUpdateReport{Reason: "pending block is smaller than summary chunk size", Status: "waiting"}, nil
	}
	if summarizer == nil {
		summarizer = LocalSummarySummarizer{}
	}

	block := cloneMessages(history[summary.CoveredMessages:targetCovered])
	newlyCompressed := len(block)
	text, err := summarizer.Summarize(ctx, summary, block)
	if err != nil {
		return summary, SummaryUpdateReport{}, err
	}

	updated := ConversationSummary{
		Version:         1,
		Summary:         text,
		CoveredMessages: targetCovered,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.Save(ctx, updated); err != nil {
		return summary, SummaryUpdateReport{}, err
	}
	return updated, SummaryUpdateReport{
		Updated:                 true,
		NewlyCompressedMessages: newlyCompressed,
		Reason:                  "pending block reached summary chunk size",
		Status:                  "updated",
	}, nil
}

func summaryUpdateState(enabled bool, summary ConversationSummary, cfg CompressionConfig, historyLen int, pendingOldMessages int, update SummaryUpdateReport) (string, int, string) {
	if !enabled {
		return "disabled", 0, "Summary update disabled"
	}
	if update.Updated || update.Status == "updated" {
		return "updated", 0, "Summary updated"
	}
	switch update.Status {
	case "disabled":
		return "disabled", 0, "Summary update disabled"
	case "summarizing":
		return "summarizing", 0, "Summarizing old messages"
	case "failed":
		return "failed", 0, "Summary update failed"
	case "not_needed":
		return "not_needed", 0, "Summary is up to date"
	}

	if pendingOldMessages <= 0 {
		return "not_needed", 0, "Summary is up to date"
	}
	if pendingOldMessages >= cfg.ChunkSize {
		return "ready", 0, "Ready to summarize now"
	}
	messagesUntilSummaryUpdate := maxInt(0, cfg.ChunkSize-pendingOldMessages)
	return "waiting", messagesUntilSummaryUpdate, fmt.Sprintf("Waiting for %d more messages", messagesUntilSummaryUpdate)
}

func normalizeSummaryForHistory(summary ConversationSummary, historyLen int) ConversationSummary {
	if summary.CoveredMessages < 0 {
		summary.CoveredMessages = 0
	}
	if summary.CoveredMessages > historyLen {
		summary.CoveredMessages = historyLen
	}
	if strings.TrimSpace(summary.Summary) == "" {
		summary.CoveredMessages = 0
	}
	return summary
}

func targetCoveredMessages(historyLen int, cfg CompressionConfig) int {
	target := historyLen - cfg.KeepLastMessages
	if target < 0 {
		return 0
	}
	return target
}

func compressionReason(enabled bool, summary ConversationSummary, cfg CompressionConfig, historyLen int, pendingOldMessages int) string {
	if !enabled {
		return "compression is disabled"
	}
	if historyLen <= cfg.KeepLastMessages && summary.CoveredMessages == 0 {
		return "history is still within keep-last-messages window"
	}
	if pendingOldMessages <= 0 {
		return "all old messages are already covered by summary"
	}
	if pendingOldMessages < cfg.ChunkSize {
		return "pending block is smaller than summary chunk size"
	}
	return "pending block reached summary chunk size"
}

func compressionSavingStatus(enabled bool, summary ConversationSummary, saved int) string {
	if !enabled {
		return "compression disabled"
	}
	if summary.CoveredMessages == 0 {
		return "no summary yet"
	}
	if saved == 0 {
		return "summary exists, but compressed prompt is not shorter yet"
	}
	return "compressed prompt is shorter than full prompt"
}

func compressionContextLimitStatus(report TokenReport) string {
	if report.ContextLimit <= 0 {
		return "disabled"
	}
	if report.OverflowTokens > 0 {
		return fmt.Sprintf("actual prompt input exceeds local context-limit by %d tokens", report.OverflowTokens)
	}
	return fmt.Sprintf("actual prompt input fits local context-limit with %d tokens remaining", report.RemainingTokens)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
