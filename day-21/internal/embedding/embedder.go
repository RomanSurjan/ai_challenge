package embedding

import (
	"context"
	"fmt"
	"math"
)

type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float64, error)
}

func EmbedAll(ctx context.Context, embedder Embedder, model string, texts []string, batchSize int) ([][]float64, int, error) {
	if model == "" {
		return nil, 0, fmt.Errorf("embedding model is required")
	}
	if batchSize <= 0 {
		return nil, 0, fmt.Errorf("batch size must be positive")
	}
	if len(texts) == 0 {
		return nil, 0, fmt.Errorf("at least one text is required")
	}
	all := make([][]float64, 0, len(texts))
	dimension := 0
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		vectors, err := embedder.Embed(ctx, model, texts[start:end])
		if err != nil {
			return nil, 0, fmt.Errorf("embed batch %d-%d: %w", start, end-1, err)
		}
		if len(vectors) != end-start {
			return nil, 0, fmt.Errorf("embedding count mismatch for batch %d-%d: sent %d texts, received %d vectors", start, end-1, end-start, len(vectors))
		}
		for i := range vectors {
			if dimension == 0 {
				dimension = len(vectors[i])
				if dimension == 0 {
					return nil, 0, fmt.Errorf("embedding vector is empty")
				}
			}
			if len(vectors[i]) != dimension {
				return nil, 0, fmt.Errorf("embedding dimension mismatch at input %d: got %d, want %d", start+i, len(vectors[i]), dimension)
			}
			if err := Normalize(vectors[i]); err != nil {
				return nil, 0, fmt.Errorf("normalize embedding %d: %w", start+i, err)
			}
		}
		all = append(all, vectors...)
	}
	return all, dimension, nil
}

// Normalize verifies finite values and safely L2-normalizes a vector. Ollama
// normally returns unit vectors, so vectors within tolerance are left intact.
func Normalize(vector []float64) error {
	if len(vector) == 0 {
		return fmt.Errorf("empty vector")
	}
	var sum float64
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("vector contains a non-finite value")
		}
		sum += value * value
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return fmt.Errorf("zero-length vector")
	}
	if math.Abs(norm-1) <= 1e-6 {
		return nil
	}
	for i := range vector {
		vector[i] /= norm
	}
	return nil
}
