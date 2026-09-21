package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutoMemoryOrdinaryAnswerOnlySavesShortTerm(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	var request chatRequest
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: finalReply("обычный ответ")}}, &request)

	answer, err := agent.AskInDialog(context.Background(), dialog.ID, "обычный вопрос")
	if err != nil {
		t.Fatalf("AskInDialog: %v", err)
	}
	if len(answer.MemoryUpdates) != 0 || len(request.Tools) != 14 || request.ToolChoice != "auto" {
		t.Fatalf("unexpected response/request: answer=%+v tools=%d choice=%q", answer, len(request.Tools), request.ToolChoice)
	}
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	longTerm, _ := layers.LoadLongTerm(context.Background())
	if len(short.Turns) != 1 || short.Turns[0].Assistant != "обычный ответ" {
		t.Fatalf("short-term not saved: %+v", short)
	}
	if working.Goal != "" || len(working.Notes) != 0 || len(longTerm.Profile)+len(longTerm.Decisions)+len(longTerm.Knowledge) != 0 {
		t.Fatalf("non-short memory changed: working=%+v long=%+v", working, longTerm)
	}
}

func TestAutoMemoryWorkingNoteIsDialogScoped(t *testing.T) {
	layers, first := initializedLayers(t, t.TempDir())
	second, _ := layers.CreateDialog(context.Background(), "Второй")
	replies := []scriptedReply{
		{body: toolReply(toolCallFor("call-note", "memory_add_working_note", `{"content":"Проверить граничные случаи"}`))},
		{body: finalReply("Заметка учтена")},
	}
	agent := scriptedAgent(t, layers, nil, replies, nil)
	answer, err := agent.AskInDialog(context.Background(), first.ID, "Продолжим задачу")
	if err != nil {
		t.Fatalf("AskInDialog: %v", err)
	}
	firstWorking, _ := layers.LoadDialogWorking(context.Background(), first.ID)
	secondWorking, _ := layers.LoadDialogWorking(context.Background(), second.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), first.ID)
	if len(firstWorking.Notes) != 1 || firstWorking.Notes[0].Content != "Проверить граничные случаи" || len(secondWorking.Notes) != 0 {
		t.Fatalf("working scope broken: first=%+v second=%+v", firstWorking, secondWorking)
	}
	if len(short.Turns) != 1 || len(answer.MemoryUpdates) != 1 || answer.MemoryUpdates[0].Category != "notes" {
		t.Fatalf("unexpected short-term/updates: short=%+v updates=%+v", short, answer.MemoryUpdates)
	}
}

func TestAutoMemoryWorkingGoalAndStatusUseTypedValidation(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	replies := []scriptedReply{
		{body: toolReply(
			toolCallFor("call-goal", "memory_set_working_goal", `{"goal":"Подготовить доклад"}`),
			toolCallFor("call-status", "memory_set_working_status", `{"status":"not_started"}`),
		)},
		{body: finalReply("Планирую")},
	}
	agent := scriptedAgent(t, layers, nil, replies, nil)
	answer, err := agent.AskInDialog(context.Background(), dialog.ID, "Начни работу")
	if err != nil {
		t.Fatalf("AskInDialog: %v", err)
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	if working.Goal != "Подготовить доклад" || working.Status != TaskStatusNotStarted || working.Task == nil || working.Task.Stage != TaskStagePlanning {
		t.Fatalf("unexpected working memory: %+v", working)
	}
	if len(answer.MemoryUpdates) != 1 || answer.MemoryUpdates[0].Category != "goal" {
		t.Fatalf("unexpected memory updates: %+v", answer.MemoryUpdates)
	}

	before := cloneWorkingMemory(working)
	invalidAgent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(toolCallFor("bad-stage", "task_transition", `{"stage":"almost_done"}`))},
	}, nil)
	if _, err := invalidAgent.AskInDialog(context.Background(), dialog.ID, "Неверный этап"); !errors.Is(err, ErrMemoryToolCall) {
		t.Fatalf("invalid task tool error=%v", err)
	}
	after, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid stage changed working memory: before=%+v after=%+v", before, after)
	}
	if len(short.Turns) != 1 {
		t.Fatalf("invalid stage changed short-term: %+v", short)
	}
}

func TestAutoMemoryLongTermKnowledgeIsSharedAndNotDuplicatedInDialogs(t *testing.T) {
	dir := t.TempDir()
	layers, first := initializedLayers(t, dir)
	second, _ := layers.CreateDialog(context.Background(), "Второй")
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(toolCallFor("knowledge", "memory_add_knowledge", `{"topic":"Проект","content":"Релиз по пятницам"}`))},
		{body: finalReply("Принято")},
	}, nil)
	if _, err := agent.AskInDialog(context.Background(), first.ID, "Релизимся по пятницам"); err != nil {
		t.Fatalf("AskInDialog: %v", err)
	}
	for _, id := range []string{first.ID, second.ID} {
		memory, err := layers.LoadLongTerm(context.Background())
		if err != nil || len(memory.Knowledge) != 1 || memory.Knowledge[0].Content != "Релиз по пятницам" {
			t.Fatalf("long-term unavailable for %s: memory=%+v err=%v", id, memory, err)
		}
		if _, err := os.Stat(dialogFile(dir, id, longTermFileName)); !os.IsNotExist(err) {
			t.Fatalf("dialog-scoped long-term file exists for %s: %v", id, err)
		}
	}
}

func TestAutoMemoryProfileUpsertDoesNotDuplicateKey(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(
			toolCallFor("profile-1", "memory_upsert_profile", `{"key":"tone","value":"brief"}`),
			toolCallFor("profile-2", "memory_upsert_profile", `{"key":"tone","value":"detailed"}`),
		)},
		{body: finalReply("Обновлено")},
	}, nil)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "Предпочитаю подробные ответы"); err != nil {
		t.Fatalf("AskInDialog: %v", err)
	}
	memory, _ := layers.LoadLongTerm(context.Background())
	if len(memory.Profile) != 1 || memory.Profile[0].Key != "tone" || memory.Profile[0].Value != "detailed" {
		t.Fatalf("profile was duplicated or not updated: %+v", memory.Profile)
	}
}

func TestAutoMemoryMultipleCallsAppliedOnceAndReturnedByAPI(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	duplicate := toolCallFor("note-duplicate", "memory_add_working_note", `{"content":"Одна заметка"}`)
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(
			toolCallFor("note", "memory_add_working_note", `{"content":"Одна заметка"}`),
			duplicate,
			toolCallFor("decision", "memory_add_decision", `{"statement":"Использовать JSON","rationale":"Строгая схема"}`),
		)},
		{body: finalReply("Готово")},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"Сохрани","dialog_id":"`+dialog.ID+`"}`))
	rec := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got chatAPIResponse
	decodeResponse(t, rec, &got)
	if len(got.MemoryUpdates) != 2 || got.MemoryUpdates[0].Summary != "рабочая заметка" || got.MemoryUpdates[1].Summary != "решение" {
		t.Fatalf("unexpected memory_updates: %+v", got.MemoryUpdates)
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	longTerm, _ := layers.LoadLongTerm(context.Background())
	if len(working.Notes) != 1 || len(longTerm.Decisions) != 1 {
		t.Fatalf("duplicate/multiple application broken: working=%+v long=%+v", working, longTerm)
	}
}

func TestInvalidMemoryToolCallsNeverWrite(t *testing.T) {
	tests := []struct {
		name string
		call toolCall
	}{
		{"unknown name", toolCallFor("bad-1", "memory_delete_everything", `{}`)},
		{"malformed JSON", toolCallFor("bad-2", "memory_add_working_note", `{"content":`)},
		{"unknown dialog field", toolCallFor("bad-3", "memory_add_working_note", `{"content":"x","dialog_id":"dlg-other"}`)},
		{"unknown path field", toolCallFor("bad-4", "memory_add_knowledge", `{"topic":"x","content":"y","path":"/tmp/other"}`)},
		{"empty value", toolCallFor("bad-5", "memory_add_working_note", `{"content":" "}`)},
		{"too long", toolCallFor("bad-6", "memory_upsert_profile", `{"key":"`+strings.Repeat("x", maxProfileKeyRunes+1)+`","value":"v"}`)},
		{"secret", toolCallFor("bad-7", "memory_add_knowledge", `{"topic":"credential","content":"api_key=secret-value"}`)},
		{"invalid status", toolCallFor("bad-8", "memory_set_working_status", `{"status":"almost_done"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			layers, dialog := initializedLayers(t, dir)
			agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(test.call)}}, nil)
			if _, err := agent.AskInDialog(context.Background(), dialog.ID, "test"); !errors.Is(err, ErrMemoryToolCall) {
				t.Fatalf("error=%v", err)
			}
			short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
			working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
			longTerm, _ := layers.LoadLongTerm(context.Background())
			if len(short.Turns) != 0 || len(working.Notes) != 0 || len(longTerm.Knowledge) != 0 {
				t.Fatalf("invalid call changed memory: short=%+v working=%+v long=%+v", short, working, longTerm)
			}
		})
	}
}

func TestInvalidTaskToolCallsRollbackWholeTurn(t *testing.T) {
	tests := []struct {
		name string
		call toolCall
	}{
		{"unknown stage", toolCallFor("bad-task-1", "task_transition", `{"stage":"almost_done"}`)},
		{"malformed JSON", toolCallFor("bad-task-2", "task_transition", `{"stage":`)},
		{"unknown field", toolCallFor("bad-task-3", "task_transition", `{"stage":"execution","raw":"do not leak"}`)},
		{"empty required value", toolCallFor("bad-task-4", "task_create", `{"goal":" ","current_step":"План","expected_action":"Утвердить"}`)},
		{"too long value", toolCallFor("bad-task-5", "task_approve_plan", `{"plan":"`+strings.Repeat("x", maxWorkingContentRunes+1)+`"}`)},
		{"sensitive value", toolCallFor("bad-task-6", "task_create", `{"goal":"api_key=secret-value","current_step":"План","expected_action":"Утвердить"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			layers, dialog := initializedLayers(t, dir)
			beforeWorking, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
			beforeShort, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
			beforeLong, _ := layers.LoadLongTerm(context.Background())
			beforeDialogs, _ := layers.ListDialogs(context.Background())
			agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(
				toolCallFor("pending-note", "memory_add_working_note", `{"content":"Не сохранять"}`),
				toolCallFor("pending-knowledge", "memory_add_knowledge", `{"topic":"rollback","content":"Не сохранять"}`),
				test.call,
			)}}, nil)

			if _, err := agent.AskInDialog(context.Background(), dialog.ID, "test"); !errors.Is(err, ErrMemoryToolCall) {
				t.Fatalf("error=%v", err)
			}
			afterWorking, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
			afterShort, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
			afterLong, _ := layers.LoadLongTerm(context.Background())
			afterDialogs, _ := layers.ListDialogs(context.Background())
			if !reflect.DeepEqual(beforeWorking, afterWorking) || !reflect.DeepEqual(beforeShort, afterShort) || !reflect.DeepEqual(beforeLong, afterLong) || !reflect.DeepEqual(beforeDialogs, afterDialogs) {
				t.Fatalf("invalid task call changed memory: working=%+v short=%+v long=%+v", afterWorking, afterShort, afterLong)
			}
			if _, statErr := os.Stat(dialogFile(dir, dialog.ID, workingFileName)); !os.IsNotExist(statErr) {
				t.Fatalf("rejected task call created working.json: %v", statErr)
			}
		})
	}
}

func TestSuccessfulTaskToolFlowCommitsOnlyAfterFinalAnswer(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(http.StatusOK, toolReply(
				toolCallFor("create", "task_create", `{"goal":"Цель","current_step":"План","expected_action":"Утвердить"}`),
				toolCallFor("approve", "task_approve_plan", `{"plan":"План"}`),
				toolCallFor("execute", "task_transition", `{"stage":"execution"}`),
			)), nil
		}
		working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
		short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
		if working.Task != nil || len(short.Turns) != 0 {
			t.Fatalf("pending task flow was committed before final answer: working=%+v short=%+v", working, short)
		}
		return response(http.StatusOK, finalReply("Выполнение начато")), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)

	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "Начинай"); err != nil {
		t.Fatal(err)
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if calls != 2 || working.Task == nil || working.Task.Stage != TaskStageExecution || !working.Task.PlanApproved || len(short.Turns) != 1 {
		t.Fatalf("successful task flow was not committed: calls=%d working=%+v short=%+v", calls, working, short)
	}
}

func TestDeepSeekFailureAfterToolCallDiscardsPendingMemory(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(toolCallFor("pending", "memory_add_working_note", `{"content":"Не применять"}`))},
		{status: http.StatusInternalServerError, body: `{"error":{"message":"failure"}}`},
	}, nil)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "test"); !errors.Is(err, ErrDeepSeek) {
		t.Fatalf("error=%v", err)
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if len(working.Notes) != 0 || len(short.Turns) != 0 {
		t.Fatalf("pending changes were persisted: working=%+v short=%+v", working, short)
	}
}

func TestAutoMemoryToolRoundLimit(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	replies := make([]scriptedReply, maxMemoryToolRounds+1)
	for i := range replies {
		replies[i].body = toolReply(toolCallFor("round-"+string(rune('a'+i)), "memory_add_working_note", `{"content":"same"}`))
	}
	agent := scriptedAgent(t, layers, nil, replies, nil)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "loop"); !errors.Is(err, ErrMemoryToolCall) || !strings.Contains(err.Error(), "лимит раундов") {
		t.Fatalf("error=%v", err)
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if len(working.Notes) != 0 || len(short.Turns) != 0 {
		t.Fatalf("loop persisted memory: working=%+v short=%+v", working, short)
	}
}

func TestAutoMemoryOperationLimit(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	calls := make([]toolCall, 0, maxMemoryOperations+1)
	for i := 0; i < maxMemoryOperations+1; i++ {
		calls = append(calls, toolCallFor("operation-"+string(rune('a'+i)), "memory_add_working_note", `{"content":"note `+string(rune('a'+i))+`"}`))
	}
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(calls...)}}, nil)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "too many"); !errors.Is(err, ErrMemoryToolCall) || !strings.Contains(err.Error(), "лимит операций") {
		t.Fatalf("error=%v", err)
	}
	working, _ := layers.LoadDialogWorking(context.Background(), dialog.ID)
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if len(working.Notes) != 0 || len(short.Turns) != 0 {
		t.Fatalf("operation overflow persisted memory: working=%+v short=%+v", working, short)
	}
}

func TestAutoMemoryDisabledOmitsToolsAndKeepsShortTerm(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	disabled := false
	var request chatRequest
	agent := scriptedAgent(t, layers, &disabled, []scriptedReply{{body: finalReply("без авто-памяти")}}, &request)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "вопрос"); err != nil {
		t.Fatalf("AskInDialog: %v", err)
	}
	if len(request.Tools) != 0 || request.ToolChoice != nil || strings.Contains(joinedMessageContent(request.Messages), memoryPolicyInstruction) {
		t.Fatalf("disabled request contains memory tools/instruction: %+v", request)
	}
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if len(short.Turns) != 1 {
		t.Fatalf("short-term stopped working: %+v", short)
	}
}

func TestDisabledLayersDoNotExposeTheirTools(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	selection := MemorySelection{ShortTerm: true, Working: true, LongTerm: false}
	var request chatRequest
	agent := scriptedAgentWithSelection(t, layers, nil, selection, []scriptedReply{{body: finalReply("ok")}}, &request)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "question"); err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 11 {
		t.Fatalf("working-only tool count=%d", len(request.Tools))
	}
	for _, tool := range request.Tools {
		if strings.HasPrefix(tool.Function.Name, "memory_add_decision") || strings.Contains(tool.Function.Name, "profile") || strings.Contains(tool.Function.Name, "knowledge") {
			t.Fatalf("long-term tool exposed: %s", tool.Function.Name)
		}
	}
}

func TestThinkingToolCallReasoningContentIsReturned(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	var requests []chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		if len(requests) == 1 {
			return response(http.StatusOK, toolReplyWithReasoning("checked", toolCallFor("call", "memory_add_working_note", `{"content":"note"}`))), nil
		}
		return response(http.StatusOK, finalReply("ok")), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers, Thinking: true}, client)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "question"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || len(requests[1].Messages) < 2 || requests[1].Messages[len(requests[0].Messages)].ReasoningContent != "checked" {
		t.Fatalf("reasoning_content not forwarded: %+v", requests)
	}
}

func TestConcurrentAutoMemoryRequestsRemainConsistent(t *testing.T) {
	layers, first := initializedLayers(t, t.TempDir())
	second, _ := layers.CreateDialog(context.Background(), "Второй")
	var mu sync.Mutex
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return response(http.StatusOK, finalReply("ok")), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)
	var wg sync.WaitGroup
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(dialogID string) {
			defer wg.Done()
			if _, err := agent.AskInDialog(context.Background(), dialogID, "question"); err != nil {
				t.Errorf("AskInDialog: %v", err)
			}
		}(id)
	}
	wg.Wait()
	for _, id := range []string{first.ID, second.ID} {
		short, _ := layers.LoadDialogShortTerm(context.Background(), id)
		if len(short.Turns) != 1 {
			t.Fatalf("dialog %s turns=%d", id, len(short.Turns))
		}
	}
}

func TestAutoMemoryFrontendContract(t *testing.T) {
	required := []string{
		`className = 'memory-update'`,
		`notice.textContent = 'Память обновлена: '`,
		`validateMemoryUpdates(data.memory_updates)`,
		`addMemoryUpdateNotice(memoryUpdates)`,
		`'working:notes'`,
		`'long-term:knowledge'`,
	}
	for _, fragment := range required {
		if !strings.Contains(chatPageHTML, fragment) {
			t.Fatalf("frontend misses %q", fragment)
		}
	}
	for _, forbidden := range []string{"update.dialog_id", "update.path", "update.file"} {
		if strings.Contains(chatPageHTML, forbidden) {
			t.Fatalf("frontend exposes internal memory field %q", forbidden)
		}
	}
}

func TestAutoMemoryPolicyIsSeparateSystemMessage(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	var request chatRequest
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: finalReply("ok")}}, &request)
	agent.cfg.System = "Основной контракт"
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "вопрос"); err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) < 3 || request.Messages[0].Content != "Основной контракт" || request.Messages[1].Content != memoryPolicyInstruction {
		t.Fatalf("memory policy is not a separate system message: %+v", request.Messages)
	}
}

func TestRequiredMemoryToolsAreExposedWithNarrowSchemas(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	required := map[string][]string{
		"memory_set_working_goal":   {"goal"},
		"memory_set_working_status": {"status"},
		"memory_add_working_note":   {"content"},
		"memory_add_working_result": {"content"},
		"memory_upsert_profile":     {"key", "value"},
		"memory_add_decision":       {"statement", "rationale"},
		"memory_add_knowledge":      {"topic", "content"},
	}
	for _, tool := range agent.memoryTools() {
		want, ok := required[tool.Function.Name]
		if !ok {
			continue
		}
		if tool.Function.Parameters["additionalProperties"] != false || !reflect.DeepEqual(tool.Function.Parameters["required"], want) {
			t.Fatalf("tool %s has a broad schema: %+v", tool.Function.Name, tool.Function.Parameters)
		}
		delete(required, tool.Function.Name)
	}
	if len(required) != 0 {
		t.Fatalf("required memory tools are missing: %+v", required)
	}
}

func TestMemoryToolSchemasInSerializedModelPayload(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	var payload []byte
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var err error
		payload, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read model payload: %v", err)
		}
		return response(http.StatusOK, finalReply("ok")), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "question"); err != nil {
		t.Fatalf("AskInDialog: %v", err)
	}
	if bytes.Contains(payload, []byte(`"required":null`)) {
		t.Fatalf("serialized model payload contains required:null: %s", payload)
	}

	var request struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string                     `json:"name"`
				Parameters map[string]json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode serialized model payload: %v", err)
	}

	wantRequired := map[string][]string{
		"memory_set_working_goal":   {"goal"},
		"memory_set_working_status": {"status"},
		"memory_add_working_note":   {"content"},
		"memory_add_working_result": {"content"},
		"task_create":               {"goal", "current_step", "expected_action"},
		"task_update_progress":      {"current_step", "expected_action"},
		"task_approve_plan":         {"plan"},
		"task_transition":           {"stage"},
		"task_record_validation":    {"passed", "details"},
		"task_pause":                nil,
		"task_resume":               nil,
		"memory_upsert_profile":     {"key", "value"},
		"memory_add_decision":       {"statement", "rationale"},
		"memory_add_knowledge":      {"topic", "content"},
	}
	if len(request.Tools) != len(wantRequired) {
		t.Fatalf("serialized tool count=%d want %d", len(request.Tools), len(wantRequired))
	}

	seen := make(map[string]struct{}, len(request.Tools))
	for _, tool := range request.Tools {
		name := tool.Function.Name
		want, ok := wantRequired[name]
		if !ok {
			t.Fatalf("unexpected serialized tool %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("duplicate serialized tool %q", name)
		}
		seen[name] = struct{}{}
		if tool.Type != "function" {
			t.Errorf("tool %s type=%q want function", name, tool.Type)
		}

		parameters := tool.Function.Parameters
		var schemaType string
		if err := json.Unmarshal(parameters["type"], &schemaType); err != nil || schemaType != "object" {
			t.Errorf("tool %s parameters.type=%q err=%v", name, schemaType, err)
		}
		var additionalProperties bool
		if err := json.Unmarshal(parameters["additionalProperties"], &additionalProperties); err != nil || additionalProperties {
			t.Errorf("tool %s additionalProperties=%t err=%v", name, additionalProperties, err)
		}
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(parameters["properties"], &properties); err != nil || properties == nil {
			t.Errorf("tool %s properties is not an object: raw=%s err=%v", name, parameters["properties"], err)
		}

		requiredJSON, hasRequired := parameters["required"]
		if len(want) == 0 {
			if len(properties) != 0 {
				t.Errorf("no-argument tool %s properties=%s want empty object", name, parameters["properties"])
			}
			if hasRequired {
				var got []string
				if err := json.Unmarshal(requiredJSON, &got); err != nil || got == nil || len(got) != 0 {
					t.Errorf("no-argument tool %s required=%s; want absent or empty array", name, requiredJSON)
				}
			}
			continue
		}
		if !hasRequired {
			t.Errorf("tool %s has no required array", name)
			continue
		}
		var got []string
		if err := json.Unmarshal(requiredJSON, &got); err != nil {
			t.Errorf("tool %s required is not a string array: raw=%s err=%v", name, requiredJSON, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tool %s required=%v want %v", name, got, want)
		}
	}
	if len(seen) != len(wantRequired) {
		t.Fatalf("serialized tool set changed: got=%v", seen)
	}
}

type scriptedReply struct {
	status int
	body   string
	err    error
}

func scriptedAgent(t *testing.T, layers *MemoryLayers, autoMemory *bool, replies []scriptedReply, lastRequest *chatRequest) *Agent {
	return scriptedAgentWithSelection(t, layers, autoMemory, AllMemorySelection(), replies, lastRequest)
}

func scriptedAgentWithSelection(t *testing.T, layers *MemoryLayers, autoMemory *bool, selection MemorySelection, replies []scriptedReply, lastRequest *chatRequest) *Agent {
	t.Helper()
	var mu sync.Mutex
	next := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if lastRequest != nil {
			*lastRequest = request
		}
		if next >= len(replies) {
			t.Fatalf("unexpected DeepSeek request %d", next+1)
		}
		reply := replies[next]
		next++
		if reply.err != nil {
			return nil, reply.err
		}
		status := reply.status
		if status == 0 {
			status = http.StatusOK
		}
		return response(status, reply.body), nil
	})}
	return NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers, Selection: &selection, AutoMemory: autoMemory}, client)
}

func toolCallFor(id, name, arguments string) toolCall {
	return toolCall{ID: id, Type: "function", Function: toolCallFunction{Name: name, Arguments: arguments}}
}

func toolReply(calls ...toolCall) string {
	return toolReplyWithReasoning("", calls...)
}

func toolReplyWithReasoning(reasoning string, calls ...toolCall) string {
	responseBody := chatResponse{}
	responseBody.Choices = append(responseBody.Choices, struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	}{Message: chatMessage{Role: "assistant", ReasoningContent: reasoning, ToolCalls: calls}, FinishReason: "tool_calls"})
	data, _ := json.Marshal(responseBody)
	return string(data)
}

func finalReply(content string) string {
	responseBody := chatResponse{}
	responseBody.Choices = append(responseBody.Choices, struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	}{Message: chatMessage{Role: "assistant", Content: content}, FinishReason: "stop"})
	data, _ := json.Marshal(responseBody)
	return string(data)
}

type failingLongTermStore struct{}

func (failingLongTermStore) Load(context.Context) (LongTermMemory, error) {
	return LongTermMemory{}, nil
}
func (failingLongTermStore) Save(context.Context, LongTermMemory) error {
	return errors.New("storage failed")
}

func TestLongTermWriteFailureReturnsSafeErrorAndDoesNotSaveShortTerm(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	layers.LongTerm = failingLongTermStore{}
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(toolCallFor("knowledge", "memory_add_knowledge", `{"topic":"topic","content":"content"}`))},
		{body: finalReply("answer")},
	}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"test","dialog_id":"`+dialog.ID+`"}`))
	rec := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "storage failed") || strings.Contains(rec.Body.String(), dialog.ID) {
		t.Fatalf("unsafe write error: status=%d body=%s", rec.Code, rec.Body.String())
	}
	short, _ := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	if len(short.Turns) != 0 {
		t.Fatalf("short-term saved after memory write failure: %+v", short)
	}
}

func TestAutoMemoryConfigDefaultsToEnabled(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "key")
	cfg, err := readConfig([]string{"-prompt", "hello"}, strings.NewReader(""))
	if err != nil || !cfg.AutoMemory {
		t.Fatalf("default config=%+v err=%v", cfg, err)
	}
	cfg, err = readConfig([]string{"-prompt", "hello", "-auto-memory=false"}, strings.NewReader(""))
	if err != nil || cfg.AutoMemory {
		t.Fatalf("disabled config=%+v err=%v", cfg, err)
	}
}

func TestLongTermKnowledgeLivesOnlyAtSharedPath(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	if _, err := layers.AddKnowledge(context.Background(), "topic", "content"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, longTermFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dialogFile(dir, dialog.ID, longTermFileName)); !os.IsNotExist(err) {
		t.Fatalf("unexpected dialog long-term file: %v", err)
	}
}
