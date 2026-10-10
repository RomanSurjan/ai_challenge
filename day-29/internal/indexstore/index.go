package indexstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
)

const (
	SchemaVersion              = "1"
	ExpectedCorpusID           = "b7d4f83675e1fc7c8d1c112bee933610e15ecd5aa5ade9e6d30a7bbc070cc451"
	ExpectedEmbeddingModel     = "qwen3-embedding:0.6b"
	ExpectedEmbeddingDimension = 1024
)

type Chunking struct {
	TargetWords  int `json:"target_words"`
	OverlapWords int `json:"overlap_words"`
}

type Stats struct {
	Documents         int     `json:"documents"`
	Chunks            int     `json:"chunks"`
	MinChunkWords     int     `json:"min_chunk_words"`
	AverageChunkWords float64 `json:"average_chunk_words"`
	MaxChunkWords     int     `json:"max_chunk_words"`
	BuildDurationMS   int64   `json:"build_duration_ms"`
}

type Chunk struct {
	ChunkID   string    `json:"chunk_id"`
	Source    string    `json:"source"`
	Title     string    `json:"title"`
	Section   string    `json:"section"`
	Strategy  string    `json:"strategy"`
	Ordinal   int       `json:"ordinal"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	WordCount int       `json:"word_count"`
	Text      string    `json:"text"`
	Embedding []float64 `json:"embedding"`
}

type Index struct {
	SchemaVersion      string    `json:"schema_version"`
	Strategy           string    `json:"strategy"`
	Chunking           Chunking  `json:"chunking"`
	Model              string    `json:"model"`
	EmbeddingDimension int       `json:"embedding_dimension"`
	GeneratedAt        time.Time `json:"generated_at"`
	CorpusID           string    `json:"corpus_id"`
	Stats              Stats     `json:"stats"`
	Chunks             []Chunk   `json:"chunks"`
}

func Load(path string) (Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return Index{}, fmt.Errorf("open Day 21 index: %w", err)
	}
	defer f.Close()

	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var index Index
	if err := decoder.Decode(&index); err != nil {
		return Index{}, fmt.Errorf("decode Day 21 index: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("unexpected trailing JSON value")
		}
		return Index{}, fmt.Errorf("decode Day 21 index: %w", err)
	}
	if err := Validate(index); err != nil {
		return Index{}, fmt.Errorf("validate Day 21 index: %w", err)
	}
	return index, nil
}

func Validate(index Index) error {
	if index.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q, want %q", index.SchemaVersion, SchemaVersion)
	}
	if index.Strategy != "fixed" && index.Strategy != "structural" {
		return fmt.Errorf("unsupported strategy %q", index.Strategy)
	}
	if index.Chunking.TargetWords <= 0 || index.Chunking.OverlapWords < 0 || index.Chunking.OverlapWords >= index.Chunking.TargetWords {
		return fmt.Errorf("invalid chunking parameters: target=%d overlap=%d", index.Chunking.TargetWords, index.Chunking.OverlapWords)
	}
	if index.CorpusID != ExpectedCorpusID {
		return fmt.Errorf("incompatible corpus_id %q, want %q", index.CorpusID, ExpectedCorpusID)
	}
	if index.Model != ExpectedEmbeddingModel {
		return fmt.Errorf("incompatible embedding model %q, want %q", index.Model, ExpectedEmbeddingModel)
	}
	if index.EmbeddingDimension != ExpectedEmbeddingDimension {
		return fmt.Errorf("incompatible embedding dimension %d, want %d", index.EmbeddingDimension, ExpectedEmbeddingDimension)
	}
	if len(index.Chunks) == 0 {
		return fmt.Errorf("index contains no chunks")
	}
	ids := make(map[string]struct{}, len(index.Chunks))
	for i, chunk := range index.Chunks {
		if strings.TrimSpace(chunk.ChunkID) == "" || strings.TrimSpace(chunk.Source) == "" || strings.TrimSpace(chunk.Section) == "" || strings.TrimSpace(chunk.Text) == "" {
			return fmt.Errorf("chunk %d has missing required metadata", i)
		}
		if chunk.Strategy != index.Strategy {
			return fmt.Errorf("chunk %s strategy %q does not match index strategy %q", chunk.ChunkID, chunk.Strategy, index.Strategy)
		}
		if _, exists := ids[chunk.ChunkID]; exists {
			return fmt.Errorf("duplicate chunk ID %s", chunk.ChunkID)
		}
		ids[chunk.ChunkID] = struct{}{}
		if len(chunk.Embedding) != index.EmbeddingDimension {
			return fmt.Errorf("chunk %s embedding dimension is %d, want %d", chunk.ChunkID, len(chunk.Embedding), index.EmbeddingDimension)
		}
		for _, value := range chunk.Embedding {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("chunk %s embedding contains a non-finite value", chunk.ChunkID)
			}
		}
	}
	return nil
}
