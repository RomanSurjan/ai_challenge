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

	"ai-challenge/day-23/internal/agent"
	"ai-challenge/day-23/internal/embedding"
	"ai-challenge/day-23/internal/evaluation"
	"ai-challenge/day-23/internal/generation"
	"ai-challenge/day-23/internal/indexstore"
	"ai-challenge/day-23/internal/rewriting"
)

const (
	defaultIndex            = "../day-21/artifacts/index-structural.json"
	defaultTimeout          = 2 * time.Minute
	defaultCandidateK       = 20
	defaultFinalK           = 5
	defaultMinSimilarity    = 0.45
	defaultAlpha            = 0.70
	defaultBeta             = 0.20
	defaultGamma            = 0.10
	defaultRewriteMaxTokens = 96
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
	case "sweep":
		return runSweep(ctx, args[1:])
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
func usage() { fmt.Fprintln(os.Stderr, "usage: rag-agent <ask|sweep|eval> [flags]") }

type commonFlags struct {
	indexPath        *string
	embedEndpoint    *string
	embedModel       *string
	chatEndpoint     *string
	chatModel        *string
	rewriteModel     *string
	rewriteMaxTokens *int
	candidateK       *int
	finalK           *int
	minSimilarity    *float64
	alpha            *float64
	beta             *float64
	gamma            *float64
	timeout          *time.Duration
	temperature      *float64
	maxTokens        *int
}

func addCommonFlags(flags *flag.FlagSet) commonFlags {
	return commonFlags{
		indexPath:     flags.String("index", envString("RAG_INDEX", defaultIndex), "compatible Day 21 JSON index"),
		embedEndpoint: flags.String("embed-endpoint", envString("OLLAMA_EMBED_ENDPOINT", embedding.DefaultEndpoint), "Ollama embedding endpoint"), embedModel: flags.String("embed-model", envString("OLLAMA_EMBED_MODEL", embedding.DefaultModel), "embedding model"),
		chatEndpoint: flags.String("chat-endpoint", envString("OLLAMA_CHAT_ENDPOINT", generation.DefaultEndpoint), "Ollama chat endpoint"), chatModel: flags.String("chat-model", envString("OLLAMA_CHAT_MODEL", generation.DefaultModel), "answer model"), rewriteModel: flags.String("rewrite-model", envString("OLLAMA_REWRITE_MODEL", generation.DefaultModel), "query rewrite model"), rewriteMaxTokens: flags.Int("rewrite-max-tokens", envInt("OLLAMA_REWRITE_MAX_TOKENS", defaultRewriteMaxTokens), "rewrite response token limit"),
		candidateK: flags.Int("candidate-k", envInt("RAG_CANDIDATE_K", defaultCandidateK), "candidate top-K before filtering"), finalK: flags.Int("final-k", envInt("RAG_FINAL_K", defaultFinalK), "context top-K after filtering/reranking"), minSimilarity: flags.Float64("min-similarity", envFloat("RAG_MIN_SIMILARITY", defaultMinSimilarity), "cosine threshold"), alpha: flags.Float64("alpha", envFloat("RAG_RERANK_ALPHA", defaultAlpha), "normalized cosine weight"), beta: flags.Float64("beta", envFloat("RAG_RERANK_BETA", defaultBeta), "lexical overlap weight"), gamma: flags.Float64("gamma", envFloat("RAG_RERANK_GAMMA", defaultGamma), "metadata overlap weight"),
		timeout: flags.Duration("timeout", envDuration("OLLAMA_TIMEOUT", defaultTimeout), "HTTP timeout"), temperature: flags.Float64("temperature", envFloat("OLLAMA_TEMPERATURE", 0), "answer temperature"), maxTokens: flags.Int("max-tokens", envInt("OLLAMA_MAX_TOKENS", generation.DefaultMaxTokens), "answer token limit"),
	}
}

func (o commonFlags) build() (*agent.Agent, indexstore.Index, error) {
	if *o.timeout <= 0 || *o.maxTokens <= 0 || *o.rewriteMaxTokens <= 0 {
		return nil, indexstore.Index{}, fmt.Errorf("timeout and token limits must be positive")
	}
	if *o.minSimilarity < 0 || *o.minSimilarity > 1 {
		return nil, indexstore.Index{}, fmt.Errorf("min-similarity must be in [0, 1]")
	}
	pipeline := agent.PipelineConfig{CandidateK: *o.candidateK, FinalK: *o.finalK, MinSimilarity: *o.minSimilarity, Alpha: *o.alpha, Beta: *o.beta, Gamma: *o.gamma}
	if err := pipeline.Validate(); err != nil {
		return nil, indexstore.Index{}, err
	}
	index, err := indexstore.Load(*o.indexPath)
	if err != nil {
		return nil, indexstore.Index{}, err
	}
	if *o.embedModel != index.Model {
		return nil, indexstore.Index{}, fmt.Errorf("embedding model %q does not match index model %q", *o.embedModel, index.Model)
	}
	settings := generation.DefaultSettings()
	settings.Temperature = *o.temperature
	settings.MaxTokens = *o.maxTokens
	chat := generation.NewOllama(*o.chatEndpoint, *o.timeout)
	client := &agent.Agent{Index: &index, IndexPath: *o.indexPath, Embedder: embedding.NewOllama(*o.embedEndpoint, *o.timeout), Generator: chat, Rewriter: rewriting.NewOllama(chat, *o.rewriteModel, *o.rewriteMaxTokens), EmbedModel: *o.embedModel, ChatModel: *o.chatModel, Settings: settings, Pipeline: pipeline}
	return client, index, nil
}

func runAsk(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("ask", flag.ContinueOnError)
	modeFlag := flags.String("mode", "baseline", "baseline, filtered, rewritten, enhanced, or all")
	question := flags.String("question", "", "question to answer")
	jsonOutput := flags.Bool("json", false, "print complete JSON")
	common := addCommonFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	mode := agent.Mode(strings.ToLower(strings.TrimSpace(*modeFlag)))
	if mode != agent.All && !mode.Valid() {
		return fmt.Errorf("--mode must be baseline, filtered, rewritten, enhanced, or all")
	}
	if strings.TrimSpace(*question) == "" {
		return fmt.Errorf("--question is required")
	}
	client, _, err := common.build()
	if err != nil {
		return err
	}
	if mode == agent.All {
		results, err := client.AskAll(ctx, *question)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return printJSON(results)
		}
		for i, r := range results {
			if i > 0 {
				fmt.Println()
			}
			printResult(r)
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

func runSweep(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("sweep", flag.ContinueOnError)
	questionsPath := flags.String("questions", "eval/questions.json", "evaluation dataset")
	outDir := flags.String("out", "artifacts", "artifact directory")
	values := flags.String("thresholds", "0.30,0.35,0.40,0.45,0.50,0.55,0.60", "comma-separated thresholds")
	jsonOutput := flags.Bool("json", false, "print complete JSON")
	common := addCommonFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	client, index, err := common.build()
	if err != nil {
		return err
	}
	questions, err := evaluation.LoadQuestions(*questionsPath)
	if err != nil {
		return err
	}
	sha, err := evaluation.DatasetSHA256(*questionsPath)
	if err != nil {
		return err
	}
	thresholds, err := parseThresholds(*values)
	if err != nil {
		return err
	}
	report, err := evaluation.Sweep(ctx, index, *common.indexPath, client.Embedder, *common.embedModel, questions, *questionsPath, sha, *common.candidateK, *common.finalK, *common.alpha, *common.beta, *common.gamma, thresholds)
	if err != nil {
		return err
	}
	if err := evaluation.SaveSweep(*outDir, report); err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(report)
	}
	fmt.Printf("selected threshold: %.2f\nartifacts: %s/threshold-sweep.json, %s/threshold-sweep.md\n", report.SelectedThreshold, *outDir, *outDir)
	return nil
}

func runEval(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	questionsPath := flags.String("questions", "eval/questions.json", "evaluation dataset")
	outDir := flags.String("out", "artifacts", "artifact directory")
	jsonOutput := flags.Bool("json", false, "print summary JSON")
	common := addCommonFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	questions, err := evaluation.LoadQuestions(*questionsPath)
	if err != nil {
		return err
	}
	sha, err := evaluation.DatasetSHA256(*questionsPath)
	if err != nil {
		return err
	}
	client, index, err := common.build()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "evaluating %d questions in four modes with %s...\n", len(questions), *common.chatModel)
	report := evaluation.Run(ctx, client, questions, evaluation.Report{ChatModel: *common.chatModel, RewriteModel: *common.rewriteModel, EmbeddingModel: *common.embedModel, Index: *common.indexPath, IndexCorpusID: index.CorpusID, Dataset: *questionsPath, DatasetSHA256: sha, Pipeline: client.Pipeline.ForMode(agent.Enhanced), Temperature: *common.temperature, MaxTokens: *common.maxTokens})
	if err := evaluation.Save(*outDir, report); err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(report.Summary)
	}
	for _, mode := range evaluation.Modes {
		s := report.Summary[mode]
		fmt.Printf("%s: concepts %.3f, final recall %.3f, precision %.3f, MRR %.3f, errors %d\n", mode, s.ConceptCoverage, s.FinalRecall, s.FinalPrecision, s.MRR, s.Errors)
	}
	fmt.Printf("artifacts: %s/evaluation.json, %s/comparison.md\n", *outDir, *outDir)
	for _, m := range report.Summary {
		if m.Errors > 0 {
			return fmt.Errorf("evaluation completed with errors; see artifacts")
		}
	}
	return nil
}

func printResult(r agent.Result) {
	fmt.Printf("=== %s ===\nSearch query: %s\nRewrite fallback: %t", strings.ToUpper(string(r.Mode)), r.RewrittenQuery, r.Rewrite.Fallback)
	if r.Rewrite.Error != "" {
		fmt.Printf(" (%s)", r.Rewrite.Error)
	}
	fmt.Printf("\nAnswer:\n%s\n", r.Answer)
	fmt.Printf("Candidates: %d, rejected/omitted: %d, final: %d\n", len(r.Candidates), len(r.RejectedCandidates), len(r.FinalContext))
	for _, c := range r.FinalContext {
		fmt.Printf("  S%d cosine=%.6f source=%s section=%q chunk_id=%s\n", c.Rank, c.Score, c.Source, c.Section, c.ChunkID)
	}
	if len(r.InvalidCitations) > 0 {
		fmt.Printf("Invalid citations: %s\n", strings.Join(r.InvalidCitations, ", "))
	}
	fmt.Printf("Latency rewrite/retrieval/rerank/generation/total: %d/%d/%d/%d/%d ms; tokens: %d/%d\n", r.Rewrite.LatencyMS, r.Timing.RetrievalMS, r.Timing.FilterRerankMS, r.Timing.GenerationMS, r.Timing.TotalMS, r.Generation.PromptTokens, r.Generation.CompletionTokens)
}
func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	e.SetEscapeHTML(false)
	return e.Encode(v)
}
func parseThresholds(value string) ([]float64, error) {
	parts := strings.Split(value, ",")
	out := make([]float64, 0, len(parts))
	for _, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || v < 0 || v > 1 {
			return nil, fmt.Errorf("invalid threshold %q", part)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one threshold is required")
	}
	return out, nil
}
func envString(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
func envDuration(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return parsed
}
func envInt(name string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
func envFloat(name string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil {
		return fallback
	}
	return v
}
