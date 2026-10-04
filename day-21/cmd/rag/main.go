package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ai-challenge/day-21/internal/chunk"
	"ai-challenge/day-21/internal/document"
	"ai-challenge/day-21/internal/embedding"
	"ai-challenge/day-21/internal/evaluation"
	"ai-challenge/day-21/internal/indexstore"
	"ai-challenge/day-21/internal/search"
)

const defaultInputs = "../day-16,../day-17,../day-18,../day-19,../day-20"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a command is required")
	}
	switch args[0] {
	case "corpus":
		return runCorpus(args[1:])
	case "index":
		return runIndex(ctx, args[1:])
	case "search":
		return runSearch(ctx, args[1:])
	case "compare":
		return runCompare(ctx, args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rag <corpus|index|search|compare> [flags]")
}

func runCorpus(args []string) error {
	flags := flag.NewFlagSet("corpus", flag.ContinueOnError)
	input := flags.String("input", defaultInputs, "comma-separated input paths")
	out := flags.String("out", "artifacts/corpus-manifest.json", "manifest path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	docs, inputs, err := loadDocuments(*input)
	if err != nil {
		return err
	}
	manifest := document.NewManifest(inputs, docs, time.Now())
	if err := document.WriteManifest(*out, manifest); err != nil {
		return err
	}
	fmt.Printf("corpus: %d documents, %d words, %.2f estimated pages\nmanifest: %s\ncorpus_id: %s\n",
		manifest.Stats.Documents, manifest.Stats.Words, manifest.Stats.EstimatedPages, *out, manifest.CorpusID)
	if manifest.Stats.EstimatedPages < 30 {
		fmt.Fprintln(os.Stderr, "warning: corpus is below the assignment target of 30 estimated pages")
	}
	return nil
}

func runIndex(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("index", flag.ContinueOnError)
	input := flags.String("input", defaultInputs, "comma-separated input paths")
	strategyFlag := flags.String("strategy", "all", "fixed, structural, or all")
	outDir := flags.String("out", "artifacts", "output directory")
	target := flags.Int("target-words", 700, "target words per chunk")
	overlap := flags.Int("overlap", 100, "overlap words for oversized ranges")
	endpoint := flags.String("endpoint", envString("OLLAMA_ENDPOINT", embedding.DefaultEndpoint), "Ollama embed endpoint")
	model := flags.String("model", envString("OLLAMA_MODEL", embedding.DefaultModel), "embedding model")
	timeout := flags.Duration("timeout", envDuration("OLLAMA_TIMEOUT", embedding.DefaultTimeout), "HTTP timeout")
	batchSize := flags.Int("batch-size", envInt("OLLAMA_BATCH_SIZE", embedding.DefaultBatchSize), "embedding batch size")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config := chunk.Config{TargetWords: *target, OverlapWords: *overlap}
	if err := config.Validate(); err != nil {
		return err
	}
	docs, inputs, err := loadDocuments(*input)
	if err != nil {
		return err
	}
	manifest := document.NewManifest(inputs, docs, time.Now())
	if err := document.WriteManifest(filepath.Join(*outDir, "corpus-manifest.json"), manifest); err != nil {
		return err
	}
	strategies, err := parseStrategies(*strategyFlag)
	if err != nil {
		return err
	}
	embedder := embedding.NewOllama(*endpoint, *timeout)
	for _, strategy := range strategies {
		fmt.Printf("building %s index with model %s...\n", strategy, *model)
		index, err := indexstore.Build(ctx, docs, strategy, config, *model, *batchSize, embedder)
		if err != nil {
			return fmt.Errorf("build %s index: %w", strategy, err)
		}
		path := filepath.Join(*outDir, "index-"+string(strategy)+".json")
		if err := indexstore.Save(path, index); err != nil {
			return err
		}
		fmt.Printf("saved %s: %d chunks, dimension %d, %d ms\n", path, index.Stats.Chunks, index.EmbeddingDimension, index.Stats.BuildDurationMS)
	}
	return nil
}

func runSearch(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	indexPath := flags.String("index", "artifacts/index-fixed.json", "index path")
	query := flags.String("query", "", "semantic search query")
	topK := flags.Int("top-k", 5, "number of results")
	endpoint := flags.String("endpoint", envString("OLLAMA_ENDPOINT", embedding.DefaultEndpoint), "Ollama embed endpoint")
	modelOverride := flags.String("model", os.Getenv("OLLAMA_MODEL"), "model override (must match the index)")
	timeout := flags.Duration("timeout", envDuration("OLLAMA_TIMEOUT", embedding.DefaultTimeout), "HTTP timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*query) == "" {
		return fmt.Errorf("--query is required")
	}
	index, err := indexstore.Load(*indexPath)
	if err != nil {
		return err
	}
	if *modelOverride != "" && *modelOverride != index.Model {
		return fmt.Errorf("requested model %q does not match index model %q", *modelOverride, index.Model)
	}
	embedder := embedding.NewOllama(*endpoint, *timeout)
	vectors, dimension, err := embedding.EmbedAll(ctx, embedder, index.Model, []string{*query}, 1)
	if err != nil {
		return err
	}
	if dimension != index.EmbeddingDimension {
		return fmt.Errorf("query dimension %d does not match index dimension %d", dimension, index.EmbeddingDimension)
	}
	results, err := search.Vector(index, vectors[0], *topK)
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Printf("%d. score=%.6f source=%s section=%q chunk_id=%s\n   %s\n",
			result.Rank, result.Score, result.Source, result.Section, result.ChunkID, result.Snippet)
	}
	return nil
}

func runCompare(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	fixedPath := flags.String("fixed", "artifacts/index-fixed.json", "fixed index path")
	structuralPath := flags.String("structural", "artifacts/index-structural.json", "structural index path")
	evalPath := flags.String("eval", "eval/questions.json", "evaluation questions")
	outDir := flags.String("out", "artifacts", "output directory")
	topK := flags.Int("top-k", 5, "retrieval depth (minimum 5)")
	endpoint := flags.String("endpoint", envString("OLLAMA_ENDPOINT", embedding.DefaultEndpoint), "Ollama embed endpoint")
	modelOverride := flags.String("model", os.Getenv("OLLAMA_MODEL"), "model override (must match the indexes)")
	timeout := flags.Duration("timeout", envDuration("OLLAMA_TIMEOUT", embedding.DefaultTimeout), "HTTP timeout")
	batchSize := flags.Int("batch-size", envInt("OLLAMA_BATCH_SIZE", embedding.DefaultBatchSize), "embedding batch size")
	if err := flags.Parse(args); err != nil {
		return err
	}
	questions, err := evaluation.LoadQuestions(*evalPath)
	if err != nil {
		return err
	}
	fixed, err := indexstore.Load(*fixedPath)
	if err != nil {
		return err
	}
	if *modelOverride != "" && *modelOverride != fixed.Model {
		return fmt.Errorf("requested model %q does not match index model %q", *modelOverride, fixed.Model)
	}
	embedder := embedding.NewOllama(*endpoint, *timeout)
	report, err := evaluation.Compare(ctx, *fixedPath, *structuralPath, questions, *topK, *batchSize, embedder)
	if err != nil {
		return err
	}
	if err := evaluation.Save(*outDir, report); err != nil {
		return err
	}
	fmt.Printf("fixed: Hit@1 %.3f Hit@5 %.3f MRR@5 %.3f\n", report.Fixed.Metrics.HitAt1, report.Fixed.Metrics.HitAt5, report.Fixed.Metrics.MRRAt5)
	fmt.Printf("structural: Hit@1 %.3f Hit@5 %.3f MRR@5 %.3f\n", report.Structural.Metrics.HitAt1, report.Structural.Metrics.HitAt5, report.Structural.Metrics.MRRAt5)
	fmt.Printf("reports: %s, %s\n", filepath.Join(*outDir, "comparison.json"), filepath.Join(*outDir, "comparison.md"))
	return nil
}

func loadDocuments(raw string) ([]document.Document, []string, error) {
	var inputs []string
	for _, input := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(input); value != "" {
			inputs = append(inputs, value)
		}
	}
	docs, err := document.Load(inputs)
	return docs, inputs, err
}

func parseStrategies(value string) ([]chunk.Strategy, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all":
		return []chunk.Strategy{chunk.Fixed, chunk.Structural}, nil
	case "fixed":
		return []chunk.Strategy{chunk.Fixed}, nil
	case "structural":
		return []chunk.Strategy{chunk.Structural}, nil
	default:
		return nil, fmt.Errorf("--strategy must be fixed, structural, or all")
	}
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
