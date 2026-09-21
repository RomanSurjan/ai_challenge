package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestTaskHTTPConflictContainsMachineStateAndDoesNotWriteHistory(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	handler := taskAPIHandler(agent)

	created := callTaskAPI(t, handler, http.MethodPost, "/api/task?dialog_id="+dialog.ID, `{"goal":"Добавить FSM","current_step":"Составить план","expected_action":"Утвердить план"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	beforeWorking, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	beforeShort, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)

	conflict := callTaskAPI(t, handler, http.MethodPost, "/api/task/transition?dialog_id="+dialog.ID, `{"stage":"validation"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	var body taskAPIResponse
	decodeResponse(t, conflict, &body)
	if body.CurrentStage != TaskStagePlanning || body.RequestedStage != TaskStageValidation || !reflect.DeepEqual(body.AllowedTransitions, []TaskStage{TaskStageExecution}) || body.ExpectedAction != "Утвердить план" || !strings.Contains(body.Reason, "таблице переходов") {
		t.Fatalf("incomplete conflict body: %+v", body)
	}
	afterWorking, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	afterShort, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if !reflect.DeepEqual(beforeWorking, afterWorking) || !reflect.DeepEqual(beforeShort, afterShort) {
		t.Fatalf("conflict changed memory/history: before=%+v after=%+v", beforeWorking, afterWorking)
	}
}

func TestTaskHTTPHappyPathPauseResumeAndInputErrors(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	handler := taskAPIHandler(agent)
	base := "?dialog_id=" + dialog.ID

	steps := []struct {
		path string
		body string
		code int
	}{
		{"/api/task" + base, `{"goal":"FSM"}`, http.StatusCreated},
		{"/api/task/pause" + base, `{"unexpected":true}`, http.StatusBadRequest},
		{"/api/task/approve-plan" + base, `{"plan":"1. Код 2. Тесты"}`, http.StatusOK},
		{"/api/task/transition" + base, `{"stage":"execution"}`, http.StatusOK},
		{"/api/task/pause" + base, ``, http.StatusOK},
		{"/api/task/progress" + base, `{"current_step":"Код","expected_action":"Тесты"}`, http.StatusConflict},
		{"/api/task/resume" + base, ``, http.StatusOK},
		{"/api/task/progress" + base, `{"current_step":"Код","expected_action":"Тесты"}`, http.StatusOK},
		{"/api/task/transition" + base, `{"stage":"validation"}`, http.StatusOK},
		{"/api/task/transition" + base, `{"stage":"done"}`, http.StatusConflict},
		{"/api/task/validation" + base, `{"passed":true,"details":"go test: ok"}`, http.StatusOK},
		{"/api/task/transition" + base, `{"stage":"done"}`, http.StatusOK},
		{"/api/task/pause" + base, ``, http.StatusConflict},
	}
	for _, step := range steps {
		recorder := callTaskAPI(t, handler, http.MethodPost, step.path, step.body)
		if recorder.Code != step.code {
			t.Fatalf("%s status=%d want %d body=%s", step.path, recorder.Code, step.code, recorder.Body.String())
		}
	}

	unknown := callTaskAPI(t, handler, http.MethodPost, "/api/task/transition"+base, `{"stage":"almost_done"}`)
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "неизвестный этап") {
		t.Fatalf("unknown stage status=%d body=%s", unknown.Code, unknown.Body.String())
	}
	malformed := callTaskAPI(t, handler, http.MethodPost, "/api/task/progress"+base, `{"current_step":`)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d body=%s", malformed.Code, malformed.Body.String())
	}

	read := callTaskAPI(t, handler, http.MethodGet, "/api/task"+base, "")
	if read.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", read.Code, read.Body.String())
	}
	var body taskAPIResponse
	decodeResponse(t, read, &body)
	if body.TaskState == nil || body.TaskState.Stage != TaskStageDone || !body.TaskState.ValidationPassed {
		t.Fatalf("unexpected final task: %+v", body)
	}
}

func TestChatAPIIncludesCurrentTaskState(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	if _, err := layers.CreateTaskInDialog(context.Background(), dialog.ID, "Цель", "План", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	disabled := false
	agent := scriptedAgent(t, layers, &disabled, []scriptedReply{{body: finalReply("Ответ")}}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"Статус?","dialog_id":"`+dialog.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response chatAPIResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TaskState == nil || response.TaskState.Stage != TaskStagePlanning {
		t.Fatalf("task state missing from chat response: %+v", response)
	}
}

func TestChatAPITaskGuardConflictIsTypedAndRollsBack(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Цель", "План", "Утвердить план"); err != nil {
		t.Fatal(err)
	}
	beforeWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	beforeShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(
		toolCallFor("pending", "memory_add_working_note", `{"content":"Не сохранять"}`),
		toolCallFor("forbidden", "task_transition", `{"stage":"validation"}`),
	)}}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"Перейди к validation","dialog_id":"`+dialog.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	chatAPIHandler(agent).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body chatAPIResponse
	decodeResponse(t, recorder, &body)
	if body.CurrentStage != TaskStagePlanning || body.RequestedStage != TaskStageValidation ||
		!reflect.DeepEqual(body.AllowedTransitions, []TaskStage{TaskStageExecution}) ||
		body.ExpectedAction != "Утвердить план" || body.Reason == "" ||
		body.TaskState == nil || !reflect.DeepEqual(*body.TaskState, *beforeWorking.Task) ||
		body.ErrorCategory != "fsm_conflict" || !body.FSMGuard {
		t.Fatalf("incomplete conflict response: %+v", body)
	}
	for _, leaked := range []string{layers.dir, `{"stage"`, "secret-value", "raw"} {
		if strings.Contains(recorder.Body.String(), leaked) {
			t.Fatalf("response leaked %q: %s", leaked, recorder.Body.String())
		}
	}
	afterWorking, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	afterShort, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if !reflect.DeepEqual(beforeWorking, afterWorking) || !reflect.DeepEqual(beforeShort, afterShort) {
		t.Fatalf("chat conflict changed memory: before=%+v after=%+v", beforeWorking, afterWorking)
	}
}

func TestLegacyWorkingStatusCannotBypassTaskMachine(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	if _, err := agent.CreateTaskInDialog(context.Background(), dialog.ID, "Цель", "План", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/memory/working/status?dialog_id="+dialog.ID, strings.NewReader(`{"value":"completed"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	memoryAPIHandler(agent).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("legacy completed status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state, err := agent.TaskStateInDialog(context.Background(), dialog.ID)
	if err != nil || state == nil || state.Stage != TaskStagePlanning {
		t.Fatalf("legacy endpoint changed task: state=%+v err=%v", state, err)
	}
}

func callTaskAPI(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
