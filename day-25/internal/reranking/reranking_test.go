package reranking

import (
	"reflect"
	"testing"

	"ai-challenge/day-25/internal/retrieval"
)

func TestUnicodeTokensAndScores(t *testing.T) {
	tokens := Tokens("Как АТОМАРНЫЙ claim сохраняет tool_call_id и path42?")
	for _, want := range []string{"атомарный", "claim", "tool", "call", "path42"} {
		if _, ok := tokens[want]; !ok {
			t.Errorf("missing token %q in %v", want, tokens)
		}
	}
	if _, ok := tokens["как"]; ok {
		t.Fatal("stop token retained")
	}
	lex, meta := Scores("artifact store path", "Store validates path traversal", "day-20/artifact/store.go Save")
	if lex <= 0 || meta <= 0 {
		t.Fatalf("scores=%v/%v", lex, meta)
	}
}

func TestThresholdBoundaryAndStableChunkIDTieBreak(t *testing.T) {
	input := []retrieval.Result{{Rank: 1, Score: .5, ChunkID: "z", Source: "s", Section: "x", Text: "same"}, {Rank: 2, Score: .5, ChunkID: "a", Source: "s", Section: "x", Text: "same"}, {Rank: 3, Score: .499, ChunkID: "b", Source: "s", Section: "x", Text: "same"}}
	out, err := Apply("same", input, Config{MinSimilarity: .5, Alpha: 1, FinalK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Final) != 2 || out.Final[0].ChunkID != "a" || out.Final[1].ChunkID != "z" {
		t.Fatalf("final=%+v", out.Final)
	}
	if len(out.Rejected) != 1 || out.Rejected[0].ChunkID != "b" || out.Candidates[0].PassedThreshold != true {
		t.Fatalf("output=%+v", out)
	}
}

func TestCandidateAndFinalKAndNoMutation(t *testing.T) {
	input := []retrieval.Result{{Rank: 1, Score: .9, ChunkID: "a", Source: "a", Section: "A", Text: "query"}, {Rank: 2, Score: .8, ChunkID: "b", Source: "b", Section: "B", Text: "query"}, {Rank: 3, Score: .7, ChunkID: "c", Source: "c", Section: "C", Text: "query"}}
	before := append([]retrieval.Result(nil), input...)
	out, err := Apply("query", input, Config{MinSimilarity: 0, Alpha: 1, FinalK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 3 || len(out.Final) != 1 || len(out.Rejected) != 2 {
		t.Fatalf("out=%+v", out)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("reranker mutated retrieval input")
	}
	for _, r := range out.Rejected {
		if r.ExcludedReason == "" {
			t.Fatal("missing exclusion reason")
		}
	}
}

func TestAllFilteredHasNoFallback(t *testing.T) {
	out, err := Apply("q", []retrieval.Result{{Rank: 1, Score: .2, ChunkID: "a", Source: "a", Section: "A", Text: "q"}}, Config{MinSimilarity: .3, Alpha: 1, FinalK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Final) != 0 || len(out.Rejected) != 1 {
		t.Fatalf("hidden fallback: %+v", out)
	}
}
