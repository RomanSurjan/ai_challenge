package evaluation

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"ai-challenge/day-23/internal/agent"
	"ai-challenge/day-23/internal/reranking"
	"ai-challenge/day-23/internal/retrieval"
)

func TestConceptCoverageUnicodeLowercaseAndWhitespace(t *testing.T) {
	concepts := []RequiredConcept{{Name: "routing", Alternatives: []string{"ТАБЛИЦА   ВЛАДЕЛЬЦЕВ"}}, {Name: "session", Alternatives: []string{"MCP-сессия"}}, {Name: "missing", Alternatives: []string{"нет такого"}}}
	score := ConceptCoverage("Используется таблица\nвладельцев и MCP-СЕССИЯ.", concepts)
	if score.Matched != 2 || score.Total != 3 || score.All || len(score.Missing) != 1 {
		t.Fatalf("score=%+v", score)
	}
}
func TestSourceMetrics(t *testing.T) {
	if got := SourceRecall([]string{"a", "b", "c"}, []string{"b", "b", "c", "x"}); got != 2.0/3.0 {
		t.Fatalf("recall=%v", got)
	}
	if got := SourcePrecision([]string{"a", "b"}, []string{"b", "x"}); got != .5 {
		t.Fatalf("precision=%v", got)
	}
}

func TestSummarizeRetrievalAndCostMetrics(t *testing.T) {
	r := &agent.Result{Candidates: []reranking.Candidate{{Source: "a", PassedThreshold: true}, {Source: "b", PassedThreshold: false}}, RejectedCandidates: []reranking.Candidate{{Source: "b"}}, FinalContext: []retrieval.Result{{Rank: 1, Source: "a"}}, Citations: []agent.Citation{{Source: "a"}}, Generation: agent.GenerationInfo{PromptTokens: 10, CompletionTokens: 4, ContextRunes: 50}, Timing: agent.Timing{RetrievalMS: 2, FilterRerankMS: 1, GenerationMS: 7, TotalMS: 10}}
	mr := ModeResult{Result: r, Concepts: ConceptScore{Coverage: .5}, Retrieval: RetrievalScore{CandidateRecall: 1, FinalRecall: .5, FinalPrecision: 1, CitedRecall: .5, HitAt1: 1, MRR: 1, RankChange: 1, RankChangeMeasured: true}}
	results := []QuestionResult{{Modes: map[agent.Mode]ModeResult{agent.Baseline: mr, agent.Filtered: mr, agent.Rewritten: mr, agent.Enhanced: mr}}}
	s := Summarize(results)[agent.Enhanced]
	if s.FinalPrecision != 1 || s.RejectedFraction != .5 || s.AverageTotalMS != 10 || s.MRR != 1 || s.AverageExpectedRankChange != 1 {
		t.Fatalf("summary=%+v", s)
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	report := Report{ChatModel: "chat", Summary: map[agent.Mode]ModeMetrics{agent.Baseline: {ConceptCoverage: .5}}, Results: []QuestionResult{{ID: "q", Modes: map[agent.Mode]ModeResult{agent.Baseline: {Result: &agent.Result{Mode: agent.Baseline, Citations: []agent.Citation{}, InvalidCitations: []string{}}}}}}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, decoded) {
		t.Fatalf("round trip mismatch: %+v != %+v", report, decoded)
	}
}

func TestSelectThresholdRule(t *testing.T) {
	values := []SweepMetrics{{Threshold: .3, ExpectedSourceRecall: 1, ExpectedSourcePrecision: .2, MRR: .5}, {Threshold: .4, ExpectedSourceRecall: .96, ExpectedSourcePrecision: .4, MRR: .6}, {Threshold: .5, ExpectedSourceRecall: .8, ExpectedSourcePrecision: .9, MRR: .9}}
	if got := selectThreshold(values); math.Abs(got-.4) > 1e-12 {
		t.Fatalf("selected=%v", got)
	}
}
