package main

import (
	"context"
	"encoding/json"
	"fmt"
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

func testProfile(name string, detail ProfileDetail, format ProfileFormat) UserProfile {
	return UserProfile{
		Name: name, Language: ProfileLanguageRussian, Tone: ProfileToneFormal,
		Detail: detail, Format: format, Context: "Go-разработчик",
		Constraints: []string{"Не использовать примеры на Python"},
	}
}

func profilePointer(profile UserProfile) *UserProfile {
	return &profile
}

func TestProfilePersistsAcrossRestartAndIsSharedAcrossDialogs(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, first := initializedLayers(t, dir)
	second, err := layers.CreateDialog(ctx, "Второй")
	if err != nil {
		t.Fatal(err)
	}
	want := testProfile("Роман", ProfileDetailDetailed, ProfileFormatSteps)
	saved, err := layers.SetProfile(ctx, want)
	if err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	if saved.UpdatedAt.IsZero() {
		t.Fatal("profile timestamp was not assigned")
	}
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, first.ID, "A", "AA")
	_, _ = layers.SetDialogWorkingGoal(ctx, first.ID, "задача A")

	restarted := NewJSONMemoryLayers(dir)
	got, err := restarted.LoadProfile(ctx)
	if err != nil {
		t.Fatalf("LoadProfile after restart: %v", err)
	}
	if got == nil || got.Name != want.Name || got.Detail != want.Detail || got.Format != want.Format || !reflect.DeepEqual(got.Constraints, want.Constraints) {
		t.Fatalf("profile was not restored: %+v", got)
	}
	for _, dialogID := range []string{first.ID, second.ID} {
		memory, err := (&Agent{cfg: AgentConfig{Memory: restarted}, selection: AllMemorySelection()}).loadContextMemory(ctx, dialogID)
		if err != nil || memory.Profile == nil || memory.Profile.Name != "Роман" {
			t.Fatalf("dialog %s did not get shared profile: profile=%+v err=%v", dialogID, memory.Profile, err)
		}
	}
	secondShort, _ := restarted.LoadDialogShortTerm(ctx, second.ID)
	secondWorking, _ := restarted.LoadDialogWorking(ctx, second.ID)
	if len(secondShort.Turns) != 0 || secondWorking.Goal != "" {
		t.Fatalf("dialog-scoped memory leaked: short=%+v working=%+v", secondShort, secondWorking)
	}
}

func TestDifferentProfilesProduceDifferentRequestsAndDeterministicAnswers(t *testing.T) {
	ctx := context.Background()
	profiles := []UserProfile{
		testProfile("Профиль A", ProfileDetailConcise, ProfileFormatPlainText),
		testProfile("Профиль B", ProfileDetailDetailed, ProfileFormatSteps),
	}
	var mu sync.Mutex
	var requests []chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode model request: %v", err)
		}
		text := joinedMessageContent(request.Messages)
		answer := "ответ A"
		if strings.Contains(text, "Обращение: Профиль B") && strings.Contains(text, "пошаговый список") {
			answer = "ответ B"
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		return response(http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, answer)), nil
	})}

	answers := make([]string, 0, 2)
	for _, profile := range profiles {
		layers, dialog := initializedLayers(t, t.TempDir())
		if _, err := layers.SetProfile(ctx, profile); err != nil {
			t.Fatal(err)
		}
		agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", System: "system", Timeout: time.Second, Memory: layers}, client)
		result, err := agent.AskInDialog(ctx, dialog.ID, "Одинаковый вопрос")
		if err != nil {
			t.Fatalf("AskInDialog: %v", err)
		}
		answers = append(answers, result.Content)
	}
	if !reflect.DeepEqual(answers, []string{"ответ A", "ответ B"}) {
		t.Fatalf("profile path did not affect deterministic answers: %v", answers)
	}
	if len(requests) != 2 || reflect.DeepEqual(requests[0].Messages, requests[1].Messages) {
		t.Fatalf("profile requests must differ: %+v", requests)
	}
	for index, request := range requests {
		other := "Профиль B"
		if index == 1 {
			other = "Профиль A"
		}
		if strings.Contains(joinedMessageContent(request.Messages), other) {
			t.Fatalf("profile leaked between stores: %s", joinedMessageContent(request.Messages))
		}
	}
}

func TestProfileIsInjectedWhenLongTermSelectionIsDisabled(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	_, _ = layers.SetProfile(context.Background(), testProfile("Всегда", ProfileDetailConcise, ProfileFormatPlainText))
	_, _ = layers.AddKnowledge(context.Background(), "секция", "не должна попасть")
	selection := MemorySelection{}
	var request chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers, Selection: &selection}, client)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "вопрос"); err != nil {
		t.Fatal(err)
	}
	content := joinedMessageContent(request.Messages)
	if !strings.Contains(content, "[USER_PROFILE]") || !strings.Contains(content, "Обращение: Всегда") {
		t.Fatalf("profile is missing with disabled long-term: %s", content)
	}
	if strings.Contains(content, "не должна попасть") {
		t.Fatalf("ordinary long-term memory was not disabled: %s", content)
	}
}

func TestProfileUpdateAffectsNextRequestAndEmptyProfileAddsNoBlock(t *testing.T) {
	ctx := context.Background()
	layers, dialog := initializedLayers(t, t.TempDir())
	var requests []chatRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request chatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		requests = append(requests, request)
		return response(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)
	if _, err := agent.AskInDialog(ctx, dialog.ID, "без профиля"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(joinedMessageContent(requests[0].Messages), "[USER_PROFILE]") {
		t.Fatal("empty profile produced a system block")
	}
	_, _ = layers.SetProfile(ctx, testProfile("Первый", ProfileDetailConcise, ProfileFormatPlainText))
	_, _ = agent.AskInDialog(ctx, dialog.ID, "после первого")
	_, _ = layers.SetProfile(ctx, testProfile("Второй", ProfileDetailDetailed, ProfileFormatBullets))
	_, _ = agent.AskInDialog(ctx, dialog.ID, "после второго")
	if !strings.Contains(joinedMessageContent(requests[1].Messages), "Обращение: Первый") || strings.Contains(joinedMessageContent(requests[1].Messages), "Обращение: Второй") {
		t.Fatalf("first update was not isolated to its request: %s", joinedMessageContent(requests[1].Messages))
	}
	if !strings.Contains(joinedMessageContent(requests[2].Messages), "Обращение: Второй") {
		t.Fatalf("second update did not affect next request: %s", joinedMessageContent(requests[2].Messages))
	}
}

func TestProfileAPIValidatesAndPreservesLastGoodProfile(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	agent := NewAgent(AgentConfig{Memory: layers}, nil)
	handler := memoryAPIHandler(agent)
	valid := `{"name":"Роман","language":"ru","tone":"formal","detail":"concise","format":"plain_text","context":"Go-разработчик","constraints":["Без Python"]}`
	recorder := profileRequest(t, handler, http.MethodPut, valid)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid profile status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	invalidBodies := []string{
		`{"name":"X","language":"de","tone":"formal","detail":"concise","format":"plain_text"}`,
		`{"name":"X","language":"","tone":"formal","detail":"concise","format":"plain_text"}`,
		`{"name":"X","language":"ru","tone":"formal","detail":"concise","format":"plain_text","context":"` + strings.Repeat("я", maxProfileContextRunes+1) + `"}`,
		`{"name":"X","language":"ru","tone":"formal","detail":"concise","format":"plain_text","extra":true}`,
	}
	for _, body := range invalidBodies {
		recorder = profileRequest(t, handler, http.MethodPut, body)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "error") {
			t.Fatalf("invalid profile status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	profile, err := layers.LoadProfile(context.Background())
	if err != nil || profile == nil || profile.Name != "Роман" {
		t.Fatalf("invalid write corrupted saved profile: profile=%+v err=%v", profile, err)
	}

	recorder = profileRequest(t, handler, http.MethodGet, "")
	var response profileAPIResponse
	decodeResponse(t, recorder, &response)
	if recorder.Code != http.StatusOK || !response.Configured || response.Profile == nil || response.Profile.Name != "Роман" {
		t.Fatalf("GET profile response=%+v status=%d", response, recorder.Code)
	}
}

func TestProfileAPIAbsentVersusCorruptedStorage(t *testing.T) {
	dir := t.TempDir()
	layers, _ := initializedLayers(t, dir)
	handler := memoryAPIHandler(NewAgent(AgentConfig{Memory: layers}, nil))
	recorder := profileRequest(t, handler, http.MethodGet, "")
	var empty profileAPIResponse
	decodeResponse(t, recorder, &empty)
	if recorder.Code != http.StatusOK || empty.Configured || empty.Profile != nil {
		t.Fatalf("absent profile response=%+v status=%d", empty, recorder.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, longTermFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder = profileRequest(t, handler, http.MethodGet, "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("corrupted profile store status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestLegacyLongTermLoadsWithoutLossAndMigratesRecognizedProfile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	legacy := fmt.Sprintf(`{
  "version": 1,
  "data": {
    "profile": [
      {"key":"language","value":"Russian","updated_at":%q},
      {"key":"tone","value":"formal","updated_at":%q},
      {"key":"detail","value":"brief","updated_at":%q},
      {"key":"format","value":"text","updated_at":%q},
      {"key":"unknown_legacy_key","value":"сохранить","updated_at":%q}
    ],
    "decisions": [{"statement":"Сохранённое решение","recorded_at":%q}],
    "knowledge": [{"topic":"Go","content":"Сохранённое знание","recorded_at":%q}]
  },
  "updated_at": %q
}`, now, now, now, now, now, now, now, now)
	if err := os.WriteFile(filepath.Join(dir, longTermFileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	layers := NewJSONMemoryLayers(dir)
	memory, err := layers.LoadLongTerm(context.Background())
	if err != nil {
		t.Fatalf("load legacy long-term: %v", err)
	}
	if len(memory.Profile) != 5 || len(memory.Decisions) != 1 || len(memory.Knowledge) != 1 {
		t.Fatalf("legacy data was lost: %+v", memory)
	}
	profile := memory.EffectiveProfile()
	if profile == nil || profile.Language != ProfileLanguageRussian || profile.Detail != ProfileDetailConcise || profile.Format != ProfileFormatPlainText {
		t.Fatalf("recognized legacy profile was not migrated in memory: %+v", profile)
	}
}

func TestProfileContextOrderIsStableAndContainsNoServiceData(t *testing.T) {
	profile := testProfile("Роман", ProfileDetailDetailed, ProfileFormatSteps)
	memory := ContextMemory{
		Profile:   profilePointer(profile),
		Working:   WorkingMemory{Goal: "цель", Status: TaskStatusInProgress},
		LongTerm:  LongTermMemory{Knowledge: []Knowledge{{Topic: "Go", Content: "факт", RecordedAt: time.Now().UTC()}}},
		ShortTerm: ShortTermMemory{Turns: []ConversationTurn{{User: "старый", Assistant: "ответ", CreatedAt: time.Now().UTC()}}},
	}
	first := (ContextBuilder{}).Build("основной system", "новый", AllMemorySelection(), memory)
	second := (ContextBuilder{}).Build("основной system", "новый", AllMemorySelection(), memory)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("context order is not deterministic")
	}
	want := []string{"основной system", "[USER_PROFILE]", "Рабочая память", "Долговременная память", "старый", "ответ", "новый"}
	for index, fragment := range want {
		if !strings.Contains(first[index].Content, fragment) {
			t.Fatalf("message %d does not contain %q: %+v", index, fragment, first)
		}
	}
	joined := joinedMessageContent(first)
	for _, forbidden := range []string{`"language"`, `"updated_at"`, longTermFileName, "/tmp/"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("service data leaked into prompt: %q in %s", forbidden, joined)
		}
	}
}

func TestLegacyProfilePOSTRemainsAvailable(t *testing.T) {
	layers, _ := initializedLayers(t, t.TempDir())
	handler := memoryAPIHandler(NewAgent(AgentConfig{Memory: layers}, nil))
	recorder := profileRequest(t, handler, http.MethodPost, `{"key":"language","value":"Russian"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("legacy profile POST status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	memory, err := layers.LoadLongTerm(context.Background())
	if err != nil || len(memory.Profile) != 1 || memory.Profile[0].Key != "language" {
		t.Fatalf("legacy profile write failed: memory=%+v err=%v", memory, err)
	}
}

func TestChatPageProfileEditorContract(t *testing.T) {
	for _, fragment := range []string{
		`id="edit-profile"`,
		`id="profile-dialog"`,
		`id="profile-language"`,
		`id="profile-constraints"`,
		`fetch('/api/memory/long-term/profile')`,
		`method: 'PUT'`,
		`body: JSON.stringify(profile)`,
		`Следующий ответ будет учитывать профиль`,
	} {
		if !strings.Contains(chatPageHTML, fragment) {
			t.Fatalf("chat page misses profile editor contract %q", fragment)
		}
	}
}

func profileRequest(t *testing.T, handler http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/api/memory/long-term/profile", strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
