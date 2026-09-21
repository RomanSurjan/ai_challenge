package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFirstRunCreatesActiveDialogAndRestoresIt(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers := NewJSONMemoryLayers(dir)
	first, err := layers.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if first.Title != "Диалог 1" || validateDialogID(first.ID) != nil {
		t.Fatalf("unexpected first dialog: %+v", first)
	}
	second, err := layers.CreateDialog(ctx, "Рабочий чат")
	if err != nil {
		t.Fatalf("CreateDialog: %v", err)
	}
	if second.ID == first.ID || second.Title != "Рабочий чат" || validateDialogID(second.ID) != nil {
		t.Fatalf("unexpected second dialog: %+v", second)
	}

	restarted := NewJSONMemoryLayers(dir)
	active, err := restarted.Initialize(ctx)
	if err != nil {
		t.Fatalf("restart Initialize: %v", err)
	}
	if active.ID != second.ID {
		t.Fatalf("active dialog was not restored: got %s want %s", active.ID, second.ID)
	}
	list, err := restarted.ListDialogs(ctx)
	if err != nil {
		t.Fatalf("ListDialogs: %v", err)
	}
	if list.ActiveDialogID != second.ID || len(list.Dialogs) != 2 {
		t.Fatalf("unexpected list: %+v", list)
	}
	for _, dialog := range list.Dialogs {
		if dialog.CreatedAt.IsZero() || dialog.UpdatedAt.IsZero() || dialog.Title == "" {
			t.Fatalf("incomplete metadata: %+v", dialog)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, dialogIndexName)); err != nil {
		t.Fatalf("dialog index not persisted: %v", err)
	}
}

func TestSuccessfulTurnsAndWorkingMemoryAreIsolated(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, first := initializedLayers(t, dir)
	second, err := layers.CreateDialog(ctx, "Второй")
	if err != nil {
		t.Fatalf("CreateDialog: %v", err)
	}
	if _, err := layers.AppendSuccessfulTurnToDialog(ctx, first.ID, "вопрос A", "ответ A"); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if _, err := layers.AppendSuccessfulTurnToDialog(ctx, second.ID, "вопрос B", "ответ B"); err != nil {
		t.Fatalf("append second: %v", err)
	}
	if _, err := layers.SetDialogWorkingGoal(ctx, first.ID, "задача A"); err != nil {
		t.Fatalf("working first: %v", err)
	}
	if _, err := layers.SetDialogWorkingGoal(ctx, second.ID, "задача B"); err != nil {
		t.Fatalf("working second: %v", err)
	}
	firstShort, _ := layers.LoadDialogShortTerm(ctx, first.ID)
	secondShort, _ := layers.LoadDialogShortTerm(ctx, second.ID)
	firstWorking, _ := layers.LoadDialogWorking(ctx, first.ID)
	secondWorking, _ := layers.LoadDialogWorking(ctx, second.ID)
	if len(firstShort.Turns) != 1 || firstShort.Turns[0].User != "вопрос A" || len(secondShort.Turns) != 1 || secondShort.Turns[0].User != "вопрос B" {
		t.Fatalf("short-term histories mixed: first=%+v second=%+v", firstShort, secondShort)
	}
	if firstWorking.Goal != "задача A" || secondWorking.Goal != "задача B" {
		t.Fatalf("working memories mixed: first=%+v second=%+v", firstWorking, secondWorking)
	}
	if _, err := os.Stat(dialogFile(dir, first.ID, shortTermFileName)); err != nil {
		t.Fatalf("first short-term file missing: %v", err)
	}
	if _, err := os.Stat(dialogFile(dir, second.ID, workingFileName)); err != nil {
		t.Fatalf("second working file missing: %v", err)
	}
}

func TestAgentBuildsDifferentRequestsForDifferentDialogs(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, first := initializedLayers(t, dir)
	second, _ := layers.CreateDialog(ctx, "Второй")
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, first.ID, "история A", "ответ A")
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, second.ID, "история B", "ответ B")
	_, _ = layers.SetDialogWorkingGoal(ctx, first.ID, "цель A")
	_, _ = layers.SetDialogWorkingGoal(ctx, second.ID, "цель B")
	_, _ = layers.SetProfile(ctx, testProfile("Общий", ProfileDetailConcise, ProfileFormatPlainText))

	var mu sync.Mutex
	var requests []chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, got)
		mu.Unlock()
		return response(http.StatusOK, `{"choices":[{"message":{"content":"новый ответ"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", System: "system", Timeout: time.Second, Memory: layers}, client)
	for _, id := range []string{first.ID, second.ID} {
		answer, err := agent.AskInDialog(ctx, id, "одинаковый вопрос")
		if err != nil {
			t.Fatalf("AskInDialog(%s): %v", id, err)
		}
		if answer.DialogID != id {
			t.Fatalf("response dialog ID=%s want %s", answer.DialogID, id)
		}
	}
	if len(requests) != 2 || reflect.DeepEqual(requests[0].Messages, requests[1].Messages) {
		t.Fatalf("dialog requests should differ: %+v", requests)
	}
	firstText := joinedMessageContent(requests[0].Messages)
	secondText := joinedMessageContent(requests[1].Messages)
	for _, expected := range []string{"история A", "цель A", "Обращение: Общий", "одинаковый вопрос"} {
		if !strings.Contains(firstText, expected) {
			t.Fatalf("first context misses %q:\n%s", expected, firstText)
		}
	}
	if strings.Contains(firstText, "история B") || strings.Contains(firstText, "цель B") || strings.Contains(secondText, "история A") || strings.Contains(secondText, "цель A") {
		t.Fatalf("dialog context leaked:\nfirst=%s\nsecond=%s", firstText, secondText)
	}
	if !strings.Contains(secondText, "Обращение: Общий") {
		t.Fatalf("shared long-term memory missing from second context: %s", secondText)
	}
}

func TestDeepSeekErrorDoesNotSaveTurn(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, dialog := initializedLayers(t, dir)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusInternalServerError, `{"error":{"message":"failure"}}`), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)
	if _, err := agent.AskInDialog(ctx, dialog.ID, "не сохраняй"); err == nil {
		t.Fatal("expected DeepSeek error")
	}
	memory, err := layers.LoadDialogShortTerm(ctx, dialog.ID)
	if err != nil {
		t.Fatalf("LoadDialogShortTerm: %v", err)
	}
	if len(memory.Turns) != 0 {
		t.Fatalf("failed turn was saved: %+v", memory)
	}
}

func TestLongTermIsSharedAndExplicitWritesDoNotChangeHistories(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, first := initializedLayers(t, dir)
	second, _ := layers.CreateDialog(ctx, "Второй")
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, first.ID, "A", "AA")
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, second.ID, "B", "BB")
	firstBefore, _ := layers.LoadDialogShortTerm(ctx, first.ID)
	secondBefore, _ := layers.LoadDialogShortTerm(ctx, second.ID)
	if _, err := layers.AddKnowledge(ctx, "shared", "общий факт"); err != nil {
		t.Fatalf("AddKnowledge: %v", err)
	}
	if _, err := layers.AddDialogWorkingNote(ctx, first.ID, "только первая задача"); err != nil {
		t.Fatalf("AddDialogWorkingNote: %v", err)
	}
	firstAfter, _ := layers.LoadDialogShortTerm(ctx, first.ID)
	secondAfter, _ := layers.LoadDialogShortTerm(ctx, second.ID)
	secondWorking, _ := layers.LoadDialogWorking(ctx, second.ID)
	longTerm, _ := layers.LoadLongTerm(ctx)
	if !reflect.DeepEqual(firstBefore, firstAfter) || !reflect.DeepEqual(secondBefore, secondAfter) {
		t.Fatal("explicit working/long-term write changed a dialog history")
	}
	if len(secondWorking.Notes) != 0 {
		t.Fatalf("working note leaked to second dialog: %+v", secondWorking)
	}
	if len(longTerm.Knowledge) != 1 || longTerm.Knowledge[0].Content != "общий факт" {
		t.Fatalf("unexpected shared long-term memory: %+v", longTerm)
	}
}

func TestCorruptedDialogIsIsolated(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, first := initializedLayers(t, dir)
	second, _ := layers.CreateDialog(ctx, "Второй")
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, first.ID, "A", "AA")
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, second.ID, "B", "BB")
	if err := os.WriteFile(dialogFile(dir, first.ID, shortTermFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatalf("corrupt first dialog: %v", err)
	}
	if _, err := layers.LoadDialogShortTerm(ctx, first.ID); err == nil || !strings.Contains(err.Error(), first.ID) || !strings.Contains(err.Error(), "краткосрочную память") {
		t.Fatalf("expected clear dialog error, got %v", err)
	}
	secondMemory, err := layers.LoadDialogShortTerm(ctx, second.ID)
	if err != nil || len(secondMemory.Turns) != 1 {
		t.Fatalf("second dialog should remain readable: memory=%+v err=%v", secondMemory, err)
	}
}

func TestDialogIDValidationRejectsTraversalAndUnknownDialog(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	ctx := context.Background()
	for _, id := range []string{"", "../secret", "dlg-../../secret", "dlg-UPPER", "dlg-with/slash", "other-123"} {
		if id == "" {
			continue
		}
		if _, err := layers.GetDialog(ctx, id); !errors.Is(err, ErrInvalidDialogID) {
			t.Fatalf("GetDialog(%q) error=%v, want ErrInvalidDialogID", id, err)
		}
	}
	if _, err := layers.GetDialog(ctx, "dlg-00000000000000000000000000000000"); !errors.Is(err, ErrDialogNotFound) {
		t.Fatalf("unknown valid ID error=%v", err)
	}
}

func TestLegacyMigrationIsIdempotentAndPreservesMemory(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	now := time.Now().UTC()
	legacyShort := ShortTermMemory{Turns: []ConversationTurn{{User: "старый вопрос", Assistant: "старый ответ", CreatedAt: now}}}
	legacyWorking := WorkingMemory{Goal: "старая задача", Status: TaskStatusInProgress}
	if err := NewJSONShortTermStore(filepath.Join(dir, shortTermFileName)).Save(ctx, legacyShort); err != nil {
		t.Fatalf("seed legacy short-term: %v", err)
	}
	if err := NewJSONWorkingMemoryStore(filepath.Join(dir, workingFileName)).Save(ctx, legacyWorking); err != nil {
		t.Fatalf("seed legacy working: %v", err)
	}
	layers := NewJSONMemoryLayers(dir)
	dialog, err := layers.Initialize(ctx)
	if err != nil {
		t.Fatalf("migration: %v", err)
	}
	if dialog.ID != legacyDialogID {
		t.Fatalf("legacy dialog ID=%q", dialog.ID)
	}
	shortAfter, _ := layers.LoadDialogShortTerm(ctx, dialog.ID)
	workingAfter, _ := layers.LoadDialogWorking(ctx, dialog.ID)
	if !reflect.DeepEqual(shortAfter, legacyShort) || !reflect.DeepEqual(workingAfter, legacyWorking) {
		t.Fatalf("migration lost data: short=%+v working=%+v", shortAfter, workingAfter)
	}
	restarted := NewJSONMemoryLayers(dir)
	if _, err := restarted.Initialize(ctx); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	list, _ := restarted.ListDialogs(ctx)
	if len(list.Dialogs) != 1 || list.Dialogs[0].ID != legacyDialogID {
		t.Fatalf("migration duplicated dialog: %+v", list)
	}
}

func TestCorruptedLegacyFileReturnsMigrationError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, shortTermFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewJSONMemoryLayers(dir).Initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "мигрировать прежнюю краткосрочную память") || !strings.Contains(err.Error(), "разобрать") {
		t.Fatalf("unexpected migration error: %v", err)
	}
}

func TestContextBuilderOrderAndSelection(t *testing.T) {
	now := time.Now().UTC()
	memory := ContextMemory{
		ShortTerm: ShortTermMemory{Turns: []ConversationTurn{{User: "старый вопрос", Assistant: "старый ответ", CreatedAt: now}}},
		Working:   WorkingMemory{Goal: "цель", Status: TaskStatusInProgress},
		Profile:   profilePointer(testProfile("Роман", ProfileDetailConcise, ProfileFormatPlainText)),
		LongTerm:  LongTermMemory{Decisions: []Decision{{Statement: "решение", RecordedAt: now}}},
	}
	messages := (ContextBuilder{}).Build("system", "текущий вопрос", AllMemorySelection(), memory)
	if len(messages) != 7 {
		t.Fatalf("unexpected messages: %+v", messages)
	}
	wantRoles := []string{"system", "system", "system", "system", "user", "assistant", "user"}
	gotRoles := make([]string, 0, len(messages))
	for _, message := range messages {
		gotRoles = append(gotRoles, message.Role)
	}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("context order=%v want %v", gotRoles, wantRoles)
	}
	withoutShort := (ContextBuilder{}).Build("system", "текущий вопрос", MemorySelection{Working: true, LongTerm: true}, memory)
	if strings.Contains(joinedMessageContent(withoutShort), "старый вопрос") {
		t.Fatal("unselected short-term memory leaked")
	}
	if strings.Contains(joinedMessageContent(messages), `"goal"`) || strings.Contains(joinedMessageContent(messages), `"profile"`) {
		t.Fatal("service JSON leaked into context")
	}
}

func TestDialogAndHistoryAPI(t *testing.T) {
	dir := t.TempDir()
	layers, first := initializedLayers(t, dir)
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	dialogsHandler := dialogsAPIHandler(agent)

	createReq := httptest.NewRequest(http.MethodPost, "/api/dialogs", strings.NewReader(`{"title":"API чат"}`))
	createRec := httptest.NewRecorder()
	dialogsHandler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created dialogsAPIResponse
	decodeResponse(t, createRec, &created)
	if created.DialogID == "" || created.Dialog == nil || created.DialogID != created.Dialog.ID {
		t.Fatalf("create response misses dialog_id: %+v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/dialogs", nil)
	listRec := httptest.NewRecorder()
	dialogsHandler.ServeHTTP(listRec, listReq)
	var list dialogsAPIResponse
	decodeResponse(t, listRec, &list)
	if list.ActiveDialogID != created.DialogID || len(list.Dialogs) != 2 {
		t.Fatalf("unexpected API list: %+v", list)
	}
	for _, dialog := range list.Dialogs {
		if dialog.ID == "" || dialog.Title == "" {
			t.Fatalf("dialog list must keep both ID and title: %+v", dialog)
		}
	}
	getReq := httptest.NewRequest(http.MethodGet, "/api/dialogs/"+created.DialogID, nil)
	getRec := httptest.NewRecorder()
	dialogsHandler.ServeHTTP(getRec, getReq)
	var fetched dialogsAPIResponse
	decodeResponse(t, getRec, &fetched)
	if fetched.DialogID != created.DialogID || fetched.Dialog == nil || fetched.Dialog.Title != "API чат" {
		t.Fatalf("unexpected dialog metadata: %+v", fetched)
	}

	selectReq := httptest.NewRequest(http.MethodPost, "/api/dialogs/active", strings.NewReader(`{"dialog_id":"`+first.ID+`"}`))
	selectRec := httptest.NewRecorder()
	dialogsHandler.ServeHTTP(selectRec, selectReq)
	var selected dialogsAPIResponse
	decodeResponse(t, selectRec, &selected)
	if selected.DialogID != first.ID {
		t.Fatalf("selected wrong dialog: %+v", selected)
	}

	_, _ = layers.AppendSuccessfulTurnToDialog(context.Background(), first.ID, "вопрос", "ответ")
	historyReq := httptest.NewRequest(http.MethodGet, "/api/dialogs/"+first.ID+"/history", nil)
	historyRec := httptest.NewRecorder()
	dialogsHandler.ServeHTTP(historyRec, historyReq)
	var history historyAPIResponse
	decodeResponse(t, historyRec, &history)
	if history.DialogID != first.ID || len(history.Messages) != 2 {
		t.Fatalf("unexpected history response: %+v", history)
	}

	badReq := httptest.NewRequest(http.MethodGet, "/api/dialogs/dlg-UPPER", nil)
	badRec := httptest.NewRecorder()
	dialogsHandler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid ID status=%d body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestChatPageDialogSidebarContract(t *testing.T) {
	required := []string{
		`class="sidebar"`,
		`id="new-dialog"`,
		`id="dialog-list"`,
		`id="current-dialog-title"`,
		`title.textContent = dialog.title;`,
		`currentDialogTitle.textContent = activeDialog ? activeDialog.title : 'Диалог';`,
		`dialogButton.classList.toggle('active', isActive);`,
		`dialogButton.setAttribute('aria-current', 'page');`,
		`dialogButton.removeAttribute('aria-current');`,
		`const createdID = validateDialogID(data.dialog_id);`,
		`body: JSON.stringify({dialog_id: requestedID}),`,
		`body: JSON.stringify({message: text, dialog_id: requestDialogID}),`,
		`fetch('/api/history?dialog_id=' + encodeURIComponent(dialogID))`,
		`showHistoryLoading('Переключаем диалог');`,
		`token !== navigationToken || dialogID !== activeDialogID`,
	}
	for _, fragment := range required {
		if !strings.Contains(chatPageHTML, fragment) {
			t.Fatalf("chat page misses frontend contract %q", fragment)
		}
	}

	if strings.Contains(chatPageHTML, `<select id="dialog`) || strings.Contains(chatPageHTML, `option.textContent`) {
		t.Fatal("dialog picker must be an accessible button list, not a select")
	}
	for lineNumber, line := range strings.Split(chatPageHTML, "\n") {
		if strings.Contains(line, "textContent") && strings.Contains(line, ".id") {
			t.Fatalf("line %d renders an internal dialog ID: %s", lineNumber+1, strings.TrimSpace(line))
		}
		if strings.Contains(line, "aria-label") && strings.Contains(line, ".id") {
			t.Fatalf("line %d exposes an internal dialog ID to assistive technology: %s", lineNumber+1, strings.TrimSpace(line))
		}
	}
}

func TestChatPageJavaScriptSyntax(t *testing.T) {
	start := strings.Index(chatPageHTML, "<script>")
	end := strings.LastIndex(chatPageHTML, "</script>")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("chat page script block not found")
	}
	script := chatPageHTML[start+len("<script>") : end]
	path := filepath.Join(t.TempDir(), "chat-page.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("write temporary JavaScript: %v", err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable; JavaScript syntax check skipped")
	}
	if output, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("JavaScript syntax error: %v\n%s", err, output)
	}
}

func TestChatAndMemoryAPIReturnDialogID(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ответ"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)

	chatReq := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"вопрос","dialog_id":"`+dialog.ID+`"}`))
	chatRec := httptest.NewRecorder()
	chatAPIHandler(agent).ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", chatRec.Code, chatRec.Body.String())
	}
	var chat chatAPIResponse
	decodeResponse(t, chatRec, &chat)
	if chat.DialogID != dialog.ID || chat.Content != "ответ" {
		t.Fatalf("unexpected chat response: %+v", chat)
	}

	for _, path := range []string{"short-term", "working"} {
		req := httptest.NewRequest(http.MethodGet, "/api/memory/"+path+"?dialog_id="+dialog.ID, nil)
		rec := httptest.NewRecorder()
		memoryAPIHandler(agent).ServeHTTP(rec, req)
		var got memoryAPIResponse
		decodeResponse(t, rec, &got)
		if got.DialogID != dialog.ID || got.Layer != path {
			t.Fatalf("GET %s response=%+v", path, got)
		}
	}

	writeReq := httptest.NewRequest(http.MethodPost, "/api/memory/working/goal?dialog_id="+dialog.ID, strings.NewReader(`{"value":"цель API"}`))
	writeRec := httptest.NewRecorder()
	memoryAPIHandler(agent).ServeHTTP(writeRec, writeReq)
	var working memoryAPIResponse
	decodeResponse(t, writeRec, &working)
	if working.DialogID != dialog.ID || working.Working == nil || working.Working.Goal != "цель API" {
		t.Fatalf("unexpected working response: %+v", working)
	}
}

func TestAPIStatusCodes(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	handler := dialogsAPIHandler(agent)

	for _, test := range []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodDelete, "/api/dialogs", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/dialogs", "{broken", http.StatusBadRequest},
		{http.MethodGet, "/api/dialogs/dlg-00000000000000000000000000000000", "", http.StatusNotFound},
		{http.MethodPost, "/api/dialogs/active", `{"dialog_id":"../bad"}`, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != test.want {
			t.Fatalf("%s %s status=%d want=%d body=%s", test.method, test.path, rec.Code, test.want, rec.Body.String())
		}
	}
	if got := httpStatusForError(ErrDialogConflict); got != http.StatusConflict {
		t.Fatalf("conflict status=%d", got)
	}
}

func TestCorruptedDialogReturnsHTTP500(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	if err := os.MkdirAll(filepath.Dir(dialogFile(dir, dialog.ID, shortTermFileName)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dialogFile(dir, dialog.ID, shortTermFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/history?dialog_id="+dialog.ID, nil)
	rec := httptest.NewRecorder()
	historyAPIHandler(agent).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), dialog.ID) {
		t.Fatalf("corrupted store status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestConfigDialogModesAndDay11Paths(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DAY11_MEMORY_DIR", "")
	list, err := readConfig([]string{"-dialog-list"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("dialog-list config: %v", err)
	}
	if !list.DialogList || list.MemoryDir != "day-11-15/memory" {
		t.Fatalf("unexpected list config: %+v", list)
	}
	created, err := readConfig([]string{"-dialog-new", "-dialog-title", "Проект"}, strings.NewReader(""))
	if err != nil || !created.DialogNew || created.DialogTitle != "Проект" {
		t.Fatalf("unexpected create config: %+v err=%v", created, err)
	}
	if _, err := readConfig([]string{"-dialog-list", "-dialog-new"}, strings.NewReader("")); err == nil {
		t.Fatal("expected mutually exclusive mode error")
	}
	if _, err := readConfig([]string{"-dialog-title", "bad"}, strings.NewReader("")); err == nil {
		t.Fatal("expected standalone dialog-title error")
	}
	if _, err := readConfig([]string{"-dialog", "../bad", "-prompt", "x"}, strings.NewReader("")); !errors.Is(err, ErrInvalidDialogID) {
		t.Fatalf("expected invalid dialog ID, got %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "key")
	prompt, err := readConfig([]string{"-prompt", "hello", "-dialog", "dlg-00000000000000000000000000000000"}, strings.NewReader(""))
	if err != nil || prompt.DialogID == "" {
		t.Fatalf("prompt config: %+v err=%v", prompt, err)
	}
	for _, name := range []string{"run_web.sh", "README.md", "chat_page.html"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(content), "day-7") || strings.Contains(string(content), "DAY7") {
			t.Fatalf("%s contains a legacy Day 7 reference", name)
		}
	}
}

func TestUnselectedCorruptedLayerIsNotLoaded(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, dialog := initializedLayers(t, dir)
	if err := os.MkdirAll(filepath.Dir(dialogFile(dir, dialog.ID, workingFileName)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dialogFile(dir, dialog.ID, workingFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	selection := MemorySelection{ShortTerm: true, LongTerm: true}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers, Selection: &selection}, client)
	if _, err := agent.AskInDialog(ctx, dialog.ID, "question"); err != nil {
		t.Fatalf("unselected corrupted layer affected request: %v", err)
	}
}

func initializedLayers(t *testing.T, dir string) (*MemoryLayers, DialogMetadata) {
	t.Helper()
	layers := NewJSONMemoryLayers(dir)
	dialog, err := layers.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return layers, dialog
}

func dialogFile(dir, dialogID, name string) string {
	return filepath.Join(dir, dialogsDirName, dialogID, name)
}

func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(target); err != nil {
		t.Fatalf("decode response (%d %s): %v", recorder.Code, recorder.Body.String(), err)
	}
}

func response(statusCode int, body string) *http.Response {
	return &http.Response{StatusCode: statusCode, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func joinedMessageContent(messages []chatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Role+": "+message.Content)
	}
	return strings.Join(parts, "\n")
}
