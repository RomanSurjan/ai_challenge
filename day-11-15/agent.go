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
	APIKey           string
	BaseURL          string
	Model            string
	System           string
	Timeout          time.Duration
	MaxTokens        int
	Temperature      float64
	Thinking         bool
	Memory           *MemoryLayers
	Selection        *MemorySelection
	AutoMemory       *bool
	InvariantChecker InvariantChecker
}

type Agent struct {
	cfg        AgentConfig
	client     *http.Client
	selection  MemorySelection
	builder    ContextBuilder
	checker    InvariantChecker
	mu         sync.Mutex
	autoMemory bool
}

type AgentResponse struct {
	DialogID      string
	Content       string
	Usage         *tokenUsage
	FinishReason  string
	MemoryUpdates []MemoryUpdate
	TaskState     *TaskState
}

type chatMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolDefinition struct {
	Type     string             `json:"type"`
	Function toolFunctionSchema `json:"function"`
}

type toolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type MemoryUpdate struct {
	Layer    string `json:"layer"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model       string           `json:"model"`
	Messages    []chatMessage    `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Thinking    *thinkingConfig  `json:"thinking,omitempty"`
	Tools       []toolDefinition `json:"tools,omitempty"`
	ToolChoice  any              `json:"tool_choice,omitempty"`
	Stream      bool             `json:"stream"`
}

type namedToolChoice struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
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
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *tokenUsage `json:"usage,omitempty"`
}

type apiError struct {
	Error any `json:"error"`
}

var (
	ErrDeepSeek       = errors.New("ошибка DeepSeek")
	ErrMemoryToolCall = errors.New("модель предложила некорректное обновление памяти")
)

type MemoryToolErrorCategory string

const (
	MemoryToolErrorInvalidCall   MemoryToolErrorCategory = "invalid_call"
	MemoryToolErrorUnknownTool   MemoryToolErrorCategory = "unknown_tool"
	MemoryToolErrorMalformedJSON MemoryToolErrorCategory = "malformed_json"
	MemoryToolErrorMissingField  MemoryToolErrorCategory = "missing_field"
	MemoryToolErrorUnknownField  MemoryToolErrorCategory = "unknown_field"
	MemoryToolErrorInvalidType   MemoryToolErrorCategory = "invalid_type"
	MemoryToolErrorTrailingJSON  MemoryToolErrorCategory = "trailing_json"
	MemoryToolErrorInvalidValue  MemoryToolErrorCategory = "invalid_value"
	MemoryToolErrorTooLong       MemoryToolErrorCategory = "too_long"
	MemoryToolErrorSensitive     MemoryToolErrorCategory = "sensitive"
)

// MemoryToolCallError contains only safe structural metadata. It intentionally
// never stores raw arguments, field values, prompts, paths, or credentials.
type MemoryToolCallError struct {
	Tool     string                  `json:"tool,omitempty"`
	Category MemoryToolErrorCategory `json:"category"`
	Field    string                  `json:"field,omitempty"`
	JSONType string                  `json:"json_type,omitempty"`
	Rule     string                  `json:"validation_rule,omitempty"`
}

func (e *MemoryToolCallError) Error() string {
	if e == nil {
		return ErrMemoryToolCall.Error()
	}
	message := string(e.Category)
	if e.Tool != "" {
		message += ": tool=" + e.Tool
	}
	if e.Field != "" {
		message += ", field=" + e.Field
	}
	if e.JSONType != "" {
		message += ", json_type=" + e.JSONType
	}
	if e.Rule != "" {
		message += ", validation_rule=" + e.Rule
	}
	return message
}

func (e *MemoryToolCallError) Unwrap() error { return ErrMemoryToolCall }

const (
	maxMemoryToolRounds      = 4
	maxMemoryOperations      = 8
	maxWorkingGoalRunes      = 2000
	maxWorkingContentRunes   = 4000
	maxProfileKeyRunes       = 120
	maxProfileValueRunes     = 2000
	maxDecisionRunes         = 4000
	maxRationaleRunes        = 4000
	maxKnowledgeTopicRunes   = 240
	maxKnowledgeContentRunes = 6000
)

const memoryPolicyInstruction = `Автоматическая память: не записывай ничего, если это не пригодится позже. Working используй только для текущей задачи; profile — для устойчивых предпочтений и данных пользователя; decisions — для действительно принятых решений; knowledge — для проверенных или явно сообщённых знаний. Любую явную команду пользователя изменить состояние задачи выполняй ровно одним соответствующим task-инструментом до финального ответа. Обязательно вызывай этот инструмент даже если считаешь операцию запрещённой текущим состоянием: только backend FSM проверяет guards и формирует отказ. Не имитируй изменение или отказ обычным текстом, не добавляй вторую task-операцию и не переходи на следующий этап автоматически, если пользователь явно запросил только создание, утверждение плана, обновление прогресса, один переход, результат валидации, pause или resume. Не дублируй факты, не считай каждую реплику долговременной памятью и не записывай догадки как факты. Не сохраняй секреты или чувствительные персональные, медицинские или финансовые данные. Не утверждай, что память обновлена, если инструмент не был вызван или вернул ошибку.`

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

	selection := AllMemorySelection()
	if cfg.Selection != nil {
		selection = *cfg.Selection
	}
	autoMemory := true
	if cfg.AutoMemory != nil {
		autoMemory = *cfg.AutoMemory
	}
	checker := cfg.InvariantChecker
	if checker == nil {
		checker = TermInvariantChecker{}
	}
	return &Agent{cfg: cfg, client: client, selection: selection, checker: checker, autoMemory: autoMemory}
}

func (a *Agent) Ask(ctx context.Context, userPrompt string) (AgentResponse, error) {
	return a.AskInDialog(ctx, "", userPrompt)
}

func (a *Agent) AskInDialog(ctx context.Context, dialogID, userPrompt string) (AgentResponse, error) {
	if strings.TrimSpace(a.cfg.APIKey) == "" {
		return AgentResponse{}, errors.New("переменная окружения DEEPSEEK_API_KEY не задана")
	}

	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return AgentResponse{}, errors.New("передайте текст через -prompt или stdin")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	resolvedID := dialogID
	if a.cfg.Memory != nil {
		dialog, err := a.cfg.Memory.GetDialog(ctx, dialogID)
		if err != nil {
			return AgentResponse{}, err
		}
		resolvedID = dialog.ID
	}

	memory, err := a.loadContextMemory(ctx, resolvedID)
	if err != nil {
		return AgentResponse{}, err
	}
	preCheck := a.checker.Check(InvariantTargetRequest, userPrompt, memory.Invariants)
	if !preCheck.Allowed {
		return AgentResponse{}, &InvariantViolationError{Target: InvariantTargetRequest, Violations: preCheck.Violations}
	}

	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	baseRequest := a.buildRequest(userPrompt, memory)
	request := baseRequest
	for attempt := 1; attempt <= maxInvariantAnswerAttempts; attempt++ {
		result, err := a.completeAttempt(ctx, request, memory.Working)
		if err != nil {
			return AgentResponse{}, err
		}
		postCheck := a.checker.Check(InvariantTargetResponse, result.answer, memory.Invariants)
		if !postCheck.Allowed {
			if attempt == maxInvariantAnswerAttempts {
				return AgentResponse{}, &InvariantViolationError{Target: InvariantTargetResponse, Violations: postCheck.Violations}
			}
			request = requestWithInvariantFeedback(baseRequest, postCheck.Violations)
			continue
		}

		commit, err := a.commitAcceptedTurn(ctx, resolvedID, userPrompt, result.answer, result.pending)
		if err != nil {
			return AgentResponse{}, err
		}

		var taskState *TaskState
		if a.cfg.Memory != nil && a.selection.Working && commit.Working != nil {
			if commit.Working.Task != nil {
				state := *commit.Working.Task
				taskState = &state
			}
		}

		return AgentResponse{
			DialogID:      resolvedID,
			Content:       result.answer,
			Usage:         result.response.Usage,
			FinishReason:  result.finishReason,
			MemoryUpdates: commit.Updates,
			TaskState:     taskState,
		}, nil
	}
	return AgentResponse{}, errors.New("не удалось завершить проверку инвариантов")
}

type completionAttempt struct {
	answer       string
	finishReason string
	response     chatResponse
	pending      []pendingMemoryOperation
}

func (a *Agent) completeAttempt(ctx context.Context, request chatRequest, working WorkingMemory) (completionAttempt, error) {
	request.Messages = append([]chatMessage(nil), request.Messages...)
	request.Tools = append([]toolDefinition(nil), request.Tools...)
	pending := make([]pendingMemoryOperation, 0, maxMemoryOperations)
	simulatedWorking := cloneWorkingMemory(working)
	seenCallIDs := make(map[string]string)
	seenOperations := make(map[string]struct{})
	toolRounds := 0
	operationCount := 0
	taskTransitionsThisTurn := 0
	forcedTaskTool := namedToolChoiceName(request.ToolChoice)
	forcedTaskToolCalled := false

	for {
		decoded, err := a.complete(ctx, request)
		if err != nil {
			return completionAttempt{}, err
		}
		choice := decoded.Choices[0]
		if len(choice.Message.ToolCalls) > 0 || choice.FinishReason == "tool_calls" {
			if len(choice.Message.ToolCalls) == 0 || len(request.Tools) == 0 {
				return completionAttempt{}, fmt.Errorf("%w: неполный tool call", ErrMemoryToolCall)
			}
			toolRounds++
			if toolRounds > maxMemoryToolRounds {
				return completionAttempt{}, fmt.Errorf("%w: превышен лимит раундов инструментов", ErrMemoryToolCall)
			}
			if forcedTaskTool != "" && !forcedTaskToolCalled {
				if len(choice.Message.ToolCalls) != 1 || choice.Message.ToolCalls[0].Function.Name != forcedTaskTool {
					return completionAttempt{}, &MemoryToolCallError{Tool: forcedTaskTool, Category: MemoryToolErrorInvalidCall}
				}
				forcedTaskToolCalled = true
				request.ToolChoice = "none"
			}

			choice.Message.Role = "assistant"
			request.Messages = append(request.Messages, choice.Message)
			for _, call := range choice.Message.ToolCalls {
				operationCount++
				if operationCount > maxMemoryOperations {
					return completionAttempt{}, fmt.Errorf("%w: превышен лимит операций памяти", ErrMemoryToolCall)
				}
				op, canonical, err := a.validateMemoryToolCall(call)
				if err != nil {
					return completionAttempt{}, err
				}
				if previous, exists := seenCallIDs[call.ID]; exists && previous != canonical {
					return completionAttempt{}, fmt.Errorf("%w: повторный ID инструмента с другими аргументами", ErrMemoryToolCall)
				}
				seenCallIDs[call.ID] = canonical
				if op.name == "task_transition" && taskTransitionsThisTurn > 0 {
					return completionAttempt{}, taskConflictForWorking(working, "transition", op.stage, "за один пользовательский ход разрешён только один переход")
				}
				result := `{"status":"accepted"}`
				if _, duplicate := seenOperations[canonical]; duplicate {
					result = `{"status":"duplicate","applied":false}`
				} else if isTaskToolName(op.name) {
					next, simulationErr := simulateTaskOperation(simulatedWorking, op)
					if simulationErr != nil {
						var stateErr *TaskStateError
						if errors.As(simulationErr, &stateErr) {
							return completionAttempt{}, stateErr
						}
						return completionAttempt{}, fmt.Errorf("%w: некорректная task operation", ErrMemoryToolCall)
					} else {
						seenOperations[canonical] = struct{}{}
						pending = append(pending, op)
						simulatedWorking = next
						if op.name == "task_transition" {
							taskTransitionsThisTurn++
						}
						result = taskToolResult(simulatedWorking.Task, nil)
					}
				} else {
					seenOperations[canonical] = struct{}{}
					pending = append(pending, op)
				}
				request.Messages = append(request.Messages, chatMessage{Role: "tool", Content: result, ToolCallID: call.ID})
			}
			continue
		}

		answer := strings.TrimSpace(choice.Message.Content)
		if forcedTaskTool != "" && !forcedTaskToolCalled {
			return completionAttempt{}, &MemoryToolCallError{Tool: forcedTaskTool, Category: MemoryToolErrorInvalidCall}
		}
		if answer == "" {
			return completionAttempt{}, fmt.Errorf("%w: API вернул пустой ответ", ErrDeepSeek)
		}
		return completionAttempt{answer: answer, finishReason: choice.FinishReason, response: decoded, pending: pending}, nil
	}
}

func requestWithInvariantFeedback(base chatRequest, violations []InvariantViolation) chatRequest {
	request := base
	request.Messages = append([]chatMessage(nil), base.Messages...)
	feedback := chatMessage{Role: "system", Content: formatInvariantRetryFeedback(violations)}
	insertAt := len(request.Messages)
	if insertAt > 0 && request.Messages[insertAt-1].Role == "user" {
		insertAt--
	}
	request.Messages = append(request.Messages, chatMessage{})
	copy(request.Messages[insertAt+1:], request.Messages[insertAt:])
	request.Messages[insertAt] = feedback
	return request
}

func formatInvariantRetryFeedback(violations []InvariantViolation) string {
	lines := []string{"[INVARIANT_VALIDATION]", "Предыдущий черновик программно отклонён. Сформируй новый ответ, который соблюдает каждое правило ниже; не цитируй запрещённые элементы."}
	for _, violation := range violations {
		lines = append(lines, fmt.Sprintf("- %s: %s. %s", violation.InvariantID, violation.Description, invariantConflictReason(violation)))
	}
	lines = append(lines, "[END_INVARIANT_VALIDATION]")
	return strings.Join(lines, "\n")
}

func (a *Agent) History(ctx context.Context) ([]chatMessage, error) {
	return a.HistoryInDialog(ctx, "")
}

func (a *Agent) HistoryInDialog(ctx context.Context, dialogID string) ([]chatMessage, error) {
	memory, err := a.ShortTermMemoryInDialog(ctx, dialogID)
	if err != nil {
		return nil, err
	}
	messages := make([]chatMessage, 0, len(memory.Turns)*2)
	for _, turn := range memory.Turns {
		messages = append(messages,
			chatMessage{Role: "user", Content: turn.User},
			chatMessage{Role: "assistant", Content: turn.Assistant},
		)
	}
	return messages, nil
}

func (a *Agent) ShortTermMemory(ctx context.Context) (ShortTermMemory, error) {
	return a.ShortTermMemoryInDialog(ctx, "")
}

func (a *Agent) ShortTermMemoryInDialog(ctx context.Context, dialogID string) (ShortTermMemory, error) {
	if a.cfg.Memory == nil {
		return ShortTermMemory{}, nil
	}
	return a.cfg.Memory.LoadDialogShortTerm(ctx, dialogID)
}

func (a *Agent) WorkingMemory(ctx context.Context) (WorkingMemory, error) {
	return a.WorkingMemoryInDialog(ctx, "")
}

func (a *Agent) WorkingMemoryInDialog(ctx context.Context, dialogID string) (WorkingMemory, error) {
	if a.cfg.Memory == nil {
		return WorkingMemory{}, nil
	}
	return a.cfg.Memory.LoadDialogWorking(ctx, dialogID)
}

func (a *Agent) TaskState(ctx context.Context) (*TaskState, error) {
	return a.TaskStateInDialog(ctx, "")
}

func (a *Agent) TaskStateInDialog(ctx context.Context, dialogID string) (*TaskState, error) {
	memory, err := a.WorkingMemoryInDialog(ctx, dialogID)
	if err != nil || memory.Task == nil {
		return nil, err
	}
	state := *memory.Task
	return &state, nil
}

func (a *Agent) CreateTaskInDialog(ctx context.Context, dialogID, goal, currentStep, expectedAction string) (TaskState, error) {
	if a.cfg.Memory == nil {
		return TaskState{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.CreateTaskInDialog(ctx, dialogID, goal, currentStep, expectedAction)
}

func (a *Agent) UpdateTaskProgressInDialog(ctx context.Context, dialogID, currentStep, expectedAction string) (TaskState, error) {
	if a.cfg.Memory == nil {
		return TaskState{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.UpdateTaskProgressInDialog(ctx, dialogID, currentStep, expectedAction)
}

func (a *Agent) ApproveTaskPlanInDialog(ctx context.Context, dialogID, plan string) (TaskState, error) {
	if a.cfg.Memory == nil {
		return TaskState{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.ApproveTaskPlanInDialog(ctx, dialogID, plan)
}

func (a *Agent) TransitionTaskInDialog(ctx context.Context, dialogID string, stage TaskStage) (TaskState, error) {
	if a.cfg.Memory == nil {
		return TaskState{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.TransitionTaskInDialog(ctx, dialogID, stage)
}

func (a *Agent) RecordTaskValidationInDialog(ctx context.Context, dialogID string, passed bool, details string) (TaskState, error) {
	if a.cfg.Memory == nil {
		return TaskState{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.RecordTaskValidationInDialog(ctx, dialogID, passed, details)
}

func (a *Agent) PauseTaskInDialog(ctx context.Context, dialogID string) (TaskState, error) {
	if a.cfg.Memory == nil {
		return TaskState{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.PauseTaskInDialog(ctx, dialogID)
}

func (a *Agent) ResumeTaskInDialog(ctx context.Context, dialogID string) (TaskState, error) {
	if a.cfg.Memory == nil {
		return TaskState{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.ResumeTaskInDialog(ctx, dialogID)
}

func (a *Agent) LongTermMemory(ctx context.Context) (LongTermMemory, error) {
	if a.cfg.Memory == nil {
		return LongTermMemory{}, nil
	}
	return a.cfg.Memory.LoadLongTerm(ctx)
}

func (a *Agent) Invariants(ctx context.Context) (InvariantSet, error) {
	if a.cfg.Memory == nil {
		return InvariantSet{}, errors.New("хранилище инвариантов не настроено")
	}
	return a.cfg.Memory.LoadInvariants(ctx)
}

// SetInvariants is a trusted host-configuration operation. It is intentionally
// not exposed as a model tool or a writable public HTTP endpoint.
func (a *Agent) SetInvariants(ctx context.Context, set InvariantSet) (InvariantSet, error) {
	if a.cfg.Memory == nil {
		return InvariantSet{}, errors.New("хранилище инвариантов не настроено")
	}
	return a.cfg.Memory.SetInvariants(ctx, set)
}

func (a *Agent) Profile(ctx context.Context) (*UserProfile, error) {
	if a.cfg.Memory == nil {
		return nil, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.LoadProfile(ctx)
}

func (a *Agent) SetProfile(ctx context.Context, profile UserProfile) (*UserProfile, error) {
	if a.cfg.Memory == nil {
		return nil, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.SetProfile(ctx, profile)
}

func (a *Agent) SetWorkingGoal(ctx context.Context, goal string) (WorkingMemory, error) {
	return a.SetWorkingGoalInDialog(ctx, "", goal)
}

func (a *Agent) SetWorkingGoalInDialog(ctx context.Context, dialogID, goal string) (WorkingMemory, error) {
	if a.cfg.Memory == nil {
		return WorkingMemory{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.SetDialogWorkingGoal(ctx, dialogID, goal)
}

func (a *Agent) SetWorkingStatus(ctx context.Context, status TaskStatus) (WorkingMemory, error) {
	return a.SetWorkingStatusInDialog(ctx, "", status)
}

func (a *Agent) SetWorkingStatusInDialog(ctx context.Context, dialogID string, status TaskStatus) (WorkingMemory, error) {
	if a.cfg.Memory == nil {
		return WorkingMemory{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.SetDialogWorkingStatus(ctx, dialogID, status)
}

func (a *Agent) AddWorkingNote(ctx context.Context, content string) (WorkingMemory, error) {
	return a.AddWorkingNoteInDialog(ctx, "", content)
}

func (a *Agent) AddWorkingNoteInDialog(ctx context.Context, dialogID, content string) (WorkingMemory, error) {
	if a.cfg.Memory == nil {
		return WorkingMemory{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.AddDialogWorkingNote(ctx, dialogID, content)
}

func (a *Agent) AddWorkingResult(ctx context.Context, content string) (WorkingMemory, error) {
	return a.AddWorkingResultInDialog(ctx, "", content)
}

func (a *Agent) AddWorkingResultInDialog(ctx context.Context, dialogID, content string) (WorkingMemory, error) {
	if a.cfg.Memory == nil {
		return WorkingMemory{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.AddDialogWorkingResult(ctx, dialogID, content)
}

func (a *Agent) UpsertProfile(ctx context.Context, key, value string) (LongTermMemory, error) {
	if a.cfg.Memory == nil {
		return LongTermMemory{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.UpsertProfile(ctx, key, value)
}

func (a *Agent) AddDecision(ctx context.Context, statement, rationale string) (LongTermMemory, error) {
	if a.cfg.Memory == nil {
		return LongTermMemory{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.AddDecision(ctx, statement, rationale)
}

func (a *Agent) AddKnowledge(ctx context.Context, topic, content string) (LongTermMemory, error) {
	if a.cfg.Memory == nil {
		return LongTermMemory{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.AddKnowledge(ctx, topic, content)
}

func (a *Agent) Initialize(ctx context.Context) (DialogMetadata, error) {
	if a.cfg.Memory == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.Initialize(ctx)
}

func (a *Agent) CreateDialog(ctx context.Context, title string) (DialogMetadata, error) {
	if a.cfg.Memory == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.CreateDialog(ctx, title)
}

func (a *Agent) Dialogs(ctx context.Context) (DialogList, error) {
	if a.cfg.Memory == nil {
		return DialogList{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.ListDialogs(ctx)
}

func (a *Agent) Dialog(ctx context.Context, dialogID string) (DialogMetadata, error) {
	if a.cfg.Memory == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.GetDialog(ctx, dialogID)
}

func (a *Agent) SelectDialog(ctx context.Context, dialogID string) (DialogMetadata, error) {
	if a.cfg.Memory == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	return a.cfg.Memory.SelectDialog(ctx, dialogID)
}

func (a *Agent) loadContextMemory(ctx context.Context, dialogID string) (ContextMemory, error) {
	var memory ContextMemory
	if a.cfg.Memory == nil {
		return memory, nil
	}
	var err error
	memory.Invariants, err = a.cfg.Memory.LoadInvariants(ctx)
	if err != nil {
		return ContextMemory{}, fmt.Errorf("не удалось загрузить программные инварианты: %w", err)
	}
	if a.selection.Working {
		memory.Working, err = a.cfg.Memory.LoadDialogWorking(ctx, dialogID)
		if err != nil {
			return ContextMemory{}, err
		}
	}
	longTerm, err := a.cfg.Memory.LoadLongTerm(ctx)
	if err != nil {
		return ContextMemory{}, err
	}
	memory.Profile = longTerm.EffectiveProfile()
	if a.selection.LongTerm {
		memory.LongTerm = longTerm
	}
	if a.selection.ShortTerm {
		memory.ShortTerm, err = a.cfg.Memory.LoadDialogShortTerm(ctx, dialogID)
		if err != nil {
			return ContextMemory{}, err
		}
	}
	return memory, nil
}

func (a *Agent) buildRequest(userPrompt string, memory ContextMemory) chatRequest {
	messages := a.builder.Build(a.cfg.System, userPrompt, a.selection, memory)
	tools := a.memoryTools()
	if len(tools) > 0 {
		policy := chatMessage{Role: "system", Content: memoryPolicyInstruction}
		insertAt := 0
		if strings.TrimSpace(a.cfg.System) != "" && len(messages) > 0 {
			insertAt = 1
		}
		messages = append(messages, chatMessage{})
		copy(messages[insertAt+1:], messages[insertAt:])
		messages[insertAt] = policy
	}
	req := chatRequest{
		Model:       a.cfg.Model,
		Messages:    messages,
		Temperature: a.cfg.Temperature,
		MaxTokens:   a.cfg.MaxTokens,
		Tools:       tools,
		Stream:      false,
	}
	if len(tools) > 0 {
		req.ToolChoice = "auto"
		if !a.cfg.Thinking {
			if toolName := taskToolForExplicitCommand(userPrompt); toolName != "" && containsTool(tools, toolName) {
				choice := namedToolChoice{Type: "function"}
				choice.Function.Name = toolName
				req.ToolChoice = choice
			}
		}
	}
	if !a.cfg.Thinking {
		req.Thinking = &thinkingConfig{Type: "disabled"}
	}
	return req
}

func containsTool(tools []toolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func namedToolChoiceName(choice any) string {
	switch value := choice.(type) {
	case namedToolChoice:
		return value.Function.Name
	case *namedToolChoice:
		if value != nil {
			return value.Function.Name
		}
	}
	return ""
}

func taskToolForExplicitCommand(prompt string) string {
	value := strings.ToLower(strings.TrimSpace(prompt))
	if value == "" || strings.HasSuffix(value, "?") {
		return ""
	}
	switch {
	case strings.Contains(value, "создай задачу") || strings.Contains(value, "create a task"):
		return "task_create"
	case strings.Contains(value, "утверждаю план") || strings.Contains(value, "утверди план") || strings.Contains(value, "approve the plan"):
		return "task_approve_plan"
	case strings.Contains(value, "обнови прогресс") || strings.Contains(value, "update progress"):
		return "task_update_progress"
	case strings.Contains(value, "зафиксируй") && strings.Contains(value, "валидац"), strings.Contains(value, "record validation"):
		return "task_record_validation"
	case strings.Contains(value, "сними задачу с пауз") || strings.Contains(value, "возобнови задачу") || strings.Contains(value, "resume the task"):
		return "task_resume"
	case strings.Contains(value, "поставь задачу на пауз") || strings.Contains(value, "pause the task"):
		return "task_pause"
	case explicitTransitionCommand(value):
		return "task_transition"
	default:
		return ""
	}
}

func explicitTransitionCommand(value string) bool {
	verb := strings.Contains(value, "перейди в ") || strings.Contains(value, "переведи задачу в ") || strings.Contains(value, "transition to ")
	if !verb {
		return false
	}
	for _, stage := range []TaskStage{TaskStagePlanning, TaskStageExecution, TaskStageValidation, TaskStageDone} {
		if strings.Contains(value, string(stage)) {
			return true
		}
	}
	return false
}

func (a *Agent) complete(ctx context.Context, request chatRequest) (chatResponse, error) {
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
		return chatResponse{}, fmt.Errorf("%w: API недоступен: %v", ErrDeepSeek, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatResponse{}, fmt.Errorf("%w: не удалось прочитать ответ: %v", ErrDeepSeek, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return chatResponse{}, fmt.Errorf("%w: API вернул HTTP %d: %s", ErrDeepSeek, resp.StatusCode, formatAPIError(respBody))
	}
	var decoded chatResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return chatResponse{}, fmt.Errorf("%w: не удалось разобрать JSON-ответ: %v", ErrDeepSeek, err)
	}
	if len(decoded.Choices) == 0 {
		return chatResponse{}, fmt.Errorf("%w: API вернул ответ без choices", ErrDeepSeek)
	}
	return decoded, nil
}

func (a *Agent) memoryTools() []toolDefinition {
	if !a.autoMemory || a.cfg.Memory == nil {
		return nil
	}
	tools := make([]toolDefinition, 0, 14)
	if a.selection.Working {
		tools = append(tools,
			memoryTool("memory_set_working_goal", "Установить цель только для текущей задачи выбранного диалога.", map[string]any{"goal": stringProperty("Цель текущей задачи.")}, []string{"goal"}),
			memoryTool("memory_set_working_status", "Установить валидный статус текущей задачи, не обходя ограничения её жизненного цикла.", map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{string(TaskStatusNotStarted), string(TaskStatusInProgress), string(TaskStatusBlocked), string(TaskStatusCompleted)}},
			}, []string{"status"}),
			memoryTool("memory_add_working_note", "Добавить рабочую заметку, полезную только для текущей задачи.", map[string]any{"content": stringProperty("Рабочая заметка.")}, []string{"content"}),
			memoryTool("memory_add_working_result", "Добавить промежуточный результат текущей задачи.", map[string]any{"content": stringProperty("Промежуточный результат.")}, []string{"content"}),
			memoryTool("task_create", "Вызвать только по явной просьбе создать задачу. Создаёт её на этапе planning; не добавляй другие task-вызовы в этом ходе.", map[string]any{
				"goal": boundedStringProperty("Цель задачи.", maxWorkingGoalRunes), "current_step": boundedStringProperty("Первый шаг планирования.", maxWorkingContentRunes), "expected_action": boundedStringProperty("Следующее ожидаемое действие.", maxWorkingContentRunes),
			}, []string{"goal", "current_step", "expected_action"}),
			memoryTool("task_update_progress", "Вызвать только по явной просьбе обновить прогресс. Обновляет шаг и ожидаемое действие без смены этапа; не добавляй другие task-вызовы.", map[string]any{
				"current_step": boundedStringProperty("Текущий шаг.", maxWorkingContentRunes), "expected_action": boundedStringProperty("Следующее ожидаемое действие.", maxWorkingContentRunes),
			}, []string{"current_step", "expected_action"}),
			memoryTool("task_approve_plan", "Вызвать только когда пользователь явно утверждает план. Фиксирует план, но не переводит задачу в execution и не добавляет другие task-вызовы.", map[string]any{"plan": boundedStringProperty("Полный утверждённый план.", maxWorkingContentRunes)}, []string{"plan"}),
			memoryTool("task_transition", "Всегда вызвать по явной просьбе перейти на указанный этап, даже если ожидаешь отказ guard: решение принимает backend FSM. Выполняет ровно один переход; не добавляй другие task-вызовы.", map[string]any{
				"stage": map[string]any{"type": "string", "enum": []string{string(TaskStagePlanning), string(TaskStageExecution), string(TaskStageValidation), string(TaskStageDone)}},
			}, []string{"stage"}),
			memoryTool("task_record_validation", "Вызвать только по явной просьбе зафиксировать результат валидации. Не переводит задачу в done и не добавляет другие task-вызовы.", map[string]any{
				"passed": map[string]any{"type": "boolean"}, "details": boundedStringProperty("Проверки и их результат.", maxWorkingContentRunes),
			}, []string{"passed", "details"}),
			memoryTool("task_pause", "Вызвать только по явной просьбе поставить активную задачу на паузу. Передай пустой JSON-объект и не добавляй другие task-вызовы.", map[string]any{}, nil),
			memoryTool("task_resume", "Вызвать только по явной просьбе продолжить задачу. Передай пустой JSON-объект, сохрани позицию и не добавляй другие task-вызовы.", map[string]any{}, nil),
		)
	}
	if a.selection.LongTerm {
		tools = append(tools,
			memoryTool("memory_upsert_profile", "Сохранить устойчивую черту профиля или предпочтение пользователя.", map[string]any{"key": stringProperty("Краткий стабильный ключ."), "value": stringProperty("Устойчивое значение.")}, []string{"key", "value"}),
			memoryTool("memory_add_decision", "Сохранить действительно принятое решение для будущих диалогов.", map[string]any{"statement": stringProperty("Формулировка принятого решения."), "rationale": stringProperty("Обоснование, если оно было явно дано.")}, []string{"statement", "rationale"}),
			memoryTool("memory_add_knowledge", "Сохранить проверенное или явно сообщённое знание, полезное в будущих диалогах.", map[string]any{"topic": stringProperty("Краткая тема."), "content": stringProperty("Проверенное знание.")}, []string{"topic", "content"}),
		)
	}
	return tools
}

func memoryTool(name, description string, properties map[string]any, required []string) toolDefinition {
	parameters := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		parameters["required"] = required
	}
	return toolDefinition{Type: "function", Function: toolFunctionSchema{
		Name: name, Description: description,
		Parameters: parameters,
	}}
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func boundedStringProperty(description string, maxLength int) map[string]any {
	return map[string]any{"type": "string", "description": description, "minLength": 1, "maxLength": maxLength}
}

type pendingMemoryOperation struct {
	name           string
	goal           string
	status         TaskStatus
	content        string
	key            string
	value          string
	statement      string
	rationale      string
	topic          string
	currentStep    string
	expectedAction string
	plan           string
	stage          TaskStage
	validationPass bool
	details        string
}

func (a *Agent) validateMemoryToolCall(call toolCall) (pendingMemoryOperation, string, error) {
	if strings.TrimSpace(call.ID) == "" || len(call.ID) > 200 || call.Type != "function" {
		return pendingMemoryOperation{}, "", &MemoryToolCallError{Category: MemoryToolErrorInvalidCall}
	}
	allowed := make(map[string]struct{})
	for _, tool := range a.memoryTools() {
		allowed[tool.Function.Name] = struct{}{}
	}
	if _, ok := allowed[call.Function.Name]; !ok {
		return pendingMemoryOperation{}, "", &MemoryToolCallError{Category: MemoryToolErrorUnknownTool}
	}
	toolName := call.Function.Name
	op := pendingMemoryOperation{name: toolName}
	var err error
	switch toolName {
	case "memory_set_working_goal":
		var args struct {
			Goal string `json:"goal"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.goal = args.Goal
		err = firstError(err, validateToolText(toolName, "goal", op.goal, maxWorkingGoalRunes, true, hasToolField(fields, "goal")))
	case "memory_set_working_status":
		var args struct {
			Status TaskStatus `json:"status"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = firstError(decodeErr, requireToolField(toolName, fields, "status"))
		op.status = args.Status
		if err == nil && !validTaskStatus(op.status) {
			err = &MemoryToolCallError{Tool: toolName, Category: MemoryToolErrorInvalidValue, Field: "status"}
		}
	case "memory_add_working_note", "memory_add_working_result":
		var args struct {
			Content string `json:"content"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.content = args.Content
		err = firstError(err, validateToolText(toolName, "content", op.content, maxWorkingContentRunes, true, hasToolField(fields, "content")))
	case "task_create":
		var args struct {
			Goal           string `json:"goal"`
			CurrentStep    string `json:"current_step"`
			ExpectedAction string `json:"expected_action"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.goal, op.currentStep, op.expectedAction = args.Goal, args.CurrentStep, args.ExpectedAction
		err = firstError(err, validateToolText(toolName, "goal", op.goal, maxWorkingGoalRunes, true, hasToolField(fields, "goal")))
		err = firstError(err, validateToolText(toolName, "current_step", op.currentStep, maxWorkingContentRunes, true, hasToolField(fields, "current_step")))
		err = firstError(err, validateToolText(toolName, "expected_action", op.expectedAction, maxWorkingContentRunes, true, hasToolField(fields, "expected_action")))
	case "task_update_progress":
		var args struct {
			CurrentStep    string `json:"current_step"`
			ExpectedAction string `json:"expected_action"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.currentStep, op.expectedAction = args.CurrentStep, args.ExpectedAction
		err = firstError(err, validateToolText(toolName, "current_step", op.currentStep, maxWorkingContentRunes, true, hasToolField(fields, "current_step")))
		err = firstError(err, validateToolText(toolName, "expected_action", op.expectedAction, maxWorkingContentRunes, true, hasToolField(fields, "expected_action")))
	case "task_approve_plan":
		var args struct {
			Plan string `json:"plan"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.plan = args.Plan
		err = firstError(err, validateToolText(toolName, "plan", op.plan, maxWorkingContentRunes, true, hasToolField(fields, "plan")))
	case "task_transition":
		var args struct {
			Stage string `json:"stage"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = firstError(decodeErr, requireToolField(toolName, fields, "stage"))
		if err == nil {
			op.stage, decodeErr = ParseTaskStage(args.Stage)
			if decodeErr != nil {
				err = &MemoryToolCallError{Tool: toolName, Category: MemoryToolErrorInvalidValue, Field: "stage"}
			}
		}
	case "task_record_validation":
		var args struct {
			Passed  bool   `json:"passed"`
			Details string `json:"details"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = firstError(decodeErr, requireToolField(toolName, fields, "passed"))
		op.validationPass, op.details = args.Passed, args.Details
		err = firstError(err, validateToolText(toolName, "details", op.details, maxWorkingContentRunes, true, hasToolField(fields, "details")))
	case "task_pause", "task_resume":
		var args struct{}
		_, err = decodeToolArguments(toolName, call.Function.Arguments, &args, true)
	case "memory_upsert_profile":
		var args struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.key, op.value = args.Key, args.Value
		err = firstError(err, validateToolText(toolName, "key", op.key, maxProfileKeyRunes, true, hasToolField(fields, "key")))
		err = firstError(err, validateToolText(toolName, "value", op.value, maxProfileValueRunes, true, hasToolField(fields, "value")))
	case "memory_add_decision":
		var args struct {
			Statement string `json:"statement"`
			Rationale string `json:"rationale"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.statement, op.rationale = args.Statement, args.Rationale
		err = firstError(err, validateToolText(toolName, "statement", op.statement, maxDecisionRunes, true, hasToolField(fields, "statement")))
		err = firstError(err, validateToolText(toolName, "rationale", op.rationale, maxRationaleRunes, true, hasToolField(fields, "rationale")))
	case "memory_add_knowledge":
		var args struct {
			Topic   string `json:"topic"`
			Content string `json:"content"`
		}
		fields, decodeErr := decodeToolArguments(toolName, call.Function.Arguments, &args, false)
		err = decodeErr
		op.topic, op.content = args.Topic, args.Content
		err = firstError(err, validateToolText(toolName, "topic", op.topic, maxKnowledgeTopicRunes, true, hasToolField(fields, "topic")))
		err = firstError(err, validateToolText(toolName, "content", op.content, maxKnowledgeContentRunes, true, hasToolField(fields, "content")))
	}
	if err != nil {
		return pendingMemoryOperation{}, "", err
	}
	op.trim()
	canonical := strings.Join([]string{
		op.name, op.goal, string(op.status), op.content, op.key, op.value, op.statement, op.rationale, op.topic,
		op.currentStep, op.expectedAction, op.plan, string(op.stage), fmt.Sprintf("%t", op.validationPass), op.details,
	}, "\x00")
	return op, canonical, nil
}

func decodeToolArguments(tool, raw string, target any, allowEmpty bool) (map[string]json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" && allowEmpty {
		raw = "{}"
	}
	var fields map[string]json.RawMessage
	shapeDecoder := json.NewDecoder(strings.NewReader(raw))
	if err := shapeDecoder.Decode(&fields); err != nil {
		return nil, classifyToolJSONError(tool, err)
	}
	if err := shapeDecoder.Decode(&struct{}{}); err != io.EOF {
		return nil, &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorTrailingJSON}
	}
	if fields == nil {
		return nil, &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorInvalidType, JSONType: "null"}
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, classifyToolJSONError(tool, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorTrailingJSON}
	}
	for field, rawValue := range fields {
		if bytes.Equal(bytes.TrimSpace(rawValue), []byte("null")) {
			return nil, &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorInvalidType, Field: safeToolIdentifier(field), JSONType: "null"}
		}
	}
	return fields, nil
}

func classifyToolJSONError(tool string, err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorInvalidType, Field: typeErr.Field, JSONType: typeErr.Value}
	}
	const unknownPrefix = "json: unknown field \""
	message := err.Error()
	if strings.HasPrefix(message, unknownPrefix) && strings.HasSuffix(message, "\"") {
		field := safeToolIdentifier(strings.TrimSuffix(strings.TrimPrefix(message, unknownPrefix), "\""))
		return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorUnknownField, Field: field}
	}
	return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorMalformedJSON}
}

func safeToolIdentifier(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return ""
	}
	return value
}

func hasToolField(fields map[string]json.RawMessage, field string) bool {
	_, ok := fields[field]
	return ok
}

func requireToolField(tool string, fields map[string]json.RawMessage, field string) error {
	if hasToolField(fields, field) {
		return nil
	}
	return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorMissingField, Field: field}
}

func validateToolText(tool, field, value string, maxRunes int, required, present bool) error {
	if required && !present {
		return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorMissingField, Field: field}
	}
	value = strings.TrimSpace(value)
	if required && value == "" {
		return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorInvalidValue, Field: field}
	}
	if len([]rune(value)) > maxRunes {
		return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorTooLong, Field: field}
	}
	if rule := sensitiveRuleFor(value); value != "" && rule != "" {
		return &MemoryToolCallError{Tool: tool, Category: MemoryToolErrorSensitive, Field: field, Rule: string(rule)}
	}
	return nil
}

func firstError(current, next error) error {
	if current != nil {
		return current
	}
	return next
}

func (op *pendingMemoryOperation) trim() {
	op.goal = strings.TrimSpace(op.goal)
	op.content = strings.TrimSpace(op.content)
	op.key = strings.TrimSpace(op.key)
	op.value = strings.TrimSpace(op.value)
	op.statement = strings.TrimSpace(op.statement)
	op.rationale = strings.TrimSpace(op.rationale)
	op.topic = strings.TrimSpace(op.topic)
	op.currentStep = strings.TrimSpace(op.currentStep)
	op.expectedAction = strings.TrimSpace(op.expectedAction)
	op.plan = strings.TrimSpace(op.plan)
	op.details = strings.TrimSpace(op.details)
}

func isTaskToolName(name string) bool {
	return strings.HasPrefix(name, "task_")
}

func simulateTaskOperation(memory WorkingMemory, op pendingMemoryOperation) (WorkingMemory, error) {
	nextMemory := cloneWorkingMemory(memory)
	if op.name == "task_create" {
		if nextMemory.Task != nil && nextMemory.Task.Stage != TaskStageDone {
			return WorkingMemory{}, nextMemory.Task.conflict("create_task", "", "сначала завершите активную задачу")
		}
		state, err := NewTaskState(op.currentStep, op.expectedAction, time.Now().UTC())
		if err != nil {
			return WorkingMemory{}, err
		}
		nextMemory = WorkingMemory{Goal: op.goal, Task: &state, Status: taskStatusForState(state)}
		return nextMemory, nil
	}

	state, err := requireTaskState(nextMemory)
	if err != nil {
		return WorkingMemory{}, err
	}
	var next TaskState
	switch op.name {
	case "task_update_progress":
		next, err = state.UpdateProgress(op.currentStep, op.expectedAction, time.Now().UTC())
	case "task_approve_plan":
		next, err = state.ApprovePlan(op.plan, time.Now().UTC())
	case "task_transition":
		next, err = state.Transition(op.stage, time.Now().UTC())
	case "task_record_validation":
		next, err = state.RecordValidation(op.validationPass, op.details, time.Now().UTC())
	case "task_pause":
		next, err = state.Pause(time.Now().UTC())
	case "task_resume":
		next, err = state.Resume(time.Now().UTC())
	default:
		return WorkingMemory{}, errors.New("неизвестная операция состояния задачи")
	}
	if err != nil {
		return WorkingMemory{}, err
	}
	nextMemory.Task = &next
	nextMemory.Status = taskStatusForState(next)
	return nextMemory, nil
}

func taskConflictForWorking(memory WorkingMemory, operation string, requested TaskStage, reason string) error {
	if memory.Task == nil {
		return &TaskStateError{Operation: operation, RequestedStage: requested, AllowedTransitions: []TaskStage{TaskStagePlanning}, Reason: reason}
	}
	return memory.Task.conflict(operation, requested, reason)
}

func taskToolResult(state *TaskState, operationErr error) string {
	result := map[string]any{"status": "accepted"}
	if state != nil {
		copy := *state
		result["task_state"] = copy
	}
	if operationErr != nil {
		result["status"] = "rejected"
		var stateErr *TaskStateError
		if errors.As(operationErr, &stateErr) {
			result["error"] = stateErr
		} else {
			generic := &TaskStateError{Operation: "task_tool", Reason: operationErr.Error()}
			if state != nil {
				generic.CurrentStage = state.Stage
				generic.AllowedTransitions = state.AllowedTransitions()
				generic.ExpectedAction = state.ExpectedAction
			} else {
				generic.AllowedTransitions = []TaskStage{TaskStagePlanning}
			}
			result["error"] = generic
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return `{"status":"rejected","error":{"reason":"не удалось сформировать результат инструмента"}}`
	}
	return string(data)
}

func (a *Agent) commitAcceptedTurn(ctx context.Context, dialogID, user, assistant string, pending []pendingMemoryOperation) (acceptedTurnCommit, error) {
	if a.cfg.Memory == nil {
		return acceptedTurnCommit{}, nil
	}
	working := make([]workingMemoryMutation, 0, len(pending))
	longTerm := make([]longTermMemoryMutation, 0, len(pending))
	for _, op := range pending {
		switch op.name {
		case "memory_set_working_goal":
			working = append(working, workingMemoryMutation{kind: workingMutationGoal, value: op.goal})
		case "memory_set_working_status":
			working = append(working, workingMemoryMutation{kind: workingMutationStatus, status: op.status})
		case "memory_add_working_note":
			working = append(working, workingMemoryMutation{kind: workingMutationNote, value: op.content})
		case "memory_add_working_result":
			working = append(working, workingMemoryMutation{kind: workingMutationResult, value: op.content})
		case "task_create":
			working = append(working, workingMemoryMutation{kind: workingMutationTaskCreate, value: op.goal, extra: op.currentStep, expectedAction: op.expectedAction})
		case "task_update_progress":
			working = append(working, workingMemoryMutation{kind: workingMutationTaskProgress, value: op.currentStep, expectedAction: op.expectedAction})
		case "task_approve_plan":
			working = append(working, workingMemoryMutation{kind: workingMutationPlanApprove, value: op.plan})
		case "task_transition":
			working = append(working, workingMemoryMutation{kind: workingMutationTaskTransition, stage: op.stage})
		case "task_record_validation":
			working = append(working, workingMemoryMutation{kind: workingMutationValidation, value: op.details, validationPass: op.validationPass})
		case "task_pause":
			working = append(working, workingMemoryMutation{kind: workingMutationTaskPause})
		case "task_resume":
			working = append(working, workingMemoryMutation{kind: workingMutationTaskResume})
		case "memory_upsert_profile":
			longTerm = append(longTerm, longTermMemoryMutation{kind: longTermMutationProfile, key: op.key, value: op.value})
		case "memory_add_decision":
			longTerm = append(longTerm, longTermMemoryMutation{kind: longTermMutationDecision, value: op.statement, extra: op.rationale})
		case "memory_add_knowledge":
			longTerm = append(longTerm, longTermMemoryMutation{kind: longTermMutationKnowledge, key: op.topic, value: op.content})
		}
	}

	commit, err := a.cfg.Memory.commitAcceptedTurn(ctx, dialogID, user, assistant, working, longTerm, a.selection.Working)
	if err != nil {
		return acceptedTurnCommit{}, errors.New("не удалось атомарно сохранить успешный ход")
	}
	return commit, nil
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
