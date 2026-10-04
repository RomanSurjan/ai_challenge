package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultEndpoint  = "http://127.0.0.1:11434/api/embed"
	DefaultModel     = "qwen3-embedding:0.6b"
	DefaultBatchSize = 16
)

type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float64, error)
}

type Ollama struct {
	Endpoint string
	Client   *http.Client
}

func NewOllama(endpoint string, timeout time.Duration) *Ollama {
	return &Ollama{Endpoint: endpoint, Client: &http.Client{Timeout: timeout}}
}

func (o *Ollama) Embed(ctx context.Context, model string, texts []string) ([][]float64, error) {
	if o == nil || o.Client == nil || strings.TrimSpace(o.Endpoint) == "" {
		return nil, fmt.Errorf("Ollama embedding client is not configured")
	}
	if strings.TrimSpace(model) == "" || len(texts) == 0 {
		return nil, fmt.Errorf("embedding model and at least one input are required")
	}
	payload, err := json.Marshal(struct {
		Model    string   `json:"model"`
		Input    []string `json:"input"`
		Truncate bool     `json:"truncate"`
	}{Model: model, Input: texts, Truncate: false})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call Ollama embedding endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	var decoded struct {
		Embeddings [][]float64 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode embedding response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || decoded.Error != "" {
		message := strings.TrimSpace(decoded.Error)
		if message == "" {
			message = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("Ollama embedding HTTP %d: %s", resp.StatusCode, message)
	}
	if len(decoded.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding count is %d, want %d", len(decoded.Embeddings), len(texts))
	}
	for i := range decoded.Embeddings {
		if err := Normalize(decoded.Embeddings[i]); err != nil {
			return nil, fmt.Errorf("embedding %d: %w", i, err)
		}
	}
	return decoded.Embeddings, nil
}

func Normalize(vector []float64) error {
	if len(vector) == 0 {
		return fmt.Errorf("empty vector")
	}
	var sum float64
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("non-finite vector value")
		}
		sum += value * value
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return fmt.Errorf("zero vector")
	}
	if math.Abs(norm-1) > 1e-6 {
		for i := range vector {
			vector[i] /= norm
		}
	}
	return nil
}
