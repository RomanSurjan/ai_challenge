package retrieval

import (
	"fmt"
	"math"
	"sort"

	"ai-challenge/day-22/internal/indexstore"
)

type Result struct {
	Rank    int     `json:"rank"`
	Score   float64 `json:"score"`
	ChunkID string  `json:"chunk_id"`
	Source  string  `json:"source"`
	Section string  `json:"section"`
	Text    string  `json:"text"`
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

func Search(index indexstore.Index, query []float64, topK int) ([]Result, error) {
	if topK <= 0 {
		return nil, fmt.Errorf("top-k must be positive")
	}
	if len(query) != index.EmbeddingDimension {
		return nil, fmt.Errorf("query embedding dimension is %d, index dimension is %d", len(query), index.EmbeddingDimension)
	}
	type scored struct {
		chunk indexstore.Chunk
		score float64
	}
	all := make([]scored, len(index.Chunks))
	for i, chunk := range index.Chunks {
		score, err := Cosine(query, chunk.Embedding)
		if err != nil {
			return nil, fmt.Errorf("score chunk %s: %w", chunk.ChunkID, err)
		}
		all[i] = scored{chunk: chunk, score: score}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score == all[j].score {
			return all[i].chunk.ChunkID < all[j].chunk.ChunkID
		}
		return all[i].score > all[j].score
	})
	topK = min(topK, len(all))
	results := make([]Result, topK)
	for i := range results {
		results[i] = Result{
			Rank: i + 1, Score: all[i].score, ChunkID: all[i].chunk.ChunkID,
			Source: all[i].chunk.Source, Section: all[i].chunk.Section, Text: all[i].chunk.Text,
		}
	}
	return results, nil
}
