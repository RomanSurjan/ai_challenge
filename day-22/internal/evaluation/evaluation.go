package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ai-challenge/day-22/internal/agent"
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

type QuestionResult struct {
	ID                string            `json:"id"`
	Question          string            `json:"question"`
	ExpectedAnswer    string            `json:"expected_answer"`
	RequiredConcepts  []RequiredConcept `json:"required_concepts"`
	ExpectedSources   []string          `json:"expected_sources"`
	Plain             *agent.Result     `json:"plain,omitempty"`
	RAG               *agent.Result     `json:"rag,omitempty"`
	PlainConcepts     ConceptScore      `json:"plain_concepts"`
	RAGConcepts       ConceptScore      `json:"rag_concepts"`
	TopKSourceRecall  float64           `json:"top_k_source_recall"`
	CitedSourceRecall float64           `json:"cited_source_recall"`
	PlainError        string            `json:"plain_error,omitempty"`
	RAGError          string            `json:"rag_error,omitempty"`
}

type ModeMetrics struct {
	ConceptCoverage          float64 `json:"concept_coverage"`
	AllConceptsRate          float64 `json:"all_concepts_rate"`
	AverageGenerationMS      float64 `json:"average_generation_ms"`
	AverageAnswerLengthRunes float64 `json:"average_answer_length_runes"`
	AveragePromptTokens      float64 `json:"average_prompt_tokens"`
	AverageCompletionTokens  float64 `json:"average_completion_tokens"`
}

type RetrievalMetrics struct {
	ExpectedSourceRecallTopK      float64 `json:"expected_source_recall_top_k"`
	ExpectedSourceRecallCitations float64 `json:"expected_source_recall_citations"`
	InvalidCitations              int     `json:"invalid_citations"`
	AverageRetrievalMS            float64 `json:"average_retrieval_ms"`
}

type Summary struct {
	Questions int              `json:"questions"`
	Plain     ModeMetrics      `json:"plain"`
	RAG       ModeMetrics      `json:"rag"`
	Retrieval RetrievalMetrics `json:"retrieval"`
	Errors    int              `json:"errors"`
}

type Report struct {
	GeneratedAt    time.Time        `json:"generated_at"`
	ChatModel      string           `json:"chat_model"`
	EmbeddingModel string           `json:"embedding_model"`
	Index          string           `json:"index"`
	TopK           int              `json:"top_k"`
	Temperature    float64          `json:"temperature"`
	MaxTokens      int              `json:"max_tokens"`
	Results        []QuestionResult `json:"results"`
	Summary        Summary          `json:"summary"`
}

type Runner interface {
	Ask(ctx context.Context, question string, mode agent.Mode) (agent.Result, error)
}

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
	ids := make(map[string]bool, len(questions))
	for i, question := range questions {
		if strings.TrimSpace(question.ID) == "" || strings.TrimSpace(question.Question) == "" || strings.TrimSpace(question.ExpectedAnswer) == "" || len(question.RequiredConcepts) == 0 || len(question.ExpectedSources) == 0 {
			return nil, fmt.Errorf("question %d has incomplete ground truth", i)
		}
		if ids[question.ID] {
			return nil, fmt.Errorf("duplicate question ID %q", question.ID)
		}
		ids[question.ID] = true
		for _, concept := range question.RequiredConcepts {
			if strings.TrimSpace(concept.Name) == "" || len(concept.Alternatives) == 0 {
				return nil, fmt.Errorf("question %s has invalid required concept", question.ID)
			}
		}
	}
	return questions, nil
}

func Run(ctx context.Context, runner Runner, questions []Question, metadata Report) Report {
	metadata.GeneratedAt = time.Now().UTC()
	metadata.Results = make([]QuestionResult, 0, len(questions))
	for _, question := range questions {
		item := QuestionResult{
			ID: question.ID, Question: question.Question, ExpectedAnswer: question.ExpectedAnswer,
			RequiredConcepts: question.RequiredConcepts, ExpectedSources: question.ExpectedSources,
		}
		plain, err := runner.Ask(ctx, question.Question, agent.Plain)
		if err != nil {
			item.PlainError = err.Error()
		} else {
			item.Plain = &plain
			item.PlainConcepts = ConceptCoverage(plain.Answer, question.RequiredConcepts)
		}
		rag, err := runner.Ask(ctx, question.Question, agent.RAG)
		if err != nil {
			item.RAGError = err.Error()
		} else {
			item.RAG = &rag
			item.RAGConcepts = ConceptCoverage(rag.Answer, question.RequiredConcepts)
			var retrieved, cited []string
			if rag.Retrieval != nil {
				for _, chunk := range rag.Retrieval.Chunks {
					retrieved = append(retrieved, chunk.Source)
				}
			}
			for _, citation := range rag.Citations {
				cited = append(cited, citation.Source)
			}
			item.TopKSourceRecall = SourceRecall(question.ExpectedSources, retrieved)
			item.CitedSourceRecall = SourceRecall(question.ExpectedSources, cited)
		}
		metadata.Results = append(metadata.Results, item)
	}
	metadata.Summary = Summarize(metadata.Results)
	return metadata
}

func normalize(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func ConceptCoverage(answer string, concepts []RequiredConcept) ConceptScore {
	score := ConceptScore{Total: len(concepts), Missing: []string{}}
	normalized := normalize(answer)
	for _, concept := range concepts {
		matched := false
		for _, alternative := range concept.Alternatives {
			if candidate := normalize(alternative); candidate != "" && strings.Contains(normalized, candidate) {
				matched = true
				break
			}
		}
		if matched {
			score.Matched++
		} else {
			score.Missing = append(score.Missing, concept.Name)
		}
	}
	if score.Total > 0 {
		score.Coverage = float64(score.Matched) / float64(score.Total)
	}
	score.All = score.Total > 0 && score.Matched == score.Total
	return score
}

func SourceRecall(expected, actual []string) float64 {
	if len(expected) == 0 {
		return 0
	}
	present := make(map[string]bool, len(actual))
	for _, source := range actual {
		present[source] = true
	}
	matched := 0
	seen := make(map[string]bool, len(expected))
	for _, source := range expected {
		if seen[source] {
			continue
		}
		seen[source] = true
		if present[source] {
			matched++
		}
	}
	return float64(matched) / float64(len(seen))
}

func Summarize(results []QuestionResult) Summary {
	summary := Summary{Questions: len(results)}
	var plainCount, ragCount int
	for _, item := range results {
		if item.PlainError != "" {
			summary.Errors++
		}
		if item.RAGError != "" {
			summary.Errors++
		}
		if item.Plain != nil {
			plainCount++
			summary.Plain.ConceptCoverage += item.PlainConcepts.Coverage
			if item.PlainConcepts.All {
				summary.Plain.AllConceptsRate++
			}
			summary.Plain.AverageGenerationMS += float64(item.Plain.Generation.DurationMS)
			summary.Plain.AverageAnswerLengthRunes += float64(utf8.RuneCountInString(item.Plain.Answer))
			summary.Plain.AveragePromptTokens += float64(item.Plain.Generation.PromptTokens)
			summary.Plain.AverageCompletionTokens += float64(item.Plain.Generation.CompletionTokens)
		}
		if item.RAG != nil {
			ragCount++
			summary.RAG.ConceptCoverage += item.RAGConcepts.Coverage
			if item.RAGConcepts.All {
				summary.RAG.AllConceptsRate++
			}
			summary.RAG.AverageGenerationMS += float64(item.RAG.Generation.DurationMS)
			summary.RAG.AverageAnswerLengthRunes += float64(utf8.RuneCountInString(item.RAG.Answer))
			summary.RAG.AveragePromptTokens += float64(item.RAG.Generation.PromptTokens)
			summary.RAG.AverageCompletionTokens += float64(item.RAG.Generation.CompletionTokens)
			summary.Retrieval.ExpectedSourceRecallTopK += item.TopKSourceRecall
			summary.Retrieval.ExpectedSourceRecallCitations += item.CitedSourceRecall
			if item.RAG.Retrieval != nil {
				summary.Retrieval.AverageRetrievalMS += float64(item.RAG.Retrieval.DurationMS)
			}
			summary.Retrieval.InvalidCitations += len(item.RAG.InvalidCitations)
		}
	}
	averageMode(&summary.Plain, plainCount)
	averageMode(&summary.RAG, ragCount)
	if ragCount > 0 {
		count := float64(ragCount)
		summary.Retrieval.ExpectedSourceRecallTopK /= count
		summary.Retrieval.ExpectedSourceRecallCitations /= count
		summary.Retrieval.AverageRetrievalMS /= count
	}
	return summary
}

func averageMode(metrics *ModeMetrics, count int) {
	if count == 0 {
		return
	}
	value := float64(count)
	metrics.ConceptCoverage /= value
	metrics.AllConceptsRate /= value
	metrics.AverageGenerationMS /= value
	metrics.AverageAnswerLengthRunes /= value
	metrics.AveragePromptTokens /= value
	metrics.AverageCompletionTokens /= value
}

func Save(outDir string, report Report) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	jsonPath := filepath.Join(outDir, "evaluation.json")
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode evaluation report: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWrite(jsonPath, data); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(outDir, "comparison.md"), []byte(Markdown(report))); err != nil {
		return err
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".day22-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary artifact: %w", err)
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
		return fmt.Errorf("write artifact: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close artifact: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace artifact: %w", err)
	}
	ok = true
	return nil
}

func Markdown(report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Day 22 — фактическое сравнение plain и RAG\n\n")
	fmt.Fprintf(&b, "Запуск: `%s`  \nChat model: `%s`  \nEmbedding model: `%s`  \nIndex: `%s`  \nВопросов: %d, top-k: %d, temperature: %.2f, max tokens: %d\n\n",
		report.GeneratedAt.Format(time.RFC3339), report.ChatModel, report.EmbeddingModel, report.Index,
		report.Summary.Questions, report.TopK, report.Temperature, report.MaxTokens)
	b.WriteString("## Общие результаты\n\n")
	b.WriteString("| Метрика | Plain | RAG |\n|---|---:|---:|\n")
	fmt.Fprintf(&b, "| Coverage обязательных concepts | %.3f | %.3f |\n", report.Summary.Plain.ConceptCoverage, report.Summary.RAG.ConceptCoverage)
	fmt.Fprintf(&b, "| Ответы со всеми concepts | %.3f | %.3f |\n", report.Summary.Plain.AllConceptsRate, report.Summary.RAG.AllConceptsRate)
	fmt.Fprintf(&b, "| Средняя generation latency, ms | %.1f | %.1f |\n", report.Summary.Plain.AverageGenerationMS, report.Summary.RAG.AverageGenerationMS)
	fmt.Fprintf(&b, "| Средняя длина ответа, Unicode-символы | %.1f | %.1f |\n", report.Summary.Plain.AverageAnswerLengthRunes, report.Summary.RAG.AverageAnswerLengthRunes)
	fmt.Fprintf(&b, "| Средние prompt / completion tokens | %.1f / %.1f | %.1f / %.1f |\n\n", report.Summary.Plain.AveragePromptTokens, report.Summary.Plain.AverageCompletionTokens, report.Summary.RAG.AveragePromptTokens, report.Summary.RAG.AverageCompletionTokens)
	fmt.Fprintf(&b, "Expected-source recall в retrieved top-k: **%.3f**. В citations: **%.3f**. Invalid citations: **%d**. Средняя retrieval latency: **%.1f ms**. Ошибок запуска: **%d**.\n\n",
		report.Summary.Retrieval.ExpectedSourceRecallTopK, report.Summary.Retrieval.ExpectedSourceRecallCitations,
		report.Summary.Retrieval.InvalidCitations, report.Summary.Retrieval.AverageRetrievalMS, report.Summary.Errors)
	b.WriteString("Concept coverage — детерминированный Unicode-aware substring match после lowercase и нормализации пробелов. Он удобен для воспроизводимого сравнения, но не доказывает полную семантическую корректность ответа.\n\n")
	b.WriteString("## Результаты по вопросам\n\n")
	b.WriteString("| ID | Plain concepts | RAG concepts | Top-k source recall | Cited source recall | Invalid |\n|---|---:|---:|---:|---:|---:|\n")
	for _, item := range report.Results {
		invalid := 0
		if item.RAG != nil {
			invalid = len(item.RAG.InvalidCitations)
		}
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f | %.3f | %d |\n", item.ID, item.PlainConcepts.Coverage, item.RAGConcepts.Coverage, item.TopKSourceRecall, item.CitedSourceRecall, invalid)
	}
	for _, item := range report.Results {
		fmt.Fprintf(&b, "\n### %s — %s\n\n", item.ID, item.Question)
		fmt.Fprintf(&b, "Ожидание: %s\n\n", item.ExpectedAnswer)
		if item.PlainError != "" {
			fmt.Fprintf(&b, "**Plain error:** %s\n\n", item.PlainError)
		} else if item.Plain != nil {
			fmt.Fprintf(&b, "**Plain answer** (concepts %d/%d, %d ms):\n\n%s\n\n", item.PlainConcepts.Matched, item.PlainConcepts.Total, item.Plain.Generation.DurationMS, item.Plain.Answer)
		}
		if item.RAGError != "" {
			fmt.Fprintf(&b, "**RAG error:** %s\n\n", item.RAGError)
			continue
		}
		if item.RAG == nil {
			continue
		}
		fmt.Fprintf(&b, "**RAG answer** (concepts %d/%d, retrieval %d ms, generation %d ms):\n\n%s\n\n", item.RAGConcepts.Matched, item.RAGConcepts.Total, item.RAG.Retrieval.DurationMS, item.RAG.Generation.DurationMS, item.RAG.Answer)
		b.WriteString("Retrieved sources:\n\n")
		for _, chunk := range item.RAG.Retrieval.Chunks {
			fmt.Fprintf(&b, "- S%d, score `%.6f`: `%s` — %s — `%s`\n", chunk.Rank, chunk.Score, chunk.Source, chunk.Section, chunk.ChunkID)
		}
		b.WriteString("\nCited sources:\n\n")
		if len(item.RAG.Citations) == 0 {
			b.WriteString("- Нет валидных citations.\n")
		} else {
			for _, citation := range item.RAG.Citations {
				fmt.Fprintf(&b, "- %s: `%s` — %s — `%s`\n", citation.ID, citation.Source, citation.Section, citation.ChunkID)
			}
		}
		if len(item.RAG.InvalidCitations) > 0 {
			fmt.Fprintf(&b, "\nInvalid citations: `%s`.\n", strings.Join(item.RAG.InvalidCitations, "`, `"))
		}
	}
	b.WriteString("\n## Вывод\n\n")
	delta := report.Summary.RAG.ConceptCoverage - report.Summary.Plain.ConceptCoverage
	switch {
	case delta > 0:
		fmt.Fprintf(&b, "На этом наборе RAG повысил среднее substring concept coverage на %.3f. Это фактический результат данного запуска, а не доказательство общего превосходства RAG.\n", delta)
	case delta < 0:
		fmt.Fprintf(&b, "На этом наборе plain превысил RAG по среднему substring concept coverage на %.3f. Retrieval не гарантирует улучшение генерации и требует настройки.\n", -delta)
	default:
		b.WriteString("На этом наборе plain и RAG получили одинаковое среднее substring concept coverage; retrieval не дал измеримого преимущества по этой метрике.\n")
	}
	return b.String()
}
