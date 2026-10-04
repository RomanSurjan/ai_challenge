package evaluation

import (
	"encoding/json"
	"reflect"
	"testing"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/generation"
	"ai-challenge/day-25/internal/judging"
	"ai-challenge/day-25/internal/reranking"
	"ai-challenge/day-25/internal/retrieval"
)

func TestConceptAndSourceMetrics(t *testing.T) {
	concepts := []RequiredConcept{{Name: "routing", Alternatives: []string{"ТАБЛИЦА   ВЛАДЕЛЬЦЕВ"}}, {Name: "session", Alternatives: []string{"MCP-сессия"}}, {Name: "missing", Alternatives: []string{"нет такого"}}}
	score := ConceptCoverage("Используется таблица\nвладельцев и MCP-СЕССИЯ.", concepts)
	if score.Matched != 2 || score.Total != 3 || score.All || len(score.Missing) != 1 {
		t.Fatalf("score=%+v", score)
	}
	if got := sourceRecall([]string{"a", "b", "c"}, []string{"b", "b", "c", "x"}); got != 2.0/3.0 {
		t.Fatalf("recall=%v", got)
	}
	if got := sourcePrecision([]string{"a", "b"}, []string{"b", "x"}); got != .5 {
		t.Fatalf("precision=%v", got)
	}
}

func TestSummarizeSourceQuoteEntailmentAndCostMetrics(t *testing.T) {
	r := &agent.Result{Status: agent.StatusAnswered, Gate: agent.Gate{Score: .8}, Candidates: []reranking.Candidate{{Source: "a"}}, FinalContext: []retrieval.Result{{Rank: 1, Source: "a", ChunkID: "c"}}, Sources: []evidence.Source{{ID: "S1", Source: "a", Section: "sec", ChunkID: "c"}}, Claims: []evidence.Claim{{ID: "C1", SourceIDs: []string{"S1"}}}, Citations: []evidence.Citation{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: "long exact quote", ExactMatch: true}}, Entailment: []judging.Result{{ClaimID: "C1", Verdict: judging.Supported}}, Timing: agent.Timing{RetrievalMS: 2, FilterRerankMS: 1, GenerationMS: 7, ValidationMS: 1, JudgeMS: 3, TotalMS: 14}, Tokens: agent.TokenUsage{Generator: generation.Usage{PromptTokens: 10, CompletionTokens: 4}, Judge: generation.Usage{PromptTokens: 5, CompletionTokens: 2}}, ContextRunes: 50}
	mr := ModeResult{Result: r, Concepts: ConceptScore{Coverage: .5}, Retrieval: RetrievalScore{CandidateRecall: 1, FinalRecall: .5, FinalPrecision: 1, CitedRecall: .5, HitAt1: 1, MRR: 1}}
	modes := map[agent.Mode]ModeResult{}
	for _, mode := range Modes {
		modes[mode] = mr
	}
	s := Summarize([]QuestionResult{{Modes: modes}})[agent.Strict]
	if s.Answered != 1 || s.AnsweredWithSourceRate != 1 || s.SourceMetadataCompletenessRate != 1 || s.ValidFinalContextSourceIDRate != 1 || s.AnsweredWithQuoteRate != 1 || s.ClaimsWithQuoteRate != 1 || s.ExactSubstringQuoteRate != 1 || s.ClaimsWithSourceIDsRate != 1 || s.ClaimsWithValidatedEvidenceRate != 1 || s.SupportedClaims != 1 || s.FullySupportedAnswerRate != 1 || s.AverageTotalMS != 14 || s.MRR != 1 {
		t.Fatalf("summary=%+v", s)
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	report := Report{ChatModel: "chat", Summary: map[agent.Mode]ModeMetrics{agent.Strict: {ConceptCoverage: .5}}, Results: []QuestionResult{{ID: "q", Modes: map[agent.Mode]ModeResult{agent.Strict: {Result: &agent.Result{Mode: agent.Strict, Status: agent.StatusInsufficientContext, Citations: []evidence.Citation{}, InvalidSourceIDs: []string{}}}}}}}
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

func TestGatePointFormulaAndTieBreakInputs(t *testing.T) {
	tp, fn, tn, fp := 8, 2, 5, 1
	point := GatePoint{PositiveAnswerRate: ratio(tp, tp+fn), FalseRefusalRate: ratio(fn, tp+fn), NegativeAbstentionRate: ratio(tn, tn+fp), UnsafeAnswerRate: ratio(fp, tn+fp), Precision: ratio(tp, tp+fp), Recall: ratio(tp, tp+fn)}
	point.F1 = 2 * point.Precision * point.Recall / (point.Precision + point.Recall)
	if point.FalseRefusalRate != .2 || point.Precision <= .8 || point.F1 <= .8 {
		t.Fatalf("point=%+v", point)
	}
}
