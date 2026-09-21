package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTaskTransitionTable(t *testing.T) {
	want := map[TaskStage][]TaskStage{
		TaskStagePlanning:   {TaskStageExecution},
		TaskStageExecution:  {TaskStagePlanning, TaskStageValidation},
		TaskStageValidation: {TaskStageExecution, TaskStageDone},
		TaskStageDone:       {},
	}
	stages := []TaskStage{TaskStagePlanning, TaskStageExecution, TaskStageValidation, TaskStageDone}
	for _, from := range stages {
		state := taskStateAtStage(t, from, true)
		if got := state.AllowedTransitions(); len(got) != len(want[from]) || !reflect.DeepEqual(got, want[from][:len(got)]) {
			t.Fatalf("allowed transitions for %s=%v want %v", from, got, want[from])
		}
		for _, to := range stages {
			_, err := state.Transition(to, time.Now())
			if containsTaskStage(want[from], to) && err != nil {
				t.Errorf("allowed transition %s -> %s failed: %v", from, to, err)
			}
			if !containsTaskStage(want[from], to) && !errors.Is(err, ErrTaskConflict) {
				t.Errorf("forbidden transition %s -> %s error=%v", from, to, err)
			}
		}
	}
}

func TestTaskTransitionGuardsAndHappyPath(t *testing.T) {
	state, err := NewTaskState("Составить план", "Утвердить план", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Transition(TaskStageExecution, time.Now()); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("planning -> execution without approval error=%v", err)
	}
	if _, err := state.Transition(TaskStageValidation, time.Now()); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("planning -> validation error=%v", err)
	}
	state, err = state.ApprovePlan("1. Реализовать\n2. Проверить", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.Transition(TaskStageExecution, time.Now())
	if err != nil || state.Stage != TaskStageExecution {
		t.Fatalf("approved planning -> execution: state=%+v err=%v", state, err)
	}
	if _, err := state.Transition(TaskStageDone, time.Now()); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("execution -> done error=%v", err)
	}
	state, err = state.Transition(TaskStageValidation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Transition(TaskStageDone, time.Now()); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("validation -> done without validation error=%v", err)
	}
	state, err = state.RecordValidation(true, "go test: ok", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.Transition(TaskStageDone, time.Now())
	if err != nil || state.Stage != TaskStageDone || !state.ValidationPassed {
		t.Fatalf("happy path did not reach done: state=%+v err=%v", state, err)
	}
	for _, target := range []TaskStage{TaskStagePlanning, TaskStageExecution, TaskStageValidation, TaskStageDone} {
		if _, err := state.Transition(target, time.Now()); !errors.Is(err, ErrTaskConflict) {
			t.Fatalf("done -> %s error=%v", target, err)
		}
	}
}

func TestTaskReturnTransitionsResetStaleConfirmations(t *testing.T) {
	execution := taskStateAtStage(t, TaskStageExecution, false)
	planning, err := execution.Transition(TaskStagePlanning, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if planning.PlanApproved || planning.ValidationPassed {
		t.Fatalf("execution -> planning kept stale confirmations: %+v", planning)
	}

	validation := taskStateAtStage(t, TaskStageValidation, true)
	execution, err = validation.Transition(TaskStageExecution, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if execution.ValidationPassed || execution.ValidationDetails != "" {
		t.Fatalf("validation -> execution kept validation: %+v", execution)
	}
}

func TestTaskPauseResumeAtEveryNonTerminalStage(t *testing.T) {
	for _, stage := range []TaskStage{TaskStagePlanning, TaskStageExecution, TaskStageValidation} {
		t.Run(string(stage), func(t *testing.T) {
			state := taskStateAtStage(t, stage, stage == TaskStageValidation)
			before := state
			paused, err := state.Pause(time.Now().Add(time.Second))
			if err != nil || !paused.Paused {
				t.Fatalf("pause: state=%+v err=%v", paused, err)
			}
			if paused.Stage != before.Stage || paused.CurrentStep != before.CurrentStep || paused.ExpectedAction != before.ExpectedAction || paused.PlanApproved != before.PlanApproved || paused.ValidationPassed != before.ValidationPassed {
				t.Fatalf("pause lost position: before=%+v after=%+v", before, paused)
			}
			again, err := paused.Pause(time.Now().Add(2 * time.Second))
			if err != nil || again != paused {
				t.Fatalf("repeated pause is not idempotent: state=%+v err=%v", again, err)
			}
			if _, err := paused.UpdateProgress("Новый шаг", "Новое действие", time.Now()); !errors.Is(err, ErrTaskConflict) {
				t.Fatalf("progress during pause error=%v", err)
			}
			for _, target := range paused.AllowedTransitions() {
				if _, err := paused.Transition(target, time.Now()); !errors.Is(err, ErrTaskConflict) {
					t.Fatalf("transition during pause to %s error=%v", target, err)
				}
			}
			resumed, err := paused.Resume(time.Now().Add(3 * time.Second))
			if err != nil || resumed.Paused || resumed.Stage != before.Stage || resumed.CurrentStep != before.CurrentStep || resumed.ExpectedAction != before.ExpectedAction {
				t.Fatalf("resume lost position: state=%+v err=%v", resumed, err)
			}
		})
	}
	done := taskStateAtStage(t, TaskStageDone, true)
	if _, err := done.Pause(time.Now()); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("pause done error=%v", err)
	}
	if _, err := done.Resume(time.Now()); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("resume done error=%v", err)
	}
}

func TestTaskStatePersistsAcrossRestartAndDialogsAreIsolated(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, first := initializedLayers(t, dir)
	second, err := layers.CreateDialog(ctx, "Второй")
	if err != nil {
		t.Fatal(err)
	}
	firstState, err := layers.CreateTaskInDialog(ctx, first.ID, "Первая цель", "Шаг A", "Утвердить план")
	if err != nil {
		t.Fatal(err)
	}
	firstState, err = layers.ApproveTaskPlanInDialog(ctx, first.ID, "План A")
	if err != nil {
		t.Fatal(err)
	}
	firstState, err = layers.TransitionTaskInDialog(ctx, first.ID, TaskStageExecution)
	if err != nil {
		t.Fatal(err)
	}
	firstState, err = layers.UpdateTaskProgressInDialog(ctx, first.ID, "Реализовать A", "Запустить тесты A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layers.CreateTaskInDialog(ctx, second.ID, "Вторая цель", "Шаг B", "Утвердить B"); err != nil {
		t.Fatal(err)
	}

	restarted := NewJSONMemoryLayers(dir)
	firstMemory, err := restarted.LoadDialogWorking(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondMemory, err := restarted.LoadDialogWorking(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstMemory.Task == nil || *firstMemory.Task != firstState || firstMemory.Goal != "Первая цель" {
		t.Fatalf("first task not restored: %+v", firstMemory)
	}
	if secondMemory.Task == nil || secondMemory.Task.Stage != TaskStagePlanning || secondMemory.Goal != "Вторая цель" {
		t.Fatalf("second task mixed or missing: %+v", secondMemory)
	}
}

func TestTaskContextSurvivesEmptyShortTermHistory(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Восстановить задачу", "Подготовить план", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	if _, err := layers.ApproveTaskPlanInDialog(ctx, dialog.ID, "1. Сделать\n2. Проверить"); err != nil {
		t.Fatal(err)
	}
	if _, err := layers.TransitionTaskInDialog(ctx, dialog.ID, TaskStageExecution); err != nil {
		t.Fatal(err)
	}
	if _, err := layers.UpdateTaskProgressInDialog(ctx, dialog.ID, "Сделать второй шаг", "Запустить проверки"); err != nil {
		t.Fatal(err)
	}
	short, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if len(short.Turns) != 0 {
		t.Fatalf("precondition: short-term is not empty: %+v", short)
	}

	disabled := false
	var request chatRequest
	agent := scriptedAgent(t, NewJSONMemoryLayers(layers.dir), &disabled, []scriptedReply{{body: finalReply("Продолжаю")}}, &request)
	if _, err := agent.AskInDialog(ctx, dialog.ID, "Продолжай"); err != nil {
		t.Fatal(err)
	}
	joined := joinedMessageContent(request.Messages)
	for _, want := range []string{"[TASK_STATE]", "Восстановить задачу", "1. Сделать", "Сделать второй шаг", "Запустить проверки", "Этап: execution"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("task context misses %q:\n%s", want, joined)
		}
	}
}

func TestLegacyWorkingWithoutTaskStateAndUnknownStoredStage(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_, dialog := initializedLayers(t, dir)
	path := dialogFile(dir, dialog.ID, workingFileName)
	legacy := `{"version":1,"data":{"goal":"legacy","status":"in_progress"},"updated_at":"2026-09-18T00:00:00Z"}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	memory, err := NewJSONMemoryLayers(dir).LoadDialogWorking(ctx, dialog.ID)
	if err != nil || memory.Task != nil || memory.Goal != "legacy" {
		t.Fatalf("legacy working rejected: memory=%+v err=%v", memory, err)
	}

	corrupted := `{"version":1,"data":{"goal":"bad","status":"not_started","task_state":{"stage":"unknown","current_step":"x","expected_action":"y","paused":false,"plan_approved":false,"validation_passed":false,"updated_at":"2026-09-18T00:00:00Z"}},"updated_at":"2026-09-18T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJSONMemoryLayers(dir).LoadDialogWorking(ctx, dialog.ID); !errors.Is(err, ErrInvalidTaskStage) {
		t.Fatalf("unknown stage error=%v", err)
	}
}

func TestRejectedTaskOperationDoesNotChangeWorkingOrHistory(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Цель", "План", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	beforeWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	beforeShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if _, err := layers.TransitionTaskInDialog(ctx, dialog.ID, TaskStageValidation); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("forbidden transition error=%v", err)
	}
	afterWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	afterShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if !reflect.DeepEqual(beforeWorking, afterWorking) || !reflect.DeepEqual(beforeShort, afterShort) {
		t.Fatalf("rejected operation changed memory: before=%+v after=%+v", beforeWorking, afterWorking)
	}
}

func TestTaskToolsRejectSecondTransitionInOneUserTurn(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Цель", "План", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	if _, err := layers.ApproveTaskPlanInDialog(ctx, dialog.ID, "План"); err != nil {
		t.Fatal(err)
	}
	beforeWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	beforeShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusOK, toolReply(
			toolCallFor("execute", "task_transition", `{"stage":"execution"}`),
			toolCallFor("validate", "task_transition", `{"stage":"validation"}`),
		)), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)
	_, err := agent.AskInDialog(ctx, dialog.ID, "Выполни всё сразу")
	var stateErr *TaskStateError
	if !errors.As(err, &stateErr) || !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("error=%v", err)
	}
	afterWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	afterShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if !reflect.DeepEqual(beforeWorking, afterWorking) || !reflect.DeepEqual(beforeShort, afterShort) {
		t.Fatalf("second transition did not roll back the whole turn: before=%+v after=%+v", beforeWorking, afterWorking)
	}
	if requests != 1 || stateErr.RequestedStage != TaskStageValidation || !strings.Contains(stateErr.Reason, "только один переход") {
		t.Fatalf("unexpected conflict: requests=%d error=%+v", requests, stateErr)
	}
}

func TestTaskToolGuardConflictRollsBackWorkingAndHistory(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Цель", "План", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	beforeWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	beforeShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(
		toolCallFor("pending-note", "memory_add_working_note", `{"content":"Не сохранять"}`),
		toolCallFor("forbidden", "task_transition", `{"stage":"validation"}`),
	)}}, nil)

	_, err := agent.AskInDialog(ctx, dialog.ID, "Перейди к проверке")
	var stateErr *TaskStateError
	if !errors.As(err, &stateErr) || !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("error=%v", err)
	}
	afterWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	afterShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if !reflect.DeepEqual(beforeWorking, afterWorking) || !reflect.DeepEqual(beforeShort, afterShort) {
		t.Fatalf("guard conflict changed memory: before=%+v after=%+v", beforeWorking, afterWorking)
	}
}

func TestTaskToolOperationDuringPauseRollsBackTurn(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Цель", "План", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	if _, err := layers.PauseTaskInDialog(ctx, dialog.ID); err != nil {
		t.Fatal(err)
	}
	beforeWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	beforeShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(
		toolCallFor("pending-note", "memory_add_working_note", `{"content":"Не сохранять"}`),
		toolCallFor("progress", "task_update_progress", `{"current_step":"Код","expected_action":"Тесты"}`),
	)}}, nil)

	_, err := agent.AskInDialog(ctx, dialog.ID, "Продолжай")
	if !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("error=%v", err)
	}
	afterWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	afterShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if !reflect.DeepEqual(beforeWorking, afterWorking) || !reflect.DeepEqual(beforeShort, afterShort) {
		t.Fatalf("paused task operation changed memory: before=%+v after=%+v", beforeWorking, afterWorking)
	}
}

func taskStateAtStage(t *testing.T, stage TaskStage, validationPassed bool) TaskState {
	t.Helper()
	state, err := NewTaskState("План", "Утвердить", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stage == TaskStagePlanning {
		if validationPassed {
			state, err = state.ApprovePlan("План", time.Now())
		}
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	state, err = state.ApprovePlan("План", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.Transition(TaskStageExecution, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stage == TaskStageExecution {
		return state
	}
	state, err = state.Transition(TaskStageValidation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if validationPassed || stage == TaskStageDone {
		state, err = state.RecordValidation(true, "Проверки успешны", time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if stage == TaskStageDone {
		state, err = state.Transition(TaskStageDone, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	return state
}
