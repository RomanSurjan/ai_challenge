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
	"unicode/utf8"

	"ai-challenge/day-23/internal/agent"
	"ai-challenge/day-23/internal/embedding"
	"ai-challenge/day-23/internal/indexstore"
	"ai-challenge/day-23/internal/reranking"
	"ai-challenge/day-23/internal/retrieval"
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
type ConceptScore struct {
	Matched  int      `json:"matched"`
	Total    int      `json:"total"`
	Coverage float64  `json:"coverage"`
	All      bool     `json:"all"`
	Missing  []string `json:"missing"`
}

type RetrievalScore struct {
	CandidateRecall    float64 `json:"expected_source_recall_candidate_k"`
	FinalRecall        float64 `json:"expected_source_recall_after_filtering"`
	FinalPrecision     float64 `json:"expected_source_precision_after_filtering"`
	CitedRecall        float64 `json:"cited_source_recall"`
	HitAt1             float64 `json:"hit_at_1"`
	MRR                float64 `json:"mrr"`
	RankChange         float64 `json:"expected_source_rank_change"`
	RankChangeMeasured bool    `json:"rank_change_measured"`
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
	ConceptCoverage           float64 `json:"concept_coverage"`
	AllConceptsRate           float64 `json:"all_concepts_rate"`
	CandidateRecall           float64 `json:"expected_source_recall_candidate_k"`
	FinalRecall               float64 `json:"expected_source_recall_after_filtering"`
	FinalPrecision            float64 `json:"expected_source_precision_after_filtering"`
	CitedRecall               float64 `json:"cited_source_recall"`
	HitAt1                    float64 `json:"hit_at_1"`
	MRR                       float64 `json:"mrr"`
	AverageCandidatesBefore   float64 `json:"average_candidates_before"`
	AverageContextAfter       float64 `json:"average_context_after"`
	RejectedFraction          float64 `json:"rejected_fraction"`
	EmptyContextRate          float64 `json:"empty_context_rate"`
	AverageExpectedRankChange float64 `json:"average_expected_source_rank_change"`
	AverageRewriteMS          float64 `json:"average_rewrite_ms"`
	AverageRetrievalMS        float64 `json:"average_retrieval_ms"`
	AverageFilterRerankMS     float64 `json:"average_filter_rerank_ms"`
	AverageGenerationMS       float64 `json:"average_generation_ms"`
	AverageTotalMS            float64 `json:"average_total_ms"`
	AveragePromptTokens       float64 `json:"average_prompt_tokens"`
	AverageCompletionTokens   float64 `json:"average_completion_tokens"`
	AverageContextRunes       float64 `json:"average_context_runes"`
	AverageAnswerRunes        float64 `json:"average_answer_runes"`
	RewriteFallbacks          int     `json:"rewrite_fallbacks"`
	InvalidCitations          int     `json:"invalid_citations"`
	Errors                    int     `json:"errors"`
}

type Report struct {
	GeneratedAt    time.Time                  `json:"generated_at"`
	ChatModel      string                     `json:"chat_model"`
	RewriteModel   string                     `json:"rewrite_model"`
	EmbeddingModel string                     `json:"embedding_model"`
	Index          string                     `json:"index"`
	IndexCorpusID  string                     `json:"index_corpus_id"`
	Dataset        string                     `json:"dataset"`
	DatasetSHA256  string                     `json:"dataset_sha256"`
	Pipeline       agent.PipelineConfig       `json:"pipeline"`
	Temperature    float64                    `json:"temperature"`
	MaxTokens      int                        `json:"max_tokens"`
	Results        []QuestionResult           `json:"results"`
	Summary        map[agent.Mode]ModeMetrics `json:"summary"`
}

var Modes = []agent.Mode{agent.Baseline, agent.Filtered, agent.Rewritten, agent.Enhanced}

func LoadQuestions(path string) ([]Question, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open evaluation questions: %w", err)
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var questions []Question
	if err := decoder.Decode(&questions); err != nil {
		return nil, fmt.Errorf("decode evaluation questions: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode evaluation questions: unexpected trailing JSON")
	}
	if len(questions) < 10 {
		return nil, fmt.Errorf("evaluation requires at least 10 questions, got %d", len(questions))
	}
	ids := make(map[string]bool)
	for i, q := range questions {
		if strings.TrimSpace(q.ID) == "" || strings.TrimSpace(q.Question) == "" || strings.TrimSpace(q.ExpectedAnswer) == "" || len(q.RequiredConcepts) == 0 || len(q.ExpectedSources) == 0 {
			return nil, fmt.Errorf("question %d has incomplete ground truth", i)
		}
		if ids[q.ID] {
			return nil, fmt.Errorf("duplicate question ID %q", q.ID)
		}
		ids[q.ID] = true
		for _, c := range q.RequiredConcepts {
			if strings.TrimSpace(c.Name) == "" || len(c.Alternatives) == 0 {
				return nil, fmt.Errorf("question %s has invalid required concept", q.ID)
			}
		}
	}
	return questions, nil
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
	score := ConceptScore{Total: len(concepts), Missing: []string{}}
	normalized := normalize(answer)
	for _, c := range concepts {
		matched := false
		for _, a := range c.Alternatives {
			if candidate := normalize(a); candidate != "" && strings.Contains(normalized, candidate) {
				matched = true
				break
			}
		}
		if matched {
			score.Matched++
		} else {
			score.Missing = append(score.Missing, c.Name)
		}
	}
	if score.Total > 0 {
		score.Coverage = float64(score.Matched) / float64(score.Total)
	}
	score.All = score.Total > 0 && score.Matched == score.Total
	return score
}

func sourceSet(values []string) map[string]bool {
	result := make(map[string]bool)
	for _, v := range values {
		result[v] = true
	}
	return result
}
func SourceRecall(expected, actual []string) float64 {
	if len(expected) == 0 {
		return 0
	}
	want := sourceSet(expected)
	seen := make(map[string]bool)
	matched := 0
	for _, v := range actual {
		if want[v] && !seen[v] {
			matched++
			seen[v] = true
		}
	}
	return float64(matched) / float64(len(want))
}
func SourcePrecision(expected, actual []string) float64 {
	if len(actual) == 0 {
		return 0
	}
	want := sourceSet(expected)
	matched := 0
	for _, v := range actual {
		if want[v] {
			matched++
		}
	}
	return float64(matched) / float64(len(actual))
}

func scoreRetrieval(expected []string, result agent.Result) RetrievalScore {
	candidateSources := make([]string, 0, len(result.Candidates))
	for _, c := range result.Candidates {
		candidateSources = append(candidateSources, c.Source)
	}
	finalSources := make([]string, 0, len(result.FinalContext))
	for _, c := range result.FinalContext {
		finalSources = append(finalSources, c.Source)
	}
	cited := make([]string, 0, len(result.Citations))
	for _, c := range result.Citations {
		cited = append(cited, c.Source)
	}
	score := RetrievalScore{CandidateRecall: SourceRecall(expected, candidateSources), FinalRecall: SourceRecall(expected, finalSources), FinalPrecision: SourcePrecision(expected, finalSources), CitedRecall: SourceRecall(expected, cited)}
	want := sourceSet(expected)
	firstBefore, firstAfter := 0, 0
	for _, c := range result.Candidates {
		if want[c.Source] {
			firstBefore = c.RankBefore
			break
		}
	}
	for _, c := range result.FinalContext {
		if want[c.Source] {
			firstAfter = c.Rank
			break
		}
	}
	if firstAfter > 0 {
		score.HitAt1 = boolFloat(firstAfter == 1)
		score.MRR = 1 / float64(firstAfter)
	}
	if firstBefore > 0 && firstAfter > 0 {
		score.RankChange = float64(firstBefore - firstAfter)
		score.RankChangeMeasured = true
	}
	return score
}
func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func Run(ctx context.Context, runner *agent.Agent, questions []Question, metadata Report) Report {
	metadata.GeneratedAt = time.Now().UTC()
	metadata.Results = make([]QuestionResult, 0, len(questions))
	for _, q := range questions {
		item := QuestionResult{ID: q.ID, Question: q.Question, ExpectedAnswer: q.ExpectedAnswer, RequiredConcepts: q.RequiredConcepts, ExpectedSources: q.ExpectedSources, Modes: make(map[agent.Mode]ModeResult)}
		rewrite := runner.PrepareRewrite(ctx, q.Question)
		for _, mode := range Modes {
			var result agent.Result
			var err error
			if mode.UsesRewrite() {
				result, err = runner.AskPrepared(ctx, q.Question, mode, &rewrite)
			} else {
				result, err = runner.AskPrepared(ctx, q.Question, mode, nil)
			}
			if err != nil {
				item.Modes[mode] = ModeResult{Error: err.Error()}
				continue
			}
			item.Modes[mode] = ModeResult{Result: &result, Concepts: ConceptCoverage(result.Answer, q.RequiredConcepts), Retrieval: scoreRetrieval(q.ExpectedSources, result)}
		}
		metadata.Results = append(metadata.Results, item)
	}
	metadata.Summary = Summarize(metadata.Results)
	return metadata
}

func Summarize(results []QuestionResult) map[agent.Mode]ModeMetrics {
	summary := make(map[agent.Mode]ModeMetrics, len(Modes))
	for _, mode := range Modes {
		var m ModeMetrics
		successes, rankCount, totalCandidates, thresholdRejected := 0, 0, 0, 0
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
			m.CandidateRecall += mr.Retrieval.CandidateRecall
			m.FinalRecall += mr.Retrieval.FinalRecall
			m.FinalPrecision += mr.Retrieval.FinalPrecision
			m.CitedRecall += mr.Retrieval.CitedRecall
			m.HitAt1 += mr.Retrieval.HitAt1
			m.MRR += mr.Retrieval.MRR
			if mr.Retrieval.RankChangeMeasured {
				m.AverageExpectedRankChange += mr.Retrieval.RankChange
				rankCount++
			}
			totalCandidates += len(r.Candidates)
			for _, candidate := range r.Candidates {
				if !candidate.PassedThreshold {
					thresholdRejected++
				}
			}
			m.AverageCandidatesBefore += float64(len(r.Candidates))
			m.AverageContextAfter += float64(len(r.FinalContext))
			if r.InsufficientContext {
				m.EmptyContextRate++
			}
			if mode.UsesRewrite() {
				m.AverageRewriteMS += float64(r.Rewrite.LatencyMS)
				if r.Rewrite.Fallback {
					m.RewriteFallbacks++
				}
			}
			m.AverageRetrievalMS += float64(r.Timing.RetrievalMS)
			m.AverageFilterRerankMS += float64(r.Timing.FilterRerankMS)
			m.AverageGenerationMS += float64(r.Timing.GenerationMS)
			m.AverageTotalMS += float64(r.Timing.TotalMS)
			m.AveragePromptTokens += float64(r.Generation.PromptTokens)
			m.AverageCompletionTokens += float64(r.Generation.CompletionTokens)
			m.AverageContextRunes += float64(r.Generation.ContextRunes)
			m.AverageAnswerRunes += float64(utf8.RuneCountInString(r.Answer))
			m.InvalidCitations += len(r.InvalidCitations)
		}
		if successes > 0 {
			d := float64(successes)
			m.ConceptCoverage /= d
			m.AllConceptsRate /= d
			m.CandidateRecall /= d
			m.FinalRecall /= d
			m.FinalPrecision /= d
			m.CitedRecall /= d
			m.HitAt1 /= d
			m.MRR /= d
			m.AverageCandidatesBefore /= d
			m.AverageContextAfter /= d
			m.EmptyContextRate /= d
			m.AverageRewriteMS /= d
			m.AverageRetrievalMS /= d
			m.AverageFilterRerankMS /= d
			m.AverageGenerationMS /= d
			m.AverageTotalMS /= d
			m.AveragePromptTokens /= d
			m.AverageCompletionTokens /= d
			m.AverageContextRunes /= d
			m.AverageAnswerRunes /= d
		}
		if totalCandidates > 0 {
			m.RejectedFraction = float64(thresholdRejected) / float64(totalCandidates)
		}
		if rankCount > 0 {
			m.AverageExpectedRankChange /= float64(rankCount)
		}
		summary[mode] = m
	}
	return summary
}

type SweepMetrics struct {
	Threshold               float64 `json:"threshold"`
	ExpectedSourceRecall    float64 `json:"expected_source_recall_after_filtering"`
	ExpectedSourcePrecision float64 `json:"expected_source_precision"`
	HitAt1                  float64 `json:"hit_at_1"`
	MRR                     float64 `json:"mrr"`
	AverageRemaining        float64 `json:"average_remaining_chunks"`
	RejectedFraction        float64 `json:"rejected_fraction"`
	EmptyContextRate        float64 `json:"empty_context_rate"`
}
type SweepReport struct {
	GeneratedAt       time.Time      `json:"generated_at"`
	Index             string         `json:"index"`
	EmbeddingModel    string         `json:"embedding_model"`
	Dataset           string         `json:"dataset"`
	DatasetSHA256     string         `json:"dataset_sha256"`
	CandidateK        int            `json:"candidate_k"`
	FinalK            int            `json:"final_k"`
	Alpha             float64        `json:"alpha"`
	Beta              float64        `json:"beta"`
	Gamma             float64        `json:"gamma"`
	Values            []SweepMetrics `json:"values"`
	SelectedThreshold float64        `json:"selected_threshold"`
	SelectionRule     string         `json:"selection_rule"`
}

func Sweep(ctx context.Context, index indexstore.Index, indexPath string, embedder embedding.Embedder, model string, questions []Question, dataset, datasetSHA string, candidateK, finalK int, alpha, beta, gamma float64, thresholds []float64) (SweepReport, error) {
	if candidateK <= 0 || finalK <= 0 || finalK > candidateK {
		return SweepReport{}, fmt.Errorf("candidate-k and final-k must be positive and final-k <= candidate-k")
	}
	type cached struct {
		q Question
		c []retrieval.Result
	}
	cache := make([]cached, 0, len(questions))
	for _, q := range questions {
		v, err := embedder.Embed(ctx, model, []string{q.Question})
		if err != nil {
			return SweepReport{}, fmt.Errorf("embed %s: %w", q.ID, err)
		}
		if len(v) != 1 {
			return SweepReport{}, fmt.Errorf("embed %s returned %d vectors", q.ID, len(v))
		}
		c, err := retrieval.Search(index, v[0], candidateK)
		if err != nil {
			return SweepReport{}, err
		}
		cache = append(cache, cached{q: q, c: c})
	}
	report := SweepReport{GeneratedAt: time.Now().UTC(), Index: indexPath, EmbeddingModel: model, Dataset: dataset, DatasetSHA256: datasetSHA, CandidateK: candidateK, FinalK: finalK, Alpha: alpha, Beta: beta, Gamma: gamma, SelectionRule: "highest MRR among thresholds retaining at least 95% of maximum recall; ties: precision, lower empty-context rate, then higher threshold"}
	for _, threshold := range thresholds {
		var m SweepMetrics
		m.Threshold = threshold
		totalBefore, thresholdRejected := 0, 0
		for _, item := range cache {
			out, err := reranking.Apply(item.q.Question, item.c, reranking.Config{MinSimilarity: threshold, Alpha: alpha, Beta: beta, Gamma: gamma, FinalK: finalK})
			if err != nil {
				return SweepReport{}, err
			}
			sources := make([]string, 0, len(out.Final))
			for _, r := range out.Final {
				sources = append(sources, r.Source)
			}
			m.ExpectedSourceRecall += SourceRecall(item.q.ExpectedSources, sources)
			m.ExpectedSourcePrecision += SourcePrecision(item.q.ExpectedSources, sources)
			if len(sources) == 0 {
				m.EmptyContextRate++
			} else {
				want := sourceSet(item.q.ExpectedSources)
				for i, s := range sources {
					if want[s] {
						if i == 0 {
							m.HitAt1++
						}
						m.MRR += 1 / float64(i+1)
						break
					}
				}
			}
			m.AverageRemaining += float64(len(out.Final))
			totalBefore += len(out.Candidates)
			for _, candidate := range out.Candidates {
				if !candidate.PassedThreshold {
					thresholdRejected++
				}
			}
		}
		d := float64(len(cache))
		if d > 0 {
			m.ExpectedSourceRecall /= d
			m.ExpectedSourcePrecision /= d
			m.HitAt1 /= d
			m.MRR /= d
			m.AverageRemaining /= d
			m.EmptyContextRate /= d
		}
		if totalBefore > 0 {
			m.RejectedFraction = float64(thresholdRejected) / float64(totalBefore)
		}
		report.Values = append(report.Values, m)
	}
	report.SelectedThreshold = selectThreshold(report.Values)
	return report, nil
}

func selectThreshold(values []SweepMetrics) float64 {
	if len(values) == 0 {
		return 0
	}
	maxRecall := 0.0
	for _, v := range values {
		if v.ExpectedSourceRecall > maxRecall {
			maxRecall = v.ExpectedSourceRecall
		}
	}
	eligible := append([]SweepMetrics(nil), values...)
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		ai, bi := a.ExpectedSourceRecall >= .95*maxRecall, b.ExpectedSourceRecall >= .95*maxRecall
		if ai != bi {
			return ai
		}
		if a.MRR != b.MRR {
			return a.MRR > b.MRR
		}
		if a.ExpectedSourcePrecision != b.ExpectedSourcePrecision {
			return a.ExpectedSourcePrecision > b.ExpectedSourcePrecision
		}
		if a.EmptyContextRate != b.EmptyContextRate {
			return a.EmptyContextRate < b.EmptyContextRate
		}
		return a.Threshold > b.Threshold
	})
	return eligible[0].Threshold
}

func Save(outDir string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := atomicWrite(filepath.Join(outDir, "evaluation.json"), data); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(outDir, "comparison.md"), []byte(Markdown(report)))
}
func SaveSweep(outDir string, report SweepReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := atomicWrite(filepath.Join(outDir, "threshold-sweep.json"), data); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(outDir, "threshold-sweep.md"), []byte(SweepMarkdown(report)))
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".day23-*.tmp")
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

func SweepMarkdown(r SweepReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Day 23 — threshold sweep\n\nRun: `%s`  \nDataset: `%s` (`%s`)  \nIndex: `%s`  \nCandidate-K: %d, final-K: %d, weights: %.2f / %.2f / %.2f\n\n", r.GeneratedAt.Format(time.RFC3339), r.Dataset, r.DatasetSHA256, r.Index, r.CandidateK, r.FinalK, r.Alpha, r.Beta, r.Gamma)
	b.WriteString("| Threshold | Recall | Precision | Hit@1 | MRR | Avg kept | Rejected | Empty |\n|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, v := range r.Values {
		fmt.Fprintf(&b, "| %.2f | %.3f | %.3f | %.3f | %.3f | %.2f | %.3f | %.3f |\n", v.Threshold, v.ExpectedSourceRecall, v.ExpectedSourcePrecision, v.HitAt1, v.MRR, v.AverageRemaining, v.RejectedFraction, v.EmptyContextRate)
	}
	fmt.Fprintf(&b, "\nSelected threshold: **%.2f**. Rule: %s. Sweep is retrieval-only: answer generation was not called.\n", r.SelectedThreshold, r.SelectionRule)
	return b.String()
}

func Markdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Day 23 — filter, reranker и query rewrite\n\nRun: `%s`  \nModels: chat `%s`, rewrite `%s`, embedding `%s`  \nIndex: `%s` (corpus `%s`)  \nDataset: `%s`, SHA-256 `%s`  \nPipeline: candidate-K %d, final-K %d, threshold %.2f, weights %.2f / %.2f / %.2f\n\n", r.GeneratedAt.Format(time.RFC3339), r.ChatModel, r.RewriteModel, r.EmbeddingModel, r.Index, r.IndexCorpusID, r.Dataset, r.DatasetSHA256, r.Pipeline.CandidateK, r.Pipeline.FinalK, r.Pipeline.MinSimilarity, r.Pipeline.Alpha, r.Pipeline.Beta, r.Pipeline.Gamma)
	b.WriteString("## Overall\n\n| Mode | Concept coverage | All concepts | Cited recall | Invalid | Errors | Gen ms | Total ms | Prompt / completion tokens | Context runes |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, m := range Modes {
		s := r.Summary[m]
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f | %d | %d | %.1f | %.1f | %.1f / %.1f | %.1f |\n", m, s.ConceptCoverage, s.AllConceptsRate, s.CitedRecall, s.InvalidCitations, s.Errors, s.AverageGenerationMS, s.AverageTotalMS, s.AveragePromptTokens, s.AverageCompletionTokens, s.AverageContextRunes)
	}
	b.WriteString("\n## Retrieval before and after\n\n| Mode | Candidate recall | Final recall | Final precision | Hit@1 | MRR | Before / after | Threshold rejected | Empty | Rank Δ | Rewrite ms | Retrieval ms | Rerank ms |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, m := range Modes {
		s := r.Summary[m]
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f | %.3f | %.3f | %.1f / %.1f | %.3f | %.3f | %+.2f | %.1f | %.1f | %.1f |\n", m, s.CandidateRecall, s.FinalRecall, s.FinalPrecision, s.HitAt1, s.MRR, s.AverageCandidatesBefore, s.AverageContextAfter, s.RejectedFraction, s.EmptyContextRate, s.AverageExpectedRankChange, s.AverageRewriteMS, s.AverageRetrievalMS, s.AverageFilterRerankMS)
	}
	b.WriteString("\n## Per question\n\n| ID | Baseline concepts | Filtered | Rewritten | Enhanced | Baseline / enhanced final recall | Baseline / enhanced MRR |\n|---|---:|---:|---:|---:|---:|---:|\n")
	for _, q := range r.Results {
		a, bm := q.Modes[agent.Baseline], q.Modes[agent.Enhanced]
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f | %.3f | %.3f / %.3f | %.3f / %.3f |\n", q.ID, a.Concepts.Coverage, q.Modes[agent.Filtered].Concepts.Coverage, q.Modes[agent.Rewritten].Concepts.Coverage, bm.Concepts.Coverage, a.Retrieval.FinalRecall, bm.Retrieval.FinalRecall, a.Retrieval.MRR, bm.Retrieval.MRR)
	}
	for _, idx := range exampleIndexes(r.Results) {
		q := r.Results[idx]
		base, enh := q.Modes[agent.Baseline], q.Modes[agent.Enhanced]
		fmt.Fprintf(&b, "\n### %s — %s\n\nOriginal: %s\n\nRewritten: `%s` (fallback: %t", q.ID, q.Question, q.Question, enh.Result.RewrittenQuery, enh.Result.Rewrite.Fallback)
		if enh.Result.Rewrite.Error != "" {
			fmt.Fprintf(&b, ", error: %s", enh.Result.Rewrite.Error)
		}
		b.WriteString(")\n\nCandidates before filtering:\n\n")
		for _, c := range enh.Result.Candidates {
			fmt.Fprintf(&b, "- #%d cosine `%.4f`, lexical `%.3f`, metadata `%.3f`, rerank `%.4f`: `%s` — %s\n", c.RankBefore, c.CosineScore, c.LexicalScore, c.MetadataScore, c.RerankScore, c.Source, c.Section)
		}
		b.WriteString("\nRejected / omitted:\n\n")
		if len(enh.Result.RejectedCandidates) == 0 {
			b.WriteString("- None.\n")
		} else {
			for _, c := range enh.Result.RejectedCandidates {
				fmt.Fprintf(&b, "- #%d `%s`: %s\n", c.RankBefore, c.Source, c.ExcludedReason)
			}
		}
		b.WriteString("\nFinal order:\n\n")
		for _, c := range enh.Result.FinalContext {
			fmt.Fprintf(&b, "- S%d cosine `%.4f`: `%s` — %s\n", c.Rank, c.Score, c.Source, c.Section)
		}
		fmt.Fprintf(&b, "\nBaseline answer (concepts %.3f, citations %s):\n\n%s\n\nEnhanced answer (concepts %.3f, citations %s):\n\n%s\n\nMetrics: final recall %.3f → %.3f; precision %.3f → %.3f; MRR %.3f → %.3f.\n", base.Concepts.Coverage, citationIDs(base.Result), base.Result.Answer, enh.Concepts.Coverage, citationIDs(enh.Result), enh.Result.Answer, base.Retrieval.FinalRecall, enh.Retrieval.FinalRecall, base.Retrieval.FinalPrecision, enh.Retrieval.FinalPrecision, base.Retrieval.MRR, enh.Retrieval.MRR)
	}
	base, enh := r.Summary[agent.Baseline], r.Summary[agent.Enhanced]
	delta := enh.ConceptCoverage - base.ConceptCoverage
	b.WriteString("\n## Honest conclusion\n\n")
	if delta > 0 {
		fmt.Fprintf(&b, "Enhanced improved concept coverage by **%.3f** on this run.", delta)
	} else if delta < 0 {
		fmt.Fprintf(&b, "Enhanced reduced concept coverage by **%.3f** on this run; filtering/rewrite are not an automatic win.", -delta)
	} else {
		b.WriteString("Enhanced and baseline tied on concept coverage in this run.")
	}
	fmt.Fprintf(&b, " Final expected-source precision changed %.3f → %.3f, recall %.3f → %.3f, and average total latency %.1f → %.1f ms. These deterministic metrics are useful for comparison but do not prove semantic correctness.\n", base.FinalPrecision, enh.FinalPrecision, base.FinalRecall, enh.FinalRecall, base.AverageTotalMS, enh.AverageTotalMS)
	return b.String()
}

func citationIDs(r *agent.Result) string {
	if r == nil || len(r.Citations) == 0 {
		return "none"
	}
	ids := make([]string, 0, len(r.Citations))
	for _, c := range r.Citations {
		ids = append(ids, c.ID+"="+c.Source)
	}
	return strings.Join(ids, ", ")
}
func exampleIndexes(results []QuestionResult) []int {
	if len(results) <= 3 {
		out := make([]int, len(results))
		for i := range out {
			out[i] = i
		}
		return out
	}
	best, worst := 0, 0
	bestDelta, worstDelta := -2.0, 2.0
	for i, q := range results {
		d := q.Modes[agent.Enhanced].Concepts.Coverage - q.Modes[agent.Baseline].Concepts.Coverage
		if d > bestDelta {
			bestDelta = d
			best = i
		}
		if d < worstDelta {
			worstDelta = d
			worst = i
		}
	}
	third := 0
	for third == best || third == worst {
		third++
	}
	out := []int{best, worst, third}
	sort.Ints(out)
	return out
}
