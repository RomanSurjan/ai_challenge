package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStrictJSONLoadingRejectsUnknownFieldsAtEveryLevel(t *testing.T) {
	type testCase struct {
		name   string
		store  string
		mutate func(map[string]any)
	}
	tests := []testCase{
		{name: "dialog envelope", store: "dialogs", mutate: func(root map[string]any) { root["unknown"] = true }},
		{name: "dialog data", store: "dialogs", mutate: func(root map[string]any) { root["data"].(map[string]any)["unknown"] = true }},
		{name: "dialog metadata", store: "dialogs", mutate: func(root map[string]any) {
			root["data"].(map[string]any)["dialogs"].([]any)[0].(map[string]any)["unknown"] = true
		}},
		{name: "short-term envelope", store: "short", mutate: func(root map[string]any) { root["unknown"] = true }},
		{name: "short-term data", store: "short", mutate: func(root map[string]any) { root["data"].(map[string]any)["unknown"] = true }},
		{name: "short-term turn", store: "short", mutate: func(root map[string]any) {
			root["data"].(map[string]any)["turns"].([]any)[0].(map[string]any)["unknown"] = true
		}},
		{name: "working envelope", store: "working", mutate: func(root map[string]any) { root["unknown"] = true }},
		{name: "working data", store: "working", mutate: func(root map[string]any) { root["data"].(map[string]any)["unknown"] = true }},
		{name: "working task state", store: "working", mutate: func(root map[string]any) {
			root["data"].(map[string]any)["task_state"].(map[string]any)["unknown"] = true
		}},
		{name: "long-term envelope", store: "long", mutate: func(root map[string]any) { root["unknown"] = true }},
		{name: "long-term data", store: "long", mutate: func(root map[string]any) { root["data"].(map[string]any)["unknown"] = true }},
		{name: "long-term profile entry", store: "long", mutate: func(root map[string]any) {
			root["data"].(map[string]any)["profile"].([]any)[0].(map[string]any)["unknown"] = true
		}},
		{name: "long-term typed profile", store: "long", mutate: func(root map[string]any) {
			root["data"].(map[string]any)["user_profile"].(map[string]any)["unknown"] = true
		}},
		{name: "long-term decision", store: "long", mutate: func(root map[string]any) {
			root["data"].(map[string]any)["decisions"].([]any)[0].(map[string]any)["unknown"] = true
		}},
		{name: "long-term knowledge", store: "long", mutate: func(root map[string]any) {
			root["data"].(map[string]any)["knowledge"].([]any)[0].(map[string]any)["unknown"] = true
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			layers, dialog := initializedLayers(t, dir)
			if _, err := layers.AppendSuccessfulTurnToDialog(ctx, dialog.ID, "вопрос", "ответ"); err != nil {
				t.Fatal(err)
			}
			if _, err := layers.CreateTaskInDialog(ctx, dialog.ID, "цель", "составить план", "утвердить план"); err != nil {
				t.Fatal(err)
			}
			if _, err := layers.UpsertProfile(ctx, "legacy-key", "legacy-value"); err != nil {
				t.Fatal(err)
			}
			if _, err := layers.SetProfile(ctx, testProfile("Профиль", ProfileDetailConcise, ProfileFormatPlainText)); err != nil {
				t.Fatal(err)
			}
			if _, err := layers.AddDecision(ctx, "решение", "обоснование"); err != nil {
				t.Fatal(err)
			}
			if _, err := layers.AddKnowledge(ctx, "тема", "знание"); err != nil {
				t.Fatal(err)
			}

			var path string
			var load func() error
			switch test.store {
			case "dialogs":
				path = filepath.Join(dir, dialogIndexName)
				load = func() error { _, err := layers.ListDialogs(ctx); return err }
			case "short":
				path = dialogFile(dir, dialog.ID, shortTermFileName)
				load = func() error { _, err := layers.LoadDialogShortTerm(ctx, dialog.ID); return err }
			case "working":
				path = dialogFile(dir, dialog.ID, workingFileName)
				load = func() error { _, err := layers.LoadDialogWorking(ctx, dialog.ID); return err }
			case "long":
				path = filepath.Join(dir, longTermFileName)
				load = func() error { _, err := layers.LoadLongTerm(ctx); return err }
			default:
				t.Fatalf("unknown test store %q", test.store)
			}

			mutateJSONFile(t, path, test.mutate)
			if err := load(); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("unknown field was accepted: %v", err)
			}
		})
	}
}

func TestStrictJSONLoadingRejectsEmptyBrokenAndMultipleValues(t *testing.T) {
	now := time.Now().UTC()
	valid, err := json.Marshal(jsonEnvelope[ShortTermMemory]{Version: 1, Data: ShortTermMemory{}, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "broken", data: []byte(`{"version":`)},
		{name: "multiple", data: append(append([]byte(nil), valid...), []byte(` {}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), shortTermFileName)
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewJSONShortTermStore(path).Load(context.Background()); err == nil {
				t.Fatal("invalid JSON file was accepted")
			}
		})
	}
}

func TestLegacyWorkingAndLongTermJSONRemainCompatible(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Nanosecond)

	workingPath := filepath.Join(dir, workingFileName)
	writeTestEnvelope(t, workingPath, map[string]any{
		"goal": "старая цель", "status": string(TaskStatusInProgress),
		"notes": []any{map[string]any{"content": "старая заметка", "recorded_at": now}},
	})
	working, err := NewJSONWorkingMemoryStore(workingPath).Load(ctx)
	if err != nil {
		t.Fatalf("legacy working load: %v", err)
	}
	if working.Goal != "старая цель" || working.Task != nil || len(working.Notes) != 1 || working.Notes[0].Content != "старая заметка" {
		t.Fatalf("legacy working data was lost: %+v", working)
	}

	longTermPath := filepath.Join(dir, longTermFileName)
	writeTestEnvelope(t, longTermPath, map[string]any{
		"profile":   []any{map[string]any{"key": "tone", "value": "brief", "updated_at": now}},
		"decisions": []any{map[string]any{"statement": "старое решение", "rationale": "причина", "recorded_at": now}},
		"knowledge": []any{map[string]any{"topic": "старая тема", "content": "старое знание", "recorded_at": now}},
	})
	longTerm, err := NewJSONLongTermMemoryStore(longTermPath).Load(ctx)
	if err != nil {
		t.Fatalf("legacy long-term load: %v", err)
	}
	if len(longTerm.Profile) != 1 || len(longTerm.Decisions) != 1 || len(longTerm.Knowledge) != 1 ||
		longTerm.Profile[0].Value != "brief" || longTerm.Decisions[0].Statement != "старое решение" || longTerm.Knowledge[0].Content != "старое знание" {
		t.Fatalf("legacy long-term data was lost: %+v", longTerm)
	}
}

func TestStrictWorkingLoadRejectsUnknownTaskStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), workingFileName)
	now := time.Now().UTC()
	writeTestEnvelope(t, path, map[string]any{
		"goal":   "цель",
		"status": string(TaskStatusInProgress),
		"task_state": map[string]any{
			"stage": "almost_done", "current_step": "шаг", "expected_action": "действие",
			"paused": false, "plan": "план", "plan_approved": true, "validation_passed": false, "updated_at": now,
		},
	})
	if _, err := NewJSONWorkingMemoryStore(path).Load(context.Background()); !errors.Is(err, ErrInvalidTaskStage) {
		t.Fatalf("error=%v, want ErrInvalidTaskStage", err)
	}
}

func TestUnknownFieldInOneDialogDoesNotAffectAnother(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, first := initializedLayers(t, dir)
	second, err := layers.CreateDialog(ctx, "Второй")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, first.ID, "первый", "ответ 1")
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, second.ID, "второй", "ответ 2")
	mutateJSONFile(t, dialogFile(dir, first.ID, shortTermFileName), func(root map[string]any) {
		root["data"].(map[string]any)["turns"].([]any)[0].(map[string]any)["unknown"] = true
	})
	if _, err := layers.LoadDialogShortTerm(ctx, first.ID); err == nil {
		t.Fatal("corrupted dialog was accepted")
	}
	got, err := layers.LoadDialogShortTerm(ctx, second.ID)
	if err != nil || len(got.Turns) != 1 || got.Turns[0].User != "второй" {
		t.Fatalf("second dialog was affected: memory=%+v err=%v", got, err)
	}
}

func TestLooksSensitiveDigitSequences(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		sensitive bool
	}{
		{name: "at start", value: "4111111111111111 use later", sensitive: true},
		{name: "in middle", value: "keep 4111111111111111 for later", sensitive: true},
		{name: "at end", value: "use later 4111111111111111", sensitive: true},
		{name: "spaces", value: "4111 1111 1111 1111 use later", sensitive: true},
		{name: "hyphens", value: "use 4111-1111-1111-1111 later", sensitive: true},
		{name: "short ordinary number", value: "заказ 123456 готов", sensitive: false},
		{name: "existing marker", value: "password=hunter2", sensitive: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := looksSensitive(test.value); got != test.sensitive {
				t.Fatalf("looksSensitive(%q)=%t want %t", test.value, got, test.sensitive)
			}
		})
	}
}

func TestLooksSensitiveDistinguishesTerminologyFromSecretValues(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		sensitive bool
		rule      sensitiveRule
	}{
		{name: "email and password terminology", value: "Реализовать авторизацию по email и password", sensitive: false},
		{name: "password reset terminology", value: "Проверить password reset", sensitive: false},
		{name: "access token storage terminology", value: "Настроить хранение access token", sensitive: false},
		{name: "api key rotation terminology", value: "Реализовать ротацию API key", sensitive: false},
		{name: "authorization header terminology", value: "Проверить заголовок Authorization", sensitive: false},
		{name: "card masking terminology", value: "Проверить маскирование номера карты", sensitive: false},
		{name: "password equals", value: "password=hunter2", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "password colon", value: "password: hunter2", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "api key assignment", value: "api_key=secret-value", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "access token assignment", value: "access_token=actual-secret-token", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "passwd assignment", value: "passwd=actual-secret", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "russian password assignment", value: "пароль: actual-secret", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "secret key assignment", value: "secret key=actual-secret", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "russian secret key assignment", value: "секретный ключ: actual-secret", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "cvv assignment", value: "cvv=123", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "cvc assignment", value: "cvc: 123", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "pin code assignment", value: "pin code=1234", sensitive: true, rule: sensitiveRuleCredentialAssignment},
		{name: "bearer token", value: "Authorization: Bearer actual-secret-token", sensitive: true, rule: sensitiveRuleBearerToken},
		{name: "payment card", value: "4111 1111 1111 1111", sensitive: true, rule: sensitiveRulePaymentCard},
		{name: "private key", value: "-----BEGIN PRIVATE KEY-----", sensitive: true, rule: sensitiveRulePrivateKey},
		{name: "key-like token", value: "sk-0123456789abcdefghijklmnop", sensitive: true, rule: sensitiveRuleKeyLikeToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := looksSensitive(test.value); got != test.sensitive {
				t.Fatalf("looksSensitive(%q)=%t want %t", test.value, got, test.sensitive)
			}
			if got := sensitiveRuleFor(test.value); got != test.rule {
				t.Fatalf("sensitiveRuleFor(%q)=%q want %q", test.value, got, test.rule)
			}
		})
	}
}

func TestSensitiveToolCallDiscardsAllPendingOperations(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	agent := scriptedAgent(t, layers, nil, []scriptedReply{{body: toolReply(
		toolCallFor("pending-note", "memory_add_working_note", `{"content":"не применять"}`),
		toolCallFor("sensitive-knowledge", "memory_add_knowledge", `{"topic":"payment","content":"4111 1111 1111 1111 use later"}`),
	)}}, nil)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "запомни"); !errors.Is(err, ErrMemoryToolCall) {
		t.Fatalf("error=%v, want ErrMemoryToolCall", err)
	}
	short, shortErr := layers.LoadDialogShortTerm(context.Background(), dialog.ID)
	working, workingErr := layers.LoadDialogWorking(context.Background(), dialog.ID)
	longTerm, longErr := layers.LoadLongTerm(context.Background())
	if shortErr != nil || workingErr != nil || longErr != nil {
		t.Fatalf("load after rejection: short=%v working=%v long=%v", shortErr, workingErr, longErr)
	}
	if len(short.Turns) != 0 || working.Goal != "" || len(working.Notes) != 0 || len(working.Results) != 0 || working.Task != nil ||
		len(longTerm.Profile) != 0 || longTerm.UserProfile != nil || len(longTerm.Decisions) != 0 || len(longTerm.Knowledge) != 0 {
		t.Fatalf("rejected tool call changed memory: short=%+v working=%+v long=%+v", short, working, longTerm)
	}
}

func TestMemoryLayerSelectionChangesPayloadAndAgentAnswer(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	layers, dialog := initializedLayers(t, dir)
	_, _ = layers.AppendSuccessfulTurnToDialog(ctx, dialog.ID, "SHORT_MARKER", "short answer")
	_, _ = layers.AddDialogWorkingNote(ctx, dialog.ID, "WORKING_MARKER")
	_, _ = layers.AddKnowledge(ctx, "selection", "LONG_MARKER")
	autoMemory := false

	tests := []struct {
		name      string
		selection MemorySelection
		answer    string
		present   string
	}{
		{name: "short-term", selection: MemorySelection{ShortTerm: true}, answer: "short-response", present: "SHORT_MARKER"},
		{name: "working", selection: MemorySelection{Working: true}, answer: "working-response", present: "WORKING_MARKER"},
		{name: "long-term", selection: MemorySelection{LongTerm: true}, answer: "long-response", present: "LONG_MARKER"},
		{name: "none", selection: MemorySelection{}, answer: "neutral-response"},
	}
	allMarkers := []string{"SHORT_MARKER", "WORKING_MARKER", "LONG_MARKER"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var request chatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				payload := joinedMessageContent(request.Messages)
				for _, marker := range allMarkers {
					want := marker == test.present
					if strings.Contains(payload, marker) != want {
						t.Fatalf("marker %q presence=%t want %t in:\n%s", marker, strings.Contains(payload, marker), want, payload)
					}
				}
				answer := "neutral-response"
				switch {
				case strings.Contains(payload, "SHORT_MARKER"):
					answer = "short-response"
				case strings.Contains(payload, "WORKING_MARKER"):
					answer = "working-response"
				case strings.Contains(payload, "LONG_MARKER"):
					answer = "long-response"
				}
				return response(http.StatusOK, finalReply(answer)), nil
			})}
			agent := NewAgent(AgentConfig{
				APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers,
				Selection: &test.selection, AutoMemory: &autoMemory,
			}, client)
			result, err := agent.AskInDialog(ctx, dialog.ID, "current question")
			if err != nil {
				t.Fatalf("AskInDialog: %v", err)
			}
			if result.Content != test.answer {
				t.Fatalf("answer=%q want %q", result.Content, test.answer)
			}
		})
	}
}

func mutateJSONFile(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	mutate(root)
	data, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestEnvelope(t *testing.T, path string, data any) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"version": 1, "data": data, "updated_at": time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMissingStrictJSONFileStillMeansEmptyLayer(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	short, shortErr := NewJSONShortTermStore(filepath.Join(dir, "missing-short.json")).Load(ctx)
	working, workingErr := NewJSONWorkingMemoryStore(filepath.Join(dir, "missing-working.json")).Load(ctx)
	longTerm, longErr := NewJSONLongTermMemoryStore(filepath.Join(dir, "missing-long.json")).Load(ctx)
	if shortErr != nil || workingErr != nil || longErr != nil {
		t.Fatalf("missing files returned errors: %v %v %v", shortErr, workingErr, longErr)
	}
	if !reflect.DeepEqual(short, ShortTermMemory{}) || !reflect.DeepEqual(working, WorkingMemory{}) || !reflect.DeepEqual(longTerm, LongTermMemory{}) {
		t.Fatalf("missing files did not return empty layers: short=%+v working=%+v long=%+v", short, working, longTerm)
	}
}
