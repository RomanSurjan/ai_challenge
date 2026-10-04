package app

import (
	"flag"
	"testing"
	"time"

	"ai-challenge/day-25/internal/chatservice"
	"ai-challenge/day-25/internal/embedding"
	"ai-challenge/day-25/internal/generation"
)

func TestConfigFromEnvPreservesDefaultsAndOverrides(t *testing.T) {
	for _, name := range []string{
		"RAG_INDEX", "RAG_CHAT_STORE_DIR", "OLLAMA_EMBED_ENDPOINT", "OLLAMA_EMBED_MODEL",
		"OLLAMA_CHAT_ENDPOINT", "OLLAMA_CHAT_MODEL", "OLLAMA_JUDGE_MODEL", "OLLAMA_MEMORY_MODEL",
		"OLLAMA_RESOLVER_MODEL", "RAG_CANDIDATE_K", "RAG_FINAL_K", "RAG_QUOTE_MIN_RUNES",
		"RAG_QUOTE_MAX_RUNES", "OLLAMA_MAX_TOKENS", "OLLAMA_JUDGE_MAX_TOKENS",
		"OLLAMA_MEMORY_MAX_TOKENS", "OLLAMA_RESOLVER_MAX_TOKENS", "RAG_CHUNK_MIN_SIMILARITY",
		"RAG_ANSWER_MIN_RELEVANCE", "RAG_RERANK_ALPHA", "RAG_RERANK_BETA", "RAG_RERANK_GAMMA",
		"OLLAMA_TEMPERATURE", "OLLAMA_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
	config := ConfigFromEnv()
	if config.IndexPath != defaultIndex || config.StoreDir != defaultStoreDir {
		t.Fatalf("unexpected path defaults: %+v", config)
	}
	if config.EmbedEndpoint != embedding.DefaultEndpoint || config.EmbedModel != embedding.DefaultModel {
		t.Fatalf("unexpected embedding defaults: %+v", config)
	}
	if config.ChatEndpoint != generation.DefaultEndpoint || config.ChatModel != generation.DefaultModel {
		t.Fatalf("unexpected generation defaults: %+v", config)
	}
	if config.Timeout != defaultTimeout || config.CandidateK != defaultCandidateK || config.FinalK != defaultFinalK {
		t.Fatalf("unexpected runtime defaults: %+v", config)
	}

	t.Setenv("RAG_INDEX", "custom-index.json")
	t.Setenv("RAG_CANDIDATE_K", "31")
	t.Setenv("RAG_RERANK_ALPHA", "0.6")
	t.Setenv("OLLAMA_TIMEOUT", "45s")
	config = ConfigFromEnv()
	if config.IndexPath != "custom-index.json" || config.CandidateK != 31 || config.Alpha != 0.6 || config.Timeout != 45*time.Second {
		t.Fatalf("environment overrides were not applied: %+v", config)
	}
}

func TestBindFlagsOverridesConfig(t *testing.T) {
	config := ConfigFromEnv()
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	config.BindFlags(f)
	if err := f.Parse([]string{"--index", "index.json", "--store-dir", "sessions", "--chat-model", "answer", "--candidate-k", "12", "--timeout", "30s"}); err != nil {
		t.Fatal(err)
	}
	if config.IndexPath != "index.json" || config.StoreDir != "sessions" || config.ChatModel != "answer" || config.CandidateK != 12 || config.Timeout != 30*time.Second {
		t.Fatalf("flag overrides were not applied: %+v", config)
	}
}

func TestParseMode(t *testing.T) {
	mode, err := ParseMode(" TASK-MEMORY ")
	if err != nil || mode != chatservice.TaskMemory {
		t.Fatalf("ParseMode returned %q, %v", mode, err)
	}
	if _, err := ParseMode("unknown"); err == nil {
		t.Fatal("ParseMode accepted an unsupported mode")
	}
}

func TestBuildRejectsInvalidLimitsBeforeLoadingIndex(t *testing.T) {
	config := ConfigFromEnv()
	config.Timeout = 0
	if _, err := config.Build(); err == nil {
		t.Fatal("Build accepted a non-positive timeout")
	}
}
