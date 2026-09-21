package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type TaskStage string

const (
	TaskStagePlanning   TaskStage = "planning"
	TaskStageExecution  TaskStage = "execution"
	TaskStageValidation TaskStage = "validation"
	TaskStageDone       TaskStage = "done"
)

var taskTransitions = map[TaskStage][]TaskStage{
	TaskStagePlanning:   {TaskStageExecution},
	TaskStageExecution:  {TaskStagePlanning, TaskStageValidation},
	TaskStageValidation: {TaskStageExecution, TaskStageDone},
	TaskStageDone:       {},
}

var (
	ErrInvalidTaskStage = errors.New("неизвестный этап задачи")
	ErrTaskConflict     = errors.New("операция противоречит состоянию задачи")
	ErrTaskNotStarted   = errors.New("задача не создана")
)

type TaskState struct {
	Stage             TaskStage `json:"stage"`
	CurrentStep       string    `json:"current_step"`
	ExpectedAction    string    `json:"expected_action"`
	Paused            bool      `json:"paused"`
	Plan              string    `json:"plan,omitempty"`
	PlanApproved      bool      `json:"plan_approved"`
	ValidationPassed  bool      `json:"validation_passed"`
	ValidationDetails string    `json:"validation_details,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TaskStateError is returned when an otherwise well-formed task operation is
// rejected by the state machine. It contains everything an API client or model
// needs to explain the refusal without guessing from conversation text.
type TaskStateError struct {
	Operation          string      `json:"operation"`
	CurrentStage       TaskStage   `json:"current_stage,omitempty"`
	RequestedStage     TaskStage   `json:"requested_stage,omitempty"`
	AllowedTransitions []TaskStage `json:"allowed_transitions"`
	ExpectedAction     string      `json:"expected_action,omitempty"`
	Reason             string      `json:"reason"`
}

func (e *TaskStateError) Error() string {
	if e == nil {
		return ErrTaskConflict.Error()
	}
	if e.RequestedStage != "" {
		return fmt.Sprintf("%s: этап %q, запрошен %q: %s", ErrTaskConflict, e.CurrentStage, e.RequestedStage, e.Reason)
	}
	return fmt.Sprintf("%s: этап %q: %s", ErrTaskConflict, e.CurrentStage, e.Reason)
}

func (e *TaskStateError) Unwrap() error { return ErrTaskConflict }

func NewTaskState(currentStep, expectedAction string, now time.Time) (TaskState, error) {
	currentStep = strings.TrimSpace(currentStep)
	expectedAction = strings.TrimSpace(expectedAction)
	if currentStep == "" {
		currentStep = "Подготовить план"
	}
	if expectedAction == "" {
		expectedAction = "Пользователь должен утвердить план"
	}
	state := TaskState{
		Stage:          TaskStagePlanning,
		CurrentStep:    currentStep,
		ExpectedAction: expectedAction,
		UpdatedAt:      normalizedTaskTime(now),
	}
	return state, validateTaskState(state)
}

func ParseTaskStage(value string) (TaskStage, error) {
	stage := TaskStage(strings.TrimSpace(value))
	if !validTaskStage(stage) {
		return "", fmt.Errorf("%w: %q", ErrInvalidTaskStage, value)
	}
	return stage, nil
}

func (s TaskState) AllowedTransitions() []TaskStage {
	return append([]TaskStage{}, taskTransitions[s.Stage]...)
}

func (s TaskState) Transition(to TaskStage, now time.Time) (TaskState, error) {
	if err := validateTaskState(s); err != nil {
		return TaskState{}, err
	}
	if !validTaskStage(to) {
		return TaskState{}, fmt.Errorf("%w: %q", ErrInvalidTaskStage, to)
	}
	if s.Paused {
		return TaskState{}, s.conflict("transition", to, "задача приостановлена; сначала выполните resume")
	}
	if !containsTaskStage(s.AllowedTransitions(), to) {
		reason := "переход отсутствует в таблице переходов"
		if s.Stage == TaskStageDone {
			reason = "done — терминальное состояние"
		}
		return TaskState{}, s.conflict("transition", to, reason)
	}
	if s.Stage == TaskStagePlanning && to == TaskStageExecution && !s.PlanApproved {
		return TaskState{}, s.conflict("transition", to, "план ещё не утверждён")
	}
	if s.Stage == TaskStageValidation && to == TaskStageDone && !s.ValidationPassed {
		return TaskState{}, s.conflict("transition", to, "успешная валидация ещё не зафиксирована")
	}

	next := s
	next.Stage = to
	next.UpdatedAt = normalizedTaskTime(now)
	switch {
	case s.Stage == TaskStageExecution && to == TaskStagePlanning:
		next.PlanApproved = false
		next.ValidationPassed = false
		next.ValidationDetails = ""
		next.CurrentStep = "Скорректировать план"
		next.ExpectedAction = "Пользователь должен повторно утвердить план"
	case s.Stage == TaskStageValidation && to == TaskStageExecution:
		next.ValidationPassed = false
		next.ValidationDetails = ""
		next.CurrentStep = "Исправить результат после валидации"
		next.ExpectedAction = "Выполнить исправления и снова перейти к валидации"
	case to == TaskStageExecution:
		next.CurrentStep = "Выполнить утверждённый план"
		next.ExpectedAction = "Выполнить текущий шаг, затем перейти к валидации"
	case to == TaskStageValidation:
		next.ValidationPassed = false
		next.ValidationDetails = ""
		next.CurrentStep = "Проверить результат"
		next.ExpectedAction = "Зафиксировать результат валидации"
	case to == TaskStageDone:
		next.CurrentStep = "Задача завершена"
		next.ExpectedAction = "Создать новую задачу для продолжения работы"
	}
	return next, validateTaskState(next)
}

func (s TaskState) UpdateProgress(currentStep, expectedAction string, now time.Time) (TaskState, error) {
	if err := validateTaskState(s); err != nil {
		return TaskState{}, err
	}
	if s.Paused {
		return TaskState{}, s.conflict("update_progress", "", "во время паузы прогресс изменять нельзя")
	}
	if s.Stage == TaskStageDone {
		return TaskState{}, s.conflict("update_progress", "", "done — терминальное состояние")
	}
	currentStep = strings.TrimSpace(currentStep)
	expectedAction = strings.TrimSpace(expectedAction)
	if currentStep == "" || expectedAction == "" {
		return TaskState{}, errors.New("current_step и expected_action не могут быть пустыми")
	}
	next := s
	next.CurrentStep = currentStep
	next.ExpectedAction = expectedAction
	next.UpdatedAt = normalizedTaskTime(now)
	return next, validateTaskState(next)
}

func (s TaskState) ApprovePlan(plan string, now time.Time) (TaskState, error) {
	if err := validateTaskState(s); err != nil {
		return TaskState{}, err
	}
	if s.Paused {
		return TaskState{}, s.conflict("approve_plan", "", "во время паузы план утверждать нельзя")
	}
	if s.Stage != TaskStagePlanning {
		return TaskState{}, s.conflict("approve_plan", "", "план можно утвердить только на этапе planning")
	}
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return TaskState{}, errors.New("утверждаемый план не может быть пустым")
	}
	next := s
	next.Plan = plan
	next.PlanApproved = true
	next.ExpectedAction = "Перейти к выполнению утверждённого плана"
	next.UpdatedAt = normalizedTaskTime(now)
	return next, validateTaskState(next)
}

func (s TaskState) RecordValidation(passed bool, details string, now time.Time) (TaskState, error) {
	if err := validateTaskState(s); err != nil {
		return TaskState{}, err
	}
	if s.Paused {
		return TaskState{}, s.conflict("record_validation", "", "во время паузы результат валидации менять нельзя")
	}
	if s.Stage != TaskStageValidation {
		return TaskState{}, s.conflict("record_validation", "", "результат можно фиксировать только на этапе validation")
	}
	details = strings.TrimSpace(details)
	if details == "" {
		return TaskState{}, errors.New("описание результата валидации не может быть пустым")
	}
	next := s
	next.ValidationPassed = passed
	next.ValidationDetails = details
	if passed {
		next.ExpectedAction = "Перейти в done"
	} else {
		next.ExpectedAction = "Вернуться в execution и исправить ошибки"
	}
	next.UpdatedAt = normalizedTaskTime(now)
	return next, validateTaskState(next)
}

func (s TaskState) Pause(now time.Time) (TaskState, error) {
	if err := validateTaskState(s); err != nil {
		return TaskState{}, err
	}
	if s.Stage == TaskStageDone {
		return TaskState{}, s.conflict("pause", "", "завершённую задачу нельзя поставить на паузу")
	}
	if s.Paused {
		return s, nil
	}
	next := s
	next.Paused = true
	next.UpdatedAt = normalizedTaskTime(now)
	return next, validateTaskState(next)
}

func (s TaskState) Resume(now time.Time) (TaskState, error) {
	if err := validateTaskState(s); err != nil {
		return TaskState{}, err
	}
	if s.Stage == TaskStageDone {
		return TaskState{}, s.conflict("resume", "", "завершённую задачу нельзя возобновить")
	}
	if !s.Paused {
		return TaskState{}, s.conflict("resume", "", "задача не находится на паузе")
	}
	next := s
	next.Paused = false
	next.UpdatedAt = normalizedTaskTime(now)
	return next, validateTaskState(next)
}

func (s TaskState) conflict(operation string, requested TaskStage, reason string) *TaskStateError {
	return &TaskStateError{
		Operation:          operation,
		CurrentStage:       s.Stage,
		RequestedStage:     requested,
		AllowedTransitions: s.AllowedTransitions(),
		ExpectedAction:     s.ExpectedAction,
		Reason:             reason,
	}
}

func validateTaskState(state TaskState) error {
	if !validTaskStage(state.Stage) {
		return fmt.Errorf("%w: %q", ErrInvalidTaskStage, state.Stage)
	}
	if strings.TrimSpace(state.CurrentStep) == "" || strings.TrimSpace(state.ExpectedAction) == "" {
		return errors.New("current_step и expected_action состояния задачи не могут быть пустыми")
	}
	if state.UpdatedAt.IsZero() {
		return errors.New("updated_at состояния задачи не задан")
	}
	if state.PlanApproved && strings.TrimSpace(state.Plan) == "" {
		return errors.New("утверждённый план не может быть пустым")
	}
	if state.Stage != TaskStagePlanning && !state.PlanApproved {
		return errors.New("этап после planning требует утверждённого плана")
	}
	if state.ValidationPassed && state.Stage != TaskStageValidation && state.Stage != TaskStageDone {
		return errors.New("успешная валидация допустима только на этапах validation и done")
	}
	if state.Stage == TaskStageDone {
		if state.Paused {
			return errors.New("done нельзя поставить на паузу")
		}
		if !state.ValidationPassed {
			return errors.New("done требует успешной валидации")
		}
	}
	return nil
}

func validTaskStage(stage TaskStage) bool {
	_, ok := taskTransitions[stage]
	return ok
}

func containsTaskStage(stages []TaskStage, stage TaskStage) bool {
	for _, candidate := range stages {
		if candidate == stage {
			return true
		}
	}
	return false
}

func normalizedTaskTime(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC()
}
