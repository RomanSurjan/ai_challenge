package chatservice

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/chatstore"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/generation"
	"ai-challenge/day-25/internal/indexstore"
	"ai-challenge/day-25/internal/judging"
	"ai-challenge/day-25/internal/memory"
)

func TestFakeTenTurnScenarioSurvivesWindowRestartAndSupersede(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := chatstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	index := indexstore.Index{Model: "embed", EmbeddingDimension: 2, Chunks: []indexstore.Chunk{{ChunkID: "c1", Source: "store.go", Section: "Save", Text: "Атомарная запись выполняется через временный файл, fsync и безопасный rename.", Embedding: []float64{1, 0}}}}
	before := append([]indexstore.Chunk(nil), index.Chunks...)
	gen := &fakeAnswerGenerator{}
	rag := &agent.Agent{Index: &index, IndexPath: "index.json", Embedder: fakeEmbedder{}, Generator: gen, Judge: fakeJudge{}, Validator: evidence.Validator{MinQuoteRunes: 20, MaxQuoteRunes: 160}, EmbedModel: "embed", ChatModel: "chat", Settings: generation.DefaultSettings(), Pipeline: agent.PipelineConfig{CandidateK: 1, FinalK: 1, MinSimilarity: 0, AnswerMinRelevance: .55, Alpha: .7, Beta: .2, Gamma: .1}}
	service := &Service{Store: store, RAG: rag, MemoryExtractor: &fakeMemory{}, Resolver: fakeResolver{}, MemoryValidator: memory.Validator{MaxEntries: 8, Now: func() time.Time { return time.Unix(1, 0) }}, Limits: chat.Limits{MaxRecentTurns: 2, MaxHistoryRunes: 200, MaxTaskStateItems: 8, MaxSessionMessages: 40, MaxMessageRunes: 1000}}
	messages := []string{"Наша цель — аудит Artifact MCP.", "Ограничение: только Go.", "вопрос 3", "вопрос 4", "вопрос 5", "вопрос 6", "вопрос 7", "вопрос 8", "Больше не учитывай ограничение только Go.", "А что было нашей целью?"}
	var last TurnResult
	for _, message := range messages {
		last, err = service.Ask(ctx, "scenario", TaskMemory, message)
		if err != nil {
			t.Fatal(err)
		}
	}
	if last.Record.Status != "memory_only" || !strings.Contains(last.Record.Answer, "аудит Artifact MCP") {
		t.Fatalf("goal did not survive window: %+v", last.Record)
	}
	restarted, err := chatstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.Load(ctx, "scenario")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TaskState.Goal == nil || len(loaded.TaskState.History) != 1 || len(loaded.TaskState.Constraints) != 1 || !strings.Contains(loaded.TaskState.Constraints[0].Value, "не ограничиваться") {
		t.Fatalf("restart/supersede failed: %+v", loaded.TaskState)
	}
	if !reflect.DeepEqual(before, index.Chunks) {
		t.Fatal("RAG index was mutated")
	}
	data, err := json.Marshal(last.Record)
	if err != nil {
		t.Fatal(err)
	}
	var round chat.TurnRecord
	if err := json.Unmarshal(data, &round); err != nil || round.UserMessageID != last.Record.UserMessageID {
		t.Fatalf("turn JSON round trip failed: %v", err)
	}
	for _, prompt := range gen.prompts {
		if strings.Contains(prompt, "expected_sources") || strings.Contains(prompt, "required_concepts") {
			t.Fatalf("evaluation data leaked into prompt: %s", prompt)
		}
	}
}

func TestMessageAndSessionLimits(t *testing.T) {
	limits := chat.Limits{MaxRecentTurns: 1, MaxHistoryRunes: 10, MaxTaskStateItems: 2, MaxSessionMessages: 2, MaxMessageRunes: 2}
	store, _ := chatstore.New(t.TempDir())
	service := &Service{Store: store, RAG: &agent.Agent{}, Limits: limits}
	if _, err := service.Ask(context.Background(), "s", Stateless, "длинно"); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("Unicode message limit not enforced: %v", err)
	}
	session, _ := store.Create(context.Background(), "full")
	session.Messages = append(session.Messages, chat.Message{ID: "U1", Turn: 1, Role: chat.RoleUser, Content: "x"})
	session.Version++
	if err := store.Save(context.Background(), 1, session); err != nil {
		t.Fatal(err)
	}
	service.Limits.MaxMessageRunes = 20
	if _, err := service.Ask(context.Background(), "full", Stateless, "ok"); err == nil || !strings.Contains(err.Error(), "session message limit") {
		t.Fatalf("session limit not enforced: %v", err)
	}
}

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(context.Context, string, []string) ([][]float64, error) {
	return [][]float64{{1, 0}}, nil
}

type fakeAnswerGenerator struct{ prompts []string }

func (f *fakeAnswerGenerator) Generate(_ context.Context, _ string, prompt string, _ generation.Settings) (generation.Response, error) {
	f.prompts = append(f.prompts, prompt)
	return generation.Response{Text: `{"status":"answered","answer":"Файл сохраняется атомарно [S1].","claims":[{"id":"C1","text":"Файл сохраняется атомарно","kind":"corpus","source_ids":["S1"]}],"evidence":[{"source_id":"S1","claim_ids":["C1"],"quote":"временный файл, fsync и безопасный rename"}],"clarification_question":""}`, Usage: generation.Usage{PromptTokens: 10, CompletionTokens: 5}}, nil
}

type fakeJudge struct{}

func (fakeJudge) Judge(_ context.Context, r judging.Request) judging.Result {
	return judging.Result{ClaimID: r.Claim.ID, Verdict: judging.Supported, Reason: "ok"}
}

type fakeResolver struct{}

func (fakeResolver) ResolveDetailed(_ context.Context, current chat.Message, _ []chat.Message, state chat.TaskState) (chat.ResolutionTrace, generation.Usage) {
	q := current.Content
	if state.Goal != nil {
		q = state.Goal.Value + " " + q
	}
	return chat.ResolutionTrace{Result: chat.Resolution{SearchQuery: q, UsedTurnIDs: []string{current.ID}, UsedMemoryIDs: []string{}}}, generation.Usage{PromptTokens: 1, CompletionTokens: 1}
}

type fakeMemory struct{}

func (fakeMemory) Extract(_ context.Context, request memory.Request, _ []string) (generation.Response, error) {
	op := chat.MemoryOperation{Op: "noop"}
	switch {
	case strings.HasPrefix(request.Current.Content, "Наша цель"):
		op = chat.MemoryOperation{Op: "set_goal", Kind: chat.KindGoal, Value: "аудит Artifact MCP", SourceTurnID: request.Current.ID, UserQuote: request.Current.Content}
	case strings.HasPrefix(request.Current.Content, "Ограничение:"):
		op = chat.MemoryOperation{Op: "add", Kind: chat.KindConstraint, Value: "только Go", SourceTurnID: request.Current.ID, UserQuote: request.Current.Content}
	case strings.HasPrefix(request.Current.Content, "Больше не"):
		old := ""
		if len(request.State.Constraints) > 0 {
			old = request.State.Constraints[0].ID
		}
		op = chat.MemoryOperation{Op: "supersede", Kind: chat.KindConstraint, Value: "не ограничиваться только Go", SourceTurnID: request.Current.ID, UserQuote: request.Current.Content, Supersedes: old}
	}
	payload, _ := json.Marshal(memory.Payload{Operations: []chat.MemoryOperation{op}})
	return generation.Response{Text: string(payload)}, nil
}
