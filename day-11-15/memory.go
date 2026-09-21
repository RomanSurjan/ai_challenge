package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	shortTermFileName          = "short_term.json"
	workingFileName            = "working.json"
	longTermFileName           = "long_term.json"
	dialogIndexName            = "dialogs.json"
	dialogsDirName             = "dialogs"
	legacyDialogID             = "dlg-legacy"
	transactionJournalFileName = ".turn_transaction.json"
)

var (
	ErrInvalidDialogID = errors.New("некорректный ID диалога")
	ErrDialogNotFound  = errors.New("диалог не найден")
	ErrDialogConflict  = errors.New("конфликт диалога")
)

type ConversationTurn struct {
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
	CreatedAt time.Time `json:"created_at"`
}

type ShortTermMemory struct {
	Turns []ConversationTurn `json:"turns"`
}

type TaskStatus string

const (
	TaskStatusNotStarted TaskStatus = "not_started"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusBlocked    TaskStatus = "blocked"
	TaskStatusCompleted  TaskStatus = "completed"
)

type workingMutationKind string

const (
	workingMutationGoal           workingMutationKind = "goal"
	workingMutationStatus         workingMutationKind = "status"
	workingMutationNote           workingMutationKind = "notes"
	workingMutationResult         workingMutationKind = "results"
	workingMutationTaskCreate     workingMutationKind = "task_create"
	workingMutationTaskProgress   workingMutationKind = "task_progress"
	workingMutationPlanApprove    workingMutationKind = "task_approve_plan"
	workingMutationTaskTransition workingMutationKind = "task_transition"
	workingMutationValidation     workingMutationKind = "task_validation"
	workingMutationTaskPause      workingMutationKind = "task_pause"
	workingMutationTaskResume     workingMutationKind = "task_resume"
)

type workingMemoryMutation struct {
	kind           workingMutationKind
	value          string
	extra          string
	status         TaskStatus
	stage          TaskStage
	validationPass bool
	expectedAction string
}

func (mutation workingMemoryMutation) memoryUpdate() MemoryUpdate {
	summaries := map[workingMutationKind]string{
		workingMutationGoal:           "цель текущей задачи",
		workingMutationStatus:         "статус текущей задачи",
		workingMutationNote:           "рабочая заметка",
		workingMutationResult:         "промежуточный результат",
		workingMutationTaskCreate:     "новая задача",
		workingMutationTaskProgress:   "текущий шаг задачи",
		workingMutationPlanApprove:    "утверждение плана",
		workingMutationTaskTransition: "переход задачи",
		workingMutationValidation:     "результат валидации",
		workingMutationTaskPause:      "пауза задачи",
		workingMutationTaskResume:     "продолжение задачи",
	}
	return MemoryUpdate{Layer: "working", Category: string(mutation.kind), Summary: summaries[mutation.kind]}
}

type longTermMutationKind string

const (
	longTermMutationProfile   longTermMutationKind = "profile"
	longTermMutationDecision  longTermMutationKind = "decisions"
	longTermMutationKnowledge longTermMutationKind = "knowledge"
)

type longTermMemoryMutation struct {
	kind  longTermMutationKind
	key   string
	value string
	extra string
}

func (mutation longTermMemoryMutation) memoryUpdate() MemoryUpdate {
	summaries := map[longTermMutationKind]string{
		longTermMutationProfile:   "профиль",
		longTermMutationDecision:  "решение",
		longTermMutationKnowledge: "знание",
	}
	return MemoryUpdate{Layer: "long-term", Category: string(mutation.kind), Summary: summaries[mutation.kind]}
}

type WorkingNote struct {
	Content    string    `json:"content"`
	RecordedAt time.Time `json:"recorded_at"`
}

type WorkingResult struct {
	Content    string    `json:"content"`
	RecordedAt time.Time `json:"recorded_at"`
}

type WorkingMemory struct {
	Goal    string          `json:"goal,omitempty"`
	Status  TaskStatus      `json:"status,omitempty"`
	Notes   []WorkingNote   `json:"notes,omitempty"`
	Results []WorkingResult `json:"results,omitempty"`
	Task    *TaskState      `json:"task_state,omitempty"`
}

type ProfileEntry struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Decision struct {
	Statement  string    `json:"statement"`
	Rationale  string    `json:"rationale,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

type Knowledge struct {
	Topic      string    `json:"topic"`
	Content    string    `json:"content"`
	RecordedAt time.Time `json:"recorded_at"`
}

type LongTermMemory struct {
	Profile     []ProfileEntry `json:"profile,omitempty"`
	UserProfile *UserProfile   `json:"user_profile,omitempty"`
	Decisions   []Decision     `json:"decisions,omitempty"`
	Knowledge   []Knowledge    `json:"knowledge,omitempty"`
}

type DialogMetadata struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DialogIndex struct {
	ActiveDialogID string           `json:"active_dialog_id"`
	Dialogs        []DialogMetadata `json:"dialogs"`
}

type DialogList struct {
	ActiveDialogID string           `json:"active_dialog_id"`
	Dialogs        []DialogMetadata `json:"dialogs"`
}

type ShortTermStore interface {
	Load(context.Context) (ShortTermMemory, error)
	Save(context.Context, ShortTermMemory) error
}

type WorkingMemoryStore interface {
	Load(context.Context) (WorkingMemory, error)
	Save(context.Context, WorkingMemory) error
}

type LongTermMemoryStore interface {
	Load(context.Context) (LongTermMemory, error)
	Save(context.Context, LongTermMemory) error
}

type JSONShortTermStore struct {
	file *jsonFileStore[ShortTermMemory]
}
type JSONWorkingMemoryStore struct{ file *jsonFileStore[WorkingMemory] }
type JSONLongTermMemoryStore struct {
	file *jsonFileStore[LongTermMemory]
}

func NewJSONShortTermStore(path string) *JSONShortTermStore {
	return newJSONShortTermStore(path, "краткосрочную память")
}

func newJSONShortTermStore(path, label string) *JSONShortTermStore {
	return &JSONShortTermStore{file: newJSONFileStore(path, label, validateShortTermMemory, cloneShortTermMemory)}
}

func NewJSONWorkingMemoryStore(path string) *JSONWorkingMemoryStore {
	return newJSONWorkingMemoryStore(path, "рабочую память")
}

func newJSONWorkingMemoryStore(path, label string) *JSONWorkingMemoryStore {
	return &JSONWorkingMemoryStore{file: newJSONFileStore(path, label, validateWorkingMemory, cloneWorkingMemory)}
}

func NewJSONLongTermMemoryStore(path string) *JSONLongTermMemoryStore {
	return &JSONLongTermMemoryStore{file: newJSONFileStore(path, "долговременную память", validateLongTermMemory, cloneLongTermMemory)}
}

func (s *JSONShortTermStore) Load(ctx context.Context) (ShortTermMemory, error) {
	if s == nil {
		return ShortTermMemory{}, nil
	}
	return s.file.Load(ctx)
}

func (s *JSONShortTermStore) Save(ctx context.Context, memory ShortTermMemory) error {
	if s == nil {
		return nil
	}
	return s.file.Save(ctx, memory)
}

func (s *JSONWorkingMemoryStore) Load(ctx context.Context) (WorkingMemory, error) {
	if s == nil {
		return WorkingMemory{}, nil
	}
	return s.file.Load(ctx)
}

func (s *JSONWorkingMemoryStore) Save(ctx context.Context, memory WorkingMemory) error {
	if s == nil {
		return nil
	}
	return s.file.Save(ctx, memory)
}

func (s *JSONLongTermMemoryStore) Load(ctx context.Context) (LongTermMemory, error) {
	if s == nil {
		return LongTermMemory{}, nil
	}
	return s.file.Load(ctx)
}

func (s *JSONLongTermMemoryStore) Save(ctx context.Context, memory LongTermMemory) error {
	if s == nil {
		return nil
	}
	return s.file.Save(ctx, memory)
}

type MemoryLayers struct {
	dir        string
	dialogs    *jsonFileStore[DialogIndex]
	LongTerm   LongTermMemoryStore
	Invariants InvariantStore

	dialogMu         sync.Mutex
	longTermMu       sync.Mutex
	transactionHooks *turnTransactionHooks
}

func NewJSONMemoryLayers(dir string) *MemoryLayers {
	return &MemoryLayers{
		dir:        dir,
		dialogs:    newJSONFileStore(filepath.Join(dir, dialogIndexName), "индекс диалогов", validateDialogIndex, cloneDialogIndex),
		LongTerm:   NewJSONLongTermMemoryStore(filepath.Join(dir, longTermFileName)),
		Invariants: newInvariantStoreForDirectory(dir),
	}
}

func (m *MemoryLayers) Initialize(ctx context.Context) (DialogMetadata, error) {
	if m == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return DialogMetadata{}, err
	}
	return dialogByID(index, index.ActiveDialogID)
}

func (m *MemoryLayers) CreateDialog(ctx context.Context, title string) (DialogMetadata, error) {
	if m == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return DialogMetadata{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = fmt.Sprintf("Диалог %d", len(index.Dialogs)+1)
	}
	if len([]rune(title)) > 120 {
		return DialogMetadata{}, errors.New("название диалога не может быть длиннее 120 символов")
	}
	var id string
	for attempt := 0; attempt < 4; attempt++ {
		id, err = generateDialogID()
		if err != nil {
			return DialogMetadata{}, err
		}
		if _, lookupErr := dialogByID(index, id); errors.Is(lookupErr, ErrDialogNotFound) {
			break
		}
		id = ""
	}
	if id == "" {
		return DialogMetadata{}, fmt.Errorf("%w: не удалось создать уникальный ID", ErrDialogConflict)
	}
	now := time.Now().UTC()
	dialog := DialogMetadata{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}
	index.Dialogs = append(index.Dialogs, dialog)
	index.ActiveDialogID = id
	if err := m.dialogs.Save(ctx, index); err != nil {
		return DialogMetadata{}, err
	}
	return dialog, nil
}

func (m *MemoryLayers) ListDialogs(ctx context.Context) (DialogList, error) {
	if m == nil {
		return DialogList{}, errors.New("хранилище памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return DialogList{}, err
	}
	return DialogList{ActiveDialogID: index.ActiveDialogID, Dialogs: append([]DialogMetadata(nil), index.Dialogs...)}, nil
}

func (m *MemoryLayers) GetDialog(ctx context.Context, id string) (DialogMetadata, error) {
	if m == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return DialogMetadata{}, err
	}
	return resolveDialog(index, id)
}

func (m *MemoryLayers) SelectDialog(ctx context.Context, id string) (DialogMetadata, error) {
	if err := validateDialogID(id); err != nil {
		return DialogMetadata{}, err
	}
	if m == nil {
		return DialogMetadata{}, errors.New("хранилище памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return DialogMetadata{}, err
	}
	dialog, err := dialogByID(index, id)
	if err != nil {
		return DialogMetadata{}, err
	}
	if index.ActiveDialogID != id {
		index.ActiveDialogID = id
		if err := m.dialogs.Save(ctx, index); err != nil {
			return DialogMetadata{}, err
		}
	}
	return dialog, nil
}

func (m *MemoryLayers) LoadShortTerm(ctx context.Context) (ShortTermMemory, error) {
	return m.LoadDialogShortTerm(ctx, "")
}

func (m *MemoryLayers) LoadDialogShortTerm(ctx context.Context, dialogID string) (ShortTermMemory, error) {
	if m == nil {
		return ShortTermMemory{}, nil
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return ShortTermMemory{}, err
	}
	dialog, err := resolveDialog(index, dialogID)
	if err != nil {
		return ShortTermMemory{}, err
	}
	return m.shortTermStore(dialog.ID).Load(ctx)
}

func (m *MemoryLayers) LoadWorking(ctx context.Context) (WorkingMemory, error) {
	return m.LoadDialogWorking(ctx, "")
}

func (m *MemoryLayers) LoadDialogWorking(ctx context.Context, dialogID string) (WorkingMemory, error) {
	if m == nil {
		return WorkingMemory{}, nil
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return WorkingMemory{}, err
	}
	dialog, err := resolveDialog(index, dialogID)
	if err != nil {
		return WorkingMemory{}, err
	}
	return m.workingStore(dialog.ID).Load(ctx)
}

func (m *MemoryLayers) LoadLongTerm(ctx context.Context) (LongTermMemory, error) {
	if m == nil || m.LongTerm == nil {
		return LongTermMemory{}, nil
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	if err := m.recoverTurnTransactionLocked(); err != nil {
		return LongTermMemory{}, err
	}
	m.longTermMu.Lock()
	defer m.longTermMu.Unlock()
	return m.LongTerm.Load(ctx)
}

func (m *MemoryLayers) AppendSuccessfulTurn(ctx context.Context, user, assistant string) (ShortTermMemory, error) {
	return m.AppendSuccessfulTurnToDialog(ctx, "", user, assistant)
}

func (m *MemoryLayers) AppendSuccessfulTurnToDialog(ctx context.Context, dialogID, user, assistant string) (ShortTermMemory, error) {
	turn := ConversationTurn{User: strings.TrimSpace(user), Assistant: strings.TrimSpace(assistant), CreatedAt: time.Now().UTC()}
	if err := validateConversationTurn(turn); err != nil {
		return ShortTermMemory{}, err
	}
	if m == nil {
		return ShortTermMemory{}, nil
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return ShortTermMemory{}, err
	}
	dialog, err := resolveDialog(index, dialogID)
	if err != nil {
		return ShortTermMemory{}, err
	}
	store := m.shortTermStore(dialog.ID)
	memory, err := store.Load(ctx)
	if err != nil {
		return ShortTermMemory{}, err
	}
	memory.Turns = append(memory.Turns, turn)
	if err := store.Save(ctx, memory); err != nil {
		return ShortTermMemory{}, err
	}
	if err := m.touchDialogLocked(ctx, &index, dialog.ID, turn.CreatedAt); err != nil {
		return ShortTermMemory{}, err
	}
	return cloneShortTermMemory(memory), nil
}

func (m *MemoryLayers) SetWorkingGoal(ctx context.Context, goal string) (WorkingMemory, error) {
	return m.SetDialogWorkingGoal(ctx, "", goal)
}

func (m *MemoryLayers) SetDialogWorkingGoal(ctx context.Context, dialogID, goal string) (WorkingMemory, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return WorkingMemory{}, errors.New("цель рабочей памяти не может быть пустой")
	}
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationGoal, value: goal}})
	return memory, err
}

func (m *MemoryLayers) SetWorkingStatus(ctx context.Context, status TaskStatus) (WorkingMemory, error) {
	return m.SetDialogWorkingStatus(ctx, "", status)
}

func (m *MemoryLayers) SetDialogWorkingStatus(ctx context.Context, dialogID string, status TaskStatus) (WorkingMemory, error) {
	if !validTaskStatus(status) {
		return WorkingMemory{}, fmt.Errorf("неподдерживаемый статус рабочей памяти: %q", status)
	}
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationStatus, status: status}})
	return memory, err
}

func (m *MemoryLayers) CreateTask(ctx context.Context, goal, currentStep, expectedAction string) (TaskState, error) {
	return m.CreateTaskInDialog(ctx, "", goal, currentStep, expectedAction)
}

func (m *MemoryLayers) CreateTaskInDialog(ctx context.Context, dialogID, goal, currentStep, expectedAction string) (TaskState, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return TaskState{}, errors.New("цель задачи не может быть пустой")
	}
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{
		kind: workingMutationTaskCreate, value: goal, extra: currentStep, expectedAction: expectedAction,
	}})
	return taskStateFromWorking(memory, err)
}

func (m *MemoryLayers) UpdateTaskProgressInDialog(ctx context.Context, dialogID, currentStep, expectedAction string) (TaskState, error) {
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationTaskProgress, value: currentStep, expectedAction: expectedAction}})
	return taskStateFromWorking(memory, err)
}

func (m *MemoryLayers) ApproveTaskPlanInDialog(ctx context.Context, dialogID, plan string) (TaskState, error) {
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationPlanApprove, value: plan}})
	return taskStateFromWorking(memory, err)
}

func (m *MemoryLayers) TransitionTaskInDialog(ctx context.Context, dialogID string, stage TaskStage) (TaskState, error) {
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationTaskTransition, stage: stage}})
	return taskStateFromWorking(memory, err)
}

func (m *MemoryLayers) RecordTaskValidationInDialog(ctx context.Context, dialogID string, passed bool, details string) (TaskState, error) {
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationValidation, value: details, validationPass: passed}})
	return taskStateFromWorking(memory, err)
}

func (m *MemoryLayers) PauseTaskInDialog(ctx context.Context, dialogID string) (TaskState, error) {
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationTaskPause}})
	return taskStateFromWorking(memory, err)
}

func (m *MemoryLayers) ResumeTaskInDialog(ctx context.Context, dialogID string) (TaskState, error) {
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationTaskResume}})
	return taskStateFromWorking(memory, err)
}

func (m *MemoryLayers) AddWorkingNote(ctx context.Context, content string) (WorkingMemory, error) {
	return m.AddDialogWorkingNote(ctx, "", content)
}

func (m *MemoryLayers) AddDialogWorkingNote(ctx context.Context, dialogID, content string) (WorkingMemory, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return WorkingMemory{}, errors.New("заметка рабочей памяти не может быть пустой")
	}
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationNote, value: content}})
	return memory, err
}

func (m *MemoryLayers) AddWorkingResult(ctx context.Context, content string) (WorkingMemory, error) {
	return m.AddDialogWorkingResult(ctx, "", content)
}

func (m *MemoryLayers) AddDialogWorkingResult(ctx context.Context, dialogID, content string) (WorkingMemory, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return WorkingMemory{}, errors.New("результат рабочей памяти не может быть пустым")
	}
	memory, _, err := m.applyDialogWorkingMutations(ctx, dialogID, []workingMemoryMutation{{kind: workingMutationResult, value: content}})
	return memory, err
}

func (m *MemoryLayers) applyDialogWorkingMutations(ctx context.Context, dialogID string, mutations []workingMemoryMutation) (WorkingMemory, []bool, error) {
	if m == nil {
		return WorkingMemory{}, nil, errors.New("хранилище рабочей памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	index, err := m.ensureIndexLocked(ctx)
	if err != nil {
		return WorkingMemory{}, nil, err
	}
	dialog, err := resolveDialog(index, dialogID)
	if err != nil {
		return WorkingMemory{}, nil, err
	}
	store := m.workingStore(dialog.ID)
	memory, err := store.Load(ctx)
	if err != nil {
		return WorkingMemory{}, nil, err
	}
	changed := make([]bool, len(mutations))
	for i, mutation := range mutations {
		switch mutation.kind {
		case workingMutationGoal:
			goal := strings.TrimSpace(mutation.value)
			if goal == "" {
				return WorkingMemory{}, nil, errors.New("цель рабочей памяти не может быть пустой")
			}
			if memory.Task != nil && memory.Goal != goal {
				return WorkingMemory{}, nil, memory.Task.conflict("set_goal", "", "цель активной задачи нельзя заменить; создайте новую задачу")
			}
			if memory.Task == nil {
				state, createErr := NewTaskState("", "", time.Now().UTC())
				if createErr != nil {
					return WorkingMemory{}, nil, createErr
				}
				memory.Goal = goal
				memory.Task = &state
				changed[i] = true
			}
		case workingMutationStatus:
			if !validTaskStatus(mutation.status) {
				return WorkingMemory{}, nil, fmt.Errorf("неподдерживаемый статус рабочей памяти: %q", mutation.status)
			}
			if memory.Task != nil {
				expected := taskStatusForState(*memory.Task)
				if mutation.status != expected {
					return WorkingMemory{}, nil, memory.Task.conflict("set_legacy_status", "", fmt.Sprintf("status является производным от FSM и сейчас равен %q", expected))
				}
				break
			}
			if mutation.status != TaskStatusNotStarted {
				return WorkingMemory{}, nil, &TaskStateError{Operation: "set_legacy_status", AllowedTransitions: []TaskStage{TaskStagePlanning}, Reason: "сначала создайте задачу; старый status не управляет этапами"}
			}
			if memory.Status != TaskStatusNotStarted {
				memory.Status = mutation.status
				changed[i] = true
			}
		case workingMutationNote:
			content := strings.TrimSpace(mutation.value)
			if content == "" {
				return WorkingMemory{}, nil, errors.New("заметка рабочей памяти не может быть пустой")
			}
			if !containsWorkingNote(memory.Notes, content) {
				memory.Notes = append(memory.Notes, WorkingNote{Content: content, RecordedAt: time.Now().UTC()})
				changed[i] = true
			}
		case workingMutationResult:
			content := strings.TrimSpace(mutation.value)
			if content == "" {
				return WorkingMemory{}, nil, errors.New("результат рабочей памяти не может быть пустым")
			}
			if !containsWorkingResult(memory.Results, content) {
				memory.Results = append(memory.Results, WorkingResult{Content: content, RecordedAt: time.Now().UTC()})
				changed[i] = true
			}
		case workingMutationTaskCreate:
			goal := strings.TrimSpace(mutation.value)
			if goal == "" {
				return WorkingMemory{}, nil, errors.New("цель задачи не может быть пустой")
			}
			if memory.Task != nil && memory.Task.Stage != TaskStageDone {
				return WorkingMemory{}, nil, memory.Task.conflict("create_task", "", "сначала завершите активную задачу")
			}
			state, createErr := NewTaskState(mutation.extra, mutation.expectedAction, time.Now().UTC())
			if createErr != nil {
				return WorkingMemory{}, nil, createErr
			}
			memory = WorkingMemory{Goal: goal, Task: &state}
			changed[i] = true
		case workingMutationTaskProgress:
			state, taskErr := requireTaskState(memory)
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			next, taskErr := state.UpdateProgress(mutation.value, mutation.expectedAction, time.Now().UTC())
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			memory.Task = &next
			changed[i] = true
		case workingMutationPlanApprove:
			state, taskErr := requireTaskState(memory)
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			next, taskErr := state.ApprovePlan(mutation.value, time.Now().UTC())
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			memory.Task = &next
			changed[i] = true
		case workingMutationTaskTransition:
			state, taskErr := requireTaskState(memory)
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			next, taskErr := state.Transition(mutation.stage, time.Now().UTC())
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			memory.Task = &next
			changed[i] = true
		case workingMutationValidation:
			state, taskErr := requireTaskState(memory)
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			next, taskErr := state.RecordValidation(mutation.validationPass, mutation.value, time.Now().UTC())
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			memory.Task = &next
			changed[i] = true
		case workingMutationTaskPause:
			state, taskErr := requireTaskState(memory)
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			next, taskErr := state.Pause(time.Now().UTC())
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			changed[i] = next != state
			memory.Task = &next
		case workingMutationTaskResume:
			state, taskErr := requireTaskState(memory)
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			next, taskErr := state.Resume(time.Now().UTC())
			if taskErr != nil {
				return WorkingMemory{}, nil, taskErr
			}
			memory.Task = &next
			changed[i] = true
		default:
			return WorkingMemory{}, nil, errors.New("неизвестная операция рабочей памяти")
		}
	}
	if memory.Task != nil {
		memory.Status = taskStatusForState(*memory.Task)
	} else if memory.Status == "" && (memory.Goal != "" || len(memory.Notes) > 0 || len(memory.Results) > 0) {
		memory.Status = TaskStatusNotStarted
	}
	if !anyChanged(changed) {
		return cloneWorkingMemory(memory), changed, nil
	}
	if err := store.Save(ctx, memory); err != nil {
		return WorkingMemory{}, nil, err
	}
	if err := m.touchDialogLocked(ctx, &index, dialog.ID, time.Now().UTC()); err != nil {
		return WorkingMemory{}, nil, err
	}
	return cloneWorkingMemory(memory), changed, nil
}

func (m *MemoryLayers) UpsertProfile(ctx context.Context, key, value string) (LongTermMemory, error) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return LongTermMemory{}, errors.New("ключ и значение профиля не могут быть пустыми")
	}
	memory, _, err := m.applyLongTermMutations(ctx, []longTermMemoryMutation{{kind: longTermMutationProfile, key: key, value: value}})
	return memory, err
}

func (m *MemoryLayers) AddDecision(ctx context.Context, statement, rationale string) (LongTermMemory, error) {
	statement = strings.TrimSpace(statement)
	rationale = strings.TrimSpace(rationale)
	if statement == "" {
		return LongTermMemory{}, errors.New("решение не может быть пустым")
	}
	memory, _, err := m.applyLongTermMutations(ctx, []longTermMemoryMutation{{kind: longTermMutationDecision, value: statement, extra: rationale}})
	return memory, err
}

func (m *MemoryLayers) AddKnowledge(ctx context.Context, topic, content string) (LongTermMemory, error) {
	topic = strings.TrimSpace(topic)
	content = strings.TrimSpace(content)
	if topic == "" || content == "" {
		return LongTermMemory{}, errors.New("тема и содержание знания не могут быть пустыми")
	}
	memory, _, err := m.applyLongTermMutations(ctx, []longTermMemoryMutation{{kind: longTermMutationKnowledge, key: topic, value: content}})
	return memory, err
}

func (m *MemoryLayers) ensureIndexLocked(ctx context.Context) (DialogIndex, error) {
	if strings.TrimSpace(m.dir) == "" {
		return DialogIndex{}, errors.New("каталог памяти не настроен")
	}
	if err := m.recoverTurnTransactionLocked(); err != nil {
		return DialogIndex{}, err
	}
	indexPath := filepath.Join(m.dir, dialogIndexName)
	if _, err := os.Stat(indexPath); err == nil {
		return m.dialogs.Load(ctx)
	} else if !os.IsNotExist(err) {
		return DialogIndex{}, fmt.Errorf("не удалось проверить индекс диалогов: %w", err)
	}

	legacyShortPath := filepath.Join(m.dir, shortTermFileName)
	legacyWorkingPath := filepath.Join(m.dir, workingFileName)
	shortExists, err := regularFileExists(legacyShortPath)
	if err != nil {
		return DialogIndex{}, err
	}
	workingExists, err := regularFileExists(legacyWorkingPath)
	if err != nil {
		return DialogIndex{}, err
	}

	id := legacyDialogID
	if !shortExists && !workingExists {
		id, err = generateDialogID()
		if err != nil {
			return DialogIndex{}, err
		}
	}
	now := time.Now().UTC()
	dialog := DialogMetadata{ID: id, Title: "Диалог 1", CreatedAt: now, UpdatedAt: now}

	if shortExists {
		memory, loadErr := NewJSONShortTermStore(legacyShortPath).Load(ctx)
		if loadErr != nil {
			return DialogIndex{}, fmt.Errorf("не удалось мигрировать прежнюю краткосрочную память: %w", loadErr)
		}
		if saveErr := m.shortTermStore(id).Save(ctx, memory); saveErr != nil {
			return DialogIndex{}, fmt.Errorf("не удалось мигрировать прежнюю краткосрочную память диалога %q: %w", id, saveErr)
		}
	}
	if workingExists {
		memory, loadErr := NewJSONWorkingMemoryStore(legacyWorkingPath).Load(ctx)
		if loadErr != nil {
			return DialogIndex{}, fmt.Errorf("не удалось мигрировать прежнюю рабочую память: %w", loadErr)
		}
		if saveErr := m.workingStore(id).Save(ctx, memory); saveErr != nil {
			return DialogIndex{}, fmt.Errorf("не удалось мигрировать прежнюю рабочую память диалога %q: %w", id, saveErr)
		}
	}

	index := DialogIndex{ActiveDialogID: id, Dialogs: []DialogMetadata{dialog}}
	if err := m.dialogs.Save(ctx, index); err != nil {
		return DialogIndex{}, err
	}
	return index, nil
}

func (m *MemoryLayers) touchDialogLocked(ctx context.Context, index *DialogIndex, id string, updatedAt time.Time) error {
	for i := range index.Dialogs {
		if index.Dialogs[i].ID == id {
			if updatedAt.Before(index.Dialogs[i].CreatedAt) {
				updatedAt = index.Dialogs[i].CreatedAt
			}
			index.Dialogs[i].UpdatedAt = updatedAt.UTC()
			return m.dialogs.Save(ctx, *index)
		}
	}
	return fmt.Errorf("%w: %s", ErrDialogNotFound, id)
}

func (m *MemoryLayers) shortTermStore(id string) *JSONShortTermStore {
	path := filepath.Join(m.dir, dialogsDirName, id, shortTermFileName)
	return newJSONShortTermStore(path, fmt.Sprintf("краткосрочную память диалога %q", id))
}

func (m *MemoryLayers) workingStore(id string) *JSONWorkingMemoryStore {
	path := filepath.Join(m.dir, dialogsDirName, id, workingFileName)
	return newJSONWorkingMemoryStore(path, fmt.Sprintf("рабочую память диалога %q", id))
}

func generateDialogID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("не удалось сгенерировать ID диалога: %w", err)
	}
	return "dlg-" + hex.EncodeToString(random), nil
}

func validateDialogID(id string) error {
	if id == "" || len(id) > 64 || !strings.HasPrefix(id, "dlg-") {
		return fmt.Errorf("%w: %q", ErrInvalidDialogID, id)
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return fmt.Errorf("%w: %q", ErrInvalidDialogID, id)
	}
	return nil
}

func resolveDialog(index DialogIndex, id string) (DialogMetadata, error) {
	if id == "" {
		id = index.ActiveDialogID
	} else if err := validateDialogID(id); err != nil {
		return DialogMetadata{}, err
	}
	return dialogByID(index, id)
}

func dialogByID(index DialogIndex, id string) (DialogMetadata, error) {
	for _, dialog := range index.Dialogs {
		if dialog.ID == id {
			return dialog, nil
		}
	}
	return DialogMetadata{}, fmt.Errorf("%w: %s", ErrDialogNotFound, id)
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("не удалось проверить %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("ожидался обычный файл: %s", path)
	}
	return true, nil
}

type jsonFileStore[T any] struct {
	path     string
	label    string
	validate func(T) error
	clone    func(T) T
	mu       sync.Mutex
}

type jsonEnvelope[T any] struct {
	Version   int       `json:"version"`
	Data      T         `json:"data"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newJSONFileStore[T any](path, label string, validate func(T) error, clone func(T) T) *jsonFileStore[T] {
	return &jsonFileStore[T]{path: path, label: label, validate: validate, clone: clone}
}

func newStrictJSONFileStore[T any](path, label string, validate func(T) error, clone func(T) T) *jsonFileStore[T] {
	return newJSONFileStore(path, label, validate, clone)
}

func (s *jsonFileStore[T]) Load(ctx context.Context) (T, error) {
	var zero T
	if s == nil || strings.TrimSpace(s.path) == "" {
		return zero, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return zero, nil
		}
		return zero, fmt.Errorf("не удалось прочитать %s: %w", s.label, err)
	}
	if len(data) == 0 {
		return zero, fmt.Errorf("не удалось разобрать %s: файл пуст", s.label)
	}
	var file jsonEnvelope[T]
	if err := s.decode(data, &file); err != nil {
		return zero, fmt.Errorf("не удалось разобрать %s: %w", s.label, err)
	}
	if file.Version != 1 {
		return zero, fmt.Errorf("неподдерживаемая версия файла %s: %d", s.label, file.Version)
	}
	if err := s.validate(file.Data); err != nil {
		return zero, fmt.Errorf("%s содержит некорректные данные: %w", s.label, err)
	}
	return s.clone(file.Data), nil
}

func (s *jsonFileStore[T]) decode(data []byte, target *jsonEnvelope[T]) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("файл должен содержать ровно один JSON-объект")
	}
	return nil
}

func (s *jsonFileStore[T]) Save(ctx context.Context, value T) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validate(value); err != nil {
		return fmt.Errorf("%s содержит некорректные данные: %w", s.label, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("не удалось создать каталог для %s: %w", s.label, err)
	}
	file := jsonEnvelope[T]{Version: 1, Data: s.clone(value), UpdatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось собрать JSON для %s: %w", s.label, err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("не удалось создать временный файл для %s: %w", s.label, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("не удалось настроить временный файл для %s: %w", s.label, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("не удалось записать временный файл для %s: %w", s.label, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("не удалось синхронизировать временный файл для %s: %w", s.label, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("не удалось закрыть временный файл для %s: %w", s.label, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("не удалось атомарно сохранить %s: %w", s.label, err)
	}
	return nil
}

func validateDialogIndex(index DialogIndex) error {
	if len(index.Dialogs) == 0 {
		return errors.New("индекс не содержит диалогов")
	}
	if err := validateDialogID(index.ActiveDialogID); err != nil {
		return fmt.Errorf("active_dialog_id: %w", err)
	}
	seen := make(map[string]struct{}, len(index.Dialogs))
	activeFound := false
	for i, dialog := range index.Dialogs {
		if err := validateDialogID(dialog.ID); err != nil {
			return fmt.Errorf("диалог %d: %w", i, err)
		}
		if strings.TrimSpace(dialog.Title) == "" {
			return fmt.Errorf("диалог %q: название не может быть пустым", dialog.ID)
		}
		if dialog.CreatedAt.IsZero() || dialog.UpdatedAt.IsZero() || dialog.UpdatedAt.Before(dialog.CreatedAt) {
			return fmt.Errorf("диалог %q: некорректные временные метки", dialog.ID)
		}
		if _, exists := seen[dialog.ID]; exists {
			return fmt.Errorf("дублирующийся ID диалога %q", dialog.ID)
		}
		seen[dialog.ID] = struct{}{}
		activeFound = activeFound || dialog.ID == index.ActiveDialogID
	}
	if !activeFound {
		return fmt.Errorf("активный диалог %q отсутствует в индексе", index.ActiveDialogID)
	}
	return nil
}

func validateShortTermMemory(memory ShortTermMemory) error {
	for i, turn := range memory.Turns {
		if err := validateConversationTurn(turn); err != nil {
			return fmt.Errorf("ход %d: %w", i, err)
		}
	}
	return nil
}

func validateConversationTurn(turn ConversationTurn) error {
	if strings.TrimSpace(turn.User) == "" {
		return errors.New("сообщение user не может быть пустым")
	}
	if strings.TrimSpace(turn.Assistant) == "" {
		return errors.New("сообщение assistant не может быть пустым")
	}
	if turn.CreatedAt.IsZero() {
		return errors.New("время успешного хода не задано")
	}
	return nil
}

func validateWorkingMemory(memory WorkingMemory) error {
	nonEmpty := strings.TrimSpace(memory.Goal) != "" || len(memory.Notes) > 0 || len(memory.Results) > 0 || memory.Task != nil
	if memory.Status != "" && !validTaskStatus(memory.Status) {
		return fmt.Errorf("неподдерживаемый статус: %q", memory.Status)
	}
	if nonEmpty && memory.Status == "" {
		return errors.New("для непустой рабочей памяти должен быть задан статус")
	}
	if memory.Task != nil {
		if strings.TrimSpace(memory.Goal) == "" {
			return errors.New("состояние задачи требует сохранённой цели")
		}
		if err := validateTaskState(*memory.Task); err != nil {
			return fmt.Errorf("некорректное состояние задачи: %w", err)
		}
		if expected := taskStatusForState(*memory.Task); memory.Status != expected {
			return fmt.Errorf("status %q не соответствует состоянию задачи; ожидается %q", memory.Status, expected)
		}
	}
	for i, note := range memory.Notes {
		if strings.TrimSpace(note.Content) == "" || note.RecordedAt.IsZero() {
			return fmt.Errorf("некорректная заметка на позиции %d", i)
		}
	}
	for i, result := range memory.Results {
		if strings.TrimSpace(result.Content) == "" || result.RecordedAt.IsZero() {
			return fmt.Errorf("некорректный результат на позиции %d", i)
		}
	}
	return nil
}

func validTaskStatus(status TaskStatus) bool {
	switch status {
	case TaskStatusNotStarted, TaskStatusInProgress, TaskStatusBlocked, TaskStatusCompleted:
		return true
	default:
		return false
	}
}

func validateLongTermMemory(memory LongTermMemory) error {
	if memory.UserProfile != nil {
		if err := validateStoredUserProfile(*memory.UserProfile); err != nil {
			return fmt.Errorf("некорректный типизированный профиль: %w", err)
		}
	}
	seenProfileKeys := make(map[string]struct{}, len(memory.Profile))
	for i, entry := range memory.Profile {
		if strings.TrimSpace(entry.Key) == "" || strings.TrimSpace(entry.Value) == "" || entry.UpdatedAt.IsZero() {
			return fmt.Errorf("некорректная запись профиля на позиции %d", i)
		}
		if _, exists := seenProfileKeys[entry.Key]; exists {
			return fmt.Errorf("дублирующийся ключ профиля %q", entry.Key)
		}
		seenProfileKeys[entry.Key] = struct{}{}
	}
	for i, decision := range memory.Decisions {
		if strings.TrimSpace(decision.Statement) == "" || decision.RecordedAt.IsZero() {
			return fmt.Errorf("некорректное решение на позиции %d", i)
		}
	}
	for i, knowledge := range memory.Knowledge {
		if strings.TrimSpace(knowledge.Topic) == "" || strings.TrimSpace(knowledge.Content) == "" || knowledge.RecordedAt.IsZero() {
			return fmt.Errorf("некорректное знание на позиции %d", i)
		}
	}
	return nil
}

func cloneDialogIndex(index DialogIndex) DialogIndex {
	index.Dialogs = append([]DialogMetadata(nil), index.Dialogs...)
	return index
}

func cloneShortTermMemory(memory ShortTermMemory) ShortTermMemory {
	memory.Turns = append([]ConversationTurn(nil), memory.Turns...)
	return memory
}

func cloneWorkingMemory(memory WorkingMemory) WorkingMemory {
	memory.Notes = append([]WorkingNote(nil), memory.Notes...)
	memory.Results = append([]WorkingResult(nil), memory.Results...)
	if memory.Task != nil {
		state := *memory.Task
		memory.Task = &state
	}
	return memory
}

func requireTaskState(memory WorkingMemory) (TaskState, error) {
	if memory.Task == nil {
		return TaskState{}, &TaskStateError{
			Operation:          "task_operation",
			AllowedTransitions: []TaskStage{TaskStagePlanning},
			Reason:             ErrTaskNotStarted.Error() + "; сначала создайте задачу",
		}
	}
	return *memory.Task, nil
}

func taskStateFromWorking(memory WorkingMemory, err error) (TaskState, error) {
	if err != nil {
		return TaskState{}, err
	}
	return requireTaskState(memory)
}

func taskStatusForState(state TaskState) TaskStatus {
	if state.Paused {
		return TaskStatusBlocked
	}
	switch state.Stage {
	case TaskStagePlanning:
		return TaskStatusNotStarted
	case TaskStageExecution, TaskStageValidation:
		return TaskStatusInProgress
	case TaskStageDone:
		return TaskStatusCompleted
	default:
		return ""
	}
}

func cloneLongTermMemory(memory LongTermMemory) LongTermMemory {
	memory.Profile = append([]ProfileEntry(nil), memory.Profile...)
	memory.UserProfile = cloneUserProfile(memory.UserProfile)
	memory.Decisions = append([]Decision(nil), memory.Decisions...)
	memory.Knowledge = append([]Knowledge(nil), memory.Knowledge...)
	return memory
}
