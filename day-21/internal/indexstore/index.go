package indexstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ai-challenge/day-21/internal/chunk"
	"ai-challenge/day-21/internal/document"
	"ai-challenge/day-21/internal/embedding"
)

const SchemaVersion = "1"

type Stats struct {
	Documents         int     `json:"documents"`
	Chunks            int     `json:"chunks"`
	MinChunkWords     int     `json:"min_chunk_words"`
	AverageChunkWords float64 `json:"average_chunk_words"`
	MaxChunkWords     int     `json:"max_chunk_words"`
	BuildDurationMS   int64   `json:"build_duration_ms"`
}

type Index struct {
	SchemaVersion      string         `json:"schema_version"`
	Strategy           chunk.Strategy `json:"strategy"`
	Chunking           chunk.Config   `json:"chunking"`
	Model              string         `json:"model"`
	EmbeddingDimension int            `json:"embedding_dimension"`
	GeneratedAt        time.Time      `json:"generated_at"`
	CorpusID           string         `json:"corpus_id"`
	Stats              Stats          `json:"stats"`
	Chunks             []chunk.Chunk  `json:"chunks"`
}

func Build(ctx context.Context, docs []document.Document, strategy chunk.Strategy, config chunk.Config, model string, batchSize int, embedder embedding.Embedder) (Index, error) {
	started := time.Now()
	chunks, err := chunk.Build(docs, strategy, config)
	if err != nil {
		return Index{}, err
	}
	if len(chunks) == 0 {
		return Index{}, fmt.Errorf("chunking produced no chunks")
	}
	texts := make([]string, len(chunks))
	for i := range chunks {
		texts[i] = chunks[i].Text
	}
	vectors, dimension, err := embedding.EmbedAll(ctx, embedder, model, texts, batchSize)
	if err != nil {
		return Index{}, err
	}
	for i := range chunks {
		chunks[i].Embedding = vectors[i]
	}
	index := Index{
		SchemaVersion: SchemaVersion, Strategy: strategy, Chunking: config,
		Model: model, EmbeddingDimension: dimension, GeneratedAt: time.Now().UTC(),
		CorpusID: document.CorpusID(docs), Chunks: chunks,
	}
	index.Stats = makeStats(len(docs), chunks, time.Since(started))
	if err := Validate(index); err != nil {
		return Index{}, err
	}
	return index, nil
}

func makeStats(documents int, chunks []chunk.Chunk, elapsed time.Duration) Stats {
	stats := Stats{Documents: documents, Chunks: len(chunks), BuildDurationMS: elapsed.Milliseconds()}
	if len(chunks) == 0 {
		return stats
	}
	stats.MinChunkWords = chunks[0].WordCount
	var total int
	for _, item := range chunks {
		stats.MinChunkWords = min(stats.MinChunkWords, item.WordCount)
		stats.MaxChunkWords = max(stats.MaxChunkWords, item.WordCount)
		total += item.WordCount
	}
	stats.AverageChunkWords = float64(total) / float64(len(chunks))
	return stats
}

func Validate(index Index) error {
	if index.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported index schema %q", index.SchemaVersion)
	}
	if index.Strategy != chunk.Fixed && index.Strategy != chunk.Structural {
		return fmt.Errorf("invalid index strategy %q", index.Strategy)
	}
	if err := index.Chunking.Validate(); err != nil {
		return fmt.Errorf("invalid chunking parameters: %w", err)
	}
	if index.Model == "" || index.CorpusID == "" {
		return fmt.Errorf("index model and corpus ID are required")
	}
	if index.EmbeddingDimension <= 0 {
		return fmt.Errorf("embedding dimension must be positive")
	}
	if len(index.Chunks) == 0 {
		return fmt.Errorf("index contains no chunks")
	}
	if err := chunk.Validate(index.Chunks); err != nil {
		return err
	}
	for _, item := range index.Chunks {
		if item.Strategy != index.Strategy {
			return fmt.Errorf("chunk %s strategy %q does not match index strategy %q", item.ChunkID, item.Strategy, index.Strategy)
		}
		if len(item.Embedding) != index.EmbeddingDimension {
			return fmt.Errorf("chunk %s embedding dimension is %d, want %d", item.ChunkID, len(item.Embedding), index.EmbeddingDimension)
		}
	}
	return nil
}

func Save(path string, index Index) error {
	if err := Validate(index); err != nil {
		return fmt.Errorf("validate index before save: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".index-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary index: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("set index permissions: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(index); err != nil {
		return fmt.Errorf("encode index: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close index: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace index atomically: %w", err)
	}
	ok = true
	return nil
}

func Load(path string) (Index, error) {
	file, err := os.Open(path)
	if err != nil {
		return Index{}, fmt.Errorf("open index: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var index Index
	if err := decoder.Decode(&index); err != nil {
		return Index{}, fmt.Errorf("decode index: %w", err)
	}
	if err := Validate(index); err != nil {
		return Index{}, fmt.Errorf("validate index: %w", err)
	}
	return index, nil
}
