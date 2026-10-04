package search

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"ai-challenge/day-21/internal/chunk"
	"ai-challenge/day-21/internal/indexstore"
)

type Result struct {
	Rank    int     `json:"rank"`
	Score   float64 `json:"score"`
	ChunkID string  `json:"chunk_id"`
	Source  string  `json:"source"`
	Section string  `json:"section"`
	Snippet string  `json:"snippet"`
}

func Cosine(a, b []float64) (float64, error) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, fmt.Errorf("vectors must have the same positive dimension (got %d and %d)", len(a), len(b))
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0, fmt.Errorf("cosine similarity is undefined for a zero vector")
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}

func Vector(index indexstore.Index, query []float64, topK int) ([]Result, error) {
	if topK <= 0 {
		return nil, fmt.Errorf("top-k must be positive")
	}
	if len(query) != index.EmbeddingDimension {
		return nil, fmt.Errorf("query dimension is %d, index dimension is %d", len(query), index.EmbeddingDimension)
	}
	type scored struct {
		chunk chunk.Chunk
		score float64
	}
	scores := make([]scored, len(index.Chunks))
	for i, item := range index.Chunks {
		score, err := Cosine(query, item.Embedding)
		if err != nil {
			return nil, fmt.Errorf("score chunk %s: %w", item.ChunkID, err)
		}
		scores[i] = scored{chunk: item, score: score}
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].score == scores[j].score {
			return scores[i].chunk.ChunkID < scores[j].chunk.ChunkID
		}
		return scores[i].score > scores[j].score
	})
	topK = min(topK, len(scores))
	results := make([]Result, topK)
	for i := 0; i < topK; i++ {
		item := scores[i]
		results[i] = Result{
			Rank: i + 1, Score: item.score, ChunkID: item.chunk.ChunkID,
			Source: item.chunk.Source, Section: item.chunk.Section,
			Snippet: snippet(item.chunk.Text, 220),
		}
	}
	return results, nil
}

func snippet(text string, limit int) string {
	compact := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(compact) <= limit {
		return compact
	}
	runes := []rune(compact)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
