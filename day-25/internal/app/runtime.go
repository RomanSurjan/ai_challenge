package app

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/chatservice"
	"ai-challenge/day-25/internal/chatstore"
	"ai-challenge/day-25/internal/embedding"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/generation"
	"ai-challenge/day-25/internal/indexstore"
	"ai-challenge/day-25/internal/judging"
	"ai-challenge/day-25/internal/memory"
	"ai-challenge/day-25/internal/queryresolver"
	"ai-challenge/day-25/internal/rewriting"
)

const (
	defaultIndex           = "../day-21/artifacts/index-structural.json"
	defaultStoreDir        = ".sessions"
	defaultTimeout         = 2 * time.Minute
	defaultCandidateK      = 20
	defaultFinalK          = 5
	defaultChunkSimilarity = 0.45
	defaultAnswerRelevance = 0.55
	defaultAlpha           = 0.70
	defaultBeta            = 0.20
	defaultGamma           = 0.10
	defaultQuoteMin        = 20
	defaultQuoteMax        = 160
	defaultJudgeTokens     = 128
	defaultMemoryTokens    = 256
	defaultResolverTokens  = 192
)

// Config contains the shared CLI and web configuration for the Day 25 RAG
// runtime. ConfigFromEnv supplies the historical rag-chat defaults and BindFlags
// exposes the same command-line surface to either executable.
type Config struct {
	IndexPath       string
	StoreDir        string
	EmbedEndpoint   string
	EmbedModel      string
	ChatEndpoint    string
	ChatModel       string
	JudgeModel      string
	MemoryModel     string
	ResolverModel   string
	CandidateK      int
	FinalK          int
	QuoteMin        int
	QuoteMax        int
	MaxTokens       int
	JudgeTokens     int
	MemoryTokens    int
	ResolverTokens  int
	ChunkSimilarity float64
	AnswerRelevance float64
	Alpha           float64
	Beta            float64
	Gamma           float64
	Temperature     float64
	Timeout         time.Duration
}

// Runtime owns the single service and file store used by a process, together
// with the loaded index and RAG agent needed by the evaluation command.
type Runtime struct {
	Service *chatservice.Service
	Store   *chatstore.FileStore
	Index   indexstore.Index
	RAG     *agent.Agent
}

// ConfigFromEnv returns the existing rag-chat defaults with supported
// environment overrides applied.
func ConfigFromEnv() Config {
	return Config{
		IndexPath:       envString("RAG_INDEX", defaultIndex),
		StoreDir:        envString("RAG_CHAT_STORE_DIR", defaultStoreDir),
		EmbedEndpoint:   envString("OLLAMA_EMBED_ENDPOINT", embedding.DefaultEndpoint),
		EmbedModel:      envString("OLLAMA_EMBED_MODEL", embedding.DefaultModel),
		ChatEndpoint:    envString("OLLAMA_CHAT_ENDPOINT", generation.DefaultEndpoint),
		ChatModel:       envString("OLLAMA_CHAT_MODEL", generation.DefaultModel),
		JudgeModel:      envString("OLLAMA_JUDGE_MODEL", generation.DefaultModel),
		MemoryModel:     envString("OLLAMA_MEMORY_MODEL", generation.DefaultModel),
		ResolverModel:   envString("OLLAMA_RESOLVER_MODEL", generation.DefaultModel),
		CandidateK:      envInt("RAG_CANDIDATE_K", defaultCandidateK),
		FinalK:          envInt("RAG_FINAL_K", defaultFinalK),
		QuoteMin:        envInt("RAG_QUOTE_MIN_RUNES", defaultQuoteMin),
		QuoteMax:        envInt("RAG_QUOTE_MAX_RUNES", defaultQuoteMax),
		MaxTokens:       envInt("OLLAMA_MAX_TOKENS", generation.DefaultMaxTokens),
		JudgeTokens:     envInt("OLLAMA_JUDGE_MAX_TOKENS", defaultJudgeTokens),
		MemoryTokens:    envInt("OLLAMA_MEMORY_MAX_TOKENS", defaultMemoryTokens),
		ResolverTokens:  envInt("OLLAMA_RESOLVER_MAX_TOKENS", defaultResolverTokens),
		ChunkSimilarity: envFloat("RAG_CHUNK_MIN_SIMILARITY", defaultChunkSimilarity),
		AnswerRelevance: envFloat("RAG_ANSWER_MIN_RELEVANCE", defaultAnswerRelevance),
		Alpha:           envFloat("RAG_RERANK_ALPHA", defaultAlpha),
		Beta:            envFloat("RAG_RERANK_BETA", defaultBeta),
		Gamma:           envFloat("RAG_RERANK_GAMMA", defaultGamma),
		Temperature:     envFloat("OLLAMA_TEMPERATURE", 0),
		Timeout:         envDuration("OLLAMA_TIMEOUT", defaultTimeout),
	}
}

// BindFlags binds every historical rag-chat runtime flag to c.
func (c *Config) BindFlags(f *flag.FlagSet) {
	f.StringVar(&c.IndexPath, "index", c.IndexPath, "read-only Day 21 structural index")
	f.StringVar(&c.StoreDir, "store-dir", c.StoreDir, "persistent session directory")
	f.StringVar(&c.EmbedEndpoint, "embed-endpoint", c.EmbedEndpoint, "Ollama embed endpoint")
	f.StringVar(&c.EmbedModel, "embed-model", c.EmbedModel, "embedding model")
	f.StringVar(&c.ChatEndpoint, "chat-endpoint", c.ChatEndpoint, "Ollama chat endpoint")
	f.StringVar(&c.ChatModel, "chat-model", c.ChatModel, "answer model")
	f.StringVar(&c.JudgeModel, "judge-model", c.JudgeModel, "judge model")
	f.StringVar(&c.MemoryModel, "memory-model", c.MemoryModel, "memory extraction model")
	f.StringVar(&c.ResolverModel, "resolver-model", c.ResolverModel, "query resolver model")
	f.IntVar(&c.CandidateK, "candidate-k", c.CandidateK, "candidate top-K")
	f.IntVar(&c.FinalK, "final-k", c.FinalK, "final top-K")
	f.IntVar(&c.QuoteMin, "quote-min-runes", c.QuoteMin, "minimum exact quote length")
	f.IntVar(&c.QuoteMax, "quote-max-runes", c.QuoteMax, "maximum exact quote length")
	f.IntVar(&c.MaxTokens, "generation-max-tokens", c.MaxTokens, "answer token limit")
	f.IntVar(&c.JudgeTokens, "judge-max-tokens", c.JudgeTokens, "judge token limit")
	f.IntVar(&c.MemoryTokens, "memory-max-tokens", c.MemoryTokens, "memory extraction token limit")
	f.IntVar(&c.ResolverTokens, "resolver-max-tokens", c.ResolverTokens, "resolver token limit")
	f.Float64Var(&c.ChunkSimilarity, "chunk-min-similarity", c.ChunkSimilarity, "Day 23 chunk threshold")
	f.Float64Var(&c.AnswerRelevance, "answer-min-relevance", c.AnswerRelevance, "Day 24 answer threshold")
	f.Float64Var(&c.Alpha, "alpha", c.Alpha, "cosine weight")
	f.Float64Var(&c.Beta, "beta", c.Beta, "lexical weight")
	f.Float64Var(&c.Gamma, "gamma", c.Gamma, "metadata weight")
	f.Float64Var(&c.Temperature, "temperature", c.Temperature, "model temperature")
	f.DurationVar(&c.Timeout, "timeout", c.Timeout, "Ollama HTTP timeout")
}

// Build validates the shared configuration and constructs the complete Day 25
// RAG runtime.
func (c Config) Build() (*Runtime, error) {
	if c.Timeout <= 0 || c.MaxTokens <= 0 || c.JudgeTokens <= 0 || c.MemoryTokens <= 0 || c.ResolverTokens <= 0 {
		return nil, fmt.Errorf("timeouts and token limits must be positive")
	}
	pipeline := agent.PipelineConfig{
		CandidateK:         c.CandidateK,
		FinalK:             c.FinalK,
		MinSimilarity:      c.ChunkSimilarity,
		AnswerMinRelevance: c.AnswerRelevance,
		Alpha:              c.Alpha,
		Beta:               c.Beta,
		Gamma:              c.Gamma,
		UseRewrite:         false,
	}
	if err := pipeline.Validate(); err != nil {
		return nil, err
	}
	if c.QuoteMin <= 0 || c.QuoteMax < c.QuoteMin {
		return nil, fmt.Errorf("quote bounds must satisfy 0 < min <= max")
	}
	index, err := indexstore.Load(c.IndexPath)
	if err != nil {
		return nil, err
	}
	if c.EmbedModel != index.Model {
		return nil, fmt.Errorf("embedding model %q does not match index model %q", c.EmbedModel, index.Model)
	}
	store, err := chatstore.New(c.StoreDir)
	if err != nil {
		return nil, err
	}
	settings := generation.DefaultSettings()
	settings.Temperature = c.Temperature
	settings.MaxTokens = c.MaxTokens
	chatClient := generation.NewOllama(c.ChatEndpoint, c.Timeout)
	rag := &agent.Agent{
		Index:      &index,
		IndexPath:  c.IndexPath,
		Embedder:   embedding.NewOllama(c.EmbedEndpoint, c.Timeout),
		Generator:  chatClient,
		Rewriter:   rewriting.NewOllama(chatClient, c.ChatModel, 96),
		Judge:      judging.NewOllama(chatClient, c.JudgeModel, c.JudgeTokens),
		Validator:  evidence.Validator{MinQuoteRunes: c.QuoteMin, MaxQuoteRunes: c.QuoteMax},
		EmbedModel: c.EmbedModel,
		ChatModel:  c.ChatModel,
		Settings:   settings,
		Pipeline:   pipeline,
	}
	limits := chat.DefaultLimits()
	service := &chatservice.Service{
		Store:           store,
		RAG:             rag,
		MemoryExtractor: memory.NewOllama(chatClient, c.MemoryModel, c.MemoryTokens),
		Resolver:        queryresolver.NewOllama(chatClient, c.ResolverModel, c.ResolverTokens),
		MemoryValidator: memory.Validator{MaxEntries: limits.MaxTaskStateItems},
		Limits:          limits,
	}
	return &Runtime{Service: service, Store: store, Index: index, RAG: rag}, nil
}

// ParseMode validates a user-facing chat mode.
func ParseMode(value string) (chatservice.Mode, error) {
	mode := chatservice.Mode(strings.ToLower(strings.TrimSpace(value)))
	if !mode.Valid() {
		return "", fmt.Errorf("--mode must be stateless, history, or task-memory")
	}
	return mode, nil
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envFloat(name string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil {
		return fallback
	}
	return value
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
