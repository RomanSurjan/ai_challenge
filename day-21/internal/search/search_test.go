package search

import (
	"math"
	"testing"

	"ai-challenge/day-21/internal/chunk"
	"ai-challenge/day-21/internal/indexstore"
)

func TestCosineSimilarity(t *testing.T) {
	got, err := Cosine([]float64{1, 0}, []float64{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1/math.Sqrt2) > 1e-12 {
		t.Fatalf("cosine = %f", got)
	}
}

func TestVectorSortsDeterministically(t *testing.T) {
	index := indexstore.Index{EmbeddingDimension: 2, Chunks: []chunk.Chunk{
		{ChunkID: "b", Source: "b", Section: "s", Text: "b", Embedding: []float64{1, 0}},
		{ChunkID: "a", Source: "a", Section: "s", Text: "a", Embedding: []float64{1, 0}},
		{ChunkID: "c", Source: "c", Section: "s", Text: "c", Embedding: []float64{0, 1}},
	}}
	results, err := Vector(index, []float64{1, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].ChunkID != "a" || results[1].ChunkID != "b" || results[2].ChunkID != "c" {
		t.Fatalf("order = %#v", results)
	}
}
