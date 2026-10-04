package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/judging"
)

type RequiredConcept struct {
	Name         string   `json:"name"`
	Alternatives []string `json:"alternatives"`
}
type Question struct {
	ID               string            `json:"id"`
	Question         string            `json:"question"`
	ExpectedAnswer   string            `json:"expected_answer"`
	RequiredConcepts []RequiredConcept `json:"required_concepts"`
	ExpectedSources  []string          `json:"expected_sources"`
}
type AbstentionQuestion struct {
	ID             string `json:"id"`
	Question       string `json:"question"`
	ExpectedStatus string `json:"expected_status"`
}
type ConceptScore struct {
	Matched  int      `json:"matched"`
	Total    int      `json:"total"`
	Coverage float64  `json:"coverage"`
	All      bool     `json:"all"`
	Missing  []string `json:"missing"`
}
type RetrievalScore struct {
	CandidateRecall float64 `json:"candidate_source_recall"`
	FinalRecall     float64 `json:"final_source_recall"`
	FinalPrecision  float64 `json:"final_source_precision"`
	CitedRecall     float64 `json:"cited_source_recall"`
	HitAt1          float64 `json:"hit_at_1"`
	MRR             float64 `json:"mrr"`
}
type ModeResult struct {
	Result    *agent.Result  `json:"result,omitempty"`
	Concepts  ConceptScore   `json:"concepts"`
	Retrieval RetrievalScore `json:"retrieval"`
	Error     string         `json:"error,omitempty"`
}
type QuestionResult struct {
	ID               string                    `json:"id"`
	Question         string                    `json:"question"`
	ExpectedAnswer   string                    `json:"expected_answer"`
	RequiredConcepts []RequiredConcept         `json:"required_concepts"`
	ExpectedSources  []string                  `json:"expected_sources"`
	Modes            map[agent.Mode]ModeResult `json:"modes"`
}

type ModeMetrics struct {
	ConceptCoverage                  float64 `json:"concept_coverage"`
	AllConceptsRate                  float64 `json:"all_required_concepts_rate"`
	Answered                         int     `json:"answered"`
	Abstained                        int     `json:"abstained"`
	Errors                           int     `json:"errors"`
	FalseRefusalRate                 float64 `json:"false_refusal_rate"`
	AnsweredWithSourceRate           float64 `json:"answered_with_source_rate"`
	SourceMetadataCompletenessRate   float64 `json:"source_metadata_completeness_rate"`
	ValidFinalContextSourceIDRate    float64 `json:"valid_final_context_source_id_rate"`
	CitedSourceRecall                float64 `json:"cited_source_recall"`
	InvalidSourceIDs                 int     `json:"invalid_source_ids"`
	DuplicateSources                 int     `json:"duplicate_sources"`
	AnsweredWithQuoteRate            float64 `json:"answered_with_quote_rate"`
	ClaimsWithQuoteRate              float64 `json:"claims_with_quote_rate"`
	ExactSubstringQuoteRate          float64 `json:"exact_substring_quote_rate"`
	InvalidQuotes                    int     `json:"invalid_quotes"`
	WrongChunkQuotes                 int     `json:"wrong_chunk_quotes"`
	TooShortQuotes                   int     `json:"too_short_quotes"`
	TooLongQuotes                    int     `json:"too_long_quotes"`
	AverageQuotesPerAnswer           float64 `json:"average_quotes_per_answer"`
	ClaimsWithSourceIDsRate          float64 `json:"claims_with_source_ids_rate"`
	ClaimsWithValidatedEvidenceRate  float64 `json:"claims_with_validated_evidence_rate"`
	SupportedClaims                  int     `json:"supported_claims"`
	PartiallySupportedClaims         int     `json:"partially_supported_claims"`
	UnsupportedClaims                int     `json:"unsupported_claims"`
	UnverifiableClaims               int     `json:"unverifiable_claims"`
	FullySupportedAnswerRate         float64 `json:"fully_supported_answer_rate"`
	CandidateRecall                  float64 `json:"candidate_source_recall"`
	FinalRecall                      float64 `json:"final_source_recall"`
	FinalPrecision                   float64 `json:"final_source_precision"`
	HitAt1                           float64 `json:"hit_at_1"`
	MRR                              float64 `json:"mrr"`
	AverageTop1Cosine                float64 `json:"average_top_1_cosine"`
	AverageFinalContextSize          float64 `json:"average_final_context_size"`
	AverageRetrievalMS               float64 `json:"average_retrieval_ms"`
	AverageFilterRerankMS            float64 `json:"average_filter_rerank_ms"`
	AverageGenerationMS              float64 `json:"average_generation_ms"`
	AverageValidationMS              float64 `json:"average_validation_ms"`
	AverageJudgeMS                   float64 `json:"average_judge_ms"`
	AverageTotalMS                   float64 `json:"average_total_ms"`
	AverageGeneratorPromptTokens     float64 `json:"average_generator_prompt_tokens"`
	AverageGeneratorCompletionTokens float64 `json:"average_generator_completion_tokens"`
	AverageJudgePromptTokens         float64 `json:"average_judge_prompt_tokens"`
	AverageJudgeCompletionTokens     float64 `json:"average_judge_completion_tokens"`
	AverageContextRunes              float64 `json:"average_context_runes"`
}

type Report struct {
	GeneratedAt    time.Time                  `json:"generated_at"`
	ChatModel      string                     `json:"chat_model"`
	JudgeModel     string                     `json:"judge_model"`
	EmbeddingModel string                     `json:"embedding_model"`
	Index          string                     `json:"index"`
	IndexCorpusID  string                     `json:"index_corpus_id"`
	Dataset        string                     `json:"dataset"`
	DatasetSHA256  string                     `json:"dataset_sha256"`
	Pipeline       agent.PipelineConfig       `json:"pipeline"`
	QuoteMinRunes  int                        `json:"quote_min_runes"`
	QuoteMaxRunes  int                        `json:"quote_max_runes"`
	Temperature    float64                    `json:"temperature"`
	MaxTokens      int                        `json:"max_tokens"`
	JudgeMaxTokens int                        `json:"judge_max_tokens"`
	Results        []QuestionResult           `json:"results"`
	Summary        map[agent.Mode]ModeMetrics `json:"summary"`
}

var Modes = []agent.Mode{agent.Day23, agent.Grounded, agent.Strict}

func LoadQuestions(path string) ([]Question, error) {
	var questions []Question
	if err := decodeFile(path, &questions); err != nil {
		return nil, fmt.Errorf("load evaluation questions: %w", err)
	}
	if len(questions) != 10 {
		return nil, fmt.Errorf("evaluation requires exactly 10 questions, got %d", len(questions))
	}
	ids := map[string]bool{}
	for i, q := range questions {
		if strings.TrimSpace(q.ID) == "" || strings.TrimSpace(q.Question) == "" || strings.TrimSpace(q.ExpectedAnswer) == "" || len(q.RequiredConcepts) == 0 || len(q.ExpectedSources) == 0 {
			return nil, fmt.Errorf("question %d has incomplete ground truth", i)
		}
		if ids[q.ID] {
			return nil, fmt.Errorf("duplicate question ID %q", q.ID)
		}
		ids[q.ID] = true
	}
	return questions, nil
}

func LoadAbstentionQuestions(path string) ([]AbstentionQuestion, error) {
	var questions []AbstentionQuestion
	if err := decodeFile(path, &questions); err != nil {
		return nil, fmt.Errorf("load abstention questions: %w", err)
	}
	if len(questions) < 5 {
		return nil, fmt.Errorf("abstention evaluation requires at least 5 questions")
	}
	ids := map[string]bool{}
	for i, q := range questions {
		if strings.TrimSpace(q.ID) == "" || strings.TrimSpace(q.Question) == "" || q.ExpectedStatus != agent.StatusInsufficientContext {
			return nil, fmt.Errorf("abstention question %d is invalid", i)
		}
		if ids[q.ID] {
			return nil, fmt.Errorf("duplicate abstention question ID %q", q.ID)
		}
		ids[q.ID] = true
	}
	return questions, nil
}

func decodeFile(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func DatasetSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func normalize(value string) string { return strings.Join(strings.Fields(strings.ToLower(value)), " ") }
func ConceptCoverage(answer string, concepts []RequiredConcept) ConceptScore {
	s := ConceptScore{Total: len(concepts), Missing: []string{}}
	answer = normalize(answer)
	for _, concept := range concepts {
		matched := false
		for _, alternative := range concept.Alternatives {
			if value := normalize(alternative); value != "" && strings.Contains(answer, value) {
				matched = true
				break
			}
		}
		if matched {
			s.Matched++
		} else {
			s.Missing = append(s.Missing, concept.Name)
		}
	}
	if s.Total > 0 {
		s.Coverage = float64(s.Matched) / float64(s.Total)
	}
	s.All = s.Total > 0 && s.Matched == s.Total
	return s
}

func set(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
func sourceRecall(expected, actual []string) float64 {
	if len(expected) == 0 {
		return 0
	}
	want, seen, matched := set(expected), map[string]bool{}, 0
	for _, value := range actual {
		if want[value] && !seen[value] {
			matched++
			seen[value] = true
		}
	}
	return float64(matched) / float64(len(want))
}
func sourcePrecision(expected, actual []string) float64 {
	if len(actual) == 0 {
		return 0
	}
	want, matched := set(expected), 0
	for _, value := range actual {
		if want[value] {
			matched++
		}
	}
	return float64(matched) / float64(len(actual))
}
func retrievalScore(expected []string, result agent.Result) RetrievalScore {
	candidate, final, cited := []string{}, []string{}, []string{}
	for _, c := range result.Candidates {
		candidate = append(candidate, c.Source)
	}
	for _, c := range result.FinalContext {
		final = append(final, c.Source)
	}
	for _, c := range result.Sources {
		cited = append(cited, c.Source)
	}
	s := RetrievalScore{CandidateRecall: sourceRecall(expected, candidate), FinalRecall: sourceRecall(expected, final), FinalPrecision: sourcePrecision(expected, final), CitedRecall: sourceRecall(expected, cited)}
	want := set(expected)
	for i, source := range final {
		if want[source] {
			if i == 0 {
				s.HitAt1 = 1
			}
			s.MRR = 1 / float64(i+1)
			break
		}
	}
	return s
}

func Run(ctx context.Context, runner *agent.Agent, questions []Question, metadata Report) Report {
	metadata.GeneratedAt = time.Now().UTC()
	metadata.Results = make([]QuestionResult, 0, len(questions))
	for _, q := range questions {
		item := QuestionResult{ID: q.ID, Question: q.Question, ExpectedAnswer: q.ExpectedAnswer, RequiredConcepts: q.RequiredConcepts, ExpectedSources: q.ExpectedSources, Modes: map[agent.Mode]ModeResult{}}
		results, err := runner.AskAll(ctx, q.Question)
		if err != nil {
			for _, mode := range Modes {
				item.Modes[mode] = ModeResult{Error: err.Error()}
			}
			metadata.Results = append(metadata.Results, item)
			continue
		}
		for _, result := range results {
			copyResult := result
			item.Modes[result.Mode] = ModeResult{Result: &copyResult, Concepts: ConceptCoverage(result.Answer, q.RequiredConcepts), Retrieval: retrievalScore(q.ExpectedSources, result)}
		}
		metadata.Results = append(metadata.Results, item)
	}
	metadata.Summary = Summarize(metadata.Results)
	return metadata
}

func Summarize(results []QuestionResult) map[agent.Mode]ModeMetrics {
	summary := map[agent.Mode]ModeMetrics{}
	for _, mode := range Modes {
		var m ModeMetrics
		successes, totalSources, completeSources, validSourceIDs, totalCitations, exactCitations, totalClaims, claimsWithSource, claimsWithQuote, fullySupported, answeredWithSource, answeredWithQuote, totalQuotes := 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
		for _, item := range results {
			mr, ok := item.Modes[mode]
			if !ok || mr.Error != "" || mr.Result == nil {
				m.Errors++
				continue
			}
			r := mr.Result
			successes++
			m.ConceptCoverage += mr.Concepts.Coverage
			if mr.Concepts.All {
				m.AllConceptsRate++
			}
			if r.Status == agent.StatusAnswered {
				m.Answered++
				if len(r.Sources) > 0 {
					answeredWithSource++
				}
				if len(r.Citations) > 0 && r.Citations[0].Quote != "" {
					answeredWithQuote++
				}
				for _, citation := range r.Citations {
					if citation.Quote != "" {
						totalQuotes++
					}
				}
			} else if r.Status == agent.StatusInsufficientContext {
				m.Abstained++
			} else {
				m.Errors++
			}
			m.CandidateRecall += mr.Retrieval.CandidateRecall
			m.FinalRecall += mr.Retrieval.FinalRecall
			m.FinalPrecision += mr.Retrieval.FinalPrecision
			m.CitedSourceRecall += mr.Retrieval.CitedRecall
			m.HitAt1 += mr.Retrieval.HitAt1
			m.MRR += mr.Retrieval.MRR
			m.AverageTop1Cosine += r.Gate.Score
			m.AverageFinalContextSize += float64(len(r.FinalContext))
			m.InvalidSourceIDs += len(r.InvalidSourceIDs)
			m.InvalidQuotes += len(r.InvalidQuotes)
			seenSource := map[string]bool{}
			finalIDs := map[string]bool{}
			for i := range r.FinalContext {
				finalIDs[fmt.Sprintf("S%d", i+1)] = true
			}
			for _, source := range r.Sources {
				totalSources++
				if source.ID != "" && source.Source != "" && source.Section != "" && source.ChunkID != "" {
					completeSources++
				}
				if seenSource[source.ID] {
					m.DuplicateSources++
				}
				seenSource[source.ID] = true
				if finalIDs[source.ID] { /* numerator tracked below */
				}
			}
			for _, source := range r.Sources {
				if finalIDs[source.ID] {
					validSourceIDs++
				}
			}
			for _, citation := range r.Citations {
				totalCitations++
				if citation.ExactMatch {
					exactCitations++
				}
			}
			quotedClaims := map[string]bool{}
			for _, citation := range r.Citations {
				for _, id := range citation.ClaimIDs {
					quotedClaims[id] = true
				}
			}
			for _, claim := range r.Claims {
				totalClaims++
				if len(claim.SourceIDs) > 0 {
					claimsWithSource++
				}
				if quotedClaims[claim.ID] {
					claimsWithQuote++
				}
			}
			allSupported := len(r.Claims) > 0
			for _, verdict := range r.Entailment {
				switch verdict.Verdict {
				case judging.Supported:
					m.SupportedClaims++
				case judging.PartiallySupported:
					m.PartiallySupportedClaims++
					allSupported = false
				case judging.Unsupported:
					m.UnsupportedClaims++
					allSupported = false
				default:
					m.UnverifiableClaims++
					allSupported = false
				}
			}
			if r.Status == agent.StatusAnswered && allSupported {
				fullySupported++
			}
			for _, attempt := range r.ValidationAttempts {
				if attempt.Valid {
					continue
				}
				for _, validationError := range attempt.Errors {
					switch validationError.Code {
					case "quote_wrong_chunk":
						m.WrongChunkQuotes++
					case "quote_too_short":
						m.TooShortQuotes++
					case "quote_too_long":
						m.TooLongQuotes++
					}
				}
			}
			m.AverageRetrievalMS += float64(r.Timing.RetrievalMS)
			m.AverageFilterRerankMS += float64(r.Timing.FilterRerankMS)
			m.AverageGenerationMS += float64(r.Timing.GenerationMS)
			m.AverageValidationMS += float64(r.Timing.ValidationMS)
			m.AverageJudgeMS += float64(r.Timing.JudgeMS)
			m.AverageTotalMS += float64(r.Timing.TotalMS)
			m.AverageGeneratorPromptTokens += float64(r.Tokens.Generator.PromptTokens)
			m.AverageGeneratorCompletionTokens += float64(r.Tokens.Generator.CompletionTokens)
			m.AverageJudgePromptTokens += float64(r.Tokens.Judge.PromptTokens)
			m.AverageJudgeCompletionTokens += float64(r.Tokens.Judge.CompletionTokens)
			m.AverageContextRunes += float64(r.ContextRunes)
		}
		if successes > 0 {
			d := float64(successes)
			m.ConceptCoverage /= d
			m.AllConceptsRate /= d
			m.FalseRefusalRate = float64(m.Abstained) / d
			m.CandidateRecall /= d
			m.FinalRecall /= d
			m.FinalPrecision /= d
			m.CitedSourceRecall /= d
			m.HitAt1 /= d
			m.MRR /= d
			m.AverageTop1Cosine /= d
			m.AverageFinalContextSize /= d
			m.AverageRetrievalMS /= d
			m.AverageFilterRerankMS /= d
			m.AverageGenerationMS /= d
			m.AverageValidationMS /= d
			m.AverageJudgeMS /= d
			m.AverageTotalMS /= d
			m.AverageGeneratorPromptTokens /= d
			m.AverageGeneratorCompletionTokens /= d
			m.AverageJudgePromptTokens /= d
			m.AverageJudgeCompletionTokens /= d
			m.AverageContextRunes /= d
		}
		if m.Answered > 0 {
			d := float64(m.Answered)
			m.AnsweredWithSourceRate = float64(answeredWithSource) / d
			m.AnsweredWithQuoteRate = float64(answeredWithQuote) / d
			m.AverageQuotesPerAnswer = float64(totalQuotes) / d
			m.FullySupportedAnswerRate = float64(fullySupported) / d
		}
		if totalSources > 0 {
			m.SourceMetadataCompletenessRate = float64(completeSources) / float64(totalSources)
			m.ValidFinalContextSourceIDRate = float64(validSourceIDs) / float64(totalSources)
		}
		if totalCitations > 0 {
			m.ExactSubstringQuoteRate = float64(exactCitations) / float64(totalCitations)
		}
		if totalClaims > 0 {
			m.ClaimsWithSourceIDsRate = float64(claimsWithSource) / float64(totalClaims)
			m.ClaimsWithQuoteRate = float64(claimsWithQuote) / float64(totalClaims)
			m.ClaimsWithValidatedEvidenceRate = m.ClaimsWithQuoteRate
		}
		summary[mode] = m
	}
	return summary
}

type CalibrationItem struct {
	ID                 string  `json:"id"`
	Question           string  `json:"question"`
	ExpectedSufficient bool    `json:"expected_sufficient"`
	Score              float64 `json:"score"`
	FinalContextSize   int     `json:"final_context_size"`
}
type GatePoint struct {
	Threshold              float64 `json:"threshold"`
	PositiveAnswerRate     float64 `json:"positive_answer_rate"`
	FalseRefusalRate       float64 `json:"false_refusal_rate"`
	NegativeAbstentionRate float64 `json:"negative_abstention_rate"`
	UnsafeAnswerRate       float64 `json:"unsafe_answer_rate"`
	Precision              float64 `json:"precision"`
	Recall                 float64 `json:"recall"`
	F1                     float64 `json:"f1"`
}
type GateCalibrationReport struct {
	GeneratedAt           time.Time         `json:"generated_at"`
	PositiveDataset       string            `json:"positive_dataset"`
	PositiveDatasetSHA256 string            `json:"positive_dataset_sha256"`
	NegativeDataset       string            `json:"negative_dataset"`
	NegativeDatasetSHA256 string            `json:"negative_dataset_sha256"`
	ScoreFormula          string            `json:"score_formula"`
	SelectionRule         string            `json:"selection_rule"`
	Items                 []CalibrationItem `json:"items"`
	Values                []GatePoint       `json:"values"`
	SelectedThreshold     float64           `json:"selected_threshold"`
	SafetyCompromise      bool              `json:"safety_compromise"`
	AnswerModelCalls      int               `json:"answer_model_calls"`
}

func CalibrateGate(ctx context.Context, runner *agent.Agent, positives []Question, negatives []AbstentionQuestion, metadata GateCalibrationReport, thresholds []float64) (GateCalibrationReport, error) {
	metadata.GeneratedAt = time.Now().UTC()
	metadata.ScoreFormula = "maximum cosine among chunks in the Day 23 filtered final context; empty context scores 0; sufficient iff context is non-empty and score >= threshold"
	metadata.SelectionRule = "maximum F1 for sufficient-context classification with false-refusal rate <= 20%; ties: lower unsafe-answer rate, then higher threshold"
	metadata.AnswerModelCalls = 0
	for _, q := range positives {
		snapshot, err := runner.Retrieve(ctx, q.Question)
		if err != nil {
			return metadata, fmt.Errorf("retrieve positive %s: %w", q.ID, err)
		}
		metadata.Items = append(metadata.Items, CalibrationItem{ID: q.ID, Question: q.Question, ExpectedSufficient: true, Score: snapshot.Score, FinalContextSize: len(snapshot.FinalContext)})
	}
	for _, q := range negatives {
		snapshot, err := runner.Retrieve(ctx, q.Question)
		if err != nil {
			return metadata, fmt.Errorf("retrieve negative %s: %w", q.ID, err)
		}
		metadata.Items = append(metadata.Items, CalibrationItem{ID: q.ID, Question: q.Question, ExpectedSufficient: false, Score: snapshot.Score, FinalContextSize: len(snapshot.FinalContext)})
	}
	for _, threshold := range thresholds {
		tp, fn, tn, fp := 0, 0, 0, 0
		for _, item := range metadata.Items {
			predicted := item.FinalContextSize > 0 && item.Score >= threshold
			if item.ExpectedSufficient {
				if predicted {
					tp++
				} else {
					fn++
				}
			} else {
				if predicted {
					fp++
				} else {
					tn++
				}
			}
		}
		point := GatePoint{Threshold: threshold, PositiveAnswerRate: ratio(tp, tp+fn), FalseRefusalRate: ratio(fn, tp+fn), NegativeAbstentionRate: ratio(tn, tn+fp), UnsafeAnswerRate: ratio(fp, tn+fp), Precision: ratio(tp, tp+fp), Recall: ratio(tp, tp+fn)}
		if point.Precision+point.Recall > 0 {
			point.F1 = 2 * point.Precision * point.Recall / (point.Precision + point.Recall)
		}
		metadata.Values = append(metadata.Values, point)
	}
	eligible := make([]GatePoint, 0)
	for _, point := range metadata.Values {
		if point.FalseRefusalRate <= 0.20 {
			eligible = append(eligible, point)
		}
	}
	if len(eligible) == 0 {
		eligible = append(eligible, metadata.Values...)
		metadata.SafetyCompromise = true
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].F1 != eligible[j].F1 {
			return eligible[i].F1 > eligible[j].F1
		}
		if eligible[i].UnsafeAnswerRate != eligible[j].UnsafeAnswerRate {
			return eligible[i].UnsafeAnswerRate < eligible[j].UnsafeAnswerRate
		}
		return eligible[i].Threshold > eligible[j].Threshold
	})
	if len(eligible) > 0 {
		metadata.SelectedThreshold = eligible[0].Threshold
	}
	return metadata, nil
}
func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

type AbstentionResult struct {
	ID       string       `json:"id"`
	Question string       `json:"question"`
	Result   agent.Result `json:"result"`
}
type AbstentionSummary struct {
	Questions              int     `json:"questions"`
	AbstentionRate         float64 `json:"abstention_rate"`
	UnsafeAnsweredRate     float64 `json:"unsafe_answered_rate"`
	AnswerModelNotCalled   int     `json:"answer_model_not_called"`
	LowRelevance           int     `json:"low_relevance"`
	EmptyContext           int     `json:"empty_context"`
	OutputValidationFailed int     `json:"output_validation_failed"`
	RuntimeErrors          int     `json:"runtime_errors"`
	AverageLatencyMS       float64 `json:"average_latency_ms"`
}
type AbstentionReport struct {
	GeneratedAt     time.Time          `json:"generated_at"`
	Dataset         string             `json:"dataset"`
	DatasetSHA256   string             `json:"dataset_sha256"`
	AnswerThreshold float64            `json:"answer_threshold"`
	Results         []AbstentionResult `json:"results"`
	Summary         AbstentionSummary  `json:"summary"`
}

func RunAbstention(ctx context.Context, runner *agent.Agent, questions []AbstentionQuestion, metadata AbstentionReport) AbstentionReport {
	metadata.GeneratedAt = time.Now().UTC()
	metadata.Summary.Questions = len(questions)
	for _, q := range questions {
		result, err := runner.Ask(ctx, q.Question, agent.Strict)
		if err != nil {
			result = agent.Result{Mode: agent.Strict, Status: agent.StatusError, OriginalQuestion: q.Question, Error: err.Error(), AbstentionReason: agent.ReasonRuntimeError}
		}
		metadata.Results = append(metadata.Results, AbstentionResult{ID: q.ID, Question: q.Question, Result: result})
		if result.Status == agent.StatusInsufficientContext {
			metadata.Summary.AbstentionRate++
		}
		if result.Status == agent.StatusAnswered {
			metadata.Summary.UnsafeAnsweredRate++
		}
		if !result.AnswerModelCalled {
			metadata.Summary.AnswerModelNotCalled++
		}
		switch result.AbstentionReason {
		case agent.ReasonLowRelevance:
			metadata.Summary.LowRelevance++
		case agent.ReasonEmptyContext:
			metadata.Summary.EmptyContext++
		case agent.ReasonValidationFailed:
			metadata.Summary.OutputValidationFailed++
		case agent.ReasonRuntimeError:
			metadata.Summary.RuntimeErrors++
		}
		metadata.Summary.AverageLatencyMS += float64(result.Timing.TotalMS)
	}
	if len(questions) > 0 {
		d := float64(len(questions))
		metadata.Summary.AbstentionRate /= d
		metadata.Summary.UnsafeAnsweredRate /= d
		metadata.Summary.AverageLatencyMS /= d
	}
	return metadata
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".day24-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}

func Save(outDir string, report Report) error {
	if err := writeJSON(filepath.Join(outDir, "evaluation.json"), report); err != nil {
		return err
	}
	var abst *AbstentionReport
	if data, err := os.ReadFile(filepath.Join(outDir, "abstention-evaluation.json")); err == nil {
		var value AbstentionReport
		if json.Unmarshal(data, &value) == nil {
			abst = &value
		}
	}
	return atomicWrite(filepath.Join(outDir, "comparison.md"), []byte(Markdown(report, abst)))
}
func SaveCalibration(outDir string, report GateCalibrationReport) error {
	if err := writeJSON(filepath.Join(outDir, "gate-calibration.json"), report); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(outDir, "gate-calibration.md"), []byte(CalibrationMarkdown(report)))
}
func SaveAbstention(outDir string, report AbstentionReport) error {
	if err := writeJSON(filepath.Join(outDir, "abstention-evaluation.json"), report); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(outDir, "abstention-comparison.md"), []byte(AbstentionMarkdown(report))); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(outDir, "evaluation.json"))
	if err == nil {
		var main Report
		if json.Unmarshal(data, &main) == nil {
			return atomicWrite(filepath.Join(outDir, "comparison.md"), []byte(Markdown(main, &report)))
		}
	}
	return nil
}

var _ = evidence.StatusAnswered
