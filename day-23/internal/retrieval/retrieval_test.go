package retrieval

import (
	"math"
	"testing"

	"ai-challenge/day-23/internal/indexstore"
)

func TestCosineSimilarity(t *testing.T) {
	got, err := Cosine([]float64{1, 0}, []float64{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1/math.Sqrt2) > 1e-12 {
		t.Fatalf("Cosine() = %v", got)
	}
	if _, err := Cosine([]float64{0, 0}, []float64{1, 0}); err == nil {
		t.Fatal("zero vector was accepted")
	}
}

func TestSearchTopKAndStableTieBreak(t *testing.T) {
	index := indexstore.Index{EmbeddingDimension: 2, Chunks: []indexstore.Chunk{
		{ChunkID: "z", Source: "z.md", Section: "Z", Text: "z", Embedding: []float64{1, 0}},
		{ChunkID: "a", Source: "a.md", Section: "A", Text: "a", Embedding: []float64{1, 0}},
		{ChunkID: "b", Source: "b.md", Section: "B", Text: "b", Embedding: []float64{0, 1}},
	}}
	results, err := Search(index, []float64{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].ChunkID != "a" || results[1].ChunkID != "z" {
		t.Fatalf("unexpected stable top-k: %+v", results)
	}
	if results[0].Rank != 1 || results[1].Rank != 2 {
		t.Fatalf("unexpected ranks: %+v", results)
	}
}
