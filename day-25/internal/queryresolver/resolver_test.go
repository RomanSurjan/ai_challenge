package queryresolver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/generation"
)

func TestValidateStandaloneAndPronounResolution(t *testing.T) {
	current := chat.Message{ID: "U2", Role: chat.RoleUser, Content: "А как реализовано второе?"}
	history := []chat.Message{{ID: "U1", Role: chat.RoleUser, Content: "Важны path traversal и atomic write"}, {ID: "A1", Role: chat.RoleAssistant, Content: "не источник"}}
	state := chat.TaskState{Goal: &chat.MemoryItem{ID: "M1", Kind: chat.KindGoal, Value: "аудит Artifact MCP"}}
	resolved := chat.Resolution{SearchQuery: "Как Artifact MCP реализует atomic write?", UsedTurnIDs: []string{"U1"}, UsedMemoryIDs: []string{"M1"}}
	if err := Validate(resolved, current, history, state); err != nil {
		t.Fatal(err)
	}
	standalone := chat.Resolution{SearchQuery: "Как работает ListTools?", UsedTurnIDs: []string{}, UsedMemoryIDs: []string{}}
	if err := Validate(standalone, current, history, state); err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnknownAssistantAndStaleIDs(t *testing.T) {
	current := chat.Message{ID: "U2", Role: chat.RoleUser, Content: "это?"}
	history := []chat.Message{{ID: "A1", Role: chat.RoleAssistant, Content: "fact"}}
	state := chat.TaskState{Constraints: []chat.MemoryItem{{ID: "Mactive", Kind: chat.KindConstraint, Value: "x"}}, History: []chat.MemoryItem{{ID: "Mstale", Kind: chat.KindConstraint, Value: "old"}}}
	for _, r := range []chat.Resolution{
		{SearchQuery: "q", UsedTurnIDs: []string{"U9"}},
		{SearchQuery: "q", UsedTurnIDs: []string{"A1"}},
		{SearchQuery: "q", UsedMemoryIDs: []string{"Mstale"}},
		{SearchQuery: "", UsedMemoryIDs: []string{}},
	} {
		if err := Validate(r, current, history, state); err == nil {
			t.Fatalf("invalid resolution accepted: %+v", r)
		}
	}
}

func TestMalformedAndModelErrorFallbackPreserveOriginal(t *testing.T) {
	current := chat.Message{ID: "U1", Role: chat.RoleUser, Content: "Исходное сообщение"}
	for _, fake := range []*fakeGenerator{{response: generation.Response{Text: "{"}}, {err: errors.New("timeout")}} {
		resolver := NewOllama(fake, "model", 64)
		trace, _ := resolver.ResolveDetailed(context.Background(), current, nil, chat.TaskState{})
		if !trace.Fallback || trace.Result.SearchQuery != current.Content || trace.Error == "" {
			t.Fatalf("bad fallback: %+v", trace)
		}
	}
}

func TestPromptContainsCurrentGoalAndNoExpectedData(t *testing.T) {
	current := chat.Message{ID: "U2", Role: chat.RoleUser, Content: "а второй вариант?"}
	state := chat.TaskState{Goal: &chat.MemoryItem{ID: "M1", Kind: chat.KindGoal, Value: "Artifact audit", SourceTurnID: "U1"}, History: []chat.MemoryItem{{ID: "Mold", Kind: chat.KindConstraint, Value: "stale expected answer"}}}
	prompt := Prompt(current, []chat.Message{{ID: "U1", Role: chat.RoleUser, Content: "цель"}}, state)
	if !strings.Contains(prompt, current.Content) || !strings.Contains(prompt, "Artifact audit") {
		t.Fatalf("prompt lost current/goal: %s", prompt)
	}
	if strings.Contains(prompt, "stale expected answer") || strings.Contains(prompt, "expected_sources") {
		t.Fatalf("prompt leaked stale/evaluation data: %s", prompt)
	}
}

type fakeGenerator struct {
	response generation.Response
	err      error
}

func (f *fakeGenerator) Generate(context.Context, string, string, generation.Settings) (generation.Response, error) {
	return f.response, f.err
}
