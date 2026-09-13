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
	APIKey      string
	BaseURL     string
	Model       string
	System      string
	Timeout     time.Duration
	MaxTokens   int
	Strategy    string
	Window      int
	Temperature float64
	Thinking    bool
	Memory      MessageStore
}

type Agent struct {
	cfg    AgentConfig
	client *http.Client
	mu     sync.Mutex
}

type AgentResponse struct {
	Content      string
	Usage        *tokenUsage
	Context      ContextMetadata
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
	strategyConfigured := strings.TrimSpace(cfg.Strategy) != ""
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	cfg.Strategy = normalizeStrategyName(cfg.Strategy)
	if cfg.Window < 0 || cfg.Window == 0 && !strategyConfigured {
		cfg.Window = defaultWindowMessages
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

	state, err := a.State(ctx)
	if err != nil {
		return AgentResponse{}, err
	}
	if a.usesBranchingStrategy() {
		state = normalizeBranchState(state)
	}

	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	contextOutput, err := a.buildContext(userPrompt, state)
	if err != nil {
		return AgentResponse{}, err
	}

	payload, err := json.Marshal(a.buildRequest(contextOutput.Messages))
	if err != nil {
		return AgentResponse{}, fmt.Errorf("не удалось собрать JSON-запрос: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return AgentResponse{}, fmt.Errorf("не удалось создать HTTP-запрос: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return AgentResponse{}, fmt.Errorf("DeepSeek API недоступен: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return AgentResponse{}, fmt.Errorf("не удалось прочитать ответ DeepSeek: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return AgentResponse{}, fmt.Errorf("DeepSeek API вернул HTTP %d: %s", resp.StatusCode, formatAPIError(respBody))
	}

	var decoded chatResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return AgentResponse{}, fmt.Errorf("не удалось разобрать JSON-ответ DeepSeek: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return AgentResponse{}, errors.New("DeepSeek API вернул ответ без choices")
	}

	answer := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if answer == "" {
		return AgentResponse{}, errors.New("DeepSeek API вернул пустой ответ")
	}

	if a.usesBranchingStrategy() {
		branchIndex := activeBranchIndex(state.Branches)
		if branchIndex == -1 {
			return AgentResponse{}, errors.New("active branch не найдена")
		}
		state.Branches.Items[branchIndex].Messages = append(cloneMessages(state.Branches.Items[branchIndex].Messages),
			chatMessage{Role: "user", Content: userPrompt},
			chatMessage{Role: "assistant", Content: answer},
		)
		if err := a.saveState(ctx, state); err != nil {
			return AgentResponse{}, err
		}
		active := state.Branches.Items[branchIndex]
		contextOutput.Metadata.BranchID = active.ID
		contextOutput.Metadata.BranchTitle = active.Title
		contextOutput.Metadata.CheckpointMessages = len(state.Branches.Checkpoint)
		contextOutput.Metadata.BranchMessages = len(active.Messages)
		contextOutput.Metadata.HistoryMessages = len(state.Branches.Checkpoint) + len(active.Messages)
	} else {
		updatedHistory := append(cloneMessages(state.Messages),
			chatMessage{Role: "user", Content: userPrompt},
			chatMessage{Role: "assistant", Content: answer},
		)
		updatedHistory, err = a.trimStoredHistory(updatedHistory)
		if err != nil {
			return AgentResponse{}, err
		}
		factsForTurn := state.Facts
		if a.usesFactsStrategy() {
			factsForTurn = MergeFacts(state.Facts, ExtractFacts(userPrompt))
		}
		state.Messages = updatedHistory
		state.Facts = factsForTurn
		if err := a.saveState(ctx, state); err != nil {
			return AgentResponse{}, err
		}
		contextOutput.Metadata.HistoryMessages = len(updatedHistory)
		if a.usesFactsStrategy() {
			contextOutput.Metadata.FactsCount = countFacts(factsForTurn)
		}
	}

	return AgentResponse{
		Content:      answer,
		Usage:        decoded.Usage,
		Context:      contextOutput.Metadata,
		FinishReason: decoded.Choices[0].FinishReason,
	}, nil
}

func (a *Agent) History(ctx context.Context) ([]chatMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.State(ctx)
	if err != nil {
		return nil, err
	}
	if a.usesBranchingStrategy() {
		return visibleBranchHistory(state), nil
	}
	return state.Messages, nil
}

func (a *Agent) Facts(ctx context.Context) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.State(ctx)
	if err != nil {
		return nil, err
	}
	return cloneFacts(state.Facts), nil
}

func (a *Agent) State(ctx context.Context) (ConversationState, error) {
	if a.cfg.Memory == nil {
		return ConversationState{}, nil
	}
	if store, ok := a.cfg.Memory.(ConversationStore); ok {
		state, err := store.LoadState(ctx)
		if err != nil {
			return ConversationState{}, err
		}
		return ConversationState{
			Messages: cloneMessages(state.Messages),
			Facts:    cloneFacts(state.Facts),
			Branches: cloneBranchState(state.Branches),
		}, nil
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
	var err error
	if store, ok := a.cfg.Memory.(ConversationStore); ok {
		err = store.SaveState(ctx, state)
	} else {
		err = a.cfg.Memory.Save(ctx, state.Messages)
	}
	if err != nil {
		return fmt.Errorf("не удалось сохранить историю: %w", err)
	}
	return nil
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

	a.cfg.Strategy = normalizeStrategyName(settings.Strategy)
	a.cfg.Window = settings.WindowMessages
	return nil
}

func (a *Agent) Branches(ctx context.Context) (BranchState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.State(ctx)
	if err != nil {
		return BranchState{}, err
	}
	state = normalizeBranchState(state)
	return cloneBranchState(state.Branches), nil
}

func (a *Agent) CreateCheckpoint(ctx context.Context) (BranchState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.State(ctx)
	if err != nil {
		return BranchState{}, err
	}
	state = normalizeBranchState(state)
	index := activeBranchIndex(state.Branches)
	if index == -1 {
		return BranchState{}, errors.New("active branch не найдена")
	}
	active := state.Branches.Items[index]
	checkpoint := make([]chatMessage, 0, len(state.Branches.Checkpoint)+len(active.Messages))
	checkpoint = append(checkpoint, state.Branches.Checkpoint...)
	checkpoint = append(checkpoint, active.Messages...)
	state.Branches.Checkpoint = checkpoint
	state.Branches.Items[index].Messages = nil
	if err := a.saveState(ctx, state); err != nil {
		return BranchState{}, err
	}
	return cloneBranchState(state.Branches), nil
}

func (a *Agent) CreateBranch(ctx context.Context, title string) (BranchState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.State(ctx)
	if err != nil {
		return BranchState{}, err
	}
	state = normalizeBranchState(state)
	title = strings.TrimSpace(title)
	if title == "" {
		title = defaultNewBranchTitle(state.Branches)
	}
	id := nextBranchID(state.Branches)
	state.Branches.Items = append(state.Branches.Items, ConversationBranch{
		ID:    id,
		Title: title,
	})
	state.Branches.ActiveBranchID = id
	if err := a.saveState(ctx, state); err != nil {
		return BranchState{}, err
	}
	return cloneBranchState(state.Branches), nil
}

func (a *Agent) SwitchBranch(ctx context.Context, id string) (BranchState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.State(ctx)
	if err != nil {
		return BranchState{}, err
	}
	state = normalizeBranchState(state)
	id = strings.TrimSpace(id)
	if findBranchIndex(state.Branches, id) == -1 {
		return BranchState{}, fmt.Errorf("ветка %q не найдена", id)
	}
	state.Branches.ActiveBranchID = id
	if err := a.saveState(ctx, state); err != nil {
		return BranchState{}, err
	}
	return cloneBranchState(state.Branches), nil
}

func (a *Agent) DeleteBranch(ctx context.Context, id string) (BranchState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.State(ctx)
	if err != nil {
		return BranchState{}, err
	}
	state = normalizeBranchState(state)
	id = strings.TrimSpace(id)
	if len(state.Branches.Items) <= 1 {
		return BranchState{}, errors.New("нельзя удалить последнюю ветку")
	}
	index := findBranchIndex(state.Branches, id)
	if index == -1 {
		return BranchState{}, fmt.Errorf("ветка %q не найдена", id)
	}
	state.Branches.Items = append(state.Branches.Items[:index], state.Branches.Items[index+1:]...)
	if state.Branches.ActiveBranchID == id {
		state.Branches.ActiveBranchID = state.Branches.Items[0].ID
	}
	if err := a.saveState(ctx, state); err != nil {
		return BranchState{}, err
	}
	return cloneBranchState(state.Branches), nil
}

func (a *Agent) buildContext(userPrompt string, state ConversationState) (ContextOutput, error) {
	strategy, err := NewContextStrategy(a.cfg.Strategy, a.cfg.Window)
	if err != nil {
		return ContextOutput{}, err
	}
	return strategy.Build(ContextInput{
		System:     a.cfg.System,
		History:    state.Messages,
		Facts:      state.Facts,
		Branches:   state.Branches,
		UserPrompt: userPrompt,
	})
}

func (a *Agent) trimStoredHistory(messages []chatMessage) ([]chatMessage, error) {
	strategy, err := NewContextStrategy(a.cfg.Strategy, a.cfg.Window)
	if err != nil {
		return nil, err
	}
	return strategy.TrimStoredHistory(messages)
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

func (a *Agent) usesFactsStrategy() bool {
	return normalizeStrategyName(a.cfg.Strategy) == StrategyFacts
}

func (a *Agent) usesBranchingStrategy() bool {
	return normalizeStrategyName(a.cfg.Strategy) == StrategyBranching
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
