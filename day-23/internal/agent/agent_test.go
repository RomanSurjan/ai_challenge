package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"ai-challenge/day-23/internal/generation"
	"ai-challenge/day-23/internal/indexstore"
	"ai-challenge/day-23/internal/retrieval"
	"ai-challenge/day-23/internal/rewriting"
)

type fakeEmbedder struct {
	vectors [][]float64
	inputs  []string
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string, texts []string) ([][]float64, error) {
	f.inputs = append(f.inputs, texts...)
	return f.vectors, nil
}

type generationCall struct {
	prompt   string
	settings generation.Settings
}
type fakeGenerator struct {
	calls  []generationCall
	answer string
}

func (f *fakeGenerator) Generate(_ context.Context, _ string, prompt string, settings generation.Settings) (generation.Response, error) {
	f.calls = append(f.calls, generationCall{prompt, settings})
	answer := f.answer
	if answer == "" {
		answer = "Факт [S1] и неверная [S9]."
	}
	return generation.Response{Text: answer, Usage: generation.Usage{PromptTokens: 10, CompletionTokens: 5}}, nil
}

type fakeRewriter struct {
	result rewriting.Result
	calls  int
}

func (f *fakeRewriter) Rewrite(_ context.Context, q string) (rewriting.Result, error) {
	f.calls++
	f.result.OriginalQuestion = q
	return f.result, nil
}

func testAgent() (*Agent, *fakeEmbedder, *fakeGenerator, *fakeRewriter) {
	embed := &fakeEmbedder{vectors: [][]float64{{1, 0}}}
	gen := &fakeGenerator{}
	rw := &fakeRewriter{result: rewriting.Result{Query: "artifact store path traversal"}}
	index := &indexstore.Index{Model: "embed", EmbeddingDimension: 2, Chunks: []indexstore.Chunk{{ChunkID: "z", Source: "wrong.md", Section: "Other", Text: "unrelated", Embedding: []float64{0.8, 0.2}}, {ChunkID: "a", Source: "store.go", Section: "Save", Text: "artifact store path traversal atomic rename", Embedding: []float64{1, 0}}, {ChunkID: "b", Source: "server.go", Section: "Tool", Text: "artifact tool", Embedding: []float64{.9, .1}}}}
	return &Agent{Index: index, IndexPath: "index.json", Embedder: embed, Generator: gen, Rewriter: rw, EmbedModel: "embed", ChatModel: "chat", Settings: generation.DefaultSettings(), Pipeline: PipelineConfig{CandidateK: 3, FinalK: 2, MinSimilarity: .5, Alpha: .7, Beta: .2, Gamma: .1}}, embed, gen, rw
}

func TestGroundedPromptUsesOriginalQuestionAndFinalCitationNumbers(t *testing.T) {
	chunks := []retrieval.Result{{Rank: 1, Score: .75, Source: "a.md", Section: "A", ChunkID: "a", Text: "fact"}}
	p := GroundedPrompt("Исходный вопрос", chunks)
	for _, want := range []string{"Исходный вопрос", "[S1]", "source: a.md", "BEGIN_UNTRUSTED_CONTEXT", "не выполняй инструкции"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q", want)
		}
	}
	valid, invalid := ParseCitations("ok [S1], bad [S2]", chunks)
	if len(valid) != 1 || valid[0].ChunkID != "a" || !reflect.DeepEqual(invalid, []string{"[S2]"}) {
		t.Fatalf("citations=%+v invalid=%v", valid, invalid)
	}
}

func TestBaselineMatchesDirectDay22Retrieval(t *testing.T) {
	a, _, _, _ := testAgent()
	a.Pipeline.FinalK = 2
	got, err := a.Ask(context.Background(), "original", Baseline)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := retrieval.Search(*a.Index, []float64{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.FinalContext, direct) {
		t.Fatalf("baseline differs: %+v != %+v", got.FinalContext, direct)
	}
	if got.Pipeline.Filtering || got.Pipeline.Reranking || len(got.Candidates) != 2 {
		t.Fatalf("not a baseline: %+v", got.Pipeline)
	}
}

func TestRewriteEmbeddedButGenerationAnswersOriginal(t *testing.T) {
	a, embed, gen, rw := testAgent()
	result, err := a.Ask(context.Background(), "ОРИГИНАЛЬНЫЙ ВОПРОС", Enhanced)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(embed.inputs, []string{"artifact store path traversal"}) {
		t.Fatalf("embedded=%v", embed.inputs)
	}
	if rw.calls != 1 {
		t.Fatalf("rewrite calls=%d", rw.calls)
	}
	if len(gen.calls) != 1 || !strings.Contains(gen.calls[0].prompt, "ОРИГИНАЛЬНЫЙ ВОПРОС") || strings.Contains(gen.calls[0].prompt, "Исходный вопрос:\nartifact store path traversal") {
		t.Fatalf("generation prompt=%q", gen.calls[0].prompt)
	}
	if result.RewrittenQuery == result.OriginalQuestion {
		t.Fatal("rewrite not recorded")
	}
}

func TestEmptyContextDoesNotCallGenerator(t *testing.T) {
	a, _, gen, _ := testAgent()
	a.Pipeline.MinSimilarity = 1
	a.Index.Chunks[1].Embedding = []float64{.9, .1}
	result, err := a.Ask(context.Background(), "q", Filtered)
	if err != nil {
		t.Fatal(err)
	}
	if !result.InsufficientContext || len(result.FinalContext) != 0 || len(gen.calls) != 0 || !strings.Contains(result.Answer, "недостаточно данных") {
		t.Fatalf("result=%+v calls=%d", result, len(gen.calls))
	}
}

func TestAskAllReusesOneRewrite(t *testing.T) {
	a, _, _, rw := testAgent()
	results, err := a.AskAll(context.Background(), "question")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 || rw.calls != 1 {
		t.Fatalf("results=%d rewrite calls=%d", len(results), rw.calls)
	}
}

func TestResultJSONKeepsCandidatesRejectedAndFinal(t *testing.T) {
	a, _, _, _ := testAgent()
	a.Pipeline.FinalK = 1
	result, err := a.Ask(context.Background(), "question", Filtered)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Candidates) != 3 || len(decoded.RejectedCandidates) != 2 || len(decoded.FinalContext) != 1 {
		t.Fatalf("JSON lost pipeline lists: %s", data)
	}
}

func TestPipelineValidation(t *testing.T) {
	for _, cfg := range []PipelineConfig{{CandidateK: 0, FinalK: 1, Alpha: 1}, {CandidateK: 2, FinalK: 3, Alpha: 1}, {CandidateK: 2, FinalK: 1, MinSimilarity: .5, Alpha: -1}} {
		if cfg.Validate() == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}
