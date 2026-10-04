package indexstore_test

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"ai-challenge/day-21/internal/chunk"
	"ai-challenge/day-21/internal/document"
	"ai-challenge/day-21/internal/indexstore"
	"ai-challenge/day-21/internal/search"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, _ string, texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i, text := range texts {
		if strings.Contains(text, "routing") {
			result[i] = []float64{1, 0, 0}
		} else {
			result[i] = []float64{0, 1, float64(len(text) % 3)}
		}
	}
	return result, nil
}

func TestJSONRoundTripAndEndToEndPipeline(t *testing.T) {
	docs := []document.Document{
		{Source: "a.md", Title: "a.md", Type: "markdown", Text: "# Routing\nrouting MCP calls to owners", SHA256: "a"},
		{Source: "b.md", Title: "b.md", Type: "markdown", Text: "# Storage\natomic JSON storage", SHA256: "b"},
	}
	index, err := indexstore.Build(context.Background(), docs, chunk.Structural, chunk.Config{TargetWords: 10, OverlapWords: 2}, "fake-model", 2, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := indexstore.Save(path, index); err != nil {
		t.Fatal(err)
	}
	loaded, err := indexstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Chunks) != len(index.Chunks) || loaded.EmbeddingDimension != 3 {
		t.Fatalf("round trip mismatch: %#v", loaded)
	}
	results, err := search.Vector(loaded, []float64{1, 0, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Source != "a.md" || math.Abs(results[0].Score-1) > 1e-12 {
		t.Fatalf("unexpected search result: %#v", results)
	}
}

func TestValidateRejectsEmbeddingDimensionMismatch(t *testing.T) {
	index := indexstore.Index{
		SchemaVersion: indexstore.SchemaVersion, Strategy: chunk.Fixed,
		Chunking: chunk.Config{TargetWords: 10, OverlapWords: 1},
		Model:    "model", EmbeddingDimension: 3, CorpusID: "corpus",
		Chunks: []chunk.Chunk{{
			ChunkID: "id", Source: "a", Title: "a", Section: "Document",
			Strategy: chunk.Fixed, Start: 0, End: 1, WordCount: 1, Text: "x",
			Embedding: []float64{1, 0},
		}},
	}
	if err := indexstore.Validate(index); err == nil {
		t.Fatal("expected dimension validation error")
	}
}
