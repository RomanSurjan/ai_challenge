package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-challenge/day-21/internal/embedding"
	"ai-challenge/day-21/internal/indexstore"
	"ai-challenge/day-21/internal/search"
)

type Question struct {
	ID               string   `json:"id"`
	Query            string   `json:"query"`
	ExpectedSources  []string `json:"expected_sources"`
	ExpectedSections []string `json:"expected_sections,omitempty"`
	Explanation      string   `json:"explanation"`
}

type Metrics struct {
	HitAt1                float64 `json:"hit_at_1"`
	HitAt5                float64 `json:"hit_at_5"`
	MRRAt5                float64 `json:"mrr_at_5"`
	AverageTop1Similarity float64 `json:"average_top_1_similarity"`
}

type IndexResult struct {
	Strategy          string  `json:"strategy"`
	Metrics           Metrics `json:"metrics"`
	Chunks            int     `json:"chunks"`
	MinChunkWords     int     `json:"min_chunk_words"`
	AverageChunkWords float64 `json:"average_chunk_words"`
	MaxChunkWords     int     `json:"max_chunk_words"`
	BuildDurationMS   int64   `json:"build_duration_ms"`
	JSONSizeBytes     int64   `json:"json_size_bytes"`
}

type Example struct {
	QuestionID string          `json:"question_id"`
	Query      string          `json:"query"`
	Fixed      []search.Result `json:"fixed"`
	Structural []search.Result `json:"structural"`
}

type Report struct {
	SchemaVersion string      `json:"schema_version"`
	GeneratedAt   time.Time   `json:"generated_at"`
	CorpusID      string      `json:"corpus_id"`
	Model         string      `json:"model"`
	TopK          int         `json:"top_k"`
	Questions     int         `json:"questions"`
	Fixed         IndexResult `json:"fixed"`
	Structural    IndexResult `json:"structural"`
	Examples      []Example   `json:"examples"`
	Conclusion    string      `json:"conclusion"`
}

func LoadQuestions(path string) ([]Question, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read evaluation questions: %w", err)
	}
	var questions []Question
	if err := json.Unmarshal(data, &questions); err != nil {
		return nil, fmt.Errorf("decode evaluation questions: %w", err)
	}
	if len(questions) < 1 {
		return nil, fmt.Errorf("evaluation set is empty")
	}
	ids := make(map[string]bool)
	for i, question := range questions {
		if question.ID == "" || question.Query == "" || len(question.ExpectedSources) == 0 || question.Explanation == "" {
			return nil, fmt.Errorf("question %d has missing required fields", i)
		}
		if ids[question.ID] {
			return nil, fmt.Errorf("duplicate question ID %q", question.ID)
		}
		ids[question.ID] = true
	}
	return questions, nil
}

func Compare(ctx context.Context, fixedPath, structuralPath string, questions []Question, topK, batchSize int, embedder embedding.Embedder) (Report, error) {
	if topK < 5 {
		return Report{}, fmt.Errorf("top-k must be at least 5 to calculate Hit@5 and MRR@5")
	}
	fixed, err := indexstore.Load(fixedPath)
	if err != nil {
		return Report{}, fmt.Errorf("load fixed index: %w", err)
	}
	structural, err := indexstore.Load(structuralPath)
	if err != nil {
		return Report{}, fmt.Errorf("load structural index: %w", err)
	}
	if fixed.Strategy != "fixed" || structural.Strategy != "structural" {
		return Report{}, fmt.Errorf("the supplied indexes must be fixed and structural respectively")
	}
	if fixed.CorpusID != structural.CorpusID {
		return Report{}, fmt.Errorf("indexes use different corpora")
	}
	if fixed.Model != structural.Model {
		return Report{}, fmt.Errorf("indexes use different embedding models")
	}
	if fixed.EmbeddingDimension != structural.EmbeddingDimension {
		return Report{}, fmt.Errorf("indexes use different embedding dimensions")
	}
	queries := make([]string, len(questions))
	for i := range questions {
		queries[i] = questions[i].Query
	}
	vectors, dimension, err := embedding.EmbedAll(ctx, embedder, fixed.Model, queries, batchSize)
	if err != nil {
		return Report{}, fmt.Errorf("embed evaluation queries: %w", err)
	}
	if dimension != fixed.EmbeddingDimension {
		return Report{}, fmt.Errorf("query embedding dimension %d does not match index dimension %d", dimension, fixed.EmbeddingDimension)
	}

	fixedRanked := make([][]bool, len(questions))
	structuralRanked := make([][]bool, len(questions))
	var fixedTop1, structuralTop1 float64
	var examples []Example
	for i, question := range questions {
		fixedResults, err := search.Vector(fixed, vectors[i], topK)
		if err != nil {
			return Report{}, err
		}
		structuralResults, err := search.Vector(structural, vectors[i], topK)
		if err != nil {
			return Report{}, err
		}
		fixedRanked[i] = relevance(question, fixedResults)
		structuralRanked[i] = relevance(question, structuralResults)
		fixedTop1 += fixedResults[0].Score
		structuralTop1 += structuralResults[0].Score
		if len(examples) < 3 {
			examples = append(examples, Example{
				QuestionID: question.ID, Query: question.Query,
				Fixed:      fixedResults[:min(3, len(fixedResults))],
				Structural: structuralResults[:min(3, len(structuralResults))],
			})
		}
	}
	fixedMetrics := ComputeMetrics(fixedRanked)
	structuralMetrics := ComputeMetrics(structuralRanked)
	fixedMetrics.AverageTop1Similarity = fixedTop1 / float64(len(questions))
	structuralMetrics.AverageTop1Similarity = structuralTop1 / float64(len(questions))
	fixedInfo, err := os.Stat(fixedPath)
	if err != nil {
		return Report{}, fmt.Errorf("stat fixed index: %w", err)
	}
	structuralInfo, err := os.Stat(structuralPath)
	if err != nil {
		return Report{}, fmt.Errorf("stat structural index: %w", err)
	}
	report := Report{
		SchemaVersion: "1", GeneratedAt: time.Now().UTC(), CorpusID: fixed.CorpusID,
		Model: fixed.Model, TopK: topK, Questions: len(questions), Examples: examples,
		Fixed:      summarize(fixed, fixedMetrics, fixedInfo.Size()),
		Structural: summarize(structural, structuralMetrics, structuralInfo.Size()),
	}
	report.Conclusion = conclusion(report.Fixed, report.Structural)
	return report, nil
}

func relevance(question Question, results []search.Result) []bool {
	values := make([]bool, len(results))
	for i, result := range results {
		for _, expected := range question.ExpectedSources {
			if result.Source == expected {
				// Source is the common relevance label for both strategies.
				// ExpectedSections remains descriptive because fixed chunks
				// deliberately do not carry structural heading paths.
				values[i] = true
				break
			}
		}
	}
	return values
}

func ComputeMetrics(ranked [][]bool) Metrics {
	if len(ranked) == 0 {
		return Metrics{}
	}
	var hit1, hit5, reciprocal float64
	for _, results := range ranked {
		if len(results) > 0 && results[0] {
			hit1++
		}
		for i := 0; i < min(5, len(results)); i++ {
			if results[i] {
				hit5++
				reciprocal += 1 / float64(i+1)
				break
			}
		}
	}
	count := float64(len(ranked))
	return Metrics{HitAt1: hit1 / count, HitAt5: hit5 / count, MRRAt5: reciprocal / count}
}

func summarize(index indexstore.Index, metrics Metrics, size int64) IndexResult {
	return IndexResult{
		Strategy: string(index.Strategy), Metrics: metrics,
		Chunks: index.Stats.Chunks, MinChunkWords: index.Stats.MinChunkWords,
		AverageChunkWords: index.Stats.AverageChunkWords, MaxChunkWords: index.Stats.MaxChunkWords,
		BuildDurationMS: index.Stats.BuildDurationMS, JSONSizeBytes: size,
	}
}

func conclusion(fixed, structural IndexResult) string {
	if structural.Metrics.MRRAt5 > fixed.Metrics.MRRAt5 {
		return "Structural chunking ranked relevant chunks earlier on this evaluation set (higher MRR@5)."
	}
	if fixed.Metrics.MRRAt5 > structural.Metrics.MRRAt5 {
		return "Fixed chunking ranked relevant chunks earlier on this evaluation set (higher MRR@5)."
	}
	if structural.Metrics.HitAt5 > fixed.Metrics.HitAt5 {
		return "MRR@5 was tied; structural chunking retrieved more expected sources in the top five."
	}
	if fixed.Metrics.HitAt5 > structural.Metrics.HitAt5 {
		return "MRR@5 was tied; fixed chunking retrieved more expected sources in the top five."
	}
	if structural.Metrics.HitAt1 > fixed.Metrics.HitAt1 {
		return "MRR@5 and Hit@5 were tied; structural chunking returned more expected sources at rank one."
	}
	if fixed.Metrics.HitAt1 > structural.Metrics.HitAt1 {
		return "MRR@5 and Hit@5 were tied; fixed chunking returned more expected sources at rank one."
	}
	return "The two strategies tied on Hit@1, Hit@5, and MRR@5 for this evaluation set; chunk count and storage cost should guide the choice."
}

func Save(outDir string, report Report) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create comparison directory: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := atomicWrite(filepath.Join(outDir, "comparison.json"), data); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(outDir, "comparison.md"), []byte(markdown(report)))
}

func markdown(report Report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Fixed vs structural chunking\n\n")
	fmt.Fprintf(&out, "Corpus: `%s`  \nModel: `%s`  \nQuestions: %d, top-k: %d\n\n", report.CorpusID, report.Model, report.Questions, report.TopK)
	out.WriteString("| Strategy | Hit@1 | Hit@5 | MRR@5 | Chunks | Chunk words min/avg/max | Build, ms | JSON, bytes | Avg top-1 similarity |\n")
	out.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	writeRow := func(item IndexResult) {
		fmt.Fprintf(&out, "| %s | %.3f | %.3f | %.3f | %d | %d / %.1f / %d | %d | %d | %.4f |\n",
			item.Strategy, item.Metrics.HitAt1, item.Metrics.HitAt5, item.Metrics.MRRAt5,
			item.Chunks, item.MinChunkWords, item.AverageChunkWords, item.MaxChunkWords,
			item.BuildDurationMS, item.JSONSizeBytes, item.Metrics.AverageTop1Similarity)
	}
	writeRow(report.Fixed)
	writeRow(report.Structural)
	out.WriteString("\n## Examples\n\n")
	for _, example := range report.Examples {
		fmt.Fprintf(&out, "### %s — %s\n\n", example.QuestionID, example.Query)
		out.WriteString("Fixed:\n\n")
		for _, result := range example.Fixed {
			fmt.Fprintf(&out, "- %d. `%.4f` `%s` — %s\n", result.Rank, result.Score, result.Source, result.Section)
		}
		out.WriteString("\nStructural:\n\n")
		for _, result := range example.Structural {
			fmt.Fprintf(&out, "- %d. `%.4f` `%s` — %s\n", result.Rank, result.Score, result.Source, result.Section)
		}
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "## Conclusion\n\n%s\n", report.Conclusion)
	return out.String()
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".comparison-*.tmp")
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
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
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
