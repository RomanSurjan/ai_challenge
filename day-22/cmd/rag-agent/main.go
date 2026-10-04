package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ai-challenge/day-22/internal/agent"
	"ai-challenge/day-22/internal/embedding"
	"ai-challenge/day-22/internal/evaluation"
	"ai-challenge/day-22/internal/generation"
	"ai-challenge/day-22/internal/indexstore"
)

const (
	defaultIndex   = "../day-21/artifacts/index-structural.json"
	defaultTimeout = 2 * time.Minute
)

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
	case "ask":
		return runAsk(ctx, args[1:])
	case "eval":
		return runEval(ctx, args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rag-agent <ask|eval> [flags]")
}

type commonFlags struct {
	indexPath     *string
	topK          *int
	embedEndpoint *string
	embedModel    *string
	chatEndpoint  *string
	chatModel     *string
	timeout       *time.Duration
	temperature   *float64
	maxTokens     *int
}

func addCommonFlags(flags *flag.FlagSet) commonFlags {
	return commonFlags{
		indexPath:     flags.String("index", defaultIndex, "compatible Day 21 JSON index"),
		topK:          flags.Int("top-k", 5, "retrieved chunks"),
		embedEndpoint: flags.String("embed-endpoint", envString("OLLAMA_EMBED_ENDPOINT", embedding.DefaultEndpoint), "Ollama embedding endpoint"),
		embedModel:    flags.String("embed-model", envString("OLLAMA_EMBED_MODEL", embedding.DefaultModel), "embedding model (must match index)"),
		chatEndpoint:  flags.String("chat-endpoint", envString("OLLAMA_CHAT_ENDPOINT", generation.DefaultEndpoint), "Ollama chat endpoint"),
		chatModel:     flags.String("chat-model", envString("OLLAMA_CHAT_MODEL", generation.DefaultModel), "Ollama chat model"),
		timeout:       flags.Duration("timeout", envDuration("OLLAMA_TIMEOUT", defaultTimeout), "HTTP timeout"),
		temperature:   flags.Float64("temperature", envFloat("OLLAMA_TEMPERATURE", 0), "generation temperature"),
		maxTokens:     flags.Int("max-tokens", envInt("OLLAMA_MAX_TOKENS", generation.DefaultMaxTokens), "maximum generated tokens"),
	}
}

func (options commonFlags) build(mode agent.Mode) (*agent.Agent, error) {
	if *options.topK <= 0 || *options.maxTokens <= 0 || *options.timeout <= 0 {
		return nil, fmt.Errorf("top-k, max-tokens, and timeout must be positive")
	}
	settings := generation.DefaultSettings()
	settings.Temperature = *options.temperature
	settings.MaxTokens = *options.maxTokens
	result := &agent.Agent{
		Generator: generation.NewOllama(*options.chatEndpoint, *options.timeout),
		ChatModel: *options.chatModel, Settings: settings, TopK: *options.topK,
	}
	if mode == agent.RAG || mode == agent.Both {
		index, err := indexstore.Load(*options.indexPath)
		if err != nil {
			return nil, err
		}
		if *options.embedModel != index.Model {
			return nil, fmt.Errorf("embedding model %q does not match index model %q", *options.embedModel, index.Model)
		}
		result.Index = &index
		result.IndexPath = *options.indexPath
		result.Embedder = embedding.NewOllama(*options.embedEndpoint, *options.timeout)
		result.EmbedModel = *options.embedModel
	}
	return result, nil
}

func runAsk(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("ask", flag.ContinueOnError)
	modeFlag := flags.String("mode", "rag", "plain, rag, or both")
	question := flags.String("question", "", "question to answer")
	jsonOutput := flags.Bool("json", false, "print JSON")
	common := addCommonFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	mode := agent.Mode(strings.ToLower(strings.TrimSpace(*modeFlag)))
	if mode != agent.Plain && mode != agent.RAG && mode != agent.Both {
		return fmt.Errorf("--mode must be plain, rag, or both")
	}
	if strings.TrimSpace(*question) == "" {
		return fmt.Errorf("--question is required")
	}
	client, err := common.build(mode)
	if err != nil {
		return err
	}
	if mode == agent.Both {
		results, err := client.AskBoth(ctx, *question)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return printJSON(results)
		}
		for i := range results {
			if i > 0 {
				fmt.Println()
			}
			printResult(results[i])
		}
		return nil
	}
	result, err := client.Ask(ctx, *question, mode)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(result)
	}
	printResult(result)
	return nil
}

func runEval(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	questionsPath := flags.String("questions", "eval/questions.json", "evaluation dataset")
	outDir := flags.String("out", "artifacts", "artifact directory")
	jsonOutput := flags.Bool("json", false, "print summary as JSON")
	common := addCommonFlags(flags)
	_ = flags.Int("batch-size", envInt("OLLAMA_BATCH_SIZE", embedding.DefaultBatchSize), "reserved embedding batch size")
	if err := flags.Parse(args); err != nil {
		return err
	}
	questions, err := evaluation.LoadQuestions(*questionsPath)
	if err != nil {
		return err
	}
	client, err := common.build(agent.Both)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "evaluating %d questions with %s (plain + RAG)...\n", len(questions), *common.chatModel)
	report := evaluation.Run(ctx, client, questions, evaluation.Report{
		ChatModel: *common.chatModel, EmbeddingModel: *common.embedModel, Index: *common.indexPath,
		TopK: *common.topK, Temperature: *common.temperature, MaxTokens: *common.maxTokens,
	})
	if err := evaluation.Save(*outDir, report); err != nil {
		return err
	}
	if *jsonOutput {
		if err := printJSON(report.Summary); err != nil {
			return err
		}
	} else {
		fmt.Printf("plain concept coverage: %.3f, all concepts: %.3f\n", report.Summary.Plain.ConceptCoverage, report.Summary.Plain.AllConceptsRate)
		fmt.Printf("RAG concept coverage: %.3f, all concepts: %.3f\n", report.Summary.RAG.ConceptCoverage, report.Summary.RAG.AllConceptsRate)
		fmt.Printf("retrieved source recall: %.3f, cited source recall: %.3f, invalid citations: %d\n", report.Summary.Retrieval.ExpectedSourceRecallTopK, report.Summary.Retrieval.ExpectedSourceRecallCitations, report.Summary.Retrieval.InvalidCitations)
		fmt.Printf("artifacts: %s/evaluation.json, %s/comparison.md\n", *outDir, *outDir)
	}
	if report.Summary.Errors > 0 {
		return fmt.Errorf("evaluation completed with %d generation/retrieval errors; see artifacts", report.Summary.Errors)
	}
	return nil
}

func printResult(result agent.Result) {
	fmt.Printf("=== %s ===\n", strings.ToUpper(string(result.Mode)))
	fmt.Printf("Model: %s\nAnswer:\n%s\n", result.Model, result.Answer)
	if result.Retrieval != nil {
		fmt.Println("Retrieved sources:")
		for _, chunk := range result.Retrieval.Chunks {
			fmt.Printf("  %d. score=%.6f source=%s section=%q chunk_id=%s\n", chunk.Rank, chunk.Score, chunk.Source, chunk.Section, chunk.ChunkID)
		}
		fmt.Println("Citations:")
		if len(result.Citations) == 0 {
			fmt.Println("  none")
		}
		for _, citation := range result.Citations {
			fmt.Printf("  %s source=%s section=%q chunk_id=%s\n", citation.ID, citation.Source, citation.Section, citation.ChunkID)
		}
		if len(result.InvalidCitations) == 0 {
			fmt.Println("Invalid citations: none")
		} else {
			fmt.Printf("Invalid citations: %s\n", strings.Join(result.InvalidCitations, ", "))
		}
		fmt.Printf("Retrieval latency: %d ms\n", result.Retrieval.DurationMS)
	}
	fmt.Printf("Generation latency: %d ms; prompt tokens: %d; completion tokens: %d; answer length: %d runes\n",
		result.Generation.DurationMS, result.Generation.PromptTokens, result.Generation.CompletionTokens, utf8.RuneCountInString(result.Answer))
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
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

func envInt(name string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envFloat(name string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil {
		return fallback
	}
	return parsed
}
