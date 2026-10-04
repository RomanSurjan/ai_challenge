package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ai-challenge/day-24/internal/evidence"
	"ai-challenge/day-24/internal/generation"
	"ai-challenge/day-24/internal/indexstore"
	"ai-challenge/day-24/internal/judging"
	"ai-challenge/day-24/internal/reranking"
	"ai-challenge/day-24/internal/retrieval"
)

type fakeEmbedder struct {
	vector []float64
	calls  int
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string, texts []string) ([][]float64, error) {
	f.calls++
	return [][]float64{append([]float64(nil), f.vector...)}, nil
}

type scriptedGenerator struct {
	answers []string
	calls   int
	prompts []string
	fail    error
}

func (f *scriptedGenerator) Generate(_ context.Context, _ string, prompt string, _ generation.Settings) (generation.Response, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.fail != nil {
		return generation.Response{}, f.fail
	}
	answer := f.answers[min(f.calls-1, len(f.answers)-1)]
	return generation.Response{Text: answer, Usage: generation.Usage{PromptTokens: 10, CompletionTokens: 5}}, nil
}

type fakeJudge struct {
	result judging.Result
	calls  int
}

func (f *fakeJudge) Judge(_ context.Context, r judging.Request) judging.Result {
	f.calls++
	out := f.result
	out.ClaimID = r.Claim.ID
	return out
}

func structuredJSON() string {
	payload := evidence.Response{Status: evidence.StatusAnswered, Answer: "Сохранение выполняется атомарно [S1].", Claims: []evidence.Claim{{ID: "C1", Text: "Сохранение выполняется атомарно", SourceIDs: []string{"S1"}}}, Evidence: []evidence.Quote{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: "атомарная запись выполняется через временный файл и rename"}}}
	data, _ := json.Marshal(payload)
	return string(data)
}
func testAgent(score float64, answers ...string) (*Agent, *fakeEmbedder, *scriptedGenerator, *fakeJudge) {
	embed := &fakeEmbedder{vector: []float64{1, 0}}
	gen := &scriptedGenerator{answers: answers}
	judge := &fakeJudge{result: judging.Result{Verdict: judging.Supported, Reason: "цитата подтверждает claim"}}
	index := &indexstore.Index{Model: "embed", EmbeddingDimension: 2, Chunks: []indexstore.Chunk{{ChunkID: "a", Source: "store.go", Section: "Save", Text: "В реализации атомарная запись выполняется через временный файл и rename после fsync.", Embedding: []float64{score, (1 - score)}}, {ChunkID: "b", Source: "other.go", Section: "Other", Text: "Другой технический текст достаточной длины для второго источника.", Embedding: []float64{0, 1}}}}
	a := &Agent{Index: index, IndexPath: "index.json", Embedder: embed, Generator: gen, Judge: judge, Validator: evidence.Validator{MinQuoteRunes: 20, MaxQuoteRunes: 500}, EmbedModel: "embed", ChatModel: "chat", Settings: generation.DefaultSettings(), Pipeline: PipelineConfig{CandidateK: 2, FinalK: 1, MinSimilarity: 0, AnswerMinRelevance: .5, Alpha: .7, Beta: .2, Gamma: .1}}
	return a, embed, gen, judge
}

func TestStructuredEndToEndAndJSONRoundTrip(t *testing.T) {
	a, _, gen, judge := testAgent(1, structuredJSON())
	before := append([]indexstore.Chunk(nil), a.Index.Chunks...)
	result, err := a.Ask(context.Background(), "Как сохраняется файл?", Grounded)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusAnswered || len(result.Claims) != 1 || len(result.Sources) != 1 || len(result.Citations) != 1 || !result.Citations[0].ExactMatch || gen.calls != 1 || judge.calls != 1 {
		t.Fatalf("result=%+v calls=%d/%d", result, gen.calls, judge.calls)
	}
	if !reflect.DeepEqual(before, a.Index.Chunks) {
		t.Fatal("pipeline mutated index/chunks")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Answer != result.Answer || decoded.Sources[0].ChunkID != "a" {
		t.Fatal("JSON round trip lost fields")
	}
}

func TestRepairOnceThenSuccess(t *testing.T) {
	a, _, gen, _ := testAgent(1, "not json", structuredJSON())
	result, err := a.Ask(context.Background(), "q", Grounded)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusAnswered || gen.calls != 2 || len(result.ValidationAttempts) != 2 || result.ValidationAttempts[0].Valid || !result.ValidationAttempts[1].Valid {
		t.Fatalf("result=%+v calls=%d", result, gen.calls)
	}
	if !strings.Contains(gen.prompts[1], "malformed_json") {
		t.Fatal("repair prompt lacks concrete validation error")
	}
}

func TestRepeatedValidationFailureIsSafeRefusal(t *testing.T) {
	a, _, gen, judge := testAgent(1, "bad", "still bad")
	result, err := a.Ask(context.Background(), "q", Strict)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusInsufficientContext || result.AbstentionReason != ReasonValidationFailed || gen.calls != 2 || judge.calls != 0 || !strings.Contains(result.Answer, "Не знаю") || result.ClarificationQuestion == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestLowRelevanceBoundaryAndNoGeneratorCall(t *testing.T) {
	a, _, gen, _ := testAgent(.6, structuredJSON())
	snapshot, err := a.Retrieve(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	a.Pipeline.AnswerMinRelevance = snapshot.Score
	result, err := a.Ask(context.Background(), "q", Strict)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusAnswered {
		t.Fatalf("score==threshold should pass: %+v", result)
	}
	a.Pipeline.AnswerMinRelevance = snapshot.Score + .0001
	result, err = a.Ask(context.Background(), "q", Strict)
	if err != nil {
		t.Fatal(err)
	}
	if result.AbstentionReason != ReasonLowRelevance || result.AnswerModelCalled || gen.calls != 1 || !strings.Contains(result.Answer, "Не знаю") || result.ClarificationQuestion == "" {
		t.Fatalf("low relevance result=%+v calls=%d", result, gen.calls)
	}
}

func TestEmptyContextAndRuntimeReasons(t *testing.T) {
	a, _, gen, _ := testAgent(1, structuredJSON())
	a.Pipeline.MinSimilarity = 1
	a.Index.Chunks[0].Embedding = []float64{.8, .6}
	result, err := a.Ask(context.Background(), "q", Strict)
	if err != nil {
		t.Fatal(err)
	}
	if result.AbstentionReason != ReasonEmptyContext || gen.calls != 0 {
		t.Fatalf("result=%+v", result)
	}
	a, _, gen, _ = testAgent(1, structuredJSON())
	gen.fail = errors.New("offline")
	result, err = a.Ask(context.Background(), "q", Grounded)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusError || result.AbstentionReason != ReasonRuntimeError || result.Error == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRetrieveMatchesDay23FilteredAndRenumbers(t *testing.T) {
	a, _, _, _ := testAgent(1, structuredJSON())
	snapshot, err := a.Retrieve(context.Background(), "artifact atomic save")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := retrieval.Search(*a.Index, []float64{1, 0}, a.Pipeline.CandidateK)
	if err != nil {
		t.Fatal(err)
	}
	ranked, err := reranking.Apply("artifact atomic save", direct, reranking.Config{MinSimilarity: a.Pipeline.MinSimilarity, Alpha: a.Pipeline.Alpha, Beta: a.Pipeline.Beta, Gamma: a.Pipeline.Gamma, FinalK: a.Pipeline.FinalK})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.FinalContext, ranked.Final) || snapshot.FinalContext[0].Rank != 1 {
		t.Fatalf("snapshot=%+v direct=%+v", snapshot.FinalContext, ranked.Final)
	}
}

func TestAskAllReusesEmbeddingRetrieval(t *testing.T) {
	a, embed, _, _ := testAgent(1, structuredJSON(), structuredJSON(), structuredJSON())
	results, err := a.AskAll(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || embed.calls != 1 {
		t.Fatalf("results=%d embedding calls=%d", len(results), embed.calls)
	}
}

func TestPromptsExcludeEvaluationGroundTruth(t *testing.T) {
	chunks := []retrieval.Result{{Rank: 1, Source: "a", Section: "s", ChunkID: "c", Text: "text", Score: .8}}
	prompt := StructuredPrompt("question", chunks, 20, 500)
	for _, forbidden := range []string{"expected_answer", "required_concepts", "expected_sources"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt leaks %s", forbidden)
		}
	}
}

func TestPipelineValidation(t *testing.T) {
	for _, cfg := range []PipelineConfig{{CandidateK: 0, FinalK: 1, Alpha: 1}, {CandidateK: 2, FinalK: 3, Alpha: 1}, {CandidateK: 2, FinalK: 1, MinSimilarity: .5, AnswerMinRelevance: 1.1, Alpha: 1}, {CandidateK: 2, FinalK: 1, MinSimilarity: .5, Alpha: -1}} {
		if cfg.Validate() == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}
