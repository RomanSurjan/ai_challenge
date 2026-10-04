package memory

import (
	"context"
	"reflect"
	"testing"
	"time"

	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/generation"
)

func validator() Validator {
	return Validator{MaxEntries: 8, Now: func() time.Time { return time.Unix(1, 0) }}
}
func user(id, text string) chat.Message {
	return chat.Message{ID: id, Role: chat.RoleUser, Content: text}
}
func op(kind chat.MemoryKind, value, id, quote string) chat.MemoryOperation {
	return chat.MemoryOperation{Op: "add", Kind: kind, Value: value, SourceTurnID: id, UserQuote: quote}
}

func TestValidGoalConstraintTermDecisionAndDeterministicOrder(t *testing.T) {
	messages := []chat.Message{user("U1", "Наша цель — аудит."), user("U2", "Ограничение: только Go."), user("U3", "Термин: атомарно."), user("U4", "Решение: проверять fsync.")}
	state, errs := validator().Apply(chat.TaskState{}, []chat.MemoryOperation{{Op: "set_goal", Kind: chat.KindGoal, Value: "аудит", SourceTurnID: "U1", UserQuote: "Наша цель — аудит."}, op(chat.KindConstraint, "только Go", "U2", "Ограничение: только Go."), op(chat.KindTerm, "атомарно", "U3", "Термин: атомарно."), op(chat.KindDecision, "проверять fsync", "U4", "Решение: проверять fsync.")}, messages)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if state.Goal == nil || len(state.Constraints) != 1 || len(state.Terms) != 1 || len(state.Decisions) != 1 {
		t.Fatalf("bad state: %+v", state)
	}
	state2, errs := validator().Apply(chat.TaskState{}, []chat.MemoryOperation{{Op: "set_goal", Kind: chat.KindGoal, Value: "аудит", SourceTurnID: "U1", UserQuote: "Наша цель — аудит."}, op(chat.KindConstraint, "только Go", "U2", "Ограничение: только Go.")}, messages)
	if len(errs) > 0 || state.Goal.ID != state2.Goal.ID || state.Constraints[0].ID != state2.Constraints[0].ID {
		t.Fatalf("stable IDs failed")
	}
}

func TestProvenanceAndContractRejections(t *testing.T) {
	messages := []chat.Message{user("U1", "Ограничение: только Go."), {ID: "A1", Role: chat.RoleAssistant, Content: "только Go"}}
	tests := []struct {
		name      string
		operation chat.MemoryOperation
		contains  string
	}{
		{"quote not exact", op(chat.KindConstraint, "Go", "U1", "только Rust"), "exact substring"},
		{"assistant source", op(chat.KindConstraint, "Go", "A1", "только Go"), "not a user"},
		{"unknown turn", op(chat.KindConstraint, "Go", "U9", "Go"), "unknown source"},
		{"invalid kind", chat.MemoryOperation{Op: "add", Kind: "fact", Value: "x", SourceTurnID: "U1", UserQuote: "Go"}, "invalid kind"},
		{"invalid op", chat.MemoryOperation{Op: "delete", Kind: chat.KindConstraint, Value: "x", SourceTurnID: "U1", UserQuote: "Go"}, "invalid op"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := validator().Apply(chat.TaskState{}, []chat.MemoryOperation{tt.operation}, messages)
			if len(errs) == 0 {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDuplicateSupersedeConflictAndAuditHistory(t *testing.T) {
	messages := []chat.Message{user("U1", "Ограничение: только Go."), user("U2", "Больше не учитывай только Go.")}
	state, errs := validator().Apply(chat.TaskState{}, []chat.MemoryOperation{op(chat.KindConstraint, "только Go", "U1", "Ограничение: только Go.")}, messages)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, errs = validator().Apply(state, []chat.MemoryOperation{op(chat.KindConstraint, "  Только   go ", "U1", "Ограничение: только Go.")}, messages); len(errs) == 0 {
		t.Fatal("duplicate accepted")
	}
	old := state.Constraints[0]
	replacement := chat.MemoryOperation{Op: "supersede", Kind: chat.KindConstraint, Value: "не ограничиваться Go", SourceTurnID: "U2", UserQuote: "Больше не учитывай только Go.", Supersedes: old.ID}
	state, errs = validator().Apply(state, []chat.MemoryOperation{replacement}, messages)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(state.History) != 1 || state.History[0].ID != old.ID || len(state.Constraints) != 1 || state.Constraints[0].Supersedes != old.ID {
		t.Fatalf("bad supersede: %+v", state)
	}
	wrong := replacement
	wrong.Kind = chat.KindTerm
	if _, errs = validator().Apply(state, []chat.MemoryOperation{wrong}, messages); len(errs) == 0 {
		t.Fatal("cross-kind supersede accepted")
	}
	unknown := replacement
	unknown.Supersedes = "Munknown"
	if _, errs = validator().Apply(state, []chat.MemoryOperation{unknown}, messages); len(errs) == 0 {
		t.Fatal("unknown supersede accepted")
	}
}

func TestMalformedRepairFailureLeavesStateUnchanged(t *testing.T) {
	initial := chat.TaskState{Constraints: []chat.MemoryItem{{ID: "M1", Kind: chat.KindConstraint, Value: "x", SourceTurnID: "U1", UserQuote: "x"}}}
	extractor := &fakeExtractor{responses: []generation.Response{{Text: "{"}, {Text: `{"operations":[{"op":"add","kind":"constraint","value":"y","source_turn_id":"U9","user_quote":"y","supersedes":""}]}`}}}
	result := Update(context.Background(), extractor, validator(), Request{Current: user("U2", "y"), State: initial}, []chat.Message{user("U1", "x"), user("U2", "y")})
	if !result.Failed || len(result.Attempts) != 2 || !reflect.DeepEqual(result.State, initial) {
		t.Fatalf("failed update changed state: %+v", result)
	}
	if extractor.errorsSeen == 0 {
		t.Fatal("repair did not receive validation errors")
	}
}

func TestStateLimitAndStrictJSON(t *testing.T) {
	if _, err := Parse(`{"operations":[]} trailing`); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	v := validator()
	v.MaxEntries = 1
	messages := []chat.Message{user("U1", "a"), user("U2", "b")}
	_, errs := v.Apply(chat.TaskState{}, []chat.MemoryOperation{op(chat.KindConstraint, "a", "U1", "a"), op(chat.KindTerm, "b", "U2", "b")}, messages)
	if len(errs) == 0 {
		t.Fatal("state limit accepted")
	}
}

type fakeExtractor struct {
	responses  []generation.Response
	calls      int
	errorsSeen int
}

func (f *fakeExtractor) Extract(_ context.Context, _ Request, errs []string) (generation.Response, error) {
	if len(errs) > 0 {
		f.errorsSeen++
	}
	r := f.responses[f.calls]
	f.calls++
	return r, nil
}
