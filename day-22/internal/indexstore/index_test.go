package indexstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validIndex() Index {
	vector := make([]float64, ExpectedEmbeddingDimension)
	vector[0] = 1
	return Index{
		SchemaVersion: SchemaVersion, Strategy: "structural",
		Chunking: Chunking{TargetWords: 700, OverlapWords: 100},
		Model:    ExpectedEmbeddingModel, EmbeddingDimension: ExpectedEmbeddingDimension,
		GeneratedAt: time.Now().UTC(), CorpusID: ExpectedCorpusID,
		Stats: Stats{Documents: 1, Chunks: 1, MinChunkWords: 1, AverageChunkWords: 1, MaxChunkWords: 1},
		Chunks: []Chunk{{
			ChunkID: "structural-test", Source: "day-18/README.md", Title: "README.md", Section: "Architecture",
			Strategy: "structural", Ordinal: 0, Start: 0, End: 4, WordCount: 1, Text: "text", Embedding: vector,
		}},
	}
}

func TestLoadCompatibleDay21Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	data, err := json.Marshal(validIndex())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CorpusID != ExpectedCorpusID || len(loaded.Chunks) != 1 {
		t.Fatalf("unexpected loaded index: %+v", loaded)
	}
}

func TestRejectsIncompatibleSchemaModelAndDimension(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Index)
		want   string
	}{
		{"schema", func(index *Index) { index.SchemaVersion = "2" }, "schema_version"},
		{"model", func(index *Index) { index.Model = "other" }, "embedding model"},
		{"dimension", func(index *Index) { index.EmbeddingDimension = 3 }, "embedding dimension"},
		{"corpus", func(index *Index) { index.CorpusID = "other" }, "corpus_id"},
		{"duplicate", func(index *Index) { index.Chunks = append(index.Chunks, index.Chunks[0]) }, "duplicate chunk ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := validIndex()
			test.mutate(&index)
			if err := Validate(index); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}
