package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestTaskToolSchemasHaveExactSerializedContract(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	payload, err := json.Marshal(agent.memoryTools())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"required":null`) {
		t.Fatalf("serialized tools contain required:null")
	}

	want := map[string]struct {
		required   []string
		properties map[string]string
	}{
		"task_create":            {[]string{"goal", "current_step", "expected_action"}, map[string]string{"goal": "string", "current_step": "string", "expected_action": "string"}},
		"task_update_progress":   {[]string{"current_step", "expected_action"}, map[string]string{"current_step": "string", "expected_action": "string"}},
		"task_approve_plan":      {[]string{"plan"}, map[string]string{"plan": "string"}},
		"task_transition":        {[]string{"stage"}, map[string]string{"stage": "string"}},
		"task_record_validation": {[]string{"passed", "details"}, map[string]string{"passed": "boolean", "details": "string"}},
		"task_pause":             {nil, map[string]string{}},
		"task_resume":            {nil, map[string]string{}},
	}

	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name       string `json:"name"`
			Parameters struct {
				Type                 string                     `json:"type"`
				Properties           map[string]json.RawMessage `json:"properties"`
				Required             []string                   `json:"required"`
				AdditionalProperties bool                       `json:"additionalProperties"`
			} `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(payload, &tools); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, tool := range tools {
		expected, ok := want[tool.Function.Name]
		if !ok {
			continue
		}
		seen[tool.Function.Name] = true
		parameters := tool.Function.Parameters
		if tool.Type != "function" || parameters.Type != "object" || parameters.AdditionalProperties {
			t.Fatalf("tool %s has invalid envelope: %+v", tool.Function.Name, tool)
		}
		if !reflect.DeepEqual(parameters.Required, expected.required) {
			t.Fatalf("tool %s required=%v want=%v", tool.Function.Name, parameters.Required, expected.required)
		}
		if len(parameters.Properties) != len(expected.properties) {
			t.Fatalf("tool %s properties=%v want=%v", tool.Function.Name, parameters.Properties, expected.properties)
		}
		for field, wantType := range expected.properties {
			var property map[string]any
			if err := json.Unmarshal(parameters.Properties[field], &property); err != nil {
				t.Fatal(err)
			}
			if property["type"] != wantType {
				t.Fatalf("tool %s field %s type=%v want=%s", tool.Function.Name, field, property["type"], wantType)
			}
			if wantType == "string" && field != "stage" {
				if property["minLength"] != float64(1) || property["maxLength"] == nil {
					t.Fatalf("tool %s field %s misses string bounds: %v", tool.Function.Name, field, property)
				}
			}
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("task tool set changed: seen=%v", seen)
	}
}

func TestTaskToolPolicyDelegatesGuardsToBackend(t *testing.T) {
	for _, required := range []string{
		"ровно одним соответствующим task-инструментом",
		"только backend FSM проверяет guards",
		"не добавляй вторую task-операцию",
		"не переходи на следующий этап автоматически",
	} {
		if !strings.Contains(memoryPolicyInstruction, required) {
			t.Fatalf("task tool policy misses %q", required)
		}
	}
}

func TestExplicitTaskCommandsUseNamedToolChoice(t *testing.T) {
	tests := map[string]string{
		"Создай задачу «Проверка».":              "task_create",
		"Утверждаю план: проверить и запустить.": "task_approve_plan",
		"Перейди в execution.":                   "task_transition",
		"Обнови прогресс: текущий шаг — тест, ожидаемое действие — отчёт.": "task_update_progress",
		"Поставь задачу на паузу.":                     "task_pause",
		"Сними задачу с паузы.":                        "task_resume",
		"Зафиксируй успешную валидацию: тесты прошли.": "task_record_validation",
		"Create a task for the HTTP client.":           "task_create",
		"Transition to validation.":                    "task_transition",
	}
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	for prompt, want := range tests {
		request := agent.buildRequest(prompt, ContextMemory{})
		if got := namedToolChoiceName(request.ToolChoice); got != want {
			t.Errorf("prompt=%q tool_choice=%q want=%q", prompt, got, want)
		}
	}
	for _, prompt := range []string{"Покажи состояние задачи.", "Как поставить задачу на паузу?", "Обычный вопрос"} {
		request := agent.buildRequest(prompt, ContextMemory{})
		if request.ToolChoice != "auto" {
			t.Errorf("non-command prompt=%q tool_choice=%v want auto", prompt, request.ToolChoice)
		}
	}
}

func TestNamedTaskToolChoiceBecomesNoneAfterOneCall(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	requests := make([]chatRequest, 0, 2)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var decoded chatRequest
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, decoded)
		if len(requests) == 1 {
			return response(http.StatusOK, toolReply(toolCallFor("create", "task_create", `{"goal":"Цель","current_step":"План","expected_action":"Утвердить"}`))), nil
		}
		return response(http.StatusOK, finalReply("created")), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Memory: layers}, client)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "Создай задачу «Цель»."); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d want=2", len(requests))
	}
	first, ok := requests[0].ToolChoice.(map[string]any)
	if !ok || first["type"] != "function" {
		t.Fatalf("first tool_choice=%#v", requests[0].ToolChoice)
	}
	function, ok := first["function"].(map[string]any)
	if !ok || function["name"] != "task_create" {
		t.Fatalf("first named tool_choice=%#v", requests[0].ToolChoice)
	}
	if requests[1].ToolChoice != "none" {
		t.Fatalf("second tool_choice=%#v want none", requests[1].ToolChoice)
	}
}

func TestRealProviderLikeEmptyArgumentsForNoArgumentTaskTools(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Цель", "Точный шаг", "Точное действие"); err != nil {
		t.Fatal(err)
	}
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(toolCallFor("pause", "task_pause", ""))},
		{body: finalReply("paused")},
		{body: toolReply(toolCallFor("resume", "task_resume", ""))},
		{body: finalReply("resumed")},
	}, nil)
	if _, err := agent.AskInDialog(ctx, dialog.ID, "Пауза"); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.AskInDialog(ctx, dialog.ID, "Продолжить"); err != nil {
		t.Fatal(err)
	}
	working, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	short, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if working.Task == nil || working.Task.Paused || working.Task.CurrentStep != "Точный шаг" || working.Task.ExpectedAction != "Точное действие" || len(short.Turns) != 2 {
		t.Fatalf("provider-like no-argument calls lost state: working=%+v turns=%d", working, len(short.Turns))
	}
}

func TestEveryTaskToolSucceedsThroughModelProtocol(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	replies := []scriptedReply{
		{body: toolReply(toolCallFor("create", "task_create", `{"goal":"Цель","current_step":"План","expected_action":"Утвердить"}`))}, {body: finalReply("created")},
		{body: toolReply(toolCallFor("approve", "task_approve_plan", `{"plan":"1. Код 2. Тесты"}`))}, {body: finalReply("approved")},
		{body: toolReply(toolCallFor("execute", "task_transition", `{"stage":"execution"}`))}, {body: finalReply("executing")},
		{body: toolReply(toolCallFor("progress", "task_update_progress", `{"current_step":"Код","expected_action":"Тесты"}`))}, {body: finalReply("progress")},
		{body: toolReply(toolCallFor("pause", "task_pause", `{}`))}, {body: finalReply("paused")},
		{body: toolReply(toolCallFor("resume", "task_resume", `{}`))}, {body: finalReply("resumed")},
		{body: toolReply(toolCallFor("validate-stage", "task_transition", `{"stage":"validation"}`))}, {body: finalReply("validating")},
		{body: toolReply(toolCallFor("validation", "task_record_validation", `{"passed":true,"details":"go test: ok"}`))}, {body: finalReply("validated")},
	}
	agent := scriptedAgent(t, layers, nil, replies, nil)
	for index := 0; index < 8; index++ {
		if _, err := agent.AskInDialog(context.Background(), dialog.ID, "step"); err != nil {
			t.Fatalf("step %d: %v", index+1, err)
		}
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if working.Task == nil || working.Task.Stage != TaskStageValidation || !working.Task.ValidationPassed || working.Task.Paused || len(short.Turns) != 8 {
		t.Fatalf("unexpected final state: working=%+v turns=%d", working, len(short.Turns))
	}
}

func TestTaskToolValidationCategories(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	tests := []struct {
		name, arguments string
		category        MemoryToolErrorCategory
		field, jsonType string
	}{
		{"malformed", `{"stage":`, MemoryToolErrorMalformedJSON, "", ""},
		{"missing", `{}`, MemoryToolErrorMissingField, "stage", ""},
		{"unknown field", `{"stage":"execution","raw":"hidden"}`, MemoryToolErrorUnknownField, "raw", ""},
		{"unknown stage", `{"stage":"almost_done"}`, MemoryToolErrorInvalidValue, "stage", ""},
		{"wrong type", `{"stage":true}`, MemoryToolErrorInvalidType, "stage", "bool"},
		{"null type", `{"stage":null}`, MemoryToolErrorInvalidType, "stage", "null"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := agent.validateMemoryToolCall(toolCallFor("call", "task_transition", test.arguments))
			var toolErr *MemoryToolCallError
			if !errors.As(err, &toolErr) || !errors.Is(err, ErrMemoryToolCall) {
				t.Fatalf("error=%v", err)
			}
			if toolErr.Tool != "task_transition" || toolErr.Category != test.category || toolErr.Field != test.field || toolErr.JSONType != test.jsonType {
				t.Fatalf("diagnostic=%+v", toolErr)
			}
		})
	}
}

func TestTaskToolsAllowBenignSecurityTerminology(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	tests := []struct {
		name      string
		tool      string
		arguments string
	}{
		{
			name:      "task create goal",
			tool:      "task_create",
			arguments: `{"goal":"Реализовать авторизацию по email и password","current_step":"Проверить password reset","expected_action":"Проверить передачу API key"}`,
		},
		{
			name:      "task update progress",
			tool:      "task_update_progress",
			arguments: `{"current_step":"Настроить хранение access token","expected_action":"Проверить маскирование номера карты"}`,
		},
		{
			name:      "task approve plan",
			tool:      "task_approve_plan",
			arguments: `{"plan":"Реализовать ротацию API key и проверить заголовок Authorization"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := agent.validateMemoryToolCall(toolCallFor("call", test.tool, test.arguments)); err != nil {
				t.Fatalf("safe terminology rejected: %v", err)
			}
		})
	}
}

func TestTaskToolsRejectRealSecretsInEveryStringField(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	tests := []struct {
		name, tool, field, arguments string
		rule                         sensitiveRule
	}{
		{"create goal", "task_create", "goal", `{"goal":"password=hunter2","current_step":"План","expected_action":"Утвердить"}`, sensitiveRuleCredentialAssignment},
		{"create current step", "task_create", "current_step", `{"goal":"Цель","current_step":"api_key=secret-value","expected_action":"Утвердить"}`, sensitiveRuleCredentialAssignment},
		{"create expected action", "task_create", "expected_action", `{"goal":"Цель","current_step":"План","expected_action":"access_token=actual-secret-token"}`, sensitiveRuleCredentialAssignment},
		{"progress current step", "task_update_progress", "current_step", `{"current_step":"Authorization: Bearer actual-secret-token","expected_action":"Проверить"}`, sensitiveRuleBearerToken},
		{"progress expected action", "task_update_progress", "expected_action", `{"current_step":"Проверить","expected_action":"4111 1111 1111 1111"}`, sensitiveRulePaymentCard},
		{"approve plan", "task_approve_plan", "plan", `{"plan":"-----BEGIN PRIVATE KEY-----"}`, sensitiveRulePrivateKey},
		{"validation details", "task_record_validation", "details", `{"passed":false,"details":"sk-0123456789abcdefghijklmnop"}`, sensitiveRuleKeyLikeToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := agent.validateMemoryToolCall(toolCallFor("call", test.tool, test.arguments))
			var toolErr *MemoryToolCallError
			if !errors.As(err, &toolErr) || toolErr.Tool != test.tool || toolErr.Field != test.field ||
				toolErr.Category != MemoryToolErrorSensitive || toolErr.Rule != string(test.rule) {
				t.Fatalf("diagnostic=%+v err=%v", toolErr, err)
			}
			for _, forbidden := range []string{"hunter2", "secret-value", "actual-secret-token", "4111", "PRIVATE KEY", "sk-0123", test.arguments} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error leaked secret material %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestTaskCreateWithSecurityTerminologyCommitsThroughModelProtocol(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(toolCallFor("create", "task_create", `{"goal":"Реализовать авторизацию по email и password","current_step":"Составить план интеграции","expected_action":"Утвердить план"}`))},
		{body: finalReply("Задача создана")},
	}, nil)
	result, err := agent.AskInDialog(context.Background(), dialog.ID, "Создай задачу")
	if err != nil {
		t.Fatal(err)
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if working.Task == nil || working.Goal != "Реализовать авторизацию по email и password" ||
		working.Task.Stage != TaskStagePlanning || working.Task.PlanApproved || len(short.Turns) != 1 ||
		len(result.MemoryUpdates) != 1 || result.MemoryUpdates[0].Category != "task_create" {
		t.Fatalf("task_create was not committed correctly: result=%+v working=%+v short=%+v", result, working, short)
	}
}

func TestTaskProgressAndApprovalWithSecurityTerminologyCommitThroughModelProtocol(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "Авторизация", "Составить план", "Утвердить"); err != nil {
		t.Fatal(err)
	}
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(toolCallFor("progress", "task_update_progress", `{"current_step":"Проверить password reset","expected_action":"Настроить хранение access token"}`))},
		{body: finalReply("Прогресс обновлён")},
		{body: toolReply(toolCallFor("approve", "task_approve_plan", `{"plan":"Реализовать ротацию API key и проверить заголовок Authorization"}`))},
		{body: finalReply("План утверждён")},
	}, nil)
	if _, err := agent.AskInDialog(ctx, dialog.ID, "Обнови прогресс"); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.AskInDialog(ctx, dialog.ID, "Утверждаю план"); err != nil {
		t.Fatal(err)
	}
	working, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	short, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if working.Task == nil || working.Task.CurrentStep != "Проверить password reset" ||
		working.Task.Plan != "Реализовать ротацию API key и проверить заголовок Authorization" ||
		!working.Task.PlanApproved || len(short.Turns) != 2 {
		t.Fatalf("safe task updates were not committed: working=%+v short=%+v", working, short)
	}
}

func TestSensitiveTaskToolHTTPErrorIsStructuredSafeAndAtomic(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	before := captureTurnState(t, layers, dialog.ID)
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(
		toolCallFor("pending-note", "memory_add_working_note", `{"content":"Не сохранять"}`),
		toolCallFor("bad", "task_create", `{"goal":"password=hunter2","current_step":"План","expected_action":"Утвердить"}`),
	)}}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"Проверь данные","dialog_id":"`+dialog.ID+`"}`))
	recorder := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response chatAPIResponse
	decodeResponse(t, recorder, &response)
	if response.Tool != "task_create" || response.ErrorCategory != string(MemoryToolErrorSensitive) ||
		response.ErrorField != "goal" || response.ValidationRule != string(sensitiveRuleCredentialAssignment) || response.FSMGuard {
		t.Fatalf("unsafe or incomplete diagnostic: %+v", response)
	}
	for _, forbidden := range []string{"hunter2", "password=", `{"goal"`, "arguments", dir, "key"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
	assertTurnStateUnchanged(t, dir, dialog.ID, before)
	if _, statErr := os.Stat(dialogFile(dir, dialog.ID, workingFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("rejected task call created working.json: %v", statErr)
	}
}

func TestChatAPIMemoryToolErrorIsSafeAndSpecific(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(
		toolCallFor("bad", "task_transition", `{"stage":"execution","raw":"secret-value"}`),
	)}}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"Перейди","dialog_id":"`+dialog.ID+`"}`))
	recorder := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response chatAPIResponse
	decodeResponse(t, recorder, &response)
	if response.Tool != "task_transition" || response.ErrorCategory != string(MemoryToolErrorUnknownField) || response.ErrorField != "raw" || response.FSMGuard {
		t.Fatalf("unsafe or incomplete diagnostic: %+v", response)
	}
	for _, forbidden := range []string{"secret-value", `{"stage"`, layers.dir, "arguments"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if working.Task != nil || len(short.Turns) != 0 {
		t.Fatalf("rejected call changed memory: working=%+v short=%+v", working, short)
	}
}
