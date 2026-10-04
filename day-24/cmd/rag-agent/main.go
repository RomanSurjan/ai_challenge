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

	"ai-challenge/day-24/internal/agent"
	"ai-challenge/day-24/internal/embedding"
	"ai-challenge/day-24/internal/evaluation"
	"ai-challenge/day-24/internal/evidence"
	"ai-challenge/day-24/internal/generation"
	"ai-challenge/day-24/internal/indexstore"
	"ai-challenge/day-24/internal/judging"
	"ai-challenge/day-24/internal/rewriting"
)

const (
	defaultIndex            = "../day-21/artifacts/index-structural.json"
	defaultTimeout          = 2 * time.Minute
	defaultCandidateK       = 20
	defaultFinalK           = 5
	defaultChunkSimilarity  = 0.45
	defaultAnswerRelevance  = 0.55 // Retrieval-only calibration: max F1, <=20% false refusal, highest-threshold tie-break.
	defaultAlpha            = 0.70
	defaultBeta             = 0.20
	defaultGamma            = 0.10
	defaultQuoteMin         = 20
	defaultQuoteMax         = 160
	defaultRewriteMaxTokens = 96
	defaultJudgeMaxTokens   = 128
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
	case "calibrate-gate":
		return runCalibration(ctx, args[1:])
	case "eval":
		return runEval(ctx, args[1:])
	case "eval-abstention":
		return runAbstention(ctx, args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: rag-agent <ask|calibrate-gate|eval|eval-abstention> [flags]")
}

type commonFlags struct {
	indexPath, embedEndpoint, embedModel, chatEndpoint, chatModel, judgeModel, rewriteModel *string
	candidateK, finalK, quoteMin, quoteMax, maxTokens, judgeMaxTokens, rewriteMaxTokens     *int
	chunkSimilarity, answerRelevance, alpha, beta, gamma, temperature                       *float64
	timeout                                                                                 *time.Duration
	useRewrite                                                                              *bool
}

func addCommonFlags(f *flag.FlagSet) commonFlags {
	return commonFlags{
		indexPath: f.String("index", envString("RAG_INDEX", defaultIndex), "read-only Day 21 index"), embedEndpoint: f.String("embed-endpoint", envString("OLLAMA_EMBED_ENDPOINT", embedding.DefaultEndpoint), "Ollama embed endpoint"), embedModel: f.String("embed-model", envString("OLLAMA_EMBED_MODEL", embedding.DefaultModel), "embedding model"), chatEndpoint: f.String("chat-endpoint", envString("OLLAMA_CHAT_ENDPOINT", generation.DefaultEndpoint), "Ollama chat endpoint"), chatModel: f.String("chat-model", envString("OLLAMA_CHAT_MODEL", generation.DefaultModel), "answer model"), judgeModel: f.String("judge-model", envString("OLLAMA_JUDGE_MODEL", generation.DefaultModel), "entailment judge model"), rewriteModel: f.String("rewrite-model", envString("OLLAMA_REWRITE_MODEL", generation.DefaultModel), "optional rewrite model"),
		candidateK: f.Int("candidate-k", envInt("RAG_CANDIDATE_K", defaultCandidateK), "candidate top-K"), finalK: f.Int("final-k", envInt("RAG_FINAL_K", defaultFinalK), "final context top-K"), chunkSimilarity: f.Float64("chunk-min-similarity", envFloat("RAG_CHUNK_MIN_SIMILARITY", defaultChunkSimilarity), "Day 23 per-chunk cosine threshold"), answerRelevance: f.Float64("answer-min-relevance", envFloat("RAG_ANSWER_MIN_RELEVANCE", defaultAnswerRelevance), "separate answer/abstention threshold"), alpha: f.Float64("alpha", envFloat("RAG_RERANK_ALPHA", defaultAlpha), "cosine weight"), beta: f.Float64("beta", envFloat("RAG_RERANK_BETA", defaultBeta), "lexical weight"), gamma: f.Float64("gamma", envFloat("RAG_RERANK_GAMMA", defaultGamma), "metadata weight"), quoteMin: f.Int("quote-min-runes", envInt("RAG_QUOTE_MIN_RUNES", defaultQuoteMin), "minimum exact quote length"), quoteMax: f.Int("quote-max-runes", envInt("RAG_QUOTE_MAX_RUNES", defaultQuoteMax), "maximum exact quote length"), useRewrite: f.Bool("rewrite", envBool("RAG_USE_REWRITE", false), "optional query rewrite; disabled by default"), rewriteMaxTokens: f.Int("rewrite-max-tokens", envInt("OLLAMA_REWRITE_MAX_TOKENS", defaultRewriteMaxTokens), "rewrite token limit"), timeout: f.Duration("timeout", envDuration("OLLAMA_TIMEOUT", defaultTimeout), "HTTP timeout"), temperature: f.Float64("temperature", envFloat("OLLAMA_TEMPERATURE", 0), "answer temperature"), maxTokens: f.Int("generation-max-tokens", envInt("OLLAMA_MAX_TOKENS", generation.DefaultMaxTokens), "answer token limit"), judgeMaxTokens: f.Int("judge-max-tokens", envInt("OLLAMA_JUDGE_MAX_TOKENS", defaultJudgeMaxTokens), "judge token limit"),
	}
}
func (o commonFlags) build() (*agent.Agent, indexstore.Index, error) {
	if *o.timeout <= 0 || *o.maxTokens <= 0 || *o.judgeMaxTokens <= 0 || *o.rewriteMaxTokens <= 0 {
		return nil, indexstore.Index{}, fmt.Errorf("timeout and token limits must be positive")
	}
	if *o.chunkSimilarity < 0 || *o.chunkSimilarity > 1 {
		return nil, indexstore.Index{}, fmt.Errorf("chunk-min-similarity must be in [0,1]")
	}
	if *o.answerRelevance < 0 || *o.answerRelevance > 1 {
		return nil, indexstore.Index{}, fmt.Errorf("answer-min-relevance must be in [0,1]")
	}
	if *o.quoteMin <= 0 || *o.quoteMax < *o.quoteMin {
		return nil, indexstore.Index{}, fmt.Errorf("quote bounds must satisfy 0 < min <= max")
	}
	pipeline := agent.PipelineConfig{CandidateK: *o.candidateK, FinalK: *o.finalK, MinSimilarity: *o.chunkSimilarity, AnswerMinRelevance: *o.answerRelevance, Alpha: *o.alpha, Beta: *o.beta, Gamma: *o.gamma, UseRewrite: *o.useRewrite}
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
	client := &agent.Agent{Index: &index, IndexPath: *o.indexPath, Embedder: embedding.NewOllama(*o.embedEndpoint, *o.timeout), Generator: chat, Rewriter: rewriting.NewOllama(chat, *o.rewriteModel, *o.rewriteMaxTokens), Judge: judging.NewOllama(chat, *o.judgeModel, *o.judgeMaxTokens), Validator: evidence.Validator{MinQuoteRunes: *o.quoteMin, MaxQuoteRunes: *o.quoteMax}, EmbedModel: *o.embedModel, ChatModel: *o.chatModel, Settings: settings, Pipeline: pipeline}
	return client, index, nil
}

func runAsk(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("ask", flag.ContinueOnError)
	modeValue := f.String("mode", "strict", "day23, grounded, strict, or all")
	question := f.String("question", "", "question to answer")
	jsonOutput := f.Bool("json", false, "print complete JSON")
	common := addCommonFlags(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	mode := agent.Mode(strings.ToLower(strings.TrimSpace(*modeValue)))
	if mode != agent.All && !mode.Valid() {
		return fmt.Errorf("--mode must be day23, grounded, strict, or all")
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
		for i, result := range results {
			if i > 0 {
				fmt.Println()
			}
			printResult(result)
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

func runCalibration(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("calibrate-gate", flag.ContinueOnError)
	positivePath := f.String("questions", "eval/questions.json", "unchanged Day 23 positive dataset")
	negativePath := f.String("abstention-questions", "eval/abstention_questions.json", "out-of-domain dataset")
	out := f.String("out", "artifacts", "artifact directory")
	thresholdValues := f.String("thresholds", defaultCalibrationGrid(), "predeclared comma-separated thresholds")
	jsonOutput := f.Bool("json", false, "print report JSON")
	common := addCommonFlags(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	client, _, err := common.build()
	if err != nil {
		return err
	}
	positives, err := evaluation.LoadQuestions(*positivePath)
	if err != nil {
		return err
	}
	negatives, err := evaluation.LoadAbstentionQuestions(*negativePath)
	if err != nil {
		return err
	}
	positiveSHA, err := evaluation.DatasetSHA256(*positivePath)
	if err != nil {
		return err
	}
	negativeSHA, err := evaluation.DatasetSHA256(*negativePath)
	if err != nil {
		return err
	}
	thresholds, err := parseThresholds(*thresholdValues)
	if err != nil {
		return err
	}
	report, err := evaluation.CalibrateGate(ctx, client, positives, negatives, evaluation.GateCalibrationReport{PositiveDataset: *positivePath, PositiveDatasetSHA256: positiveSHA, NegativeDataset: *negativePath, NegativeDatasetSHA256: negativeSHA}, thresholds)
	if err != nil {
		return err
	}
	if err := evaluation.SaveCalibration(*out, report); err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(report)
	}
	fmt.Printf("selected answer threshold: %.3f\nartifacts: %s/gate-calibration.json, %s/gate-calibration.md\n", report.SelectedThreshold, *out, *out)
	return nil
}

func runEval(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("eval", flag.ContinueOnError)
	questionsPath := f.String("questions", "eval/questions.json", "main dataset")
	out := f.String("out", "artifacts", "artifact directory")
	jsonOutput := f.Bool("json", false, "print summary JSON")
	common := addCommonFlags(f)
	if err := f.Parse(args); err != nil {
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
	fmt.Fprintf(os.Stderr, "evaluating %d questions in day23, grounded, strict with shared retrieval...\n", len(questions))
	report := evaluation.Run(ctx, client, questions, evaluation.Report{ChatModel: *common.chatModel, JudgeModel: *common.judgeModel, EmbeddingModel: *common.embedModel, Index: *common.indexPath, IndexCorpusID: index.CorpusID, Dataset: *questionsPath, DatasetSHA256: sha, Pipeline: client.Pipeline, QuoteMinRunes: client.Validator.MinQuoteRunes, QuoteMaxRunes: client.Validator.MaxQuoteRunes, Temperature: *common.temperature, MaxTokens: *common.maxTokens, JudgeMaxTokens: *common.judgeMaxTokens})
	if err := evaluation.Save(*out, report); err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(report.Summary)
	}
	for _, mode := range evaluation.Modes {
		s := report.Summary[mode]
		fmt.Printf("%s: concepts %.3f, answered %d, abstained %d, exact quotes %.3f, fully supported %.3f, errors %d\n", mode, s.ConceptCoverage, s.Answered, s.Abstained, s.ExactSubstringQuoteRate, s.FullySupportedAnswerRate, s.Errors)
	}
	fmt.Printf("artifacts: %s/evaluation.json, %s/comparison.md\n", *out, *out)
	return nil
}

func runAbstention(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("eval-abstention", flag.ContinueOnError)
	questionsPath := f.String("questions", "eval/abstention_questions.json", "out-of-domain dataset")
	out := f.String("out", "artifacts", "artifact directory")
	jsonOutput := f.Bool("json", false, "print summary JSON")
	common := addCommonFlags(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	client, _, err := common.build()
	if err != nil {
		return err
	}
	questions, err := evaluation.LoadAbstentionQuestions(*questionsPath)
	if err != nil {
		return err
	}
	sha, err := evaluation.DatasetSHA256(*questionsPath)
	if err != nil {
		return err
	}
	report := evaluation.RunAbstention(ctx, client, questions, evaluation.AbstentionReport{Dataset: *questionsPath, DatasetSHA256: sha, AnswerThreshold: client.Pipeline.AnswerMinRelevance})
	if err := evaluation.SaveAbstention(*out, report); err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(report.Summary)
	}
	fmt.Printf("abstention %.3f, unsafe answered %.3f, answer model not called %d/%d\n", report.Summary.AbstentionRate, report.Summary.UnsafeAnsweredRate, report.Summary.AnswerModelNotCalled, report.Summary.Questions)
	fmt.Printf("artifacts: %s/abstention-evaluation.json, %s/abstention-comparison.md\n", *out, *out)
	return nil
}

func printResult(r agent.Result) {
	fmt.Printf("=== %s ===\nStatus: %s\nSearch query: %s\nRelevance: %.6f / %.6f (passed=%t)\nAnswer:\n%s\n", strings.ToUpper(string(r.Mode)), r.Status, r.SearchQuery, r.Gate.Score, r.Gate.Threshold, r.Gate.Passed, r.Answer)
	if r.ClarificationQuestion != "" {
		fmt.Printf("Clarification: %s\n", r.ClarificationQuestion)
	}
	for _, s := range r.Sources {
		fmt.Printf("  %s %s — %s — %s\n", s.ID, s.Source, s.Section, s.ChunkID)
	}
	for _, c := range r.Citations {
		if c.Quote != "" {
			fmt.Printf("  quote %s → %s: %q\n", c.SourceID, strings.Join(c.ClaimIDs, ","), c.Quote)
		}
	}
	fmt.Printf("Latency retrieval/rerank/generation/validation/judge/total: %d/%d/%d/%d/%d/%d ms\n", r.Timing.RetrievalMS, r.Timing.FilterRerankMS, r.Timing.GenerationMS, r.Timing.ValidationMS, r.Timing.JudgeMS, r.Timing.TotalMS)
}
func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	e.SetEscapeHTML(false)
	return e.Encode(v)
}
func defaultCalibrationGrid() string {
	values := make([]string, 0, 36)
	for i := 45; i <= 80; i++ {
		values = append(values, fmt.Sprintf("%.2f", float64(i)/100))
	}
	return strings.Join(values, ",")
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
func envBool(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}
