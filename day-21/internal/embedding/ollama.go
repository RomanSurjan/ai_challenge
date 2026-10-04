package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultEndpoint  = "http://127.0.0.1:11434/api/embed"
	DefaultModel     = "qwen3-embedding:0.6b"
	DefaultTimeout   = 2 * time.Minute
	DefaultBatchSize = 16
)

type Ollama struct {
	Endpoint string
	Client   *http.Client
}

type ollamaRequest struct {
	Model    string   `json:"model"`
	Input    []string `json:"input"`
	Truncate bool     `json:"truncate"`
}

type ollamaResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
	Error      string      `json:"error"`
}

func NewOllama(endpoint string, timeout time.Duration) *Ollama {
	return &Ollama{
		Endpoint: endpoint,
		Client:   &http.Client{Timeout: timeout},
	}
}

func (o *Ollama) Embed(ctx context.Context, model string, texts []string) ([][]float64, error) {
	if o == nil || o.Client == nil {
		return nil, fmt.Errorf("Ollama client is not configured")
	}
	if strings.TrimSpace(o.Endpoint) == "" {
		return nil, fmt.Errorf("Ollama endpoint is empty")
	}
	payload, err := json.Marshal(ollamaRequest{Model: model, Input: texts, Truncate: false})
	if err != nil {
		return nil, fmt.Errorf("encode Ollama request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create Ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call Ollama at %s: %w (is Ollama running and is model %q installed?)", o.Endpoint, err, model)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read Ollama response: %w", err)
	}
	var decoded ollamaResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode Ollama response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(decoded.Error)
		if message == "" {
			message = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("Ollama returned HTTP %d: %s (ensure model %q is installed)", resp.StatusCode, message, model)
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("Ollama error: %s", decoded.Error)
	}
	return decoded.Embeddings, nil
}
